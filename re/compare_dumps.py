#!/usr/bin/env python3
"""Compare two fx-CG50 flash dumps region by region.

  python re/compare_dumps.py NEW.bin [OLD.bin]     # OLD defaults to os/flash_dump/flash_full_32mb.bin

Prints size, OS version (header at 0x20000), SHA-256, and for each region the number of
differing bytes and the 64 KB blocks they fall in.
"""
import hashlib
import os
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
REGIONS = [
    ("boot", 0x000000, 0x020000),
    ("OS", 0x020000, 0x0A00000),
    ("system/main mem", 0x0A00000, 0x0C80000),
    ("fls0 low half", 0x0C80000, 0x1000000),
    ("fls0 upper 16MB", 0x1000000, 0x2000000),
]


def osver(b):
    if b[0x20000:0x20008] != b"CASIOWIN":
        return "no CASIOWIN header"
    return b[0x20020:0x2002A].decode("ascii", "replace")


def main():
    new = sys.argv[1]
    old = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "os", "flash_dump", "flash_full_32mb.bin")
    a, b = open(new, "rb").read(), open(old, "rb").read()
    for name, d in (("new", a), ("old", b)):
        print(f"{name}: {len(d)} bytes  OS {osver(d)}  sha256 {hashlib.sha256(d).hexdigest()[:16]}…")
    n = min(len(a), len(b))
    for name, lo, hi in REGIONS:
        if lo >= n:
            continue
        hi = min(hi, n)
        diff = sum(1 for i in range(lo, hi) if a[i] != b[i]) if a[lo:hi] != b[lo:hi] else 0
        blocks = [k for k in range(lo, hi, 0x10000) if a[k:k + 0x10000] != b[k:k + 0x10000]]
        erased = sum(1 for k in range(lo, hi, 0x10000) if a[k:k + 0x10000] == b"\xff" * 0x10000)
        span = f"  blocks {', '.join(hex(k) for k in blocks[:8])}{' …' if len(blocks) > 8 else ''}" if blocks else ""
        print(f"  {name:16} {hex(lo):>9}-{hex(hi):9} differ {diff:9} bytes in {len(blocks):3} x 64K"
              f"  (new: {erased} erased blocks){span}")


if __name__ == "__main__":
    main()
