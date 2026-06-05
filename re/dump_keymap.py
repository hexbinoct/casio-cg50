#!/usr/bin/env python3
"""Full fx-CG50 (OS 3.60) keyboard map dumper.

Combines three authoritative pieces from os.bin:
  1. Matrix scan table  DAT_805ff7ec[col*0x1c + row*4]  (col,row 1-based) -> raw code 0x01..0x3c
     (the value the KEYSC scan + key queue produce for a physical key at matrix (col,row)).
  2. The raw->getkey remap tables used by FUN_80194dda / FUN_80194e3c, selected by the
     SHIFT/ALPHA modifier state:
        set A (checked first):  primary=PTR_DAT_80194fe0  alpha=PTR_DAT_80194fe4  shift=PTR_DAT_80194fe8
        set B (used if A==0):   primary=PTR_DAT_80194fec  alpha=PTR_DAT_80194ff0  shift=PTR_DAT_80194ff4  shift2=PTR_DAT_80194ff8
     Final code for a modifier = A[raw] if nonzero else B[raw].
  3. ALPHA-LOCK lowercases A-Z (final +0x20) — handled in FUN_80194dda for state 8/0x88.

Our emulator harness injects a physical key as  inject "<row-1>-<col-1>"  (0-based),
i.e. grid (C,R) -> inject string "{R-1}-{C-1}".  SHIFT/ALPHA are themselves keys you inject
*before* the target key; the OS runs its own modifier state machine, exactly like a real press.

vaddr -> file off = vaddr & 0x1FFFFFFF (image mapped at phys 0).
"""
import os, struct

OS = os.path.join(os.path.dirname(__file__), "..", "os", "flash_dump", "os.bin")
img = open(OS, "rb").read()

def rd32(vaddr):
    o = vaddr & 0x1FFFFFFF
    return struct.unpack(">I", img[o:o+4])[0]

def ptr(vaddr):
    """Pointer stored at vaddr (PTR_DAT_xxxx) -> the table base vaddr."""
    return rd32(vaddr)

# --- 1. matrix scan table -------------------------------------------------
MATRIX = 0x805ff7ec
def matrix_raw(col, row):
    return rd32(MATRIX + col * 0x1c + row * 4)

# --- 2. remap tables ------------------------------------------------------
A_PRIMARY = ptr(0x80194fe0)
A_ALPHA   = ptr(0x80194fe4)
A_SHIFT   = ptr(0x80194fe8)
B_PRIMARY = ptr(0x80194fec)
B_ALPHA   = ptr(0x80194ff0)
B_SHIFT   = ptr(0x80194ff4)
B_SHIFT2  = ptr(0x80194ff8)

def remap(rawcode, a_tbl, b_tbl):
    """Final getkey code for a raw code under one modifier: A if nonzero else B."""
    v = rd32(a_tbl + rawcode * 4)
    if v != 0:
        return v
    return rd32(b_tbl + rawcode * 4)

def codes_for(rawcode):
    primary = remap(rawcode, A_PRIMARY, B_PRIMARY)
    shift   = remap(rawcode, A_SHIFT,   B_SHIFT)
    alpha   = remap(rawcode, A_ALPHA,   B_ALPHA)
    # alpha-lock = alpha but A-Z lowercased
    alock = alpha
    if 0x40 < alock < 0x5b:
        alock += 0x20
    return primary, shift, alpha, alock

def show(v):
    if v == 0:
        return "--"
    # printable ASCII gets an annotation
    if 0x20 <= v < 0x7f:
        return f"{v:#06x} '{chr(v)}'"
    return f"{v:#06x}"

