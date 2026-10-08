# Keyboard (KEYSC, `0xA44B0000`)

The keys sit on a grid of wires: a **matrix** of rows and columns. Pressing a key connects one
row to one column. The SH7305 has a **key-scan controller** (KEYSC) that checks the matrix in
hardware and stores which keys are down in six 16-bit registers. When a key goes down it raises
an interrupt, and the OS's keyboard interrupt handler (ISR) reads those registers, works out
which key it is, and handles debouncing, key repeat and the key queue.

The OS does not drive the matrix lines itself. Everything goes through this unit. (Some older
notes put the key scanner at `0xA4080000`; that address is the
[interrupt controller](interrupts.md).)

!!! success "Verified on hardware"
    A probe add-in (`keyprobe`, September 2026) read the KEYSC registers at `0xA44B0000` and the
    keyboard's interrupt settings at `0xA4080000` on a real fx-CG50, with the OS's own
    configuration live, while RIGHT was held and again with no key held. The raw capture is in
    the repository under `os/devic_probes/`.

## The key matrix

Software sees **8 rows** (0–7) and **12 columns** (0–11), both counted from 0. The six key-data
words each cover two columns:

```text
word = col >> 1             the word at 0xA44B0000 + 2 * word
bit  = row + 8 * (col & 1)  low byte = even column, high byte = odd column
```

A bit is 1 while its key is held. For example RIGHT is row 1, column 7: word 3, bit 9, so
word 3 reads `0x0200`.

Where each key sits (row down, column across). Columns 10 and 11 hold no keys of the keypad:

