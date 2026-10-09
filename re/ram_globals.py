"""Read OS 3.60 RAM globals from the real DRAM dump (os/flash_dump/dram.bin, physical 0x0C000000,
captured on a real fx-CG50 under the gint dumper add-in).

Run: python re/ram_globals.py [0x8C04CA24 ...]
"""
import os
import struct
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
dram = open(os.path.join(ROOT, "os", "flash_dump", "dram.bin"), "rb").read()
addrs = [int(a, 0) for a in sys.argv[1:]] or [0x8C04CA24, 0x8C04C9F8, 0x8C04C9FC, 0x8C04CF0C, 0x8C0A1160]
for a in addrs:
    off = (a & 0x1FFFFFFF) - 0x0C000000
    v = struct.unpack_from(">I", dram, off)[0]
    print(f"{a:#010x} = {v:#010x}")
