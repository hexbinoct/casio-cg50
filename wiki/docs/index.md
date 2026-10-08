# CG50 Internals

Notes on how the **Casio fx-CG50** works, seen from software: the memory map, the SH-4A CPU
core and its quirks, the on-chip peripherals, and the internals of the calculator's OS (3.60).

It grew out of [casio-cg50](https://github.com/hexbinoct/casio-cg50), an emulator that boots
the real OS from reset and runs the built-in apps and add-ins. Building it meant pinning down
exactly how the hardware behaves; this site is where those findings are written down.

!!! note "Software only"
    Nothing here involves opening the calculator. Everything comes from reading the OS in
    Ghidra, running it in the emulator, and small probe add-ins run on a real fx-CG50.

## How to read a page

Each fact says how it was established:

!!! success "Verified on hardware"
    Measured on a real fx-CG50 with a probe add-in. The strongest kind of evidence.

!!! info "Verified in the emulator"
    The OS (or an add-in) only works when the emulator behaves this way, and a test in the
    emulator's suite holds it to that.

!!! warning "Unconfirmed"
    Read from the OS's code, or observed informally, but not measured. Treat as a good guess.

See [How facts are verified](about/verification.md) for the details.

## Start here

- [Memory map](memory-map.md): where flash, RAM, the on-chip memories and the peripherals live.
- [CPU](cpu.md): the SH-4A core as it behaves in the SH7305, including what differs from the manuals.
- [Interrupts](peripherals/interrupts.md): the INTC, and the OS 3.60's dispatcher and handler table.
- [Keyboard](peripherals/keyboard.md), [Display](peripherals/display.md),
  [Timers](peripherals/timers.md) and the [Battery ADC](peripherals/battery-adc.md): the
  peripherals the OS runs on.
- [BCD ALU](peripherals/bcd-alu.md): an undocumented decimal arithmetic unit every calculation goes through.
- [Boot and power-off](os/boot.md): what the OS does from reset to the MAIN MENU.

## Other resources

These cover ground this site doesn't repeat:

- [WikiPrizm](https://prizm.cemetech.net/): syscalls and the PrizmSDK/libfxcg view of the calculator.
- [gint](https://git.planet-casio.com/Lephenixnoir/gint) and [Planète Casio](https://www.planet-casio.com/):
  the add-in kernel whose drivers are themselves a map of the SH7305.
- Simon Lothar's *fx calculators based on SuperH* documentation: OS headers and syscall lists.

!!! abstract "Not affiliated with Casio"
    Casio and fx-CG50 are trademarks of Casio Computer Co., Ltd. This site contains no Casio
    firmware. OS addresses refer to OS 3.60 unless stated.
