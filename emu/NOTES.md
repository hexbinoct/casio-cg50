# fx-CG50 emulator — skeleton notes

Python SH-4A interpreter that boots the unpacked OS image and validates the
hardware contract we reverse-engineered (see `../RECON_NOTES.md`). This is the
reference/oracle model; the Android target will port the hot path to C++ once
the contracts are proven (or reconcile with Heath123/casio-emu).

## Files
- `memory.py` — address space. P0/P1/P2 alias phys = vaddr & 0x1FFFFFFF; ROM (OS
  image) @ phys 0, DRAM @ 0x0C000000, on-chip RAM @ 0xFD800000; MMIO ranges
  delegated to the bus. P4 control space handled specially.
- `mmio.py`   — MMIO bus + peripheral stubs (CPG/PFC/WDT/KEYSC/TMU/ETMU/BSC/DMAC/
  CCN/R61524). Stubs return values that satisfy the OS poll loops (CPG PLL ready,
  DMA TE done, KEYSC all-released) and log every access.
- `cpu.py`    — SH-4/SH-4A interpreter. Integer + system ISA, correct delayed
  branches, register banking on SR.RB. FPU stubbed. `div1` is a stub (TODO).
- `run.py`    — loads the image, PC=0x80000000, single-steps with disasm trace
  (reuses `re/sh4dis.py`), reports where it stops + unmapped MMIO.

Run:  `python emu/run.py [max_instructions] [trace_count]`

## Status — BOOTS TO FRAME PUSH ("it's alive")
Boots the real OS from reset through **12.8M instructions** and reaches
**Bdisp_PutDisp_DD (0x80055260)** — the OS rendered a frame and pushed it to the LCD.
At that point the DMAC is programmed **exactly** as reverse-engineered:
`SAR=0x0C000000` (VRAM=DRAM start), `DAR=0x14000000` (LCD area-5), `DMATCR=0x1440` —
total validation of the display contract by execution. SR shows BL=0 (interrupts
enabled) by ~40M instructions, i.e. past boot into normal running.

Early boot MMIO writes reproduce the RE byte-for-byte: BSC 0xFEC10000<-0x00010013;
CPG 0xA4150020/30/38; CCR<-0x800; MMUCR<-4; PFC pin-mux; WDT 0x5A00/0xA507.

Fixes that peeled back each boot layer (all matching real HW behaviour):
icbi decode; real `div1`; free-running counter @0xA4130000 (delay loops); ETMU
elapsed-flag @0xA44A0060 (one-shot waits); DMAC TE-done for all channels; NOR-flash
address space (reads=image/0xFF, writes=commands) for the on-chip-RAM flash driver.

`capture_frame.py` boots to the push and dumps VRAM (RGB565) to `frame.bmp`. First
push is a near-blank screen-clear (the menu render needs interrupts + more runtime).

## Interrupt delivery — IMPLEMENTED (entry validated)
cpu.py: `raise_irq(intevt, level)` + `_accept_interrupt()` does SH-4 hardware entry
when SR.BL==0 and level>IMASK: save SSR=SR, SPC=PC, SGR=R15; write INTEVT(0xFF000028);
SR |= MD|RB|BL; PC = VBR+0x600. `run_irq.py` injects a periodic timer IRQ (INTEVT 0x188).
VALIDATED: an injected IRQ vectors correctly to the RELOCATED dispatcher @0x80021500
(runtime VBR=0x80020f00), which saves context and dispatches via:
  handler = *(0xFD8010C8 + ((INTEVT-0x40)>>3)*4);  prio = *(0xFD8012C8 + ...);
  common-return PR = 0x80021020.   (Boot-time table was 0xFD8004D0; relocated to 0xFD8010C8.)

## ✅ FLASH WALL BROKEN (2026-06-02) — real fls0 backing → OS reaches idle
The "wall" was the flash filesystem driver getting 0xFF (we only had the OS RCDATA, no
fls0). FIX (Path 2): boot the **physical full flash dump** `os/flash_dump/flash_full.bin`
(16MB, OS 3.60 + real fls0 tail) — it backs the flash window with real filesystem data.
Differential proof (`emu/run_full.py`, both 3.60 so version is not a confound):
  - A = os.bin (no fls0): bails out of flash init fast.
  - B = flash_full (real fls0): genuinely processes the FS (oscillates OS-code <-> flash
    driver), and by **~14M instructions reaches a STABLE IDLE state** at PC≈0x801de57e /
    0x802af478, holding there to 40M with **NO fault, irqs=0**. = OS booted to idle/wait.
