# Interrupts

An **interrupt** is a request from a peripheral (a timer ran out, a key was pressed) that
makes the CPU stop what it is doing, run a handler, and then carry on. On the fx-CG50 three
parts are involved:

1. The **peripheral** raises a request, identified by a code called **INTEVT**.
2. The **interrupt controller** (INTC, `0xA4080000`) decides whether that request may go
   through, and at which priority level.
3. The **CPU** accepts it when its status register allows, and jumps to the OS's
   **dispatcher**, which looks the code up in a table and calls the handler.

## How the CPU accepts an interrupt

The CPU accepts a request when **SR.BL = 0** (exceptions not blocked) and the request's
level is **higher than SR.IMASK** (the 4-bit interrupt mask in SR, bits 4–7). Accepting it:

| Step | Effect |
|---|---|
| Save state | SSR ← SR, SPC ← PC, SGR ← R15 |
| Record the source | INTEVT (`0xFF000028`) ← the request's code |
| Enter privileged mode | SR.MD = 1, SR.RB = 1 (register bank 1), SR.BL = 1 |
| Jump | PC ← **VBR + `0x600`** |

SR.IMASK is left alone, unless **CPUOPM.INTMU** is set; see [CPU](../cpu.md#cpuopmintmu-0xff2f0000-bit-3).
The OS never sets INTMU. Its dispatcher sets IMASK itself (below).

A `sleep` instruction halts the CPU until a request arrives. A request wakes the CPU even
when SR.BL = 1; it is then accepted once BL is cleared. The OS idles exactly this way: it
sets BL, sleeps, and clears BL after waking.

!!! info "Verified in the emulator"
    Every interrupt the OS and gint add-ins use goes through this path. Tests:
    `TestSleepWakesOnRequest` (a request ends `sleep` with BL = 1 and is taken once BL
    clears), `TestCPUOPMIntmuSetsIMASK` (SSR keeps the interrupted IMASK).

!!! warning "Unconfirmed"
    When several requests are pending, the emulator takes the highest level first, and
    among equal levels the highest INTEVT. The real chip's order among equal levels has not
    been measured.

## The interrupt controller (`0xA4080000`)

The INTC has a 4-bit **priority field** and a **mask bit** for each source.

| Address | Registers | Access | Meaning |
|---|---|---|---|
| `0xA4080000` + 4·n | IPRA … IPRL (n = 0–11) | 16-bit | Four 4-bit priority fields: bits 15–12, 11–8, 7–4, 3–0 |
| `0xA4080080` + 4·n | IMR0 … IMR12 | 8-bit | Mask bits. Writing 1 **masks** that source; writing 0 changes nothing |
| `0xA40800C0` + 4·n | IMCR0 … IMCR12 (also called MSKCLR) | 8-bit | Writing 1 **unmasks** that source |

A source is requested only while its **priority field is non-zero** and its **mask bit is
clear**. The priority field is also the level the CPU compares with SR.IMASK, so 0 means
"disabled" and 15 is the highest.

The OS changes one field at a time with read-modify-write, so the priority registers must
read back what was written. For example, the keyboard's enable helper (`0x801DEDDA`) sets
IPRF bits 15–12 to 13 and then writes `0x80` to IMCR5; the disable helper (`0x801DEDD2`)
writes `0x80` to IMR5 and clears the same IPRF field.

!!! info "Verified in the emulator"
    gint add-ins rely on this gate. gint zeroes every priority field and masks everything
    at startup, then enables only its own timers and DMA. Before the emulator modelled the
    gate, the OS's battery-ADC interrupt kept arriving in gint's empty vector slot and the
    add-in died with "illegal instruction at `0xE5200022`". Test: `TestINTCGate`.

### Sources and their fields

| INTEVT | Source | Priority field | Mask bit | Used by |
|---|---|---|---|---|
| `0x400`, `0x420`, `0x440` | TMU0, TMU1, TMU2 | IPRA 15–12, 11–8, 7–4 | IMR4 bits 4, 5, 6 | gint |
| `0x560` | [Battery A/D converter](battery-adc.md) (`0xA4610000`) | IPRB 15–12 (OS: 12) | IMR4 bit 3 | OS |
| `0x800`–`0x860` | DMAC channels 0–3, transfer end | IPRE 15–12 | IMR1 bits 0–3 | gint |
| `0x900` | ETMU3 | IPRE 7–4 | IMR2 bit 0 | |
| `0x9E0` | ETMU0 | IPRJ 15–12 | IMR6 bit 3 | |
| `0xAA0` | RTC periodic interrupt | IPRK 15–12 (OS: 8) | IMR10 bit 1 | OS |
| `0xB80`, `0xBA0` | DMAC channels 4, 5, transfer end | IPRF 11–8 | IMR5 bits 4, 5 | gint |
| `0xBE0` | [KEYSC](keyboard.md), the key-scan unit | IPRF 15–12 (OS: 13) | IMR5 bit 7 | OS |
| `0xC20`, `0xC40` | ETMU1, ETMU2 | IPRG 11–8, 7–4 | IMR5 bits 1, 2 | |
| `0xD00` | ETMU4 | IPRI 15–12 | IMR6 bit 4 | |
| `0xFA0` | ETMU5 | IPRL 15–12 | IMR8 bit 1 | gint (key scanning) |

The timers are described on [Timers](timers.md); the DMAC moves frames to the
[display](display.md).

!!! info "Verified in the emulator"
    The OS's three sources use the fields its own code writes: `0x560` (handler helpers
    `0x801DE51E`/`0x801DE532`: IMR4 bit 3, IPRB ← 12), `0xBE0` (the helpers above) and
    `0xAA0` (IPRK = `0x8000` and IMCR10 = `0x03` written during a cold boot, probe
    `TestDumpINTC`). The TMU, ETMU and DMAC fields come from gint's INTC driver; the gint
    add-ins DASM and Upsilon run in the emulator with them. Tests: `TestINTCGate`,
    `TestETMUChannels`, `TestTMUChannel`, `TestDMACInterrupt`.

