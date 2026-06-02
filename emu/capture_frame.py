#!/usr/bin/env python3
"""
Boot the OS in the emulator until Bdisp_PutDisp_DD (the VRAM->LCD frame push) is
called, then snapshot the framebuffer the OS handed to the DMAC and write it to a
BMP — our first rendered fx-CG50 frame.

The display is 396x224, 16-bit RGB565. Bdisp_PutDisp_DD programs DMAC channel 2:
SAR (0xFE008020) = VRAM phys source, DAR = LCD, DMATCR = count. We read SAR after
the DMA is set up and decode that memory as RGB565.
"""
import os
import struct
import sys

sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))

from memory import Memory
from mmio import MMIOBus
from cpu import CPU

IMG = os.path.join(os.path.dirname(__file__), "..", "os", "os_image", "cg50_os_3.80.plain.bin")
OUT = os.path.join(os.path.dirname(__file__), "frame.bmp")
W, H = 396, 224
PUTDISP = 0x80055260


def write_bmp(path, w, h, rgb):
    """rgb: list of (r,g,b) row-major top-to-bottom. Writes 24-bit BMP."""
    row_pad = (-(w * 3)) % 4
    pixarray = bytearray()
    for y in range(h - 1, -1, -1):          # BMP rows are bottom-up
        for x in range(w):
            r, g, b = rgb[y * w + x]
            pixarray += bytes((b, g, r))
        pixarray += b"\x00" * row_pad
    size = 54 + len(pixarray)
    hdr = b"BM" + struct.pack("<IHHI", size, 0, 0, 54)
    dib = struct.pack("<IiiHHIIiiII", 40, w, h, 1, 24, 0, len(pixarray), 2835, 2835, 0, 0)
    open(path, "wb").write(hdr + dib + bytes(pixarray))


def rgb565(px):
    r = (px >> 11) & 0x1F
    g = (px >> 5) & 0x3F
    b = px & 0x1F
    return (r << 3) | (r >> 2), (g << 2) | (g >> 4), (b << 3) | (b >> 2)


def main():
    max_ins = int(sys.argv[1], 0) if len(sys.argv) > 1 else 15_000_000
    image = open(IMG, "rb").read()
    mmio = MMIOBus(log=False)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000
    print("booting to first frame push...")

    dmac = next(r for r in mmio.regions if r.name == "DMAC")
    pushes = 0
    best_nz = 0
    for i in range(max_ins):
        if cpu.pc == PUTDISP:
            pushes += 1
            # sample VRAM richness at this push
            sar = dmac.regs.get(0x20, 0) or 0x0C000000
            vram = 0x8C000000 | (sar & 0x1FFFFFFF)
            nz = sum(1 for k in range(0, W * H, 64) if mem.r16(vram + k * 2))
            if nz > best_nz:
                best_nz = nz
                print(f"  push #{pushes} @{cpu.cycles}: ~{nz} non-zero (sampled), SAR=0x{sar:08x}")
        cpu.step()
    print(f"  total frame pushes: {pushes}")

    sar = dmac.regs.get(0x20, 0)
    dar = dmac.regs.get(0x24, 0)
    tcr = dmac.regs.get(0x28, 0)
    print(f"  DMAC ch2: SAR=0x{sar:08x} DAR=0x{dar:08x} DMATCR=0x{tcr:08x}")

    # VRAM virtual address (read via cached DRAM window)
    vram = 0x8C000000 | (sar & 0x1FFFFFFF) if sar else 0x8C000000
    print(f"  reading VRAM @ 0x{vram:08x}")
    rgb = []
    nonzero = 0
    for i in range(W * H):
        px = mem.r16(vram + i * 2)
        if px:
            nonzero += 1
        rgb.append(rgb565(px))
    write_bmp(OUT, W, H, rgb)
    print(f"  wrote {OUT}  ({nonzero}/{W*H} non-zero pixels)")


if __name__ == "__main__":
    main()
