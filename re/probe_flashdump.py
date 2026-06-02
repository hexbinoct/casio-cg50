#!/usr/bin/env python3
"""Verify + probe the physical fx-CG50 flash dump (os/flash_dump/).

1. SHA256-verify every file against SHA256SUMS.txt.
2. Probe each blob: magic/version strings, region identity, entropy profile.
3. Cross-check os.bin against our unpacked updater image (cg50_os_3.80.plain.bin).
4. Decode the live on-chip RAM (ilram.bin) structures we mapped in Ghidra:
   IRQ handler table @0xFD8004D0, priority table @0xFD8006D0, kbd struct @0xFD8007D0.
Single `python re/probe_flashdump.py` run = whole step.
"""
import hashlib, os, math, struct, re

ROOT = r"F:\ru\myprojects\may\cg50"
DD   = os.path.join(ROOT, "os", "flash_dump")
PLAIN= os.path.join(ROOT, "os", "os_image", "cg50_os_3.80.plain.bin")

def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()

def entropy(b):
    if not b: return 0.0
    freq = [0]*256
    for x in b: freq[x] += 1
    n = len(b); e = 0.0
    for c in freq:
        if c:
            p = c/n; e -= p*math.log2(p)
    return e

def ascii_strings(b, minlen=5, limit=40):
    out = []
    cur = bytearray()
    for x in b:
        if 32 <= x < 127:
            cur.append(x)
        else:
            if len(cur) >= minlen:
                out.append(cur.decode("ascii"))
                if len(out) >= limit: break
            cur = bytearray()
    return out

def find_all(hay, needle):
    out, i = [], hay.find(needle)
    while i != -1:
        out.append(i); i = hay.find(needle, i+1)
    return out

# ---- 1. checksum verification ----
print("="*72)
print("1. SHA256 VERIFICATION")
print("="*72)
expected = {}
with open(os.path.join(DD, "SHA256SUMS.txt"), encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line or line.startswith("#"): continue
        h, name = line.split()
        expected[name] = h
allok = True
for name, exp in expected.items():
    p = os.path.join(DD, name)
    if not os.path.exists(p):
        print(f"  [MISSING] {name}"); allok = False; continue
    got = sha256(p)
    ok = (got == exp)
    allok &= ok
    print(f"  [{'OK ' if ok else 'BAD'}] {name:16} {os.path.getsize(p):>10,} B")
    if not ok:
        print(f"        expected {exp}\n        got      {got}")
print(f"\n  => {'ALL FILES VERIFIED' if allok else 'CHECKSUM MISMATCH(ES)!'}")

# ---- 2/3. per-blob probe ----
def probe(name, note):
    p = os.path.join(DD, name)
    if not os.path.exists(p): return None
    b = open(p, "rb").read()
    print("\n" + "="*72)
    print(f"2. PROBE {name}  ({len(b):,} B)  — {note}")
    print("="*72)
    # entropy in 1MB windows
    win = 1 << 20
    profs = []
    for off in range(0, len(b), win):
        profs_e = entropy(b[off:off+win])
        profs.append(profs_e)
    print("  entropy/1MB:", " ".join(f"{e:.1f}" for e in profs))
    # markers
    for mark in (b"CASIOWIN", b"CASIOABS", b"USBPower", b"Bootloader", b"GETKEY"):
        locs = find_all(b, mark)
        if locs:
            shown = ", ".join(hex(x) for x in locs[:6])
            print(f"  {mark.decode():10} x{len(locs):<4} @ {shown}")
    # version-looking strings NN.NN.NNNN
    vers = sorted(set(re.findall(rb"\d\d\.\d\d\.\d{4}", b)))
    if vers:
        print("  version strings:", ", ".join(v.decode() for v in vers[:8]))
    return b

fl = probe("flash_full.bin", "full 16MB NOR flash")
ob = probe("os.bin",        "OS region")
dr = probe("dram.bin",      "DRAM 0x8C000000 (live)")

# ---- cross-check os.bin / flash_full vs unpacked plain image ----
print("\n" + "="*72)
print("3. CROSS-CHECK vs unpacked updater image")
print("="*72)
if os.path.exists(PLAIN):
    pb = open(PLAIN, "rb").read()
    print(f"  plain image: {len(pb):,} B")
    def cmp(name, b):
        if b is None: return
        n = min(len(pb), len(b))
        # find largest matching prefix and count equal bytes
        eq = sum(1 for i in range(0, n, 977) if pb[i] == b[i])  # sampled
        # locate plain image inside b (search first 64B of plain)
        loc = b.find(pb[:64])
        firstdiff = next((i for i in range(n) if pb[i] != b[i]), None)
        print(f"  vs {name:14}: sampled-eq {eq}/{(n//977)+1}; "
              f"plain[:64] found @ {hex(loc) if loc>=0 else 'NO'}; "
              f"first byte diff @ {hex(firstdiff) if firstdiff is not None else 'identical-prefix'}")
    cmp("os.bin", ob)
    cmp("flash_full.bin", fl)
else:
    print(f"  (plain image not found at {PLAIN})")

# ---- 4. on-chip RAM (ilram) live structures ----
print("\n" + "="*72)
print("4. on-chip RAM live structures (ilram.bin)")
print("="*72)
ip = os.path.join(DD, "ilram.bin")
if os.path.exists(ip):
    il = open(ip, "rb").read()
    print(f"  ilram.bin {len(il):,} B; entropy {entropy(il):.2f}")
    # gint dumps on-chip RAM; base address unknown until we see the data.
    # Our Ghidra map put kernel structs at 0xFD800000. If this blob is that
    # region, offset = addr - 0xFD800000.
    BASE = 0xFD800000
    def at(addr, n=16):
        off = addr - BASE
        if 0 <= off < len(il)-n:
            return il[off:off+n]
        return None
    for label, addr in (("IRQ handler table @0xFD8004D0", 0xFD8004D0),
                        ("IRQ priority    @0xFD8006D0", 0xFD8006D0),
                        ("kbd-state struct @0xFD8007D0", 0xFD8007D0),
                        ("boot stack top  @0xFD804000", 0xFD803FF0)):
        d = at(addr)
        if d is not None:
            words = struct.unpack(">4I", d)
            print(f"  {label}: " + " ".join(f"{w:08x}" for w in words))
        else:
            print(f"  {label}: (outside this blob; base guess wrong)")
    # show first handful of non-zero 32-bit words to infer base
    print("  first 8 non-zero BE words:")
    cnt = 0
    for off in range(0, min(len(il), 0x2000), 4):
        w = struct.unpack(">I", il[off:off+4])[0]
        if w not in (0, 0xFFFFFFFF):
            print(f"    +{off:04x}: {w:08x}")
            cnt += 1
            if cnt >= 8: break
print("\nDONE.")
