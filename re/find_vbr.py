#!/usr/bin/env python3
"""
Scan the plain OS image for SH-4 VBR / control-register access instructions, to
locate the interrupt vector base setup and the exception entry/exit code.

SH-4 encodings (16-bit, big-endian):
  LDC   Rm,VBR        0100mmmm0010_1110   -> byte0 0x4m, byte1 0x2E   (SET vbr)
  LDC.L @Rm+,VBR      0100mmmm0010_0111   -> byte0 0x4m, byte1 0x27   (restore vbr)
  STC   VBR,Rn        0000nnnn0010_0010   -> byte0 0x0n, byte1 0x22   (read vbr)
  STC.L VBR,@-Rn      0100nnnn0010_0011   -> byte0 0x4n, byte1 0x23   (save vbr)
  LDC   Rm,SR         0100mmmm0000_1110   -> byte0 0x4m, byte1 0x0E   (set SR)
  RTE                 0000000000101011    -> 0x002B                  (return from exception)
"""
import struct

IMG = r"F:\ru\myprojects\may\cg50\os\os_image\cg50_os_3.80.plain.bin"
BASE = 0x80000000
data = open(IMG, "rb").read()

PATTERNS = {
    "LDC Rm,VBR   (set VBR)":   lambda b0, b1: 0x40 <= b0 <= 0x4F and b1 == 0x2E,
    "LDC.L @Rm+,VBR (restore)": lambda b0, b1: 0x40 <= b0 <= 0x4F and b1 == 0x27,
    "STC VBR,Rn   (read VBR)":  lambda b0, b1: 0x00 <= b0 <= 0x0F and b1 == 0x22,
    "STC.L VBR,@-Rn (save)":    lambda b0, b1: 0x40 <= b0 <= 0x4F and b1 == 0x23,
    "RTE":                      lambda b0, b1: b0 == 0x00 and b1 == 0x2B,
}

for label, test in PATTERNS.items():
    hits = []
    for off in range(0, len(data) - 1, 2):
        b0, b1 = data[off], data[off + 1]
        if test(b0, b1):
            reg = (b0 & 0x0F)
            hits.append((BASE + off, reg))
    print(f"\n=== {label} : {len(hits)} hit(s) ===")
    for va, reg in hits[:40]:
        print(f"  0x{va:08x}   (R{reg})")
    if len(hits) > 40:
        print(f"  ... +{len(hits)-40} more")
