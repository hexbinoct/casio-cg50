#!/usr/bin/env python3
"""Peek literal-pool / data words in the plain OS image (vaddr -> value).
Edit PEEKS for whatever addresses we're chasing. file off = vaddr & 0x0fffffff
(works for both 0x80.. and 0xA0.. since they mirror the same bytes)."""
import struct

IMG = r"F:\ru\myprojects\may\cg50\os\os_image\cg50_os_3.80.plain.bin"
data = open(IMG, "rb").read()


def off(vaddr):
    return vaddr & 0x0FFFFFFF


def u32(vaddr):
    return struct.unpack(">I", data[off(vaddr):off(vaddr) + 4])[0]


def u16(vaddr):
    return struct.unpack(">H", data[off(vaddr):off(vaddr) + 2])[0]


# Dump the whole boot literal pool shared by the reset subroutines.
# 16-bit slots (half-word constants) vs 32-bit slots (reg bases / fn ptrs):
LO, HI = 0xa0000730, 0xa00007c4
HALFWORD = {0x734, 0x736, 0x738, 0x73a, 0x73c, 0x73e}  # off&0xfff that are u16

print(f"image {len(data)} bytes")
print(f"\n=== boot literal pool 0x{LO:08x}..0x{HI:08x} ===")
va = LO
while va < HI:
    low = va & 0xfff
    if low in HALFWORD:
        print(f"  0x{va:08x} (u16) = 0x{u16(va):04x}")
        va += 2
    else:
        v = u32(va)
        tag = ""
        if 0x80000000 <= v <= 0x80b60000 or 0xa0000000 <= v <= 0xa0b60000 \
           or 0x8c000000 <= v <= 0x8d000000:
            tag = "  <- code/RAM ptr"
        elif (v >> 24) in (0xa4, 0xfe, 0xff, 0xb4):
            tag = "  <- MMIO reg"
        print(f"  0x{va:08x} (u32) = 0x{v:08x}{tag}")
        va += 4
