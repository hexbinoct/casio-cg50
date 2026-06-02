#!/usr/bin/env python3
"""
Locate the main fx-CG50 OS image. The 5 USBPower segments turned out to be add-ins
(Geometry, Physium, Picture Plot, 3D Graph, Prob Sim). The OS must live in SetupFile1
(64KB) or SetupFile2 (12MB PE) -- probably embedded in the PE. Scan every MSI stream
for the USBPower magic (plain & inverted) at ANY offset, and for OS/app markers.
"""
import os, re

MSI = r"F:\ru\myprojects\may\cg50\os\msi_files"

MAGIC_PLAIN = b"USBPower"
MAGIC_INV   = bytes((~x)&0xFF for x in MAGIC_PLAIN)   # AA AC BD AF 90 88 9A 8D

# core OS apps / markers that would NOT be in a single add-in but ARE in the OS
MARKERS = [b"RUNMAT", b"Run", b"Matrix", b"GRAPH", b"STAT", b"eActivity", b"eACT",
           b"SYSTEM", b"System", b"Bfile", b"fx-CG", b"CASIO", b"fls0", b"MainMenu",
           b"OS ", b"Renesas", b"SH730", b"PHYSIUM", b"Geometry"]

def all_offsets(hay, needle, limit=12):
    offs=[]; i=hay.find(needle)
    while i!=-1 and len(offs)<limit:
        offs.append(i); i=hay.find(needle, i+1)
    return offs, hay.count(needle)

def hexdump(b, base, n=96):
    out=[]
    for r in range(0,min(n,len(b)),16):
        ch=b[r:r+16]
        out.append(f"    {base+r:08X}: "+" ".join(f"{x:02X}" for x in ch).ljust(48)
                   +"  "+"".join(chr(x) if 32<=x<127 else '.' for x in ch))
    return "\n".join(out)

for name in sorted(os.listdir(MSI)):
    p=os.path.join(MSI,name); b=open(p,"rb").read()
    print(f"\n=== {name}  ({len(b)} bytes) ===")
    po,pc = all_offsets(b, MAGIC_PLAIN)
    io,ic = all_offsets(b, MAGIC_INV)
    print(f"  'USBPower' plain:    count={pc}  offsets={[hex(o) for o in po]}")
    print(f"  'USBPower' inverted: count={ic}  offsets={[hex(o) for o in io]}")
    mk={}
    for m in MARKERS:
        c=b.count(m)
        if c: mk[m.decode('latin1')]=c
        ci=b.count(bytes((~x)&0xFF for x in m))   # inverted form too
        if ci: mk[m.decode('latin1')+"(inv)"]=ci
    print(f"  markers: {mk}")
    # if there's a 2nd USBPower (beyond offset 0), show context -> likely embedded OS
    interesting = [o for o in (po+io) if o>0x100]
    for o in interesting[:3]:
        print(f"  -- context @ {hex(o)} (raw bytes):")
        print(hexdump(b[o:o+96], o, 96))
