#!/usr/bin/env python3
"""Dump the KEYSC matrix->keycode table at DAT_805ff7ec (used by FUN_801952cc as
table[col*0x1c + row*4], col 1..12, row 1..7) so we can identify which (col,row)
matrix position is EXE / arrows / F-keys for keyboard injection.
vaddr -> file off = vaddr & 0x1FFFFFFF (image at phys 0)."""
import os, struct

OS = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "os.bin")
img = open(OS, "rb").read()
BASE = 0x805ff7ec
off0 = BASE & 0x1FFFFFFF

def entry(col, row):
    o = off0 + col * 0x1c + row * 4
    return struct.unpack(">i", img[o:o+4])[0]

print(f"key table @0x{BASE:08x} (file off 0x{off0:x}); entries = table[col*0x1c+row*4]")
print("      " + "".join(f" row{r:<6}" for r in range(0, 8)))
for col in range(0, 13):
    cells = []
    for row in range(0, 8):
        v = entry(col, row)
        cells.append(f"{v & 0xffffffff:08x}" if v not in (-1,) else "  --    ")
    print(f"col{col:2d}: " + " ".join(cells))
