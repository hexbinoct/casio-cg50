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
| `0x00000000` | 32 MB | NOR flash: the OS, then the storage memory (`fls0`) | `0x80000000` / `0xA0000000` |
| `0x0C000000` | 8 MB | DRAM | `0x8C000000` / `0xAC000000` |
| `0x14000000` | — | R61524 LCD controller (bus area 5) | `0xB4000000` |

### Flash

- The OS image sits at the start. For OS 3.60 it occupies the first ~11.5 MB
  (`0x000000`–`0xB80000`); the main header with the `CASIOWIN` magic is at `0x80020000`.
- The rest of the 32 MB holds the storage memory filesystem (`fls0`): your files, add-ins
  and settings. Add-in code is run straight out of these flash pages (see below).

!!! success "Verified on hardware"
    From a full 32 MB dump of a real calculator.

!!! warning "Unconfirmed"
    Where exactly `fls0` begins inside the flash has not been pinned down yet. Some file
    clusters sit below the 16 MB mark.

### DRAM

8 MB at physical `0x0C000000`. How the OS uses it, measured over a 3.5 billion instruction
session (every built-in app, Python, several add-ins, file installs):

| Physical range | Use |
|---|---|
| `0x0C000000`–`0x0C0DFFFF` | OS data; much of it rewritten at every app launch |
| `0x0C0E0000`–`0x0C4DFFFF` | Rewritten at every app launch. `0x0C1E0000`–`0x0C4DFFFF` (3 MB) is zero-filled by the OS at launch, then left alone |
| `0x0C160000`–`0x0C1DFFFF` | The add-in RAM: 512 KB, mapped at `0x08100000` for every add-in |
| **`0x0C4E0000`–`0x0C7FFFFF`** | **Never written and never read** by the OS, any app, any add-in launch or the boot (3200 KB) |

!!! success "Verified on hardware"
    In a dump of a real calculator's DRAM the top range holds power-up noise (random bits,
    no `0x00` bytes at all), and the boundary is exactly `0x0C4E0000`, as in the emulator.
    Nothing ever wrote there. It is a safe home for a resident tool such as a debugger.

## On-chip memories (P4)

| Address | Size | Use |
|---|---|---|
| `0xFD800000` | 16 KB, mirrored every 16 KB | The OS's "IL RAM": boot stack, the interrupt handler table, the flash routines, kernel variables |
| `0xE500E000`–`0xE5011FFF` | 16 KB | X/Y memory. The OS never touches it; gint add-ins use it |
| `0xE5200000`–`0xE5200FFF` | 4 KB | IL memory. The OS never touches it; gint puts its interrupt trampoline here |
| `0xFE200000` | ? | Used by the OS for kernel lists and structures |

Details of the OS's IL RAM at `0xFD800000`:

- The OS writes about 5 KB at `0xFD800000`–`0xFD8024BF`: its flash routines (4 KB, copied
  here again at every app launch) and variables.
- The boot uses `0xFD803FC0`–`0xFD803FFF` as its stack (`SP = 0xFD804000` at reset).
- About 6 KB at `0xFD8026C0`–`0xFD803F3F` is never used.

!!! success "Verified on hardware"
    The 16 KB size comes from a dump: the contents repeat with a 16 KB period.

!!! warning "Unconfirmed"
    The real size of the memory at `0xFE200000` is unknown; it has not been dumped yet.

## Add-in mappings

When an add-in (`.g3a`) runs, the OS sets up the MMU so that:

| Virtual | Physical | Notes |
|---|---|---|
| `0x00300000`… | the `.g3a` file's own pages in flash | Code and read-only data, 4 KB pages mapped on demand from a table at `0x8C04CF0C`. Not contiguous: the filesystem scatters the file. |
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
