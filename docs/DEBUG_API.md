# The core's debug API

The emulator core (`emu_go/`, package `cg50emu`) has a debugger for the host: CG50 Studio's
debugger windows, tests, scripts. It is in `emu_go/debug.go`, its tests in
`emu_go/debug_test.go`. This page lists what each call does and what it guarantees.

The **host** debugger described here is separate from the calculator's own **User Break
Controller** (`emu_go/ubc.go`), which the emulator models for debuggers that run on the
calculator (DASM, gint's GDB stub). The two work at the same time without affecting each other:
a UBC break is an exception the guest handles, a host stop pauses the whole machine.

## How it runs

- **Cost.** Nothing is checked until a feature is armed: a breakpoint, a watchpoint, the trace,
  a break on syscalls, exceptions or interrupts, or a step in progress. `Step` decides once per
  call whether the debugger is on. While it is off the run loop is the one used without a
  debugger (measured: within noise of the code before the debugger, see below).
- **Armed**, each instruction goes through a debug step that wraps the same dispatch the plain
  loop uses (interrupt acceptance, HLE, UBC, MMU, execute), so the machine (registers, RAM,
  device timing, cycle counts) is exactly what an undebugged run gives. Two tests check this:
  `TestDebugPathMatchesPlain` (3 M instructions of the boot, both debug loops) and
  `TestDebugPathMatchesPlainOS` (20 M cycles on the MAIN MENU with RIGHT held: 8.5 M
  instructions, 50 interrupts, 4 frame pushes, the rest asleep).
- With only breakpoints (or the syscall break, a run-to target, an exception break) armed,
  only instructions on a 4 KB page that holds a stop address go through the full check: a bit
  per page, one test per instruction.
- **Threads.** Every call takes the emulator's mutex. `Step`/`Run` hold it for one chunk, so a
  call from the UI goroutine waits until the chunk ends.

| Configuration (16 MB dump, boot, 250 000-instruction chunks, Apple M4 Pro) | Minstr/s |
|---|---|
| before the debugger existed (HEAD, median of 10) | 175.6 |
| debugger off (median of 10, alternated with the line above) | 174.2 |
| 3 breakpoints, one on a page the boot runs | ~136 |
| trace ring on (64 K entries) | ~108 |
| one watchpoint | ~95 |

The calculator itself runs about 45 M instructions per second, so 1× speed is the same in every
configuration. `go -C emu_go test -run XXX -bench Debug -benchtime 4x .` measures it.

## Stops

A stop **latches**: the machine stays stopped, `Step` does nothing and `Run` returns the stop
again, until `Continue` or a step command. A host loop can therefore keep calling `Step`/`Run`
on its own goroutine: it pauses by itself on a stop and the UI asks `Stopped()` why.

```go
type Stop struct {
    Reason  StopReason // see below
    PC      uint32     // the next instruction to run
    Cycles  uint64
    Breakpoint int      // StopBreakpoint
    Watch      WatchHit // StopWatchpoint
    Syscall, Handler uint32 // StopSyscall: the number (r0) and the OS function it leads to
    Code, From       uint32 // StopException/StopInterrupt: EXPEVT/INTEVT, and SPC
    Fault string            // StopFault
}
```

| Reason | When | PC at the stop |
|---|---|---|
| `StopNone` | `Run` used its whole budget (not a stop) | |
| `StopPause` | `Pause` | the next instruction |
| `StopBreakpoint` | execution reached an enabled breakpoint | the breakpoint, **not yet run** |
| `StopSyscall` | an add-in called a syscall (see below) | 0x80020070, not yet run |
| `StopStep` | a step command or `RunTo` finished | where it ended |
| `StopWatchpoint` | a data access matched | the instruction **after** the access |
| `StopException` | the CPU entered an exception handler | the handler's first instruction |
| `StopInterrupt` | the CPU accepted an interrupt | VBR+0x600, nothing of the handler run |
| `StopFault` | the core halted (`Fault()`) | where it halted |

Resuming (`Continue`, a step) never stops again at the address it resumes from before the
instruction there has run, so continuing from a breakpoint does not hit it again at once.

## Execution control

| Call | What it does |
|---|---|
| `Run(n int) Stop` | Runs up to `n` instructions (as `Step`); returns the stop, or `StopNone`. |
| `Step(n int)` | Unchanged signature; the same as `Run` without the result. |
| `Stopped() (Stop, bool)` | The current stop, if stopped (a fault counts). |
| `Pause()` | Stops between two instructions (after the running chunk). Drops a step in progress. |
| `Continue()` | Lets a stopped machine run again from the next `Run`/`Step`. |
| `StepInto() Stop` | One instruction. |
| `StepOver() Stop` | One instruction; a call (`bsr`, `bsrf`, `jsr`) runs until it returns. |
| `StepOut() Stop` | Runs until the current function returns. |
| `RunTo(addr uint32) Stop` | Runs until the next instruction is at `addr`. |

**Steps.** One step is one instruction the way a debugger shows it: a delayed branch and its
delay slot are one step (the core runs them together), and a `bt/s`/`bf/s` that is not taken
is one step with its slot too. If an interrupt is accepted before the instruction, `StepInto`
ends at the handler's entry. A sleeping CPU (after `sleep`) steps when an interrupt wakes it.

**Step over and step out** count call depth: each `bsr`/`bsrf`/`jsr` that runs adds one, each
`rts` takes one off, and the goal is met by the `rts` that brings it below where it started.
The stop is after that `rts` and its slot, at the return address. Recursion and calls in the
callee therefore don't end it early, and a tail call through `jmp` (the syscall routine is one)
is followed to the `rts` that really returns. An instruction that an exception undid (an MMU
fault, rolled back and run again after the handler) is not counted. HLE functions (the native
blitter) count as returning. Interrupt and exception handlers in between balance their own
calls; `rte` is not counted.

**Synchronous part.** The step commands run up to 2 M instructions inside the call. Most finish
there and return their `StopStep`. One that doesn't (stepping over `GetKey`, which waits for a
key) returns `StopNone` and stays armed: the host's next `Run`/`Step` calls finish it, and
`Stopped()` then reports it. Any other stop (a breakpoint inside the callee) ends the step.

