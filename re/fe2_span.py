"""How much of the on-chip memory at 0xFE200000 does OS 3.60 address? Lists every 32-bit literal
loaded by mov.l @(disp,pc) in 0xFE000000-0xFEFFFFFF (excluding the DMAC at 0xFE008000 and BSC
0xFEC10000 pages), grouped by 4 KB page, with the lowest/highest value.

Run: python re/fe2_span.py
"""
import os
import struct
from collections import defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
img = open(os.path.join(ROOT, "os", "flash_dump", "os.bin"), "rb").read()
vals = defaultdict(list)
for pc in range(0, len(img) - 1, 2):
    op = struct.unpack_from(">H", img, pc)[0]
    if op & 0xF000 == 0xD000:
        ea = (pc & ~3) + 4 + (op & 0xFF) * 4
        if ea + 4 <= len(img):
            v = struct.unpack_from(">I", img, ea)[0]
            if 0xFE000000 <= v < 0xFF000000:
                vals[v].append(0x80000000 + pc)
pages = defaultdict(list)
for v in vals:
    pages[v & ~0xFFF].append(v)
for p in sorted(pages):
    vs = sorted(pages[p])
    n = sum(len(vals[v]) for v in vs)
    users = sorted({a for v in vs for a in vals[v]})
    print(f"page {p:#010x}: {len(vs):3d} values {vs[0]:#010x}..{vs[-1]:#010x}, {n} loads, first users "
          + " ".join(f"{a:#x}" for a in users[:4]))
fe2 = sorted(v for v in vals if 0xFE200000 <= v < 0xFE400000)
if fe2:
    print(f"\n0xFE2xxxxx literals: lowest {fe2[0]:#010x}, highest {fe2[-1]:#010x}, {len(fe2)} distinct")
