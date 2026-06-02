#!/usr/bin/env python3
"""
Dissect SetupFile2 (12MB x86 PE) to find the embedded fx-CG50 OS image.
- Parse PE section table -> detect overlay (bytes after last raw section).
- Use 7-Zip to dump PE resources, list the biggest ones.
- Map where OS markers (fls0, System, STAT, app names) cluster in the file.
"""
import os, struct, subprocess, collections, re

MSI = r"F:\ru\myprojects\may\cg50\os\msi_files"
OUT = r"F:\ru\myprojects\may\cg50\os\pe2_dump"
SEVENZIP = r"C:\Program Files\7-Zip\7z.exe"
F = os.path.join(MSI, "ISSetupFile.SetupFile2")
os.makedirs(OUT, exist_ok=True)

b = open(F, "rb").read()
print(f"SetupFile2 size = {len(b)}")

# --- minimal PE parse ---
e_lfanew = struct.unpack_from("<I", b, 0x3C)[0]
assert b[e_lfanew:e_lfanew+4] == b"PE\0\0", "not a PE"
coff = e_lfanew + 4
num_sections = struct.unpack_from("<H", b, coff+2)[0]
opt_size = struct.unpack_from("<H", b, coff+16)[0]
opt_off = coff + 20
magic = struct.unpack_from("<H", b, opt_off)[0]
print(f"PE: sections={num_sections} optmagic={magic:#06x} ({'PE32+' if magic==0x20b else 'PE32'})")
sec_off = opt_off + opt_size
max_end = 0
print("Sections:")
for i in range(num_sections):
    o = sec_off + i*40
    name = b[o:o+8].rstrip(b"\0").decode("latin1","replace")
    vsize, vaddr, rawsize, rawptr = struct.unpack_from("<IIII", b, o+8)
    end = rawptr + rawsize
    max_end = max(max_end, end)
    print(f"  {name:<8} vaddr={vaddr:#010x} vsize={vsize:#010x} rawptr={rawptr:#010x} rawsize={rawsize:#010x} end={end:#x}")
overlay = len(b) - max_end
print(f"End of last section = {max_end:#x};  OVERLAY = {overlay} bytes ({overlay/1e6:.2f} MB)")
if overlay > 0x10000:
    ov = b[max_end:]
    open(os.path.join(OUT, "overlay.bin"), "wb").write(ov)
    print(f"  wrote overlay.bin ({len(ov)} bytes), first bytes: {ov[:16].hex()}  '{''.join(chr(x) if 32<=x<127 else '.' for x in ov[:16])}'")

# --- marker clustering across whole file ---
print("\nMarker positions (first few) across SetupFile2:")
for m in (b"\\\\fls0", b"fls0", b"System", b"STATGRPH", b"STAT", b"@RUNMAT", b"RUNMAT", b"eActivity", b"CASIO"):
    idxs=[]; i=b.find(m)
    while i!=-1 and len(idxs)<8:
        idxs.append(i); i=b.find(m,i+1)
    if idxs: print(f"  {m.decode('latin1'):<10} -> {[hex(x) for x in idxs]}")

# --- 7z resource dump ---
print("\n7-Zip resource listing (top 15 by size):")
try:
    r = subprocess.run([SEVENZIP, "l", F], capture_output=True, text=True, timeout=120)
    lines = [ln for ln in r.stdout.splitlines() if re.search(r"\.rsrc|RCDATA|\bBIN\b|\[0\]", ln)]
    # also just show the largest entries overall
    rows=[]
    for ln in r.stdout.splitlines():
        m=re.match(r"\s*\S+ \S+ \S+\s+(\d+)\s+(\d+)?\s+(.+)$", ln)
        if m:
            rows.append((int(m.group(1)), m.group(3)))
    for sz,nm in sorted(rows, reverse=True)[:15]:
        print(f"  {sz:>10}  {nm}")
except Exception as e:
    print("  7z failed:", e)
