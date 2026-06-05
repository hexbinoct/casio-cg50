#!/usr/bin/env python3
"""
Find every reference to the 0xA4CB00xx peripheral (cont.18c root cause) in the OS image.

SH literal pools embed 32-bit constants (the MMIO register addresses) near the code that
uses them. Scanning flash_full.bin for big-endian words in the peripheral's address window
reveals every register offset in use AND the code location (the literal pool sits a few
bytes after/within the accessor function). We print each hit as a runtime 0x80xxxxxx addr
so they can be looked up in Ghidra.

Run:  python re/find_periph.py
"""
import os, struct, collections

HERE = os.path.dirname(__file__)
FLASH = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")
flash = open(FLASH, "rb").read()

# the peripheral lives at 0xA4CB0000; scan a generous window in case of more registers
LO, HI = 0xA4CB0000, 0xA4CB0100


def scan_window(lo, hi, label):
    hits = []  # (phys_offset_of_literal, value)
    regs = collections.Counter()
    n = len(flash)
    for off in range(0, n - 3, 2):           # SH literals are 4-byte aligned to 2 (mov.l pools are 4)
        v = (flash[off] << 24) | (flash[off+1] << 16) | (flash[off+2] << 8) | flash[off+3]
        if lo <= v < hi:
            hits.append((off, v))
            regs[v] += 1
    print(f"\n=== {label}: {len(hits)} literal-pool refs to [0x{lo:08x},0x{hi:08x}) ===")
    print("register offsets referenced (value : count):")
    for v in sorted(regs):
        print(f"   0x{v:08x}  (+0x{v & 0xff:02x})  x{regs[v]}")
    print("\nliteral locations (runtime 0x80.. addr = where the constant sits in code):")
    last = None
    for off, v in hits:
        run = 0x80000000 + off
        # group: print a blank line when there's a gap (different function/pool)
        if last is not None and off - last > 0x40:
            print("   ----")
        print(f"   pool@0x{run:08x}  = 0x{v:08x} (+0x{v & 0xff:02x})")
        last = off


def main():
    print(f"flash: {len(flash)} bytes")
    scan_window(LO, HI, "peripheral 0xA4CB00xx")
    # also scan the whole 0xA4CB0000 page boundary +/- to catch a different base guess
    scan_window(0xA4CB0000, 0xA4CC0000, "wider 0xA4CB____ window")


if __name__ == "__main__":
    main()
