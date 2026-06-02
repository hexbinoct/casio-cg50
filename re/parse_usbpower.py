#!/usr/bin/env python3
"""
Empirical analysis of the Casio fx-CG50 'USBPower' OS-update container.

We have two orientations of each firmware segment:
  - os/msi_files/ISSetupFile.SetupFileN  -> raw bytes as stored in the MSI
  - os/decoded/fwN_*.bin                 -> whole-file bitwise-NOT of the above
The decoded one shows a clean "USBPower" header. Hypothesis: the container framing
is plain but the OS *data* payload is inverted, so the OTHER orientation (raw MSI)
should contain plain OS strings/code. This script measures that and dumps structure.
"""
import os, re, collections

BASE = r"F:\ru\myprojects\may\cg50\os"
MSI  = os.path.join(BASE, "msi_files")
DEC  = os.path.join(BASE, "decoded")

# map raw MSI stream -> decoded filename
PAIRS = [
    ("ISSetupFile.SetupFile3", "fw3_770k.bin"),
    ("ISSetupFile.SetupFile4", "fw4_1m8.bin"),
    ("ISSetupFile.SetupFile5", "fw5_83k.bin"),
    ("ISSetupFile.SetupFile6", "fw6_329k.bin"),
    ("ISSetupFile.SetupFile7", "fw7_406k.bin"),
]

STR_RE = re.compile(rb"[\x20-\x7e]{6,}")

def inv(b: bytes) -> bytes:
    return bytes((~x) & 0xFF for x in b)

def hexdump(b: bytes, base=0, n=64):
    out = []
    for r in range(0, min(n, len(b)), 16):
        chunk = b[r:r+16]
        hx = " ".join(f"{x:02X}" for x in chunk)
        tx = "".join(chr(x) if 32 <= x < 127 else "." for x in chunk)
        out.append(f"  {base+r:06X}: {hx:<48}  {tx}")
    return "\n".join(out)

def str_stats(b: bytes):
    matches = STR_RE.findall(b)
    total = sum(len(m) for m in matches)
    return len(matches), total, matches

def looks_addr(v):
    # SuperH virtual addresses the OS would use
    return (0x80000000 <= v <= 0x8FFFFFFF or 0xA0000000 <= v <= 0xAFFFFFFF
            or 0x00000000 <= v <= 0x01000000 or 0x88000000 <= v <= 0x8C100000)

def be32(b, o):
    return int.from_bytes(b[o:o+4], "big")

def scan_addr_candidates(b, upto=256):
    hits = []
    for o in range(0, min(upto, len(b)-4)):
        v = be32(b, o)
        if looks_addr(v) and v not in (0, 0xFFFFFFFF):
            hits.append((o, v))
    return hits

print("="*78)
for raw_name, dec_name in PAIRS:
    raw = open(os.path.join(MSI, raw_name), "rb").read()
    dec = open(os.path.join(DEC, dec_name), "rb").read()
    # 'dec' starts with clean 'USBPower'; 'raw' is its bitwise NOT.
    r_n, r_tot, r_m = str_stats(raw)
    d_n, d_tot, d_m = str_stats(dec)
    plain, plain_name, other = (raw, raw_name, "raw-MSI") if r_tot > d_tot else (dec, dec_name, "decoded")
    pm = r_m if r_tot > d_tot else d_m

    print(f"\n### {dec_name}  ({len(dec)} bytes)")
    print(f"  ascii-string bytes:  raw-MSI={r_tot:<8}  decoded={d_tot:<8}  "
          f"=> data is PLAIN in: {other}")
    # header of the 'USBPower' (framing) orientation = decoded
    print("  -- 'USBPower' framing header (decoded orientation), first 64 bytes:")
    print(hexdump(dec, 0, 64))
    # show where strings live in the plain-data orientation
    print(f"  -- first 25 ASCII strings in the PLAIN-data orientation ({other}):")
    seen = 0
    for m in STR_RE.finditer(plain):
        s = m.group().decode("latin1")
        if len(s) >= 6:
            print(f"       @{m.start():#08x}  {s[:70]}")
            seen += 1
            if seen >= 25:
                break
    # address-looking BE32 values in the header region of both orientations
    print("  -- BE32 address-like values in first 64 bytes:")
    print(f"       decoded: {[ (hex(o),hex(v)) for o,v in scan_addr_candidates(dec,64) ]}")
    print(f"       raw    : {[ (hex(o),hex(v)) for o,v in scan_addr_candidates(raw,64) ]}")

print("\n" + "="*78)
print("DONE")
