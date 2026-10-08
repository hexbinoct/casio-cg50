#!/usr/bin/env python3
"""Compare the OS 3.60 event table (handlers 0xFD8010C8, priority bytes 0xFD8012C8) in a real
calculator's IL RAM dump (os/flash_dump/ilram.bin, 0xFD800000..) with the ROM source the boot
copies it from (0x80028A18 / 0x80028BF4 in os.bin, copy loop at 0x802eeb2c).
Also prints the dump's VBR save slot 0xFD8024A0 (written by 0x800208aa)."""
import struct
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
os_bin = (ROOT / "os" / "flash_dump" / "os.bin").read_bytes()
il = (ROOT / "os" / "flash_dump" / "ilram.bin").read_bytes()
HSRC, PSRC, N = 0x28A18, 0x28BF4, 128

same_h = same_p = 0
for k in range(N):
    rom_h = struct.unpack_from(">I", os_bin, HSRC + 4 * k)[0]
    rom_p = os_bin[PSRC + k]
    il_h = struct.unpack_from(">I", il, 0x10C8 + 4 * k)[0]
    il_p = il[0x12C8 + k]
    same_h += rom_h == il_h
    same_p += rom_p == il_p
    if rom_h != il_h or rom_p != il_p:
        print(f"  code 0x{0x40 + 0x20 * k:03x}: ROM 0x{rom_h:08x}/{rom_p:02x}  dump 0x{il_h:08x}/{il_p:02x}")
print(f"handlers identical: {same_h}/{N}, priority bytes identical: {same_p}/{N}")
# the boot-area dispatcher's table (0x80001300 -> 0xFD8004D0, found in the 3.80 updater study)
boot_same = sum(os_bin[0x1300 + i] == il[0x4D0 + i] for i in range(119 * 4))
print(f"boot-area table 0x80001300 vs dump 0xFD8004D0: {boot_same}/{119 * 4} bytes equal")
print(f"0xFD8024A0 in dump =0x{struct.unpack_from('>I', il, 0x24A0)[0]:08x}")
