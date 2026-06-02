#!/usr/bin/env python3
"""Characterize the inflated OS image: entropy, ASCII strings, structure."""
import os, math, re, collections

OUT = r"F:\ru\myprojects\may\cg50\os\os_image"
F = os.path.join(OUT, "cg50_os_3.80.plain.bin")

data = open(F, "rb").read()
print(f"file: {F}\nsize: {len(data)} (0x{len(data):x})")

def entropy(b):
    if not b: return 0.0
    c = collections.Counter(b)
    n = len(b)
    return -sum((v/n)*math.log2(v/n) for v in c.values())

print(f"\nwhole-file entropy: {entropy(data):.3f} bits/byte")
print("entropy by 1MB chunk:")
for i in range(0, len(data), 1<<20):
    chunk = data[i:i+(1<<20)]
    print(f"  0x{i:08x}: {entropy(chunk):.3f}")

# byte at each position mod 4 — test the 'every 4th byte = 1f' hunch
print("\nbyte-value distribution by (offset % 4), top 3 each, first 64KB:")
for m in range(4):
    c = collections.Counter(data[i] for i in range(m, min(len(data),1<<16), 4))
    top = c.most_common(3)
    print(f"  off%4=={m}: " + ", ".join(f"{b:#04x}:{n}" for b,n in top))

# ASCII strings (>=5 printable)
print("\nfirst 40 ASCII strings (len>=5):")
strings = re.findall(rb"[\x20-\x7e]{5,}", data)
for s in strings[:40]:
    print("   ", s.decode("ascii", "replace"))

# look for known Casio / version markers anywhere
print("\nmarker search:")
for needle in (b"CASIO", b"CG50", b"CY-", b"Bfile", b"VER", b"GETKEY",
               b"fx-CG", b"OS", b"Ver", b"3.80", b"\x80\x00\x00\x00"):
    idx = data.find(needle)
    print(f"  {needle!r:20} -> {'0x%08x'%idx if idx>=0 else 'not found'}")

print(f"\nfirst 64 bytes hex:\n{data[:64].hex()}")
