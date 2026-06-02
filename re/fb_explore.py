#!/usr/bin/env python3
"""Explore the emulator framebuffer dump (emu_go/fb_raw.bin, 256KB from 0x8c000000)
to find the correct stride / orientation / pixel format. The render is 88% nonzero
but appears diagonally skewed -> wrong row width. Try a range and write PNGs.
Requires Pillow (pip install pillow). Falls back to PPM if Pillow missing."""
import os, struct

RAW = os.path.join(os.path.dirname(__file__), "..", "emu_go", "fb_raw.bin")
OUT = os.path.join(os.path.dirname(__file__), "..", "emu_go")
data = open(RAW, "rb").read()

try:
    from PIL import Image
    HAVE_PIL = True
except ImportError:
    HAVE_PIL = False

def rgb565(p):
    r = ((p >> 11) & 0x1F) << 3
    g = ((p >> 5) & 0x3F) << 2
    b = (p & 0x1F) << 3
    return (r, g, b)

def render(width, height, off, name, rotate=False):
    px = []
    for y in range(height):
        for x in range(width):
            idx = off + (y * width + x) * 2
            if idx + 1 < len(data):
                p = (data[idx] << 8) | data[idx + 1]
            else:
                p = 0
            px.append(rgb565(p))
    if HAVE_PIL:
        img = Image.new("RGB", (width, height))
        img.putdata(px)
        if rotate:
            img = img.rotate(-90, expand=True)
        img.save(os.path.join(OUT, name))
        print("wrote", name, f"({width}x{height}{' rot' if rotate else ''})")

# fx-CG50 panel is 396x224. Try the native size and a few nearby strides, plus the
# rotated (portrait 224x396) interpretation in case the OS renders column-major.
for w in (392, 394, 395, 396, 397, 398, 400, 384, 320):
    render(w, 224, 0, f"explore_w{w}.png")
render(224, 396, 0, "explore_rot224x396.png")
render(396, 224, 0, "explore_396x224.png")
# also dump a hex peek of the first row to inspect the pattern
print("first 32 px (BE565):", " ".join(f"{(data[i*2]<<8)|data[i*2+1]:04x}" for i in range(32)))
