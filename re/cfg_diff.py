#!/usr/bin/env python3
"""
Config diff (cont.18): compare the display-format config that the Norm renderer
FUN_800f790e reads, between OUR emulator state (emu_go/fmt_snapshot.bin) and the
REAL physical device RAM dump (os/flash_dump/dram.bin + ilram.bin).

FUN_800f790e does:  r13 = DAT_800f79f8 (a fixed pointer in flash);  mode = *(byte)(r13+6)
and dispatches Norm/Fix/Sci/Eng on that mode. The pointer constant is identical on both
(it lives in flash), so any difference is purely in the RAM CONTENTS the pointer targets.
If our config/mode differs from the real device's, that is why the OS faithfully renders
"0" for a correct value.

Run:  python re/cfg_diff.py
"""
import os, struct

HERE = os.path.dirname(__file__)
FD = os.path.join(HERE, "..", "os", "flash_dump")
SNAP = os.path.join(HERE, "..", "emu_go", "fmt_snapshot.bin")

DRAM_BASE, DRAM_SIZE = 0x0C000000, 0x00800000
ILRAM_BASE, ILRAM_SIZE = 0xFD800000, 0x00010000
OCRAM_BASE, OCRAM_SIZE = 0xFE200000, 0x00200000

flash = open(os.path.join(FD, "flash_full.bin"), "rb").read()
real_dram = open(os.path.join(FD, "dram.bin"), "rb").read()
real_ilram = open(os.path.join(FD, "ilram.bin"), "rb").read()

# our snapshot: 36 regs then dram, ilram, ocram
blob = open(SNAP, "rb").read()
off = 36 * 4
our_dram = blob[off:off + DRAM_SIZE]; off += DRAM_SIZE
our_ilram = blob[off:off + ILRAM_SIZE]; off += ILRAM_SIZE
our_ocram = blob[off:off + OCRAM_SIZE]; off += OCRAM_SIZE


def be32_flash(phys):
    return int.from_bytes(flash[phys:phys + 4], "big")


def read(buf_dram, buf_ilram, buf_ocram, va, n):
    """Read n bytes at virtual addr from a (dram,ilram,ocram[,flash]) buffer set."""
    p = va & 0x1FFFFFFF
    if DRAM_BASE <= p < DRAM_BASE + DRAM_SIZE:
        o = p - DRAM_BASE
        return buf_dram[o:o + n] if buf_dram is not None else None
    if ILRAM_BASE <= va < ILRAM_BASE + ILRAM_SIZE:
        o = va - ILRAM_BASE
        return buf_ilram[o:o + n] if buf_ilram is not None else None
    if OCRAM_BASE <= va < OCRAM_BASE + OCRAM_SIZE:
        o = va - OCRAM_BASE
        return buf_ocram[o:o + n] if buf_ocram is not None else None
    if p < len(flash):
        return flash[p:p + n]
    return None


def region_name(va):
    p = va & 0x1FFFFFFF
    if DRAM_BASE <= p < DRAM_BASE + DRAM_SIZE: return "DRAM (have real dump)"
    if ILRAM_BASE <= va < ILRAM_BASE + ILRAM_SIZE: return "ILRAM (have real dump)"
    if OCRAM_BASE <= va < OCRAM_BASE + OCRAM_SIZE: return "OCRAM (NO real dump!)"
    if p < len(flash): return "FLASH (identical)"
    return "unmapped"


def show(label, b):
    if b is None:
        print(f"  {label}: <not in a region we have>")
        return
    hexs = " ".join(f"{x:02x}" for x in b)
    asc = "".join(chr(x) if 0x20 <= x < 0x7f else "." for x in b)
    print(f"  {label}: {hexs}  |{asc}|")


def diff_struct(name, va, n):
    print(f"\n=== {name} @ 0x{va:08x}  [{region_name(va)}] ===")
    ours = read(our_dram, our_ilram, our_ocram, va, n)
    real = read(real_dram, real_ilram, None, va, n)
    show("ours", ours)
    show("real", real)
    if ours and real:
        d = [i for i in range(n) if ours[i] != real[i]]
        if d:
            print(f"  DIFFER at byte offsets: {d}")
        else:
            print("  IDENTICAL")
    return ours, real


def main():
    print("Resolving fixed flash constants used by the Norm renderer FUN_800f790e:")
    cfg_ptr = be32_flash(0x000f79f8)   # DAT_800f79f8
    a08 = be32_flash(0x000f7a08)       # DAT_800f7a08
    norm = be32_flash(0x000f7c7c)      # FUN_800f7c7c
    print(f"  DAT_800f79f8 (config base) = 0x{cfg_ptr:08x}  [{region_name(cfg_ptr)}]")
    print(f"  DAT_800f7a08               = 0x{a08:08x}  [{region_name(a08)}]")
    print(f"  FUN_800f7c7c (Norm render) = 0x{norm:08x}")

    # config struct: mode is at +6; dump a window around it
    co, cr = diff_struct("config struct (DAT_800f79f8 target)", cfg_ptr, 0x20)
    if co is not None:
        print(f"  -> OUR  mode byte *(cfg+6) = 0x{co[6]:02x} ({co[6]})")
    if cr is not None:
        print(f"  -> REAL mode byte *(cfg+6) = 0x{cr[6]:02x} ({cr[6]})")

    diff_struct("DAT_800f7a08 target", a08, 0x10)

    print("\nFUN_8004c21a literal pool (value-decode helpers):")
    for a in range(0x0004c368, 0x0004c394, 4):
        print(f"  PTR @0x8004{a & 0xffff:04x} -> 0x{be32_flash(a):08x}")
    print("\nFUN_8004c69c literal pool (digit-emit helpers):")
    for a in range(0x0004c80c, 0x0004c850, 4):
        print(f"  PTR @0x8004{a & 0xffff:04x} -> 0x{be32_flash(a):08x}")
    print("\nFUN_8004b2b0 literal pool (rounding helpers):")
    for a in range(0x0004b490, 0x0004b4a4, 4):
        print(f"  PTR @0x8004{a & 0xffff:04x} -> 0x{be32_flash(a):08x}")
    print("\nFUN_8005dc06 literal pool (rounding-leaf sub-calls):")
    for a in range(0x0005de1c, 0x0005de30, 4):
        print(f"  PTR @0x8005{a & 0xffff:04x} -> 0x{be32_flash(a):08x}")
    print("\nFUN_80079dbc literal pool (round-core sub-calls):")
    for a in range(0x00079f60, 0x00079f88, 4):
        print(f"  PTR @0x8007{a & 0xffff:04x} -> 0x{be32_flash(a):08x}")
    print("\nFUN_80072fc8 HW-peripheral MMIO regs (operand/ctrl/result):")
    for a, lbl in ((0x00073000, "operand A ptr"), (0x00073004, "operand B ptr"),
                   (0x00073008, "control/cmd ptr (.w)"), (0x0007300c, "RESULT ptr")):
        print(f"  DAT @0x8007{a & 0xffff:04x} -> 0x{be32_flash(a):08x}  ({lbl})")


if __name__ == "__main__":
    main()
