#!/usr/bin/env python3
"""Check facts for wiki/docs/peripherals/keyboard.md against the OS 3.60 image.

1. Table 0x8068fa70 (used by the keyboard ISR): 16-bit entry [col*8+row] for 12 cols x 8 rows;
   report whether every entry == (row+1)<<8 | (col+1), and list the exceptions.
2. For each key row in re/KEYMAP.md: matrix (row,col), KEYSC word/bit, primary code.
Usage: python re/wiki_keysc_check.py
"""
import os, re, struct, sys

sys.stdout.reconfigure(encoding="utf-8")
HERE = os.path.dirname(os.path.abspath(__file__))
OS = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")
img = open(OS, "rb").read()

def rd16(va):
    o = va - 0x80000000
    return struct.unpack(">H", img[o:o + 2])[0]

T = 0x8068fa70
bad = []
for col in range(12):
    for row in range(8):
        v = rd16(T + 2 * (col * 8 + row))
        want = ((row + 1) << 8) | (col + 1)
        if v != want:
            bad.append((row, col, v, want))
print(f"table {T:#x}: 96 entries, {96 - len(bad)} match (row+1)<<8|(col+1)")
for b in bad:
    print(f"  row {b[0]} col {b[1]}: {b[2]:#06x} (expected {b[3]:#06x})")
# what follows the table
print("next words:", " ".join(f"{rd16(T + 192 + 2*i):04x}" for i in range(8)))

print()
for line in open(os.path.join(HERE, "KEYMAP.md"), encoding="utf-8"):
    c = [x.strip() for x in line.split("|")]
    if len(c) < 12 or not c[5].startswith("`"):
        continue
    label, inject, prim = c[1], c[5].strip("`"), c[7]
    r, col = map(int, inject.split("-"))
    word, bit = col >> 1, r + 8 * (col & 1)
    print(f"{label:22} ({r},{col:2})  word{word} bit{bit:2}  mask {1 << bit:#06x}  primary {prim}")
