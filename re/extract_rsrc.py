#!/usr/bin/env python3
"""Extract SetupFile2's .rsrc (10.5MB) via 7-Zip and find the embedded OS blob(s)."""
import os, subprocess, re

MSI = r"F:\ru\myprojects\may\cg50\os\msi_files"
OUT = r"F:\ru\myprojects\may\cg50\os\pe2_rsrc"
SEVENZIP = r"C:\Program Files\7-Zip\7z.exe"
F = os.path.join(MSI, "ISSetupFile.SetupFile2")
os.makedirs(OUT, exist_ok=True)

r = subprocess.run([SEVENZIP, "x", F, f"-o{OUT}", "-y"], capture_output=True, text=True, timeout=300)
print(r.stdout.splitlines()[-3] if r.stdout else r.stderr[-300:])

# collect all extracted files
files=[]
for root,_,fs in os.walk(OUT):
    for fn in fs:
        p=os.path.join(root,fn)
        files.append((os.path.getsize(p), p))
files.sort(reverse=True)

MAGIC_PLAIN=b"USBPower"; MAGIC_INV=bytes((~x)&0xFF for x in MAGIC_PLAIN)
APP=[b"RUNMAT",b"STAT",b"GRAPH",b"eAct",b"System",b"Bfile",b"fx-CG",b"CASIO",b"fls0",b"PHYSIUM",b"Geometry",b"Equation",b"CONICS",b"PROGRAM"]

print("\nTop 12 extracted resources by size:")
for sz,p in files[:12]:
    rel=p[len(OUT):]
    with open(p,"rb") as fh: head=fh.read(64); fh.seek(0); full=fh.read()
    has_plain = MAGIC_PLAIN in full
    has_inv   = MAGIC_INV in full
    apps=[a.decode('latin1') for a in APP if a in full]
    appsi=[a.decode('latin1')+"(inv)" for a in APP if bytes((~x)&0xFF for x in a) in full]
    hx=" ".join(f"{x:02X}" for x in head[:16])
    tx="".join(chr(x) if 32<=x<127 else '.' for x in head[:16])
    print(f"  {sz:>9}  {rel}")
    print(f"             head: {hx}  '{tx}'  USBPower:{'P' if has_plain else ''}{'I' if has_inv else ''}  apps:{apps+appsi}")
