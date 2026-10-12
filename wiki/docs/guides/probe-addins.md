# Writing probe add-ins

A probe add-in is a small program that runs on a real fx-CG50, reads some hardware
registers or memory, and saves what it saw. The OS code tells you what the OS *does* with
the hardware; only a probe tells you how the hardware *responds*. Results from probes are
the strongest evidence on this site. The probes so far, their raw results and the pages
they back are listed in the [probe table](../about/verification.md#the-probes).

This page covers building a probe, getting its results off the calculator, trying it in the
emulator first, and what a probe must never do.

!!! danger "Read the safety rules before you run anything"
    One probe on this project reset a calculator to its first-boot setup. The rules that came
    out of it are in [What is safe](#what-is-safe) below.

## The toolchain: fxSDK and gint in Docker

Probes are built with the [fxSDK](https://gitea.planet-casio.com/Lephenixnoir/fxsdk) and the
[gint](https://gitea.planet-casio.com/Lephenixnoir/gint) library, the open SDK for Casio's
SuperH calculators. gint's own tool `fxlink` talks to the calculator over USB.

The repository builds them inside Docker, from
[`tools/fxsdk-docker/Dockerfile`](https://github.com/hexbinoct/casio-cg50/blob/main/tools/fxsdk-docker/Dockerfile).
It starts from Debian 12 and installs everything with GiteaPC, the forge's package manager:
the SuperH cross-compiler `sh-elf-gcc`, the fxSDK, OpenLibm, fxlibc and gint. Building the
compiler takes 20–40 minutes, once; after that the image is reused.

```sh
# once: build the image (from the repository root)
docker build -t fxsdk:latest tools/fxsdk-docker

# every build: mount the add-in's folder as /work and run fxsdk in it
docker run --rm -v "<path to the add-in folder>:/work" fxsdk:latest fxsdk build-cg
```

`fxsdk build-cg` builds for the fx-CG series and leaves the `.g3a` file in the add-in's
folder. On Windows, give Docker the folder in its `/F/...` form, as in the comment at the top
of the Dockerfile.

### A probe's project

The three probes in `tools/` (`keyprobe/`, `timerprobe/`, `tickprobe/`) have the same
layout. Copying one is the quickest start:

| File | What it is |
|---|---|
| `CMakeLists.txt` | The fxSDK project: one source file, linked with gint, `generate_g3a(...)` names the add-in and its icons |
| `src/main.c` | The probe |
| `make_icons.py` | Draws the two 92×64 menu icons (unselected and selected) into `assets-cg/` with Pillow |
| `assets-cg/` | The icons |

The add-in's name in the MAIN MENU comes from the `NAME` in `generate_g3a`, the file name
from `OUTPUT`.

### Putting it on the calculator

Connect the calculator by USB and pick the mass-storage option on its screen. The storage
memory appears as a drive on the computer. Copy the `.g3a` to it and eject the drive; the
add-in now has an icon in the MAIN MENU.

!!! warning "Unconfirmed"
    The OS lists add-ins of up to 2 MB: the routine that builds the list (`0x8002D1A4`)
    skips larger files. Read from OS 3.60 code; probes are far smaller.

## Measuring in the OS's world

A gint add-in takes over the hardware while it runs. gint installs its own interrupt
handlers and programs the timers, the keyboard unit and the DMA controller for itself (see
[Interrupts](../peripherals/interrupts.md#gint)). Registers read from gint's world show
gint's settings, not the OS's.

To see the hardware as the OS left it, run the measurement through
`gint_world_switch(GINT_CALL(fn))` (in `<gint/gint.h>`). gint hands the hardware back to the
OS, calls `fn`, then takes it over again. Inside `fn` the OS's configuration and interrupt
handlers are live. All three probes in `tools/` measure this way.

Inside `fn`:

- read and write registers through plain `volatile` pointers;
- call no gint function (no drawing, no keyboard, no files), only after the switch back;
- put back anything you change before returning. The keyprobe masks the keyboard interrupt
  (INTC `IMR5`) for its counting window and unmasks it (`IMCR5`) at the end.

A minimal shape:

```c
#include <gint/gint.h>
#include <stdint.h>

#define R32(a) (*(volatile uint32_t *)(a))
static uint32_t frqcr;

static void measure(void) {          /* runs in the OS's world */
    frqcr = R32(0xA4150000);         /* read only */
}

int main(void) {
    gint_world_switch(GINT_CALL(measure));
    /* back in gint's world: format frqcr, save it, show it */
    return 1;
}
```

Time inside a probe is best measured against something with a known rate. The probes use
the RTC's seconds register `0xA413FEC2` (wait for it to change, then count until it changes
again) and the 32.768 kHz counter at `0xA44D00D8`, which runs in the OS's world (see
[Timers](../peripherals/timers.md)).

## Getting the results off the calculator

There are three ways, from the most to the least convenient.

### A file in storage memory

Write the results to a text file with gint's `BFile_*` calls, then read the file over USB
mass storage. This needs no extra software on the computer and works the same on every
operating system. The probes in `tools/` do this:

1. `BFile_Remove(path)` deletes an old copy (an error is fine).
2. `BFile_Create(path, BFile_File, &size)` creates the file at its final size.
3. `BFile_Open(path, BFile_WriteOnly)`, `BFile_Write(fd, buf, size)`, `BFile_Close(fd)`.

The path is a 16-bit string such as `u"\\\\fls0\\KEYPROBE.TXT"`. `BFile_Write` wants an even
size, so the probes pad the text with a newline when its length is odd. Call these only in
gint's world, after the switch back.

Files go through the OS's own file system, the same way as a copy over USB. A probe never
needs to write the flash itself.

### A named fxlink transfer over USB

`fxlink` on the computer can receive data while the add-in runs. The add-in opens gint's
USB interface, then sends each block as one named transfer: a header with an application
name, a type name and the size, then the data. The flash dumper
[`tools/flash_dump/dump.c`](https://github.com/hexbinoct/casio-cg50/blob/main/tools/flash_dump/dump.c)
does exactly this:

```c
usb_interface_t const *intf[] = { &usb_ff_bulk, NULL };
usb_open(intf, GINT_CALL_NULL);
usb_open_wait();

int pipe = usb_ff_bulk_output();
usb_fxlink_header_t header;
usb_fxlink_fill_header(&header, "myprobe", "result", size);
usb_write_sync(pipe, &header, sizeof header, false);
usb_write_sync(pipe, data, size, false);
usb_commit_sync(pipe);

usb_close();
```

On the computer, start `fxlink -i -v -w` (interactive, verbose, wait for the calculator)
before launching the add-in. It saves each named transfer to its own file, named after the
application, the type and the time, such as `fxlink-aluprobe-probe2-2026.06.05-06h01-1.bin`
in [`os/devic_probes/aluprobe_2/`](https://github.com/hexbinoct/casio-cg50/tree/main/os/devic_probes/aluprobe_2).

!!! warning "Unconfirmed"
    **Use a named transfer on macOS, not the text path.** gint's text output (`usb_fxlink_text`), shown by `fxlink` in its interactive mode,
    failed on a Mac with libusb reporting "data overrun". The same results sent as a named
    transfer arrived intact. Observed once, with the gint and fxlink of June 2026.

The USB API's names have changed between gint versions; if the build complains, check
`<gint/usb.h>` and `<gint/usb-ff-bulk.h>` in your gint.

### On the screen

As a fallback, the probes also page their text on the screen, 14 lines at a time, a key
press per page. It always works, and a photo of each page is a record.

## The probes so far

What each probe in the repository measured. Their raw results are in
[`os/devic_probes/`](https://github.com/hexbinoct/casio-cg50/tree/main/os/devic_probes); the
[probe table](../about/verification.md#the-probes) lists which facts each one backs.

| Probe | Source | What it measured | Used on |
|---|---|---|---|
| **keyprobe** | [`tools/keyprobe/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/keyprobe) | With RIGHT held: a snapshot of the keyboard unit's registers, TMU state and FRQCR; then, with the keyboard interrupt masked, the key unit's scan-complete and key-detect flags counted for 4 RTC seconds (2 seconds with no key), against the OS tick counter and TMU2 | [Keyboard](../peripherals/keyboard.md), [Timers](../peripherals/timers.md), [Display](../peripherals/display.md) |
| **timerprobe** | [`tools/timerprobe/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/timerprobe) | Every word of the timer blocks (TMU, ETMU, `0xA44A0000`, `0xA44C0000`, `0xA44D0000`) and the RTC, sampled continuously for two RTC seconds: which words changed, how often, first and last value. Also a diff of all of IL RAM between snapshots about 3 seconds apart | [Timers](../peripherals/timers.md) |
| **tickprobe** | [`tools/tickprobe/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/tickprobe) | The battery A/D unit at `0xA4610000` and its interrupt `0x560`: whether a conversion finishes while the CPU runs (v1–v3) | [Battery ADC](../peripherals/battery-adc.md) |
| **flash_dump** | [`tools/flash_dump/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/flash_dump) | Not a measurement: streams the whole 32 MB flash, the DRAM and the IL RAM to `fxlink` as named transfers. Read-only | [Memory map](../memory-map.md#flash) |
| **aluprobe**, **alusweep**, **shtest** | Not in the repository | The BCD ALU's commands and its carry/borrow latch; 202 integer arithmetic cases on the CPU | [BCD ALU](../peripherals/bcd-alu.md), [CPU](../cpu.md#integer-arithmetic-matches-the-manual) |

The tickprobe also has a v4, a copy of the OS's idle sleep. That is the probe that reset a
calculator. It is compiled out unless `TICKPROBE_SLEEP_TEST` is defined, and the default
build only reports register state.

The flash dumps and RAM dumps are not published: the flash is Casio's firmware, and the RAM
holds copies of OS code and data. Results that are plain measurements are.

## What is safe

!!! danger "Never put the CPU to sleep, never touch the clock generator"
    The tickprobe's v4 replayed the OS's idle routine from inside a world switch: it wrote
    0 to the clock generator register `0xA4150020` and executed `sleep`. The calculator
    reset to its [first-boot setup](../os/boot.md#first-boot-setup). Files in storage memory
    survived; everything in RAM was lost. The OS's own sleep paths do more preparation than
    that copy did ([CPU: `sleep`](../cpu.md#sleep)).

    - **No `sleep` instruction**, and no copy of either OS sleep path.
    - **No write to the clock generator (CPG, `0xA4150000`–`0xA41500FF`)**: not FRQCR, not
      PLL or FLL settings, not `0xA4150020`, not the module-stop registers. The keyprobe
      read FRQCR without trouble; reading other CPG registers is best left until it is
      clearly needed.
    - **No write to the flash window** (`0x80000000`–`0x81FFFFFF`, `0xA0000000`–`0xA1FFFFFF`).
      The flash chip takes commands as writes to its own addresses, so a stray write can
      start an erase or a program sequence. Save files with `BFile_*` instead (above).

!!! danger "Don't run code that can fault with SR.BL = 1"
    With exceptions blocked, a TLB miss resets the CPU instead of raising an exception
    ([CPU](../cpu.md#an-mmu-exception-with-srbl-1-resets-the-cpu)). Don't set SR.BL in a
    probe. If code runs with BL set anyway (an interrupt handler), it must only touch memory
    that is always mapped: P1/P2 addresses, IL RAM, on-chip registers. An add-in's own code
    and data at `0x00300000`/`0x08100000` are mapped through the TLB and can miss.

What has been safe on every probe so far:

- **Reading** registers and memory: the keyboard, timer, RTC, interrupt controller, battery
  A/D and BCD ALU registers, FRQCR, all of IL RAM, the flash and the DRAM.
- **Writes the probes made and undid**: masking and unmasking one interrupt source at the
  INTC, clearing the keyboard unit's status flags by writing them back (they are
  write-one-to-clear; [Keyboard](../peripherals/keyboard.md#status-flags-0x14)), and the
  BCD ALU's operand and command registers.

Reads that are **not** harmless:

- **Past the end of the flash.** The flash is 32 MB. Reading beyond it raises a bus error
  and the add-in stops with an error screen: harmless to the calculator, but the transfer
  in progress is lost.
- **Through an address with no TLB entry** in P0 (below `0x80000000`), outside your own
  add-in's code and data (see [Memory map](../memory-map.md#add-in-mappings)). Read physical
  memory through P1 (`0x80000000` + address) or P2 (`0xA0000000` + address) instead.
- **FIFO data registers** pop what they return. On the SH-4A's serial port (SCIF, at
  `0xA4410000` on this calculator, see
  [Interrupts](../peripherals/interrupts.md#usb-and-the-serial-port)), reading the receive
  data register takes a byte out of the queue. Don't sweep-read a whole peripheral block
  you don't know.

!!! warning "Unconfirmed"
    The SCIF read side effect is the SH-4A's documented behaviour; no probe has read that
    unit. What the OS does on a TLB miss outside an add-in's pages has not been traced. Other units may have registers that clear on read; when unsure, read one known
    register, not a block.

Two habits that help:

- Write the safety notes into the probe's source, as the tickprobe does, so a later build
  can't quietly bring a dangerous test back.
- Run the probe in the emulator first (next section). The emulator halts where the real CPU
  would reset, which is how a probe that could reset a calculator was caught
  ([CPU](../cpu.md#an-mmu-exception-with-srbl-1-resets-the-cpu)).

## In the emulator

The emulator runs `.g3a` add-ins, gint ones included, so a probe can be tried there first.
What it reads in the emulator is only the emulator's model, of course; the point is to catch
crashes, hangs and wrong addresses before the real run.

- **Probe tests.** Go tests built with `-tags probe` drive the emulator through a session.
  They need the 32 MB flash dump and its save-state in `os/flash_dump/` and skip without
  them. `TestGraph` (`emu_go/graph_probe_test.go`) works for any add-in: it installs the
  `.g3a` named by `GRAPH_G3A`, launches it, plays the keys in `GRAPH_KEYS` (taps as
  `row-col`, `wait:N` for N × 100 M instructions, `shot` for a screenshot) and saves the
  add-in's frames as `cg50_graph_NN.png` in the temporary directory:

    ```sh
    go -C emu_go test -tags probe -run TestGraph -count=1 -v .
    ```

    `TestRamchk` (`emu_go/ramchk_probe_test.go`) is a fuller example: it plays the same
    session as an on-device check and compares the add-in's verdict with what Go reads in
    the emulated RAM.
- **Installing.** `Emulator.InstallAddin` (`emu_go/install.go`) has the emulated OS write the
  `.g3a` to `\\fls0\` with its own file calls, as a USB copy would, then rebuilds the MAIN
  MENU. `TestInstallAddin` and `TestInstallAddinRejects` (`emu_go/install_test.go`) cover it.
  The desktop app CG50 Studio (drop the `.g3a` on its window, or "Install add-in") and the
  Android app ("Install add-in…") use the same call.
- **Trying a probe by hand:** CG50 Studio (`app/` in the repository) runs your flash dump with
  a clickable keypad, save states and screenshots. Add-ins already on the calculator are
  there too: they are in the flash dump. The command line's plain web UI
  (`go -C emu_go run ./cmd/cg50emu 0 30000 web`) also works.

Where the emulator and the calculator differ in ways that matter to probes:

- **gint timer delays are wrong.** The emulator's PLL control register reads without the FLL
  bit, so gint's `clock_freq()` computes a peripheral clock of about 65 kHz instead of
  29.49 MHz. Delays given in microseconds come out wrong.
- **`F…` (DSP) opcodes are skipped**, so a probe using DSP instructions computes nonsense
  without an error ([CPU](../cpu.md#in-the-emulator)).
- There is no instruction cache, and the instruction TLB's address arrays
  (`0xF2000000`/`0xF3000000`) are not modelled: code that manages them only really runs on
  the calculator.
- Peripherals the emulator models only roughly: [Keyboard](../peripherals/keyboard.md#in-the-emulator),
  [Timers](../peripherals/timers.md#in-the-emulator),
  [Battery ADC](../peripherals/battery-adc.md#in-the-emulator). Each page's "In the emulator"
  section lists the differences.
