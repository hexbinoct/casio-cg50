#!/usr/bin/env python3
"""Syscall resolver for the 3.60 physical image (flash_full.bin).
The table base is read from the trampoline at 0x80020070 (mov.l @(disp,pc),r2).
Usage: python re/syscall360.py <id> [id ...]   |   python re/syscall360.py <lo> <hi> --range
       python re/syscall360.py --rev <addr> [addr ...]  (which ids point at/near addr)"""
import os, struct, sys

IMG = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "flash_full.bin")
d = open(IMG, "rb").read()

def u16(va): o = va & 0x1FFFFFFF; return struct.unpack(">H", d[o:o+2])[0]
def u32(va): o = va & 0x1FFFFFFF; return struct.unpack(">I", d[o:o+4])[0]

# find the mov.l @(disp,pc),r2 in the trampoline
TABLE = None
for a in range(0x80020070, 0x80020080, 2):
    op = u16(a)
    if op & 0xFF00 == 0xD200:
        TABLE = u32(((a & ~3) + 4 + (op & 0xFF) * 4))
        break
assert TABLE, "trampoline not found"

def resolve(i): return u32(TABLE + i * 4)

args = sys.argv[1:]
print(f"SYSCALL_TABLE (3.60) = 0x{TABLE:08x}")
if args and args[0] == "--rev":
    targets = [int(x, 0) for x in args[1:]]
    for i in range(0x2000):
        h = resolve(i)
        for t in targets:
            if abs(h - t) < 0x40:
                print(f"  sc 0x{i:04x} -> 0x{h:08x}  (near 0x{t:08x})")
elif "--range" in args:
    lo, hi = int(args[0], 0), int(args[1], 0)
    for i in range(lo, hi + 1):
        print(f"  sc 0x{i:04x} -> 0x{resolve(i):08x}")
else:
    for x in args:
        i = int(x, 0)
        print(f"  sc 0x{i:04x} -> 0x{resolve(i):08x}")
