# Timers and time

The SH7305 has several units that count time. The OS uses only some of them, and no
periodic system tick has been found. Its sense of time comes from:

- the **RTC** (real-time clock): a calendar, a 1/128 s counter, and a periodic interrupt the
  OS turns on only while it sleeps, which gives the 2 Hz cursor blink;
- a **32.768 kHz down-counter** (ETMU5) that it reads for short delays;
- a **one-shot delay unit** at `0xA44A0000`;
- the other ETMU channels, behind its timer syscalls.

The TMU, the timer block most SH-4A code expects, was found stopped under the OS. gint
add-ins use it.

The OS's interrupt `0x560` looks like a timer tick but is not one: it is the end of a
[battery A/D conversion](battery-adc.md).

!!! warning "Unconfirmed"
    "No periodic tick" rests on what probes saw from an add-in (TMU and ETMU0 stopped, only
    the ETMU5 counter moving) and on the OS's idle routine below. The OS does have handlers
    for ETMU0–4, so some apps may run those timers.

| Base | Unit | Counts at | Used by |
|---|---|---|---|
| `0xA413FEC0` | RTC | 128 steps per second (R64CNT) | OS: calendar, ticks, 2 Hz wake-up |
| `0xA44D0030` + `0x20`·i | ETMU0–5 | 32.768 kHz | OS: ETMU5 free-running, ETMU0–4 timer syscalls; gint |
| `0xA4490000` | TMU0–2 | Pφ/4 to Pφ/256 | gint only; the OS leaves them stopped |
| `0xA44A0000` | One-shot delay unit | unknown | OS short delays |

## The 32.768 kHz counter (`0xA44D00D8`)

The word at `0xA44D00D8` is ETMU5's count register (TCNT). The OS runs ETMU5 as a
free-running 32-bit **down-counter**. `0xA44D00C8` reads the same value.

!!! success "Verified on hardware"
    A probe add-in, switched back into the OS's environment, read `0xA44D00D8` at two RTC
    second boundaries: it went down by exactly **32,768** per RTC second. A second probe
    sampled every timer register for two RTC seconds: `0xA44D00D8` and `0xA44D00C8` were the
    only words that moved, both from `0xF86C7B1C` to `0xF86B7B1F` (65,533 counts). All 32
    bits are used.

How the OS uses it:

- Syscall `0x08D8` (`0x800C4924`) starts it: TCOR = TCNT = `0xFFFFFFFF`, TCR = 0, TSTR = 1.
- `0x803742F8` (syscall `0x1ED5`) is a delay of about **1 ms**: it reads the counter, then
  spins until `(start − now) & 0xFFFFFF` reaches 33. 33 counts at 32.768 kHz is 1.007 ms.
  Only the low 24 bits are compared.
- `0x80318D9C` (syscall `0x1BB4`) calls that delay *n* times, so it waits *n* milliseconds.
  More than 30 places in the OS call it.

!!! warning "Unconfirmed"
    The three routines are read from OS 3.60 code. Their timing on a real calculator has not
    been measured.

!!! info "Verified in the emulator"
    When the emulated counter did not move, the OS hung forever in `0x803742F8` on its way
    to the main menu (called from the shell with a 20 ms wait). Test: `TestETMUChannels`
    checks the counter view at `0xA44D00D8`.

Inside a gint add-in's own environment, the probes read a difference of **0** over one RTC
second. That does not show the counter stopped: a channel that reloads every 256 counts
(a 128 Hz timer) also returns to the same value after exactly one second.

## ETMU channels

ETMU stands for "extra timer unit". There are six identical channels, 32 bytes apart,
starting at `0xA44D0030`. They all count down at 32.768 kHz.

| Offset in a channel | Register | Size | Meaning |
|---|---|---|---|
| `+0x0` | TSTR | 8-bit | Bit 0: the channel runs |
| `+0x4` | TCOR | 32-bit | Reload value |
| `+0x8` | TCNT | 32-bit | Current count, counts down |
| `+0xC` | TCR | 8-bit | Bit 1 UNF: underflow happened (write 0 to clear). Bit 0 UNIE: interrupt on underflow |