# --- physical-key labels, keyed by matrix grid (col,row) ------------------
# label = primary (white).  shift = yellow (top-left) printed fn.  alpha = red (top-right).
# v = empirically verified live on the emulator this session.
LABELS = {
    (1, 1):  ("AC/ON",  "OFF",      "",   ""),
    (2, 7):  ("0",      "",         "Z",  "v"),
    (2, 6):  (".",      "=",        "SPACE", "v"),  # decimal point — matrix cell holds raw 0x00
    (2, 5):  ("x10^x",  "pi",       '"',  "v"),
    (2, 4):  ("(-)",    "Ans",      "",   "v"),
    (2, 3):  ("EXE",    "<newline>", "",  "v"),
    (3, 7):  ("1",      "List",     "U",  "v"),
    (3, 6):  ("2",      "Mat",      "V",  "v"),
    (3, 5):  ("3",      "",         "W",  "v"),
    (3, 4):  ("+",      "[",        "X",  "v"),
    (3, 3):  ("-",      "]",        "Y",  "v"),
    (4, 7):  ("4",      "CATALOG",  "P",  "v"),
    (4, 6):  ("5",      "FORMAT",   "Q",  "v"),
    (4, 5):  ("6",      "",         "R",  "v"),
    (4, 4):  ("*",      "{",        "S",  "v"),
    (4, 3):  ("/",      "}",        "T",  "v"),
    (5, 7):  ("7",      "CAPTURE",  "M",  "v"),
    (5, 6):  ("8",      "CLIP",     "N",  "v"),
    (5, 5):  ("9",      "PASTE",    "O",  "v"),
    (5, 4):  ("DEL",    "INS",      "",   "v"),
    (6, 7):  ("a b/c",  "(frac)",   "G",  ""),
    (6, 6):  ("S<->D",  "",         "H",  ""),
    (6, 5):  ("(",      "",         "I",  "v"),
    (6, 4):  (")",      "",         "J",  "v"),
    (6, 3):  (",",      "",         "K",  "v"),
    (6, 2):  ("->",     "",         "L",  ""),
    (7, 7):  ("X,th,T", "",         "A",  "v"),
    (7, 6):  ("log",    "10^x",     "B",  "v"),
    (7, 5):  ("ln",     "e^x",      "C",  "v"),
    (7, 4):  ("sin",    "sin-1",    "D",  "v"),
    (7, 3):  ("cos",    "cos-1",    "E",  "v"),
    (7, 2):  ("tan",    "tan-1",    "F",  "v"),
    (8, 7):  ("ALPHA",  "A-LOCK",   "",   "v"),
    (8, 6):  ("x^2",    "sqrt",     "",   "v"),
    (8, 5):  ("^",      "x-root",   "",   "v"),
    (8, 4):  ("MENU",   "SET UP",   "",   "v"),
    (8, 3):  ("DOWN",   "",         "",   "v"),
    (8, 2):  ("RIGHT",  "",         "",   "v"),
    (9, 7):  ("SHIFT",  "",         "",   "v"),
    (9, 6):  ("OPTN",   "",         "",   "v"),
    (9, 5):  ("VARS",   "PRGM",     "",   ""),
    (9, 4):  ("EXIT",   "QUIT",     "",   ""),
    (9, 3):  ("LEFT",   "",         "",   "v"),
    (9, 2):  ("UP",     "",         "",   "v"),
    (10, 7): ("F1",     "Trace",    "",   "v"),
    (10, 6): ("F2",     "Zoom",     "",   ""),
    (10, 5): ("F3",     "V-Window", "",   ""),
    (10, 4): ("F4",     "Sketch",   "",   ""),
    (10, 3): ("F5",     "G-Solv",   "",   ""),
    (10, 2): ("F6",     "G<->T",    "",   "v"),
    (12, 4): ("(DIAGNOSTIC - AVOID)", "", "", ""),
}

# matrix cells that legitimately hold raw code 0x00 but ARE real keys (only the dot).
REAL_RAW0 = {(2, 6)}

rows = []
for col in range(1, 13):
    for row in range(1, 8):
        raw = matrix_raw(col, row)
        if raw == 0xFFFFFFFF:
            continue
        if raw == 0 and (col, row) not in REAL_RAW0:
            continue
        p, s, a, al = codes_for(raw)
        inject = f"{row-1}-{col-1}"
        rows.append((col, row, raw, inject, p, s, a, al))
rows.sort(key=lambda t: (t[0], -t[1]))

