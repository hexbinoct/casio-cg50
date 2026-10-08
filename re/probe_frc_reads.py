#!/usr/bin/env python3
"""Boot the 3.60 flash in the Python oracle (pure boot, like gen_golden.py) and record
every read that lands in the emulator's catch-all "FRC" region (0xA4130000-0xA413FFFF,
which also spans the RTC at 0xA413FEC0) and in the RTC model, with the reading PC.
Answers: does the 3.60 boot read anything at 0xA4130000 other than RTC registers?
Usage: python re/probe_frc_reads.py [instructions]   (default 3,000,000)"""
import os, sys, collections
HERE = os.path.dirname(__file__)
sys.path.insert(0, os.path.join(HERE, "..", "emu"))
sys.path.insert(0, HERE)
from memory import Memory
import mmio as M
from cpu import CPU

IMG = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")

frc = collections.Counter()
rtc = collections.Counter()
cpu = None

orig_frc = M.FreeCounter.read
orig_rtc = M.RTC.read


def frc_read(self, va, size):
    frc[(va, size, cpu.pc if cpu else 0)] += 1
    return orig_frc(self, va, size)


def rtc_read(self, va, size):
    rtc[(va, size, cpu.pc if cpu else 0)] += 1
    return orig_rtc(self, va, size)


M.FreeCounter.read = frc_read
M.RTC.read = rtc_read


def main():
    global cpu
    total = int(sys.argv[1], 0) if len(sys.argv) > 1 else 3_000_000
    image = open(IMG, "rb").read()
    bus = M.MMIOBus(log=False)
    bus.set_instr_per_second(1_000_000)
    mem = Memory(image, bus)
    cpu = CPU(mem)
    bus.cpu = cpu
    cpu.pc = 0x80000000
    for _ in range(total):
        cpu.step()
    print(f"ran {total:,} instructions, final pc={cpu.pc:#010x}")
    print(f"FRC catch-all reads: {sum(frc.values())}")
    for (va, size, pc), n in sorted(frc.items()):
        print(f"  va={va:#010x} size={size} pc={pc:#010x} x{n}")
    print(f"RTC reads: {sum(rtc.values())}")
    for (va, size, pc), n in sorted(rtc.items()):
        print(f"  va={va:#010x} size={size} pc={pc:#010x} x{n}")


if __name__ == "__main__":
    main()
