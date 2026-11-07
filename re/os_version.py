#!/usr/bin/env python3
"""Find where the fx-CG50 OS keeps its version string, in the 3.60 flash dump and in the
3.80 updater's OS image (file offset 0 = 0x80000000 in both).

Prints every "NN.NN.NNNN" (and "NN.NN") string near the start of each image, with offsets,
so we can pick a fixed location that both the emulator and the dumper add-in can read.
"""
import re
import sys

ROOT = r"F:\ru\myprojects\may\cg50\os"
IMAGES = {
    "3.60 dump": ROOT + r"\flash_dump\flash_full.bin",
    "3.80 updater OS": ROOT + r"\os_image\cg50_os_3.80.plain.bin",
}
PAT = re.compile(rb"\d\d\.\d\d\.\d\d\d\d")


def show(name, path, limit):
    try:
        data = open(path, "rb").read()
    except OSError as e:
        print(f"{name}: {e}")
        return
    print(f"\n=== {name}  ({len(data)} bytes) ===")
    n = 0
    for m in PAT.finditer(data):
        off = m.start()
        ctx = data[max(0, off - 16): off + 32]
        asc = "".join(chr(b) if 32 <= b < 127 else "." for b in ctx)
        print(f"  off 0x{off:07x}  {m.group().decode()}   |{asc}|")
        n += 1
        if n >= limit:
            print("  ...")
            break
    # the CASIOWIN header region
    for off in (0x20000, 0x20010, 0x20020, 0x20030):
        row = data[off:off + 16]
        print(f"  0x{off:06x}: " + " ".join(f"{b:02x}" for b in row) + "  |" +
              "".join(chr(b) if 32 <= b < 127 else "." for b in row) + "|")


if __name__ == "__main__":
    limit = int(sys.argv[1]) if len(sys.argv) > 1 else 30
    for k, v in IMAGES.items():
        show(k, v, limit)
