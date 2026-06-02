"""Path 1 prep: identify SetupFile2's PE architecture and stage a .exe copy for Ghidra.

- Reads the PE headers of the InstallShield updater (SetupFile2).
- Reports machine type (x86 32 vs x64), subsystem, entry point, image base.
- Copies it next to itself as `cg50_updater.exe` so Ghidra's PE loader auto-detects it
  (and so the Ghidra project has a sanely-named program).
Run:  python prep_unpacker.py
"""

import os
import shutil
import struct

SRC = r"F:\ru\myprojects\may\cg50\os\msi_files\ISSetupFile.SetupFile2"
DST = r"F:\ru\myprojects\may\cg50\os\msi_files\cg50_updater.exe"

MACHINE = {0x14c: "x86 (32-bit, IMAGE_FILE_MACHINE_I386)",
           0x8664: "x64 (AMD64)",
           0x1c0: "ARM", 0xaa64: "ARM64"}
SUBSYS = {1: "native", 2: "GUI", 3: "console"}


def main():
    with open(SRC, "rb") as f:
        data = f.read()

    if data[:2] != b"MZ":
        raise SystemExit(f"Not an MZ/PE file: starts with {data[:2]!r}")

    e_lfanew = struct.unpack_from("<I", data, 0x3C)[0]
    if data[e_lfanew:e_lfanew + 4] != b"PE\x00\x00":
        raise SystemExit(f"No PE signature at e_lfanew=0x{e_lfanew:x}")

    coff = e_lfanew + 4
    machine, n_sections = struct.unpack_from("<HH", data, coff)
    opt = coff + 20
    magic = struct.unpack_from("<H", data, opt)[0]
    is_pe32_plus = (magic == 0x20b)

    entry = struct.unpack_from("<I", data, opt + 16)[0]
    if is_pe32_plus:
        image_base = struct.unpack_from("<Q", data, opt + 24)[0]
    else:
        image_base = struct.unpack_from("<I", data, opt + 28)[0]
    subsystem = struct.unpack_from("<H", data, opt + (70 if is_pe32_plus else 68))[0]

    print(f"file            : {SRC}")
    print(f"size            : {len(data):,} bytes")
    print(f"machine         : 0x{machine:04x}  {MACHINE.get(machine, '??')}")
    print(f"PE magic        : 0x{magic:04x}  {'PE32+' if is_pe32_plus else 'PE32'}")
    print(f"sections        : {n_sections}")
    print(f"image base      : 0x{image_base:08x}")
    print(f"entry point RVA : 0x{entry:08x}  (va 0x{image_base + entry:08x})")
    print(f"subsystem       : {subsystem}  {SUBSYS.get(subsystem, '??')}")

    # section table starts after the optional header
    opt_size = struct.unpack_from("<H", data, coff + 16)[0]
    sect = opt + opt_size
    print("\nsections:")
    for i in range(n_sections):
        off = sect + i * 40
        name = data[off:off + 8].rstrip(b"\x00").decode("latin1")
        vsize, vaddr, rawsize, rawptr = struct.unpack_from("<IIII", data, off + 8)
        print(f"  {name:<8} va=0x{vaddr:08x} vsize=0x{vsize:07x} "
              f"raw=0x{rawptr:08x} rawsize=0x{rawsize:07x}")

    if not os.path.exists(DST):
        shutil.copy2(SRC, DST)
        print(f"\nstaged Ghidra copy -> {DST}")
    else:
        print(f"\nGhidra copy already present -> {DST}")

    bits = "64-bit" if is_pe32_plus else "32-bit"
    print(f"\n==> In Ghidra, import {os.path.basename(DST)} as Format: "
          f"'Portable Executable (PE)', Language: x86 {bits} (default analyzer is fine).")


if __name__ == "__main__":
    main()
