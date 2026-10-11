# MMU and add-in launch

The OS and its built-in apps run from fixed addresses in P1 and never need address
translation. Add-ins are different: every `.g3a` is linked to run at virtual `0x00300000`
with its RAM at `0x08100000`, wherever its file sits in flash. The OS uses the SH-4A's
**MMU** to make that true. This page covers the MMU registers the OS uses, the steps from
the MAIN MENU to an add-in's first instruction, how code pages arrive on demand, and what a
launch clears.

## The MMU in brief

The MMU translates addresses in P0/U0 (`0x00000000`–`0x7FFFFFFF`) and P3 when **MMUCR.AT**
is set (see [Memory map](../memory-map.md#virtual-regions)). It looks each address up in the
**UTLB**, a 64-entry table of mappings. Each entry maps one page (1 KB, 4 KB, 64 KB or 1 MB)
of virtual addresses onto physical memory. An address that no valid entry covers raises a
**TLB miss** exception, and software decides what to load.

Software loads an entry in three steps: write the virtual page to **PTEH**, the physical page
and flags to **PTEL**, the entry number to MMUCR.URC, then execute **`ldtlb`**.

| Address | Register | Use |
|---|---|---|
| `0xFF000000` | PTEH | Virtual page number (bits 31–10) and ASID (bits 7–0) of the entry to load. After a TLB miss it holds the missing page |
| `0xFF000004` | PTEL | Physical page number (bits 28–10) and the flags below |
| `0xFF00000C` | TEA | The address that caused the last MMU exception |
| `0xFF000010` | MMUCR | AT (bit 0, translation on), TI (bit 2, write 1 to invalidate every entry), SV (bit 8, single virtual mode: ASIDs are ignored in privileged mode), URC (bits 15–10, the entry `ldtlb` writes), URB (bits 23–18) |

PTEL's flag bits:

| Bit | Name | Meaning |
|---|---|---|
| 8 | V | Entry valid |
| 7, 4 | SZ1, SZ0 | Page size: 00 = 1 KB, 01 = 4 KB, 10 = 64 KB, 11 = 1 MB |
| 6–5 | PR | Protection. 3 = read and write in both privileged and user mode |
| 3 | C | Cacheable |
| 2 | D | Dirty. When clear, the first write to the page raises an "initial page write" exception |
| 1 | SH | Shared: the entry matches any ASID |
| 0 | WT | Write-through caching |

The chip also has a small instruction TLB (ITLB) that it refills from the UTLB by itself, and
memory-mapped views of both tables (the UTLB's at `0xF6000000` and `0xF7000000`). The OS
3.60 doesn't use either view.

## How OS 3.60 uses it

The MMU is **off** while the OS and built-in apps run, and the OS turns it on only to run an
add-in. All its MMU code sits at `0x8002C7C0`–`0x8002C918` and `0x8002E332`–`0x8002E8EC`, and
each routine is also a syscall:

| Syscall | Address | What it does |
|---|---|---|
| `0x0006` | `0x8002C7C0` | MMU off: MMUCR ← `0x00FC0104` (AT = 0, TI flushes the TLB, SV = 1, URB = 63). Clears the "add-in mapped" byte `0x8C04CEE1` |
| `0x0007` | `0x8002C7FA` | MMU on: MMUCR ← `0x00FC0101` (AT = 1, SV = 1, URB = 63). Sets `0x8C04CEE1` to 1 |
| `0x0008` | `0x8002C834` | Flush: sets MMUCR.TI, leaves AT as it is, resets the slot counter `0x8C04D70C` to 0 |
| `0x0009` | `0x8002C84C` | Map a 4 KB page (r4 virtual, r5 physical, r6 flags) into the next **round-robin slot**. The counter `0x8C04D70C` picks the slot and runs 0, 1, … 54, then back to 0 |
| `0x000A` | `0x8002C8A4` | Map a page into the slot given in r7; r6 holds the flags, including the size |
| `0x000C` | `0x8002C918` | The TLB-miss handler (below) |
| `0x0011` | `0x8002CC10` | Invalidate the instruction cache: sets CCR (`0xFF00001C`) bit 11, ICI, from uncached code at `0xA02E220E` |
| `0x0025` | `0x8002E332` | Unmap: MMU off, flush, then clear the add-in page table |
| `0x0026` | `0x8002E358` | Map an add-in for launch (next section) |
| `0x0027` | `0x8002E60A` | Map the add-in RAM (slots 55–62) and slot 63 |
| `0x0028` | `0x8002E666` | Rebuild the mapping of the running add-in ([below](#rebuilding-the-mapping)) |

Every mapping uses ASID 0 and SH = 0. Since the OS sets SV = 1 and add-ins run privileged
([CPU](../cpu.md#register-banks)), the ASID never matters. Each `ldtlb` in the OS is followed
by three `nop`s.

## Launching an add-in

What happens between the key press on an add-in's icon and its first instruction, in order.
The OS runs the same path for every app; for a built-in app, step 5 unmaps and step 6 is skipped.

1. **The MAIN MENU's stack is painted.** The MAIN MENU runs on a stack whose top is
   `0x8C160000`. The OS fills `0x8C0E3000` up to 1 KB below the current stack pointer with
   the byte `0x55` (`memset` from `0x80363860`).
2. **The 3 MB scratch area is zeroed.** The routine at `0x80362FD6` writes zeros to
   `0x8C1E0000`–`0x8C4DFFFF` and stores the start address in `0x8C0CBD38`.
3. **The OS switches to the app stack**, whose top is `0x8C1E0000`, with a `longjmp`
   (`0x80382408`). It paints that stack the same way: `0x55` from `0x8C160000` up to 1 KB
   below the stack pointer (`memset` from `0x8036390E`), then zeroes the 3 MB area again.
4. **The app runner** `0x803639A8` copies the OS's flash routines into IL RAM again (4 KB
   from `0x80745B24` to `0xFD800000`, by `0x801DF4A4`), invalidates the instruction cache
   (syscall `0x0011`) and fills VRAM with `0xFF` (white).
5. **Map or unmap.** It reads the app's entry address from `0x8C0A3058`. If that is
   `0x00300000`, the app is an add-in and it calls the mapper, syscall `0x0026`. Otherwise it
   calls syscall `0x0025`, which turns the MMU off and clears the page table.
6. **The mapper** (`0x8002E358`):
    1. turns the MMU off, flushes the TLB and invalidates the instruction cache (syscalls
       `0x0006`, `0x0008` and `0x0011`; the slot counter is now 0), and clears the first 1 KB
       of the add-in page table at `0x8C04CF0C`;
    2. opens the `.g3a` file and walks it 4 KB at a time. It asks the file system for the
       flash address of each 4 KB of the file, starting at file offset `0x7000`: the first
       `0x7000` bytes are the `.g3a` header (names, icons). The address of file offset
       `0x7000` + n × 4 KB goes into word n of the page table;
    3. maps the first code page, `0x00300000`, into slot 0 with syscall `0x0009`;
    4. closes the file and turns the MMU on (syscall `0x0007`);
    5. maps the RAM with syscall `0x0027`: eight 64 KB pages, `0x08100000`–`0x0817FFFF` onto
       physical `0x0C160000`–`0x0C1DFFFF`, in slots 55–62, and slot 63, virtual
       `0x00000000`–`0x0000FFFF` onto physical 0 (the start of the flash). The physical base
       comes from the constant `0x8C160000` at `0x8038BAAC`.
7. **The call.** The app runner calls the entry with `jsr` at `0x80363A9A`. The add-in's
   start-up code is an ordinary function call from the OS.

The code is never copied into RAM: the add-in's first instruction is fetched from the flash
page of its file, through the mapping.

| Page | Flags in PTEL | Meaning |
|---|---|---|
| Code, 4 KB (slots 0–54) | `0x17C` | V, 4 KB, PR = 3, cacheable, dirty |
| RAM, 64 KB (slots 55–63) | `0x1EC` | V, 64 KB, PR = 3, cacheable, dirty |

The code pages are mapped writable (PR = 3, D = 1). The MMU does not protect them; the flash
itself only changes through its command sequences (see
[Flash and storage memory](../flash.md)).

### What the add-in finds when it starts

| | Value | Notes |
|---|---|---|
| PC | `0x00300000` | File offset `0x7000` |
| PR | `0x80363A9E` | Returning from the entry returns into the OS's app runner |
| r4, r5 | 1, 0 | When started from the MAIN MENU. r5 is a 16-bit value |
| SR | MD = 1, BL = 0, IMASK = 0 | Privileged, interrupts enabled (the emulator showed `0x40000101`; see [SR bit 12](../cpu.md#sr-bit-12-is-set) for the calculator's `0x40001101`) |
| VBR | `0x80020F00` | The OS's vectors. gint replaces it ([Interrupts](../peripherals/interrupts.md#gint)) |
| r15 | `0x8C1DFFC8` | The OS's app stack, near `0x8C1E0000` |
| MMUCR | `0x00FC0101` plus URC | AT = 1 |
| UTLB | slot 0, slots 55–63 | One code page, the RAM, and virtual page 0 |

**The add-in RAM is the OS's app stack.** Physical `0x0C160000`–`0x0C1DFFFF` is mapped at
`0x08100000`–`0x0817FFFF` and is also the stack the OS runs the app on (P1 `0x8C160000`–
`0x8C1DFFFF`). The stack grows down from the top of the add-in RAM, so the add-in's own stack
and its variables share these 512 KB.

!!! info "Verified in the emulator"
    The steps, the register values, the order of the `ldtlb`s and the state at entry are from
    a single-step trace of a launch from the MAIN MENU (a small test add-in, "helomelo2", whose
    code is two 4 KB pages). The flash address in its page table, `0x00DBB000`, is exactly
    `0x7000` past the start of its `.g3a` file in the flash. Probe: `TestMMUWikiLaunch`.

!!! warning "Unconfirmed"
    - Why the OS maps virtual `0x00000000`–`0x0000FFFF` (slot 63) has not been traced.
    - The meaning of r4 and r5 at entry has not been traced in the OS.
    - The launch reads the add-in's code pages before mapping them (3072 data reads of the
      two pages of "helomelo2"), perhaps for a checksum. Not traced.
    - What the OS does when an add-in's entry function returns has not been traced.

## Code pages on demand

Only the first code page is mapped at launch. The first access to any other page raises a
TLB miss: the CPU jumps to VBR + `0x400` (`0x80021300`), and the OS's dispatcher sends
EXPEVT `0x040` (read or fetch) and `0x060` (write) to **`0x8002C918`** (see
[CPU](../cpu.md#exceptions-and-their-vectors)). The handler:

1. takes the missing page from PTEH (address & `0xFFFFF000`);
2. if the page is at or above `0x00300000`, reads word (page − `0x00300000`) / 4 KB of the
   page table at `0x8C04CF0C`;
3. if that word is not 0, maps the page onto its low 29 bits (the physical address) with
   flags `0x6C` into the next round-robin slot (syscall `0x0009`), and returns. The faulting
   instruction then runs again.

Otherwise (an address below `0x00300000`, or a page the table doesn't list) it reports the
fault: it takes the saved PC from the exception frame and calls the error routine with cause 1,
whose message is "TLB ERROR" (see the "System ERROR" screen on the
[CPU](../cpu.md#exceptions-and-their-vectors) page).

Slots 0–54 hold code pages, so at most 55 pages (220 KB) are mapped at once. When the counter
wraps, the oldest mapping is overwritten, and that page simply misses again the next time.
The RAM in slots 55–63 is never touched by the round robin.

The page table has room for 512 words before the slot counter at `0x8C04D70C`, 2 MB of
code. The mapper clears only the first 256 words (1 MB) before filling it, and its walk has
no bound of its own.

!!! info "Verified in the emulator"
    "helomelo2" took one TLB miss in its first 40 million instructions (address
    `0x00301684`, its second page), which went into slot 1. Probe: `TestMMUWikiLaunch`.
    Casio's own add-ins and the gint add-ins DASM and Upsilon all run their code through
    this handler. Tests: `TestAddinRunsThroughMMU`, `TestUpsilonStarts`.

!!! success "Verified on hardware"
    A dump of a real calculator's RAM, taken while a gint add-in ran, holds an address inside
    the storage memory at `0x8C04CF0C` (see
    [Memory map](../memory-map.md#variables-in-ram-worth-knowing)).

!!! warning "Unconfirmed"
    Whether anything limits an add-in to 2 MB (or 1 MB) of code has not been traced.

## What a launch clears

Every app launch rewrites these areas, before the app's first instruction:

| Area | Becomes | By |
|---|---|---|
| `0x8C0E3000` up to 1 KB below the MAIN MENU's stack pointer | `0x55` bytes | `memset` from `0x80363860` |
| `0x8C160000` up to 1 KB below the app stack pointer: **the add-in RAM** | `0x55` bytes | `memset` from `0x8036390E` |
| `0x8C1E0000`–`0x8C4DFFFF` (3 MB) | zeros | `0x80362FD6` |
| VRAM, `0xAC000000`, 165,888 bytes | `0xFF` (white) | `memset` from `0x803639CA` |
| IL RAM `0xFD800000`, 4 KB | the OS's flash routines, copied again from `0x80745B24` | `0x801DF4A4` |
| The UTLB and the page table `0x8C04CF0C` | flushed, cleared | syscalls `0x0006`, `0x0008` |

So **the OS does not zero an add-in's RAM**: it holds `0x55` bytes when the add-in starts. An
add-in's start-up code has to set up its own initialised data and zero its own variables.
The `0x55` fill also lets the OS measure how deep its stacks went: the routine at
`0x803640B4` counts the `0x55` bytes left at the bottom of either stack.

Nothing in the launch touches DRAM from `0x0C4E0000` up (3200 KB); see
[Memory map](../memory-map.md#dram).

!!! success "Verified on hardware"
    In a dump of a real calculator's DRAM, taken while a gint add-in ran, physical
    `0x0C170000`–`0x0C1CFFFF` (six of the add-in RAM's eight 64 KB pages) holds nothing but
    `0x55` bytes, and `0x0C1E0000`–`0x0C4DFFFF` nothing but zeros. The other two pages hold
    mostly `0x55` too.

!!! info "Verified in the emulator"
    The `memset` calls and their arguments, and the 3 MB clear, were logged during a launch.
    When the add-in starts, every word of its RAM except the top ~1 KB (the live stack) is
    `0x55555555`. Probe: `TestMMUWikiClears`.

## Leaving an add-in

- **MENU** inside an add-in brings up the MAIN MENU, but the add-in stays mapped: the MMU
  stays on and no MMU register is written.
- **Picking another app** runs the launch path above again. For a built-in app, step 5 calls
  syscall `0x0025`: the MMU goes off, the TLB is flushed and the page table is cleared.

!!! info "Verified in the emulator"
    MENU from "helomelo2" wrote no MMU register (MMUCR still has AT = 1). Opening Run-Matrix
    from there wrote MMUCR `0x00FC0104` twice (syscalls `0x0006` and `0x0008`) and left the
    page table empty. Probe: `TestMMUWikiLaunch`.

### Rebuilding the mapping

Syscall `0x0028` (`0x8002E666`) rebuilds the mapping of a running add-in, if the byte
`0x8C04CEE1` says one is mapped. It flushes the TLB (syscall `0x0008`, AT stays on), clears
the page table, walks the file again exactly like the mapper, maps the first code page, and
reloads the RAM entries **last**. It is called from three places in the OS (`0x801894CC`,
`0x8018A35C`, `0x8018C1DA`).

Between the flush and the reload, the add-in RAM at `0x0810xxxx` is unmapped. Code that
touches it in that window with SR.BL = 1 (an interrupt handler of an add-in kernel such as
gint, say) takes a TLB miss it cannot handle, and the CPU resets; see
[CPU](../cpu.md#an-mmu-exception-with-srbl-1-resets-the-cpu).

!!! warning "Unconfirmed"
    What calls syscall `0x0028`, and why a running add-in's file would need a new mapping
    (perhaps after the storage memory moved it), has not been traced.

## Redirecting a page: software breakpoints

Because the TLB-miss handler only reads the page table, a program that changes a word of the
table changes where that page of the add-in comes from. The handler masks the word to 29 bits
and maps it cacheable, so the target can be RAM as well as flash.

The debugger monitor of DASM (a disassembler and debugger add-in developed alongside the
emulator) uses this for software breakpoints in another add-in's code, which lives in flash
and cannot be patched in place:

1. copy the 4 KB code page into RAM (the monitor keeps up to 16 copies from `0x8C700000`);
2. write the copy's physical address into the page's word of the OS's table `0x8C04CF0C`
   (no TLB entry may still map the old page);
3. write `trapa #0xC3` into the copy where the breakpoint goes.

The next fetch from that page misses, the OS's handler maps the RAM copy, and the `trapa`
stops the target in the monitor.

!!! info "Verified in the emulator"
    Breakpoints set this way stop Casio's Geometry add-in and a gint add-in in the monitor.
    Probes: `TestMonitorDasm`, `TestMonitorDasmGint` (`emu_go/monitor_dasm_probe_test.go`).

!!! warning "Unconfirmed"
    On the calculator, a patched page also needs the ITLB and the instruction cache dealt
    with. The emulator has neither (below), so it cannot check that part of the monitor.

## In the emulator

- [`emu_go/mmu.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmu.go): the
  64-entry UTLB, `ldtlb`, MMUCR (AT, TI, SV, URC), translation of P0/U0 and P3 with all four
  page sizes, ASIDs, protection and the D bit. Exceptions are precise: the instruction is
  rolled back and SPC points at it (or at the branch, for a delay slot). A TLB miss goes to
  VBR + `0x400`, a protection violation or initial page write to VBR + `0x100`, with TEA,
  PTEH and EXPEVT set. The memory-mapped UTLB arrays at `0xF6000000`/`0xF7000000` work, and
  the UTLB is part of a save-state.
- The Python reference emulator has the same model (`CCN`, `UTLBArrays` in `emu/mmio.py`).
- Differences from the chip:
    - URC is only what software writes; the chip would also step it after each `ldtlb`. The
      OS always sets it first, so this never shows.
    - There is no ITLB: fetches use the UTLB directly, and the ITLB arrays at `0xF2000000`/
      `0xF3000000` are unmapped (they read 0).
    - There is no cache, so `icbi`, `ocbi`, `ocbp` and the like do nothing, and a page
      patched in RAM is seen at once.
    - An MMU exception with SR.BL = 1 halts the emulator, where the chip resets.
- Tests:
    - Conformance cases (in `emu/conformance.json`, run by `emu_go/conformance_test.go`):
      `mmu_ldtlb_load`, `mmu_store_translated`, `mmu_miss_read`, `mmu_miss_predec_rollback`,
      `mmu_initial_page_write`, `mmu_protection_write`, `mmu_user_protection_read`,
      `mmu_delay_slot_miss`, `mmu_ti_flush_then_miss`, `mmu_invalid_entry_no_match`,
      `mmu_utlb_arrays`.
    - `TestAddinRunsThroughMMU`
      ([`emu_go/mmu_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmu_test.go)):
      launches "helomelo2" from the MAIN MENU, answers its prompt, presses MENU and checks
      that the MAIN MENU still works.
    - `TestSaveStateRoundtrip` (`emu_go/state_test.go`) restores the UTLB.
- Probes (`-tags probe`): `TestMMUWikiLaunch` and `TestMMUWikiClears` in
  [`emu_go/mmuwiki_probe_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmuwiki_probe_test.go)
  produced the traces on this page.