NOTE: this runs the **3.60** OS (the physical device's version); our Ghidra anchor stays
3.80 (per user decision) — fine, the emulator is OS-version-independent (proven: 500k-instr
lockstep 3.60==3.80). For the live-menu milestone we boot 3.60 (self-consistent flash).
Tools: `emu/run_full.py` (differential boot), `emu/run_idle_probe.py` (idle SR + MMIO
poll capture, to identify the timer/INTC source for interrupt delivery).

## ✅ INTERRUPT DELIVERY WORKING (2026-06-02) — timer tick drives the OS
Full periodic timer interrupt now generated + delivered + handled, 50/50 ticks clean.
Source: `mmio.py` MMIOBus.tick(cpu) — every `timer_period` instructions it sets PERIPH_IRQ
flag bits 14/15 @0xA4610088 and calls cpu.raise_irq(INTEVT, level); cpu._accept_interrupt
gates on SR.BL/IMASK so it's safe to free-run (OS only takes it once set up). Driver:
`emu/run_live.py` (loads idle snapshot `emu/idle_state.pkl`, fires timer, observes).

★ KEY CORRECTION: the timer INTEVT is **0x560**, NOT 0x188 (RECON's 0x188 was wrong).
The 3.60 dispatcher @0x80021502 indexes a 4-byte handler table by (INTEVT-0x40)>>3, so a
valid INTEVT must be a multiple of 0x20; 0x188 gives a MISALIGNED read -> garbage handler
-> runaway. Found the real code by dumping the live handler table (`emu/dump_irqtable.py`,
119 entries @0xFD8010C8, default/spurious=0x8002c9a2) and testing every candidate that
references 0xA4610088 (`emu/test_candidates.py`): only **INTEVT 0x560 -> handler 0x801ded94**
acks bits 14/15 (flag 0xe042->0x2042) and rte's back to idle. Per-tick the ISR SCANS THE
KEYBOARD (KEYSC 0xA4080090/04/D0) — the timer->keyboard pipeline, live. After ticks start,
the OS leaves the idle loop (SPC 0x801de57a -> 0x80374306) and runs event-loop code.

## Toward a rendered menu — peripheral-wait CHAIN (each fix surfaces the next)
With interrupt delivery working, the OS advances through a series of hardware waits; each
needs a peripheral modeled. Iterate fast via the idle snapshot (`run_live.py` auto-upgrades
an old snapshot with new peripherals; `probe_wait.py <addr>` characterizes any spin loop).
 1. ✅ **0x80374300 delay loop** read the **ETMU down-counter @0xA44D00D8** (0xA44D0000),
    spinning until (reference-current)>=N. We returned constant 0 -> deadlock. FIX: modeled
    `ETMUCounter` (mmio.py) returning a value that DECREASES with cpu.cycles. OS now completes
    the delay and returns to its idle loop. (NOTE 0xA44D00D8 = ETMU TCNT, a fine delay timer.)
 2. ⏳ **timer ISR stuck polling unmodeled peripheral @0xA44B0000** (current blocker).
    (Corrected: the 0x801e5a70 `dt r2` loop is a harmless FIXED 100-count settle delay —
    `mov #0x64,r2` — not the real issue.) The REAL wall: while servicing the timer IRQ
    (so **SR.BL=1**, RB=1 — that's why irqs froze at 3, the ISR never returns), the OS runs
    a poll loop at **0x801dff06..0x801dff34**: it calls helpers (0x801e59f4, 0x801df090)
    that read the **0xA44B0000 block** (halfword data regs 0x00..0x10 + status @0x12/0x14)
    and INTX 0xA4140024, OR-accumulates into r4, and loops **while (r4 & 7)==0** — i.e.
    until the peripheral signals ready/event in bits 0..2. Status checker @0x801e508c:
    `mov.w @0xA44B0012,r0; tst #1,r0` (bit0). We return 0 for 0xA44B00xx -> condition never
    met -> infinite ISR -> BL stuck high -> no further interrupts. Tools: trace_outer.py,
    re/disasm_static.py. **NEXT: identify 0xA44B0000** (timer cluster: A449=TMU, A44A=ETMU,
    A44D=ETMU2; A44B = ? ~6-8 hw data regs + status@0x12 bit0 — possibly RTC/ADC/2nd scan
    unit) and model its ready/data status so the ISR completes. Speculative bit-poking risks
    false progress; better to confirm its identity (gint/WikiPrizm SH7305 map) first.
 3. ✅ **timer ISR poll on 0xA44B0000/INTX** — the ISR waited (BL=1) for a key-scan
    "ready" flag. **0xA44B0000 = KIU (Key Interface Unit)**, SH7724-style key INPUT DATA
    regs (KIUDATA0.. at +0,+2,+4..; 0=no key); **0xA4140024 = KIU event/status**, the ISR
    spins until **bit6 or bit5** (scan complete). FIX: `INTX` returns 0x40 (bit6) at +0x24;
    `KIU_DATA` (0xA44B0000) returns 0. ISR now completes & rte's -> interrupts flow freely
    (irqs 3 -> 117 over 20M). All these are wired by `mmio.upgrade_bus(mmio,cpu)` (shared by
    run_live + probes; also baked into MMIOBus.__init__ for fresh boots).
 4. ⏳ **CURRENT: OS alive but not rendering — compute-bound, NOT a peripheral wait.**
    With timer+ETMU+KIU/INTX done the OS is fully alive (servicing ticks, scanning keys,
    heavy flash/code execution) but VRAM stays ~blank (frame_live.bmp 66 px) across 20M.
    probe_storage.py shows the hot loop @0x803717da is pure **table-driven byte processing**
    (`b=buf[i]; t1=tbl[b]; t2=tbl2[b]; cmp`), **no MMIO, no flash commands** — it reads
    varied CODE pages (0x366-0x371, 0x71e, 0xd72…) i.e. real progress, not a tight stall.
    RUNTIME HYPOTHESIS DISPROVEN: ran the alive snapshot +50M more (run_alive.py); irqs
    kept climbing 107->315 (interrupts healthy) but VRAM stayed flat at 96 and NO new frame
    was ever pushed — PC just cycles the same band (0x803851xx, 0x80366-0x80371xxx, flash
    driver) forever. So the OS reached a STEADY STATE that never renders: it's gated on a
    condition we don't satisfy, not on time. Remaining suspects: (a) FPU (fonts float-heavy;
    no-ops now) — but this loop is integer; (b) the OS keeps RE-running storage/FS init
    (same band as boot FS) -> maybe flash WRITE/commit matters (we ignore flash writes, so
    metadata read-back never changes -> retry loop), though probe_storage saw no cmd writes
    in its window; (c) a subtle CPU/decoder bug derailing a main-loop decision. NEXT (needs
    a direction decision): trace the main-loop branch decisions (what condition gates leaving
    this band) — e.g. dump backward-branch edges + the compared values across the 0x803851xx
    / 0x80366xxx band — to find the single check that never passes. Snapshots for instant
    iteration: `idle_state.pkl` (@14.5M), `alive_state.pkl` (@26.5M).

## Known gaps / TODO (next on the core)
- Unmapped MMIO @ 0xA413FEC0 polled in a loop — add a stub (a delay/standby reg?)
  so the wait completes. Map the rest of the 0xA4xxxxxx peripheral space.
- `div1` real algorithm (division-heavy code will misbehave until then).
- FPU ops are no-ops (fine until graphics/math). MMU/TLB not modelled (P0 used 1:1).
- Interrupts not delivered yet: implement INTEVT + the 0xFD8004D0 table dispatch
  (we decoded the handler @0x80001A54) so TMU/KEYSC IRQs can fire → keyboard.
- VRAM→LCD: once boot reaches the main loop, model R61524 + DMAC ch2 enough to
  capture a framebuffer (then render 396x224 to a PNG = first "it's alive" frame).

## Go port (emu_go/) — 2026-06-02, ~1000x faster, test-validated
Python was the bottleneck (~45k instr/s). Ported the core to **Go** (emu_go/: memory.go,
mmio.go, cpu.go, main.go) — faithful port of the Python core. Measured **~64 M instr/s**
(40M-instr boot-to-alive in 0.62s vs ~10 min in Python). Run: `go -C emu_go run . [maxIns] [timerPeriod]`.

### Test harness (Python = oracle; Go held to the same frozen contract)
Goal: validate the Go port via `go test`, not ad-hoc runs. Two layers, both generated
from the reference Python emulator and frozen to disk (committed):
1. **Instruction conformance** — `emu/conformance_gen.py` defines 53 curated edge-case
   cases (carries/overflow, div1 x32, shad/shld incl -32, signed/unsigned cmp, mul/dmul
   sign, sign-ext loads, SR bank-swap, delayed branches, interrupt entry) and freezes
   inputs+expected into `emu/conformance.json`. Consumers: `emu/test_cpu.py` (Python
   regression) and `emu_go/conformance_test.go` (Go). Both: 53/53 pass.
2. **Whole-program golden boot trace** — `emu/gen_golden.py` -> `emu/golden_boot.bin`
   (full CPU state every 1000 instr over the first 2M of the flash_full boot). Go test
   `emu_go/golden_test.go` reproduces all 2000 checkpoints exactly.
Workflow: change cpu.py -> `python emu/conformance_gen.py && python emu/gen_golden.py`
to refreeze, then `go -C emu_go test .` proves the Go port still matches.

## Render-gate hunt — OS REACHES MAIN IDLE LOOP but shell doesn't draw (2026-06-02)
Major finding: the OS boots ALL THE WAY to its main event/idle loop. Go stack-dump at steady
state (emu_go main.go) shows the call chain returns through **0x802af470** = the system
idle/event loop (the one that polls PERIPH_IRQ 0xA4610088 and runs the keyboard scan). It
parks in a leaf ETMU busy-delay `FUN_803742f8`: `while ((*0xA44D00D8 - *0xA44D00D8)&0xffffff < 0x21)`.
3.60 Ghidra (rebased to 0x80000000, decompiler working) identified the surrounding subsystem:
a **flash translation layer with ECC** — 40-byte records (0x25 data + 3-byte checksum), per-record
hash FUN_803717b8, ECC verify+single-bit-correct FUN_80371718 (ret 1=clean,2=corrected,3=bad),
block reader FUN_80370ff0 (reads 0x1000 page + 0x28 record via vtable *DAT_80371234, returns -5
on ECC fail), driven by a fn-ptr table @0x80371234.
RULED OUT for the blank screen: runtime (ran 500M instr in Go @85M instr/s, VRAM flat at ~96px),
FPU (fpu_ops==0 through boot-to-idle — render path uses no FPU), and the modeled peripherals.
CONCLUSION: only ONE DMA push to the LCD ever happens (the initial screen-clear, SAR=0x0c028800);
the shell never pushes a menu frame -> it reaches the input-wait idle loop WITHOUT drawing the menu.

### Flash-FTL hypothesis TESTED & DISPROVEN (2026-06-02, emu_go ftl probe: `go run . N 30000 ftl`)
Hooked the FTL returns over boot: **block_read FUN_80370ff0 = 366 calls, ALL return 0 (OK);
ECC_verify FUN_80371718 = 1916 calls, ALL return 0 (clean match)**. So flash is read correctly
and every record passes ECC — menu resources DO load. Flash is NOT the gate.
Also: DRAM-wide framebuffer scan (main.go report) finds the densest 396x224x2 window only ~22%
non-zero — NOT a rendered menu (a real menu screen is a near-white ~95%+ field). So the menu was
**never drawn anywhere**, not "drawn but not pushed".
ELIMINATED so far: runtime (500M flat), FPU (fpu_ops==0), modeled peripherals, flash-FTL/ECC,
draw-but-no-push. REMAINING: the shell reaches SYSTEM IDLE without launching/drawing the menu app
-> a higher-level app-launch / event-trigger gate. Idle call-chain entry points to investigate in
Ghidra 3.60: 0x802aebc6, 0x801e523c, 0x8018c1ea, 0x801de5cc, 0x801e3712, 0x801e5f06 (and the
idle/event loop near 0x802af4xx). Likely next: find the menu-app launch decision & why it's skipped,
or diff against casio-emu's known-good boot. Possible suspects not yet modeled: RTC, an event the
boot posts, or a boot-mode/held-key check.

## Validation strategy
Cross-check instruction effects against Ghidra decompiles and, later, against
Heath123/casio-emu (`os` branch). The trace's MMIO log is the primary oracle vs
RECON_NOTES.md.
