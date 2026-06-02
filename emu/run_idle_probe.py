#!/usr/bin/env python3
"""Boot flash_full (3.60, real fls0) to the idle state (~15M instr), then:
  1. report CPU state at idle (SR -> is BL clear & IMASK low enough to take IRQs?
     VBR, PC) and whether the relocated IRQ handler table looks set up.
  2. capture EXACTLY which MMIO registers the idle loop polls (the event/flag it
     waits on) -> tells us which timer/INTC source to model for interrupt delivery.
  3. record the idle-loop PC band so we can name it in Ghidra (3.60).

Usage: python emu/run_idle_probe.py [capture_at] [capture_window]
"""
import os, sys, collections
try:
    sys.stdout.reconfigure(line_buffering=True)
except Exception:
    pass
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from memory import Memory
from mmio import MMIOBus
from cpu import CPU

HERE = os.path.dirname(__file__)
FULLBIN = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")

BL = 0x10000000
IMASK = 0x000000F0


def decode_sr(sr):
    return (f"SR=0x{sr:08x}  MD={ (sr>>30)&1 } RB={ (sr>>29)&1 } "
            f"BL={ (sr>>28)&1 } IMASK={ (sr&IMASK)>>4 }  "
            f"-> {'ACCEPTS IRQ' if not (sr&BL) else 'BLOCKED(BL=1)'} "
            f"(level must exceed {(sr&IMASK)>>4})")


def main():
    capture_at = int(sys.argv[1], 0) if len(sys.argv) > 1 else 15_000_000
    capture    = int(sys.argv[2], 0) if len(sys.argv) > 2 else 60_000

    image = open(FULLBIN, "rb").read()
    mmio = MMIOBus(log=False)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000
    print(f"booting flash_full.bin ({len(image):,} B) to idle @ {capture_at:,} instr...")

    step = cpu.step
    # phase 1: run silently to the idle state
    for i in range(capture_at):
        try:
            step()
        except Exception as e:
            print(f"  FAULT @0x{cpu.pc:08x} after {cpu.cycles:,}: {type(e).__name__}: {e}")
            return
    print(f"\n=== at idle (instr {cpu.cycles:,}) ===")
    print("  " + decode_sr(cpu.sr))
    print(f"  PC=0x{cpu.pc:08x}  VBR=0x{cpu.vbr:08x}  PR=0x{cpu.pr:08x}")
    # peek the relocated interrupt vector dispatcher (3.80 was VBR+0x600 -> dispatcher
    # that read *(0xFD8010C8 + ...)). Show a few words around VBR+0x600.
    try:
        disp = mem.r32(cpu.vbr + 0x600) if cpu.vbr else 0
        print(f"  *(VBR+0x600)=0x{disp:08x} (first word of interrupt vector)")
    except Exception:
        pass

    # phase 2: capture MMIO the idle loop touches
    rd = collections.Counter()
    wr = collections.Counter()
    pcset = collections.Counter()
    orig_read = mmio.read
    orig_write = mmio.write

    def rec_read(va, size):
        r, hit = mmio._find(va)
        name = r.name if r else "???"
        rd[(name, va)] += 1
        return orig_read(va, size)

    def rec_write(va, size, val):
        r, hit = mmio._find(va)
        name = r.name if r else "???"
        wr[(name, va)] += 1
        return orig_write(va, size, val)

    mmio.read = rec_read
    mmio.write = rec_write
    print(f"\n=== capturing idle-loop MMIO + PCs for {capture:,} instr ===")
    for i in range(capture):
        pcset[cpu.pc] += 1
        try:
            step()
        except Exception as e:
            print(f"  FAULT during capture @0x{cpu.pc:08x}: {type(e).__name__}: {e}")
            break

    lo, hi = min(pcset), max(pcset)
    print(f"\n  idle loop: {len(pcset)} distinct PCs, band 0x{lo:08x}..0x{hi:08x}")
    print("  hottest PCs:")
    for pc, c in pcset.most_common(8):
        print(f"    0x{pc:08x}  x{c}")
    print("\n  MMIO READS by the idle loop (register <- count):")
    for (name, va), c in rd.most_common(20):
        print(f"    {name:12s} 0x{va:08x}  x{c}")
    print("\n  MMIO WRITES by the idle loop:")
    for (name, va), c in wr.most_common(20):
        print(f"    {name:12s} 0x{va:08x}  x{c}")
    if not rd and not wr:
        print("    (none — idle loop is pure CPU/RAM polling, waiting on a memory flag"
              " that only an interrupt handler would change)")


if __name__ == "__main__":
    main()
