#!/usr/bin/env python3
"""Scan the LIVE dram.bin (real fx-CG50 DRAM snapshot, taken while the calc was
running) for the rendered framebuffer: the densest 396x224x16bpp window. The real
calc was showing its UI, so a ~near-full nonzero window = the actual VRAM. This
pins the framebuffer address to compare against the emulator (which never fills VRAM).

dram.bin maps to physical 0x0C000000 / cached 0x8C000000 (DRAM base)."""
import os, struct

DRAM = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "dram.bin")
DRAM_PHYS = 0x0C000000
DRAM_CACHED = 0x8C000000
W, H, BPP = 396, 224, 2
FB = W * H * BPP  # 177408 bytes

d = open(DRAM, "rb").read()
print(f"dram.bin = {len(d):#x} bytes ({len(d)} = {len(d)//1024//1024}MB)")

# A rendered UI frame is mostly white with COLORED content (icons/text) — rank by
# count of pixels that are neither pure black nor pure white, plus pixel variety.
step = 0x800
results = []
for off in range(0, len(d) - FB, step):
    colored = 0
    vals = set()
    for k in range(0, FB, 8 * BPP):
        p = (d[off + k] << 8) | d[off + k + 1]
        vals.add(p)
        if p != 0x0000 and p != 0xFFFF:
            colored += 1
    results.append((colored, len(vals), off))

samples = FB // (8 * BPP)
results.sort(reverse=True)
print(f"\nsampled {samples} px/window; top 14 by COLORED-pixel count (= rendered content):")
for colored, nv, off in results[:14]:
    print(f"  off={off:#08x}  cached=0x{DRAM_CACHED+off:08x}  colored={colored}/{samples}"
          f" ({100*colored//samples}%)  distinct_vals={nv}")

def dump(off, label):
    print(f"\n{label} @cached=0x{DRAM_CACHED+off:08x}: rows 0,80,112 (every 24th px):")
    for row in (0, 80, 112):
        base = off + row * W * BPP
        px = [struct.unpack('>H', d[base+i*2:base+i*2+2])[0] for i in range(0, W, 24)]
        print(f"  r{row:3d}: " + " ".join(f"{p:04x}" for p in px))

dump(results[0][2], "top colored window")
dump(0x28800, "emulator SAR region 0x28800")
