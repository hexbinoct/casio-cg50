# CPU (SH-4A core)

The SH7305 contains an SH-4A core. Code written for the SH-4A manuals mostly runs as
expected; this page collects what is specific to this chip or easy to get wrong.

## The basics

- **Big-endian.** Every multi-byte value in memory is stored most significant byte first.
- **SH4AL-DSP class.** The core behaves like Renesas's SH4AL-DSP variant rather than a plain SH-4A.
- **Numbers are decimal.** The OS's calculator arithmetic works on BCD numbers in software,
  helped by a hardware [BCD ALU](peripherals/bcd-alu.md).

## SR bit 12 is set

On a real calculator, an add-in reads `SR = 0x40001101`. Bit 12 is set although the SH-4A
manual lists it as reserved. On the SH4AL-DSP bit 12 is the **DSP** bit.

!!! success "Verified on hardware"
    Read by the DASM add-in on a real fx-CG50.

Don't mask SR's "reserved" bits off when saving and restoring it, as an SH-4A manual would
suggest.

## Integer arithmetic matches the manual

Instructions that are easy to get subtly wrong were checked against the real CPU: `addc`,
`subc`, `negc`, `addv`, `subv`, `rotcl`, `rotcr`, `shad`, `shld`, `dmulu.l`, `dmuls.l`,
`div1`, the `cmp/…` family and `mac.l` with saturation (S bit). All **202** cases give the
results the manual describes.

!!! success "Verified on hardware"
    A probe add-in ran the cases on the calculator; `re/validate_silicon.py` compares them
    with the emulator's CPU.

## CPUOPM.INTMU (`0xFF2F0000`, bit 3)

When INTMU is set, accepting an interrupt also sets **SR.IMASK to that interrupt's level**,
so only higher-priority interrupts can nest. When it is clear, IMASK is left alone.

- The OS never sets it.
- gint sets it at startup, and its interrupt dispatcher relies on it when it re-enables
  interrupts inside a handler.

!!! info "Verified in the emulator"
    Without INTMU, a gint add-in with a fast timer (Upsilon) nests interrupts until its stack
    runs into its data and it crashes. With it, Upsilon runs. Test: `TestCPUOPMIntmuSetsIMASK`.

## An MMU exception with SR.BL = 1 resets the CPU

SR.BL = 1 blocks exceptions. If the MMU raises one anyway (a TLB miss or protection
violation) while BL is set, the CPU cannot handle it and **resets**. On the calculator that
looks like a crash back to the language screen.

This bites code that runs in an exception handler or with interrupts blocked and touches
memory that may not be mapped, e.g. an add-in address right after the OS has flushed the TLB.

!!! info "Verified in the emulator"
    The emulator halts in this case instead of continuing. That exposed a probe add-in that
    could reset a real calculator: a timer interrupt arriving between the OS's TLB flush and
    its reload of the add-in's RAM mappings.

## The UBC decides at fetch time

The user break controller (UBC, `0xFF200000`) stops the CPU at an instruction or a memory
access, which is what debuggers use. Two details:

- A "break after this instruction" is decided **when the instruction is fetched**. It fires
  even when that instruction is the one that disables the break channel.
- The OS leaves the UBC **powered off** (MSTPCR0 bit 17 set) when it starts an add-in. gint
  powers it on and points DBR (the debug vector base) at its own handler.

!!! success "Verified on hardware"
    A debugger add-in single-stepped 79 times through its own "disable the channel" code on
    the calculator. An emulator that decided after execution stopped at step 19; it now
    matches the calculator.

The UBC never matches while SR.BL = 1, and the instructions `ldc`/`stc` for **DBR** and
**SGR** (the saved R15 on an exception) are available.
