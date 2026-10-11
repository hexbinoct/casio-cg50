# Open questions

Everything the other pages mark as **Unconfirmed** or "not known", gathered in one place and
grouped by area. Each item says what is known so far, links to the section that explains it,
and says what would settle it:

- **Safe probe**: an add-in on a real calculator that only reads registers or runs ordinary
  instructions. See [Writing probe add-ins](../guides/probe-addins.md).
- **Tracing**: more reading of the OS 3.60 code (Ghidra or `re/sh4dis.py`).
- **Not safe to probe**: the direct test would need a `sleep`, a CPG write or a flash write.
  Answer it by tracing instead.

!!! danger "Probes must never sleep, touch the CPG or write the flash"
    One probe that replayed the OS's idle `sleep` reset a real calculator. A probe add-in must
    not execute `sleep`, must not write the clock registers at `0xA4150000`–`0xA41500FF`
    (reading one is the owner's call), and must not write the flash. See
    [Writing probe add-ins](../guides/probe-addins.md) and
    [How facts are verified](verification.md#the-probes).

## CPU

- **What does an `F…` opcode do on the calculator?** There is no FPU; on the SH4AL-DSP these
  opcodes are DSP instructions. A 163-million-instruction run of the OS met none.
  [CPU → No FPU](../cpu.md#no-fpu).
  *Settle:* safe probe: run one DSP opcode and read back the registers.
- **Is SR bit 12 always 1, or does something set it?** An add-in reads `SR = 0x40001101`, but
  the OS start-up loads SR with bit 12 clear (`0x500000F0`).
  [CPU → SR bit 12 is set](../cpu.md#sr-bit-12-is-set).
  *Settle:* safe probe: `ldc` a value with bit 12 clear into SR, then `stc` it back.
- **Which line does the error screen show for each exception cause?** The table and the
  messages are read from the OS image; nothing has been triggered.
  [CPU → Exceptions and their vectors](../cpu.md#exceptions-and-their-vectors).
  *Settle:* safe probe: an unaligned read, and an undefined opcode.
- **Is a PC-relative load in a delay slot computed from the slot's address?** The emulator
  does so; the calculator has not been tested.
  [CPU → Delay slots](../cpu.md#delay-slots).
  *Settle:* safe probe: a `mov.l @(disp,PC)` in the slot of an `rts`.
- **Why does the deeper `sleep` path run uncached and rewrite the bus controller?** A
  low-power state that keeps the CPU off the DRAM would fit.
  [CPU → `sleep`](../cpu.md#sleep).
  *Settle:* tracing. Not safe to probe.
- **What does `0xFF000024` hold at reset?** It is EXPEVT. The boot turns its low 16 bits into
  a model code, and the OS needs model code `0xCA02`, so the emulator returns `0x0A02`.
  [Boot → From reset](../os/boot.md#from-reset).
  *Settle:* an add-in runs long after reset, when exceptions may have overwritten EXPEVT, so
  an add-in probe cannot answer this.
- **What is `0xFF2F0004`?** Both the boot code's OS-startup step (at `0xA00006E4`) and the OS
  start-up (at `0x80020384`) write 0 to it, and no other code in the OS image loads its address. On
  the SH7724 this address is EXPMASK, next to CPUOPM (`0xFF2F0000`); whether the SH7305's
  bits mean the same is not known. No page covers it yet.
  *Settle:* safe probe: read it.
- **In what order are equal-level interrupts taken?** The emulator takes the highest INTEVT
  first.
  [Interrupts → How the CPU accepts an interrupt](../peripherals/interrupts.md#how-the-cpu-accepts-an-interrupt).
  *Settle:* a probe with two timer interrupts at the same level (more than a read).

- **What does a patched code page need on the calculator?** A debugger that patches a page in
  RAM must also deal with the ITLB and the instruction cache. The emulator has neither, so it
  cannot check that part.
  [MMU → Software breakpoints](../os/mmu-and-addins.md#redirecting-a-page-software-breakpoints).
  *Settle:* safe probe: patch a `trapa` into a RAM copy of a page and see whether it fires.

## Memory

- **What do the three `CASIOMEMDATA` blocks hold?** The code that erases and writes them is
  around `0x802AF800`. They contain main memory's folder names (`system`, `library`,
  `@LOCAL40`), which supports the guess that power-off saves main memory there; the format
  is not traced.
  [Memory map → Flash](../memory-map.md#flash), [Flash → The OS's own area](../flash.md#the-oss-own-area).
  *Settle:* tracing.
- **How big are the blocks at `0xFE200000`, and what lies between them?** The sizes on the
  page are what the OS clears. Two sets of three blocks, 256 KB apart, look like a DSP's
  program and X/Y data memories, as on the SH7724's SPU2.
  [Memory map → The memory at `0xFE200000`](../memory-map.md#the-memory-at-0xfe200000).
  *Settle:* safe probe: a read-only dump in steps across the 2 MB.

- **Which flash chip is it?** The OS's chip check finds a manufacturer ID of 1 (AMD/Spansion),
  and 128 KB sectors with a 512-byte write buffer fit their S29GL-S family. The device ID has
  never been read.
  [Flash → How big the write buffer is](../flash.md#how-big-the-write-buffer-is).
  *Settle:* tracing. Reading the ID needs flash commands, so it is not a plain read probe.
- **Who sets SR.BL before the storage routines run, and what are the IL RAM routines used
  from `0x801E38xx` for?**
  [Flash → Why the routines run from IL RAM](../flash.md#why-the-routines-run-from-il-ram).
  *Settle:* tracing.
- **How does the storage memory's translation layer work?** What the sector header's *N*
  counts, the page headers, and how the newest copy of a page is found.
  [Flash → The storage memory](../flash.md#the-storage-memory).
  *Settle:* tracing (the 32 MB dump has the data).
- **Why does an add-in launch map virtual `0x0`–`0xFFFF` (slot 63) onto flash address 0?**
  [MMU → What the add-in finds when it starts](../os/mmu-and-addins.md#what-the-add-in-finds-when-it-starts).
  *Settle:* tracing.
- **Is an add-in limited to 2 MB (or 1 MB) of code?** The page table has room for 512 pages,
  but the mapper clears only 256.
  [MMU → Code pages on demand](../os/mmu-and-addins.md#code-pages-on-demand).
  *Settle:* tracing.

## Peripherals

### Interrupts

- **Which device raises INTEVT `0xF00`?** The OS has a handler for it.
  [Interrupts → The OS's handlers](../peripherals/interrupts.md#the-oss-handlers).
  *Settle:* tracing.
- **Are `0x620`/`0x640` (IRQ1/IRQ2 pins), `0xA20` (USB) and `0xC00` (serial port) right?**
  The identifications rest on the SH7724's register layouts, and every bit the OS uses
  matches them. No probe has used these units.
  [Interrupts → External pins IRQ1 and IRQ2](../peripherals/interrupts.md#external-pins-irq1-and-irq2),
  [USB and the serial port](../peripherals/interrupts.md#usb-and-the-serial-port).
  *Settle:* safe probe: read INTREQ00 (`0xA4140024`) with and without a cable.
- **What do bits 7 and 5 of `0xA4150020` do?** On the SH7724 this is STBCR, and bit 7 (STBY)
  makes `sleep` enter software standby. Bit 5 is unknown; the deeper idle routine clears it.
  [Interrupts → The OS 3.60 dispatcher](../peripherals/interrupts.md#the-os-360-dispatcher).
  *Settle:* tracing. It is a CPG register: never write it from a probe.
- **When does the OS enable the ETMU interrupts?** The OS's ETMU handlers touch IPRJ, IPRG,
  IPRE and IPRI; the mask bits come only from gint.
  [Interrupts → Sources and their fields](../peripherals/interrupts.md#sources-and-their-fields).
  *Settle:* tracing.
- **How long does the boot code's interrupt dispatcher stay in use?** In the real IL RAM dump
  its table at `0xFD8004D0` is gone.
  [Interrupts → The boot code's dispatcher](../peripherals/interrupts.md#the-boot-codes-dispatcher).
  *Settle:* tracing.

### Keyboard

- **Which keys sit at column 11, rows 1, 3, 5 and 6?** The OS's position table has entries
  there; row 3 gives the diagnostic key code `0x7D00`. Only RIGHT has been read on the
  calculator.
  [Keyboard → The key matrix](../peripherals/keyboard.md#the-key-matrix).
  *Settle:* safe probe: read the key-data words while each key is held.
- **Are `+0x1A`/`+0x1C` column and row enables?** The reading rests on their widths (12 and
  8 bits). The emulator models them that way.
  [Keyboard → Registers](../peripherals/keyboard.md#registers).
  *Settle:* safe probe: set both to 1 with the key interrupt masked, press keys, read the
  key-data words.
- **What do bit 1 of `+0x12` and the register at `+0x1E` mean?** Both read `0x0002`. The OS
  only tests bit 0 of `+0x12` (scan in progress).
  [Keyboard → Registers](../peripherals/keyboard.md#registers).
  *Settle:* tracing.
- **What do the other mode values and status flags 2, 4, 5 and 6 mean?** Only mode `0x0200`
  has been seen; flags 2, 4, 5 and 6 never.
  [Keyboard → Mode](../peripherals/keyboard.md#mode-0x10),
  [Status flags](../peripherals/keyboard.md#status-flags-0x14).
  *Settle:* tracing.
- **Do the key-data words hold the last scan or the live keys?** A debugger add-in behaved as
  if they held the last scan. The emulator shows live keys.
  [Keyboard → When the unit scans](../peripherals/keyboard.md#when-the-unit-scans).
  *Settle:* safe probe: read the words while pressing and releasing a key, without starting a
  scan.
- **What does the PFC byte `0xA40501C6` configure?** Every routine that switches the
  keyboard lines writes it.
  [Keyboard → Setup](../peripherals/keyboard.md#setup).
  *Settle:* tracing.
- **Which apps use the second key-repeat mode or the slow-down switch?**
  [Keyboard → Key repeat](../peripherals/keyboard.md#key-repeat).
  *Settle:* tracing.
- **Who calls the scan on demand, and for which keys?**
  [Keyboard → A scan on demand](../peripherals/keyboard.md#a-scan-on-demand).
  *Settle:* tracing.
- **The single-key check: which key does each caller ask about, and which port column is
  which key?** Syscall `0x127E` drives 12 columns through `0xA4050136`/`0xA4050138` and reads
  rows from `0xA405016C`. The emulator cannot model it without the mapping.
  [Keyboard → Checking one key without the unit](../peripherals/keyboard.md#checking-one-key-without-the-unit).
  *Settle:* tracing for the callers; safe probe for the mapping: drive one column and read
  `0xA405016C` while a key is held.

### Display

- **Is the double read of the data port after selecting R003 a dummy read?**
  [Display → Setting a window](../peripherals/display.md#setting-a-window).
  *Settle:* tracing, or a probe that reads the port once and twice.
- **Which module does bit 21 of `0xA4150030` stop?** The OS clears it before every frame push.
  [Display → Pushing a frame](../peripherals/display.md#pushing-a-frame).
  *Settle:* tracing. MSTPCR0 is a CPG register: never write it from a probe.
- **Are bit 5 of `0xA405012E` the backlight, R100 bit 2 a standby bit, and what does R111
  do?** Inferred from when the OS writes them at power-off and power-on.
  [Display → Switching the panel off and on](../peripherals/display.md#switching-the-panel-off-and-on).
  *Settle:* safe probe: read `0xA405012E`.
- **How many instructions per second does the calculator run?** About 45 million on OS code,
  from menu scrolling counted by eye against the emulator.
  [Display → How fast the screen updates](../peripherals/display.md#how-fast-the-screen-updates),
  [Timers → How fast the calculator runs](../peripherals/timers.md#how-fast-the-calculator-runs).
  *Settle:* safe probe: time a known loop against the RTC.

### Timers and clock

- **Does any app run ETMU0–4?** No periodic tick has been found, but the OS has handlers for
  them.
  [Timers and time](../peripherals/timers.md),
  [ETMU channels](../peripherals/timers.md#etmu-channels).
  *Settle:* tracing.
- **What are `0xA44D0024` and `0xA44D0028`?** Nothing in the OS refers to them; they sit
  where a channel at `0xA44D0020` would have its TCOR and TCNT.
  [Timers → ETMU channels](../peripherals/timers.md#etmu-channels).
  *Settle:* safe probe: read them.
- **How long do the 32.768 kHz delay routines take on the calculator?**
  [Timers → The 32.768 kHz counter](../peripherals/timers.md#the-32768-khz-counter-0xa44d00d8).
  *Settle:* safe probe: time them against the RTC.
- **Is Pφ exactly 29.4912 MHz?** A 1 ms gint timer counted 1001 ms per RTC second, so the
  figure is right to about 0.1%.
  [Timers → TMU](../peripherals/timers.md#tmu).
  *Settle:* a longer timing run of the same kind.
- **What do the other RCR1/RCR2 bits do?** They are listed as on the SH7724.
  [Timers → RTC](../peripherals/timers.md#rtc-0xa413fec0).
  *Settle:* safe probe: read RCR1/RCR2.
- **Does R64CNT really count 128 steps per second?**
  [Timers → Ticks](../peripherals/timers.md#ticks).
  *Settle:* safe probe: read R64CNT across several RTC seconds.
- **Does setting PES again restart the 2 Hz period?** The emulator keeps the RTC's own 1/2 s
  grid, as the SH7724 does.
  [Timers → The 2 Hz wake-up](../peripherals/timers.md#the-2-hz-wake-up).
  *Settle:* a probe that writes RCR2 (not just a read).
- **Which level does the "USB" pin (`0xA4050162` bit 1) show, with and without a cable?** It
  picks the plain or the deeper `sleep` path. The emulator reads 0, so it always takes the
  deeper one.
  [Timers → The 2 Hz wake-up](../peripherals/timers.md#the-2-hz-wake-up),
  [Interrupts → External pins IRQ1 and IRQ2](../peripherals/interrupts.md#external-pins-irq1-and-irq2).
  *Settle:* safe probe: read `0xA4050162` with and without a cable.
- **What clocks the one-shot delay unit at `0xA44A0000`?** Clearing MSTPCR0 bit 14 probably
  gives it its clock.
  [Timers → The one-shot delay unit](../peripherals/timers.md#the-one-shot-delay-unit-0xa44a0000).
  *Settle:* tracing. Not safe to probe (it means writing MSTPCR0).
- **What is the unit at `0xA44C0000`?**
  [Timers → The unit at `0xA44C0000`](../peripherals/timers.md#the-unit-at-0xa44c0000).
  *Settle:* tracing.
- **Is there anything at `0xA4130000`?** The OS never uses it.
  [Timers → No counter at `0xA4130000`](../peripherals/timers.md#no-counter-at-0xa4130000).
  *Settle:* a read probe, once it is known that reading an unused address there is harmless.
- **What does FLLFRQ (`0xA4150050`) hold, and how fast is the CPU clock?** The OS 3.60 never
  sets FLLFRQ; the 117.96 MHz figure rests on the boot's settings.
  [Timers → How fast the calculator runs](../peripherals/timers.md#how-fast-the-calculator-runs).
  *Settle:* reading FLLFRQ is a CPG read, the owner's call; otherwise time the CPU against
  the RTC.

### Battery A/D converter

- **What do bit 14 of `+0x88` and the value `0x01CF` mean?**
  [Battery ADC → Registers](../peripherals/battery-adc.md#registers).
  *Settle:* tracing.
- **Does a software start work the same on the calculator?**
  [Battery ADC → Software start](../peripherals/battery-adc.md#software-start-0x801de64a).
  *Settle:* a probe that starts a conversion (no `sleep`).
- **Does the idle conversion only end while the CPU sleeps?** Inferred from measurements.
  [Battery ADC → The idle conversion only ends while the CPU sleeps](../peripherals/battery-adc.md#the-idle-conversion-only-ends-while-the-cpu-sleeps).
  *Settle:* not safe to probe: the direct test reset a calculator.
- **What does each battery level mean to the user?**
  [Battery ADC → In the OS](../peripherals/battery-adc.md#in-the-os-360).
  *Settle:* tracing.

## OS

- **What exactly does power-off do?** Not traced: what step 1 saves in the flash, what it
  copies between `0xE5007000` and `0xE5200000`, what the step-6 loop counts with 3600, and
  whether the real CPU stays in the same `sleep` while off.
  [Boot → Power-off](../os/boot.md#power-off).
  *Settle:* tracing. Not safe to probe.
- **What triggers the three restarts?** The multi-step routine, the confirmation screen that
  calls `0x80365FC6`, and what leads to syscall `0x11ED` are not traced.
  [Boot → Restarts](../os/boot.md#restarts).
  *Settle:* tracing.
- **What do `0x8035FCE8` and `0x801504DA` check during first-boot setup?** `0x801504DA` looks
  for a 64-byte record whose second half-word is `0x95FF`.
  [Boot → First-boot setup](../os/boot.md#first-boot-setup).
  *Settle:* tracing.

- **What do r4 and r5 mean when an add-in starts, and what happens when its entry function
  returns?** From the MAIN MENU they are 1 and 0. The launch also reads the add-in's code
  pages (about 3,000 reads) before mapping them, perhaps for a checksum.
  [MMU → What the add-in finds when it starts](../os/mmu-and-addins.md#what-the-add-in-finds-when-it-starts).
  *Settle:* tracing.
- **What calls syscall `0x0028`, which rebuilds a running add-in's mapping?** Perhaps after the
  storage memory moved the file.
  [MMU → Rebuilding the mapping](../os/mmu-and-addins.md#rebuilding-the-mapping).
  *Settle:* tracing.
- **Does any syscall number mean a different function in 3.80 than in 3.60?** At least 7,415
  numbers start with the same code in both; the rest were not compared. What a number past
  `0x1F67` does on the calculator is also untested (the call routine has no range check).
  [Syscalls → OS versions](../os/syscalls.md#os-versions).
  *Settle:* tracing. Never call an out-of-range number on a calculator.
- **How does the formatter pick the number of digits?** Setup item `0x3D` is involved; the four
  cases of step 4 look like Norm, Fix, Sci and Eng; what the settings block at `0x8C08B8A0`
  is, and whether step 2 restores the setup items it rewrites, is not traced.
  [Number format → From a stored value to the screen](../os/number-format.md#from-a-stored-value-to-the-screen).
  *Settle:* tracing.
- **What are bytes 9–11 of a stored number, and a first nibble of 2, 3 or 4?** The bytes are
  always 0 but the arithmetic reads them; the unpacker accepts the nibbles, but the OS was
  never seen storing them.
  [Number format → A real number](../os/number-format.md#a-real-number).
  *Settle:* tracing.

## The emulator

Known differences from the calculator. These need a code change rather than an answer
(details on each page's "In the emulator" section):

- `F…` (DSP) opcodes are counted and skipped, so an add-in using them computes wrong results
  without an error.
- SR bit 12 is never set.
- The single-key check never sees a key: the port pins are not wired to the keypad.
- The "USB" pin reads 0; nothing models the unit at `0xA44C0000`; the IRQ pins, USB and the
  serial port raise no interrupts (right, since nothing is plugged in).
- All 2 MB from `0xFE200000` is plain RAM.
- A stripe push that does not start at row 0 sets DAR past `0x14000000`, and the emulator
  sends a DMA to the panel only when DAR is exactly `0x14000000`. This may be related to
  `Bdisp_PutDisp_DD_stripe` (syscall `0x0260`) for rows 12–203 showing garbled on the
  emulator's panel while VRAM is right; that has not been traced. [Display → Pushing a frame](../peripherals/display.md#pushing-a-frame).
- The OS's interrupt `0x560` comes at level 8 (the OS programs 12) and the RTC's `0xAA0` at 9
  (the OS programs 8). [Interrupts → In the emulator](../peripherals/interrupts.md#in-the-emulator).
- The key-data words are live, not latched; `+0x1E` is not modelled.
- Battery ADC: `+0x88`/`+0x8A` are merged, `+0x90` does not mirror, `+0x8E` is not modelled.
- CPG `PLLCR` (`0xA4150024`) reads `0x4000`, without the FLL bit 12, so gint's
  `clock_freq()` computes Pφ as about 65 kHz instead of 29.49 MHz, and gint delays given in
  µs come out wrong.
- A leftover 64 KB region at `0xA4130000` from the early bring-up.
- The flash's ID and CFI reads are not modelled, so the OS's chip check falls back to 32-byte
  writes, where the calculator uses 512 (files come out the same); unlock-bypass programming
  is not modelled (no traced write uses it).
  [Flash → In the emulator](../flash.md#in-the-emulator).
- There is no ITLB and no instruction cache (the ITLB arrays at `0xF2000000`/`0xF3000000`
  read 0), and URC never counts up by itself (the OS always sets it).
  [MMU → In the emulator](../os/mmu-and-addins.md#in-the-emulator).

## Already answered

Questions the project's early notes left open that are now settled:

- **FPU or not:** no FPU. [CPU → No FPU](../cpu.md#no-fpu).
- **Where fls0 starts:** at physical `0xC80000`. [Memory map → Flash](../memory-map.md#flash).
- **What KEYSC `+0x12` reads:** 2 on the calculator (the emulator matches since 2026-10-09).
  [Keyboard → Registers](../peripherals/keyboard.md#registers).
- **What `0xA4140024` bit 6 is:** INTREQ00's IRQ1 flag (the USB pin), not "key-scan ready".
  [Interrupts → External pins IRQ1 and IRQ2](../peripherals/interrupts.md#external-pins-irq1-and-irq2).
- **Whether power-off reboots:** it doesn't; it is a `sleep` inside PowerOff (syscall
  `0x1839`). [Boot → Power-off](../os/boot.md#power-off).
- **Whether the OS uses a counter at `0xA4130000`:** no; its timers are the ETMU, the RTC and
  the unit at `0xA44A0000`. [Timers → No counter at `0xA4130000`](../peripherals/timers.md#no-counter-at-0xa4130000).
