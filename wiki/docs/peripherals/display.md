# Display (R61524 LCD controller)

The fx-CG50's screen is driven by a **Renesas R61524** LCD controller. The controller has its
own picture memory, called **GRAM** (graphics RAM), and the panel always shows what is in GRAM.
The CPU cannot address GRAM directly; it talks to the controller through one 16-bit port.

The OS does most of its drawing somewhere else: in **VRAM**, an ordinary buffer in DRAM. When a
picture is finished, the OS copies VRAM into GRAM with the DMA controller. This is called
**pushing** a frame. A few things, such as the text cursor, skip VRAM and are written straight
into GRAM.

## Sizes and coordinates

| | Width × height | Notes |
|---|---|---|
| Panel (GRAM) | 396 × 224 | The whole glass |
| OS drawing area (VRAM) | 384 × 216 | Panel columns 6–389, rows 0–215 |
| Frame | | 6 columns left, 6 right, 8 rows below the OS area |

- Pixels are **RGB565**, 16 bits each, stored big-endian.
- The OS's status bar is the top 24 rows of its area. Several OS drawing routines take a `y`
  measured below the status bar and add 24 themselves.
- GRAM's horizontal address runs the other way from the OS's `x`: OS pixel (x, y) is panel
  address **H = 389 − x, V = y**. The OS computes it as `H = 0x18B − (x + 6)`.
- The frame is painted in a single colour by `DrawFrame` (below). gint add-ins use the whole
  396 × 224 panel, frame included.

!!! info "Verified in the emulator"
    The emulator's panel model uses exactly this geometry. The OS's own pushes, its frame fill
    and its cursor land where the real calculator shows them, and the emulator's displayed
    picture matches the OS's VRAM pixel for pixel. Tests: `TestLCDWindowStream`,
    `TestLCDDMAFill`, `TestCursorBlinks`, `TestPresentOnLCDPush`.

## VRAM

| | |
|---|---|
| Address | `0xAC000000` (physical `0x0C000000`, the start of DRAM; uncached P2 view) |
| Format | 384 × 216 RGB565, big-endian |
| Row length | 768 bytes (384 pixels, no padding) |
| Size | 165,888 bytes (`0x28800`) |

The syscall `GetVRAMAddress` (`0x01E6`, at `0x8004F6A2`) simply returns `0xAC000000`.

!!! info "Verified in the emulator"
    The OS draws through the uncached address `0xAC000000`; the emulator only showed a picture
    once that address reached DRAM. Read with 768-byte rows the picture is exact; with 792-byte
    (396-pixel) rows it is skewed.

## Talking to the controller

| Address | Size | What |
|---|---|---|
| `0xB4000000` | 16 bit | The controller's only port: register index **or** register data |
| `0xA405013C` bit 4 | 8 bit (PFC port) | **RS**, the register-select line: 0 = index, 1 = data |

The controller sits on bus area 5, physical `0x14000000`; see the [memory map](../memory-map.md).
Every access is a 16-bit read or write at `0xB4000000` itself. Whether a write is an index
or data depends only on RS, which is a pin driven by the PFC (pin function controller).

To reach a register, the OS's helper at `0x8004E272`:

1. clears bit 4 of `0xA405013C` (byte read-modify-write),
2. writes the register number to `0xB4000000`,
3. sets bit 4 again,

with a `synco` between steps. After that, reads and writes of `0xB4000000` go to the selected
register, and RS stays at 1 until the next selection. So in normal operation bit 4 reads 1.

Register reads matter. The OS changes one bit of the entry-mode register by reading it,
changing the bit and writing it back, and it reads the same register to decide how to address
GRAM.

!!! info "Verified in the emulator"
    When a read of `0xB4000000` returned the last value written instead of the register,
    R003 came out as `0x0083` instead of `0x00A0`, the OS concluded that window addressing was
    off and took a different drawing path. Correct read-back fixed it. Tests:
    `TestLCDRegisterReadback`, and `TestLCDOracleTranscript` (Go and the Python reference
    emulator read the same values).

### The gate on bit 4

Every OS routine that touches the panel starts with two checks and returns without doing
anything if either fails:

- bit 4 of `0xA405013C` must be **1**;
- DMA channel 0 must be idle (`CHCR0` at `0xFE00802C`, bit 0 `DE` = 0).

!!! info "Verified in the emulator"
    If bit 4 reads 0 (for example after restoring a machine state that did not save the PFC),
    the OS silently stops pushing frames and the screen freezes, while everything else keeps
    running. Test: `TestLegacyStateDefaults`.

### Registers the OS uses

Register names are the R61524's.

