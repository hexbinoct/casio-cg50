#!/usr/bin/env python3
"""Deliver the periodic timer interrupt (INTEVT 0x188) to the idled OS and watch
what happens. Boots flash_full (3.60) to the idle state (~14M instr, polling
PERIPH_IRQ 0xA4610088), snapshots that state to disk (so re-runs are instant),
then enables the MMIO timer source and runs, observing:
  - the first IRQ acceptances (vector PC / SPC / SSR / INTEVT) + a short ISR trace,
  - whether the OS leaves its idle band (= the tick advanced the event loop),
  - frame pushes and VRAM richness, then dumps the framebuffer to frame_live.bmp.

Usage: python emu/run_live.py [run_ins] [timer_period] [boot_ins]
"""
import os, sys, pickle
try:
    sys.stdout.reconfigure(line_buffering=True)
except Exception:
    pass
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from memory import Memory
from mmio import MMIOBus, upgrade_bus
from cpu import CPU
from capture_frame import write_bmp, rgb565, W, H

HERE = os.path.dirname(__file__)
FULLBIN = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")
SNAP = os.path.join(HERE, "idle_state.pkl")
OUT = os.path.join(HERE, "frame_live.bmp")

IDLE_LO, IDLE_HI = 0x801de560, 0x802af4a0   # measured idle band


def boot_to_idle(boot_ins):
    image = open(FULLBIN, "rb").read()
    mmio = MMIOBus(log=False)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000
    mmio.cpu = cpu
    print(f"booting flash_full to idle ({boot_ins:,} instr)...")
    step = cpu.step
    for i in range(boot_ins):
        try:
            step()
        except Exception as e:
            print(f"  FAULT during boot @0x{cpu.pc:08x}: {type(e).__name__}: {e}")
            sys.exit(1)
    print(f"  reached idle: PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x}")
    return cpu, mem, mmio


def main():
    run_ins  = int(sys.argv[1], 0) if len(sys.argv) > 1 else 3_000_000
    period   = int(sys.argv[2], 0) if len(sys.argv) > 2 else 100_000
    boot_ins = int(sys.argv[3], 0) if len(sys.argv) > 3 else 14_500_000

    if os.path.exists(SNAP):
        print(f"loading idle snapshot {SNAP}")
        cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
        upgrade_bus(mmio, cpu)
    else:
        cpu, mem, mmio = boot_to_idle(boot_ins)
        try:
            pickle.dump((cpu, mem, mmio), open(SNAP, "wb"))
            print(f"  saved idle snapshot -> {SNAP}")
        except Exception as e:
            print(f"  (snapshot save failed: {e})")

    dmac = next(r for r in mmio.regions if r.name == "DMAC")
    print(f"\nstate at idle: PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x} VBR=0x{cpu.vbr:08x} "
          f"cycles={cpu.cycles:,}")

    # enable the timer interrupt source
    mmio.timer_period = period
    mmio.timer_next = cpu.cycles
    print(f"enabling timer IRQ: INTEVT 0x{mmio.TIMER_INTEVT:x} every {period:,} instr\n")

    seen_dar = set()
    pushes = 0
    log_until = 0
    left_idle_at = None
    irq_seen = 0
    step = cpu.step
    base_cycles = cpu.cycles
    for i in range(run_ins):
        mmio.tick(cpu)
        before = cpu.irq_count
        # trace the first 3 ISR entries in detail
        if cpu.irq_count != before:
            pass
        if log_until and cpu.cycles < log_until:
            mmio.log = True
        elif mmio.log:
            mmio.log = False
        try:
            step()
        except Exception as e:
            print(f"  FAULT @0x{cpu.pc:08x} after +{cpu.cycles-base_cycles:,}: "
                  f"{type(e).__name__}: {e}  (ticks={mmio.timer_ticks} irqs={cpu.irq_count})")
            break
        if cpu.irq_count > irq_seen:
            irq_seen = cpu.irq_count
            if irq_seen <= 3:
                print(f"  IRQ #{irq_seen} accepted @+{cpu.cycles-base_cycles:,}: "
                      f"vectored to 0x{cpu.pc:08x} (SPC=0x{cpu.spc:08x} "
                      f"SSR=0x{cpu.ssr:08x} INTEVT=0x{mem.r32(0xFF000028):x})")
                log_until = cpu.cycles + 120     # short ISR trace
        # detect leaving the idle band
        if left_idle_at is None and not (IDLE_LO <= cpu.pc <= IDLE_HI) \
                and 0x80020000 <= cpu.pc < 0x80800000:
            left_idle_at = cpu.cycles
            print(f"  >>> OS LEFT idle band @+{cpu.cycles-base_cycles:,}: "
                  f"now at 0x{cpu.pc:08x} (irqs={cpu.irq_count})")
        # frame push detection
        if (i & 0x3FF) == 0:
            for choff in (0x00, 0x10, 0x20, 0x30):
                if dmac.regs.get(choff + 0x04, 0) == 0x14000000:
                    sar = dmac.regs.get(choff + 0x00, 0)
                    if (choff, sar) not in seen_dar:
                        seen_dar.add((choff, sar))
                        pushes += 1
                        print(f"  [+{cpu.cycles-base_cycles:>9,}] VRAM->LCD push "
                              f"ch@+0x{choff:02x} SAR=0x{sar:08x}")
        # periodic progress checkpoint
        if i and (i % 2_000_000) == 0:
            sar = next((dmac.regs.get(c, 0) for c in (0x20, 0, 0x10, 0x30)
                        if dmac.regs.get(c + 4, 0) == 0x14000000), 0x0C000000)
            vram = 0x8C000000 | ((sar or 0x0C000000) & 0x1FFFFFFF)
            nz = sum(1 for k in range(0, W*H, 8) if mem.r16(vram + k*2))
            print(f"  [+{i:>9,}] PC=0x{cpu.pc:08x} ticks={mmio.timer_ticks} "
                  f"irqs={cpu.irq_count} vram_nz~{nz*8}")

    print(f"\n  ran +{cpu.cycles-base_cycles:,} instr; timer ticks={mmio.timer_ticks} "
          f"irqs accepted={cpu.irq_count} frame pushes={pushes}")
    print(f"  final PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x}")

    # dump the framebuffer (VRAM = DRAM @ 0x0C000000 region; last SAR or default)
    sar = 0
    for choff in (0x20, 0x00, 0x10, 0x30):
        if dmac.regs.get(choff + 0x04, 0) == 0x14000000:
            sar = dmac.regs.get(choff, 0); break
    vram = 0x8C000000 | ((sar or 0x0C000000) & 0x1FFFFFFF)
    rgb = []
    nz = 0
    for k in range(W * H):
        px = mem.r16(vram + k * 2)
        if px: nz += 1
        rgb.append(rgb565(px))
    write_bmp(OUT, W, H, rgb)
    print(f"  wrote {OUT} ({nz}/{W*H} non-zero px) VRAM=0x{vram:08x}")


if __name__ == "__main__":
    main()