When TCNT counts past 0 it reloads from TCOR, sets UNF, and requests the channel's
interrupt if UNIE is set. One period is (TCOR + 1) / 32768 seconds.

| Channel | Base | Interrupt (INTEVT) |
|---|---|---|
| ETMU0 | `0xA44D0030` | `0x9E0` |
| ETMU1 | `0xA44D0050` | `0xC20` |
| ETMU2 | `0xA44D0070` | `0xC40` |
| ETMU3 | `0xA44D0090` | `0x900` |
| ETMU4 | `0xA44D00B0` | `0xD00` |
| ETMU5 | `0xA44D00D0` | `0xFA0` |

The OS's interrupt table, as dumped from a real calculator, has handlers for ETMU0–4 but
**none for ETMU5**; see [Interrupts](interrupts.md#the-oss-handlers) for the INTC fields and
what goes wrong with `0xFA0`.

!!! info "Verified in the emulator"
    The gint add-ins DASM and Upsilon run with this layout. gint's driver writes every
    register of every channel at start-up and spins until each write reads back; an emulator
    that did not echo them hung there. gint picks the highest free channel for its 128 Hz
    keyboard scan, which is ETMU5. Tests: `TestETMUChannels`,
    `TestTimerChannelsSaveState`, `TestResumeInsideGintAddin`.

The OS's timer syscalls `0x08D9`–`0x08DC` (`0x800C4978`, `0x800C4A40`, `0x800C4AC2`,
`0x800C4B1E`; Timer_Install, Timer_Deinstall, Timer_Start and Timer_Stop in WikiPrizm's
naming) sit next to a table of the ETMU1–4 and ETMU0 base addresses. The helper routines
before them clear UNF and UNIE and load a default period of **`0x333`** into TCOR when a
timer slot has none: 820 counts, **25.0 ms**.

!!! success "Verified on hardware"
    Read from an add-in in the OS's environment: ETMU0's TCOR and TCNT both hold `0x333`,
    and its TSTR is 0 (stopped at that moment). The words at `0xA44D0024` and `0xA44D0028`
    also hold `0x333`.

!!! warning "Unconfirmed"
    Which channel each timer slot uses, and when the OS runs them, has not been traced. What
    the words at `+0x24`/`+0x28` are is unknown.

## TMU

The TMU has three channels. They count down at a fraction of the peripheral clock Pφ.

| Address | Register | Size | Meaning |
|---|---|---|---|
| `0xA4490004` | TSTR | 8-bit | Bits 0–2: channel 0–2 runs |
| `0xA4490008` + `0xC`·i | TCOR | 32-bit | Reload value |
| `0xA449000C` + `0xC`·i | TCNT | 32-bit | Current count |
| `0xA4490010` + `0xC`·i | TCR | 16-bit | Bits 0–2 TPSC: Pφ/4, /16, /64, /256. Bit 5 UNIE. Bit 8 UNF |

