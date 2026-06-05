#!/usr/bin/env python3
"""Read literal-pool pointer values from the 3.60 os.bin (rebased to 0x80000000)."""
import os, struct

HERE = os.path.dirname(__file__)
OSB = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")
img = open(OSB, "rb").read()

def rd32(addr):
    off = addr - 0x80000000
    return struct.unpack(">I", img[off:off+4])[0]

for a in (0x80210688, 0x8021068c, 0x80210690, 0x80210694, 0x80210698):
    print(f"  *0x{a:08x} = 0x{rd32(a):08x}")
