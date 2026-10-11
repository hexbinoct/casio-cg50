"""The OS 3.60's flash routines in IL RAM: where they come from and what they do.

1. Take the real IL RAM dump (os/flash_dump/ilram.bin, 0xFD800000...) and find where in the
   OS image each 256-byte chunk of 0xFD800000-0xFD801FFF also appears (the copy's source).
2. Find the code that copies it (literals 0xFD800000 / the source address in the OS image).
3. List the NOR command constants the IL RAM code uses (mov #imm values and 16/32-bit literals)
   so the command sequences can be read with re/sh4dis.py.

Run: python re/flash_routines360.py
"""
import os
import struct
from collections import defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OS = open(os.path.join(ROOT, "os", "flash_dump", "os.bin"), "rb").read()
IL = open(os.path.join(ROOT, "os", "flash_dump", "ilram.bin"), "rb").read()
BASE_OS = 0x80000000
BASE_IL = 0xFD800000

print(f"os.bin {len(OS):#x} bytes, ilram.bin {len(IL):#x} bytes")

# 1. chunk matching
CH = 0x100
print("\n== IL RAM chunks found in the OS image ==")
runs = []
for off in range(0, 0x2000, CH):
    chunk = IL[off:off + CH]
    if chunk.count(0) == CH or chunk.count(0xFF) == CH:
        continue
    pos = OS.find(chunk)
    hits = []
    while pos != -1 and len(hits) < 3:
        hits.append(pos)
        pos = OS.find(chunk, pos + 1)
    if hits:
        delta = hits[0] + BASE_OS - (BASE_IL + off)
        runs.append((off, hits[0], delta))
        print(f"  IL {BASE_IL + off:#010x}  <- OS {BASE_OS + hits[0]:#010x}  (x{len(hits)})")
    else:
        print(f"  IL {BASE_IL + off:#010x}  not found (variables or changed)")

# 2. who references the source
srcs = sorted({BASE_OS + h - o for o, h, _ in runs})
print("\n== candidate copy sources (OS address that maps to 0xFD800000) ==")
for s in srcs:
    print(f"  {s:#010x}")

want = set(srcs) | {BASE_IL, BASE_IL + 0x1000}
for s in list(srcs):
    want.add(s & 0x1FFFFFFF | 0xA0000000)
refs = defaultdict(list)
for pc in range(0, len(OS) - 1, 2):
    op = struct.unpack_from(">H", OS, pc)[0]
    if op & 0xF000 == 0xD000:
        ea = (pc & ~3) + 4 + (op & 0xFF) * 4
        if ea + 4 <= len(OS):
            v = struct.unpack_from(">I", OS, ea)[0]
            if v in want and len(refs[v]) < 12:
                refs[v].append(BASE_OS + pc)
print("\n== code loading those addresses ==")
for v, l in sorted(refs.items()):
    print(f"  {v:#010x}: " + " ".join(f"{a:#010x}" for a in l))

# 3. NOR constants in the IL RAM code 0xFD800000-0xFD801000
print("\n== NOR-looking constants in IL RAM code (mov #imm / mov.w / mov.l literals) ==")
cmd_imms = {0xAA: "unlock1 data", 0x55: "unlock2 data", 0xA0: "program", 0x80: "erase setup",
            0x30: "sector erase", 0x10: "chip erase", 0x25: "write-to-buffer", 0x29: "buffer confirm",
            0xF0: "reset", 0x90: "autoselect", 0x98: "CFI"}
found = defaultdict(list)
for pc in range(0, 0x1000, 2):
    op = struct.unpack_from(">H", IL, pc)[0]
    a = BASE_IL + pc
    if op & 0xF000 == 0xE000:  # mov #imm,Rn
        imm = op & 0xFF
        if imm in cmd_imms:
            found[f"mov #{imm:#04x} ({cmd_imms[imm]})"].append(a)
    elif op & 0xF000 == 0x9000:  # mov.w @(disp,pc)
        ea = pc + 4 + (op & 0xFF) * 2
        if ea + 2 <= len(IL):
            v = struct.unpack_from(">H", IL, ea)[0]
            if v in (0xAAA, 0x554, 0x555, 0x2AA, 0xAA, 0x55, 0xA0, 0x80, 0x30, 0x25, 0x29, 0xF0,
                     0xFFAA, 0xFF55, 0xFFA0, 0xFF80, 0xFF98, 0xFF90, 0xFFF0):
                found[f"mov.w literal {v:#06x}"].append(a)
    elif op & 0xF000 == 0xD000:  # mov.l @(disp,pc)
        ea = (pc & ~3) + 4 + (op & 0xFF) * 4
        if ea + 4 <= len(IL):
            v = struct.unpack_from(">I", IL, ea)[0]
            low = v & 0xFFF
            if (v >> 24) in (0xA0, 0x80, 0x00) and low in (0xAAA, 0x554, 0x555, 0x2AA) or \
                    v in (0xFFFE0000, 0xA0000000, 0xA0C80000, 0x1FFFFFFF):
                found[f"mov.l literal {v:#010x}"].append(a)
for k, l in sorted(found.items()):
    print(f"  {k:36s} at " + " ".join(f"{x:#010x}" for x in l[:10]) + (" ..." if len(l) > 10 else ""))

# 4. routine starts (rts followed by code / known entry points) - list rts positions
print("\n== rts in IL RAM 0xFD800000-0xFD801000 (routine ends) ==")
ends = [BASE_IL + pc for pc in range(0, 0x1000, 2) if struct.unpack_from(">H", IL, pc)[0] == 0x000B]
print("  " + " ".join(f"{x:#010x}" for x in ends))
