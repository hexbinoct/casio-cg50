#!/usr/bin/env python3
"""Dump the live interrupt handler table from the idle snapshot to find the REAL
INTEVT codes. The 3.60 dispatcher (0x80021502) reads:
    handler = *(0xFD8010C8 + ((INTEVT-0x40)>>3))     [4-byte entries]
    prio    =  (0xFD8012C8 + ((INTEVT-0x40)>>5)) byte
So a valid INTEVT is a multiple of 0x20; table byte-offset k -> INTEVT = 0x40 + k*8.
For each populated entry pointing at real OS code, print INTEVT, handler, prio, and
whether the handler references the timer flag reg 0xA4610088 (=> the timer ISR)."""
import os, sys, pickle
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from cpu import CPU
from memory import Memory
from mmio import MMIOBus

HERE = os.path.dirname(__file__)
SNAP = os.path.join(HERE, "idle_state.pkl")
HTAB = 0xFD8010C8
PTAB = 0xFD8012C8


def handler_touches(mem, addr, target=0xA4610088, span=0x400):
    """crude: scan the handler's first `span` bytes for the 32-bit constant `target`
    in its literal pool (SH4 loads MMIO addrs from nearby literals)."""
    tb = target.to_bytes(4, "big")
    try:
        blob = bytes(mem.read(addr + i, 1) for i in range(0, span))
    except Exception:
        return False
    return tb in blob


def main():
    cpu, mem, mmio = pickle.load(open(SNAP, "rb"))
    print(f"snapshot idle PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x}\n")
    print(f"{'INTEVT':>7}  {'k':>4}  {'handler':>10}  {'prio':>4}  notes")
    found = []
    for k in range(0, 0x200, 4):                  # 128 long entries
        handler = mem.r32(HTAB + k)
        intevt = 0x40 + k * 8
        if 0x80000000 <= handler < 0x80C00000:
            prio = mem.r8(PTAB + (k >> 2))         # (INTEVT-0x40)>>5 == k>>2
            timer = handler_touches(mem, handler)
            note = "  <-- references 0xA4610088 (TIMER ISR)" if timer else ""
            print(f"  0x{intevt:03x}  {k:4d}  0x{handler:08x}  {prio:4d}{note}")
            found.append((intevt, handler, prio, timer))
    print(f"\n{len(found)} populated handlers.")
    timers = [f for f in found if f[3]]
    if timers:
        print("timer-flag handlers (candidate timer INTEVTs):")
        for intevt, h, p, _ in timers:
            print(f"  INTEVT 0x{intevt:03x} -> 0x{h:08x} (prio {p})")
    else:
        print("no handler literal-pool referenced 0xA4610088 directly "
              "(may load it indirectly) — will identify by injection test.")


if __name__ == "__main__":
    main()
