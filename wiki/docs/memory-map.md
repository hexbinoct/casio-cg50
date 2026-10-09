# Memory map

The fx-CG50's CPU is a Renesas **SH7305** with an SH-4A core, running **big-endian**. Its
32-bit address space follows the usual SH-4A layout: the top three bits of an address pick a
region, and the regions P1 and P2 are fixed windows onto the first 512 MB of physical memory.

## Virtual regions

| Range | Name | What it is |
|---|---|---|
| `0x00000000`–`0x7FFFFFFF` | P0 / U0 | Translated by the MMU when it is on. Add-ins live here. |
| `0x80000000`–`0x9FFFFFFF` | P1 | Physical = address & `0x1FFFFFFF`, **cached**. The OS runs from here. |
| `0xA0000000`–`0xBFFFFFFF` | P2 | Physical = address & `0x1FFFFFFF`, **uncached**. Boot code, the LCD, the flash driver. |
| `0xC0000000`–`0xDFFFFFFF` | P3 | Translated by the MMU, privileged only. |
| `0xE0000000`–`0xFFFFFFFF` | P4 | On-chip memories and control registers. Not translated, not mirrored. |

So the same byte of flash can be read at `0x00xxxxxx` (when no MMU mapping covers it),
`0x80xxxxxx` and `0xA0xxxxxx`.

## Physical memory

| Physical | Size | What | Seen by the OS at |
|---|---|---|---|
| `0x00000000` | 32 MB | NOR flash: boot code, the OS, then the storage memory (`fls0`) | `0x80000000` / `0xA0000000` |
| `0x0C000000` | 8 MB | DRAM | `0x8C000000` / `0xAC000000` |
| `0x14000000` | — | R61524 LCD controller (bus area 5) | `0xB4000000` |

### Flash

The first 12 MB belong to the OS; the storage memory takes the rest from `0xC80000` on.

