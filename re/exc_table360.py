"""OS 3.60: which handlers the shared event table holds for the CPU's exception codes, and
whether the OS refers to TRA (trapa) or to the FPU-only exception codes at all.

The OS's general-exception (VBR+0x100) and TLB-miss (VBR+0x400) vectors read EXPEVT and use the
same table as interrupts: handler = word at flash 0x80028A18 + 4*((code - 0x40) / 0x20) (copied
to IL RAM 0xFD8010C8 at start-up; see wiki interrupts page). Prints one line per code.

    python re/exc_table360.py
"""
import os
import struct

HERE = os.path.dirname(os.path.abspath(__file__))
IMG = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")
BASE = 0x80000000
TABLE = 0x80028A18

CODES = [
    (0x040, "TLB miss, read/fetch"), (0x060, "TLB miss, write"),
    (0x080, "initial page write"), (0x0A0, "TLB protection, read"),
    (0x0C0, "TLB protection, write"), (0x0E0, "address error, read/fetch"),
    (0x100, "address error, write"), (0x120, "FPU exception"),
    (0x140, "TLB multiple hit"), (0x160, "trapa"),
    (0x180, "general illegal instruction"), (0x1A0, "slot illegal instruction"),
    (0x1E0, "user break"), (0x800, "general FPU disable"), (0x820, "slot FPU disable"),
]


def main():
    img = open(IMG, "rb").read()
    w = lambda a: struct.unpack_from(">I", img, a - BASE)[0]
    targets = {}
    for code, name in CODES:
        h = w(TABLE + 4 * ((code - 0x40) // 0x20))
        targets.setdefault(h, []).append(code)
        print(f"  EXPEVT {code:#05x} {name:30s} -> {h:#010x}")
    print("distinct handlers:", ", ".join(f"{h:#010x} ({len(c)} codes)" for h, c in targets.items()))
    # literal pool references (aligned words) to TRA / EXPEVT / INTEVT / FPSCR-ish registers
    for lit, name in [(0xFF000020, "TRA"), (0xFF000024, "EXPEVT"), (0xFF000028, "INTEVT")]:
        hits = [BASE + i for i in range(0, len(img) - 3, 4) if struct.unpack_from(">I", img, i)[0] == lit]
        print(f"  literal {lit:#010x} ({name}): {len(hits)} refs", " ".join(f"{h:#x}" for h in hits[:10]))


if __name__ == "__main__":
    main()
