#!/usr/bin/env python3
"""The 0x801e5a70 delay (fixed 100) sits inside an OUTER loop that spins ~74k times.
Find that outer loop's poll condition: run into the spin, then over a window record
 - taken BACKWARD branches (loop edges: target < pc),
 - MMIO reads, and DRAM/RAM addresses read many times (poll targets),
so we can see what the OS is waiting on.

Usage: python emu/trace_outer.py [start_ins] [window]
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
    start = int(sys.argv[1], 0) if len(sys.argv) > 1 else 4_000_000
    window = int(sys.argv[2], 0) if len(sys.argv) > 2 else 80_000

    cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
    upgrade(mmio, cpu)
    mmio.timer_period = 30_000
    mmio.timer_next = cpu.cycles

    # record reads (mmio + ram) only during the window
    capturing = [False]
    ramreads = collections.Counter()
    mmioreads = collections.Counter()
    orig_r = mmio.read
    def rr(va, size):
        v = orig_r(va, size)
        if capturing[0]:
            nm, _ = mmio._find(va)
            mmioreads[(nm.name if nm else "???", va)] += 1
        return v
    mmio.read = rr
    # wrap memory.read to catch RAM polls (DRAM/ilram) too
    orig_memr = mem.read
    def memrr(va, size):
        if capturing[0] and (0x8C000000 <= va < 0x8D000000 or 0xFD800000 <= va < 0xFD810000):
            ramreads[va] += 1
        return orig_memr(va, size)
    mem.read = memrr

    step = cpu.step
    backedges = collections.Counter()
    pcc = collections.Counter()
    prev = cpu.pc
    for i in range(start + window):
        if i == start:
            capturing[0] = True
            print(f"capturing at +{cpu.cycles-14_500_000:,} (SR=0x{cpu.sr:08x} "
                  f"BL={(cpu.sr>>28)&1} IMASK={(cpu.sr&0xF0)>>4} PC=0x{cpu.pc:08x})")
        if capturing[0]:
            pc = cpu.pc
            pcc[pc] += 1
            if pc < prev and (prev - pc) < 0x4000:     # backward branch (loop edge)
                backedges[(pc, prev)] += 1
            prev = pc
        mmio.tick(cpu)
        try:
            step()
        except Exception as e:
            print(f"FAULT @0x{cpu.pc:08x}: {type(e).__name__}: {e}"); break

    print(f"\ntop backward-branch loop edges (target <- from, count):")
    for (tgt, frm), c in backedges.most_common(10):
        print(f"  0x{tgt:08x} <- 0x{frm:08x}  x{c}   [{disasm(mem.r16(frm), frm)}]")
    print(f"\nMMIO reads in window:")
    for (nm, va), c in mmioreads.most_common(15):
        print(f"  {nm:10s} 0x{va:08x} x{c}")
    print(f"\nmost-polled RAM addresses (DRAM/on-chip):")
    for va, c in ramreads.most_common(15):
        print(f"  0x{va:08x} x{c}")
    # show the outer-loop span: lowest backedge target .. its source
    if backedges:
        (tgt, frm), _ = backedges.most_common(1)[0]
        print(f"\ndisasm of dominant loop 0x{tgt:08x}..0x{frm+2:08x}:")
        for a in range(tgt, frm + 4, 2):
            op = mem.r16(a)
            print(f"  0x{a:08x}: {op:04x}  {disasm(op,a)}{'  <--' if a in pcc else ''}")


if __name__ == "__main__":
    main()