| Register | Name | What the OS does with it |
|---|---|---|
| R003 | Entry mode | Set at boot; one bit (ORG) changed and tested later |
| R200 | GRAM address, horizontal | Set to 0 before a stream (window mode) |
| R201 | GRAM address, vertical | Set to 0 before a stream (window mode) |
| R202 | GRAM data | Select it, then each 16-bit write stores one pixel |
| R210 / R211 | Window horizontal start / end | Set before every stream |
| R212 / R213 | Window vertical start / end | Set before every stream |

Entry mode bits used:

| R003 bit | Name | Meaning |
|---|---|---|
| 7 | ORG | 1 = the address in R200/R201 is relative to the window |
| 5 | I/D1 | 1 = vertical address increments |
| 4 | I/D0 | 1 = horizontal address increments, 0 = decrements |
| 3 | AM | 0 = fill a row, then move to the next row |

After the OS's LCD initialisation (`0x8004DD3A`), **R003 = `0x00A0`**: ORG = 1, vertical
increment, horizontal **decrement**, row by row. The window is the whole panel,
H 0–395 × V 0–223. Because the horizontal address decrements, a stream starting at OS
`x = 0` (H = 389) walks left to right on the OS's screen.

!!! info "Verified in the emulator"
    These are the values the OS's own initialisation leaves behind when it runs in the emulator.
    A 3 × 2 block streamed with these settings fills the window starting at its top-right
    corner (highest H, lowest V). Test: `TestLCDWindowStream`.

### Setting a window

The routine at `0x8004E2AA` takes OS-style coordinates `(x1, x2, y1, y2)` and calls
`0x8004E440`, which reads R003 bit 7:

- **ORG = 1** (the normal case): R210 = `0x18B − x2`, R211 = `0x18B − x1`, R212 = `y1`,
  R213 = `y2`, then R200 = R201 = 0.
- **ORG = 0**: it only sets R200 = `0x18B − x1`, R201 = `y1`, an absolute address.

The routine at `0x8004E3E8` sets or clears ORG by read-modify-write of R003.

!!! warning "Unconfirmed"
    Read from the OS 3.60 code. Both `0x8004E3E8` and `0x8004E440` read the data port twice
    after selecting R003 and use the second value. Why (a dummy read the controller needs, or
    not) has not been measured.

## Pushing a frame

`Bdisp_PutDisp_DD` (syscall `0x025F`, at `0x8005552C`) copies all 216 rows of VRAM to the
panel. It is `Bdisp_PutDisp_DD_stripe` (syscall `0x0260`, at `0x80055532`) called for rows
0–215. The stripe routine:

1. checks the [gate](#the-gate-on-bit-4);
2. sets ORG = 1, sets the window to panel columns 6–389 and rows `y1`–`y2`, and selects R202;
3. clears bit 21 of `0xA4150030` (a CPG module-stop register);
4. programs DMA channel 0 and starts it;
5. waits until the transfer has ended, then stops the channel.

The DMA controller is at `0xFE008000`:

| Register | Address | Value for a full frame |
|---|---|---|
| SAR0 (source) | `0xFE008020` | `0x0C000000` + 768 × `y1` (VRAM, physical) |
| DAR0 (destination) | `0xFE008024` | `0x14000000` (the LCD port, physical) |
| TCR0 (count) | `0xFE008028` | `0x1440` = 5184 units of 32 bytes = one frame |
| CHCR0 (control) | `0xFE00802C` | `0x00101400`, then bit 0 (`DE`) set to start |
| DMAOR (all channels) | `0xFE008060` | 16-bit; enabled (bit 0) for the transfer, then 0 |

In `CHCR0 = 0x00101400`: the transfer size is 32-byte units, the source address increments,
the destination stays fixed, and transfers are started by software. No interrupt is requested
(bit 2 `IE` = 0); the OS polls `CHCR0` bit 1 (`TE`, transfer end) instead.

!!! info "Verified in the emulator"
    The emulator starts streaming VRAM into the panel at the moment `CHCR0` is written with
    `DE` = 1 and a destination of `0x14000000`. With that, every screen the OS draws appears,
    and nothing else is needed. Test: `TestPresentOnLCDPush`.

!!! warning "Unconfirmed"
    Read from the OS 3.60 code, not measured:

    - Step 3: which module bit 21 of `0xA4150030` stops has not been checked.
    - The wait loop also stops on `DMAOR` bit 2 (address error).
    - For a stripe that does not start at row 0, the OS sets DAR to `0x14000000` + 1536 × `y1`,
      not to `0x14000000`.

### When the screen changes

The panel changes only when the OS writes GRAM: at a push, or through one of the direct
writes below. While the OS is redrawing VRAM, the user still sees the last pushed frame.
An idle screen causes no pushes at all; there is no regular refresh from the CPU's side.

!!! info "Verified in the emulator"
    At the MAIN MENU, 3 million idle instructions cause no push. Moving the cursor causes
    pushes, and partway through the redraw VRAM differs from what the panel shows. When the
    redraw is done they agree again. Test: `TestPresentOnLCDPush`.

## The frame around the screen

`DrawFrame` (syscall `0x02A8`, at `0x800561EE`) paints the 6-column strips and the bottom
8 rows in one colour. It uses the same DMA channel with a **fixed** source: 32 bytes of the
colour are sent again and again to the port, filling each strip's window.

!!! info "Verified in the emulator"
    A fill with a fixed source must repeat the 32 bytes and must not read past them. A fill is
    not a frame push. Test: `TestLCDDMAFill`.

## The text cursor is drawn straight into GRAM

In Run-Matrix and other text inputs the blinking caret never touches VRAM and is not pushed
by DMA. The OS writes its pixels directly to the controller:

| Routine | Address | Syscall |
|---|---|---|
| `Cursor_SetFlashOn` | `0x800C3EB8` | `0x08C7` |
| `Cursor_SetFlashOff` | `0x800C3F36` | `0x08C8` |
| `Keyboard_CursorFlash` | `0x800C3F68` | `0x08CA` |
| blink tick | `0x800C3FA8` | `0x08CC` |
| draw the caret | `0x800C4006` | |
| erase the caret | `0x800C4266` | |

- The blink tick runs **twice a second**, from the RTC's periodic event (see
  [Timers](timers.md) and [Interrupts](interrupts.md)).
