#!/usr/bin/env python3
"""Find the EXE key: drive first-boot setup all the way to the final
'Note: ... Press:[EXE]' dialog with a fixed (deterministic) key sequence, then
inject ONE test key and see which one dismisses the dialog (-> main menu).
Detected as the framebuffer hash differing from the 'still on the Note' baseline.
"""
import os, struct, subprocess, shutil, collections

HERE = os.path.dirname(__file__)
EMU = os.path.join(HERE, "..", "emu_go")
EXE = os.path.join(EMU, "emu_go.exe")
OSB = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")
OUT = os.path.join(HERE, "sweep3")
os.makedirs(OUT, exist_ok=True)

TBL = 0x805ff7ec & 0x1FFFFFFF
img = open(OSB, "rb").read()
def code(C, R):
    return struct.unpack(">i", img[TBL + C*0x1c + R*4: TBL + C*0x1c + R*4 + 4])[0] & 0xffffffff

cells = []
for C in range(1, 13):
    for R in range(1, 8):
        e = struct.unpack(">i", img[TBL + C*0x1c + R*4: TBL + C*0x1c + R*4 + 4])[0]
        if e != -1:
            cells.append((C, R))

# prefix drives Language..Battery..select..confirm..Finish -> 'Note Press:[EXE]'
PREFIX = "1-9,1-9,1-9,1-9,6-9,6-9,1-9,1-9"
PRESS = 130_000_000
INTERVAL = 14_000_000
# 8 prefix keys fire at 130M..(130+8*14)=242M; test key is #9 at 256M; settle to 290M
MAXINS = 290_000_000
TIMER = 30000

print(f"sweep3: {len(cells)} test keys after PREFIX to the Note/EXE dialog")
results = {}
for C, R in cells:
    seq = f"{PREFIX},{R-1}-{C-1}"
    p = subprocess.run([EXE, str(MAXINS), str(TIMER), "seq", seq, str(PRESS), str(INTERVAL)],
                       cwd=EMU, capture_output=True, text=True)
    h = None
    for line in p.stdout.splitlines():
        if line.startswith("FBHASH"):
            h = line.split("->")[1].strip()
    results[(C, R)] = (h, code(C, R))
    shutil.copyfile(os.path.join(EMU, "seq_final.png"),
                    os.path.join(OUT, f"note_C{C:02d}_R{R}_code{code(C,R):02x}.png"))
    print(f"  C{C:2d} R{R} code=0x{code(C,R):02x} -> {h}")

groups = collections.defaultdict(list)
for cr, (h, cd) in results.items():
    groups[h].append((cr, cd))
baseline = max(groups, key=lambda h: len(groups[h]))
print(f"\nbaseline (still on Note dialog) = {baseline} ({len(groups[baseline])} cells)")
print("\n=== test keys that DISMISSED the Note dialog ===")
for h, lst in groups.items():
    if h == baseline:
        continue
    s = ", ".join(f"C{C}R{R}(0x{cd:02x})" for (C, R), cd in lst)
    print(f"  {h}: {s}")
print(f"\nPNGs in {OUT}")
