# Syscall reference

All **8,040 syscall numbers** of OS 3.60, searchable. Each row holds facts read from the OS
code by a script: where the function is in 3.60 and 3.80, whether 3.80 changed it, its size,
what it calls, the hardware it seems to touch, and the public names where they exist. How a
number becomes a call is on the [Syscalls](syscalls.md) page.

Search by number (`0x1839`, or `6201` in decimal), by name, by a message the code uses, or by
an address in either OS version (`0x802AEA24`): an address finds the syscall whose function
contains it. Click a row for the details, its calls and its neighbours.

<div id="scr-app" class="scr-app">
<noscript><p>This table needs JavaScript. The data is the file
<code>assets/syscalls360.json</code>, built by <code>re/syscall_ref.py</code>.</p></noscript>
</div>

!!! warning "Heuristics, not documentation"
    Apart from the addresses, every column is a guess made by reading the code statically,
    without running it. "Args?" and "Hardware?" in particular are heuristics: they can miss
    things and they can be wrong. The names come from other projects (below) and from this
    project's own work. Nothing here says what a function's arguments mean.

## What the columns mean

| Column | What it is | How it is found |
|---|---|---|
| Number | The syscall number, in hex and decimal | The index in the table at `0x80687CD0` |
| Name | A public or our own name, with badges: **L** libfxcg has a stub for it, **G** gint uses it, **W** WikiPrizm has a page, **ours** named by this project. Also "empty" (the function is `rts; nop`), "returns *k*" (`rts; mov #k,r0`) and "returns r4" | See [Names](#names) |
| 3.60 address | Where the table entry points in OS 3.60 | Read from the table |
| In 3.80 | **same**: the same bytes; **same, moved**: the same code, apart from parts that depend on where things are; **changed** | See [Comparing with 3.80](#comparing-with-380) |
| Size | Bytes from the entry to the end of the function's code or its last literal-pool constant. A `?` means the code has an indirect jump the script could not follow, so the end may be later | See [Function size](#function-size) |
| Args? | **A guess**: how many of `r4`–`r7` (the first four arguments) the function reads before writing them | See [Likely arguments](#likely-arguments) |
| Calls | How many other functions it calls directly | `bsr`, and `jsr`/`jmp` through a register loaded from the literal pool |
| Hardware? | **A guess**: the peripherals whose register addresses appear among its constants. A dashed chip means only a function it calls (at most two calls deep) has them | See [Hardware](#hardware) |

The details of a row add: the other numbers that lead to the same function, the calls (a
syscall number when the callee is one, otherwise an address), the syscalls that call this one,
the I/O addresses, the nearest **named** syscalls before and after it in 3.60 address order
(syscalls that sit together usually belong to one module), at most one short message the code
points to, and links to the pages of this site that mention it.

## How the facts are found

`re/syscall_ref.py` reads the 3.60 and 3.80 images and writes
`wiki/docs/assets/syscalls360.json`. It keeps addresses, counts, names and these findings
only; the one exception is the short message (40 characters at most).

### Function size

The script follows the code from the entry: conditional and unconditional branches, `rts`
ends a path, a call returns to the next instruction. It also follows `braf` switch tables of
the form `mova table,r0; mov.w @(r0,rN),r0; braf r0`. A branch out of the function is a tail
call. The function ends at whichever is further: the last instruction reached, or the last
literal-pool word the code reads (`mov.l`/`mov.w @(disp,pc)`). It never goes past the next
known function start (another syscall, or any address something calls).

!!! warning "Unconfirmed"
    216 functions have a `jmp`/`braf` whose target is computed, so their end is uncertain
    (marked `?`), and 3 run into a word that is not an instruction. A function that shares its
    end with another, or whose last part is reached only through a computed jump, is measured
    short.

### Comparing with 3.80

The same walk is made in 3.80, starting at the 3.80 table's entry for the same number. Then:

- **same**: the bytes over the function's extent are identical (5,483 numbers);
- **same, moved**: both have instructions at the same offsets, and each pair is equal except
  for the position-dependent parts, which are masked: a `bsr` displacement (the callee moved),
  a `mova`, and a `mov.l @(disp,pc)` whose two loaded values are equal or are both addresses
  of the same kind (both in the OS image, both in RAM, both in on-chip RAM). A
  `mov.w @(disp,pc)` must load the same constant (2,360 numbers);
- **changed**: anything else (197 numbers, 175 of them with a different size).

So 7,843 of the 8,040 numbers lead to the same code in both versions. A changed function may
still do the same job (a fix, a reordered literal pool).

### Likely arguments

The script tracks which registers have been written on every path from the entry. A read of
`r4`–`r7` before any write on some path marks that register as an argument. A call counts as
a read of the callee's own arguments that the caller has not set, so a function that only
passes its arguments on still shows them; this is repeated over the whole call graph until
nothing changes. The count shown is the highest register read: a function that reads `r6`
but not `r5` still takes three arguments.

!!! warning "Unconfirmed"
    It cannot see arguments the function never reads, arguments past the fourth (on the
    stack), or whether a register is a value or a pointer. With all four read, there may be
    more on the stack.

### Hardware

The 32-bit constants of a function's literal pool are matched against the register ranges
of the peripherals this site documents: [keyboard](../peripherals/keyboard.md),
[display, VRAM and DMA](../peripherals/display.md), [BCD ALU](../peripherals/bcd-alu.md),
[timers and RTC](../peripherals/timers.md), [interrupts](../peripherals/interrupts.md),
[battery ADC](../peripherals/battery-adc.md), the CPG, PFC, watchdog and bus controller of the
[memory map](../memory-map.md#peripheral-registers), the serial port, the MMU and caches, the
UBC, flash outside the OS code (`0xA0000000`–`0xA001FFFF`, `0xA0B40000` on) and, with gint's
names for these SH7305 blocks, USB (`0xA4D80000`) and the SPU (`0xFE2FFC00`, `0xFE3FFC00`).

!!! warning "Unconfirmed"
    A function that takes a register address from RAM or computes it is missed: the display
    routines, for instance, take the LCD's `0xB4000000` from a table, so the display
    controller category is empty. A constant that only looks like an address counts anyway.

## Names

| Source | Numbers | Kept |
|---|---|---|
| [libfxcg](https://github.com/Jonimoose/libfxcg) (the PrizmSDK library) | 273 | Its stub names, `SYSCALL(_Name, 0xNNNN)` in `libfxcg/syscalls/*.S` |
| [gint](https://git.planet-casio.com/Lephenixnoir/gint) | 31 | The fx-CG syscalls in `src/kernel/syscalls.S`, plus the TLB-miss handler (`tlbh.S`) and `0x1E58` (`osmenu.c`) |
| [WikiPrizm](https://prizm.cemetech.net/Syscalls/) | 247 | Links only, to its pages that exist: the page titles and the "Syscall index" numbers in its index. None of its text |
| This project | 42 | Short descriptions from the pages of this site, and the pages that mention each number |

In all, 316 numbers have a name. The name shown is libfxcg's, then WikiPrizm's page title,
then ours, then gint's label; the details list all of them. libfxcg's placeholder names
(`Test` and the number, e.g. `Test127E`) are not used as names. `re/syscall_names.py` gathers
them (it downloads each source once into the git-ignored `re/_cache/`) and writes
`re/syscall_names.json`.

Hand-written notes for single syscalls (a prototype, a description, and how sure we are:
tested, read from the code, or guessed) go in `re/syscall_docs.json`, keyed by number;
`re/syscall_ref.py` merges them into the table. It is empty so far.

## In the emulator

- The emulator does not use this table: an add-in's syscall runs the OS's own code, as on the
  calculator (see [Syscalls](syscalls.md#in-the-emulator)).
- Scripts: `re/syscall_names.py` (names), `re/syscall_ref.py` (the data). Rebuild with
  `python3 re/syscall_names.py` (only when the sources should be fetched again: add
  `--refresh`), then `python3 re/syscall_ref.py`. Both need the OS images in the git-ignored
  `os/` folder.
