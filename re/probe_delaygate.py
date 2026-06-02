#!/usr/bin/env python3
"""Resolve the MMIO pointer used by the ETMU busy-delay FUN_803742f8 and the
shell init pointers around it, reading the 3.60 os.bin directly.

vaddr -> file offset = vaddr - 0x80000000 (image is rebased to 0x80000000).
"""
import struct, os

OS = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "os.bin")
BASE = 0x80000000

with open(OS, "rb") as f:
    img = f.read()

def be32(vaddr):
    off = vaddr - BASE
    return struct.unpack(">I", img[off:off+4])[0]

print(f"image size = {len(img):#x} ({len(img)} bytes)")

# The delay loop literal: r4 = *(0x80374380) is the MMIO counter pointer.
ptr = be32(0x80374380)
print(f"\nFUN_803742f8 counter pointer @0x80374380 -> {ptr:#010x}")
print("  (expected ETMU down-counter 0xA44D00D8 if our model is right)")

# Shell init function-pointer literals of interest (from FUN_802aea26 decompile).
# These tell us what runs right before / after the 20x delay.
for name, lit in [
    ("PTR_FUN_802aedc4 (pre-delay, returns key/state)", 0x802aedc4),
    ("PTR_FUN_802aedc8 (=FUN_80318d9c, the 20x delay)", 0x802aedc8),
    ("PTR_FUN_802aedcc (byte store =3)",                0x802aedcc),
    ("PTR_FUN_802aedd0 (called right after delay)",     0x802aedd0),
    ("PTR_FUN_80318ff8 (=FUN_803742f8 inner)",          0x80318ff8),
]:
    print(f"  *{lit:#010x}  {name:50s} -> {be32(lit):#010x}")

def be16(vaddr):
    off = vaddr - BASE
    return struct.unpack(">H", img[off:off+2])[0]

print("\n--- ADC (battery?) read path: FUN_801de54a / FUN_801de62a / FUN_801de60a ---")
for name, lit in [
    ("DAT_801de684 (ADC control reg ptr)", 0x801de684),
    ("DAT_801de688 (ready mask)",          0x801de688),
    ("DAT_801de694 (ADC data reg ptr)",    0x801de694),
    ("DAT_801de93c (state struct ptr; +8 read by FUN_801de858)", 0x801de93c),
    ("DAT_802af658 (ADC-ready status byte ptr, FUN_802af470)", 0x802af658),
]:
    print(f"  *{lit:#010x}  {name:52s} -> {be32(lit):#010x}")

print("\n--- FUN_801e6bbc bucket thresholds (shorts) ---")
for name, lit in [
    ("DAT_801e6c4e", 0x801e6c4e), ("DAT_801e6c50", 0x801e6c50),
    ("DAT_801e6c52", 0x801e6c52), ("DAT_801e6c54", 0x801e6c54),
    ("DAT_801e6c56", 0x801e6c56), ("DAT_801e6c58", 0x801e6c58),
    ("DAT_801e6c5a", 0x801e6c5a),
]:
    print(f"  *{lit:#010x}  {name:14s} -> 0x{be16(lit):04x} ({be16(lit)})")

print("\n--- FUN_80365238 fls0-init call targets (where boot hangs, between FUN_80365598 and fls0_open) ---")
for name, lit in [
    ("PTR_FUN_803652c0", 0x803652c0), ("PTR_FUN_80365280", 0x80365280),
    ("PTR_FUN_803652cc", 0x803652cc), ("PTR_FUN_803652d0", 0x803652d0),
    ("PTR_FUN_803652c8", 0x803652c8), ("PTR_FUN_803654c8", 0x803654c8),
    ("PTR_FUN_803654e0", 0x803654e0), ("PTR_FUN_803654e4", 0x803654e4),
    ("PTR_FUN_803654fc", 0x803654fc), ("PTR_FUN_80365500", 0x80365500),
    ("DAT_803654d8", 0x803654d8), ("DAT_803654dc", 0x803654dc),
    ("DAT_803654e8", 0x803654e8),
]:
    print(f"  *{lit:#010x}  {name:18s} -> {be32(lit):#010x}")

print("\n--- FUN_801de9ca flash-signature verify (loop @0x80365418) ---")
print(f"  flash@0x300 (image bytes 0x300..0x304) = {img[0x300:0x304].hex()}")
ptr = be32(0x806827a4)  # DAT_806827a4 holds a pointer? or is the data inline
print(f"  DAT_806827a4 raw 4 bytes                = {img[0x6827a4:0x6827a8].hex()}  (as ptr {ptr:#010x})")
print(f"  *0xfd8018d4 model-code field is set at runtime; loop alt-exit wants 0xca02")
