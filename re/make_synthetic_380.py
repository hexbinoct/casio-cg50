#!/usr/bin/env python3
"""Build a stand-in for a 3.80 calculator's flash dump, to see what the emulator does with an
OS other than 3.60 (we have no real 3.80 dump).

  base   = our 16 MB 3.60 dump (boot code, system area, the start of fls0)
  OS     = the 3.80 updater's plain OS image, written over the 3.60 OS from offset 0
  tail   = whatever the 3.60 OS had past the end of the 3.80 image is erased (0xFF),
           up to the end of the 3.60 OS (found by scanning back for the last non-0xFF byte
           before the system area)

It is only an approximation of what an updater writes. Output (git-ignored):
  os/flash_dump/synthetic_380.bin
"""
import hashlib

ROOT = r"F:\ru\myprojects\may\cg50\os"
D360 = ROOT + r"\flash_dump\flash_full.bin"
OS380 = ROOT + r"\os_image\cg50_os_3.80.plain.bin"
OUT = ROOT + r"\flash_dump\synthetic_380.bin"

a = bytearray(open(D360, "rb").read())
b = open(OS380, "rb").read()
print(f"3.60 dump {len(a):#x} bytes, 3.80 OS image {len(b):#x} bytes")

# How do the two differ, 128 KB block by block, over the 3.80 image's extent?
blk = 0x20000
same = [i for i in range(0, len(b), blk) if a[i:i + blk] == b[i:i + blk]]
print("identical 128 KB blocks:", ", ".join(f"{i:#x}" for i in same) or "none")
FF = b"\xff"
print(f"3.80 image first 0x20000 all-FF? {b[:0x20000] == FF * 0x20000}  "
      f"all-00? {b[:0x20000] == bytes(0x20000)}")

# Where does the 3.60 OS end? Scan 64 KB blocks from the 3.80 end for erased blocks.
print("3.60 dump, 64 KB blocks from 0xB00000 to 0xD00000 (F = all 0xFF):")
row = ""
for off in range(0xB00000, 0xD00000, 0x10000):
    row += "F" if a[off:off + 0x10000] == b"\xff" * 0x10000 else "#"
print("  " + row)

end360 = 0xB60000
while end360 < 0xC00000 and a[end360:end360 + 0x10000] != b"\xff" * 0x10000:
    end360 += 0x10000
print(f"3.60 OS data runs to about {end360:#x}")

out = bytearray(a)
# Keep the dump's boot area: the 3.80 image's is the same code but with the per-device product
# ID at 0x1FFD0 erased, and gint reads an all-0xFF ID as "fx-CG Manager" (RAM at 0x88000000),
# so gint add-ins crashed on the first stand-in. A real calculator keeps its ID across updates.
diff = [i for i in range(0x20000) if a[i] != b[i]]
print(f"boot area: {len(diff)} bytes differ" + (f" ({diff[0]:#x}..{diff[-1]:#x})" if diff else ""))
out[0x20000:len(b)] = b[0x20000:]
for off in range(len(b), end360):
    out[off] = 0xFF
print("version at 0x20020:", bytes(out[0x20020:0x2002A]).decode())
open(OUT, "wb").write(out)
print(f"wrote {OUT}  sha256 {hashlib.sha256(out).hexdigest()[:16]}")
