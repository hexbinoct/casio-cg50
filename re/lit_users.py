"""List the OS 3.60 code addresses that load given 32-bit literals (mov.l @(disp,pc),Rn).

Run: python re/lit_users.py 0x8C04CA24 [0x...]
"""
import os
import struct
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
img = open(os.path.join(ROOT, "os", "flash_dump", "os.bin"), "rb").read()
want = {int(a, 0) for a in sys.argv[1:]}
hits = {v: [] for v in want}
for pc in range(0, len(img) - 1, 2):
    op = struct.unpack_from(">H", img, pc)[0]
    if op & 0xF000 == 0xD000:
        ea = (pc & ~3) + 4 + (op & 0xFF) * 4
        if ea + 4 <= len(img):
            v = struct.unpack_from(">I", img, ea)[0]
            if v in hits:
                hits[v].append(0x80000000 + pc)
for v in sorted(hits):
    a = hits[v]
    print(f"{v:#010x}: {len(a)} loads: " + " ".join(f"{x:#010x}" for x in a[:16]) + (" ..." if len(a) > 16 else ""))
