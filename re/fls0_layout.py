"""Where does the storage memory (fls0) begin in the fx-CG50's NOR flash?

1. Survey the 16 MB dump (os/flash_dump/flash_full.bin) in 128 KB sectors from 10 MB up:
   erased (all 0xFF) / data, plus ASCII hints in each sector.
2. Scan the OS 3.60 image for 32-bit literals that point into the upper flash
   (0x00A00000-0x02000000 through any of the 0x00/0x80/0xA0 windows) and list them with counts
   and the code addresses that load them (mov.l @(disp,pc)).

Run: python re/fls0_layout.py [flash.bin]
"""
import os
import re
import struct
import sys
from collections import Counter, defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FLASH = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "os", "flash_dump", "flash_full.bin")
OSBIN = os.path.join(ROOT, "os", "flash_dump", "os.bin")

flash = open(FLASH, "rb").read()
osimg = open(OSBIN, "rb").read()
print(f"flash image {len(flash):#x} bytes, os.bin {len(osimg):#x} bytes")

SEC = 0x20000
print("\n== sectors (128 KB) from 0x00A00000 ==")
prev = None
run_start = None
for off in range(0xA00000, len(flash), SEC):
    s = flash[off:off + SEC]
    ff = s.count(0xFF) / len(s)
    zero = s.count(0) / len(s)
    kind = "erased" if ff == 1.0 else f"data ff={ff:.2f} 00={zero:.2f}"
    words = re.findall(rb"[ -~]{6,}", s)
    hint = b" | ".join(words[:4]).decode("ascii", "replace")[:90]
    print(f"{off:#010x} {kind:28s} {hint}")

print("\n== literals into upper flash in the OS image ==")
lits = Counter()
where = defaultdict(list)
for pc in range(0, len(osimg) - 1, 2):
    op = struct.unpack_from(">H", osimg, pc)[0]
    if op & 0xF000 == 0xD000:  # mov.l @(disp,pc),Rn
        ea = (pc & ~3) + 4 + (op & 0xFF) * 4
        if ea + 4 > len(osimg):
            continue
        v = struct.unpack_from(">I", osimg, ea)[0]
        phys = v & 0x1FFFFFFF
        win = v >> 29
        if win in (0, 4, 5) and 0x00A00000 <= phys < 0x02000000:
            lits[v] += 1
            if len(where[v]) < 6:
                where[v].append(0x80000000 + pc)
for v, n in sorted(lits.items(), key=lambda kv: kv[0] & 0x1FFFFFFF):
    print(f"{v:#010x} x{n:<3d} loaded at " + " ".join(f"{a:#010x}" for a in where[v]))
