# Flash and storage memory

The fx-CG50 keeps everything that must survive without power in one **32 MB NOR flash** at
physical address 0: the boot code, the OS, and the storage memory (`fls0`) with your files and
add-ins. The layout is on the [Memory map](memory-map.md#flash) page. This page is about how
the OS talks to the chip: the command sequences it writes, how it waits for a program or an
erase to finish, why that code has to run from IL RAM, and how the storage memory sits on top.

All addresses are OS 3.60 runtime addresses. The OS reaches the chip through the uncached P2
window, so flash offset `x` is written as `0xA0000000 + x`.

## Talking to the chip

A NOR flash reads like ROM. To change it, software writes **commands**: short sequences of
16-bit writes to fixed addresses. Every command starts with the same two "unlock" writes.
The OS uses the classic AMD/Spansion command set, written as 16-bit stores with the
addresses as byte offsets (the chip's word addresses `0x555` and `0x2AA`):

| Command | Writes, in order (flash offset ← value) |
|---|---|
| Unlock (starts every command) | `0xAAA` ← `0xAA`, `0x554` ← `0x55` |
| Reset to reading | `0xAAA` ← `0xF0` after the unlock, or `0xF0` to any address |
| Buffered program | unlock, target ← `0x25`, sector ← *word count − 1*, the words to their addresses, sector ← `0x29` (confirm) |
| Word program in "unlock bypass" mode | unlock, `0xAAA` ← `0x20` (enter), then for each word: `0x000` ← `0xA0`, the word to its address; at the end `0x90`, then `0xF0` (exit) |
| Sector erase (128 KB) | unlock, `0xAAA` ← `0x80`, unlock again, sector ← `0x30` |
| Read the IDs | unlock, `0xAAA` ← `0x90`, then read words `0x00`, `0x02`, `0x1C`, `0x1E`; reset after |
| Read the CFI table | `0x0AA` ← `0x98` (no unlock), then read; reset after |

Programming can only turn 1 bits into 0 bits. Only an erase sets bits back to 1, and it works
on a whole **sector**: the OS always erases 128 KB at a time (its erase routines mask the
address with `0xFFFE0000`). The OS's storage memory driver and its general write routine
both program through the **write buffer**, at most one buffer's worth per command, and never
across a buffer boundary. The word-by-word routine (`0xFD800000`, below) exists too, but no
write traced so far used it.

### How big the write buffer is

At start-up (`0x800C18D8`), after copying its flash routines to IL RAM, the OS checks the chip
(`0xFD800380`): it reads the manufacturer ID and one word of the CFI table (byte offset
`0x50`, word `0x28`, the "device interface" code in the CFI standard). If the manufacturer is
1 and that word is 1, the OS programs **512 bytes** per command; otherwise **32**. The size
lives at `0x8C0A1160`; the setter (`0xFD800F8C`) accepts only 32, 64 or 512 (a table at
`0x806A7F90`). The check also reads CFI word `0x2A`, the chip's own buffer size, but does not
use it.

!!! success "Verified on hardware"
    A dump of a real calculator's RAM holds `0x200` (512) at `0x8C0A1160`, so on the
    calculator the chip check passes: the manufacturer ID reads 1 and CFI word `0x28` reads 1.

!!! warning "Unconfirmed"
    Which chip this is. A manufacturer ID of 1 is AMD/Spansion (now Infineon) in the JEDEC
    list, and 128 KB uniform sectors with a 512-byte write buffer fit their S29GL-S family,
    but the device ID has not been read on a calculator.

## Waiting for the chip

While the chip programs or erases, a read from it no longer returns data. It returns a
**status word** instead, and software polls it until the job is done. The OS's poll routine
(`0xFD800D0E`) uses these bits:

| Bit | Name | Meaning while busy | What the OS does |
|---|---|---|---|
| 7 | DQ7, "data polling" | the **inverse** of bit 7 of the data being written (0 for an erase) | done when DQ7 equals the expected bit 7 |
| 5 | DQ5 | 1 = the chip's internal time limit was exceeded | reads once more, then gives up and resets the chip |
| 3 | DQ3 | 1 = the erase has started (the window for more sector addresses closed) | checked once, right after an erase command |
| 1 | DQ1 | 1 = a buffered program was aborted | sends the abort reset (unlock, `0xAAA` ← `0xF0`) |

Bits 6 and 2 toggle on every read while the chip is busy; the OS never looks at them.

The erase routine (`0xFD800BB6`) first checks that the erase **really started**
(`0xFD800CA8`): after a short delay the first status read must show DQ7 = 0 (busy), and the
next one DQ3 = 1. An erase that already looks finished at the first read counts as a failure.
Then it polls DQ7 up to about 40,000 times, each after a short delay. A buffered program is
polled the same way, at the last address written, expecting the last word's bit 7.

The routines return 0 on success, −1 for bad arguments (address 0, odd address or length, too
long, a sector number out of range), −2 when a program's target is not erased (`0xFFFF`) or
the poll ended badly, and −3 when the poll timed out, a buffered program was aborted, or an
erase did not start. After any failure they reset the chip to reading.

!!! info "Verified in the emulator"
    Because of the "erase started" check, a flash model whose erase is over at once makes
    every erase fail. The emulator returns the busy status for the first reads after an erase
    command (`TestFlashSectorEraseStatus`), and with it the OS writes files into the storage
    memory (`TestInstallAddin`; the probe `TestFlashWikiSR` writes a 20 KB file).

## Why the routines run from IL RAM

While the chip is busy, **nothing can be fetched from it**, including instructions. Code that
starts a program or an erase and then polls for the end must not live in the flash, and
nothing it calls may live there either. So the OS keeps its flash routines in the CPU's
on-chip IL RAM at `0xFD800000` (see [Memory map](memory-map.md#on-chip-memories-p4)):

- **The copy.** `0x801DF4A4` (syscall `0x11C9`) copies the block `0x80745B24`–`0x80746AFB`
  (4056 bytes; start, end and destination come from a table at `0x806ED3D0`) to
  `0xFD800000`. The OS does it at start-up (`0x800C18CC`) and again whenever an app is opened
  from the MAIN MENU (the launch code around `0x803639B0`).
- **Interrupts blocked.** Every routine in the block runs with **SR.BL = 1**, so no interrupt
  can jump into a handler in flash halfway through. The general write routine sets BL itself
  around each command; for the storage routines the caller has set it.
- **No calls back into flash at the wrong time.** The buffered program routine calls a
  division helper in flash (`0x80383568`), but only to check its arguments, before the first
  command.
- **Its own delay.** The polls wait with a copy of the OS's short delay routine at
  `0xFD800718`, the same use of the [one-shot delay unit](peripherals/timers.md#the-one-shot-delay-unit-0xa44a0000)
  as the flash copy at `0x801DF5AA` (count = argument × 2950 / 32, about 92 per unit).

!!! info "Verified in the emulator"
    The copy runs once in a cold boot (5.4 M instructions in, called from `0x800C18CE`) and
    once more when Run-Matrix is opened from the MAIN MENU. Writing a 20 KB file
    to the storage memory and switching off: every flash read made from IL RAM (the status
    polls, the reads checking that a target is erased) happened with SR.BL = 1. Probes:
    `TestFlashWikiColdBoot`, `TestFlashWikiAppLaunch`, `TestFlashWikiSR`
    (`flashwiki_probe_test.go`).

The main routines in the block:

| IL RAM address | What it does |
|---|---|
| `0xFD800000` | Program words one at a time in unlock bypass mode, SR.BL set |
| `0xFD800394` | Write any number of bytes to any flash address: checks the chip, then buffered programs piece by piece, each with SR.BL set. 38 places in the OS load its address |
| `0xFD80011E`, `0xFD8006A0` | Erase the 128 KB sector at an absolute flash address (two near-identical copies), SR.BL set |
| `0xFD800178` | Check whether a sector is blank (all `0xFF`) |
| `0xFD8002CC` / `0xFD800380` | Read the IDs and CFI words; the chip check above |
| `0xFD800AE8` | **Storage memory**: copy bytes from flash offset `0xA0C80000` + *offset* to RAM |
| `0xFD800BB6` | **Storage memory**: erase sector *n* (0–155) of the storage memory |
| `0xFD800E00` | **Storage memory**: one buffered program of at most one buffer |
| `0xFD800C36` | Reset the chip to reading (`0xA0C80000` ← `0xF0`) |
| `0xFD800CA8`, `0xFD800D0E` | The "erase started" check and the status poll (above) |
| `0xFD800F8C` | Set the write buffer size |

The block holds a few more flash routines, used from `0x801E3800`–`0x801E3B00`, that take
their flash base from a variable at `0x8C08F834`.

!!! warning "Unconfirmed"
    What the routines used from `0x801E38xx` are for, and who sets SR.BL before the storage
    routines are called, have not been traced. The boot code (below `0x80020000`) has flash
    routines of its own, in its own IL RAM layout.

## The storage memory

The storage memory (`fls0`, what the calculator calls "Storage memory") takes the flash from
physical `0xC80000` to the end: **156 sectors of 128 KB, 19.5 MB**. Its driver addresses it as
**`0xA0C80000` + offset**:

- **Write** (`0x80292C9A`): checks offset + length ≤ `0x1380000` (19.5 MB), both even, then
  calls the IL RAM program routine for the part up to the next buffer boundary, then for
  each full buffer, then for the rest.
- **Read** (`0xFD800AE8`) and **erase** (`0xFD800BB6`) go through IL RAM directly; the erase
  takes a sector number, not an address.
- Errors from these routines become the file system's codes (−1 → −2, −3 → −7) in
  `0x80371590`.
- Two syscalls erase everything: `0x0DDA` (`0x8018B8DC`) erases the 156 storage sectors one
  by one; `0x0DDB` (`0x8018B96C`) erases 160 sectors from `0xC00000`, the unused 512 KB
  included. Both call `0x802AC66E` with a count out of 100 as they go (a progress display, by the look of it).

On top of the driver sits a **flash translation layer** that writes **4 KB pages** with small
headers (`0x803712BE`–`0x803712EE` writes 8 bytes at offset `0x20` of a page header, then the
4096 bytes of data, then the header's first 32 bytes),
so files are scattered over the flash and old copies are erased later. On top of that sits a
FAT-style file system: directories are 32-byte records with 8.3 names (`.`, `..`,
`CBASIC  G3M`), and long names are shortened the FAT way (`FSEVEN~2`).

!!! success "Verified on hardware"
    In the 16 MB dump of a real calculator every used 128 KB sector of the storage memory
    starts with a 16-byte header: two 32-bit words *N* and its complement written twice (for
    example `0xFFFFFC93`, `0x0000036C`). All 25 used sectors have it, each with a different
    *N* (from `0x14` to `0x36E`). Erased sectors
    are all `0xFF`. Directory records (`.` then `..`) appear 4 KB-aligned, from `0xC83000` on,
    27 of them in the first 16 MB. Script: `re/fls0_peek.py`, `re/fls0_layout.py`.

!!! warning "Unconfirmed"
    The translation layer's format (what *N* counts, the page headers, how it finds the
    newest copy of a page) has not been traced. The OS reads and writes files with the
    `Bfile_…` syscalls, which go through it.

Add-ins are a special case: their code is never copied out of the storage memory. The MMU maps
the `.g3a` file's 4 KB pages, wherever they lie, to virtual `0x00300000` (see
[Memory map](memory-map.md#add-in-mappings) and [MMU and add-in launch](os/mmu-and-addins.md)).

## The OS's own area

Below the storage memory the flash belongs to the OS ([table](memory-map.md#flash)). Two parts
change while the calculator is in use:

- The three **`CASIOMEMDATA`** blocks at `0xB80000`–`0xBDFFFF`. At every power-off the OS
  rewrites one of them ([Boot](os/boot.md#power-off), step 1), through the general write
  routine `0xFD800394`.
- The word at flash offset `0x300`, which decides whether the first-boot setup runs
  ([Boot](os/boot.md#power-on-vs-cold-start)).

!!! info "Verified in the emulator"
    Switching off from the MAIN MENU changed 65,027 bytes in the sector at `0xB80000`
    (from `0xB80013` to `0xB9FFFF`). The flash reads made during it came from the chip check
    and from the general write routine's poll, all with SR.BL = 1. Probe: `TestFlashWikiSR`.

!!! warning "Unconfirmed"
    The `CASIOMEMDATA` blocks contain the names of main memory's folders (`system`,
    `library`, `@LOCAL40`, …), which supports the guess that the OS saves main memory there at
    power-off. Their format has not been traced.

## In the emulator

- [`emu_go/memory.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/memory.go),
  the flash section: a 128 MB window whose first 32 MB is a writable copy of the image (the
  rest of the 32 MB `0xFF` when the image is shorter; beyond 32 MB reads `0xFF`). A command
  state machine handles unlock, buffered program (`0x25` … `0x29`), single-word program
  (`0xA0`), sector erase (`0x30`, 128 KB) and chip erase (`0x10`). After a sector erase the
  next **4 reads** of that sector return the busy status (DQ7 = 0, DQ6 and DQ2 toggling,
  DQ3 = 1, DQ5 = 0), then data again. Programming is instant.
- The flash pages the OS changed are saved next to a save-state as a delta against the image
  (`FLS1` format, 4 KB pages), so the file holds only the storage memory and settings, not the
  OS.
- Which image boots: `os/flash_dump/flash_full.bin` (the 16 MB dump; the goldens and the
  suite are built on it), or the full 32 MB dump through `CG50_FLASH` and `CG50_STATE`.
- The Python reference has the same model (`emu/memory.py`, `_flash_cmd`).
  `emu/flash_selftest.py` drives it through OS-shaped sequences and writes
  `emu/flash_golden.txt`, which `TestFlashGolden` (`flash_test.go`) replays against the Go
  model.
- Tests: `TestFlashGolden`, `TestFlashSectorEraseStatus` and `TestInstallAddin`
  (`install_test.go`: the OS writes a `.g3a` into the storage memory and runs it; needs the
  32 MB image). Probes (`-tags probe`): `TestFls0Trace` (`fls0_trace_test.go`, the call tree
  under the file system's mount), `TestFlashWiki…` (`flashwiki_probe_test.go`).

Differences from the hardware:

- The ID and CFI commands change nothing: reads keep returning the array. So the chip check
  fails in the emulator (it reads `0xDF1E` as the manufacturer ID) and the OS programs in
  32-byte pieces instead of the calculator's 512 (`TestFlashWikiColdBoot`). Files come out
  the same; only the number of commands differs.
- DQ5 (time-out) and DQ1 (buffer abort) never appear, and an erase lasts 4 status reads, not
  real time.
- Unlock bypass (`0x20`) is not modelled: a word written that way would be lost. The OS's writes
  traced so far (a file write, a power-off) don't use it.
