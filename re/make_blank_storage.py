#!/usr/bin/env python3
"""Make a copy of a flash dump with the storage memory (fls0) erased, to see what the OS does
with a calculator whose files were never in the dump ("a dump without my files").

fls0 runs from phys 0x00C80000 to the end of the 32 MB flash (tools/flash_dump/src/main.c); the OS
code and its system area below that are kept.

  python re/make_blank_storage.py [in.bin] [out.bin]
  default: os/flash_dump/flash_full.bin -> os/flash_dump/blank_storage.bin (git-ignored)
"""
import sys

ROOT = r"F:\ru\myprojects\may\cg50\os\flash_dump"
FLS0 = 0x00C80000

src = sys.argv[1] if len(sys.argv) > 1 else ROOT + r"\flash_full.bin"
dst = sys.argv[2] if len(sys.argv) > 2 else ROOT + r"\blank_storage.bin"
b = bytearray(open(src, "rb").read())
used = sum(1 for i in range(FLS0, len(b), 0x1000) if b[i:i + 0x1000] != b"\xff" * 0x1000)
b[FLS0:] = b"\xff" * (len(b) - FLS0)
open(dst, "wb").write(b)
print(f"{src}: {len(b) >> 20} MB, {used} non-blank 4 KB pages in fls0 erased -> {dst}")