# --- console summary ------------------------------------------------------
print(f"pointers: A.primary={A_PRIMARY:#010x} A.alpha={A_ALPHA:#010x} A.shift={A_SHIFT:#010x}")
print(f"          B.primary={B_PRIMARY:#010x} B.alpha={B_ALPHA:#010x} B.shift={B_SHIFT:#010x} B.shift2={B_SHIFT2:#010x}")
print(f"total physical matrix positions: {len(rows)}\n")

# --- write KEYMAP.md ------------------------------------------------------
MD = os.path.join(os.path.dirname(__file__), "KEYMAP.md")
out = []
out.append("# fx-CG50 (OS 3.60) keyboard map — physical key → inject coords → codes\n")
out.append("**Auto-generated by `re/dump_keymap.py` from `os/flash_dump/os.bin`. Do not hand-edit the table.**\n")
out.append("See the prose sections below the table for how injection + SHIFT/ALPHA work.\n")
out.append("`inject` is the harness string for `seq`/`key` mode: `\"<row0>-<col0>\"` (0-based), i.e. ")
out.append("grid (C,R) → `\"{R-1}-{C-1}\"`. `ver` = ✓ empirically verified live on the emulator.\n")
out.append("Codes: `0x75xx`=Casio control getkey codes, ASCII=character input, `0x00xx`=editor function tokens.\n")
out.append("| Key | SHIFT fn | ALPHA | grid (C,R) | inject | raw | primary | SHIFT code | ALPHA code | a-lock | ver |")
out.append("|-----|----------|-------|-----------|--------|-----|---------|-----------|-----------|--------|-----|")
for col, row, raw, inject, p, s, a, al in rows:
    lbl = LABELS.get((col, row), ("?", "", "", ""))
    name, shf, alf, ver = lbl
    vmark = "✓" if ver == "v" else ""
    out.append(f"| {name} | {shf} | {alf} | C{col} R{row} | `{inject}` | {raw:#04x} | "
               f"{show(p)} | {show(s)} | {show(a)} | {show(al)} | {vmark} |")
