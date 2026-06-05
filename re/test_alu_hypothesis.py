#!/usr/bin/env python3
"""
Validate the 0xA4CB0000 ALU hypothesis WITHOUT the device, by monkeypatching the
peripheral into the Python oracle and re-running the result formatter from the snapshot.

Hypothesis (from Ghidra RE cont.18c):
  reg 0x14=opA, 0x18=opB, 0x10=cmd(16b), 0x1C=result. Multi-word BCD ALU, carry/borrow
  latched between words. cmd bit1 = first(0)/continue(1); cmd bit0 = add(1)/sub(0):
    cmd0 = BCD sub first ; cmd1 = BCD add first ; cmd2 = BCD sub cont ; cmd3 = BCD add cont
    cmd4 = (unknown) -> try passthrough (result = opA)
If correct, the rounding leaf returns 98765 unchanged and the formatter emits "98765".

Run:  python re/test_alu_hypothesis.py
"""
import os, sys, struct
HERE = os.path.dirname(__file__)
sys.path.insert(0, os.path.join(HERE, "..", "emu")); sys.path.insert(0, HERE)
from memory import Memory, DRAM_SIZE, ILRAM_SIZE, OCRAM_SIZE
from mmio import MMIOBus
from cpu import CPU

FLASH = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")
SNAP = os.path.join(HERE, "..", "emu_go", "fmt_snapshot.bin")
FMT_ENTRY = 0x800fc5a4


def bcd_add(a, b, cin):
    res = 0; c = cin
    for i in range(8):
        s = ((a >> (4*i)) & 0xf) + ((b >> (4*i)) & 0xf) + c
        c = 1 if s >= 10 else 0
        if s >= 10: s -= 10
        res |= s << (4*i)
    return res & 0xFFFFFFFF, c


def bcd_sub(a, b, bin_):
    res = 0; bw = bin_
    for i in range(8):
        s = ((a >> (4*i)) & 0xf) - ((b >> (4*i)) & 0xf) - bw
        bw = 1 if s < 0 else 0
        if s < 0: s += 10
        res |= s << (4*i)
    return res & 0xFFFFFFFF, bw


class ALU:
    def __init__(self): self.A = self.B = self.res = self.c = self.bw = 0
    def write(self, off, val):
        if off == 0x14: self.A = val & 0xFFFFFFFF
        elif off == 0x18: self.B = val & 0xFFFFFFFF
        elif off == 0x10:
            cmd = val & 0xFFFF
            if cmd == 1:   self.res, self.c  = bcd_add(self.A, self.B, 0)
            elif cmd == 3: self.res, self.c  = bcd_add(self.A, self.B, self.c)
            elif cmd == 0: self.res, self.bw = bcd_sub(self.A, self.B, 0)
            elif cmd == 2: self.res, self.bw = bcd_sub(self.A, self.B, self.bw)
            elif cmd == 4: self.res = self.A           # guess: passthrough
            else:          self.res = self.A
    def read(self, off):
        return self.res if off == 0x1C else 0


def build():
    mmio = MMIOBus(log=False)
    mem = Memory(open(FLASH, "rb").read(), mmio)
    cpu = CPU(mem)
    blob = open(SNAP, "rb").read()
    regs = struct.unpack_from("<36I", blob, 0); off = 36*4
    cpu.r = list(regs[0:16]); cpu.rbank1 = list(regs[16:24])
    (cpu.pc, cpu.pr, cpu.gbr, cpu.vbr, cpu.ssr, cpu.spc, cpu.sgr,
     cpu.mach, cpu.macl, cpu.fpul, cpu.fpscr, sr) = regs[24:36]
    cpu._sr = sr
    mem.dram[:] = blob[off:off+DRAM_SIZE]; off += DRAM_SIZE
    mem.ilram[:] = blob[off:off+ILRAM_SIZE]; off += ILRAM_SIZE
    mem.ocram[:] = blob[off:off+OCRAM_SIZE]
    return cpu, mem


def main():
    cpu, mem = build()
    alu = ALU()
    orig_read, orig_write = mem.read, mem.write
    def read(va, size):
        p = va & 0x1FFFFFFF
        if 0x04CB0000 <= p < 0x04CB0100:
            return alu.read(p - 0x04CB0000)
        return orig_read(va, size)
    def write(va, size, val):
        p = va & 0x1FFFFFFF
        if 0x04CB0000 <= p < 0x04CB0100:
            alu.write(p - 0x04CB0000, val); return
        orig_write(va, size, val)
    mem.read, mem.write = read, write

    entry_pr, entry_sp = cpu.pr, cpu.r[15]
    steps = 0
    while steps < 5_000_000:
        if steps > 0 and cpu.pc == entry_pr and cpu.r[15] >= entry_sp:
            break
        op = mem.r16(cpu.pc); cpu.pc = (cpu.pc + 2) & 0xFFFFFFFF; cpu.execute(op); cpu.cycles += 1
        steps += 1

    # the formatted decimal string was located at 0x8c1866f0 in the un-patched run; the
    # display object param-2 region carries width = (#glyphs)*18. Dump both to judge.
    def hx(a, n): return " ".join(f"{mem.r8((a+i) & 0xFFFFFFFF):02x}" for i in range(n))
    s = "".join(chr(mem.r8(0x8c1866f0+i)) if 0x20 <= mem.r8(0x8c1866f0+i) < 0x7f else "." for i in range(12))
    print(f"formatter returned after {steps} steps, r0=0x{cpu.r[0]:08x}")
    print(f"formatted-string region 0x8c1866f0: {hx(0x8c1866f0, 12)}  |{s}|")
    print(f"display object 0x8c187234       : {hx(0x8c187234, 12)}  (width@+0 = {mem.r16(0x8c187234)} px = {mem.r16(0x8c187234)//18} glyph(s))")


if __name__ == "__main__":
    main()
