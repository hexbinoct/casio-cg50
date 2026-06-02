#!/usr/bin/env python3
"""
Unpack the fx-CG50 OS from the updater's RCDATA resources.

Discovered by reversing cg50_updater.exe (SetupFile2) in Ghidra:
  FUN_10004580(id) loads RT_RCDATA(0xa) id, then rebuilds a gzip stream and
  calls FUN_100018d0 == a zlib-1.2.3 wrapper:
      inflateInit2_(strm, windowBits=0x1f /*gzip*/, "1.2.3", 0x38)
      inflate(strm, Z_FINISH)
      inflateEnd(strm)

  The stored resource has been tampered so it doesn't look like gzip:
    * the 10-byte gzip header is stripped (restored from DAT_101263a4..),
    * one byte at compressed-stream offset 0x2ff6 is removed and restored
      per image:  0x02 for the OS (3070/3071), 0x1f for the bootloader (3069).

  Reconstructed gzip stream:
      header(10) + res[0:0x2ff6] + flag_byte + res[0x2ff6:]
  inflate (wbits=31) -> plain OS (malloc'd 0xb60000 in the updater).

This script rebuilds and inflates each blob, brute-forcing the header/flag if
the canonical guess fails, and writes the plain images to os/os_image/.
"""
import os
import zlib

BASE = r"F:\ru\myprojects\may\cg50"
RCDATA = os.path.join(BASE, "os", "pe2_rsrc", ".rsrc", "1033", "RCDATA")
OUT = os.path.join(BASE, "os", "os_image")

SPLIT = 0x2ff6  # offset where the updater inserts the flag byte

# id -> (output name, flag byte the updater inserts)
TARGETS = {
    "3070": ("cg50_os_3.80.plain.bin", 0x02),   # fx-CG50 OS  <-- our target
    "3071": ("graph90_os_3.80.plain.bin", 0x02), # Graph 90+E (FR) OS
    "3069": ("bootloader_3.80.plain.bin", 0x1f), # bootloader / preloader
}

# Canonical 10-byte gzip header: magic 1f8b, method 08=deflate, flags 0, mtime 0,
# xfl 0, os 0.  This is what DAT_101263a4.. almost certainly holds.
CANON_HEADER = bytes([0x1F, 0x8B, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00])

# Header candidates to try if canonical fails (vary XFL/OS bytes).
def header_candidates():
    yield CANON_HEADER
    for xfl in (0x00, 0x02, 0x04):
        for osb in (0x00, 0x0b, 0xff):
            yield bytes([0x1F, 0x8B, 0x08, 0x00, 0,0,0,0, xfl, osb])


def try_inflate(stream):
    """Try several wbits; return (plain, wbits) or (None, None)."""
    for wbits in (31, 47, 15, -15):
        try:
            d = zlib.decompressobj(wbits)
            out = d.decompress(stream)
            out += d.flush()
            if len(out) > 0:
                return out, wbits
        except Exception:
            pass
    return None, None


def build(res, header, flag, insert=True):
    if insert:
        return header + res[:SPLIT] + bytes([flag]) + res[SPLIT:]
    # alternative theory: flag *replaces* the byte at SPLIT
    return header + res[:SPLIT] + bytes([flag]) + res[SPLIT + 1:]


def unpack(rid, outname, flag):
    path = os.path.join(RCDATA, rid)
    if not os.path.isfile(path):
        print(f"[{rid}] MISSING: {path}")
        return
    res = open(path, "rb").read()
    print(f"[{rid}] resource {len(res)} bytes, first8={res[:8].hex()}")

    # Strategy 1: canonical/known approach (insert flag byte).
    for insert in (True, False):
        for header in header_candidates():
            stream = build(res, header, flag, insert=insert)
            plain, wbits = try_inflate(stream)
            if plain is not None:
                os.makedirs(OUT, exist_ok=True)
                outpath = os.path.join(OUT, outname)
                open(outpath, "wb").write(plain)
                mode = "insert" if insert else "replace"
                print(f"[{rid}] OK  hdr={header.hex()} flag={flag:#04x} "
                      f"mode={mode} wbits={wbits} -> {len(plain)} bytes")
                print(f"[{rid}]     first16 of plain: {plain[:16].hex()}")
                print(f"[{rid}]     written: {outpath}")
                return
    print(f"[{rid}] FAILED to inflate with all header/flag/mode guesses")


def main():
    print("RCDATA dir:", RCDATA)
    for rid, (outname, flag) in TARGETS.items():
        unpack(rid, outname, flag)
    print("done.")


if __name__ == "__main__":
    main()