Interrupts: `0x400`, `0x420`, `0x440` for channels 0, 1, 2. The OS's interrupt table (dumped
from a real calculator, see [Interrupts](interrupts.md#the-oss-handlers)) has no handler for
any of them.

!!! success "Verified on hardware"
    In the OS's environment, TSTR is **0**: no TMU channel runs. TMU1 is programmed but not
    started (TCOR = TCNT = `0x0002D000`, TCR = `0x0023`). TMU0 holds TCOR = TCNT =
    `0xFFFFFFFF`, TCR = 0; TMU2 holds TCOR = `0xFFFFFFFF`, TCNT = 0, TCR = `0x0003`.

!!! info "Verified in the emulator"
    gint add-ins use the TMU for their sleeps. Upsilon sleeps 0 ms early on, so gint
    starts TMU2 with TCOR = 0 and it underflows continuously. That only works with
    CPUOPM.INTMU set (see [CPU](../cpu.md#cpuopmintmu-0xff2f0000-bit-3)). Tests:
    `TestTMUChannel`, `TestUpsilonStarts`.

!!! warning "Unconfirmed"
    The emulator takes Pφ as **29.4912 MHz** (a CPU clock of 117.96 MHz divided by 4). This
    has not been measured. A 1 ms gint timer (a TMU channel, per the project notes) counted
    1001 ms per RTC second on a real calculator, so the timer clock gint assumes is right to
    about 0.1%. Whether that equals the emulator's figure has not been checked.

## RTC (`0xA413FEC0`)

The real-time clock keeps a calendar in BCD (binary-coded decimal: each 4 bits hold one
digit). Its layout matches the SH7724's RTC.

| Address | Register | Size | Meaning |
|---|---|---|---|
| `0xA413FEC0` | R64CNT | 8-bit | Sub-second counter, 128 steps per second (bits 6–0) |
| `0xA413FEC2` | RSECCNT | 8-bit | Seconds (BCD) |
| `0xA413FEC4` | RMINCNT | 8-bit | Minutes |
| `0xA413FEC6` | RHRCNT | 8-bit | Hours |
| `0xA413FEC8` | RWKCNT | 8-bit | Day of the week, 0 = Sunday |
| `0xA413FECA` | RDAYCNT | 8-bit | Day of the month |
| `0xA413FECC` | RMONCNT | 8-bit | Month |
| `0xA413FECE` | RYRCNT | 16-bit | Year, 4 BCD digits |
| `0xA413FEDC` | RCR1 | 8-bit | Bit 7 CF (carry: a count rolled over while being read), bit 4 CIE, bit 3 AIE, bit 0 AF |
| `0xA413FEDE` | RCR2 | 8-bit | Bit 7 PEF (periodic flag), bits 6–4 PES (periodic rate), bit 3 RTCEN, bit 2 ADJ, bit 1 RESET, bit 0 START |

PES selects the periodic interrupt: 1 = every 1/256 s, 2 = 1/64 s, 3 = 1/16 s, 4 = 1/4 s,
**5 = 1/2 s**, 6 = 1 s, 7 = 2 s, 0 = off. Each period sets PEF and requests interrupt
`0xAA0`.

!!! success "Verified on hardware"
    RSECCNT advances once per 32,768 counts of the ETMU5 counter (probe, OS environment).

!!! info "Verified in the emulator"
    The OS's use of PES = 5, PEF and interrupt `0xAA0` works only with this model (see
    [The 2 Hz wake-up](#the-2-hz-wake-up)). Test: `TestRTCPeriodicInRunMatrix`.

!!! warning "Unconfirmed"
    The OS reads the year, month, day, hours, minutes and seconds at these addresses (the
    routine at `0x801DF990`) and uses RCR1's carry flag, PES and PEF. The other RCR1/RCR2
    bits are listed as on the SH7724 and have not been checked.

### Ticks

`RTC_GetTicks` (syscall `0x02C1`, `0x80057DBC`) returns **R64CNT + 128 × (seconds since
midnight)**, from RSECCNT, RMINCNT and RHRCNT. It clears RCR1's carry flag first and reads
again if the flag was set during the read. So one tick is 1/128 s.

The start-up code also waits on R64CNT: at `0x80000B5C` (and a copy at `0x800207B8`) it
spins until R64CNT has moved on by more than 2 steps, roughly 20 ms. The code right after
the wait writes the bus controller (`0xFEC15040`).

!!! warning "Unconfirmed"
    Read from OS 3.60 code. The 128 steps per second of R64CNT has not been timed on a
    calculator.

### The 2 Hz wake-up

The OS turns the periodic interrupt on **only around `sleep`**. Its idle routine at
`0x802AE742`:

1. sets RCR2 = (RCR2 & `0x0F`) | `0x50`: PES = 1/2 s;
2. sets SR.BL (blocks interrupts) with IMASK = 0;
3. calls `0x801DF084`; if it returns 1, writes 0 to the clock generator register
   `0xA4150020` and executes `sleep`, otherwise calls `0xA0020926`;
4. after waking, clears SR.BL and sets RCR2 &= `0x0F`, turning the periodic interrupt off.

`sleep` halts the CPU until any interrupt request arrives, even with BL set; the request is
taken once BL is cleared (see [Interrupts](interrupts.md)). The RTC handler at
`0x801DFC6C` clears RCR2's PEF and PES, then sets bit 7 of the flag byte at `0x8C0A1568`
(through `0x802AF458`). The main loop then calls the cursor blink routine `0x800C3FA8`
(syscall `0x08CC`, called at `0x801E6228`), which draws or erases the text cursor (see
[Display](display.md#the-text-cursor-is-drawn-straight-into-gram)).

!!! info "Verified in the emulator"
    In Run-Matrix the RTC handler runs 6 times in 3 emulated seconds and the OS sleeps
    between them; without the RTC's periodic interrupt the cursor never blinked. Tests:
    `TestRTCPeriodicInRunMatrix`, `TestCursorBlinks`, `TestSleepWakesOnRequest`.

!!! warning "Unconfirmed"
    The OS sets PES again on every pass through its idle routine. The emulator keeps the
    period on the RTC's own 1/2 s grid, so setting PES again does not restart it. That is
    how the SH7724's RTC derives the periodic event, but it has not been measured here.
    What `0x801DF084` checks, and the other sleep path `0xA0020926`, have not been traced.

!!! danger "Careful with the CPG and `sleep` from probe add-ins"
    A probe add-in that copied this idle routine (write `0xA4150020`, then `sleep`) reset a
    real calculator to its first-boot setup: files survived, everything in RAM was lost.
    Don't `sleep` or touch the CPG from a probe.

## The one-shot delay unit (`0xA44A0000`)

The OS's other delay, `0x801DF5AA` (syscall `0x11D6`; `0x801DF69A`, syscall `0x11D7`,
multiplies its argument by 100 and jumps to it), uses a unit at `0xA44A0000`:

1. clears bit 14 of the clock generator register `0xA4150030` (and sets it again at the end
   if it was set);
2. clears the start bit (bit 5 of `+0x00`) and waits for bit 13 of `+0x60` to clear;
3. writes `+0x60` = 5, `+0x64` = 0, and a computed count to `+0x68`;
4. sets the start bit and spins until **bit 15 of `+0x60`** is set.

It is called from about 20 places, among them the routine at `0x801E6D40` that programs the
unknown unit at `0xA44C0000`.

!!! warning "Unconfirmed"
    Read from OS 3.60 code. The unit's clock, and so the length of a delay per unit of the
    argument, is unknown.

## No counter at `0xA4130000`

Older notes, and the emulator, treat `0xA4130000` as a free-running counter. In OS 3.60
nothing points there. No 4-byte-aligned word in the OS image holds an address between
`0xA4130000` and the RTC (the only values in that range are odd numbers inside code and
data), and an emulated 3.60 boot (the first 3 million instructions) never reads that range.
The start-up's "wait until a counter moves" loops read the RTC's R64CNT at `0xA413FEC0`,
which lies inside the same 64 KB page.

!!! warning "Unconfirmed"
    Checked by a scan of the OS image and one emulated boot, not on hardware. Whether
    anything exists at `0xA4130000` on the chip is not known.

## How fast the calculator runs

The clock settings on a real calculator:

!!! success "Verified on hardware"
    FRQCR (`0xA4150000`, the clock generator's frequency control register) reads
    `0x0F011112` from an add-in.

!!! warning "Unconfirmed"
    The value is taken to be the stock setting, with the CPU at about **118 MHz**. The
    register has not been decoded here and nobody has timed the CPU directly.

How many instructions per second the OS actually gets is less clear. The only comparison so
far uses the main menu's key repeat. With a key held, the keyboard is scanned every 30.3 ms;
the OS repeats after 20 scans, then once per scan, so it can move the menu cursor at most
33 times a second (see [Keyboard](keyboard.md)).

!!! success "Verified on hardware"
    The 30.3 ms scan period (132 scans in 4 s with a key held) was measured by a probe
    add-in.

In the emulator, each move costs roughly 2 million instructions (1.3–2.5 million in
different measurements), mostly in the OS's per-pixel
[bitmap routine](display.md#the-oss-bitmap-routine). With RIGHT held on the main menu, the emulator gives (probe
`TestRepeatRateProbe`, emulated time):

| Instructions per emulated second | Moves per second after the first repeat |
|---|---|
| 22 million | 8.6 |
| 44 million | 17.4–17.8 |
| 66 million | 24.5 |
| 100 million | 29 |
| 150 million | 33 (the OS's limit) |

On a real calculator, holding RIGHT for 1.2 s gives **11 or 12** moves, counted by eye. The
emulator gives the same at **45 million** instructions per emulated second, and 17–18 moves
at 100 million. So for this workload the calculator behaves like about 45 million emulator
instructions per second, not the ~100 million a 118 MHz clock might suggest.

!!! warning "Unconfirmed"
    The 45 million figure comes from one workload (menu scrolling), with the real side
    counted by eye, not by a probe. The emulator's figures are from probe runs, not from
    tests that hold them. The reason the real figure is lower than the clock (wait states on
    flash, slow writes to video RAM) is a guess. Instructions per second on the real chip
    have not been measured and will differ between workloads.

## In the emulator

The emulator counts executed instructions (plus time spent in `sleep`) and turns them into
time with one setting, instructions per emulated second (`SetInstrPerSecond`). The RTC, the
ETMU and TMU counters, the key scan and the battery converter all derive their timing from
it. It defaults to 70 million; the Android app uses 45 million ("Original hardware") or 100
million ("Fast"); the frozen boot goldens use 1 million so the start-up's R64CNT waits stay
short.

Counters are computed when read, from the instruction count at which they started; device
events are scheduled instead of polled every instruction, and a sleeping CPU skips ahead to
the next event. `TestScheduledStepMatchesExact` proves this gives a bit-identical machine to
polling every device before every instruction.

- [`emu_go/gtimer.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/gtimer.go):
  ETMU0–5 and TMU0–2 (`timerChan`, `etmuCounter`, `tmu`), their save-state keys. Until
  software programs ETMU5, `0xA44D00D8`/`C8` return the older free-counter view the boot
  golden was frozen with. Tests in
  [`emu_go/gtimer_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/gtimer_test.go)
  (`TestETMUChannels`, `TestTMUChannel`) and `emu_go/resume_addin_test.go`
  (`TestTimerChannelsSaveState`, `TestResumeInsideGintAddin`, `TestUpsilonStarts`).
- [`emu_go/rtc.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/rtc.go): the
  RTC. The calendar starts at 2010-01-01 00:00:00 (a Friday) so runs are repeatable; hosts
  can set the real time. Tests in
  [`emu_go/rtc_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/rtc_test.go)
  (`TestRTCRegisters`, `TestSleepWakesOnRequest`, `TestRTCPeriodicInRunMatrix`) and
  `TestCursorBlinks` in `emu_go/lcd_test.go`.
- [`emu_go/mmio.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmio.go):
  `SetInstrPerSecond`; `etmu`, a stub for `0xA44A0000` whose `+0x60` always reads `0x8000`,
  so every one-shot delay ends at once; `freeCounter`, the catch-all at `0xA4130000` that
  rises on every read.
- [`emu_go/schedule_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/schedule_test.go):
  `TestScheduledStepMatchesExact`.
- The Python oracle (`emu/mmio.py`: `RTC`, `ETMUCounter`, `ETMU`, `FreeCounter`) models the
  RTC and the ETMU5 counter the same way. The ETMU0–5 channels and the TMU exist only in
  the Go emulator; the frozen boot goldens do not depend on them.
