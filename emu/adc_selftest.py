#!/usr/bin/env python3
"""Oracle transcript for the battery-ADC model (emu/mmio.py PeriphIRQ, the INTEVT 0x560 source).
Drives the bus with OS-shaped sequences — the legacy periodic tick before the first start, a
software start (+0x88 bit 13, battery monitor), the idle routine's start (+0x8C bit 15) that
completes only after enough CPU *sleep*, re-arming, cancelling, the ISR's flag clears — and
writes emu/adc_golden.txt, one op per line:
    ips N          set instructions per second
    at C I         set cpu.cycles = C, cpu.idle = I
    w OFF VAL      16-bit write to 0xA4610000+OFF
    tick           one MMIOBus.tick
    r OFF V        16-bit read of 0xA4610000+OFF and its value
    irq LIST       INTEVT codes raised since the last irq line (then cleared)
emu_go/adc_test.go replays the file against the Go bus and must read/raise the same.
Regenerate after any change to PeriphIRQ / periphIRQ:  python emu/adc_selftest.py
"""
import os, sys
sys.path.insert(0, os.path.dirname(__file__))
from mmio import MMIOBus

OUT = os.path.join(os.path.dirname(__file__), "adc_golden.txt")
BASE = 0xA4610000


class _CPU:
    def __init__(self):
        self.cycles = 0
        self.idle = 0
        self.raised = []

    def raise_irq(self, intevt, level):
        self.raised.append(intevt)


def run():
    bus = MMIOBus(log=False)
    cpu = _CPU()
    bus.cpu = cpu
    bus.timer_period = 30000
    lines = []

    def ips(n):
        bus.set_instr_per_second(n); lines.append(f"ips {n}")

    def at(c, i):
        cpu.cycles, cpu.idle = c, i; lines.append(f"at {c} {i}")

    def w(off, val):
        bus.write(BASE + off, 2, val); lines.append(f"w {off:x} {val:04x}")

    def tick():
        bus.tick(cpu); lines.append("tick")

    def r(off):
        lines.append(f"r {off:x} {bus.read(BASE + off, 2):04x}")

    def irq():
        lines.append("irq" + "".join(f" {x:x}" for x in cpu.raised)); cpu.raised = []

    ips(1_000_000)                         # 100 instr per software conversion, 15625 of sleep
    # legacy periodic tick until the first start
    at(0, 0); tick(); irq(); r(0x88)
    at(30000, 0); tick(); irq(); r(0x88)
    w(0x88, 0x0042); w(0x8A, 0x0000)       # ISR-style clears
    # software start (battery monitor): +0x88 |= 0x4000 then |= 0x2000
    at(40000, 0); w(0x88, 0x4042); w(0x88, 0x6042); r(0x88)
    at(40050, 0); tick(); irq(); r(0x88)   # not yet
    at(40100, 0); tick(); irq(); r(0x88)   # done: flags set, 0x560
    at(40101, 0); tick(); irq()            # once only
    at(60000, 0); tick(); irq()            # would-be periodic tick: legacy mode is off now
    w(0x88, 0x0042)
    # idle-routine start: completes only after 15625 cycles of sleep
    w(0x8A, 0x01CF); w(0x8C, 0x0000); w(0x88, 0x0042); w(0x8C, 0x8000); w(0x8A, 0x01CF); r(0x8C)
    at(100000, 0); tick(); irq()           # CPU ran 60k cycles, slept none: nothing
    at(110000, 10000); tick(); irq()       # 10k of sleep: not yet
    at(115625, 15625); tick(); irq(); r(0x8C); r(0x88)
    w(0x88, 0x0042)
    # re-arm restarts the count; writing 0x8C = 0 cancels
    w(0x8C, 0x8000); at(120000, 20000); tick(); irq()
    w(0x8C, 0x0000); w(0x8C, 0x8000)       # restart at idle 20000
    at(130000, 30000); tick(); irq()       # 10k since restart: not yet
    w(0x8C, 0x0000)                         # cancel
    at(200000, 90000); tick(); irq()       # nothing: cancelled
    # results always read as the modelled battery voltage
    r(0x82); r(0x84)
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    t = run()
    open(OUT, "w", newline="\n").write(t)
    print(f"wrote {OUT} ({t.count(chr(10))} ops)")
