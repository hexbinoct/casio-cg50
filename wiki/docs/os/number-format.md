# Number format

The OS stores every number as **packed BCD** (binary-coded decimal: one decimal digit per
4 bits), with 15 significant digits and a decimal exponent from −99 to +99. The CPU has no
FPU and no decimal instructions, so all arithmetic is done in software, with the
[BCD ALU](../peripherals/bcd-alu.md) adding and subtracting eight digits at a time. This page
describes the stored format, how arithmetic uses the ALU, and the chain of routines that
turns a stored value into the digits on the screen.

!!! info "Verified in the emulator"
    Every byte pattern on this page is what OS 3.60 itself stored for a result typed into
    Run-Matrix, captured at the entry of the result formatter `0x800FC5A4`. Probes:
    `TestNumFormat`, `TestNumFraction`, `TestNumArith` in `emu_go/numfmt_probe_test.go`.

## A real number

A real number takes **12 bytes**: three 32-bit words, read and written as words by the
arithmetic code. Counting 4-bit nibbles from the start of byte 0:

| Nibbles | Bytes | Meaning |
|---|---|---|
| 0 | byte 0, high half | Bit 3 (`0x80` of byte 0): an imaginary part follows ([below](#complex-numbers)). Bits 2–0: the sign and the exponent's hundreds digit: **0** or **1** for a positive number, **5** or **6** (0 or 1, plus 5) for a negative one |
| 1–2 | byte 0 low half, byte 1 high half | The exponent's tens and units digits |
| 3–17 | byte 1 low half to byte 8 | The 15 digits of the mantissa, most significant first |
| 18–23 | bytes 9–11 | 0 in every stored value seen |

The three exponent digits (with the 5 removed) form a number **E = 100 + e**, where e is
the power of ten. The mantissa has the decimal point after its first digit, so the value is
d₀.d₁d₂…d₁₄ × 10^e. The first digit is never 0, except in zero itself, which is twelve 0
bytes.

So E runs from 001 (10^−99) to 199 (10^99), which is the calculator's range.

### Examples

| Typed | Bytes 0–8 (bytes 9–11 are 0) | E | Value |
|---|---|---|---|
| `98765` | `10 49 87 65 00 00 00 00 00` | 104 | 9.8765 × 10^4 |
| `(-)98765` | `60 49 87 65 00 00 00 00 00` | 104, negative | −9.8765 × 10^4 |
| `0.5` | `09 95 00 00 00 00 00 00 00` | 099 | 5 × 10^−1 |
| `0.000123` | `09 61 23 00 00 00 00 00 00` | 096 | 1.23 × 10^−4 |
| `(-)0.000123` | `59 61 23 00 00 00 00 00 00` | 096, negative | −1.23 × 10^−4 |
| `1.5E-12` | `08 81 50 00 00 00 00 00 00` | 088 | 1.5 × 10^−12 |
| `9E99` | `19 99 00 00 00 00 00 00 00` | 199 | 9 × 10^99 |
| `1E-99` | `00 11 00 00 00 00 00 00 00` | 001 | 1 × 10^−99 |
| `123456789012345` | `11 41 23 45 67 89 01 23 45` | 114 | all 15 digits used |
| `1÷3` | `09 93 33 33 33 33 33 33 33` | 099 | fifteen 3s |
| `0` | `00 00 00 00 00 00 00 00 00` | | zero |

The value keeps all 15 digits even though the screen shows 10 (`0.3333333333` for 1÷3).

## Complex numbers

A complex number is **two** 12-byte numbers, real part first, 24 bytes in all. Bit 7 of the
real part's first byte says that the imaginary part is there. `2+3i` is stored as
`90 02 00 …` (real part 2, with bit 7 set) followed at byte 12 by `10 03 00 …` (imaginary
part 3).

Routines that take a result handle 24 bytes: `0x8004C69C` copies 24 bytes, and the rounding
routine `0x8004B270` rounds the real part, then the part at byte 12 only if bit 7 is set.

## Fractions

A fraction result (entered with the a b/c key) has **0xA in nibble 1**. The rest is a
string of digits in which 0xA separates the parts:

| Nibble | Meaning |
|---|---|
| 0 | 1 for a positive fraction, 6 for a negative one |
| 1 | `A`: this is a fraction |
| 2 | The length of the digit string, minus 1 |
| 3 onwards | `numerator A denominator`, or `whole A numerator A denominator` |

| Typed | Bytes | Digit string | Screen |
|---|---|---|---|
| 1⁄3 | `1a 21 a3` | 1 A 3 | 1⁄3 |
| 2⁄4 | `1a 21 a2` | 1 A 2 (reduced) | 1⁄2 |
| −5⁄7 | `6a 25 a7` | 5 A 7 | −5⁄7 |
| 12⁄7 | `1a 41 a5 a7` | 1 A 5 A 7 (1 5⁄7) | 12⁄7 |
| 1⁄12 | `1a 31 a1 20` | 1 A 1 2 | 1⁄12 |
| 123⁄1000 | `1a 71 23 a1 00 00` | 1 2 3 A 1 0 0 0 | 123⁄1000 |

The OS stores 12⁄7 as the mixed number 1 5⁄7 and shows it as 12⁄7 because the status bar's
"d/c" setting asks for improper fractions.

## Unpacking a number: syscall `0x0160`

Most number routines first unpack a value with `0x8004B580` (syscall `0x0160`), called with
r4 = an output block and r5 = the value. The output block:

| Offset | Size | Contents |
|---|---|---|
| `+0x00` | 4 | The exponent e as a signed integer (E − 100 for a decimal) |
| `+0x04` | 4 | The sign: +1 or −1 |
| `+0x08` | 4 | 0 for a decimal; for a fraction, the length of its digit string |
| `+0x0C` | 15 | One digit per byte (for a fraction, 10 marks a separator) |

It returns 0, or −1 if the value is malformed: a digit above 9 (above 10 in a fraction), or
bits 6–4 of byte 0 equal to 7.

!!! warning "Unconfirmed"
    The block layout is read from the routine's code. The emulator runs it on every result
    typed above (the probes record its inputs).

## Arithmetic and the BCD ALU

The arithmetic works on numbers held in **three 32-bit words**, the 12-byte format above.
Four short routines add or subtract two such numbers with the BCD ALU, lowest word first.
One number is in r13, r12, r11 and the other in r9, r8, r7, with r13 and r9 holding the
lowest eight digits:

| Routine | ALU commands | Operation |
|---|---|---|
| `0x80072F80` | 1, 3, 3 | (r9, r8, r7) ← (r13, r12, r11) + (r9, r8, r7) |
| `0x80072FC8` | 1, 3, 3 | (r13, r12, r11) ← (r13, r12, r11) + (r9, r8, r7) |
| `0x8007306E` | 0, 2, 2 | (r9, r8, r7) ← (r9, r8, r7) − (r13, r12, r11) |
| `0x800730B8` | 0, 2, 2 | (r13, r12, r11) ← (r13, r12, r11) − (r9, r8, r7) |

Around them, the number code from `0x80073000` to about `0x80074800` issues single commands
of its own. For example, `0x800737F2` writes command 0, or command 4 (subtract with a
borrow in) when r7 is not positive. In all, the
OS image holds 34 sets of pointers to the unit's four registers (`re/find_periph.py`).

These are the ALU commands the OS issued between pressing EXE and the result reaching the
formatter:

| Typed | Three-word routines | Single commands |
|---|---|---|
| `12+34` | `0x80072F80` × 2 | 3 |
| `50-8` | `0x80072F80` × 1, `0x800730B8` × 1 | 3 |
| `7×8` | `0x80072F80` × 6 | 5 |
| `1÷7` | `0x8007306E` × 45, `0x80072F80` × 3 | 4, one of them command 4 |
| `2+3i×1` | `0x80072F80` × 12 | 16 |

Multiplication and division use many additions and subtractions. Formatting the result
then uses the ALU again: the rounding step calls `0x80072FC8`.

## From a stored value to the screen

Run-Matrix calls the result formatter `0x800FC5A4` from `0x800FCC90`, with r4 pointing to
the 24-byte result. The routines below run in this order for every result; "Called from" is
the call instruction (the return address seen at entry, minus 4).

| Step | Address | Syscall | Called from | What it does |
|---|---|---|---|---|
| 1 | `0x800FC5A4` | | `0x800FCC90` | Result formatter |
| 2 | `0x800F790E` | `0x0A55` | `0x800FC8DE` | Reads setup items `0x57`, `0x2B`, `0x2A` and `0x13`, rewrites them from a settings block at `0x8C08B8A0`, then calls step 3 |
| 3 | `0x8004C21A` | `0x0170` | `0x800F7A64` | Number to text |
| 4 | `0x8004C69C` | `0x0174` | `0x8004C402` | Copies the 24-byte value and rounds it with step 5; an argument picks one of four cases |
| 5 | `0x8004B270` | `0x015E` | `0x8004C6F6` | Rounds the real part, and the imaginary part if bit 7 is set. If rounding fails, it writes `0xF0` into the first byte |
| 6 | `0x8004B2B0` | `0x015F` | `0x8004B278` | Rounds one part: reads setup item `0x3D`, unpacks with syscall `0x0160` (fractions are left alone), and calls step 7 |
| 7 | `0x8005DC06` | `0x036D` | `0x8004B3E6` | Through `0x80073F38` to the ALU adder `0x80072FC8` |

Before step 2, the formatter also reaches steps 4–7 once through `0x8028D418`.

The setup items are bytes in a table whose address is kept at `0xFD801D98`. `0x8002F310`
reads item r4 and `0x8002F31A` writes it. Syscalls `0x1A2F` and `0x1A2E` jump to these two.

With the default "Norm 1" display, rounding keeps **10 significant digits**:

| Typed | Rounded value | Screen |
|---|---|---|
| `1÷3` | `09 93 33 33 33 33 30 00 00` | `0.3333333333` |
| `123456789012345` | `11 41 23 45 67 89 00 00 00` | `1.23456789×10^14` |

If the ALU is missing, step 7 gets 0 from it and the rounded value becomes 0. That is the
"every result shows 0" fault described on the [BCD ALU](../peripherals/bcd-alu.md#why-it-matters)
page.

!!! warning "Unconfirmed"
    - What setup item `0x3D` holds, and how step 6 picks 10 digits from it.
    - What the four cases of step 4 are; the display modes Norm, Fix, Sci and Eng are the
      obvious guess.
    - What the settings block at `0x8C08B8A0` is, and whether step 2 restores the four
      setup items afterwards.
    - What bytes 9–11 of a value are for. They are 0 in every stored value, yet the
      arithmetic reads them as part of the third word.
    - What a first nibble of 2, 3 or 4 means. The unpacker accepts them as positive
      without changing the exponent; the OS was never seen storing one.

## In the emulator

There is no number code in the emulator: the OS's own routines do the arithmetic and the
formatting. The emulator provides the BCD ALU (`bcdALU` in
[`emu_go/mmio.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmio.go),
`BCDALU` in `emu/mmio.py`, tests in `emu_go/bcdalu_test.go`). Before the ALU was modelled,
every result displayed as 0.

The probes behind this page, in `emu_go/numfmt_probe_test.go` (`-tags probe`):

- `TestNumFormat` types numbers and records what reaches the formatter and the unpacker,
  plus the call chain.
- `TestNumFraction` does the same for fractions.
- `TestNumArith` records every ALU command written between EXE and the formatter.