| Row | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 |
|---|---|---|---|---|---|---|---|---|---|---|
| **0** | AC/ON | | | | | | | | | |
| **1** | | | | | | → | tan | RIGHT | UP | F6 |
| **2** | | EXE | − | ÷ | | , | cos | DOWN | LEFT | F5 |
| **3** | | (−) | + | × | DEL | ) | sin | EXIT | MENU | F4 |
| **4** | | ×10ˣ | 3 | 6 | 9 | ( | ln | ^ | VARS | F3 |
| **5** | | . | 2 | 5 | 8 | S⇔D | log | x² | OPTN | F2 |
| **6** | | 0 | 1 | 4 | 7 | a b/c | X,θ,T | ALPHA | SHIFT | F1 |
| **7** | | | | | | | | | | |

That is all 50 keys, each on its own position. AC/ON is the only key in row 0 and in column 0;
row 7 is unused.

!!! success "Verified on hardware"
    With RIGHT held, the six words read `0000 0000 0000 0200 0000 0000`: word 3, bit 9, as the
    formula gives.

!!! info "Verified in the emulator"
    The emulator sets bits by this formula and the OS's own ISR decodes them: a tap on DOWN
    moves the MAIN MENU cursor, EXE opens Run-Matrix and MENU returns from it, and a held RIGHT
    auto-repeats. Every key's position was also checked by typing into Run-Matrix and reading
    the result (the `ver` column of `re/KEYMAP.md`; `F2`–`F5`, VARS, →, S⇔D, a b/c and AC/ON
    have not been pressed one by one).

!!! warning "Unconfirmed"
    Only RIGHT has been read on the calculator. The formula for the other keys comes from the
    OS's decoder (below), which reads the words exactly this way. The OS's position table also
    has entries in column 11, at rows 1, 3, 5 and 6. Row 3, column 11 gives the key code
    `0x7D00`, marked as a diagnostic entry in `re/KEYMAP.md`. No physical key is known at any of
    them.

## Registers

The OS accesses every register as 16 bits.

| Address | Offset | Register | OS value |
|---|---|---|---|
| `0xA44B0000`–`0xA44B000A` | `+0x00`–`+0x0A` | Six key-data words (see above) | read only |
| `0xA44B000C` | `+0x0C` | Control. Bit 15 = unit enabled | `0x8000` |
| `0xA44B000E` | `+0x0E` | Configuration, meaning unknown | `0x8042` |
| `0xA44B0010` | `+0x10` | Mode (below) | `0x0200` |
| `0xA44B0012` | `+0x12` | Bit 0 = scan in progress | read only |
| `0xA44B0014` | `+0x14` | Bits 15–8: interrupt enable per flag. Bits 7–0: status flags | `0x4800` idle |
| `0xA44B0016` | `+0x16` | Configuration, meaning unknown | `0x0000` |
| `0xA44B0018` | `+0x18` | Configuration, meaning unknown | `0x00C8` |
| `0xA44B001A` | `+0x1A` | Configuration, meaning unknown | `0x0FFF` |
| `0xA44B001C` | `+0x1C` | Configuration, meaning unknown | `0x00FF` |

!!! success "Verified on hardware"
    The calculator read `+0x0C` … `+0x1E` as
    `8000 8042 0200 0002 7600 0000 00C8 0FFF 00FF 0002` with RIGHT held: the values the OS
    writes, and `+0x14` = `0x7600` (the "key held" interrupt set, no flag pending).
    `+0x12` and `+0x1E` both read `0x0002`.

!!! warning "Unconfirmed"
    The meanings of control bit 15 and of `+0x12` bit 0 come from how the OS uses them: it
    writes control = 0 to switch the unit off, and its busy test (`0x801E508C`) looks only at
    bit 0 of `+0x12`. What bit 1 of `+0x12` and the register at `+0x1E` mean is unknown.

### Mode (`+0x10`)

| Value | Meaning |
|---|---|
| `0x0200` | Normal: detect a key press, then scan while keys are held. The OS's usual mode. |
| `0x0800` | Scan now. The OS uses it for a one-off scan it waits for. |
| `0x0400` | Written by the OS just before it switches the unit off. Meaning unknown (the emulator treats it as "detect only"). |
| `0x0000` | Off. |

!!! warning "Unconfirmed"
    Only `0x0200` has been seen on the calculator. The other meanings come from how the OS uses
    them.

### Status flags (`+0x14`)

The low byte holds flags and the high byte enables an interrupt for each flag. Write a 1 to a
flag to clear it. Writing the flags you just read, with the enable byte you want, clears those
flags and sets the enables in one write. That is what the OS does.

| Bit | Flag |
|---|---|
| 3 | Key detected: the matrix went from no key to some key |
| 1 | Scan complete: the key-data words are fresh |

The OS enables `0x48` (flags 3 and 6) while waiting for a key, and `0x76` (flags 1, 2, 4, 5
and 6) while one is held or just released.

!!! success "Verified on hardware"
    With RIGHT held, only flag 1 ever set (flag 3 did not, as the key was already down when
    counting started). No other flag bit appeared.

!!! warning "Unconfirmed"
    Flags 2, 4, 5 and 6 have never been seen set. The ISR treats flags 5 and 6 as "go back to
    waiting for a key", but what sets them is unknown.

### Interrupt

The unit raises **INTEVT `0xBE0`** whenever a flag is set and enabled. At the
[interrupt controller](interrupts.md) its priority is **IPRF bits 15–12**, which the OS sets to
**13**, and its mask is **IMR5 bit 7**.

!!! success "Verified on hardware"
    In the OS, the calculator read IPRF = `0xD000` (priority 13) and IMR5 = `0x77` (bit 7
    clear: the keyboard interrupt is on).

## When the unit scans

- In normal mode, a key going down sets flag 3 at once.
- While any key is held, the unit scans every **30.3 ms** (33 times a second), setting flag 1
  after each scan.
- With no key held, it does **not** scan at all.

!!! success "Verified on hardware"
    With RIGHT held, flag 1 set 132 times in 4 seconds (30.3 ms per scan). With nothing held,
    it set 0 times in 2 seconds, and flag 3 never set. Both counts were taken with the OS's
    configuration and the keyboard interrupt masked, so the OS could not clear the flags first.

!!! warning "Unconfirmed"
    The key-data words seem to hold the result of the **last scan**, not the live state of the
    keys. A debugger add-in that read the words directly on the calculator waited forever for
    the EXE key that launched it to be released. It worked once it ran a scan the way the OS
    does (`0x801E56FA`, below). The emulator does not model this yet: its words always show
    the keys held right now.

## How the OS reads keys (3.60)

### Setup

`0x801E51C8` sets the unit up. It masks the keyboard interrupt, then writes, in order: control
`0x8000`, flags `0x00FF` (clear all), interrupt enable `0x48`, `+0x0E` = `0x8042`,
`+0x18` = `0xC8`, `+0x16` = 0, `+0x1A` = `0x0FFF`, `+0x1C` = `0xFF` and mode `0x0200`. Last, it
sets IPRF bits 15–12 to 13 and unmasks the interrupt.

!!! success "Verified on hardware"
    All these KEYSC and IPRF values read back on a running calculator. The interrupt enable read
    `0x76` instead of `0x48` because a key was held: the ISR had already switched it.

!!! warning "Unconfirmed"
    The setup also writes `0xFF` to the PFC byte at `0xA40501C6`, and the scan on demand below
    writes 0 there while the unit is off. What that byte does is not known.

### The interrupt handler

The OS's interrupt entry looks INTEVT `0xBE0` up in its handler table in IL RAM (at
`0xFD8010C8`) and reaches `0x801DEDCC`, which jumps to the keyboard ISR at **`0x801E4C00`**.