| Physical | Size | Contents |
|---|---|---|
| `0x000000`–`0x01FFFF` | 128 KB | Boot code (see [Boot and power-off](os/boot.md)) |
| `0x020000`– | about 7.7 MB | The OS: main header with the `CASIOWIN` magic at `0x80020000`, then code and data. In 3.60 the last used byte is near `0x7BD000`; the rest up to `0xB40000` is erased |
| `0xB40000`–`0xB5FFFF` | 128 KB | Language data (a block tagged `CASIOABS` listing `Langdata` entries) |
| `0xB80000`–`0xBDFFFF` | 3 × 128 KB | Three blocks, each starting with the tag `CASIOMEMDATA` |
| `0xC00000`–`0xC7FFFF` | 512 KB | Erased; the storage memory driver never addresses it |
| **`0xC80000`–`0x1FFFFFF`** | **19.5 MB** | **The storage memory (`fls0`)**: your files, add-ins and settings. Add-in code runs straight out of these flash pages (see [below](#add-in-mappings)) |

- The OS keeps the end of its own area as a constant, `0xA0C00000`, and finds its build date
  stamp just below it (`0x801526EA` copies 14 characters from `0xA0B5FFE0`). The boot code
  also stores the constant at `0x8C04C9F8` (at `0x800036AC`).
- The storage memory driver addresses the flash as **`0xA0C80000` + offset** and rejects any
  offset + length above `0x1380000` (19.5 MB): its write routine at `0x80292C9A` and a second
  routine around `0x80746610` both make that check. `0xC80000` + `0x1380000` is exactly the
  32 MB end of the flash.

!!! success "Verified on hardware"
    From dumps of a real calculator's flash (16 MB in June 2026, later the full 32 MB). In
    the 16 MB dump everything from `0xC00000` up to `0xC7FFFF` is erased (`0xFF`), and the
    first byte that is not sits at `0xC80002`, at the start of the first storage block.
    Directory entries of the filesystem (`.` and `..`) appear from `0xC83000` on. That
    agrees with the driver's base address in the OS 3.60 code.

!!! warning "Unconfirmed"
    What the three `CASIOMEMDATA` blocks hold has not been traced. The code that erases and
    writes them is around `0x802AF800`; that the OS keeps a copy of its main memory there
    is a guess from the tag.

### DRAM

8 MB at physical `0x0C000000`. How the OS uses it, measured over a 3.5 billion instruction
session (every built-in app, Python, several add-ins, file installs):

| Physical range | Use |
|---|---|
| `0x0C000000`–`0x0C0287FF` | VRAM, the OS's drawing buffer (part of the OS data range below) |
| `0x0C000000`–`0x0C0DFFFF` | OS data; much of it rewritten at every app launch |
| `0x0C0E0000`–`0x0C4DFFFF` | Rewritten at every app launch. `0x0C1E0000`–`0x0C4DFFFF` (3 MB) is zero-filled by the OS at launch, then left alone |
| `0x0C160000`–`0x0C1DFFFF` | The add-in RAM: 512 KB, mapped at `0x08100000` for every add-in |
| **`0x0C4E0000`–`0x0C7FFFFF`** | **Never written and never read** by the OS, any app, any add-in launch or the boot (3200 KB) |

!!! success "Verified on hardware"
    In a dump of a real calculator's DRAM the top range holds power-up noise (random bits,
    no `0x00` bytes at all), and the boundary is exactly `0x0C4E0000`, as in the emulator.
    Nothing ever wrote there. It is a safe home for a resident tool such as a debugger.

#### VRAM

The OS draws into **VRAM** at the start of DRAM and copies it to the screen with the DMA
controller. Details are on the [Display](peripherals/display.md#vram) page.

| | |
|---|---|
| Address | `0xAC000000` (uncached P2 view of physical `0x0C000000`) |
| Size | 384 × 216 pixels, RGB565, 768 bytes per row: 165,888 bytes |
| Who uses it | The syscall `GetVRAMAddress` (`0x01E6`, at `0x8004F6A2`) returns `0xAC000000`; about 25 places in the OS load that address directly |

gint add-ins draw the whole 396 × 224 panel from buffers of their own.

## Variables in RAM worth knowing

A few OS variables at fixed addresses that are useful when reading the OS or debugging an
add-in.

| Address | Size | What |
|---|---|---|
| `0xFD8018D4` | 32 bit | **Model code** the running OS checks: `0xCA02` on the fx-CG50. Read in 13 places, from `0x800200D6` in the OS startup on |
| `0xFD8017DC` | 32 bit | Restart flag: set to 1 by the OS just before it restarts itself, so that boot skips the first-boot setup; 2 while the setup runs. An ordinary power-off leaves it alone ([Boot](os/boot.md#power-on-vs-cold-start)) |
| `0x8C04CF0C` | table | **Add-in page table**: one word per 4 KB page of the running add-in, from virtual `0x00300000` on. Each word is the page's address in flash (only the low 29 bits are used; 0 = no page). The TLB-miss handler at `0x8002C918` reads it |
| `0x8C0A1160` | 32 bit | How many bytes the storage memory driver programs per call to the flash routine in IL RAM (`0xFD800E00`) |
| `0x8C04CA24` | 32 bit | The **boot code's** model code (`0xCA00`/`0xCA01`/`0xCA02`, step 7 of [Boot](os/boot.md#from-reset)). Only the boot code (below `0x80020000`) uses it |

!!! success "Verified on hardware"
    Read from dumps of a real calculator's RAM, taken while a gint add-in ran:
    `0xFD8018D4` holds `0xCA02`; `0x8C04CF0C` holds `0x01B4F000`, an address inside the
    storage memory (the add-in's first page); `0x8C0A1160` holds `0x200` (512 bytes);
    `0xFD8017DC` and `0x8C04CA24` hold 0. The meanings come from the OS 3.60 code that
    reads and writes them.

## On-chip memories (P4)

| Address | Size | Use |
|---|---|---|
| `0xFD800000` | 16 KB, mirrored every 16 KB | The OS's "IL RAM": boot stack, the interrupt handler table, the flash routines, kernel variables |
| `0xE500E000`–`0xE5011FFF` | 16 KB | X/Y memory. The OS never touches it; gint add-ins use it |
| `0xE5200000`–`0xE5200FFF` | 4 KB | IL memory. The OS never touches it; gint puts its interrupt trampoline here |
| `0xFE200000`–`0xFE3FFFFF` | six blocks, 752 KB in all (below) | Cleared at boot; the first 144 KB is a heap for kernel lists and structures |

Details of the OS's IL RAM at `0xFD800000`:

- The OS writes about 5 KB at `0xFD800000`–`0xFD8024BF`: its flash routines (4 KB, copied
  here again at every app launch) and variables.
- The boot uses `0xFD803FC0`–`0xFD803FFF` as its stack (`SP = 0xFD804000` at reset).
- About 6 KB at `0xFD8026C0`–`0xFD803F3F` is never used.

!!! success "Verified on hardware"
    The 16 KB size comes from a dump: the contents repeat with a 16 KB period.

### The memory at `0xFE200000`

The boot code (at `0x80004996`) and the OS's own startup (at `0x801E3EEE`) clear the same
six blocks with `memset` (`0x8000B838` in the boot code):

| Block | Size cleared |
|---|---|
| `0xFE200000` | 160 KB (`0x28000`) |
| `0xFE240000` | 168 KB (`0x2A000`) |
| `0xFE280000` | 48 KB (`0xC000`) |
| `0xFE300000` | 160 KB (`0x28000`) |
| `0xFE340000` | 168 KB (`0x2A000`) |
| `0xFE380000` | 48 KB (`0xC000`) |

Then the first 144 KB of the first block becomes a heap: a free-block header at
`0xFE200000` describes the space up to `0xFE223FF8`, and list heads sit at
`0xFE224000`–`0xFE22400C`. The OS also loads a few addresses in `0xFE2FF000`–`0xFE2FFFFF`
and `0xFE3C0000`–`0xFE3C0210`.

!!! warning "Unconfirmed"
    The sizes above are what the OS clears, not measured sizes; the region has not been
    dumped. Two sets of three blocks, 256 KB apart, look like the program and X/Y data
    memories of a DSP. On Renesas's SH7724, a close relative, the SPU2 sound DSP has its
    memories at these addresses. Whether the SH7305's are the same, and what lies between
    the blocks, is not known.

## Add-in mappings

When an add-in (`.g3a`) runs, the OS sets up the MMU so that:

| Virtual | Physical | Notes |
|---|---|---|
| `0x00300000`… | the `.g3a` file's own pages in flash | Code and read-only data, 4 KB pages mapped on demand from the table at `0x8C04CF0C`. Not contiguous: the filesystem scatters the file. |
| `0x08100000`–`0x0817FFFF` | `0x0C160000`–`0x0C1DFFFF` | The add-in's RAM: 8 entries of 64 KB, the same physical RAM for every add-in |

So an add-in's code is never copied into RAM. A TLB miss on `0x00300000` and above makes the
OS look up which flash page holds that part of the file.

## Peripheral registers

Base addresses of the on-chip peripherals the OS and gint use. Linked ones have their own
page. (Older notes list a "free-running counter" at `0xA4130000`; nothing in OS 3.60 uses that
address, see [Timers](peripherals/timers.md#no-counter-at-0xa4130000).)

| Base | Peripheral |
|---|---|
| `0xA4050000` | PFC: pin function controller (also the [LCD's](peripherals/display.md) register-select line) |
| `0xA4080000` | [INTC](peripherals/interrupts.md): interrupt controller |
| `0xA413FEC0` | [RTC](peripherals/timers.md#rtc-0xa413fec0): real-time clock |
| `0xA4150000` | CPG: clock generator (PLL, module stop bits) |
| `0xA4490000` | [TMU](peripherals/timers.md#tmu): timers |
| `0xA44A0000` | [One-shot delay unit](peripherals/timers.md#the-one-shot-delay-unit-0xa44a0000) |
| `0xA44B0000` | [KEYSC](peripherals/keyboard.md): key-scan controller |
| `0xA44D0030` | [ETMU0–5](peripherals/timers.md#etmu-channels): extra timer units, 32.768 kHz |
| `0xA4520000` | WDT: watchdog |
| `0xA4610000` | [Battery A/D converter](peripherals/battery-adc.md) (interrupt `0x560`) |
| `0xA4CB0000` | [BCD ALU](peripherals/bcd-alu.md) |
| `0xB4000000` | [R61524 LCD controller](peripherals/display.md) |
| `0xF6000000` | UTLB address/data arrays |
| `0xFE008000` | DMAC: DMA controller (frames to the [display](peripherals/display.md#pushing-a-frame)) |
| `0xFEC10000` | BSC: bus/SDRAM controller |
| `0xFF000000` | CCN: MMU and cache control (MMUCR, PTEH, EXPEVT, INTEVT…) |
| `0xFF200000` | UBC: user break controller |
| `0xFF2F0000` | CPUOPM: CPU operation mode |

!!! danger "Careful with the CPG from probe add-ins"
    A probe add-in that copied the OS's idle routine (write a CPG register, then `sleep`)
    reset a real calculator to its first-boot setup: files survived, everything in RAM was
    lost. Don't `sleep` or touch the CPG from a probe.

## In the emulator

- [`emu_go/memory.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/memory.go):
  the flash (a 128 MB window, the first 32 MB writable through the NOR command set), 8 MB
  of DRAM reachable through every mirror including VRAM at `0xAC000000`, IL RAM at
  `0xFD800000`, the X/Y and IL memories at `0xE5000000`, and the memory at `0xFE200000`.
  It also saves the flash pages the OS changed (storage memory included) next to a
  save-state, as a delta against the dump.
- [`emu_go/mmu.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmu.go): the
  UTLB and the translation of P0/P3 addresses, which is how add-ins reach their flash pages
  and RAM.
- The Python reference has the same map in `emu/memory.py`.
- Tests: `TestFlashGolden` (`flash_test.go`), `TestFlashSectorEraseStatus` and
  `TestInstallAddin` (`install_test.go`: the OS writes a file into the storage memory),
  `TestAddinRunsThroughMMU` (`mmu_test.go`), `TestWarmBoot`, `TestSaveStateRoundtrip`. The
  DRAM usage table above comes from the probe `TestRamFree` (`ramfree_probe_test.go`).

Differences from the hardware: the emulator's IL RAM is 64 KB without the 16 KB mirroring,
and it treats all 2 MB from `0xFE200000` as RAM.
