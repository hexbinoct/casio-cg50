#!/usr/bin/env python3
"""Keymap audit: our key LABELS vs what the OS actually produces.

re/KEYMAP.md's codes come straight from the OS remap tables (authoritative); its labels were
hand-written. This prints, for every physical key, our label beside the libfxcg names of the
OS's primary and SHIFT getkey codes, flags disagreements, and checks the Android keypad
(KeyMap.kt) labels against the same codes. libfxcg's keyboard.h is fetched once into re/.
Usage: python re/audit_keymap.py
"""
import os, re, sys, urllib.request

sys.stdout.reconfigure(encoding="utf-8")

HERE = os.path.dirname(os.path.abspath(__file__))
HDR = os.path.join(HERE, "libfxcg_keyboard.h")
if not os.path.exists(HDR):
    urllib.request.urlretrieve(
        "https://raw.githubusercontent.com/Jonimoose/libfxcg/master/include/fxcg/keyboard.h", HDR)

names = {}
for m in re.finditer(r"#define\s+(KEY_(?:CTRL|CHAR)_\w+)\s+(0x[0-9A-Fa-f]+|\d+)", open(HDR).read()):
    names.setdefault(int(m.group(2), 0), []).append(m.group(1))

def match(label, code):
    """True if our label agrees with the OS code (libfxcg names, ignoring the ch: prefix)."""
    got = [n.replace("ch:", "") for n in nm(code).split("/")]
    return any(e in got for e in EXPECT[label])

def nm(code):
    if code is None:
        return "--"
    return "/".join(n.replace("KEY_CTRL_", "").replace("KEY_CHAR_", "ch:") for n in names.get(code, [])) or f"?{code:#x}"

def code(cell):
    m = re.match(r"(0x[0-9a-f]+)", cell.strip())
    return int(m.group(1), 16) if m else None

# label -> the libfxcg name fragments we accept for its primary code
EXPECT = {
    "AC/ON": ["AC"], "EXE": ["EXE"], "(-)": ["PMINUS"], "x10^x": ["EXP"], ".": ["DP"],
    "+": ["PLUS"], "-": ["MINUS"], "*": ["MULT"], "/": ["DIV"], "DEL": ["DEL"],
    "a b/c": ["FRAC"], "S<->D": ["FD"], "(": ["LPAR"], ")": ["RPAR"], ",": ["COMMA"],
    "->": ["STORE"], "X,th,T": ["XTT"], "log": ["LOG"], "ln": ["LN"], "sin": ["SIN"],
    "cos": ["COS"], "tan": ["TAN"], "ALPHA": ["ALPHA"], "x^2": ["SQUARE"], "^": ["POW"],
    "MENU": ["MENU"], "EXIT": ["EXIT"], "DOWN": ["DOWN"], "RIGHT": ["RIGHT"], "LEFT": ["LEFT"],
    "UP": ["UP"], "SHIFT": ["SHIFT"], "OPTN": ["OPTN"], "VARS": ["VARS"],
    **{f"F{i}": [f"F{i}"] for i in range(1, 7)}, **{str(d): [str(d)] for d in range(10)},
}

rows = []
for line in open(os.path.join(HERE, "KEYMAP.md"), encoding="utf-8"):
    c = [x.strip() for x in line.split("|")]
    if len(c) < 12 or not c[5].startswith("`"):
        continue
    label, grid, inject = c[1], c[4], c[5].strip("`")
    rows.append((label, grid, inject, code(c[7]), code(c[8])))

bad = []
print(f"{'label':8} {'grid':8} {'inject':6}  primary -> libfxcg          shift -> libfxcg")
for label, grid, inject, p, s in rows:
    ok = label not in EXPECT or match(label, p)
    flag = "" if ok else "   <-- MISMATCH"
    if not ok:
        bad.append((label, inject, nm(p)))
    print(f"{label:8} {grid:8} {inject:6}  {(f'{p:#06x}' if p is not None else '--'):8} {nm(p):22} "
          f"{(f'{s:#06x}' if s is not None else '--'):8} {nm(s)}{flag}")

print(f"\nKEYMAP.md label mismatches: {len(bad)}")
for b in bad:
    print("  ", b)

# Android keypad: Key("LABEL", row, col, ...)
kt = os.path.join(HERE, "..", "android", "app", "src", "main", "java", "com", "hexbinoct", "cg50", "KeyMap.kt")
by_inject = {r[2]: r for r in rows}
alias = {"x²": "x^2", "×": "*", "÷": "/", "−": "-", "–": "-", "X,θ,T": "X,th,T", "→": "->",
         "▲": "UP", "▼": "DOWN", "◀": "LEFT", "▶": "RIGHT", "S⇔D": "S<->D", "×10ˣ": "x10^x",
         "(−)": "(-)", "a⁄b": "a b/c"}
print("\nAndroid KeyMap.kt:")
nbad = 0
for m in re.finditer(r'Key\("([^"]+)",\s*(\d+),\s*(\d+)[,)]', open(kt, encoding="utf-8").read()):
    lab, r, col = m.group(1), m.group(2), m.group(3)
    k = alias.get(lab, lab)
    row = by_inject.get(f"{r}-{col}")
    if row is None:
        print(f"  {lab!r} ({r},{col}): no such matrix key"); nbad += 1
        continue
    if k in EXPECT and not match(k, row[3]):
        print(f"  {lab!r} ({r},{col}): OS primary = {nm(row[3])}  <-- MISMATCH"); nbad += 1
    elif k not in EXPECT:
        print(f"  {lab!r} ({r},{col}): OS primary = {nm(row[3])} (unchecked label)")
print(f"Android mismatches: {nbad}")
