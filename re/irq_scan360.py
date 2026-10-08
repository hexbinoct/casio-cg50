#!/usr/bin/env python3
"""Locate the OS 3.60 interrupt machinery statically in os/flash_dump/os.bin.

1. Every `ldc rN,vbr` (0x4n2E) in the code area, with the literal that set rN when it is a
   PC-relative mov.l just before.
2. Every literal-pool word equal to one of the interesting constants (INTEVT, the IL-RAM
   handler/priority tables, the 3.80 addresses), with the instructions that load it.
3. Optional: `--table SRC N` dumps N 32-bit words from flash (to decode a ROM table that is
   copied to IL RAM).

Run: python re/irq_scan360.py            (scan)
     python re/irq_scan360.py --table 0x80021xxx 128
"""
import struct
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
IMG = ROOT / "os" / "flash_dump" / "os.bin"
sys.path.insert(0, str(Path(__file__).resolve().parent))
import sh4dis  # noqa: E402

sh4dis.set_image(str(IMG))
data = IMG.read_bytes()
CODE_END = 0xB80000  # OS image extent

CONSTS = {
    0xFF000028: "INTEVT",
    0xFF000024: "EXPEVT",
    0xFF2F0000: "CPUOPM",
    0xFD8010C8: "3.60 handler table",
    0xFD8012C8: "3.60 priority table",
    0xFD8004D0: "3.80 handler table",
    0xFD8006D0: "3.80 priority table",
    0x80020F00: "3.60 VBR",
    0x800014D4: "3.80 VBR",
    0x80001454: "boot VBR",
    0xA4080000: "INTC base",
}


def w16(o):
    return struct.unpack_from(">H", data, o)[0]


def w32(o):
    return struct.unpack_from(">I", data, o)[0]


def loaders(lit_off, window=0x400):
    """mov.l @(disp,PC),Rn (0xDnxx) whose target is lit_off; also mova."""
    out = []
    lo = max(0, lit_off - window)
    for o in range(lo, lit_off, 2):
        x = w16(o)
        if x >> 12 == 0xD:
            ea = ((o + 4) & ~3) + (x & 0xFF) * 4
            if ea == lit_off:
                out.append((o, (x >> 8) & 0xF))
    return out


def scan():
    print("== ldc rN,vbr ==")
    for o in range(0, CODE_END, 2):
        x = w16(o)
        if x & 0xF0FF == 0x402E:
            n = (x >> 8) & 0xF
            # look back for mov.l @(disp,PC),Rn
            src = ""
            for b in range(o - 2, max(0, o - 0x20), -2):
                y = w16(b)
                if y >> 12 == 0xD and (y >> 8) & 0xF == n:
                    ea = ((b + 4) & ~3) + (y & 0xFF) * 4
                    src = f"  r{n} = 0x{w32(ea):08x} (mov.l at 0x{0x80000000 + b:08x})"
                    break
            print(f"  0x{0x80000000 + o:08x}: ldc r{n},vbr{src}")
    print("\n== literal references ==")
    for o in range(0, CODE_END - 3, 4):
        v = w32(o)
        if v in CONSTS:
            ls = loaders(o)
            if not ls:
                continue
            refs = ", ".join(f"0x{0x80000000 + a:08x}(r{n})" for a, n in ls)
            print(f"  {CONSTS[v]:22s} 0x{v:08x} lit@0x{0x80000000 + o:08x} loaded by {refs}")


def table(src, n):
    for i in range(n):
        print(f"  +0x{i * 4:03x} 0x{w32((src & 0x0FFFFFFF) + i * 4):08x}")


HSRC, PSRC, NENT = 0x80028A18, 0x80028BF4, 128  # copied by 0x802eeb2c to 0xFD8010C8/0xFD8012C8


def irqtab():
    """The ROM image of the 3.60 event table, as the boot copy lays it out in IL RAM:
    slot k (code 0x40 + 0x20*k): handler word at HSRC+4k, priority byte at PSRC+k."""
    from collections import Counter
    hs = [w32((HSRC & 0x0FFFFFFF) + 4 * k) for k in range(NENT)]
    common = Counter(hs).most_common(1)[0][0]
    print(f"most common handler (default): 0x{common:08x}  x{hs.count(common)}")
    for k in range(NENT):
        h = hs[k]
        p = data[(PSRC & 0x0FFFFFFF) + k]
        code = 0x40 + 0x20 * k
        src = "ROM" if (HSRC + 4 * k) < PSRC else "overlaps prio bytes"
        mark = "" if h == common else "  <--"
        print(f"  code 0x{code:03x} slot {k:3d}: handler 0x{h:08x} prio 0x{p:02x} ({src}){mark}")
    print("\n== literals pointing inside the IL-RAM tables ==")
    for o in range(0, CODE_END - 3, 4):
        v = w32(o)
        if 0xFD8010C8 < v < 0xFD801348 and v != 0xFD8012C8:
            ls = loaders(o)
            if ls:
                refs = ", ".join(f"0x{0x80000000 + a:08x}(r{n})" for a, n in ls)
                print(f"  0x{v:08x} lit@0x{0x80000000 + o:08x} loaded by {refs}")


if __name__ == "__main__":
    if len(sys.argv) >= 2 and sys.argv[1] == "--irqtab":
        irqtab()
    elif len(sys.argv) >= 4 and sys.argv[1] == "--table":
        table(int(sys.argv[2], 0), int(sys.argv[3], 0))
    elif len(sys.argv) >= 4 and sys.argv[1] == "--dis":
        sh4dis.disasm(int(sys.argv[2], 0), int(sys.argv[3], 0))
    else:
        scan()
