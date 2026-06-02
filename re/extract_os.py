#!/usr/bin/env python3
"""
Extract the plain fx-CG50 OS image (fw4) from the USBPower container.

Format learned empirically:
  - bytes[0x00:0x40] = USBPower container header (plain text 'USBPower,...').
  - bytes[0x40:]     = payload, stored BITWISE-INVERTED.
The raw MSI stream is the whole genuine file bitwise-NOT'd, so in the raw MSI stream
the payload is already plain and the header is inverted. We work from the genuine
(decoded) file and invert the payload to get plain bytes -- and cross-check.
"""
import os, re, math, collections

BASE = r"F:\ru\myprojects\may\cg50\os"
MSI  = os.path.join(BASE, "msi_files")
DEC  = os.path.join(BASE, "decoded")
OUT  = os.path.join(BASE, "os_image")
os.makedirs(OUT, exist_ok=True)

HDR = 0x40
STR_RE = re.compile(rb"[\x20-\x7e]{5,}")
OS_KEYWORDS = [b"CASIO", b"fx-CG", b"Bfile", b"MainMenu", b"SYSTEM", b"System",
               b"Ver", b"OS ", b"Memory", b"error", b"Error", b"flash", b"USB",
               b"Renesas", b"font", b"Font", b"\\\\fls0", b"\\\\crd0"]

def inv(b): return bytes((~x) & 0xFF for x in b)

def entropy(b):
    if not b: return 0.0
    c = collections.Counter(b); n = len(b)
    return -sum((v/n)*math.log2(v/n) for v in c.values())

def hexdump(b, base=0, n=128):
    out=[]
    for r in range(0, min(n,len(b)), 16):
        ch=b[r:r+16]
        out.append(f"  {base+r:08X}: " + " ".join(f"{x:02X}" for x in ch).ljust(48)
                   + "  " + "".join(chr(x) if 32<=x<127 else '.' for x in ch))
    return "\n".join(out)

def kw_hits(b):
    return {k.decode('latin1','replace'): b.count(k) for k in OS_KEYWORDS if b.count(k)}

raw = open(os.path.join(MSI, "ISSetupFile.SetupFile4"), "rb").read()   # payload plain
dec = open(os.path.join(DEC, "fw4_1m8.bin"), "rb").read()              # header plain

print(f"fw4 total size: {len(dec)}  (header 0x40 + payload {len(dec)-HDR})")
print("\nContainer header (plain, from decoded):")
print(hexdump(dec[:HDR], 0, HDR))

# Two candidate plain payloads:
cand = {
    "raw_msi[0x40:]      (payload-plain)": raw[HDR:],
    "invert(decoded[0x40:])":              inv(dec[HDR:]),
}
print("\nWhich orientation yields OS content?")
best = None
for name, p in cand.items():
    h = kw_hits(p)
    nstr = len(STR_RE.findall(p))
    ent = entropy(p[:200000])
    score = sum(h.values())
    print(f"  [{name}]  entropy={ent:.2f}  strings={nstr}  kw_score={score}  hits={h}")
    if best is None or score > best[0]:
        best = (score, name, p)

payload = best[2]
print(f"\nChosen: {best[1]}")
print("\nPayload first 128 bytes:")
print(hexdump(payload, 0, 128))
print("\nFirst 30 ASCII strings (len>=5) in chosen payload:")
seen=0
for m in STR_RE.finditer(payload):
    s=m.group().decode('latin1')
    if len(s)>=5:
        print(f"   @{m.start():#08x}  {s[:72]}")
        seen+=1
        if seen>=30: break

out_path = os.path.join(OUT, "cg50_os_3.80.bin")
open(out_path, "wb").write(payload)
print(f"\nWrote OS image -> {out_path}  ({len(payload)} bytes)")
print("Ghidra: load flat, SH-4A (SuperH4), BIG-ENDIAN, base 0x80000000, mirror 0xA0000000")
