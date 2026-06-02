#!/usr/bin/env python3
"""Empirically map the fx-CG50 KEYSC matrix on the live first-boot 'Message
Language' screen.

For each valid matrix cell (grid col C 1..12, row R 1..7 in the DAT_805ff7ec
table) we boot the Go emulator, inject that key via `key` mode (which calls the
OS's own enqueue FUN_801e684c), let it settle, and read the framebuffer hash the
emulator prints (FBHASH). Cells whose hash differs from the do-nothing baseline
DID something on screen (moved the cursor / advanced the setup screen). We group
by hash and save a representative PNG per distinct outcome into re/sweep/.

Injection mapping: table[col*0x1c + row*4] is indexed by the QUEUED (col,row),
which are the injected values +1. So to hit grid cell (C,R) we inject
row=R-1, col=C-1.
"""
import os, struct, subprocess, shutil, collections

HERE = os.path.dirname(__file__)
EMU = os.path.join(HERE, "..", "emu_go")
EXE = os.path.join(EMU, "emu_go.exe")
OSB = os.path.join(HERE, "..", "os", "flash_dump", "os.bin")
OUT = os.path.join(HERE, "sweep")
os.makedirs(OUT, exist_ok=True)

PRESS = 130_000_000
MAXINS = 138_000_000
TIMER = 30000

img = open(OSB, "rb").read()
TBL = 0x805ff7ec & 0x1FFFFFFF
def code(C, R):
    o = TBL + C * 0x1c + R * 4
    return struct.unpack(">i", img[o:o+4])[0] & 0xffffffff

# candidate cells: every grid (C,R) whose table entry is not 0xffffffff (--)
cells = []
for C in range(1, 13):
    for R in range(1, 8):
        e = struct.unpack(">i", img[TBL + C*0x1c + R*4: TBL + C*0x1c + R*4 + 4])[0]
        if e != -1:
            cells.append((C, R))

print(f"sweeping {len(cells)} matrix cells (press@{PRESS}); EXE={EXE}")
results = {}   # (C,R) -> (hash, code)
for C, R in cells:
    row, col = R - 1, C - 1
    p = subprocess.run([EXE, str(MAXINS), str(TIMER), "key", str(row), str(col), str(PRESS)],
                       cwd=EMU, capture_output=True, text=True)
    h = None
    for line in p.stdout.splitlines():
        if line.startswith("FBHASH"):
            h = line.split("->")[1].strip()
    cd = code(C, R)
    results[(C, R)] = (h, cd)
    shutil.copyfile(os.path.join(EMU, "key_final.png"),
                    os.path.join(OUT, f"cell_C{C:02d}_R{R}_code{cd:02x}.png"))
    print(f"  C{C:2d} R{R} code=0x{cd:02x} -> {h}")

# group by hash; the largest group is the do-nothing baseline
groups = collections.defaultdict(list)
for cr, (h, cd) in results.items():
    groups[h].append((cr, cd))
baseline = max(groups, key=lambda h: len(groups[h]))
print(f"\nbaseline hash (no visible effect) = {baseline}  ({len(groups[baseline])} cells)")
print("\n=== cells that CHANGED the screen ===")
for h, lst in groups.items():
    if h == baseline:
        continue
    cells_s = ", ".join(f"C{C}R{R}(code 0x{cd:02x})" for (C, R), cd in lst)
    print(f"  hash {h}: {cells_s}")
print(f"\nPNGs in {OUT} (cell_C..R.._code..png). Inspect the changed ones.")
