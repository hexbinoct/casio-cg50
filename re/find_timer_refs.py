#!/usr/bin/env python3
"""List 4-aligned literal-pool words in the 3.60 os.bin that point into the timer/clock
register blocks (TMU, ETMU, the 0xA44C block, RTC, the 0xA4130000 block), so the code that
uses each register can be found. Prints value -> literal addresses (runtime 0x80xxxxxx).
Usage: python re/find_timer_refs.py"""
import os, struct, collections

OS = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "os.bin")
BASE = 0x80000000
img = open(OS, "rb").read()

RANGES = [
    ("TMU", 0xA4490000, 0xA4490100),
    ("ETMU-A44A", 0xA44A0000, 0xA44A0100),
    ("BLK-A44C", 0xA44C0000, 0xA44C0100),
    ("ETMU-A44D", 0xA44D0000, 0xA44D0100),
    ("A44E-A452", 0xA44E0000, 0xA4530000),
    ("RTC", 0xA413FEC0, 0xA413FF00),
    ("A4130000-below-RTC", 0xA4130000, 0xA413FEC0),
]

hits = collections.defaultdict(list)
for off in range(0, len(img) - 3, 4):
    v = struct.unpack_from(">I", img, off)[0]
    for name, lo, hi in RANGES:
        if lo <= v < hi:
            hits[(name, v)].append(BASE + off)

for name, lo, hi in RANGES:
    keys = sorted(k for k in hits if k[0] == name)
    print(f"== {name} ({lo:#010x}-{hi:#010x}): {sum(len(hits[k]) for k in keys)} literals")
    for k in keys:
        locs = hits[k]
        shown = " ".join(f"{a:#010x}" for a in locs[:8])
        more = f" (+{len(locs) - 8} more)" if len(locs) > 8 else ""
        print(f"  {k[1]:#010x}: {len(locs):3d}  {shown}{more}")
