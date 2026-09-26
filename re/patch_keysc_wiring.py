"""One-shot wiring patch for the KEYSC key-scan model (cont.18l). Exact-string edits with
assertions so a mismatch fails loudly instead of silently skipping. Re-runnable: each edit
is skipped if its NEW text is already present."""
ROOT = "F:/ru/myprojects/may/cg50/"


def patch(path, edits):
    p = ROOT + path
    s = open(p, encoding="utf-8", newline="").read()
    for old, new in edits:
        if new in s and old not in s:
            print(f"  [skip] {path}: already applied: {new[:50]!r}")
            continue
        n = s.count(old)
        assert n == 1, f"{path}: expected 1 match, got {n} for {old[:70]!r}"
        s = s.replace(old, new)
    open(p, "w", encoding="utf-8", newline="").write(s)
    print(f"  [ok] {path}")


# ---------------- emu_go/mmio.go ----------------
patch("emu_go/mmio.go", [
    ("""// KEYSC / KIU: all keys released = 0 in every data register.
type keysc struct{ base }

func (k *keysc) read(va, size uint32) uint32 { return 0 }
""",
     """// INTC (0xA4080000, SH7724-style IPR/IMR/IMCR — NOT the keyboard; cont.18l). The OS
// programs priorities/masks around its ISRs; masking isn't modelled (CPU-side IMASK/BL
// gating suffices), and reads return 0 as they always did.
type intcStub struct{ base }

func (k *intcStub) read(va, size uint32) uint32 { return 0 }
"""),
    ("\ttimerPeriod uint64\n",
     "\ttimerPeriod uint64\n\tkeysc       *keyscUnit // key-scan unit @0xA44B0000 (keysc.go): the real key path\n"),
    ("\tb.etmu2.bus = b\n\tb.regions = []region{\n",
     "\tb.etmu2.bus = b\n\tb.keysc = newKeysc()\n\tb.regions = []region{\n"),
    ('\t\t&keysc{base: newBase("KEYSC", 0xA4080000, 0x1000)},\n',
     '\t\t&intcStub{base: newBase("INTC", 0xA4080000, 0x1000)},\n'),
    ('\t\t&keysc{base: newBase("KIU_DATA", 0xA44B0000, 0x1000)},\n',
     '\t\tb.keysc,\n'),
    ('\t\treturn "KEYSC", b & 0xFFF, true\n\tcase 0xA44B0000:\n\t\treturn "KIU", b & 0xFFF, true\n',
     '\t\treturn "INTC", b & 0xFFF, true\n\tcase 0xA44B0000:\n\t\treturn "KEYSC", b & 0xFFF, true\n'),
    ("func (b *MMIOBus) tick(cpu *CPU) {\n\tif b.timerPeriod == 0 {\n",
     "func (b *MMIOBus) tick(cpu *CPU) {\n\tb.keysc.tick(cpu)\n\tif b.timerPeriod == 0 {\n"),
])

# ---------------- emu_go/main.go ----------------
patch("emu_go/main.go", [
    ("\t\t\tmmio.timerTicks = 0\n\t\t\tresumed = true\n",
     "\t\t\tmmio.timerTicks = 0\n\t\t\tmmio.keysc.resumeDefaults()\n\t\t\tresumed = true\n"),
])

# ---------------- emu_go/emulator_test.go ----------------
p = ROOT + "emu_go/emulator_test.go"
s = open(p, encoding="utf-8", newline="").read()
s = s.replace("e.keys", "e.tap.queue").replace("e.haveKey", "e.tap.active")
open(p, "w", encoding="utf-8", newline="").write(s)
print("  [ok] emu_go/emulator_test.go")

# ---------------- emu_go/webui.go ----------------
p = ROOT + "emu_go/webui.go"
lines = open(p, encoding="utf-8", newline="").read().split("\n")


def find(sub, start=0):
    for i in range(start, len(lines)):
        if sub in lines[i]:
            return i
    raise AssertionError(f"webui.go: not found: {sub!r}")


