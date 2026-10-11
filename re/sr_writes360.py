"""List every `ldc Rn,SR` / `ldc.l @Rn+,SR` in the OS 3.60 image (boot code included) and, where
the value is a literal (mov.l @(disp,pc) / mov #imm into Rn shortly before), that value. Used to
decide whether SR bit 12 (the SH4AL-DSP DSP bit, set on the real calculator: an add-in reads
SR = 0x40001101) can survive the OS's absolute SR writes.

Run: python re/sr_writes360.py
"""
import os
import struct

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
img = open(os.path.join(ROOT, "os", "flash_dump", "os.bin"), "rb").read()


def op_at(pc):
    return struct.unpack_from(">H", img, pc)[0]


def source(pc, n):
    """Walk back up to 12 instructions for the last write of Rn; describe it."""
    for back in range(1, 13):
        p = pc - 2 * back
        if p < 0:
            break
        op = op_at(p)
        if op & 0xF000 == 0xD000 and (op >> 8) & 0xF == n:          # mov.l @(disp,pc),Rn
            ea = (p & ~3) + 4 + (op & 0xFF) * 4
            return "literal %#010x" % struct.unpack_from(">I", img, ea)[0]
        if op & 0xF000 == 0xE000 and (op >> 8) & 0xF == n:          # mov #imm,Rn
            return "imm %#x" % ((op & 0xFF) - 0x100 if op & 0x80 else op & 0xFF)
        if op & 0xF0FF == 0x0002 and (op >> 8) & 0xF == n:          # stc SR,Rn
            return "stc sr (read-modify-write)"
        if (op >> 8) & 0xF == n and op & 0xF000 in (0x2000, 0x3000, 0x6000, 0x7000, 0xC000):
            continue                                                # and/or/add on Rn: keep looking
    return "?"


hits = []
for pc in range(0, len(img) - 1, 2):
    op = op_at(pc)
    if op & 0xF0FF == 0x400E:
        hits.append((pc, "ldc r%d,sr" % ((op >> 8) & 0xF), source(pc, (op >> 8) & 0xF)))
    elif op & 0xF0FF == 0x4007:
        hits.append((pc, "ldc.l @r%d+,sr" % ((op >> 8) & 0xF), "stack"))

kinds = {}
for pc, ins, src in hits:
    kinds.setdefault(src.split()[0], 0)
    kinds[src.split()[0]] += 1
    if src.startswith("literal") or src.startswith("imm"):
        v = int(src.split()[1], 0) & 0xFFFFFFFF
        print(f"{0x80000000 + pc:#010x}  {ins:16s} {src:28s} bit12={'1' if v & 0x1000 else '0'}")
print(f"\n{len(hits)} SR writes; by source: {kinds}")
