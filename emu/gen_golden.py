#!/usr/bin/env python3
"""Generate a golden reference trace from the (validated) Python emulator for the
Go port to assert against. Boots flash_full.bin (3.60) from reset with the timer
DISABLED (pure boot bring-up), and every `stride` instructions records full CPU
state. Binary format (big-endian u32):
   magic 'GOLD'(0x474f4c44), version=1, count, stride,
   then count records of 23 u32: pc, sr, r0..r15, pr, gbr, vbr, mach, macl
Output: emu/golden_boot.bin
"""
import os, sys, struct
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from memory import Memory
from mmio import MMIOBus
from cpu import CPU

HERE = os.path.dirname(__file__)
IMG = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")
OUT = os.path.join(HERE, "golden_boot.bin")


def main():
    total  = int(sys.argv[1], 0) if len(sys.argv) > 1 else 2_000_000
    stride = int(sys.argv[2], 0) if len(sys.argv) > 2 else 1000

    image = open(IMG, "rb").read()
    mmio = MMIOBus(log=False)            # timer_period=0 -> no interrupts (pure boot)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000

    recs = []
    def snap():
        return (cpu.pc, cpu.sr, *cpu.r, cpu.pr, cpu.gbr, cpu.vbr, cpu.mach, cpu.macl)

    for i in range(total):
        if i % stride == 0:
            recs.append(snap())
        cpu.step()

    with open(OUT, "wb") as f:
        f.write(struct.pack(">IIII", 0x474F4C44, 1, len(recs), stride))
        for rec in recs:
            f.write(struct.pack(">23I", *(v & 0xFFFFFFFF for v in rec)))
    print(f"wrote {OUT}: {len(recs)} records (stride {stride}, {total:,} instr), "
          f"final PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x}")


if __name__ == "__main__":
    main()
