#!/usr/bin/env python3
"""Oracle transcript for the NOR flash command state machine (emu/memory.py _flash_cmd).
Drives OS-shaped sequences — a word program, a 128 KB sector erase whose first reads return the
embedded-algorithm status (DQ7=0 busy, DQ6/DQ2 toggling, DQ3=1 started, as the OS's erase
routine in IL RAM 0xFD800BB6 checks), reads outside the erasing sector, a buffered program —
and writes emu/flash_golden.txt, one op per line:
    w ADDR VAL      16-bit write to ADDR (P2, 0xA0000000 | flash offset)
    r ADDR SIZE V   read of SIZE bytes at ADDR and its value
emu_go/flash_test.go replays the file against the Go memory and must read the same.
Regenerate after any change to the flash model:  python emu/flash_selftest.py
"""
import os, sys
sys.path.insert(0, os.path.dirname(__file__))
from memory import Memory

OUT = os.path.join(os.path.dirname(__file__), "flash_golden.txt")
P2 = 0xA0000000


def main():
    image = bytearray(0x80000)
    for i in range(len(image)):
        image[i] = (i * 7 + 3) & 0xFF
    m = Memory(bytes(image), None)
    lines = []

    def w(off, val):
        m.write(P2 | off, 2, val)
        lines.append(f"w {P2 | off:08x} {val:04x}")

    def r(off, size):
        v = m.read(P2 | off, size)
        lines.append(f"r {P2 | off:08x} {size} {v:x}")

    def unlock():
        w(0xAAA, 0xAA)
        w(0x554, 0x55)

    # word program: bits only go 1 -> 0
    r(0x70000, 2)
    unlock(); w(0xAAA, 0xA0); w(0x70000, 0x1234)
    r(0x70000, 2)
    # sector erase of 0x40000..0x5ffff (command at an address inside it)
    unlock(); w(0xAAA, 0x80); unlock(); w(0x52468, 0x30)
    r(0x3fffe, 2)                       # outside: array data
    for _ in range(6):                  # status reads, then array data again
        r(0x40000, 2)
    r(0x40000, 4); r(0x5fffc, 4); r(0x60000, 2)
    # an erase whose status is read with byte and long accesses
    unlock(); w(0xAAA, 0x80); unlock(); w(0x20000, 0x30)
    r(0x20001, 1); r(0x20004, 4); r(0x3fffe, 2); r(0x20000, 2); r(0x20000, 2)
    # buffered program (0x25, count-1, data, 0x29 confirm) into the erased sector
    unlock(); w(0x40000, 0x25); w(0x40000, 1); w(0x40000, 0xBEEF); w(0x40002, 0xCAFE); w(0x40000, 0x29)
    r(0x40000, 4)

    with open(OUT, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"wrote {OUT} ({len(lines)} ops)")


if __name__ == "__main__":
    main()