- To draw, the OS saves the VRAM pixels under the caret, then writes the caret through an LCD
  window. To erase, it reads each pixel back **from VRAM** and writes it to the panel, one by
  one (`0x800564CC`).

So a program that reads VRAM never sees the caret.

!!! info "Verified in the emulator"
    In Run-Matrix after typing `12`, the displayed picture changes about 4 times in 2 seconds, only
    inside the caret cell, with no push and no change to VRAM. Before the emulator modelled
    GRAM, the cursor never blinked. Test: `TestCursorBlinks`.

### Other direct writes

!!! warning "Unconfirmed"
    Read from the OS 3.60 code:

    - Syscall `0x025D` (`0x800554A6`) checks the same gate, sets a window and writes **8
      pixels** straight to R202, one per bit of a byte argument: `0xFFFF` for a set bit,
      `0x0000` for a clear one. It adds the 24-row status bar to `y`.
    - The bitmap routine below can write to the panel instead of VRAM. Its per-pixel panel
      writer `0x800557FA` sets a one-pixel window and writes R202.
    - GRAM is never read back by the OS. (The emulator returns 0 for a GRAM read and nothing
      breaks.)

## The OS's bitmap routine

The OS has one generic bitmap routine at **`0x80056900`** (syscall `0x02B0`). In a MAIN MENU
cursor move it accounts for about 75% of the instructions executed, at roughly 85
instructions per pixel.

It takes a descriptor at `r4` (big-endian fields) and a writer selector in `r5`:
`r5 = 1` writes VRAM (`0xAC000000 + y × 768 + x × 2`, through `0x8005575C`); `r5 = 2` writes
the panel directly.

| Offset | Size | Field |
|---|---|---|
| `+0x00` | 4 | `x` |
| `+0x04` | 4 | `y` (the wrappers `0x80055EA0` and `0x80055EEC` add 24 for the status bar) |
| `+0x08` | 4 | `xo`, start column inside the bitmap |
| `+0x0C` | 4 | `yo`, start row inside the bitmap |
| `+0x10` | 4 | `w`, bitmap width |
| `+0x14` | 4 | `h`, bitmap height |
| `+0x18` | 1 | Format: 1 = 4 bpp with palette, 2 = RGB565, 3 = 1 bpp |
| `+0x1C` | 4 | Pointer to the pixels |
| `+0x20`, `+0x21` | 1 + 1 | Foreground and background palette index (1 bpp) |
| `+0x22` | 1 | Transparent palette index, `0xFF` = none |
| `+0x24` | 1 | Pixel operation: 2 = invert, 3 = dither (every other pixel replaced) |
| `+0x25` | 1 | Combine with VRAM: 1 = replace, 2 = OR, 3 = AND, 4 = XOR |
| `+0x28` | 4 | Blend level; > 0 hands off to `0x8004EB54` |