out.append("")
PROSE = """
## How key injection works (faithful path)

We do **not** model the KEYSC IRQ + matrix-data-reg format. Instead the harness calls the OS's
own scan-enqueue routine `FUN_801e684c` as a subroutine (`cpu.callInject`) with a `{row,col}`
2-byte arg, landing the key in the exact queue the UI consumes — identical to a real keypress.

- Harness: `go -C emu_go run . <maxIns> <timerPeriod> seq "<r-c,r-c,...>" <pressAt> <interval>`
- `seq` injects each key starting at `pressAt`, dumping a PNG per key. Token forms:
  - `r-c`        — press, then wait the default `interval` before the next key.
  - `r-c*GAP`    — press, then wait a fixed `GAP` instructions (decimal) before the next key.
  - `r-c*wGAP`   — **decode-confirmed**: press, then wait until the OS decode `FUN_801952cc`
    actually runs for this key (proving the app READ it), re-injecting if it doesn't fire within
    40M instr, then settle `GAP`. Use this for typing INTO an app — it defeats the phase-locking
    that plain fixed delays suffer from.
- Coordinates are **0-based matrix (row,col)**. To press the key at grid (C,R) inject `"{R-1}-{C-1}"`.
- **Why decode-confirmed pacing matters:** a key injected while the app is redrawing is *flushed*
  (the app clears pending input before its next getkey) and never reaches the editor. Fixed delays
  phase-lock onto the redraw and silently drop ~20-100% of keystrokes depending on the gap. The
  `*w` form re-injects until the app's decode actually consumes the key, giving reliable typing
  (verified: full `1234567890` and `8+5-2*3/4` typed cleanly). Setup/menu nav (the boot sequence)
  still uses plain fixed `interval` since those screens don't flush the same way.

## SHIFT and ALPHA are real keys — inject them before the target key

You never compute a "combined code". You inject the modifier key's coords, then the target key's
coords, and the OS's own getkey state machine (`FUN_801952cc` → `FUN_80194dda`/`FUN_80194e3c`)
produces the shifted/alpha meaning — exactly like a user pressing SHIFT then the key.

The modifier model (from the OS, authoritative):
- **No modifier:** `primary` column (white label) — character or `0x75xx` control code.
- **SHIFT (`6-8`, code 0x7536):** one-shot. The next key yields its **SHIFT code** (yellow label:
  e.g. SHIFT then `log` = `10^x`; SHIFT then `1` = List). Annunciator: a yellow **S** appears
  top-left. Auto-clears after one key.
- **ALPHA (`6-7`, code 0x7537):** one-shot. The next key yields its **ALPHA letter** (red label:
  ALPHA then `X,θ,T` = `A` (0x41), … ALPHA then `0` = `Z`). Annunciator: red **A** top-left.
- **A-LOCK = SHIFT then ALPHA:** sticky alpha — every subsequent key types its letter until ALPHA
  is pressed again. The `a-lock` column shows the lowercase variant (`A`→`a`, OS adds `+0x20` for
  state 8/0x88 in `FUN_80194dda`); used by input contexts that allow lowercase.
- Selection internals: `FUN_80194dda`/`FUN_80194e3c` pick a table by modifier state — set A
  (primary `0x805ff160`, alpha `0x805ff254`, shift `0x805ff348`) is tried first; if it returns 0 the
  set B fallback (primary `0x805ff43c`, alpha `0x805ff530`, shift `0x805ff624`, shift2 `0x805ff718`)
  is used. The raw→table index is the matrix code from `DAT_805ff7ec[col*0x1c + row*4]`.

## Verified this session (emulator, OS 3.60) — `ver` ✓ column
Empirically confirmed by typing into Run-Matrix and reading the glyphs, or by observed behaviour:
- **Number pad:** `0`-`9` (typed `1234567890`), decimal `.` `5-1` (typed `3.14` → evaluated).
- **Operators:** `+` `3-2`, `-` `2-2`, `*` `3-3`, `/` `2-3` (typed `8+5-2*3/4`).
- **Bottom row:** `(-)` `3-1` (→ `-5`), `x10^x` `4-1` (→ `2×10^3`), `EXE` `2-1` (evaluates / new line).
- **Functions:** `sin cos tan log ln` (`3-6 2-6 1-6 5-6 4-6`); `X,θ,T` `6-6`, `x²` `5-7`, `^` `4-7`,
  `(` `4-5`, `)` `3-5`, `,` `2-5` (typed `x²+x^(3+(2,3))`).
- **Modifiers:** SHIFT `6-8` + `sin` → `sin⁻¹`; ALPHA `6-7` + `X,θ,T` → `A`. Confirms the OS applies
  the yellow/red secondary meaning when the modifier key is injected before the target.
- **Editing / nav / menus:** `DEL` `3-4` (`789`→`78`), `OPTN` `5-8` (opens LIST/MAT-VCT/… softkeys),
  `MENU` `3-7` (back to MAIN MENU), arrows UP `1-8` / DOWN `2-7` / LEFT `2-8` / RIGHT `1-7`,
  F1 `6-9` & F6 `1-9` (used to drive first-boot setup).
- **Not yet individually pressed** (codes are table-authoritative, low risk): `->` store, `S<->D`,
  `a b/c`, `VARS`, `EXIT`, `AC/ON`, `F2`-`F5`, and ALPHA letters B-Z (the ALPHA mechanism + table
  are proven via `A`).

The full first-boot→menu→app drive sequence lives in RECON_NOTES (cont.12/13).

## For the Android app
Each on-screen button maps to one row in the table above; pressing it should enqueue the same matrix
`(row,col)` we inject here. SHIFT/ALPHA/A-LOCK are stateful toggle buttons (one-shot for SHIFT/ALPHA,
sticky for A-LOCK) — render the yellow (SHIFT) and red (ALPHA) secondary labels exactly as the physical
keypad, and drive the S/A annunciators from the same OS state the keypad lights.
"""
out.append(PROSE.strip() + "\n")
with open(MD, "w", encoding="utf-8") as f:
    f.write("\n".join(out))
print(f"wrote {MD}")