## Breakpoints

| Call | What it does |
|---|---|
| `AddBreakpoint(addr uint32) int` | An enabled breakpoint at the virtual address `addr`; returns its ID. |
| `RemoveBreakpoint(id) bool`, `ClearBreakpoints()` | Delete one or all. |
| `EnableBreakpoint(id, on) bool` | Turn on or off (the hit count stays). |
| `SetBreakpointIgnore(id, n uint64) bool` | The first `n` hits don't stop. |
| `SetBreakpointCondition(id, func(*Regs) bool) bool` | Hits count only when it returns true (nil removes it). |
| `ResetBreakpointHits(id) bool` | Zero the hit count. |
| `Breakpoints() []Breakpoint` | Copies, by ID: `Addr`, `Enabled`, `Hits`, `Ignore`, `Cond`. |

The address is the exact virtual PC: the P1 and P2 aliases of one instruction (`0x80…` and
`0xA0…`) are two different breakpoints, and an add-in's code is at its virtual 0x003xxxxx
address. The condition is called with the emulator's lock held and sees the registers before
the instruction; it must not call the `Emulator`. Breakpoints and watchpoints survive `Resume`
(a stop, a step and the trace contents don't).

## Watchpoints

```go
id := e.AddWatchpoint(cg50emu.Watchpoint{Addr: 0x0C010000, Len: 4, Kind: cg50emu.WatchWrite,
    Phys: true, Enabled: true})
```

- `Kind`: `WatchRead`, `WatchWrite` or `WatchAccess` (0 = access).
- An access matches when it overlaps `[Addr, Addr+Len)` (`Len` 0 = 1).
- `Phys: false` compares the virtual address the instruction used. `Phys: true` compares
  **bus addresses**: the 29-bit physical address (through the TLB for a translated access) for
  the external bus, the P4 address for the on-chip memories (IL RAM folded onto its 16 KB). A
  physical watchpoint catches every alias: the OS reaches the same DRAM through 0x8C… and 0xAC….
- `MatchValue` with `Value`/`Mask` (`Mask` 0 = all bits): only accesses whose value `v` has
  `(v ^ Value) & Mask == 0`; `v` is the value read or written, of the access's size.
- The stop comes after the instruction (a store has been made). `Stop.Watch` gives the ID, the
  virtual and bus address, size, value, direction and the PC of the instruction (for a delay
  slot, its branch's PC). The first matching access of an instruction is reported.
- Seen: every CPU data access, PC-relative literal loads included. Not seen: instruction
  fetches, DMA and other bus masters, and the core's own register writes during exception
  entry. An access that faults (MMU) is never reported.
- `RemoveWatchpoint(id)`, `EnableWatchpoint(id, on)`, `ClearWatchpoints()`, `Watchpoints()`.

## Breaking on syscalls, exceptions and interrupts

| Call | Stops |
|---|---|
| `BreakOnSyscall(on bool, numbers ...uint32)` | when execution reaches the OS's syscall routine at **0x80020070**, before it runs, with the number in r0 (none listed = every number). |
| `BreakOnException(on bool, expevt ...uint32)` | after the CPU entered an exception: illegal (0x180) and slot illegal (0x1A0) instruction, `trapa` (0x160), MMU exceptions (TLB miss 0x040/0x060, protection, initial write), user breaks of the emulated UBC (0x1E0). |
| `BreakOnInterrupt(on bool, intevt ...uint32)` | when the CPU accepts an interrupt (INTEVT 0x560 timer, 0xBE0 key scan, …). |

**How syscalls are found.** In OS 3.60 every syscall goes through the five-instruction routine at
0x80020070 (`wiki/docs/os/syscalls.md`): `mov.l @(disp,pc),r2` loads the table address
(0x80687CD0), `shll2 r0`, `mov.l @(r0,r2),r0`, `jmp @r0`, `nop`. An add-in's stubs put the number
in r0 and jump there. The break is a stop address on that routine; `Stop.Handler` is the table
entry for the number, read from the routine without side effects, so it works with other OS
versions too. The routine uses `jmp`, so `StepOut` from the stop runs the OS function and stops
when it returns to the add-in, with its result in r0. The OS calls its own functions directly,
never through the routine: only add-ins' calls are seen. The emulator's own calls into the OS
(installing files, `oscall.go`) are never stopped by the debugger.

TLB misses are frequent while an add-in runs (the OS maps its code a page at a time), so filter
by code when breaking on exceptions.

## Registers

- `Registers() Regs`: R0-R15 as instructions see them now, both banks (`Bank0`, `Bank1`) and
  which one R0-R7 name (`Bank`: 1 only when SR.MD and SR.RB are both set), SR raw and decoded
  (`Flags`: T, S, Q, M, IMASK, DSP (bit 12), FD, BL, RB, MD), GBR, VBR, SSR, SPC, SGR, DBR, PR,
  PC, MACH, MACL, FPUL, FPSCR, `Sleeping`, `Cycles`, `Executed`. `DecodeSR(sr)` decodes any value.
- The core has no FPU and does not model the DSP's data registers (A0/A1, X0/X1, Y0/Y1, …):
  FPUL and FPSCR are the two words that `lds`/`sts` with those encodings move (on the
  SH4AL-DSP the FPSCR pair is DSR).
- `SetRegister(name string, v uint32) error`, names from `RegisterNames()`: `r0`…`r15`,
  `r0_bank0`…`r7_bank1`, `sr`, `gbr`, `vbr`, `ssr`, `spc`, `sgr`, `dbr`, `pr`, `pc`, `mach`,
  `macl`, `fpul`, `fpscr`. Writing SR is the CPU's own SR write: changing MD/RB switches the
  bank R0-R7 name, as `ldc` does. Writing PC wakes a sleeping CPU.

## Memory

| Call | What it does |
|---|---|
| `ReadMemory(addr uint32, dst []byte, phys bool) (int, error)` | Copies memory into `dst`. |
| `WriteMemory(addr uint32, src []byte, phys bool) (int, error)` | Writes `src`. |
| `Peek32(addr, phys) (uint32, error)` | One big-endian word. |
| `Translate(va) (AddrInfo, error)` | Where a virtual address leads: bus address, region, translated or not. |

- `phys: false`: a virtual address, translated through the current TLB the way the CPU would
  (P0/U0/P3 while MMUCR.AT = 1). `phys: true`: a bus address, 0x00000000-0x1FFFFFFF, or an
  on-chip memory at its P4 address.
- Both stop at the first byte they cannot reach and return how many bytes they did, with a
  `*MemError` whose `Kind` is `MemUnmapped` (nothing there), `MemTLBMiss` (no valid TLB entry:
  an add-in page the OS hasn't mapped yet reads as this), `MemMMIO` or `MemReadOnly`.
- **No side effects.** Device registers (MMIO: 0xA4000000-0xBFFFFFFF except DRAM's mirrors,
  and P4 outside the on-chip RAMs) are **refused**, since reading one can change the device
  (key scan state, flags cleared on read). The flash reads as its array, also while the OS
  erases a sector (the CPU would read status words). The TLB is only looked up, protection
  bits are ignored.
- Readable and writable: the 32 MB flash array, DRAM (every mirror), IL RAM, X/Y RAM, the
  on-chip RAM at 0xFE200000. The flash window beyond 32 MB reads 0xFF and refuses writes.
- **Writes** go straight into the backing memory: no device sees them, the flash takes them
  without its program/erase commands, and the CPU runs patched code from its next fetch (the
  core decodes every instruction from memory; there is no cache to keep coherent). One
  exception: with HLE on (`EnableHLE`, Android only), the native blitter at 0x80056900 still
  runs natively if its code is patched. Patched flash is part of the machine from then on: it
  is in save-states and `FlashImage`.

## Trace

`SetTrace(n int)` keeps the PCs of the last `n` executed instructions (rounded up to a power
of two; 0 turns it off). `Trace() ([]uint32, uint64)` returns them oldest first, with how
many were recorded in all. A delayed branch and its slot are one entry. It is off by default;
on, it costs about 40% of the speed (table above).

## Call stack

`CallStack(max int) []Frame` (0 = 32 frames). Frame 0 is the PC; each later frame is a return
address: `PC` (where the caller resumes), `Call` (the call instruction), `SP` (the stack slot it
was found in), `Source` (`FramePC`, `FramePR`, `FrameStack`) and `Confirmed`.

It is rebuilt from the code and the stack each time, best effort. Candidates are words whose
instruction 4 bytes before is a call (`bsr`, `bsrf`, `jsr`): PR, then the stack from r15 up to
16 KB. PR is used only while the current function has not saved it (no `sts.l pr,@-r15`
between the function's start, known from the caller's `bsr` or else just after the closest
`rts` before the PC, and the PC). A `bsr` frame is `Confirmed` when its target is at or shortly
before the code of the frame inside it; one that fails this is taken for a stale word and
dropped. `jsr`/`bsrf` targets are not known, so those frames are kept unconfirmed.

- Reliable: ordinary compiled code (the OS, gcc add-ins) that saves PR in its prologue.
- Can be wrong: stale return addresses left in a frame, a prologue that comes after an early
  return or that a branch skipped, tail calls (`jmp`, `bra`), and handlers (the interrupted PC
  is in SPC, not on the stack).

## Not done, or approximate

- Breakpoint conditions are Go functions; an expression language is the app's business.
- Breakpoints compare the exact virtual PC (no "every alias" option as watchpoints have).
- No shadow call stack while running; `CallStack` is a heuristic (above).
- No side-effect-free view of device registers: MMIO is refused, not shown.
- `MemTLBMiss` for add-in pages the OS would map on demand: the debugger does not read the OS's
  page table (0x8C04CF0C) to find them.
- Hardware details the core lacks are absent here too: no ITLB, no caches, no DSP registers.
- A `bt/s`/`bf/s` that is not taken runs its slot as a separate instruction in the core, so an
  interrupt can (rarely) come between them; the step still counts them as one.
