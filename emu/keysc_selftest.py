#!/usr/bin/env python3
"""Oracle transcript for the KEYSC key-scan model (emu/mmio.py KeyScan). Runs a fixed script
of host/driver operations against the Python model and writes emu/keysc_golden.txt; the Go
port replays the same script in emu_go/keysc_test.go (TestKeyscOracleTranscript) and must
produce a byte-identical transcript. Regenerate after any change to KeyScan / keysc.go:
    python emu/keysc_selftest.py
"""
import os, sys
sys.path.insert(0, os.path.dirname(__file__))
from mmio import KeyScan

OUT = os.path.join(os.path.dirname(__file__), "keysc_golden.txt")
BASE = 0xA44B0000

# (op, a, b): press/release (row,col); w (off,val) 16-bit; r (off, size); tick (cycles);
# irq () reads+clears the raised-IRQ list. Same list is hard-coded in the Go test.
SCRIPT = [
    ("press", 2, 7), ("tick", 100), ("r", 0x14, 2), ("irq",),      # disabled: nothing
    ("release", 2, 7),
    ("w", 0x0C, 0x8000), ("w", 0x14, 0x00FF), ("w", 0x14, 0x4800),
    ("w", 0x0E, 0x8042), ("w", 0x18, 0xC8), ("w", 0x10, 0x200),
    ("r", 0x0C, 2), ("r", 0x0E, 2), ("r", 0x10, 2), ("r", 0x12, 2), ("r", 0x14, 2),
    ("tick", 200), ("r", 0x14, 2), ("irq",),                         # idle scan
    ("press", 2, 7), ("press", 6, 9), ("tick", 300),                # DOWN + F1
    ("r", 0, 4), ("r", 6, 2), ("r", 6, 1), ("r", 7, 1), ("r", 8, 2), ("r", 8, 4), ("r", 0x14, 2), ("irq",),
    ("w", 0x14, 0x760A), ("r", 0x14, 2),
    ("tick", 350), ("r", 0x14, 2), ("irq",),                         # too early: no scan yet
    ("tick", 400), ("r", 0x14, 2), ("irq",), ("w", 0x14, 0x7602),
    ("release", 2, 7), ("tick", 500), ("r", 6, 2), ("r", 0x14, 2), ("irq",), ("w", 0x14, 0x7602),
    ("release", 6, 9), ("tick", 600), ("r", 0x14, 2), ("irq",), ("w", 0x14, 0x7602),
    ("tick", 700), ("r", 0x14, 2), ("irq",), ("w", 0x14, 0x7602),
    ("tick", 800), ("r", 0x14, 2), ("irq",),                         # silence
    ("w", 0x14, 0x4800), ("press", 0, 0), ("tick", 900), ("r", 0, 2), ("r", 0x14, 2), ("irq",),
    ("release", 0, 0), ("w", 0x14, 0x00FF),
    ("w", 0x10, 0x800), ("tick", 1000), ("r", 0x14, 2), ("irq",),   # sync scan, no key
    ("w", 0x14, 0x00FF), ("w", 0x10, 0x400), ("press", 1, 8), ("tick", 1100), ("r", 0x14, 2), ("irq",),
    ("tick", 1200), ("r", 0x14, 2), ("irq",),                        # detect-only: no scan flag
    ("w", 0x10, 0), ("tick", 1300), ("r", 0x14, 2), ("irq",),        # off
    ("release", 1, 8), ("press", 9, 0), ("press", 0, 12), ("r", 0, 2), ("r", 0xA, 2),  # out of range ignored
    ("w", 0x1A, 0x0FFF), ("r", 0x1A, 2),
]


class _CPU:
    def __init__(self):
        self.cycles = 0
        self.raised = []

    def raise_irq(self, intevt, level):
        self.raised.append((intevt, level))


def run():
    k = KeyScan("KEYSC", BASE, 0x1000, scan_period=100)
    cpu = _CPU()
    lines = []
    for op in SCRIPT:
        if op[0] == "press":
            k.press(op[1], op[2]); lines.append(f"press {op[1]} {op[2]}")
        elif op[0] == "release":
            k.release(op[1], op[2]); lines.append(f"release {op[1]} {op[2]}")
        elif op[0] == "w":
            k.write(BASE + op[1], 2, op[2]); lines.append(f"w {op[1]:03x} {op[2]:04x}")
        elif op[0] == "r":
            v = k.read(BASE + op[1], op[2]); lines.append(f"r {op[1]:03x}/{op[2]} = {v:x}")
        elif op[0] == "tick":
            cpu.cycles = op[1]; k.tick(cpu); lines.append(f"tick {op[1]}")
        elif op[0] == "irq":
            lines.append("irq" + "".join(f" {i:x}/{l}" for i, l in cpu.raised)); cpu.raised = []
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    text = run()
    open(OUT, "w", newline="\n").write(text)
    print(f"wrote {OUT}: {text.count(chr(10))} lines")
