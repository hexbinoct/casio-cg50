#!/usr/bin/env python3
"""Oracle transcript for the R61524 LCD register model (emu/mmio.py LCD). Drives the model
through the bus with OS-shaped sequences (setORG read-modify-write, getORG, window setup,
GRAM writes/read, index-phase read) and writes emu/lcd_golden.txt: one op per line —
    pfc VV        write PFC 0xA405013C (bit4 = RS)
    w VVVV        16-bit write to 0xB4000000
    r VVVV        16-bit read of 0xB4000000 and its result
emu_go/lcd_test.go (TestLCDOracleTranscript) replays the file against the Go bus and must
read identical values. Regenerate after any change to LCD / lcd.go:
    python emu/lcd_selftest.py
"""
import os, sys
sys.path.insert(0, os.path.dirname(__file__))
from mmio import MMIOBus

OUT = os.path.join(os.path.dirname(__file__), "lcd_golden.txt")
PFC, LCD = 0xA405013C, 0xB4000000


def run():
    bus = MMIOBus(log=False)
    lines = []

    def pfc(v):
        bus.write(PFC, 1, v); lines.append(f"pfc {v:02x}")

    def w(v):
        bus.write(LCD, 2, v); lines.append(f"w {v:04x}")

    def r():
        lines.append(f"r {bus.read(LCD, 2):04x}")

    def sel(i):
        pfc(0x00); w(i); pfc(0x10)

    sel(0x003); r()                                   # boot entry mode
    sel(0x003); r(); w(0x0030); sel(0x003); r()       # read-back after write
    sel(0x003); r(); sel(0x003); w(0x00B0); sel(0x003); r()   # setORG-style RMW
    for i, v in ((0x210, 0x14F), (0x211, 0x14F), (0x212, 0xA8), (0x213, 0xA8), (0x200, 0), (0x201, 0)):
        sel(i); w(v)
    for i in (0x210, 0x211, 0x212, 0x213, 0x200, 0x201, 0x7FF):
        sel(i); r()
    sel(0x202); w(0xFFFF); w(0x1234); r()             # GRAM write/read (read not modelled: 0)
    pfc(0x00); w(0x1003); r()                         # index phase: masked index read back
    pfc(0x10); r()
    sel(0x00B); r()
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    t = run()
    open(OUT, "w", newline="\n").write(t)
    print(f"wrote {OUT} ({t.count(chr(10))} ops, {t.count('r ')} reads)")
