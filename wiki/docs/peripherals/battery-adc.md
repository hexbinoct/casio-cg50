# Battery A/D converter (`0xA4610000`)

The SH7305 has an **A/D converter** (a unit that turns a voltage into a number) at
`0xA4610000`. The OS uses it to measure the battery. When a conversion ends, the unit raises
the interrupt with **INTEVT `0x560`** (INTEVT is the code that tells the interrupt handler
which source fired; see [Interrupts](interrupts.md)).

Older notes called this block a "timer" and `0x560` a periodic tick. It is neither: `0x560`
fires once at the end of each conversion the OS starts.

## Why it matters

- **The reading gates the main menu.** If the result register reads 0, the OS decides the
  battery is flat and never draws the main menu (see [below](#why-the-boot-stalls-without-it)).
- **The OS waits for conversions.** The battery monitor starts a conversion and waits for it
  to end, with no timeout.
- **The idle conversion is unusual.** The conversion the OS keeps in progress while it idles does
  not finish while the CPU is running. Everything points to it finishing while the CPU sleeps,
  which makes its interrupt a wake-up source for the idle OS.

## Registers

The OS uses 16-bit accesses at `+0x82`–`+0x8C`.

| Address | Offset | What | Who writes it |
|---|---|---|---|
| `0xA4610082` | `+0x82` | A/D result for channel setting `0x41` | — |
| `0xA4610084` | `+0x84` | A/D result for channel setting `0x42`: the battery reading | — |
| `0xA4610088` | `+0x88` | Control. Low byte: channel setting (`0x41` or `0x42`). Bit 13: software start. Bit 14: set by the OS just before a software start, cleared by the handler. Bit 15: conversion end | OS |
| `0xA461008A` | `+0x8A` | Second control word. The OS clears bit 13 before every start, writes `0x01CF` after an idle-style start, and the handler clears bits 14 and 15 | OS |
| `0xA461008C` | `+0x8C` | Bit 15: idle-style start. The OS writes 0 here before every start | OS |
| `0xA461008E` | `+0x8E` | Reads `0x0033` after an idle-style start; the routines on this page never write it | — |

!!! warning "Unconfirmed"
    The meaning of each bit comes from what the OS 3.60 code does with it (routines listed
    [below](#in-the-os-360)). The role of bit 14 of `+0x88` and of the value `0x01CF` is not
    known.

What a probe add-in read on a real calculator, before and after starting a conversion the
way the OS's idle routine does:

| Offset | As the OS left it | After an idle-style start |
|---|---|---|
| `+0x80` | `0000` | `0000` |
| `+0x82` | `0000` | `0000` |
| `+0x84` | `8D00`, `8D80`, `8D40` (three sessions) | `0000` |
| `+0x86` | `0000` | `0000` |
| `+0x88` | `0042` | `0000` |
| `+0x8A` | `0000` | `01CF` |
| `+0x8C` | `0000` | `8000` |
| `+0x8E` | `0000` | `0033` |

- The registers **repeat at `+0x90`–`+0x9E`**: every read of `+0x9x` matched `+0x8x`.
- After the start sequence the result register read 0, and `+0x88` read 0 although the
  sequence had just written `0x0042` there.
- The three battery readings all have the low six bits clear. The OS shifts the value right
  by 6 before using it, so the result looks like a 10-bit number stored in the top bits.

!!! success "Verified on hardware"
    Probe add-in `tools/tickprobe`, results in `os/devic_probes/tickprobe_v1..v3_2026-09-27.txt`.

## Two ways to start a conversion

### Idle-style start (`0x802AE87A`)

A small OS routine writes, in this order:

1. `+0x8A` &= ~`0x2000`
2. `+0x8C` = 0
3. `+0x88` = `0x0042`
4. `+0x8C` = `0x8000` (start)
5. `+0x8A` = `0x01CF`

The OS's idle routine `0x802AE790` waits for an event, sleeping in the meantime, and every
time that wait returns it calls this routine. So a conversion is always in progress when the
idle OS goes back to sleep.

!!! warning "Unconfirmed"
    Read from the OS 3.60 code (the wait is `0x80295128`, which sleeps through `0x802AE742`).
    Running the OS in the emulator agrees: idling at the main menu for 2 emulated seconds, it
    made 131 idle-style starts, all from `0x802AE790`, and went to sleep 132 times.

### Software start (`0x801DE64A`)

The battery monitor `0x801DE54A` first prepares the unit (`0x801DE60A`): it clears bit 13 of
`+0x8A`, writes 0 to `+0x8C`, then writes the channel to `+0x88`: `0x41` when its argument
is 0, `0x42` otherwise. Then `0x801DE64A` sets bit 14 of `+0x88`, then bit 13 (start).

It then **waits with the CPU running**. It polls an OS event flag that the `0x560` handler
sets (bit 0 of the byte at `0x8C0A1568`). After 256 polls without it, it reads `+0x88`
directly: if bit 15 is set, it does the handler's work itself and continues; if not, it
starts waiting again. There is no timeout.

Once the conversion has ended it reads the result register twice (`+0x84` for argument 1,
`+0x82` otherwise), clears bit 14 of `+0x88`, and returns the average of the two readings
shifted right by 6.

!!! warning "Unconfirmed"
    Read from the OS 3.60 code. A software start has not been tried on a real calculator.

!!! info "Verified in the emulator"
    Opening Run-Matrix runs the battery monitor, which starts a conversion by software and
    busy-waits for it. The emulator ends a software-started conversion whether or not the CPU
    sleeps, so the OS carries on; `TestMenuReturnsFromApp` goes through this path.

## The idle conversion only ends while the CPU sleeps

Started the idle way on a real calculator, a conversion **never ended while the CPU was
running**: the start bit stayed set, no result appeared and no end flag was set.

- 3 tries in the add-in's own environment: nothing within 10 s each (and 5 s more without
  restarting).
- 8 tries back in the OS's environment: nothing within 2 s each.

Yet the OS does get fresh readings: the battery value it left in `+0x84` differed from one
session to the next (`0x8D00`, `0x8D80`, `0x8D40`).

!!! success "Verified on hardware"
    `tickprobe` v2 and v3 (`os/devic_probes/tickprobe_v2_2026-09-27.txt`,
    `tickprobe_v3_2026-09-27.txt`).

The explanation that fits is that the conversion only progresses while the CPU sleeps. Its
end then raises `0x560`, and a pending interrupt wakes a sleeping CPU even when the OS
sleeps with interrupts blocked (SR.BL = 1; see [Interrupts](interrupts.md)). How long a conversion takes
during sleep is not known.

!!! warning "Unconfirmed"
    Inferred from the measurements above. The test that would have shown it directly, a
    probe that slept like the OS, reset the calculator (next box).

!!! danger "Don't replay the idle routine from a probe"
    A probe add-in that copied the OS's idle sleep (write `0` to the CPG register
    `0xA4150020`, then `sleep`) reset a real calculator to its first-boot setup: files
    survived, everything in RAM was lost. Reading the unit's registers and starting a
    conversion the way the OS does were both harmless. Don't `sleep` or touch the CPG from a
    probe.

## The interrupt (INTEVT `0x560`)

- The OS's handler for `0x560` is `0x801DED94`. It masks the source, sets the "conversion
  done" event flag (bit 0 of `0x8C0A1568`), clears bits 14 and 15 of `+0x8A` and `+0x88`,
  and unmasks the source.
- In the interrupt controller ([INTC](interrupts.md)) the source's priority is the field
  IPRB bits 15–12 (`0xA4080004`) and its mask is IMR4 bit 3 (`0xA4080090`, cleared through
  `0xA40800D0`). The OS sets the priority to **12**. Two helpers do this: `0x801DE51E` masks
  the source and zeroes the priority, `0x801DE532` restores priority 12 and unmasks it.

!!! success "Verified on hardware"
    A dump of a real calculator's on-chip IL RAM holds `0x801DED94` in the OS's interrupt
    handler table slot for `0x560` (`0xFD80116C`).

!!! warning "Unconfirmed"
    What the handler and the two helpers do is read from the OS 3.60 code. The emulator sees
    the OS program the same INTC fields (`TestDumpINTC`).

Add-ins that take over the interrupt system, such as gint ones, must mask this source;
otherwise the OS's conversions keep raising `0x560` into the add-in's handlers.

!!! info "Verified in the emulator"
    Before the emulator's INTC honoured priorities and masks, `0x560` reached a gint add-in's
    empty vector and it crashed ("illegal instruction"). `TestINTCGate` holds the gating.

## In the OS (3.60)

| Address | Routine |
|---|---|
| `0x802AE790` | Idle routine: waits for an event (sleeping), then starts an idle-style conversion |
| `0x802AE87A` | Idle-style start. Also called from `0x802AE9D6` (not traced) |
| `0x802AE742` | Sleep: RTC periodic interrupt every ½ s, SR.BL = 1 with IMASK = 0, then either writes `0xA4150020` = 0 and `sleep`s, or calls a second sleep routine at `0xA0020926` (it `sleep`s at `0xA00208E8`). Which one depends on bit 1 of the PFC byte `0xA4050162` |
| `0x801DED94` | `0x560` handler |
| `0x801DE54A` | Battery monitor (argument: channel 0 or 1) |
| `0x801DE60A` | Prepare the unit and select the channel |
| `0x801DE64A` | Software start |
| `0x801DE51E`, `0x801DE532` | Mask / unmask `0x560` in the INTC |
| `0x801E6BBC` | Battery level: battery monitor on channel 1, then thresholds |
| `0x801E6B1E` | Battery event check, called by the shell (`0x802AEA26`) through `0x801E6B5C` |

The battery level routine compares the averaged, shifted reading with one of two sets of
thresholds. A byte in RAM at `0x8C0A2EB6` picks the set: value 2 picks the first.

| Reading above | Level (set 1) | Reading above | Level (set 2) |
|---|---|---|---|
| 465 | 3 | 475 | 3 |
| 447 | 2 | 429 | 2 |
| 429 | 1 | 383 | 1 |
| 402 | `0x11` | 347 | `0x11` |
| otherwise | `0x12` | otherwise | `0x12` |

The real readings above (`0x8D00`–`0x8D80`, 564–566 after the shift) are above the top
threshold of both sets.

!!! warning "Unconfirmed"
    Read from the OS 3.60 code. What each level means to the user is not established.

## Why the boot stalls without it

The shell checks for a battery event on every pass of its loop (`0x801E6B1E`). That check
returns 1 when the battery level is `0x12` (and `0x801DE858` returns 4). A result register
that reads 0 gives level `0x12`, so the shell sees a battery event every time, skips drawing
the main menu, and goes on idling. The screen stays blank.

!!! info "Verified in the emulator"
    Before the unit was modelled its registers read 0, and the emulated boot reached the
    OS's idle loop without ever drawing the main menu. Returning a mid-range reading
    (`0x7140`, level 2) at `+0x82`/`+0x84` was the fix that let the menu draw.
    `TestADCOracleTranscript` checks that both registers read `0x7140`.

A unit that never finishes a software-started conversion would also hang the OS, in the
battery monitor's wait loop, which has no timeout (see
[Software start](#software-start-0x801de64a); read from the code, not tried).

## In the emulator

`periphIRQ` in [`emu_go/mmio.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmio.go)
and `PeriphIRQ` in [`emu/mmio.py`](https://github.com/hexbinoct/casio-cg50/blob/main/emu/mmio.py)
(the Python reference emulator), which behave identically:

- An **idle-style start** (`+0x8C` bit 15) ends after **1/64 s of CPU sleep**, counted from
  the start. The real time is unknown; this value is a choice. Writing `+0x8C` without bit
  15 cancels it.
- A **software start** (`+0x88` bit 13) ends after **100 µs**, awake or asleep.
- Either sets bits 14 and 15 of `+0x88` and raises `0x560` once.
- The result registers always read **`0x7140`** (453 after the shift, level 2).
- Until the OS first starts a conversion, `0x560` is still raised every 30 000 instructions,
  as in the emulator's early versions; the cold boot from reset was proven that way. That
  old periodic tick cost about 11.4M instructions of handler work per emulated second at
  idle; with conversion-driven interrupts it is about 0.23M.

Simplifications compared with the real unit: `+0x88`–`+0x8B` behave as one register, there
is no mirror at `+0x90`, a start does not clear the result, `+0x8E` is not modelled, and
`0x560` is delivered at level 8 rather than the programmed 12.

Tests: `TestADCOracleTranscript` in
[`emu_go/adc_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/adc_test.go)
replays `emu/adc_golden.txt`, written by
[`emu/adc_selftest.py`](https://github.com/hexbinoct/casio-cg50/blob/main/emu/adc_selftest.py),
against the Go model. `TestScheduledStepMatchesExact` (`emu_go/schedule_test.go`) and
`TestSleepWakesOnRequest` (`emu_go/rtc_test.go`) cover the scheduling and the wake from
`sleep`. The probe add-in is
[`tools/tickprobe`](https://github.com/hexbinoct/casio-cg50/blob/main/tools/tickprobe/src/main.c).
