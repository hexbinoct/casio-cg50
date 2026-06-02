#!/usr/bin/env python3
"""Deep-sweep: drive the first-boot setup to the LAST screen (Battery Settings)
with 3 'advance' (F6/Next, code 0x29 = grid C10R2 = inject row1,col9) presses,
then inject ONE test key and see which one FINISHES setup (leaves Battery
Settings -> main menu). Detected by the framebuffer hash differing from the
'stayed on Battery' baseline.
"""
import os, struct, subprocess, shutil, collections

HERE = os.path.dirname(__file__)
EMU = os.path.join(HERE, "..", "emu_go")
EXE = os.path.join(EMU, "emu_go.exe")
OSB = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")
OUT = os.path.join(HERE, "sweep2")
os.makedirs(OUT, exist_ok=True)

TBL = 0x805ff7ec & 0x1FFFFFFF
img = open(OSB, "rb").read()
def code(C, R):
    o = TBL + C*0x1c + R*4
    return struct.unpack(">i", img[o:o+4])[0] & 0xffffffff

cells = []
for C in range(1, 13):
    for R in range(1, 8):
        e = struct.unpack(">i", img[TBL + C*0x1c + R*4: TBL + C*0x1c + R*4 + 4])[0]
        if e != -1:
            cells.append((C, R))

ADV = "1-9"  # advance / Next (code 0x29)
PRESS = 130_000_000
INTERVAL = 8_000_000
MAXINS = 175_000_000  # test key fires at PRESS+3*INTERVAL=154M, ~21M settle
TIMER = 30000

print(f"deep-sweep {len(cells)} test keys after 3 advances to Battery Settings")
results = {}
for C, R in cells:
    seq = f"{ADV},{ADV},{ADV},{R-1}-{C-1}"
    p = subprocess.run([EXE, str(MAXINS), str(TIMER), "seq", seq, str(PRESS), str(INTERVAL)],
                       cwd=EMU, capture_output=True, text=True)
    h = None
    for line in p.stdout.splitlines():
        if line.startswith("FBHASH"):
            h = line.split("->")[1].strip()
    results[(C, R)] = (h, code(C, R))
    shutil.copyfile(os.path.join(EMU, "seq_final.png"),
                    os.path.join(OUT, f"batt_C{C:02d}_R{R}_code{code(C,R):02x}.png"))
    print(f"  C{C:2d} R{R} code=0x{code(C,R):02x} -> {h}")

groups = collections.defaultdict(list)
for cr, (h, cd) in results.items():
    groups[h].append((cr, cd))
baseline = max(groups, key=lambda h: len(groups[h]))
print(f"\nbaseline (stayed on Battery) = {baseline} ({len(groups[baseline])} cells)")
print("\n=== test keys that LEFT the Battery screen ===")
for h, lst in groups.items():
    if h == baseline:
        continue
    s = ", ".join(f"C{C}R{R}(0x{cd:02x})" for (C, R), cd in lst)
    print(f"  {h}: {s}")
print(f"\nPNGs in {OUT}")