- The palette is 16 RGB565 words at `0x80399D30`.
- A row of a 4 bpp bitmap is padded to an even number of pixels, a row of a 1 bpp bitmap to
  a multiple of 8.
- `xo` and `yo` are reset to 0 unless `0 ≤ xo < w` and `0 ≤ yo < h`.
- Nothing is drawn if `x` or `y` lies outside 0–383 / 0–215 or the size is not positive. The
  right and bottom edges are clipped at 384 and 216.
- A pixel whose colour equals the transparent colour is skipped.

In ordinary use the OS was only seen to call it with operation 1, combine mode 1 and blend 0,
in all three formats, with and without a transparent colour.

!!! info "Verified in the emulator"
    The emulator can replace this routine with its own code written from this description. Over
    a session of menu moves, launching Run-Matrix, typing, MENU and EXIT, every call was run
    both ways from the same state: VRAM and the registers the caller sees came out identical.
    Test: `TestHLEBlitMatchesInterpreter`.

## How fast the screen updates

There is no frame-rate limit in the display path itself: the OS pushes when it has something
new. What caps the speed of a held key, such as scrolling the MAIN MENU, is the
[keyboard](keyboard.md):

- While a key is held, the key-scan unit scans every **30.3 ms (33 Hz)**; when no key is
  held it does not scan at all.
- The OS repeats a held key after 20 scans (about 0.6 s), then once per scan.

So a held arrow can move the cursor at most 33 times a second, and each move ends in a push.

!!! success "Verified on hardware"
    The scan rate was measured by a probe add-in: 132 scans in 4 seconds with a key held,
    none in 2 seconds idle.

!!! info "Verified in the emulator"
    The repeat rule (20 scans, then one per scan) is the OS's own logic and runs unchanged in
    the emulator.

A real calculator does not reach this cap when scrolling the menu, because each cursor move
costs roughly 2 million instructions (1.3–2.5 million in different measurements), mostly in
the bitmap routine above. See [Timers](timers.md#how-fast-the-calculator-runs) for how that
compares with the calculator's speed.

!!! warning "Unconfirmed"
    Timed by hand, not by a probe: a real calculator makes about 11–12 menu moves in a 1.2 s
    hold, against about 21 at the 33 Hz cap. The emulator gives the same 11–12 when it runs
    about 45 million instructions per second, so that is roughly how fast the calculator runs
    OS code.

## gint add-ins

gint add-ins drive the controller themselves, use the full 396 × 224 panel and send frames
by DMA. Unlike the OS they set `CHCR.IE` and sleep until the DMA controller's
**transfer-end interrupt** wakes them (INTEVT `0x800` + `0x20` × channel for channels 0–3).

A channel starts only when `CHCR` is written with `DE` = 1 while `TE` = 0. gint's switch back
from the OS restores the `CHCR` values it saved, with both bits set, and that must not start
the last transfer again.

!!! info "Verified in the emulator"
    The interrupt is raised when `IE` is set and not otherwise. When restoring a `CHCR` with
    `TE` set restarted the transfer, gint add-ins showed smeared, half-drawn screens.
    Test: `TestDMACInterrupt`.

## Not yet known

!!! warning "Unconfirmed"
    How the OS switches the panel off at auto power-off has not been traced. In the emulator
    the screen does not go blank then (only a black band appears at the bottom), so part of
    what the OS does there is not modelled yet.

## In the emulator

- Panel model: [`emu_go/lcd.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/lcd.go)
  (registers with read-back, window addressing, GRAM, DMA streaming). The Python reference
  emulator models the part the CPU can see (index, register read-back) in `LCD` in
  [`emu/mmio.py`](https://github.com/hexbinoct/casio-cg50/blob/main/emu/mmio.py).
- DMA controller: `dmac` in
  [`emu_go/mmio.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmio.go).
- Native replacement of the bitmap routine:
  [`emu_go/hle.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/hle.go)
  (off by default; the Android app turns it on).
- Tests: [`emu_go/lcd_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/lcd_test.go),
  [`emu_go/present_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/present_test.go),
  [`emu_go/hle_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/hle_test.go),
  `TestDMACInterrupt` in
  [`emu_go/gtimer_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/gtimer_test.go),
  `TestLegacyStateDefaults` in
  [`emu_go/state_test.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/state_test.go).
  The Go and Python register models are kept in step by
  [`emu/lcd_selftest.py`](https://github.com/hexbinoct/casio-cg50/blob/main/emu/lcd_selftest.py),
  which writes the transcript `emu/lcd_golden.txt` that `TestLCDOracleTranscript` replays.
