#!/usr/bin/env python3
"""The OS is alive (interrupts flowing) but camped in flash/storage code, never
rendering. Characterize that loop: run (timer on, full peripheral set) to `start`,
snapshot the 'alive' state for fast iteration, then over a window record:
 - flash COMMAND writes (writes into the 0..0x08000000 flash window = NOR commands),
 - flash read addresses (which regions it reads),
 - MMIO touched, and the hottest loop PCs.
This tells us whether the blocker is the NOR flash command/status interface.

Usage: python emu/probe_storage.py [start_ins] [window]
"""
import os, sys, pickle, collections
try:
    sys.stdout.reconfigure(line_buffering=True)
except Exception:
    pass
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from cpu import CPU
from memory import Memory
from mmio import MMIOBus, upgrade_bus
try:
    import sh4dis
    def disasm(op, pc): return sh4dis.decode(op, pc)
except Exception:
    def disasm(op, pc): return f"0x{op:04x}"

HERE = os.path.dirname(__file__)
SNAP = os.path.join(HERE, "idle_state.pkl")
ALIVE = os.path.join(HERE, "alive_state.pkl")


def main():
    start = int(sys.argv[1], 0) if len(sys.argv) > 1 else 12_000_000
    window = int(sys.argv[2], 0) if len(sys.argv) > 2 else 200_000

    if os.path.exists(ALIVE):
        print(f"loading alive snapshot {ALIVE}")
        cpu, mem, mmio = pickle.load(open(ALIVE, "rb"))
        upgrade_bus(mmio, cpu)
        start = 0
    else:
        cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
        upgrade_bus(mmio, cpu)
    mmio.timer_period = 30_000
    mmio.timer_next = cpu.cycles

    step = cpu.step
    # advance to the alive/camped state
    for i in range(start):
        mmio.tick(cpu)
        try: step()
        except Exception as e:
            print(f"FAULT pre-capture @0x{cpu.pc:08x}: {e}"); return
    if not os.path.exists(ALIVE):
        try:
            pickle.dump((cpu, mem, mmio), open(ALIVE, "wb"))
            print(f"saved alive snapshot -> {ALIVE}")
        except Exception as e:
            print(f"(alive snapshot save failed: {e})")
    print(f"camped state: PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x} cycles={cpu.cycles:,}")

    # capture window
    fl_writes = collections.Counter()   # (va, val)
    fl_read_pages = collections.Counter()
    mmio_hits = collections.Counter()
    pcs = collections.Counter()
    orig_w = mem.write
    orig_r = mem.read
    def w(va, size, val):
        phys = va & 0x1FFFFFFF
        if phys < 0x08000000:
            fl_writes[(phys, val)] += 1
        return orig_w(va, size, val)
    def r(va, size):
        phys = va & 0x1FFFFFFF
        if phys < 0x08000000:
            fl_read_pages[phys >> 12] += 1     # 4KB page
        return orig_r(va, size)
    mem.write, mem.read = w, r
    om_r = mmio.read
    def mr(va, size):
        nm, _ = mmio._find(va); mmio_hits[(nm.name if nm else "???", va)] += 1
        return om_r(va, size)
    mmio.read = mr

    for i in range(window):
        pcs[cpu.pc] += 1
        mmio.tick(cpu)
        try: step()
        except Exception as e:
            print(f"FAULT @0x{cpu.pc:08x}: {e}"); break

    print(f"\nhottest loop PCs:")
    for pc, c in pcs.most_common(10):
        print(f"  0x{pc:08x} x{c:<6} {disasm(mem.r16(pc), pc)}")
    print(f"\nFLASH COMMAND WRITES (phys addr, value) — NOR command sequences:")
    if fl_writes:
        for (pa, val), c in fl_writes.most_common(15):
            print(f"  0x{pa:08x} <- 0x{val:x}  x{c}")
    else:
        print("  (none — driver is not issuing flash command writes in this window)")
    print(f"\nflash read pages (4KB page -> count), top 12:")
    for pg, c in fl_read_pages.most_common(12):
        print(f"  0x{pg<<12:08x} x{c}")
    print(f"\nMMIO touched:")
    for (nm, va), c in mmio_hits.most_common(12):
        print(f"  {nm:10s} 0x{va:08x} x{c}")


if __name__ == "__main__":
    main()
