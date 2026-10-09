# Boot and power-off

What the OS does between reset and the MAIN MENU, what it does while it waits for a key, what
"off" really is, and how a boot tells a restart with RAM intact apart from a cold start, which
runs the first-boot setup.

## From reset

The CPU starts at `0xA0000000`, the uncached view of flash address 0. The code at
`0x80000000`/`0xA0000000`:

1. **Registers.** Sets the stack to `0xFD804000` (top of the on-chip IL RAM) and SR to
   `0x700000F0` (privileged, register bank 1, exceptions blocked, all interrupts masked).
2. **Early pokes.** Writes `0x00010013` to the BSC (`0xFEC10000`), and three CPG registers
   (`0xA4150020` ← 1, `0xA4150030` ← `0x1000`, `0xA4150038` ← `0xFFFFFFFF`).
3. **Cache and TLB.** CCR (`0xFF00001C`) ← `0x800`, `icbi @0xA0000000`, then MMUCR
   (`0xFF000010`) ← 4, which flushes the TLB.
4. **Pins and watchdog** (`0xA0000670`). Four PFC port registers at `0xA4050184`, then the
   watchdog at `0xA4520000`: WTCNT ← `0x5A00`, WTCSR ← `0xA507` then `0xA504`.
5. **Clocks** (`0xA000069A`). Adjusts `0xA4150024`, writes `0x4384` to `0xA4150050`, and waits
   for bit 0 of `0xA4150060`. Then sets FRQCR (`0xA4150000`) to
   `(FRQCR & 0x000F00F0) | 0x8F001102` and waits for the same bit again.
6. **DRAM** (`0xA000063C`). Programs the bus/SDRAM controller: a block of timing registers
   at `0xFEC10000`–`0xFEC1003C`, then `0xFEC10040`. DRAM is usable from here.
7. **Model code.** Reads the low 16 bits of `0xFF000024` and stores a model code at
   `0x8C04CA24`: 0 → `0xCA00`, `0x20` → `0xCA01`, `0x0A02` → `0xCA02`. Any other value
   stores nothing.
8. **OS startup** (`0xA00006CC`), called in a loop that never returns. It runs a few
   uncached init routines, flushes the TLB again, sets a first VBR (`0x80001454`), lowers SR
   to `0x500000F0` (register bank 0), runs the cached OS init (`0x8000495A`, `0x80002600`,
   `0x80009E04`) and finally calls the **OS main loop at `0x80003550`** forever.

!!! info "Read from the 3.60 code; run by the emulator"
    The boot code of OS 3.60 is byte-for-byte the same as in the 3.80 updater, at the same
    addresses (`re/boot_compare.py`). The emulator runs this exact path on every cold boot of
    the 3.60 flash.

!!! warning "Unconfirmed: what `0xFF000024` holds at reset"
    On the SH-4A, `0xFF000024` is **EXPEVT**, the register that records which exception or
    reset happened; 0 and `0x20` are the codes for a power-on and a manual reset. Early notes
    called it a "hardware strap". The OS 3.60 needs model code `0xCA02` later in the boot
    (otherwise the storage-memory check never finishes), so the emulator returns `0x0A02`
    there after reset. What a real calculator reads there at reset has not been measured.

## Waiting for a key

The OS has no central loop that polls the hardware. Each screen (the MAIN MENU, an app, a
dialog) runs its own loop around **GetKey** (syscall `0x0EAB`, `0x801951A6`), and the CPU
sleeps whenever nothing is waiting. The chain from the MAIN MENU down to the CPU's `sleep`
instruction:

1. `GetKey` calls `0x801E5F9A` (syscall `0x12C0`, the body of `GetKeyWait_OS`, syscall
   `0x12BF`).
2. When there is no key, that calls `0x802AE790` (syscall `0x1834`), which calls the wait
   `0x8029512C` (syscall `0x16F6`; syscall `0x16F5` at `0x80295128` is the same wait with
   `r6` = 1).
