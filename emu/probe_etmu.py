#!/usr/bin/env python3
"""Understand the new wait the OS enters after timer ticks start (~0x80374300,
polling 0xA44D00D8). Loads the idle snapshot, enables the timer, runs until the
OS is in that wait, then: (a) disassembles the loop, (b) logs every 0xA44Dxxxx
(ETMU?) access with the PC, so we can model the register the OS waits on.

Usage: python emu/probe_etmu.py [run_ins] [capture]
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
from mmio import MMIOBus
try:
    import sh4dis
    def disasm(op, pc): return sh4dis.decode(op, pc)
except Exception:
    def disasm(op, pc): return f"0x{op:04x}"

HERE = os.path.dirname(__file__)
SNAP = os.path.join(HERE, "idle_state.pkl")


def main():
    run_ins = int(sys.argv[1], 0) if len(sys.argv) > 1 else 1_500_000
    capture = int(sys.argv[2], 0) if len(sys.argv) > 2 else 400_000

    cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
    mmio.timer_period = 100_000
    mmio.timer_next = cpu.cycles

    # record 0xA44Dxxxx accesses with PC + a Counter of all unmapped/region hits
    a44d = []       # (rw, va, val, pc)
    allreg = collections.Counter()
    orig_r, orig_w = mmio.read, mmio.write

    def rr(va, size):
        v = orig_r(va, size)
        if 0xA44D0000 <= va < 0xA44E0000:
            a44d.append(("rd", va, v, cpu.pc))
        r, _ = mmio._find(va); allreg[(r.name if r else "???")] += 1
        return v

    def rw(va, size, val):
        if 0xA44D0000 <= va < 0xA44E0000:
            a44d.append(("wr", va, val, cpu.pc))
        r, _ = mmio._find(va); allreg[(r.name if r else "???")] += 1
        return orig_w(va, size, val)

    mmio.read, mmio.write = rr, rw

    step = cpu.step
    pcs = collections.Counter()
    capturing = False
    cap_end = 0
    for i in range(run_ins):
        if not capturing and 0x80374000 <= cpu.pc < 0x80375000:
            capturing = True
            cap_end = i + capture
            print(f"entered 0x8037xxxx wait at instr +{cpu.cycles-14_500_000:,}; capturing {capture:,}")
        if capturing:
            pcs[cpu.pc] += 1
            if i >= cap_end:
                break
        try:
            step()
        except Exception as e:
            print(f"FAULT @0x{cpu.pc:08x}: {type(e).__name__}: {e}")
            break

    print(f"\n=== loop PCs (hottest) ===")
    for pc, c in pcs.most_common(12):
        op = mem.r16(pc)
        print(f"  0x{pc:08x} x{c:<7} {op:04x}  {disasm(op, pc)}")

    print(f"\n=== disassembly around the wait (0x803742c0..0x80374380) ===")
    for a in range(0x803742c0, 0x80374380, 2):
        op = mem.r16(a)
        mark = " <--" if a in pcs else ""
        print(f"  0x{a:08x}: {op:04x}  {disasm(op, a)}{mark}")

    print(f"\n=== 0xA44Dxxxx accesses (first 30) ===")
    for rw_, va, val, pc in a44d[:30]:
        print(f"  {rw_} 0x{va:08x} = 0x{val:08x}   (pc 0x{pc:08x})")
    print(f"  ... {len(a44d)} total A44D accesses")
    offs = collections.Counter((va & 0xFFFF) for _, va, _, _ in a44d)
    print("  A44D offsets touched:", {hex(o): c for o, c in offs.most_common()})

    print(f"\n=== all MMIO regions touched during capture ===")
    for name, c in allreg.most_common():
        print(f"  {name:12s} x{c}")


if __name__ == "__main__":
    main()