!!! warning "Unconfirmed"
    The OS's own ETMU handlers touch IPRJ (ETMU0), IPRG (ETMU1, ETMU2), IPRE (ETMU3) and
    IPRI (ETMU4), in the same field positions as gint's table. The ETMU mask bits come only
    from gint.

## Requests are gated again when accepted

The INTC's decision is not made once, when a peripheral raises its request. It holds at the
moment the CPU accepts:

- If the source's priority field is **0** by then, the request is **dropped**.
- If the source is **masked** by then, the request **waits**. It is neither accepted nor
  allowed to wake a sleeping CPU, and goes through once the source is unmasked.

This matters for gint. On every "world switch" back to the OS (for example around each file
read), gint restores the OS's INTC settings, which give ETMU5 priority 0. If ETMU5's
128 Hz key-scan tick was already pending at that moment and still got through, the OS would
look up `0xFA0` in its table, where there is no handler (see below), and jump to
`0xF0F0F0F0`.

!!! info "Verified in the emulator"
    The DASM add-in crashed this way after thousands of file reads while the emulator only
    checked the gate when a request was raised. Checking again at acceptance fixed it. Test:
    `TestINTCGateAtAcceptance` (fails on the old behaviour).

## The OS 3.60 dispatcher

OS 3.60 sets **VBR = `0x80020F00`** (the `ldc …,vbr` instructions at `0x8002039E`,
`0x800208AE` and `0x801DFEC2` all load this value). Its three vectors all end in the same
lookup:

| Vector | Address | Event register |
|---|---|---|
| VBR + `0x100`: general exceptions | `0x80021000` | EXPEVT (`0xFF000024`) |
| VBR + `0x400`: TLB miss | `0x80021300` | EXPEVT |
| VBR + `0x600`: interrupts | `0x80021500` | INTEVT (`0xFF000028`) |

So exceptions and interrupts share **one table**, indexed by their event code. For an
interrupt, the code at `0x80021500`:

1. Checks a clock-controller register (`0xA4150020`). If bit 5 is set it returns at once
   with `rte`; if bit 7 is set it first rewrites `0xA4150024`, `0xA4150020` and
   `0xFEC10044`.
2. Pushes SSR, SPC, PR and the interrupted code's R0–R7 (44 bytes) on the **interrupted
   code's own stack**. There is no separate interrupt stack.
3. Reads INTEVT and computes `slot = (INTEVT − 0x40) / 0x20`.
4. Loads the handler from `0xFD8010C8 + 4·slot` and a priority byte from
   `0xFD8012C8 + slot`.
5. Builds a new SR: the current SR with BL, RB and IMASK cleared, OR the priority byte. The
   byte is already in IMASK position, so `0xF0` means IMASK = 15.
6. Sets PR = `0x80021020` and "returns" (`rte`) into the handler.

