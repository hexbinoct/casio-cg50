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

## No FPU

The core has **no floating-point unit**. On the SH4AL-DSP, the opcodes a plain SH-4A uses
for its FPU (the ones whose first hex digit is `F`) are **DSP instructions** instead. The OS
doesn't need floating point: its numbers are decimal, computed in software with the help of
the [BCD ALU](peripherals/bcd-alu.md). SR bit 15 (FD, "FPU disable" on an SH-4A) is never
set by the OS.

!!! info "Verified in the emulator"
    A warm boot of OS 3.60 followed by `1÷7` and `2^10` in Run-Matrix, single-stepped:
    163 million instructions, **no** `F…` opcode and no transfer to or from FPSCR or FPUL,
    and both results come out right. Probe: `TestCPUWikiStats`.

!!! warning "Unconfirmed"
    Nobody has run an `F…` opcode on the calculator to see what it does. The OS sends the
    FPU exception codes (`0x120`, `0x800`, `0x820`) to its generic error screen (see
    [below](#exceptions-and-their-vectors)).

## Register banks

R0–R7 exist **twice**, as bank 0 and bank 1. In privileged mode (SR.MD = 1), **SR.RB**
chooses which bank the names R0–R7 mean. R8–R15 are not banked. The other bank stays
reachable as R0_BANK–R7_BANK through `ldc Rm,Rn_BANK`, `stc Rm_BANK,Rn` and their `.l`
memory forms.

- Taking an exception or an interrupt sets **MD, RB and BL**. The handler starts in bank 1,
  and the interrupted code's R0–R7 are still untouched in bank 0.
- The OS always runs privileged (MD = 1), normally in bank 0. Add-ins run privileged too:
  MD is bit 30 of the `SR = 0x40001101` [above](#sr-bit-12-is-set).
- R15, the stack pointer, is not banked. On entry the CPU copies it to **SGR**.

The OS 3.60 vectors use banking like this. The entry code (`0x80021000`, `0x80021300`,
and `0x8002153C` for interrupts) pushes SSR, SPC, PR and **bank 0's R0–R7** (with
`stc.l Rn_BANK,@-r15`) on the interrupted code's stack. It then enters the handler with RB
and BL cleared, so the handler is an ordinary function running in bank 0. The return path
`0x80021020` sets SR = `0x70000000` (MD, RB, BL: bank 1, blocked), pops bank 0's R0–R7 with
`ldc.l @r15+,Rn_BANK`, then SPC and SSR, and executes `rte`. See
[Interrupts](peripherals/interrupts.md#the-os-360-dispatcher) for the lookup in between.

!!! info "Verified in the emulator"
    In the probe run above, those 16 `stc.l`/`ldc.l` instructions and the two `rte` at
    `0x80021578` (into the handler) and `0x8002103A` (back to the interrupted code) are the
    only banked-register accesses and the only `rte`s the OS executed, over 816 interrupts.
    Conformance cases: `sr_bank_swap`, `stc_bank`.

## Exceptions and their vectors

An **exception** is an event caused by the instruction being executed: a TLB miss, an
address error, an illegal instruction, `trapa`. The CPU enters it the same way as an
interrupt: SSR ← SR, SPC ← the address of that instruction, SGR ← R15, MD = RB = BL = 1.
It then jumps to an offset from **VBR** (the vector base register):

| Jump to | Taken for | Code in |
|---|---|---|
| VBR + `0x100` | every exception except a TLB miss | EXPEVT (`0xFF000024`) |
| VBR + `0x400` | TLB miss | EXPEVT |
| VBR + `0x600` | interrupts | INTEVT (`0xFF000028`) |

`trapa #imm` also stores its number in **TRA** (`0xFF000020`). The OS 3.60 has no reference
to TRA, so it doesn't use `trapa`.

OS 3.60 sets VBR = `0x80020F00`. Both exception vectors save the registers the same way as
the interrupt vector, then look the EXPEVT code up in the **same table** as the interrupts
(see [Interrupts](peripherals/interrupts.md#the-os-360-dispatcher)). For the SH-4A's
exception codes, that table holds:

| EXPEVT | Exception | OS 3.60 handler |
|---|---|---|
| `0x040`, `0x060` | TLB miss (read, write) | `0x8002C918`, which maps the pages of a running add-in |
| `0x080` | Initial page write | `0x8002C918` |
| `0x0A0`, `0x0C0` | TLB protection violation (read, write) | Error screen: "PROTECT(R)" / "PROTECT(W)" |
| `0x0E0`, `0x100` | Address error (read, write) | Error screen: "ADDRESS(R)" / "ADDRESS(W)" |
| `0x180` | General illegal instruction | Error screen: "Illegal Code Err" |
| `0x120`, `0x140`, `0x160`, `0x1A0`, `0x1E0`, `0x800`, `0x820` | FPU exception, TLB multiple hit, `trapa`, slot illegal instruction, user break, FPU disable (two) | Error screen: "INTERRUPT" |

The error screen is the OS's **"System ERROR"** box, with "REBOOT :[EXIT]" and
"INITIALIZE:[EXE]". Four short entry stubs pass the routine at `0x8002C9A8` a cause:
`0x8002C98A` (2, address error), `0x8002C992` (3, protection), `0x8002C99A`
(9, illegal instruction) and `0x8002C9A2` (0, everything else).

!!! warning "Unconfirmed"
    The table entries and the messages are read from the OS 3.60 image
    (`re/exc_table360.py`). Which line the error screen shows for each cause has not been
    checked on a calculator or in the emulator.

## Delay slots

The **delayed branches**, `bra`, `bsr`, `braf`, `bsrf`, `jmp`, `jsr`, `rts`, `rte`, `bt/s`
and `bf/s`, execute the next instruction (the **delay slot**) before the jump takes effect.

- A slot must not hold an instruction that changes PC itself: another branch, `bt`/`bf`,
  `trapa`, or `ldc` to SR. The SH-4A manual makes that a **slot illegal instruction**
  exception (EXPEVT `0x1A0`). OS 3.60 shows it as "INTERRUPT" on its error screen.
- If the slot instruction raises an exception (a TLB miss, say), SPC is the address of the
  **branch**, not of the slot. After the handler, the branch and its slot run again.
- The OS never puts a PC-relative load (`mov.w`/`mov.l @(disp,PC)`, `mova`) in a slot, so
  it never depends on where such a load counts from in a slot.

!!! info "Verified in the emulator"
    In the probe run, **none** of the 10.6 million delay slots executed held a branch or a
    PC-relative load. A TLB miss in a slot restarts at the branch: conformance case
    `mmu_delay_slot_miss`. A user break on a slot: `TestUBCDelaySlot`.

!!! warning "Unconfirmed"
    The emulator computes a PC-relative load in a slot from the slot's own address. Nothing
    on the calculator has tested that.

## `sleep`

`sleep` halts the CPU until an interrupt request arrives. A request wakes it even when
SR.BL = 1; the interrupt is then taken once BL is cleared. See
[Interrupts](peripherals/interrupts.md#how-the-cpu-accepts-an-interrupt).

OS 3.60 has two `sleep` instructions. Both run with **BL = 1 and IMASK = 0**:

- `0x802AE77A`, in the idle routine `0x802AE742`, right after writing 0 to the clock
  generator register `0xA4150020`. See [Timers](peripherals/timers.md#the-2-hz-wake-up).
- `0x800208E8`, which the same idle routine reaches instead when `0x801DF084` returns 0
  (that routine reads bit 1 of the port register `0xA4050162`, the USB pin; see
  [Timers](peripherals/timers.md#the-2-hz-wake-up)). It calls `0xA0020926`, so this code runs **uncached** through P2 (at
  `0xA00208E8`). Before sleeping, it sets BL and IMASK = 15, saves VBR at `0xFD8024A0` and
  loads `0x80020F00`, changes the bus controller register `0xFEC10044` and the clock
  generator registers `0xA4150024` and `0xA4150020`, then lowers IMASK to 0. After waking,
  it restores VBR from `0xFD8024A0`.

!!! info "Verified in the emulator"
    In the probe run, all 763 `sleep`s were the second one, at `0xA00208E8`, because the
    emulator's USB pin reads 0. Which path a real calculator takes without a cable depends on
    that pin's level, which has not been read.

!!! warning "Unconfirmed"
    Why the second path runs uncached and rewrites the bus controller is not traced. A
    deeper low-power state that keeps the CPU off the DRAM would fit.

!!! danger "Don't copy either sleep path into a probe add-in"
    A probe that wrote the clock generator register and then executed `sleep`, like the
    OS's idle routine, reset a real calculator to its first-boot setup. See
    [Timers](peripherals/timers.md#the-2-hz-wake-up).

## Integer arithmetic matches the manual

Instructions that are easy to get subtly wrong were checked against the real CPU: `addc`,
`subc`, `negc`, `addv`, `subv`, `rotcl`, `rotcr`, `shad`, `shld`, `dmulu.l`, `dmuls.l`,
`div1`, the `cmp/…` family and `mac.l` with saturation (S bit). All **202** cases give the
results the manual describes.

!!! success "Verified on hardware"
    A probe add-in ran the cases on the calculator; `re/validate_silicon.py` compares them
    with the emulator's CPU.

## `div0u` clears T too

`div0u` prepares an unsigned division (the `div1` steps that follow). The SH-4 manual says it
sets **M, Q and T** to 0. Clearing only M and Q looks harmless, and the 202 hardware cases
above didn't catch it, because they all start with T = 0.

Real code does reach `div0u` with T = 1: a division routine in gcc's runtime library runs it
right after a compare that sets T. `div1` shifts T into the register it works on, so a T left
set becomes a stray 1 bit in the result. In that routine, the quotient came out 2^24 too large.

!!! info "Verified in the emulator"
    The emulator once cleared only M and Q. In the FlowCE add-in, every shape drawn with that
    division (the integral sign, sigma, icons) came out as lines across the screen. The real
    calculator drew them correctly. The OS uses it too: fixing `div0u` changed the OS 3.60
    boot within its first 24,000 instructions. Conformance cases: `div0u_clears_t`,
    `div1_full_quotient_100_7_t_set`.

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

## In the emulator

- [`emu_go/cpu.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/cpu.go): the
  instruction set, SR and bank switching (`setSR`), interrupt acceptance
  (`acceptInterrupt`), delay slots (`branchDelayed`) and `sleep`.
  [`mmu.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/mmu.go) raises the TLB
  exceptions and halts on one with SR.BL = 1;
  [`ubc.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/ubc.go) is the user
  break controller.
- `emu/cpu.py` is the Python reference emulator that the Go core is checked against.
- What the emulator does **not** do like the chip:
    - An `F…` opcode is counted and otherwise skipped, in both emulators, so an add-in using
      DSP instructions would compute wrong results without any error.
    - An illegal instruction or a `trapa` **halts** the emulator with a fault instead of
      raising EXPEVT `0x180`/`0x160`, so the OS's "System ERROR" screen never appears.
    - A branch in a delay slot is simply executed; there is no slot illegal exception.
    - The banks switch whenever SR.RB changes, even with SR.MD = 0, where the chip always
      uses bank 0. That never matters here, because the OS and add-ins always run with
      MD = 1.
- Tests:
    - `emu_go/conformance_test.go` runs the 72 cases in `emu/conformance.json` (made by
      `emu/conformance_gen.py` from the Python core), including the bank, delay-slot,
      `div0u` and MMU cases named above.
    - `TestGoldenBoot` (`emu_go/golden_test.go`) compares the full CPU state with the Python
      core every 1,000 instructions over a 2-million-instruction boot.
    - `re/validate_silicon.py` checks the core against the 202 hardware results.
    - `emu_go/intc_test.go`, `TestSleepWakesOnRequest` in `emu_go/rtc_test.go`,
      `emu_go/ubc_test.go`, `emu_go/mmu_test.go`.
- Probe `TestCPUWikiStats` (`emu_go/cpuwiki_probe_test.go`, `-tags probe`) produced the
  instruction counts on this page; `re/exc_table360.py` lists the exception table.
