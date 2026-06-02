#!/usr/bin/env python3
"""Load the idle snapshot, fire ONE timer IRQ, and trace the ISR instruction by
instruction to see where it derails (it currently runs away to 0x032527b6 instead
of rte-ing back to the idle loop). Prints pc/op/disasm + MMIO + key reg deltas.

Usage: python emu/trace_isr.py [trace_ins]
"""
import os, sys, pickle
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
    trace_ins = int(sys.argv[1], 0) if len(sys.argv) > 1 else 400
    cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
    print(f"idle: PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x} VBR=0x{cpu.vbr:08x} "
          f"PR=0x{cpu.pr:08x} R15=0x{cpu.r[15]:08x}")

    # arm timer to fire on the very next tick
    mmio.timer_period = 100_000
    mmio.timer_next = cpu.cycles

    # step until the IRQ is taken
    fired = False
    for _ in range(10):
        mmio.tick(cpu)
        before = cpu.irq_count
        cpu.step()
        if cpu.irq_count > before:
            fired = True
            break
    print(f"IRQ taken: now PC=0x{cpu.pc:08x} SPC=0x{cpu.spc:08x} SSR=0x{cpu.ssr:08x} "
          f"INTEVT=0x{mem.r32(0xFF000028):x}\n")

    # detailed trace
    mmio.log = True
    idle_lo, idle_hi = 0x801de560, 0x802af4a0
    for i in range(trace_ins):
        pc = cpu.pc
        op = mem.r16(pc)
        # annotate notable control-flow
        note = ""
        if op == 0x002B: note = "  <-- RTE"
        if op == 0x000B: note = "  <-- RTS"
        if idle_lo <= pc <= idle_hi: note = "  <-- BACK IN IDLE BAND"
        print(f"{i:4d} {pc:08x}: {op:04x} {disasm(op,pc):28s} "
              f"R15=0x{cpu.r[15]:08x} PR=0x{cpu.pr:08x}{note}")
        try:
            cpu.step()
        except Exception as e:
            print(f"  FAULT @0x{cpu.pc:08x}: {type(e).__name__}: {e}")
            break
        if idle_lo <= cpu.pc <= idle_hi:
            print(f"  *** returned to idle band at 0x{cpu.pc:08x} after {i+1} instr — ISR OK")
            break
        # bail if we run off into flash/garbage
        if cpu.pc < 0x80000000 and not (0xA0000000 <= cpu.pc < 0xB0000000):
            print(f"  *** RUNAWAY: PC=0x{cpu.pc:08x} (not in OS) after {i+1} instr")
            break


if __name__ == "__main__":
    main()
