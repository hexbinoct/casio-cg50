#!/usr/bin/env python3
"""For each candidate timer INTEVT (handlers that reference 0xA4610088), deliver it
once from the idle snapshot and classify the outcome:
  - reaches common ISR-return trampoline 0x80021020 then rte's back to SPC = GOOD
  - runs away (<0x80000000) or faults = BAD
Also report whether the handler cleared the flag bits we set at 0xA4610088 (bits 14/15)
and whether the OS subsequently leaves the idle loop to do work.
"""
import os, sys, pickle
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from cpu import CPU
from memory import Memory
from mmio import MMIOBus

HERE = os.path.dirname(__file__)
SNAP = os.path.join(HERE, "idle_state.pkl")
CANDIDATES = [0x1c0, 0x560, 0x620, 0x640, 0xbe0]
TRAMP = 0x80021020          # common ISR-return trampoline (PR set by dispatcher)
IDLE_SPC = 0x801de57a


def test(intevt, trace=4000):
    cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
    pirq = mmio.periph_irq
    pirq.set_timer_flag()                       # set bits 14/15 at 0xA4610088
    flag0 = pirq.regs.get(0x88, 0)
    cpu.raise_irq(intevt, 0x8)
    # take the IRQ
    for _ in range(4):
        b = cpu.irq_count
        cpu.step()
        if cpu.irq_count > b:
            break
    entry_pc = cpu.pc
    reached_tramp = False
    returned = False
    outcome = "ran out of trace"
    acked_at = None
    for i in range(trace):
        if cpu.pc == TRAMP:
            reached_tramp = True
        if reached_tramp and cpu.pc == IDLE_SPC:
            returned = True
            outcome = f"GOOD: handler completed, rte back to idle SPC after {i} instr"
            break
        # detect ack: flag bits 14/15 cleared
        if acked_at is None and (pirq.regs.get(0x88, 0) & 0xC000) == 0:
            acked_at = i
        try:
            cpu.step()
        except Exception as e:
            outcome = f"BAD fault @0x{cpu.pc:08x}: {type(e).__name__}: {e}"
            break
        if cpu.pc < 0x80000000 and not (0xA0000000 <= cpu.pc < 0xB0000000) \
                and not (0xFD800000 <= cpu.pc < 0xFD810000):
            outcome = f"BAD runaway: PC=0x{cpu.pc:08x} after {i} instr"
            break
    flag1 = pirq.regs.get(0x88, 0)
    return {
        "intevt": intevt, "entry": entry_pc, "outcome": outcome,
        "tramp": reached_tramp, "returned": returned,
        "flag": f"0x{flag0:04x}->0x{flag1:04x}", "acked_at": acked_at,
        "final_pc": cpu.pc, "irqs": cpu.irq_count,
    }


def main():
    print(f"testing {len(CANDIDATES)} candidate timer INTEVTs from idle snapshot:\n")
    for iv in CANDIDATES:
        r = test(iv)
        print(f"INTEVT 0x{iv:03x}: entry=0x{r['entry']:08x}  flag {r['flag']}  "
              f"acked@{r['acked_at']}  reached_tramp={r['tramp']}  "
              f"returned={r['returned']}")
        print(f"            {r['outcome']}  (final PC=0x{r['final_pc']:08x})\n")


if __name__ == "__main__":
    main()
