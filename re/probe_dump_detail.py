#!/usr/bin/env python3
"""Pin down: (a) exact OS version in the physical os.bin CASIOWIN header,
(b) where the divergence from our 3.80 plain image begins, (c) live VRAM
frame inside dram.bin (396x224x16bpp = 177408 B), (d) ilram.bin identity."""
import os, struct, re
ROOT = r"F:\ru\myprojects\may\cg50"
DD   = os.path.join(ROOT, "os", "flash_dump")
PLAIN= os.path.join(ROOT, "os", "os_image", "cg50_os_3.80.plain.bin")

ob = open(os.path.join(DD, "os.bin"), "rb").read()
pb = open(PLAIN, "rb").read()

print("== CASIOWIN headers ==")
for nm, b in (("physical os.bin", ob), ("updater plain", pb)):
    h = b.find(b"CASIOWIN", 0x1F000, 0x21000)
    ver = b[h+0x20:h+0x2c].decode("latin1", "replace") if h>=0 else "?"
    print(f"  {nm:16} CASIOWIN@{hex(h)}  version={ver!r}")

print("\n== divergence region around 0x1ffd0 ==")
n = min(len(ob), len(pb))
fd = next(i for i in range(n) if ob[i]!=pb[i])
print(f"  first diff @ {hex(fd)}")
print(f"  os.bin   [{hex(fd)}]: {ob[fd:fd+24].hex()}")
print(f"  plain    [{hex(fd)}]: {pb[fd:fd+24].hex()}")

# how much of the boot/reset area matches (before CASIOWIN @0x20000)
matchpct = sum(1 for i in range(0x20000) if ob[i]==pb[i]) / 0x20000 * 100
print(f"  boot/reset area [0..0x20000] byte-match: {matchpct:.1f}%")

# overall similarity (full sample)
eq = sum(1 for i in range(0, n, 101) if ob[i]==pb[i])
print(f"  full-image sampled match: {eq/((n//101)+1)*100:.1f}%")

print("\n== VRAM hunt in dram.bin (look for a real rendered frame) ==")
dr = open(os.path.join(DD, "dram.bin"), "rb").read()
FRAME = 396*224*2  # 177408
# scan 4KB-aligned windows for a plausible RGB565 image: many distinct
# 16-bit values, not flat, not high-entropy noise.
import math
def windscore(off):
    seg = dr[off:off+FRAME]
    if len(seg) < FRAME: return None
    # count zero halfwords and distinct halfwords (cheap)
    zeros = seg.count(0)
    return zeros
best = []
for off in range(0, len(dr)-FRAME, 0x1000):
    seg = dr[off:off+0x1000]
    nz = 0x1000 - seg.count(0)
    if nz: best.append((off, nz))
# report the densest non-zero regions (candidate live buffers)
best.sort(key=lambda t: -t[1])
print("  densest non-zero 4KB windows in dram (off, nonzero bytes):")
for off, nz in best[:8]:
    print(f"    {hex(off):>9}  nz={nz}")

print("\n== ilram.bin identity ==")
il = open(os.path.join(DD, "ilram.bin"), "rb").read()
# does this 64KB block appear verbatim inside the OS image? (relocated code)
chunk = il[0x100:0x140]
loc_os = ob.find(chunk)
loc_fl = open(os.path.join(DD,"flash_full.bin"),"rb").read().find(chunk)
print(f"  ilram[0x100:0x140] found in os.bin @ {hex(loc_os) if loc_os>=0 else 'NO'}")
print(f"  ilram strings sample:", [s for s in re.findall(rb'[ -~]{5,}', il)[:6]])
print("DONE.")
