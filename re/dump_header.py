#!/usr/bin/env python3
"""
Dump & lightly parse the fx-CG50 OS image header regions.
File offset == vaddr - 0x80000000.  Used alongside Ghidra (code) to map the
boot entry, the CASIOWIN OS header, and the version/region info block.
"""
import struct

IMG = r"F:\ru\myprojects\may\cg50\os\os_image\cg50_os_3.80.plain.bin"
BASE = 0x80000000
data = open(IMG, "rb").read()


def hexdump(off, n, label=""):
    print(f"\n=== {label}  vaddr=0x{BASE+off:08x} off=0x{off:x} len=0x{n:x} ===")
    for i in range(0, n, 16):
        row = data[off + i: off + i + 16]
        hexs = " ".join(f"{b:02x}" for b in row)
        asc = "".join(chr(b) if 32 <= b < 127 else "." for b in row)
        print(f"  {BASE+off+i:08x}  {hexs:<47}  {asc}")


# --- SH4 BE 16-bit instruction quick-decoder (just enough for the boot stub) ---
def sh4(off, count):
    print(f"\n=== SH4 disasm @ 0x{BASE+off:08x} ({count} insns) ===")
    pc = off
    for _ in range(count):
        w = struct.unpack(">H", data[pc:pc + 2])[0]
        print(f"  {BASE+pc:08x}: {w:04x}   {decode(w, pc)}")
        pc += 2


def decode(w, pc):
    n = (w >> 8) & 0xF
    m = (w >> 4) & 0xF
    d8 = w & 0xFF
    d12 = w & 0xFFF
    if w == 0x0009:
        return "nop"
    if w == 0x000b:
        return "rts"
    if (w & 0xF000) == 0xD000:          # mov.l @(disp,PC),Rn
        ea = (pc & ~3) + 4 + d8 * 4
        val = struct.unpack(">I", data[ea:ea + 4])[0]
        return f"mov.l @(0x{d8*4:x},pc),r{n}    ; [0x{BASE+ea:08x}] = 0x{val:08x}"
    if (w & 0xF000) == 0x9000:          # mov.w @(disp,PC),Rn
        ea = pc + 4 + d8 * 2
        val = struct.unpack(">H", data[ea:ea + 2])[0]
        return f"mov.w @(0x{d8*2:x},pc),r{n}    ; 0x{val:04x}"
    if (w & 0xF000) == 0xE000:          # mov #imm,Rn
        imm = d8 - 256 if d8 >= 128 else d8
        return f"mov #0x{d8:02x},r{n}    ; {imm}"
    if (w & 0xF000) == 0xA000:          # bra
        disp = d12 - 0x1000 if d12 >= 0x800 else d12
        return f"bra 0x{BASE+pc+4+disp*2:08x}"
    if (w & 0xF000) == 0xB000:          # bsr
        disp = d12 - 0x1000 if d12 >= 0x800 else d12
        return f"bsr 0x{BASE+pc+4+disp*2:08x}"
    if (w & 0xF0FF) == 0x402b:
        return f"jmp @r{n}"
    if (w & 0xF0FF) == 0x400b:
        return f"jsr @r{n}"
    if (w & 0xF0FF) == 0x4029:
        return f"shlr16 r{n}"
    if (w & 0xF00F) == 0x6003:
        return f"mov r{m},r{n}"
    if (w & 0xF00F) == 0x2002:
        return f"mov.l r{m},@r{n}"
    if (w & 0xF00F) == 0x6002:
        return f"mov.l @r{m},r{n}"
    if (w & 0xF00F) == 0x600c:
        return f"extu.b r{m},r{n}"
    if (w & 0xFF00) == 0x8b00:
        disp = d8 - 256 if d8 >= 128 else d8
        return f"bf 0x{BASE+pc+4+disp*2:08x}"
    if (w & 0xFF00) == 0x8900:
        disp = d8 - 256 if d8 >= 128 else d8
        return f"bt 0x{BASE+pc+4+disp*2:08x}"
    return "?"


# boot stub / reset
sh4(0x0, 24)
hexdump(0x0, 0x40, "boot stub bytes")

# CASIOWIN OS header (string was at 0x80000e98)
hexdump(0xe80, 0xC0, "CASIOWIN header")

# version region (string '3.80' was at 0x80020021)
hexdump(0x20000, 0x80, "version/info block")

# scan for plausible region/section tables: runs of 0x8xxxxxxx pointers early on
print("\n=== 0x8xxxxxxx-looking words in first 0x1000 ===")
for off in range(0, 0x1000, 4):
    v = struct.unpack(">I", data[off:off + 4])[0]
    if 0x80000000 <= v <= 0x80b60000 or 0xa0000000 <= v <= 0xa0b60000:
        print(f"  0x{BASE+off:08x}: 0x{v:08x}")
