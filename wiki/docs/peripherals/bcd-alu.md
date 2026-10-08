# BCD ALU (`0xA4CB0000`)

The SH7305 has a small, undocumented hardware unit that adds and subtracts **packed BCD**
numbers, 8 decimal digits (one 32-bit word) at a time. The SH-4A core has no decimal
instructions, so the OS's number library uses this unit for every decimal addition and
subtraction, and also inside rounding. Software handles the rest: shifting, masking,
exponents.

!!! success "Verified on hardware"
    Every behaviour on this page was measured on a real fx-CG50 with two probe add-ins
    (June 2026). The raw captures are in the repository under `os/devic_probes/`.

## Why it matters

If this unit is missing, nothing crashes: the result register just reads 0. The OS then
rounds every number to zero, and **every calculation displays `0`** while the correct value
sits in memory. That was the symptom that led to finding it.

## Registers

The four registers repeat every `0x10` bytes. The OS uses the copy at `+0x10`.

| Address (OS uses) | Offset | Register | Access |
|---|---|---|---|
| `0xA4CB0010` | `+0x0` | Command (write) / status (read) | 16-bit write |
| `0xA4CB0014` | `+0x4` | Operand A | 32-bit |
| `0xA4CB0018` | `+0x8` | Operand B | 32-bit |
| `0xA4CB001C` | `+0xC` | Result | 32-bit read |

- Writing the command runs the operation. The result is ready **immediately**: there is no
  busy bit, and reading it 0 or 16 instructions later gives the same value.
- The operands **stay** in their registers. Writing the same command again recomputes the
  same result.
- Reading the command register shows the carry/borrow flag: `0x10040000` when it is set,
  `0x00010000` when it is clear.

## Commands

There is **one flag bit**, shared by carry and borrow. Each command says what to feed in as
carry/borrow, and the operation's carry-out (add) or borrow-out (subtract) goes back into
the flag.

```text
operation = (cmd & 1) ? A + B : A - B
flag in   = (cmd & 4) ? 1 : (cmd & 2) ? flag : 0
bit 3 is ignored, so commands 8–15 are copies of 0–7
```

| Command | Operation | Use |
|---|---|---|
| 0 | A − B | lowest word of a subtraction |
| 1 | A + B | lowest word of an addition |
| 2 | A − B − flag | next words of a subtraction |
| 3 | A + B + flag | next words of an addition |
| 4 | A − B − 1 | subtraction starting with a borrow |
| 5 | A + B + 1 | addition starting with a carry (the OS never uses it) |
| 6, 7 | same as 4, 5 | |

A number longer than 8 digits is processed **lowest word first**: command 1 then 3, 3, …
for an addition, command 0 then 2, 2, … for a subtraction.

## Examples (from the real calculator)

| Command | A | B | Result | Flag after |
|---|---|---|---|---|
| 1 | `12345678` | `11111111` | `23456789` | 0 |
| 1 | `99999999` | `00000001` | `00000000` | 1 (carry) |
| 0 | `00000000` | `12345678` | `87654322` | 1 (borrow) |
| 4 | `00000000` | `00000000` | `99999999` | 1 |
| 4 | `00000005` | `00000003` | `00000001` | 0 |

Proof that carry and borrow share one flag: after command 4 leaves the flag set,
command 3 with A = 5, B = 3 gives **9**, not 8. The borrow from the subtraction was used as
the carry for the addition.

## In the OS (3.60)

- Two routines drive the unit: `0x80072FC8` issues commands 1, 3, 3 (a three-word addition)
  and `0x8007306E` issues 0, 2, 2 (a three-word subtraction).
- They are reached from the decimal number code, e.g. `0x80072E78`, and from the rounding
  step of the display formatter (`0x8004B2B0` → `0x8005DC06` → … → `0x80072FC8`).
- The pointers to the registers are stored as literals at `0x80073000`–`0x8007300C`.

## In the emulator

`bcdALU` in [`emu_go/mmio.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmio.go)
and `BCDALU` in `emu/mmio.py`; tests in `emu_go/bcdalu_test.go`.