The ISR masks the keyboard interrupt while it runs, reads the flags and keeps a small state
machine at `0x8C090C00`:

| State | Waits for | Then |
|---|---|---|
| 0, idle | flag 3 | Reads the six words, decodes the key, posts a **press** event, goes to state 1 |
| 1, held | flag 1 | If the same key is still down, maybe posts a **repeat**; if not, posts a **release** and goes to state 4 |
| 4, released | flag 1 | If the matrix is empty, back to state 0; if a key is down, posts a new press |

The decoder **`0x801E49BC`** walks the six words and returns how many bits are set and the
(row, column) of the first one. The ISR then looks the position up in a table at
**`0x8068FA70`**: entry `[col * 8 + row]` holds `(row + 1) << 8 | (col + 1)`, the key's
position counted from 1. Events go out through `0x801E504C` (type 1 = press, 2 = repeat,
3 = release). At the end the ISR writes the flags it read back to `+0x14` to clear them, with
enable `0x48` (idle) or `0x76` (key held), and unmasks the interrupt.

The ISR follows **one key at a time**: the first set bit in word order, row order, even column
before odd. A second key pressed while one is held is not reported.

!!! info "Verified in the emulator"
    A DOWN tap at the MAIN MENU reaches `0x801E4C00` 39 instructions after the press, and the
    app's key decoder `0x801952CC` runs after it. No shortcut into the OS's
    key queue is used.

!!! warning "Unconfirmed"
    The state table, the event types and the one-key rule are read from the OS's code.

### Key repeat

Repeat is done by the OS, counting scans, so the 30.3 ms scan period is also the repeat clock.

- On a press the ISR loads a countdown from `0x8C08BA38`. When it runs out, the next scan posts
  a repeat and reloads the countdown from `0x8C08BA3C`.
- At the MAIN MENU these hold **20** and **0**: the first repeat comes after 20 scans, then one
  repeat per scan. With the measured scan rate that is about **0.6 s**, then **33 repeats a
  second**.
- `0x801E5224` stores new values into both words; `0x801E522E` reads them back.

!!! info "Verified in the emulator"
    Holding RIGHT at the MAIN MENU auto-repeats, and stops when it is released. When the
    emulator runs fast enough to keep up, the menu cursor moves 33 times a second, one move per
    scan (probe test `TestRepeatRateProbe`).