if not any("var tap keyTapper" in l for l in lines):
    i = find("// User-key injection state: one matrix press at a time, DECODE-CONFIRMED")
    j = find("retries := 0", i)
    lines[i:j + 1] = [
        "\t// User keys: taps become press/release edges on the KEYSC matrix model — the same",
        "\t// path the physical keyboard takes (the OS's own ISR scans, debounces, repeats and",
        "\t// enqueues), so nothing is ever flushed by a redraw and no re-injection is needed.",
        "\tvar tap keyTapper",
    ]
    i = find("\t\tif !have {")
    j = i
    while lines[j] != "\t}":
        j += 1
    lines[i:j] = [
        "\t\tselect {",
        "\t\tcase c := <-coordCh:",
        "\t\t\ttap.queue = append(tap.queue, c)",
        "\t\tdefault:",
        "\t\t}",
        "\t\ttap.drive(mmio.keysc, cpu.cycles)",
    ]
    i = find("have, injected = false, false // drop any in-flight keypress")
    lines[i] = "\t\t\t\ttap = keyTapper{} // drop any in-flight keypress"
    lines.insert(i + 1, "\t\t\t\tmmio.keysc.releaseAll()")
    lines.insert(i + 2, "\t\t\t\tmmio.keysc.resumeDefaults()")
    open(p, "w", encoding="utf-8", newline="").write("\n".join(lines))
    print("  [ok] emu_go/webui.go")
else:
    print("  [skip] emu_go/webui.go already patched")

# ---------------- emu/mmio.py (oracle mirror) ----------------
KEYSCAN_PY = '''class INTCStub(Region):
    """INTC (0xA4080000, SH7724-style IPR/IMR/IMCR) — NOT the keyboard (cont.18l). Masking
    isn't modelled (CPU IMASK/BL gating suffices); reads return 0 as they always did."""
    def read(self, va, size):
        return 0


class KeyScan(Region):
    """SH7305 key-scan unit (KIU/KEYSC) @0xA44B0000 — the real path a keypress takes into
    the OS. Exact mirror of emu_go/keysc.go (see its header for the RE'd register map):
      +0x00..+0x0B six key-data words: word = col>>1, bit = row + 8*(col&1) (0-based grid
                   of re/KEYMAP.md); +0x0C ctrl (bit15 enable); +0x10 mode (0x200 normal,
                   0x400 detect-only, 0x800 scan-now, 0 off); +0x12 busy(=0);
      +0x14 [15:8] IRQ enable per flag, [7:0] flags W1C: bit3 key-detect, bit1 scan-complete.
    Every scan_period cycles: press edge -> detect flag; while held (+2 scans after
    release) -> scan-complete flag; mode 0x800 -> scan-complete every period. IRQ INTEVT
    0xBE0 level 13 while (flags & enable) != 0."""
    INTEVT = 0xBE0
    LEVEL = 13
    FLAG_SCAN = 0x02
    FLAG_DETECT = 0x08
    DEFAULT_SCAN_PERIOD = 30000

    def __init__(self, name, base, size, scan_period=DEFAULT_SCAN_PERIOD):
        super().__init__(name, base, size)
        self.held = [0] * 6
        self.ctrl = self.mode = self.ie = self.flags = 0
        self.scan_period = scan_period
        self.scan_next = 0
        self.scan_left = 0
        self.was_held = False

    def any_held(self):
        return any(self.held)

    def press(self, row, col):
        if row < 8 and col < 12:
            self.held[col >> 1] |= 1 << (row + 8 * (col & 1))

    def release(self, row, col):
        if row < 8 and col < 12:
            self.held[col >> 1] &= ~(1 << (row + 8 * (col & 1))) & 0xFFFF

    def release_all(self):
        self.held = [0] * 6

    def resume_defaults(self):
        self.ctrl, self.mode, self.ie, self.flags = 0x8000, 0x200, 0x48, 0
        self.scan_left, self.was_held = 0, False

    def read(self, va, size):
        off = va - self.base
        if off < 0x0C:
            w = self.held[off >> 1]
            if size == 2:
                return w
            if size == 1:
                return (w >> 8) if (off & 1) == 0 else (w & 0xFF)
            lo = self.held[(off >> 1) + 1] if (off >> 1) + 1 < 6 else 0
            return (w << 16) | lo
        if off == 0x0C:
            return self.ctrl
        if off == 0x10:
            return self.mode
        if off == 0x12:
            return 0
        if off == 0x14:
            return (self.ie << 8) | self.flags
        return self.regs.get(off, 0)

    def write(self, va, size, val):
        off = va - self.base
        if off == 0x0C:
            self.ctrl = val & 0xFFFF
        elif off == 0x10:
            self.mode = val & 0xFFFF
        elif off == 0x14:
            self.ie = (val >> 8) & 0xFF
            self.flags &= ~(val & 0xFF) & 0xFF
        else:
            self.regs[off] = val

    def tick(self, cpu):
        if cpu.cycles < self.scan_next:
            return
        self.scan_next = cpu.cycles + self.scan_period
        held = self.any_held()
        if (self.ctrl & 0x8000) == 0 or self.mode == 0:
            self.was_held = held
            return
        if held and not self.was_held:
            self.flags |= self.FLAG_DETECT
        if held:
            self.scan_left = 2
        if (self.mode & 0x800) or ((self.mode & 0x200) and (held or self.scan_left > 0)):
            self.flags |= self.FLAG_SCAN
            if not held and self.scan_left > 0:
                self.scan_left -= 1
        self.was_held = held
        if self.flags & self.ie:
            cpu.raise_irq(self.INTEVT, self.LEVEL)
'''

