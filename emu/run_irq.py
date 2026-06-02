#!/usr/bin/env python3
"""
Boot with periodic timer-interrupt injection, to drive the OS past its idle wait
into rendering the menu. Injects INTEVT 0x188 (the timer ISR @0x80002C8C from the
handler table) every `period` instructions; tracks IRQ acceptance, frame pushes,
and VRAM richness, then dumps the final framebuffer to frame.bmp.

Usage: python run_irq.py [max_instructions] [irq_period]
"""
import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))

from memory import Memory
from mmio import MMIOBus
from cpu import CPU
from capture_frame import write_bmp, rgb565, W, H

IMG = os.path.join(os.path.dirname(__file__), "..", "os", "os_image", "cg50_os_3.80.plain.bin")
OUT = os.path.join(os.path.dirname(__file__), "frame.bmp")
PUTDISP = 0x80055260
TIMER_INTEVT = 0x188
TIMER_LEVEL = 4


def main():
    max_ins = int(sys.argv[1], 0) if len(sys.argv) > 1 else 25_000_000
    period = int(sys.argv[2], 0) if len(sys.argv) > 2 else 30_000

    image = open(IMG, "rb").read()
    mmio = MMIOBus(log=False)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000
    dmac = next(r for r in mmio.regions if r.name == "DMAC")

    # Only inject AFTER boot has set up the interrupt handler table (0xFD8004D0)
    # and reached its idle/main-loop state — injecting earlier vectors into a
    # half-built table and crashes. First frame push is at ~12.8M instructions.
    START_INJECT = int(sys.argv[3], 0) if len(sys.argv) > 3 else 13_000_000

    print(f"booting; inject timer IRQ 0x{TIMER_INTEVT:x} every {period} instr after "
          f"{START_INJECT} instructions...")
    pushes = 0
    best_nz = 0
    last_sar = 0x0C000000
    traced = 0
    for i in range(max_ins):
        if i >= START_INJECT and i % period == 0:
            cpu.raise_irq(TIMER_INTEVT, TIMER_LEVEL)
        before_irq = cpu.irq_count
        if cpu.pc == PUTDISP:
            pushes += 1
            sar = dmac.regs.get(0x20, 0) or 0x0C000000
            last_sar = sar
            vram = 0x8C000000 | (sar & 0x1FFFFFFF)
            nz = sum(1 for k in range(0, W * H, 16) if mem.r16(vram + k * 2))
            if nz > best_nz:
                best_nz = nz
                print(f"  push #{pushes} @{cpu.cycles}: ~{nz*16} non-zero px (sampled), "
                      f"SAR=0x{sar:08x}  irqs={cpu.irq_count}")
        try:
            cpu.step()
        except Exception as e:
            print(f"  fault @0x{cpu.pc:08x} after {cpu.cycles}: {type(e).__name__}: {e}")
            break
        if cpu.irq_count > before_irq and traced < 4:
            traced += 1
            print(f"  IRQ accepted #{cpu.irq_count} @{cpu.cycles}: vectored to "
                  f"0x{cpu.pc:08x} (VBR=0x{cpu.vbr:08x} SPC=0x{cpu.spc:08x} SSR=0x{cpu.ssr:08x})")

    print(f"  total pushes={pushes}  irqs accepted={cpu.irq_count}  final PC=0x{cpu.pc:08x}")

    # dump the richest-looking frame (the last SAR)
    vram = 0x8C000000 | (last_sar & 0x1FFFFFFF)
    rgb = []
    nonzero = 0
    for k in range(W * H):
        px = mem.r16(vram + k * 2)
        if px:
            nonzero += 1
        rgb.append(rgb565(px))
    write_bmp(OUT, W, H, rgb)
    print(f"  wrote {OUT}  ({nonzero}/{W*H} non-zero pixels)  VRAM=0x{vram:08x}")


if __name__ == "__main__":
    main()