!!! warning "Unconfirmed"
    Read from the OS's code, not measured on the calculator:

    - In this default mode only the **four arrow keys** repeat (rows 1–2, columns 7–8), and
      only for 2000 scans (about a minute) after the press.
    - A second mode (set through `0x801E4FA2`, flag `0x8C090C30`) handles every key, not just
      the arrows, with its own interval at `0x8C090C34`.
    - Another switch (`0x8C08B98C`, set by `0x801E52EC`, cleared by `0x801E52F4`) slows a held
      key down: after 120 scans the ISR only acts on every fifth scan.

    Which apps use the second mode or the throttle is not known.

### A scan on demand

`0x801E56FA` checks keys without the interrupt. It switches the unit off, reprograms it with
its interrupt enables cleared, sets mode `0x0800`, waits (with a timeout) for flag 1, reads the
six words, then switches the unit off again (mode `0x0400`, control 0). It returns 1 when three
keys named by the caller are down and EXE is up.

!!! warning "Unconfirmed"
    Read from the OS's code. Who calls it, and for which keys, is not known.

### From position to key code

Applications get keys from a getkey routine whose decoder is at `0x801952CC`. It turns the
position into a "raw" key number (`0x00`–`0x3C`, table at `0x805FF7EC`), then into the final
**key code** through a table chosen by the modifier state. SHIFT and ALPHA are ordinary keys in
the matrix: pressing one changes the table used for the **next** key. The codes are the familiar
ones of the PrizmSDK/libfxcg headers: digits and letters are ASCII, `0x75xx` are control keys
(EXE `0x7534`, EXIT `0x7532`, MENU `0x7533`, the arrows `0x7542`–`0x7547`).

!!! info "Verified in the emulator"
    Typing into Run-Matrix gives the expected characters for every verified key, including
    SHIFT then `sin` (→ `sin⁻¹`) and ALPHA then `X,θ,T` (→ `A`). MENU at (3, 8) returns from
    Run-Matrix to the MAIN MENU. EXIT is (3, 7): an earlier keypad had the two swapped.
    `re/audit_keymap.py` checks every key's code against libfxcg's names.

