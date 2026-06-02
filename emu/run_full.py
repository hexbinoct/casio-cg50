#!/usr/bin/env python3
"""Differential boot test: does backing the flash with the REAL fls0 filesystem
let the OS get past the flash-driver wall (NOTES.md "THE WALL NOW")?

Both images are the SAME OS version (3.60), so version is not a confound:
  A) os.bin       (12 MB) — OS region only; flash tail reads 0xFF (no fls0)
  B) flash_full.bin (16MB) — full NOR flash incl. the real fls0 filesystem tail

We boot each from reset, run up to max_ins, and classify where it ends up:
flash-driver spin (on-chip RAM 0xFD8xxxxx) = still stuck; an OS-code idle band
(0x80xxxxxx) = booted further. We also watch the DMAC for a VRAM->LCD frame push
(ch SAR in DRAM, DAR=0x14000000) — a version-independent "rendered a frame" signal.

Usage: python emu/run_full.py [max_ins]
"""
import os, sys
try:
    sys.stdout.reconfigure(line_buffering=True)   # stream checkpoints to a redirected file
except Exception:
    pass
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from memory import Memory
from mmio import MMIOBus
from cpu import CPU

HERE = os.path.dirname(__file__)
OSBIN   = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")
FULLBIN = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")


def region(pc):
    if 0xFD800000 <= pc < 0xFD810000: return "onchip-RAM (flash drv?)"
    if 0x8C000000 <= pc < 0x8D000000: return "DRAM"
    if 0xA0000000 <= pc < 0xA0100000: return "bootROM (uncached)"
    if 0x80000000 <= pc < 0x80020000: return "boot stub"
    if 0x80020000 <= pc < 0x80800000: return "OS code"
    if 0x80800000 <= pc <= 0x80C00000: return "high OS / drivers"
    return f"other(0x{pc:08x})"


def dmac_frame_pushes(mmio):
    """count distinct VRAM->LCD programmings seen (DAR == 0x14000000)."""
    d = next(r for r in mmio.regions if r.name == "DMAC")
    return d


def boot(label, path, max_ins):
    image = open(path, "rb").read()
    mmio = MMIOBus(log=False)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000
    dmac = next(r for r in mmio.regions if r.name == "DMAC")

    print(f"\n=== boot {label}: {os.path.basename(path)} ({len(image):,} B), "
          f"up to {max_ins:,} instr ===")
    # Fast hot loop: only sample/instrument every SAMPLE instructions to keep the
    # pure-Python interpreter fast enough for tens of millions of steps.
    SAMPLE = 1024
    seen_dar = set()
    fault = None
    checkpoint = max(SAMPLE, (max_ins // 8) // SAMPLE * SAMPLE)
    pushes = 0
    # idle-loop detection: keep last N PC samples; if their span is tiny AND in OS
    # code for a sustained stretch, the OS has reached a steady main/idle loop.
    ring = []
    RING = 64                      # 64 samples * 1024 = ~65k instr window
    idle_since = None
    step = cpu.step
    for i in range(max_ins):
        try:
            step()
        except Exception as e:
            fault = f"{type(e).__name__} @0x{cpu.pc:08x}: {e}"
            break
        if i & (SAMPLE - 1):
            continue
        pc = cpu.pc
        # frame-push signal: any DMAC channel programmed to push to LCD area-5
        for choff in (0x00, 0x10, 0x20, 0x30):
            if dmac.regs.get(choff + 0x04, 0) == 0x14000000:
                sar = dmac.regs.get(choff + 0x00, 0)
                key = (choff, sar)
                if key not in seen_dar:
                    seen_dar.add(key)
                    pushes += 1
                    print(f"  [{i:>10,}] VRAM->LCD DMA: ch@+0x{choff:02x} "
                          f"SAR=0x{sar:08x} DAR=0x14000000")
        ring.append(pc)
        if len(ring) > RING:
            ring.pop(0)
            span = max(ring) - min(ring)
            in_os = all(0x80020000 <= p < 0x80800000 for p in ring)
            if in_os and span < 0x8000:
                if idle_since is None:
                    idle_since = i
                    print(f"  [{i:>10,}] *** entered tight OS-code loop "
                          f"band 0x{min(ring):08x}..0x{max(ring):08x} (span 0x{span:x})")
            else:
                idle_since = None
        if i and i % checkpoint == 0:
            print(f"  [{i:>10,}] PC=0x{cpu.pc:08x}  {region(cpu.pc)}  irqs={cpu.irq_count}")
    print(f"  --- ended after {cpu.cycles:,} instr "
          f"({'FAULT: '+fault if fault else 'reached max_ins'})")
    print(f"  final PC=0x{cpu.pc:08x}  {region(cpu.pc)}")
    if idle_since is not None:
        print(f"  *** STABLE OS idle loop since instr {idle_since:,} "
              f"(band 0x{min(ring):08x}..0x{max(ring):08x}) <- candidate main loop")
    print(f"  irqs accepted={cpu.irq_count}  frame pushes={pushes}")
    return cpu, mem, mmio


def main():
    max_ins = int(sys.argv[1], 0) if len(sys.argv) > 1 else 15_000_000
    which   = sys.argv[2].upper() if len(sys.argv) > 2 else "BOTH"   # A | B | BOTH
    if which in ("A", "BOTH"):
        boot("A (os.bin, NO fls0)", OSBIN, max_ins)
    if which in ("B", "BOTH"):
        boot("B (flash_full, real fls0)", FULLBIN, max_ins)
    print("\nIf B reaches an OS-code idle band while A stays in on-chip-RAM/flash "
          "driver (or faults), the fls0 backing unblocked the flash wall.")


if __name__ == "__main__":
    main()
