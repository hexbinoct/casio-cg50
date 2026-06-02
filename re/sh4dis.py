#!/usr/bin/env python3
"""
Compact SH-4 / SH-4A disassembler (big-endian) for the fx-CG50 OS image.
Covers the integer + system/control instruction set used by OS/driver/ISR code
(FPU ops are decoded coarsely as 'fpu ...'). Resolves PC-relative literal loads.

This is reusable project infra: `from sh4dis import disasm` or run as a script:
    python sh4dis.py 0x80001a40 0x80001b00
File offset of a vaddr = vaddr & 0x0FFFFFFF (P1/P2 both mirror the image).

Not a verification oracle — Ghidra/casio-emu remain authoritative for the CPU
core. Good enough to read control flow, MMIO accesses, and call graphs.
"""
import struct
import sys

IMG = r"F:\ru\myprojects\may\cg50\os\os_image\cg50_os_3.80.plain.bin"
_data = open(IMG, "rb").read()


def _off(va):
    return va & 0x0FFFFFFF


def w16(va):
    o = _off(va)
    return struct.unpack(">H", _data[o:o + 2])[0]


def w32(va):
    o = _off(va)
    return struct.unpack(">I", _data[o:o + 4])[0]


def decode(x, pc):
    """Decode one 16-bit instruction word x located at vaddr pc."""
    n = (x >> 8) & 0xF
    m = (x >> 4) & 0xF
    d4 = x & 0xF
    d8 = x & 0xFF
    d12 = x & 0xFFF
    simm8 = d8 - 256 if d8 >= 128 else d8

    # ---- fixed opcodes ----
    fixed = {
        0x0008: "clrt", 0x0009: "nop", 0x000b: "rts", 0x0018: "sett",
        0x0019: "div0u", 0x001b: "sleep", 0x0028: "clrmac", 0x002b: "rte",
        0x0038: "ldtlb", 0x0048: "clrs", 0x0058: "sets", 0x0019: "div0u",
    }
    if x in fixed:
        return fixed[x]

    hi = x >> 12
    # ---- 0x0 group ----
    if hi == 0x0:
        if d8 == 0x02: return f"stc sr,r{n}"
        if d8 == 0x12: return f"stc gbr,r{n}"
        if d8 == 0x22: return f"stc vbr,r{n}"
        if d8 == 0x32: return f"stc ssr,r{n}"
        if d8 == 0x42: return f"stc spc,r{n}"
        if (x & 0xFF) == 0x82: return f"stc r{(x>>4)&7}_bank,r{n}"  # 0n m2 banks
        if d8 == 0x03: return f"bsrf r{n}"
        if d8 == 0x23: return f"braf r{n}"
        if d8 == 0x29: return f"movt r{n}"
        if d8 == 0x0a: return f"sts mach,r{n}"
        if d8 == 0x1a: return f"sts macl,r{n}"
        if d8 == 0x2a: return f"sts pr,r{n}"
        if d8 == 0x5a: return f"sts fpul,r{n}"
        if d8 == 0x6a: return f"sts fpscr,r{n}"
        if d8 == 0x83: return f"pref @r{n}"
        if d8 == 0x93: return f"ocbi @r{n}"
        if d8 == 0xa3: return f"ocbp @r{n}"
        if d8 == 0xb3: return f"ocbwb @r{n}"
        if d8 == 0xc3: return f"movca.l r0,@r{n}"
        if d4 == 0x4: return f"mov.b r{m},@(r0,r{n})"
        if d4 == 0x5: return f"mov.w r{m},@(r0,r{n})"
        if d4 == 0x6: return f"mov.l r{m},@(r0,r{n})"
        if d4 == 0x7: return f"mul.l r{m},r{n}"
        if d4 == 0xc: return f"mov.b @(r0,r{m}),r{n}"
        if d4 == 0xd: return f"mov.w @(r0,r{m}),r{n}"
        if d4 == 0xe: return f"mov.l @(r0,r{m}),r{n}"
        if d4 == 0xf: return f"mac.l @r{m}+,@r{n}+"
        return f".word 0x{x:04x}"

    # ---- 0x1: mov.l Rm,@(disp,Rn) ----
    if hi == 0x1:
        return f"mov.l r{m},@(0x{d4*4:x},r{n})"
    # ---- 0x2: store group ----
    if hi == 0x2:
        t = {0: f"mov.b r{m},@r{n}", 1: f"mov.w r{m},@r{n}", 2: f"mov.l r{m},@r{n}",
             4: f"mov.b r{m},@-r{n}", 5: f"mov.w r{m},@-r{n}", 6: f"mov.l r{m},@-r{n}",
             7: f"div0s r{m},r{n}", 8: f"tst r{m},r{n}", 9: f"and r{m},r{n}",
             0xa: f"xor r{m},r{n}", 0xb: f"or r{m},r{n}", 0xc: f"cmp/str r{m},r{n}",
             0xd: f"xtrct r{m},r{n}", 0xe: f"mulu.w r{m},r{n}", 0xf: f"muls.w r{m},r{n}"}
        return t.get(d4, f".word 0x{x:04x}")
    # ---- 0x3: arithmetic/compare ----
    if hi == 0x3:
        t = {0: f"cmp/eq r{m},r{n}", 2: f"cmp/hs r{m},r{n}", 3: f"cmp/ge r{m},r{n}",
             4: f"div1 r{m},r{n}", 5: f"dmulu.l r{m},r{n}", 6: f"cmp/hi r{m},r{n}",
             7: f"cmp/gt r{m},r{n}", 8: f"sub r{m},r{n}", 0xa: f"subc r{m},r{n}",
             0xb: f"subv r{m},r{n}", 0xc: f"add r{m},r{n}", 0xd: f"dmuls.l r{m},r{n}",
             0xe: f"addc r{m},r{n}", 0xf: f"addv r{m},r{n}"}
        return t.get(d4, f".word 0x{x:04x}")
    # ---- 0x4: shifts / system control ----
    if hi == 0x4:
        t = {0x00: f"shll r{n}", 0x01: f"shlr r{n}", 0x04: f"rotl r{n}", 0x05: f"rotr r{n}",
             0x08: f"shll2 r{n}", 0x09: f"shlr2 r{n}", 0x10: f"dt r{n}", 0x11: f"cmp/pz r{n}",
             0x15: f"cmp/pl r{n}", 0x18: f"shll8 r{n}", 0x19: f"shlr8 r{n}",
             0x20: f"shal r{n}", 0x21: f"shar r{n}", 0x24: f"rotcl r{n}", 0x25: f"rotcr r{n}",
             0x28: f"shll16 r{n}", 0x29: f"shlr16 r{n}",
             0x06: f"lds.l @r{n}+,mach", 0x07: f"ldc.l @r{n}+,sr", 0x0a: f"lds r{n},mach",
             0x0b: f"jsr @r{n}", 0x0e: f"ldc r{n},sr",
             0x16: f"lds.l @r{n}+,macl", 0x17: f"ldc.l @r{n}+,gbr", 0x1a: f"lds r{n},macl",
             0x1b: f"tas.b @r{n}", 0x1e: f"ldc r{n},gbr",
             0x22: f"sts.l pr,@-r{n}", 0x23: f"stc.l vbr,@-r{n}", 0x26: f"lds.l @r{n}+,pr",
             0x27: f"ldc.l @r{n}+,vbr", 0x2a: f"lds r{n},pr", 0x2b: f"jmp @r{n}",
             0x2e: f"ldc r{n},vbr",
             0x03: f"stc.l sr,@-r{n}", 0x12: f"sts.l macl,@-r{n}", 0x02: f"sts.l mach,@-r{n}",
             0x33: f"stc.l ssr,@-r{n}", 0x37: f"ldc.l @r{n}+,ssr", 0x3e: f"ldc r{n},ssr",
             0x43: f"stc.l spc,@-r{n}", 0x47: f"ldc.l @r{n}+,spc", 0x4e: f"ldc r{n},spc",
             0x52: f"sts.l fpul,@-r{n}", 0x5a: f"lds r{n},fpul", 0x56: f"lds.l @r{n}+,fpul",
             0x62: f"sts.l fpscr,@-r{n}", 0x6a: f"lds r{n},fpscr", 0x66: f"lds.l @r{n}+,fpscr",
             0x24: f"rotcl r{n}"}
        if d8 in t:
            return t[d8]
        if d4 == 0xc: return f"shad r{m},r{n}"
        if d4 == 0xd: return f"shld r{m},r{n}"
        if d4 == 0xf: return f"mac.w @r{m}+,@r{n}+"
        if (x & 0x8F) == 0x87: return f"ldc.l @r{n}+,r{m&7}_bank"
        if (x & 0x8F) == 0x8e: return f"ldc r{n},r{m&7}_bank"
        if (x & 0x8F) == 0x83: return f"stc.l r{m&7}_bank,@-r{n}"
        return f".word 0x{x:04x}"
    # ---- 0x5: mov.l @(disp,Rm),Rn ----
    if hi == 0x5:
        return f"mov.l @(0x{d4*4:x},r{m}),r{n}"
    # ---- 0x6: move/unary ----
    if hi == 0x6:
        t = {0: f"mov.b @r{m},r{n}", 1: f"mov.w @r{m},r{n}", 2: f"mov.l @r{m},r{n}",
             3: f"mov r{m},r{n}", 4: f"mov.b @r{m}+,r{n}", 5: f"mov.w @r{m}+,r{n}",
             6: f"mov.l @r{m}+,r{n}", 7: f"not r{m},r{n}", 8: f"swap.b r{m},r{n}",
             9: f"swap.w r{m},r{n}", 0xa: f"negc r{m},r{n}", 0xb: f"neg r{m},r{n}",
             0xc: f"extu.b r{m},r{n}", 0xd: f"extu.w r{m},r{n}", 0xe: f"exts.b r{m},r{n}",
             0xf: f"exts.w r{m},r{n}"}
        return t.get(d4, f".word 0x{x:04x}")
    # ---- 0x7: add #imm,Rn ----
    if hi == 0x7:
        return f"add #0x{simm8:x},r{n}    ; {simm8}"
    # ---- 0x8: disp moves / branches ----
    if hi == 0x8:
        sub = (x >> 8) & 0xF
        if sub == 0x0: return f"mov.b r0,@(0x{d4:x},r{m})"
        if sub == 0x1: return f"mov.w r0,@(0x{d4*2:x},r{m})"
        if sub == 0x4: return f"mov.b @(0x{d4:x},r{m}),r0"
        if sub == 0x5: return f"mov.w @(0x{d4*2:x},r{m}),r0"
        if sub == 0x8: return f"cmp/eq #0x{simm8:x},r0"
        if sub == 0x9: return f"bt 0x{pc+4+simm8*2:08x}"
        if sub == 0xb: return f"bf 0x{pc+4+simm8*2:08x}"
        if sub == 0xd: return f"bt/s 0x{pc+4+simm8*2:08x}"
        if sub == 0xf: return f"bf/s 0x{pc+4+simm8*2:08x}"
        return f".word 0x{x:04x}"
    # ---- 0x9: mov.w @(disp,PC),Rn ----
    if hi == 0x9:
        ea = pc + 4 + d8 * 2
        return f"mov.w @(0x{d8*2:x},pc),r{n}    ; =0x{w16(ea):04x}"
    # ---- 0xA/0xB: bra/bsr ----
    if hi == 0xA:
        disp = d12 - 0x1000 if d12 >= 0x800 else d12
        return f"bra 0x{pc+4+disp*2:08x}"
    if hi == 0xB:
        disp = d12 - 0x1000 if d12 >= 0x800 else d12
        return f"bsr 0x{pc+4+disp*2:08x}"
    # ---- 0xC: gbr/imm ops ----
    if hi == 0xC:
        sub = (x >> 8) & 0xF
        t = {0x0: f"mov.b r0,@(0x{d8:x},gbr)", 0x1: f"mov.w r0,@(0x{d8*2:x},gbr)",
             0x2: f"mov.l r0,@(0x{d8*4:x},gbr)", 0x3: f"trapa #0x{d8:x}",
             0x4: f"mov.b @(0x{d8:x},gbr),r0", 0x5: f"mov.w @(0x{d8*2:x},gbr),r0",
             0x6: f"mov.l @(0x{d8*4:x},gbr),r0", 0x7: f"mova @(0x{d8*4:x},pc),r0",
             0x8: f"tst #0x{d8:x},r0", 0x9: f"and #0x{d8:x},r0", 0xa: f"xor #0x{d8:x},r0",
             0xb: f"or #0x{d8:x},r0", 0xc: f"tst.b #0x{d8:x},@(r0,gbr)",
             0xd: f"and.b #0x{d8:x},@(r0,gbr)", 0xe: f"xor.b #0x{d8:x},@(r0,gbr)",
             0xf: f"or.b #0x{d8:x},@(r0,gbr)"}
        return t.get(sub, f".word 0x{x:04x}")
    # ---- 0xD: mov.l @(disp,PC),Rn ----
    if hi == 0xD:
        ea = (pc & ~3) + 4 + d8 * 4
        return f"mov.l @(0x{d8*4:x},pc),r{n}    ; =0x{w32(ea):08x}"
    # ---- 0xE: mov #imm,Rn ----
    if hi == 0xE:
        return f"mov #0x{d8:x},r{n}    ; {simm8}"
    # ---- 0xF: FPU (coarse) ----
    if hi == 0xF:
        return f"fpu 0x{x:04x} (fr{m}->fr{n})"
    return f".word 0x{x:04x}"


def disasm(start, end, label=""):
    if label:
        print(f"=== {label}  0x{start:08x}..0x{end:08x} ===")
    va = start
    while va < end:
        x = w16(va)
        print(f"  {va:08x}: {x:04x}   {decode(x, va)}")
        va += 2


if __name__ == "__main__":
    a = int(sys.argv[1], 0) if len(sys.argv) > 1 else 0x80001a40
    b = int(sys.argv[2], 0) if len(sys.argv) > 2 else a + 0xC0
    disasm(a, b)