OLD_KEYSC_PY = '''class KEYSC(Region):
    """Keyboard matrix controller. All-keys-released = 0 in every data register."""
    def read(self, va, size):
        return 0
'''

patch("emu/mmio.py", [
    (OLD_KEYSC_PY, KEYSCAN_PY),
    ('            KEYSC("KEYSC", 0xA4080000, 0x1000),\n',
     '            INTCStub("INTC", 0xA4080000, 0x1000),       # interrupt controller (NOT keyboard)\n'),
    ('            KEYSC("KIU_DATA", 0xA44B0000, 0x1000),      # SH7724-style key input data (all 0 = no key)\n',
     '            self.keysc,                                  # key-scan unit = the real key path\n'),
    ('        self.etmu2.bus = self\n        self.regions = [\n',
     '        self.etmu2.bus = self\n        self.keysc = KeyScan("KEYSC", 0xA44B0000, 0x1000)\n        self.regions = [\n'),
    ('''        if not self.timer_period:
            return
        if cpu.cycles >= self.timer_next:''',
     '''        self.keysc.tick(cpu)
        if not self.timer_period:
            return
        if cpu.cycles >= self.timer_next:'''),
    ('''    if "KIU_DATA" not in names:
        mmio.regions.insert(0, KEYSC("KIU_DATA", 0xA44B0000, 0x1000))
''',
     '''    mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") not in ("KIU_DATA", "KEYSC")]
    if not isinstance(getattr(mmio, "keysc", None), KeyScan):
        mmio.keysc = KeyScan("KEYSC", 0xA44B0000, 0x1000)
    mmio.regions.insert(0, mmio.keysc)
    mmio.regions = [r for r in mmio.regions if getattr(r, "name", "") != "INTC"]
    mmio.regions.insert(0, INTCStub("INTC", 0xA4080000, 0x1000))
'''),
    ('  0xA4080000  KEYSC keyboard matrix controller (12 data regs +0..0x16)\n',
     '  0xA4080000  INTC  interrupt controller (IPR/IMR/IMCR) — NOT the keyboard\n'
     '  0xA44B0000  KEYSC/KIU key-scan unit (6 key-data words + ctrl; INTEVT 0xBE0) — see KeyScan\n'),
])
print("done")
