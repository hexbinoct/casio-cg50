#!/usr/bin/env python3
"""Compare the boot code of OS 3.80 (updater image) and OS 3.60 (physical dump).

The early Ghidra study traced the reset sequence in 3.80; this checks whether 3.60's boot
code is the same, and disassembles the 3.60 routines so the wiki's boot page can cite them.

Run:  python re/boot_compare.py
"""
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "re"))
import sh4dis  # noqa: E402

IMG380 = ROOT / "os" / "os_image" / "cg50_os_3.80.plain.bin"
IMG360 = ROOT / "os" / "flash_dump" / "os.bin"

# The routines the 3.80 study named (start addresses; end = a generous window).
ROUTINES = {
    "reset_entry": (0x80000000, 0x80000100),
    "boot_bsc_sdram_init": (0xA000063C, 0xA0000670),
    "boot_pfc_wdt_init": (0xA0000670, 0xA000069A),
    "boot_cpg_pll_init": (0xA000069A, 0xA00006CC),
    "boot_os_startup": (0xA00006CC, 0xA0000760),
}


def main():
    a = IMG380.read_bytes()
    b = IMG360.read_bytes()
    print(f"3.80 image {len(a):#x} bytes, 3.60 image {len(b):#x} bytes")

    # Where do the two images first differ, and how much of the first 128 KB matches?
    first = next((i for i in range(min(len(a), len(b))) if a[i] != b[i]), None)
    print(f"first differing byte: {first:#x}" if first is not None else "identical prefix")
    same = sum(1 for i in range(0x20000) if a[i] == b[i])
    print(f"first 0x20000 bytes: {same}/{0x20000} equal")

    for name, (lo, hi) in ROUTINES.items():
        o0, o1 = lo & 0x0FFFFFFF, hi & 0x0FFFFFFF
        eq = a[o0:o1] == b[o0:o1]
        print(f"\n== {name} {lo:#010x}..{hi:#010x}: {'IDENTICAL' if eq else 'DIFFERENT'} in 3.60")
        if not eq:
            diffs = [hex(lo + i) for i in range(o1 - o0) if a[o0 + i] != b[o0 + i]]
            print("   differing bytes at:", ", ".join(diffs[:16]), "..." if len(diffs) > 16 else "")

    sh4dis.set_image(str(IMG360))
    print("\n================ 3.60 disassembly ================")
    for name, (lo, hi) in ROUTINES.items():
        print()
        sh4dis.disasm(lo, hi, name)


if __name__ == "__main__":
    main()
