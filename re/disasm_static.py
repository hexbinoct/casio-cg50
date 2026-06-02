#!/usr/bin/env python3
"""Static SH-4 disassembly of the 3.60 physical image (flash_full.bin) at given
virtual addresses (no emulation). file_off = vaddr & 0x1FFFFFFF (OS at phys 0).
Usage: python re/disasm_static.py <start_hex> <end_hex> [start2 end2 ...]"""
import os, sys
sys.path.insert(0, os.path.dirname(__file__))
import sh4dis

IMG = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "flash_full.bin")
img = open(IMG, "rb").read()

def r16(va):
    off = va & 0x1FFFFFFF
    return (img[off] << 8) | img[off+1]

args = sys.argv[1:]
pairs = [(int(args[i], 0), int(args[i+1], 0)) for i in range(0, len(args), 2)]
for start, end in pairs:
    print(f"--- 0x{start:08x}..0x{end:08x} ---")
    a = start & ~1
    while a < end:
        op = r16(a)
        # resolve mov.l/mov.w pc-relative literal targets to actual values
        extra = ""
        try:
            txt = sh4dis.decode(op, a)
        except Exception:
            txt = f"0x{op:04x}"
        if (op & 0xF000) == 0xD000:        # mov.l @(disp,pc),Rn
            disp = op & 0xFF
            lit = ((a & ~3) + 4 + disp*4)
            loff = lit & 0x1FFFFFFF
            val = int.from_bytes(img[loff:loff+4], "big")
            extra = f"   ; [0x{lit:08x}]=0x{val:08x}"
        elif (op & 0xF000) == 0x9000:      # mov.w @(disp,pc),Rn
            disp = op & 0xFF
            lit = (a + 4 + disp*2)
            loff = lit & 0x1FFFFFFF
            val = int.from_bytes(img[loff:loff+2], "big")
            extra = f"   ; [0x{lit:08x}]=0x{val:04x}"
        print(f"  0x{a:08x}: {op:04x}  {txt}{extra}")
        a += 2
    print()