3. The wait calls the **idle routine `0x802AE742`**, which arms the RTC's 2 Hz interrupt
   and executes `sleep` (see [Timers](../peripherals/timers.md#the-2-hz-wake-up)).
4. Any interrupt wakes the CPU: a key scan, the RTC's ½-second tick (it blinks the cursor),
   a timer. The OS handles it and either returns the key to the screen or goes back to sleep.

The key-wait code is also where **auto power-off** starts: once the calculator has been idle
for the time set in *Power Properties*, it calls `PowerOff(1)` from `0x801E65C0`.

!!! info "Verified in the emulator"
    The chain above is the one the emulator runs at the MAIN MENU (each call observed by a
    single-step trace). Left idle at the MAIN MENU with the default "Auto Power Off: 10 Min.",
    the OS calls `PowerOff` from `0x801E65C0` after **599.7** emulated seconds. Probes:
    `TestPowerOffTrace`, `TestAutoPowerOff`.

## Power-off

"Off" is not a reset. `PowerOff` (syscall `0x1839` at `0x802AEA24`, which sets `r5` = 1 and
runs into `0x802AEA26`, syscall `0x183A`) switches the screen off and puts the CPU to sleep
**inside the routine**. AC/ON wakes the CPU, the routine switches the screen back on and
returns to whoever called it. RAM, the open app and the call stack are simply still there.

Both ways of switching off call `PowerOff` with argument 1:

| How | Call (`jsr`) at |
|---|---|
| SHIFT, AC/ON (OFF) at the MAIN MENU | `0x80363ACA` |
| Auto power-off | `0x801E65C0`, in the key-wait code |

What it does, in order:

1. With argument 1, runs `0x802AF4FE`. It paints the **whole panel white** (VRAM and the
   frame around it are pushed to the screen) and **writes to the flash**, through the flash
   routines the OS keeps in IL RAM.
2. Depending on the keyboard driver's state (bytes from `0x8C090C1C`, see
   [Keyboard](../peripherals/keyboard.md)), it may idle for a while first (through
   `0x802AE790`).
3. Checks for an unfinished multi-step operation (below). If there is one, it restarts the
   OS instead of switching off.
4. Flushes the cache (CCR `0xFF00001C` ← `0x808`, from uncached code at `0xA0020562`) and
   takes a battery reading (`0x801DE54A`, see [Battery ADC](../peripherals/battery-adc.md)).
5. Switches the panel off: display off (syscall `0x0197`), then the controller's power-down
   (syscall `0x0192`), then clears bit 5 of the PFC byte `0xA405012E`. See
   [Display](../peripherals/display.md#switching-the-panel-off-and-on).
6. Loops in the idle routine `0x802AE742`, asleep, until AC/ON.

Switching back on, it reprograms the pin function controller (`0x802AE5B0`, syscall
`0x182E`), runs the full LCD initialisation (`0x8004DD3A`) with the display switched on, sets
bit 5 of `0xA405012E` again and redraws the screen.

!!! info "Verified in the emulator"
    SHIFT, AC/ON at the MAIN MENU, then AC/ON again: the OS goes through these steps, sleeps,
    wakes on AC/ON and is back at the MAIN MENU. The reset vector is never reached and the
    word `0xFD8017DC` (below) stays 0 throughout. Step 1 changed nearly all of the 64 KB of
    flash at `0xB90000`–`0xB9FFFF` and 3 bytes at `0xB80013`. Between the key press and the
    panel switching off about 0.7 emulated seconds pass. Probe: `TestPowerOffTrace`.

!!! warning "Unconfirmed"
    The order and the register writes are from the emulator's trace and the OS 3.60 code.
    What step 1 saves in the flash, what it copies between the on-chip memories at
    `0xE5007000` and `0xE5200000`, and what the loop in step 6 does besides sleeping (it
    counts with the constant 3600) have not been traced. Whether the real CPU stays in the
    same `sleep` while off has not been checked on hardware (a probe would need to `sleep`,
    which is not safe, see below).

!!! danger "Don't replay the power-off from a probe"
    Switching off goes through the same clock-generator writes and `sleep` as the idle
    routine. A probe add-in that copied them reset a real calculator to its first-boot setup.

## Restarts

The OS can also **restart itself** through the reset vector, keeping RAM. Syscall `0x1187`
(`0x801DE698`) switches the display off (syscall `0x0197`), writes `0xCA02` to EXPEVT
(`0xFF000024`) and 0 to INTEVT (`0xFF000028`), blocks interrupts (SR.BL = 1, IMASK = 15) and
jumps to `0xA0000000`. The boot code then runs from step 1 above. EXPEVT's low half `0xCA02` is
none of the three values in step 7, so the boot code's model code in RAM keeps its old value.

Three routines set the word `0xFD8017DC` to 1 before restarting, so that the next boot skips
the setup:

| Routine | Sets the word | Then | Called from |
|---|---|---|---|
| `0x801504E0` | to 1, unless it is 2 | syscall `0x1187` | `PowerOff` (step 3), and the end of the multi-step routine at `0x8014EEA8` |
| `0x80365FC6` | to 1 | syscall `0x1187` | a confirmation screen in the setup code (`0x8035F4DC`) |
| `0x801DFE4A` | to 1, unless it is 2 | jumps to the OS entry `0xA0020008`, not the reset vector | the end of a low-power routine, syscall `0x11ED` (`0x801DFE90`) |

The multi-step routine at `0x8014EEA8` records its progress in bits 16–19 of the IL RAM word
`0xFD801D48` (values 1 to 5) and clears them when it is done. If the calculator is switched off
while those bits hold 1, 2 or 4, `PowerOff` restarts the OS through `0x801504E0` instead of
sleeping.

!!! warning "Unconfirmed"
    Read from the OS 3.60 code; none of these restarts has been run in the emulator. What
    the multi-step routine is, which confirmation screen calls `0x80365FC6`, and what leads to
    syscall `0x11ED` (reached through syscalls `0x1838` and `0x1837`) have not been traced.

## Power-on vs. cold start

The IL RAM word **`0xFD8017DC`** decides whether a boot runs the first-boot setup. It is
checked once, by `0x80365238`, near the end of the OS's start-up:

1. If the four bytes at flash offset `0x300` are all `0xFF` (blank), the setup is skipped
   (syscall `0x1197`, `0x801DE9CA`).
2. If the word is **1** and the model code `0xFD8018D4` is `0xCA02`, the setup is skipped.
3. Otherwise the word is set to **2** and the setup runs.
4. In every case the word is then set to **0**.

So the word is 1 only between a restart from the table above and the boot that follows. A
boot with zeroed RAM (the batteries were out) always finds 0 and runs the setup.

!!! info "Verified in the emulator"
    A cold boot of the 16 MB flash dump finds 0, sets the word to 2 and shows the setup; after
    the last screen the word is 0. Seeding the word with 1 instead reaches the MAIN MENU
    directly (after about 99 million instructions) and leaves it 0. A full 32 MB dump booted
    this way starts at that calculator's own MAIN MENU with its add-ins and settings
    (`CG50_WARM=1`). Probes: `TestFirstBootScreens` (`FIRSTBOOT_WARM=1` for the second case),
    `TestWarmBoot`.

## First-boot setup

The setup (`0x8035E1BE`) shows four screens, each a syscall called with argument 1:

| Screen | Syscall | Keys |
|---|---|---|
| Message Language | `0x1E0D` (`0x8035D234`) | F6 Next |
| Display Settings (backlight level) | `0x1E0A` (`0x8035CF58`) | F6 Next |
| Power Properties (auto power-off, backlight duration) | `0x1E05` (`0x8035C7C0`) | F6 Next |
| Battery Settings (alkaline or Ni-MH) | `0x1E07` (`0x8035CBFC`) | F1 SELECT, F1 Yes to the warning, then F6 Finish |

After the four screens, `0x80365238` calls `0x8035FCE8` and, depending on its result and on
`0x801504DA`, shows a note (`0x801F2630`). On the 16 MB dump the note says that the add-ins
deleted by Reset 1 are installed; EXE dismisses it and the MAIN MENU appears.

!!! info "Verified in the emulator"
    A cold boot of the 16 MB dump shows these screens in this order and reaches the MAIN MENU
    with exactly these keys. Probe: `TestFirstBootScreens`.

!!! warning "Unconfirmed"
    What `0x8035FCE8` and `0x801504DA` check has not been traced. `0x801504DA` looks for a
    64-byte record whose second half-word is `0x95FF`.

## In the emulator

- The boot path is the OS's own code. The emulator starts the CPU at `0x80000000`, the
  cached view of the same flash address as the real `0xA0000000` (`NewEmulator` in
  [`emu_go/emulator.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/emulator.go)).
  `TestGoldenBoot` in
  [`emu_go/golden_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/golden_test.go)
  compares the first 2 million instructions of a cold boot with the Python reference emulator.
- EXPEVT reads `0x0A02` after reset (the model code above) and afterwards whatever was last
  written or the code of the last exception: `ccn` in
  [`emu_go/mmu.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmu.go).
- `CG50_WARM=1` (in
  [`emu_go/main.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/main.go)) seeds `0xFD8017DC` = 1 before the boot (`warmFlagAddr` in
  [`emu_go/state.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/state.go)),
  so a flash image that already has its storage memory set up boots past the setup.
- `sleep` halts the emulated CPU until an interrupt request arrives; the clock-generator
  writes around it are stored but have no effect. Test: `TestSleepWakesOnRequest` in
  [`emu_go/rtc_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/rtc_test.go).
- Probes (`-tags probe`):
  [`emu_go/poweroff_probe_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/poweroff_probe_test.go)
  (`TestPowerOffTrace`, `TestAutoPowerOff`),
  [`emu_go/firstboot_probe_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/firstboot_probe_test.go)
  (`TestFirstBootScreens`),
  [`emu_go/warm_boot_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/warm_boot_test.go)
  (`TestWarmBoot`, needs the 32 MB dump).
- The emulator does not blank the screen when the OS switches the panel off; see
  [Display](../peripherals/display.md#switching-the-panel-off-and-on).