The handler is an ordinary function. It runs in register bank 0, with BL clear and the
IMASK from the table. When it returns, `0x80021020` blocks exceptions again, pops what step 2
pushed, and `rte` resumes the interrupted code.

Bit 7 of `0xA4150020` belongs to the OS's deeper idle routine `0x80020880` (see
[Timers](timers.md#the-2-hz-wake-up)). That routine sets bits 7 and 0 of `0xA4150020` and
changes `0xA4150024` and `0xFEC10044` before its `sleep`, then undoes all three after waking.
The undo is exactly what step 1 does: `0xA4150024` gets bit 14 set and bit 1 cleared,
`0xA4150020` = 1, and `0xFEC10044` gets `0x0A00` in place of its bits 15–8. So an interrupt
that finds bit 7 still set restores the normal clock and bus settings before any handler runs.

!!! warning "Unconfirmed"
    On the SH7724, `0xA4150020` is STBCR and bit 7 is STBY: with it set, `sleep` enters
    *software standby*, where the clocks stop, instead of plain sleep. That fits, but the
    SH7305's bit has not been checked. What bit 5 means is not known; the deeper idle routine
    clears it before sleeping.

### The tables in IL RAM

At start-up the routine at `0x802EEB2C` copies 128 handler words from flash `0x80028A18`
to **`0xFD8010C8`**, and 128 priority bytes from `0x80028BF4` to **`0xFD8012C8`** (both in
the on-chip IL RAM, see [Memory map](../memory-map.md#on-chip-memories-p4)). No other OS
code refers to the tables' addresses, and in a real calculator they still match the flash
copy exactly (below).

- The flash table has only **119 real entries** (codes `0x040`–`0xF00`). The copy of 128
  words runs on into the priority bytes, so the slots for codes `0xF20`–`0x1020` hold
  `0xF0F0F0F0`. One of them is **ETMU5 (`0xFA0`)**: an ETMU5 interrupt taken through the
  OS's VBR jumps to `0xF0F0F0F0`.
- **Every priority byte of the 119 entries is `0xF0`.** Every OS handler therefore runs with
  IMASK = 15: OS interrupt handlers never nest.

!!! success "Verified on hardware"
    In a dump of a real calculator's IL RAM, all 128 handler words at `0xFD8010C8` and all
    128 priority bytes at `0xFD8012C8` are identical to the flash source. The word where
    the OS saves VBR (`0xFD8024A0`, written at `0x800208AA`) holds `0x80020F00`.

!!! info "Verified in the emulator"
    The emulator runs this dispatcher for every interrupt. The keyboard and the RTC only
    work through it: a key tap reaches the keyboard handler (`TestKeyscMenuTap`) and the
    Run-Matrix cursor blinks at 2 Hz (`TestRTCPeriodicInRunMatrix`). The `0xF0F0F0F0` slot
    for `0xFA0` is the address the DASM crash above jumped to.

## The OS's handlers

Interrupt codes with their own handler in the OS 3.60 table:

| INTEVT | Source | OS handler | What it does |
|---|---|---|---|
| `0x1C0` | NMI (SH-4A non-maskable interrupt code) | `0x801DED64` | Returns at once (`rts`) |
| `0x560` | Battery A/D converter | `0x801DED94` | Masks its source, posts an event, clears the end flags (bits 14 and 15 of `0xA4610088`/`0xA461008A`), unmasks |
| `0x620` | [Pin IRQ1](#external-pins-irq1-and-irq2), the "USB" wake-up | `0x801DEEBE` | Masks IRQ1, flips its trigger level, posts an event if the pin went high, clears its request |
| `0x640` | [Pin IRQ2](#external-pins-irq1-and-irq2), the "3PIN" wake-up | `0x801DEF3A` | Masks IRQ2, posts an event, clears its request |
| `0x900`, `0xC20`, `0xC40`, `0xD00` | ETMU3, ETMU1, ETMU2, ETMU4 | `0x802D8AC4` | One handler; reads INTEVT to tell the timers apart |
| `0x9E0` | ETMU0 | `0x802D8A18` | Uses ETMU0 (`0xA44D0030`) and IPRJ |
| `0xA20` | [USB controller](#usb-and-the-serial-port) (`0xA4D80000`) | `0x803741C0` | Jumps to the USB driver at `0x80744D68` |
| `0xAA0` | RTC periodic interrupt | `0x801DFC6C` | Acknowledges RCR2 (`0xA413FEDE`); this 2 Hz wake-up drives the cursor blink |
| `0xBE0` | KEYSC | `0x801DEDCC` | Jumps to the keyboard handler `0x801E4C00` |
| `0xC00` | [Serial port](#usb-and-the-serial-port) (SCIF, `0xA4410000`) | `0x803196D2` | Reads the status registers, then runs the receive, transmit or error routine |
| `0xF00` | Unknown | `0x802D8A18` | Same handler as ETMU0. It never reads INTEVT and touches only ETMU0, IPRJ and the ETMU5 counter, so it gives no clue to the source |

Every other slot from `0x040` to `0xF00` points at a common error routine (`0x8002C9A2`,
or a variant passing an error kind). The low slots are exception codes: `0x040`–`0x080`
(TLB miss on read, on write, initial page write) go to `0x8002C918`, the handler that maps
add-in code pages on demand (see
[Memory map](../memory-map.md#add-in-mappings)). The OS has **no handler** for TMU0–2 or
any DMAC channel.

!!! success "Verified on hardware"
    The handler addresses are those in the real calculator's IL RAM dump.

!!! info "Verified in the emulator"
    `0x560`, `0xAA0` and `0xBE0` are the interrupts the OS runs on: the battery A/D
    converter, the RTC and the keyboard all work in the emulator only when they raise
    exactly these codes (`TestADCOracleTranscript`, `TestRTCPeriodicInRunMatrix`,
    `TestKeyscMenuTap`).

!!! warning "Unconfirmed"
    The device behind `0xF00` is not identified. When, if ever, the OS enables the ETMU
    interrupts is not known. The identities of `0x620`, `0x640`, `0xA20` and `0xC00` are
    explained in the next two sections.

## External pins IRQ1 and IRQ2

INTEVT `0x620` and `0x640` are two of the chip's **external interrupt pins**, IRQ1 and IRQ2.
They have their own registers at `0xA4140000`, outside the INTC block above, with the same
layout as the SH7724's IRQ pin registers:

| Address | Register | IRQ1 (`0x620`) | IRQ2 (`0x640`) |
|---|---|---|---|
| `0xA4140010` | INTPRI00, 4-bit priority per pin (32-bit) | bits 27–24, OS: 13 | bits 23–20, OS: 5 |
| `0xA414001C` | ICR1, 2-bit trigger per pin (16-bit) | bits 13–12 | bits 11–10 |
| `0xA4140024` | INTREQ00, request flags (8-bit) | bit 6 | bit 5 |
| `0xA4140044` | INTMSK00, write 1 to mask (8-bit) | bit 6 | bit 5 |
| `0xA4140064` | INTMSKCLR00, write 1 to unmask (8-bit) | bit 6 | bit 5 |

The OS uses them as **wake-up sources**. Syscall `0x11BD` (`0x801DEF60`) arms IRQ1 just
before the OS waits in `sleep`. It arms IRQ2 too, but only when setting bytes in IL RAM allow
it (`0xFD8017C9` = 1, among others in `0xFD8017C8`–`0xFD8017CB`). It is called, for example,
at `0x802AE822` in the wait routine `0x802AE790` and at `0x802AEC28`. Syscall `0x11BE`
(`0x801DEFA2`) disarms both afterwards.

Casio's own hardware test menu names the two pins: its "[REMOTE ON]" screen lists **USB** and
**3PIN** and shows ON or OFF for each. The test reads them with syscall `0x11BF`
(`0x801DF02E`), which checks bit 1 of the port register `0xA4050162` for one and bit 2 of
`0xA4050134` for the other. The IRQ1 handler reads the same bit 1 of `0xA4050162` (syscall
`0x11C0`, `0x801DF084`), so IRQ1 is the **USB** pin and IRQ2 the **3PIN** pin, the 2.5 mm
serial link socket. "Remote on" would be the calculator switching on when a cable is plugged
in.

The IRQ1 handler watches both directions. When the trigger is "high level" and the pin is
high, it posts an event and switches the trigger to "low level"; when the trigger is "low
level", it switches back to "high level". The OS's
[idle routine](timers.md#the-2-hz-wake-up) also reads this pin to choose how deeply to sleep.

!!! warning "Unconfirmed"
    Read from the OS 3.60 code. The register names, and "high level"/"low level" for ICR1's
    codes 3 and 2, come from the SH7724, whose IRQ registers sit at the same addresses with
    the same bit positions. Every bit the OS uses matches that layout. Which level the USB pin
    shows with and without a cable has not been read on a calculator.

## USB and the serial port

**`0xA20` is the USB controller at `0xA4D80000`.** On the SH7724 that address is the first
USB module, also with INTEVT `0xA20`. The OS's USB code agrees:

- the start routine (around `0x80373E96`) sets IPRF bits 7–4 to 13 (on the SH7724, IPRF 7–4
  is the USB field) and touches IMR9 bit 1;
- the stop routine `0x803741D4` masks IMR9 bit 1 and the DMAC fields (IMR5 bits 4–5, IPRE
  15–12), clears IPRF 7–4, writes 0 to `0xA4D80000`, and sets bits in the clock generator
  registers `0xA4150014` and `0xA4150038`;
- the handler jumps into the USB driver at `0x80744D68`.

**`0xC00` is the serial port (SCIF) at `0xA4410000`**, behind the 3-pin link socket. The
handler reads the 16-bit status register at `+0x10` and the line status at `+0x24`. It runs
the receive routine on an overrun (`+0x24` bit 0) or received data (`+0x10` bits 0–1), the
transmit routine when bit 5 (transmit FIFO empty) is set, and an error routine for bit 7.
These bits are those of the SH-4A SCIF's SCFSR and SCLSR.

!!! warning "Unconfirmed"
    Both identifications rest on the SH7724's register layouts and on the OS code above. No
    probe has used either unit.

## gint

gint does not use the OS's dispatcher. It points VBR at its own handlers (in DASM's build,
VBR = `0x8C160000`, the start of the add-in RAM), sets CPUOPM.INTMU, and takes over the INTC
as described above. On a world switch the OS gets its INTC settings and its VBR back, which
is how the pending ETMU5 request above could reach the OS's table.

!!! info "Verified in the emulator"
    The gint add-ins DASM and Upsilon run in the emulator with this split; DASM's VBR comes
    from the symbols in its ELF file.

## The boot code's dispatcher

The boot code at the start of flash, before the OS header at `0x80020000`, has a second
dispatcher of the same design: VBR `0x80001454` (set at `0x800006FE` and `0x800034B6`),
interrupt entry `0x80001A54`, and tables at `0xFD8004D0`/`0xFD8006D0`. Older notes, made on
the 3.80 updater, describe these as the OS's. They are present at the same addresses in the
3.60 image, but they are not the ones the running OS uses.

!!! warning "Unconfirmed"
    How long the boot code's dispatcher stays in use before the OS sets its own VBR has not
    been traced. In the real IL RAM dump, `0xFD8004D0` no longer holds that table.

## In the emulator

- [`emu_go/intc.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/intc.go):
  the INTC registers, the source table and the gate (`MMIOBus.raise`).
- [`emu_go/cpu.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/cpu.go):
  acceptance (`acceptInterrupt`), the gate at acceptance (`gatePending`), sleep wake-up.
- Sources: [`keysc.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/keysc.go),
  [`rtc.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/rtc.go),
  [`gtimer.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/gtimer.go) (TMU,
  ETMU), and `periphIRQ` (battery A/D converter) and `dmac` in
  [`mmio.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmio.go).
- The OS's three sources keep fixed levels in the emulator (`0x560`: 8, `0xBE0`: 13,
  `0xAA0`: 9) instead of the priority field; the sources only add-ins use take the field
  as their level.
- The Python reference emulator (`emu/mmio.py`, `INTCStub`) does not gate requests. The
  OS's boot does not depend on it.
- The IRQ pins, USB and the serial port raise no interrupts. The block at `0xA4140000` is a
  stub (`intx` in `mmio.go`, `INTX` in `emu/mmio.py`) whose byte `+0x24` always reads `0x40`.
  Its comment calls that bit "key-scan ready", but it is INTREQ00 bit 6, IRQ1's request flag.
  The pin-function block is plain memory that reads back what was written, and in practice
  the "USB" pin bit at `0xA4050162` reads 0.
- Tests: `emu_go/intc_test.go` (`TestINTCGate`, `TestINTCGateAtAcceptance`,
  `TestCPUOPMIntmuSetsIMASK`), `TestSleepWakesOnRequest` in `emu_go/rtc_test.go`; probe
  `emu_go/intc_dump_test.go` logs the OS's INTC writes.
- Static tools: `re/irq_scan360.py` (VBR setters, references to INTEVT and the tables, the
  table as copied from flash) and `re/irq_ilram_check.py` (the table against the real IL
  RAM dump); `emu/dump_irqtable.py` reads the table from a running emulator.
