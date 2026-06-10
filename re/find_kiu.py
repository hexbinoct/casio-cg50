#!/usr/bin/env python3
"""Locate the fx-CG50 (OS 3.60) keyboard MATRIX-SCAN hardware accesses in os.bin.

Goal: find which on-chip registers the OS reads/writes to scan the key matrix, so we
can model them in the emulator (emu_go/mmio.go keysc currently stubs them to 0). SH4
loads a 32-bit MMIO address from a literal pool (mov.l @(disp,pc),Rn) then accesses it,
so we find candidate register CONSTANTS embedded in the code as big-endian words and
report where they live + which function/literal-pool they sit in.

Candidate key/port register bases on SH7305 (Prizm/fx-CG50):
  0xA4050000  PORT (PFC / PxDR general I/O — gint scans the CG50 matrix via port regs)
  0xA4080000  KEYSC (legacy SH3/4 key scan controller; stubbed in emu)
  0xA44B0000  KIU_DATA (stubbed in emu)
We scan a generous window so nothing is missed, and also report the literal pools that
sit just after the known scan routine FUN_8011ad0c (0x8011ad0c) and FUN_80295128.
"""
import os, struct

OS = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "os.bin")
img = open(OS, "rb").read()
BASE = 0x80000000  # image rebased here at runtime; file off = vaddr & 0x1FFFFFFF


def vaddr_of(off):
    return BASE + off


# --- 1. find every 32-bit big-endian word that looks like an on-chip key/port reg ----
# On-chip peripherals live in 0xA4000000-0xA4FFFFFF (P2, uncached) and their P0 alias.
def is_candidate(v):
    # key/port register ranges of interest
    if 0xA4050000 <= v <= 0xA405FFFF:  # PORT/PFC
        return "PORT"
    if 0xA4080000 <= v <= 0xA4080FFF:  # KEYSC
        return "KEYSC"
    if 0xA44B0000 <= v <= 0xA44B0FFF:  # KIU
        return "KIU"
    return None


hits = {}  # value -> list of (off, kind)
for off in range(0, len(img) - 3, 2):  # SH4 code/literals are 2-byte aligned
    v = struct.unpack_from(">I", img, off)[0]
    kind = is_candidate(v)
    if kind:
        hits.setdefault(v, []).append((off, kind))

print("=== candidate key/port register CONSTANTS embedded in os.bin ===")
for v in sorted(hits):
    locs = hits[v]
    kind = locs[0][1]
    sample = ", ".join(f"@{vaddr_of(o):#010x}" for o, _ in locs[:8])
    more = f" (+{len(locs)-8} more)" if len(locs) > 8 else ""
    print(f"  {v:#010x}  [{kind}]  x{len(locs):<3} {sample}{more}")

# --- 2. dump the literal pool words near the known scan routines ----------------------
# A scan routine references its MMIO regs from a nearby literal pool. Show words around
# the function so we can eyeball which registers it pulls in.
def dump_pool(name, vaddr, span=0x400):
    off = vaddr & 0x1FFFFFFF
    print(f"\n=== literal-pool scan around {name} ({vaddr:#010x}), +{span:#x} ===")
    for o in range(off, off + span, 4):
        v = struct.unpack_from(">I", img, o)[0]
        # only print words that point at on-chip peripherals or RAM globals (interesting)
        if (0xA4000000 <= v <= 0xA4FFFFFF) or (0xFF000000 <= v <= 0xFFFFFFFF):
            print(f"  @{vaddr_of(o):#010x}: {v:#010x}  <- on-chip peripheral")


dump_pool("FUN_8011ad0c (scan->enqueue)", 0x8011ad0c, 0x600)
dump_pool("FUN_80295128 (other enqueue caller)", 0x80295128, 0x400)
print("\ndone.")
