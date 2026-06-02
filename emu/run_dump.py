#!/usr/bin/env python3
"""Boot the PHYSICAL 3.60 flash dump (os/flash_dump/os.bin) in our emulator,
which was built/validated on the 3.80 updater image.

Goal: confirm the hardware emulation is OS-version-independent.
  Phase A (lockstep): run 3.60 and 3.80 from reset side-by-side, step for step,
    and report the first PC where they diverge. The boot/reset region [0..0x20000]
    is byte-identical between the two versions, so a correct hardware emulator MUST
    execute them identically until the boot stub hands off into version-specific
    cached-OS init. Divergence there = expected & correct; an EARLY divergence or a
    fault in the shared boot stub would mean an emulator bug.
  Phase B (solo): keep running 3.60 alone up to max_ins, reproducing the documented
    boot MMIO writes (CCR/MMUCR/CPG/WDT/PFC/BSC), and report how far it gets.

Usage:  python emu/run_dump.py [max_ins] [lockstep_cap]
"""
import os, sys
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))

from memory import Memory, MemFault
from mmio import MMIOBus
from cpu import CPU, IllegalInstruction
try:
    import sh4dis
    def disasm(op, pc): return sh4dis.decode(op, pc)
except Exception:
    def disasm(op, pc): return f"0x{op:04x}"

HERE   = os.path.dirname(__file__)
IMG_60 = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")                 # physical 3.60
IMG_80 = os.path.join(HERE, "..", "os", "os_image", "cg50_os_3.80.plain.bin")   # updater 3.80


def make_cpu(image, log=False):
    mmio = MMIOBus(log=log)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000
    return cpu, mem, mmio


def step(cpu, mem):
    """Step one instruction; return ('ok'|reason, pc_before)."""
    pc = cpu.pc
    try:
        cpu.step()
    except IllegalInstruction as e:
        return (f"ILLEGAL @0x{pc:08x}: {e}", pc)
    except MemFault as e:
        return (f"MEMFAULT @0x{pc:08x}: {e}", pc)
    except Exception as e:
        return (f"{type(e).__name__} @0x{pc:08x}: {e}", pc)
    return ("ok", pc)


def main():
    max_ins  = int(sys.argv[1], 0) if len(sys.argv) > 1 else 2_000_000
    ls_cap   = int(sys.argv[2], 0) if len(sys.argv) > 2 else 500_000

    img60 = open(IMG_60, "rb").read()
    img80 = open(IMG_80, "rb").read()
    print(f"3.60 physical os.bin : {len(img60):,} B")
    print(f"3.80 updater plain   : {len(img80):,} B")

    # ---------------- Phase A: lockstep ----------------
    print("\n=== Phase A: lockstep 3.60 vs 3.80 from reset ===")
    c60, m60, _ = make_cpu(img60)
    c80, m80, _ = make_cpu(img80)
    diverge_at = None
    stop = None
    i = 0
    while i < ls_cap:
        pc60, pc80 = c60.pc, c80.pc
        if pc60 != pc80:
            diverge_at = (i, pc60, pc80)
            break
        op = m60.r16(pc60)
        r60, _ = step(c60, m60)
        r80, _ = step(c80, m80)
        if r60 != "ok" or r80 != "ok":
            stop = (i, pc60, op, r60, r80)
            break
        i += 1
    if diverge_at:
        i, p60, p80 = diverge_at
        print(f"  identical for {i:,} instructions, then PC diverged:")
        print(f"    3.60 -> 0x{p60:08x}   ({disasm(m60.r16(p60), p60)})")
        print(f"    3.80 -> 0x{p80:08x}   ({disasm(m80.r16(p80), p80)})")
        print(f"  (expected: boot stub identical, then version-specific OS init)")
    elif stop:
        i, pc, op, r60, r80 = stop
        print(f"  FAULT in shared boot stub at instr {i:,}, pc=0x{pc:08x} op={op:04x}")
        print(f"    3.60: {r60}\n    3.80: {r80}")
        print("  *** this would indicate an emulator bug, not a version issue ***")
    else:
        print(f"  ran {i:,} instructions fully in lockstep with NO divergence (cap hit)")

    # ---------------- Phase B: 3.60 solo ----------------
    print(f"\n=== Phase B: 3.60 solo boot, up to {max_ins:,} instructions ===")
    cpu, mem, mmio = make_cpu(img60, log=True)
    # trace first handful so we can eyeball the reset/bring-up
    trace_n = 60
    reason = "reached max_ins"
    last = None
    for i in range(max_ins):
        pc = cpu.pc
        op = mem.r16(pc)
        if i < trace_n:
            print(f"{i:5d} {pc:08x}: {op:04x}  {disasm(op, pc)}")
        else:
            mmio.log = False
        r, _ = step(cpu, mem)
        if r != "ok":
            reason = r
            break
        if cpu.pc == pc and op not in (0x0009,):
            reason = f"PC stuck (wait/poll) at 0x{pc:08x}"
            break
        last = pc
    print(f"\n=== 3.60 stopped after {cpu.cycles:,} instructions: {reason} ===")
    print(f"PC=0x{cpu.pc:08x}  PR=0x{cpu.pr:08x}  SR=0x{cpu.sr:08x}  VBR=0x{cpu.vbr:08x}")
    print("regs:", " ".join(f"r{i}=0x{cpu.r[i]:08x}" for i in range(16)))
    if mmio.unknown:
        print("\nunmapped MMIO touched (3.60):")
        for va, c in sorted(mmio.unknown.items()):
            print(f"  0x{va:08x}  x{c}")


if __name__ == "__main__":
    main()
