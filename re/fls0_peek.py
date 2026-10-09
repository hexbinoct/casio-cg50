"""Peek at the storage-memory region of a flash dump: the first bytes of each 64 KB block that
holds data between 0xC00000 and the end of the image, plus where FAT-style directory entries
("." / "..") appear. Run: python re/fls0_peek.py [flash.bin]"""
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "os", "flash_dump", "flash_full.bin")
f = open(path, "rb").read()

print("first data block >= 0xC00000:")
for off in range(0xC00000, len(f), 0x10000):
    blk = f[off:off + 0x10000]
    if blk.count(0xFF) != len(blk):
        print(f"  {off:#x}: {blk[:48].hex(' ')}")
        break

print("directory entries ('.' then '..', 32-byte records):")
dot = b".          "
n = 0
for off in range(0xC00000, len(f) - 64, 32):
    if f[off:off + 11] == dot and f[off + 32:off + 43] == b"..         ":
        n += 1
        if n <= 12:
            print(f"  {off:#x}")
print(f"  {n} directories found")

# lowest/highest non-erased byte in [0xC00000, end)
lo = next((i for i in range(0xC00000, len(f)) if f[i] != 0xFF), None)
print(f"lowest non-0xFF byte at/after 0xC00000: {lo:#x}" if lo is not None else "none")
