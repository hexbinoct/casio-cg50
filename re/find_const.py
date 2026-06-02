#!/usr/bin/env python3
"""Find literal-pool occurrences of 32-bit BE constants in the 3.60 os.bin and
report them as vaddrs (so they can be looked up in Ghidra). Used to locate code
that reads/writes a given MMIO address via a PC-relative literal.
Usage: python re/find_const.py 0xa44c0020 [0xa44c0000 ...]"""
import os, sys, struct

OS = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "os.bin")
BASE = 0x80000000
img = open(OS, "rb").read()

consts = [int(a, 0) for a in sys.argv[1:]] or [0xA44C0020, 0xA44C0000]
for c in consts:
    needle = struct.pack(">I", c)
    hits = []
    start = 0
    while True:
        i = img.find(needle, start)
        if i < 0:
            break
        if i % 4 == 0:  # literal pools are 4-aligned
            hits.append(BASE + i)
        start = i + 1
    print(f"0x{c:08x}: {len(hits)} aligned literal(s)")
    for h in hits:
        print(f"   literal @0x{h:08x}")
