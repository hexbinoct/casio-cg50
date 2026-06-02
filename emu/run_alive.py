#!/usr/bin/env python3
"""Run from the 'alive' snapshot (OS booted, interrupts live, alive_state.pkl) for a
long burst to see whether more RUNTIME alone advances it to a rendered frame, or it's
genuinely stuck. Checkpoints PC/irqs/vram and dumps any frame whose VRAM gets rich.

Usage: python emu/run_alive.py [run_ins] [timer_period]
"""
import os, sys, pickle
try:
    sys.stdout.reconfigure(line_buffering=True)
except Exception:
    pass
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from mmio import upgrade_bus
from capture_frame import write_bmp, rgb565, W, H

HERE = os.path.dirname(__file__)
ALIVE = os.path.join(HERE, "alive_state.pkl")
OUT = os.path.join(HERE, "frame_alive.bmp")


def vram_nz(mem, sar):
    vram = 0x8C000000 | ((sar or 0x0C000000) & 0x1FFFFFFF)
    return vram, sum(1 for k in range(0, W*H, 8) if mem.r16(vram + k*2))


def main():
    run_ins = int(sys.argv[1], 0) if len(sys.argv) > 1 else 60_000_000
    period  = int(sys.argv[2], 0) if len(sys.argv) > 2 else 30_000

    cpu, mem, mmio = pickle.load(open(ALIVE, "rb"))
    upgrade_bus(mmio, cpu)
    mmio.timer_period = period
    mmio.timer_next = cpu.cycles
    dmac = next(r for r in mmio.regions if r.name == "DMAC")
    base = cpu.cycles
    print(f"alive @cycles={cpu.cycles:,} PC=0x{cpu.pc:08x}; running +{run_ins:,} (tick {period:,})")

    step = cpu.step
    best_nz = 0
    pushes = set()
    for i in range(run_ins):
        mmio.tick(cpu)
        try:
            step()
        except Exception as e:
            print(f"  FAULT @0x{cpu.pc:08x} after +{cpu.cycles-base:,}: {type(e).__name__}: {e}")
            break
        if (i & 0xFFF) == 0:
            for choff in (0x00, 0x10, 0x20, 0x30):
                if dmac.regs.get(choff+4, 0) == 0x14000000:
                    sar = dmac.regs.get(choff, 0)
                    if (choff, sar) not in pushes:
                        pushes.add((choff, sar))
                        _, nz = vram_nz(mem, sar)
                        print(f"  [+{cpu.cycles-base:>10,}] push ch+0x{choff:02x} SAR=0x{sar:08x} vram_nz~{nz*8}")
        if i and (i % 5_000_000) == 0:
            sar = next((dmac.regs.get(c,0) for c in (0x20,0,0x10,0x30)
                        if dmac.regs.get(c+4,0)==0x14000000), 0x0C000000)
            v, nz = vram_nz(mem, sar)
            best_nz = max(best_nz, nz)
            print(f"  [+{i:>10,}] PC=0x{cpu.pc:08x} irqs={cpu.irq_count} vram_nz~{nz*8} (best {best_nz*8})")

    sar = next((dmac.regs.get(c,0) for c in (0x20,0,0x10,0x30)
                if dmac.regs.get(c+4,0)==0x14000000), 0x0C000000)
    vram, nz = vram_nz(mem, sar)
    rgb = [rgb565(mem.r16(vram + k*2)) for k in range(W*H)]
    full_nz = sum(1 for k in range(W*H) if mem.r16(vram + k*2))
    write_bmp(OUT, W, H, rgb)
    print(f"\n  final PC=0x{cpu.pc:08x} irqs={cpu.irq_count}; wrote {OUT} "
          f"({full_nz}/{W*H} non-zero px) VRAM=0x{vram:08x}")


if __name__ == "__main__":
    main()
