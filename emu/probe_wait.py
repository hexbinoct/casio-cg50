#!/usr/bin/env python3
"""Characterize a wait/spin loop the OS gets stuck in. Loads the idle snapshot,
enables the timer + ETMU counter, runs until PC enters the target region, then:
 - reports SR (BL/IMASK -> why interrupts are/aren't accepted),
 - disassembles the loop,
 - logs the MMIO it polls and the distinct PCs of the loop.

Usage: python emu/probe_wait.py <target_hex> [run_ins] [capture]
"""
import os, sys, pickle, collections
try:
    sys.stdout.reconfigure(line_buffering=True)
except Exception:
    pass
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from cpu import CPU
from memory import Memory
from mmio import MMIOBus, ETMUCounter
try:
    import sh4dis
    def disasm(op, pc): return sh4dis.decode(op, pc)
except Exception:
    def disasm(op, pc): return f"0x{op:04x}"

HERE = os.path.dirname(__file__)
SNAP = os.path.join(HERE, "idle_state.pkl")


def upgrade(mmio, cpu):
    mmio.cpu = cpu
    if not any(getattr(r, "name", "") == "ETMU2" for r in mmio.regions):
        e = ETMUCounter("ETMU2", 0xA44D0000, 0x1000); e.bus = mmio
        mmio.regions.insert(0, e)


def main():
    target = int(sys.argv[1], 0)
    run_ins = int(sys.argv[2], 0) if len(sys.argv) > 2 else 4_000_000
    capture = int(sys.argv[3], 0) if len(sys.argv) > 3 else 200_000
    tmask = target & 0xFFFFF000

    cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
    upgrade(mmio, cpu)
    mmio.timer_period = 30_000
    mmio.timer_next = cpu.cycles

    mmacc = []          # (rw, va, val, pc)
    regc = collections.Counter()
    orig_r, orig_w = mmio.read, mmio.write
    capturing = [False]

    def rr(va, size):
        v = orig_r(va, size)
        if capturing[0]:
            r, _ = mmio._find(va); regc[(r.name if r else "???", va)] += 1
            mmacc.append(("rd", va, v, cpu.pc))
        return v
    def rw(va, size, val):
        if capturing[0]:
            r, _ = mmio._find(va); regc[(r.name if r else "???", va)] += 1
            mmacc.append(("wr", va, val, cpu.pc))
        return orig_w(va, size, val)
    mmio.read, mmio.write = rr, rw

    step = cpu.step
    pcs = collections.Counter()
    cap_end = None
    sr_at = None
    for i in range(run_ins):
        if cap_end is None and (cpu.pc & 0xFFFFF000) == tmask:
            capturing[0] = True
            cap_end = i + capture
            sr_at = cpu.sr
            print(f"entered target region 0x{tmask:08x} at +{cpu.cycles-14_500_000:,}; "
                  f"SR=0x{cpu.sr:08x} (BL={(cpu.sr>>28)&1} IMASK={(cpu.sr&0xF0)>>4})")
        if capturing[0]:
            pcs[cpu.pc] += 1
            if i >= cap_end:
                break
        mmio.tick(cpu)
        try:
            step()
        except Exception as e:
            print(f"FAULT @0x{cpu.pc:08x}: {type(e).__name__}: {e}"); break
    if cap_end is None:
        print(f"never reached 0x{tmask:08x} in {run_ins:,} instr (final PC=0x{cpu.pc:08x})")
        return

    lo, hi = min(pcs), max(pcs)
    print(f"\nloop: {len(pcs)} distinct PCs, band 0x{lo:08x}..0x{hi:08x}")
    print("hottest PCs:")
    for pc, c in pcs.most_common(10):
        print(f"  0x{pc:08x} x{c:<6} {mem.r16(pc):04x} {disasm(mem.r16(pc), pc)}")
    print(f"\ndisassembly 0x{lo-8:08x}..0x{hi+12:08x}:")
    for a in range((lo-8) & ~1, hi+12, 2):
        op = mem.r16(a)
        print(f"  0x{a:08x}: {op:04x}  {disasm(op,a)}{'  <--' if a in pcs else ''}")
    print(f"\nMMIO touched in loop ({len(mmacc)} accesses):")
    for (name, va), c in regc.most_common(20):
        print(f"  {name:12s} 0x{va:08x} x{c}")
    if not regc:
        print("  (NONE — pure RAM poll; waiting on a memory flag set by code we don't run)")


if __name__ == "__main__":
    main()