??? note "Every key: position, KEYSC bit and key code"
    "Word.bit" is the key-data word and the bit in it. The key code is the one with no modifier.

    | Key | Row, col | Word.bit | Key code |
    |---|---|---|---|
    | AC/ON | 0, 0 | 0.0 | `0x753F` |
    | EXE | 2, 1 | 0.10 | `0x7534` |
    | (−) | 3, 1 | 0.11 | `0x0087` |
    | ×10ˣ | 4, 1 | 0.12 | `0x000F` |
    | . | 5, 1 | 0.13 | `0x002E` |
    | 0 | 6, 1 | 0.14 | `0x0030` |
    | − | 2, 2 | 1.2 | `0x0099` |
    | + | 3, 2 | 1.3 | `0x0089` |
    | 3 | 4, 2 | 1.4 | `0x0033` |
    | 2 | 5, 2 | 1.5 | `0x0032` |
    | 1 | 6, 2 | 1.6 | `0x0031` |
    | ÷ | 2, 3 | 1.10 | `0x00B9` |
    | × | 3, 3 | 1.11 | `0x00A9` |
    | 6 | 4, 3 | 1.12 | `0x0036` |
    | 5 | 5, 3 | 1.13 | `0x0035` |
    | 4 | 6, 3 | 1.14 | `0x0034` |
    | DEL | 3, 4 | 2.3 | `0x7549` |
    | 9 | 4, 4 | 2.4 | `0x0039` |
    | 8 | 5, 4 | 2.5 | `0x0038` |
    | 7 | 6, 4 | 2.6 | `0x0037` |
    | → | 1, 5 | 2.9 | `0x000E` |
    | , | 2, 5 | 2.10 | `0x002C` |
    | ) | 3, 5 | 2.11 | `0x0029` |
    | ( | 4, 5 | 2.12 | `0x0028` |
    | S⇔D | 5, 5 | 2.13 | `0x755E` |
    | a b/c | 6, 5 | 2.14 | `0x00BB` |
    | tan | 1, 6 | 3.1 | `0x0083` |
    | cos | 2, 6 | 3.2 | `0x0082` |
    | sin | 3, 6 | 3.3 | `0x0081` |
    | ln | 4, 6 | 3.4 | `0x0085` |
    | log | 5, 6 | 3.5 | `0x0095` |
    | X,θ,T | 6, 6 | 3.6 | `0x7531` |
    | RIGHT | 1, 7 | 3.9 | `0x7545` |
    | DOWN | 2, 7 | 3.10 | `0x7547` |
    | EXIT | 3, 7 | 3.11 | `0x7532` |
    | ^ | 4, 7 | 3.12 | `0x00A8` |
    | x² | 5, 7 | 3.13 | `0x008B` |
    | ALPHA | 6, 7 | 3.14 | `0x7537` |
    | UP | 1, 8 | 4.1 | `0x7542` |
    | LEFT | 2, 8 | 4.2 | `0x7544` |
    | MENU | 3, 8 | 4.3 | `0x7533` |
    | VARS | 4, 8 | 4.4 | `0x7540` |
    | OPTN | 5, 8 | 4.5 | `0x7538` |
    | SHIFT | 6, 8 | 4.6 | `0x7536` |
    | F6 | 1, 9 | 4.9 | `0x753E` |
    | F5 | 2, 9 | 4.10 | `0x753D` |
    | F4 | 3, 9 | 4.11 | `0x753C` |
    | F3 | 4, 9 | 4.12 | `0x753B` |
    | F2 | 5, 9 | 4.13 | `0x753A` |
    | F1 | 6, 9 | 4.14 | `0x7539` |

    SHIFT and ALPHA codes for every key are in `re/KEYMAP.md`, generated from the OS's tables.

## gint add-ins

gint, the kernel most add-ins are built on, masks the OS's keyboard interrupt and reads the
key-data words itself from a 128 Hz timer (in the emulator it picks ETMU5; see
[timers](timers.md)). When it hands the machine back to the OS, it restores the OS's interrupt
settings.

!!! info "Verified in the emulator"
    gint add-ins read keys in the emulator with the same KEYSC model the OS uses.

## In the emulator

- The unit: `keyscUnit` in
  [`emu_go/keysc.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/keysc.go), and
  `KeyScan` in
  [`emu/mmio.py`](https://github.com/hexbinoct/casio-cg50/blob/main/emu/mmio.py) (the Python
  reference). The interrupt's priority and mask bit:
  [`emu_go/intc.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/intc.go).
- Time in the emulator is counted in instructions. The scan period is the host's instructions
  per second divided by 33 (`MMIOBus.SetInstrPerSecond`), 750,000 instructions by default.
- Hosts press keys with `Emulator.KeyDown`/`KeyUp` (a real hold, so the OS repeats it) or
  `InjectKey` (a tap: held for 3 scans, then a gap of 3 scans before the next queued key). A tap
  shorter than 3 scans is stretched to 3 so the OS always sees it.
- Save-states store the control, mode and interrupt-enable registers.
- Known differences from the calculator: the key-data words are live, not latched at the last
  scan, and `+0x12` reads 0 (the calculator reads 2; the OS's busy test looks only at bit 0).
- Tests in
  [`emu_go/keysc_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/keysc_test.go):
  `TestKeyscProtocol` (the register protocol as the OS drives it), `TestKeyscMatrixLayout`,
  `TestKeyscOracleTranscript` (Go and Python give the same transcript;
  [`emu/keysc_selftest.py`](https://github.com/hexbinoct/casio-cg50/blob/main/emu/keysc_selftest.py)
  writes it), and, with the OS image, `TestKeyscMenuTap`, `TestKeyscFastTapStillLands`,
  `TestKeyscHoldRepeats` and `TestMenuReturnsFromApp`.
- The key map: [`re/KEYMAP.md`](https://github.com/hexbinoct/casio-cg50/blob/main/re/KEYMAP.md),
  generated from the OS image by `re/dump_keymap.py` and checked by `re/audit_keymap.py`.
