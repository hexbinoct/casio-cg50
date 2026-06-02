#!/usr/bin/env python3
"""Find why the 0x801e5a70 `dt r2; bf` busy-delay sometimes gets a HUGE r2 (near-
infinite delay). Runs from the idle snapshot with the timer + ETMU, watches every
entry to the loop, and when r2 exceeds a threshold dumps:
  - the value of r2,
  - a backtrace of the last ~140 instructions (disasm) that computed it,
  - all MMIO reads in that window (the timing/clock input we likely mis-model).
Also prints the static disasm of the code just before the loop.

Usage: python emu/probe_delay.py [threshold] [run_ins]
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
LOOP = 0x801e5a70


def upgrade(mmio, cpu):
    mmio.cpu = cpu
    if not any(getattr(r, "name", "") == "ETMU2" for r in mmio.regions):
        e = ETMUCounter("ETMU2", 0xA44D0000, 0x1000); e.bus = mmio
        mmio.regions.insert(0, e)


def main():
    threshold = int(sys.argv[1], 0) if len(sys.argv) > 1 else 200_000
    run_ins   = int(sys.argv[2], 0) if len(sys.argv) > 2 else 30_000_000

    cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
    upgrade(mmio, cpu)
    mmio.timer_period = 30_000
    mmio.timer_next = cpu.cycles

    recent = collections.deque(maxlen=140)     # (pc, op)
    mmior  = collections.deque(maxlen=60)       # (pc, va, val)
    orig_r = mmio.read
    def rr(va, size):
        v = orig_r(va, size)
        mmior.append((cpu.pc, va, v))
        return v
    mmio.read = rr

    step = cpu.step
    seen_r2 = collections.Counter()
    entries = 0
    for i in range(run_ins):
        pc = cpu.pc
        recent.append((pc, mem.r16(pc)))
        if pc == LOOP:
            entries += 1
            r2 = cpu.r[2]
            seen_r2[r2 >> 16] += 1
            if r2 >= threshold:
                print(f"*** HUGE delay at +{cpu.cycles-14_500_000:,}: r2=0x{r2:08x} "
                      f"({r2:,}) — entry #{entries}\n")
                print("backtrace (last ~140 instr, oldest first):")
                for (bpc, bop) in list(recent)[-90:]:
                    print(f"  0x{bpc:08x}: {bop:04x}  {disasm(bop, bpc)}")
                print("\nMMIO reads in the window (pc: addr -> val):")
                for (rpc, va, val) in mmior:
                    nm, _ = mmio._find(va)
                    print(f"  0x{rpc:08x}: {(nm.name if nm else '???'):10s} "
                          f"0x{va:08x} -> 0x{val:08x}")
                break
        mmio.tick(cpu)
        try:
            step()
        except Exception as e:
            print(f"FAULT @0x{cpu.pc:08x}: {type(e).__name__}: {e}"); break
    else:
        print(f"ran {run_ins:,} instr; loop entries={entries}; "
              f"no r2 >= {threshold:,}. r2-hi histogram: "
              f"{dict(seen_r2.most_common(8))}")

    print(f"\nstatic disasm 0x801e5a40..0x801e5a72 (delay setup):")
    for a in range(0x801e5a40, 0x801e5a74, 2):
        op = mem.r16(a)
        print(f"  0x{a:08x}: {op:04x}  {disasm(op, a)}")


if __name__ == "__main__":
    main()
