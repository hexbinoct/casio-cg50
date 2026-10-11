# CG50 Internals — the documentation site

A HackSpire-style site about the fx-CG50 seen from software: memory map, CPU quirks,
peripherals, OS 3.60 internals. Built with MkDocs Material from `wiki/docs/`. Started
2026-10-08 (office).

## Build and preview

| | Office (Windows) | Home (Mac) |
|---|---|---|
| install once | `python -m pip install -r wiki/requirements.txt` | `python3 -m pip install -r wiki/requirements.txt` |
| preview | `python -m mkdocs serve --livereload -f wiki/mkdocs.yml -a 127.0.0.1:5817` | same with `python3` |
| check | `python -m mkdocs build --strict -f wiki/mkdocs.yml` | same with `python3` |

- Versions are pinned (`mkdocs==1.6.1`): the Material team warns MkDocs 2.0 will break it.
- `--strict` fails on any broken link or `#anchor` (the `validation:` block in `mkdocs.yml`).
  Run it before every commit.
- On Windows the plain `serve` sometimes misses file changes: use `--livereload`, or restart.
- Output goes to `wiki/site/` (git-ignored).
- Dark theme by default; colours in `docs/assets/extra.css`. A browser that toggled before
  remembers its choice in localStorage.

## Publishing

Live at **https://hexbinoct.github.io/casio-cg50/** (since 2026-10-09). The workflow
`.github/workflows/docs.yml` builds `wiki/` with `mkdocs build --strict` and deploys it to
Pages on every push to `main` that touches `wiki/` (or by hand: Actions → Docs site → Run
workflow). The repo is **public**, so Actions are fine under the no-Actions-on-private rule.
Pages' source is "GitHub Actions" (set once). A broken link fails the build and the live site
stays as it was: check the run in the Actions tab after pushing.

## Rules for writing pages

1. **Evidence boxes, exact titles:** `!!! success "Verified on hardware"` (a probe on a real
   calculator), `!!! info "Verified in the emulator"` (the OS/add-ins only work this way and a
   test holds it), `!!! warning "Unconfirmed"` (read from code, or observed informally).
   `!!! danger` for things that can hurt a calculator.
2. **Check every fact against the current emulator code and the 3.60 image, never copy from
   `RECON_NOTES.md`.** The notes are a journal: conclusions were overturned later. The
   "Study findings" section at the end of the notes was made on the **3.80 updater image**;
   its OS addresses must be re-checked in 3.60. Already found wrong in the notes:
   - KEYSC is `0xA44B0000` (the notes' `0xA4080000` is the INTC);
   - the notes' "interrupt system" (VBR `0x800014D4`, table `0xFD8004D0`) is the **boot code's**
     dispatcher. The OS 3.60 uses VBR `0x80020F00`, tables `0xFD8010C8`/`0xFD8012C8`;
   - `0xFF000024` is EXPEVT, not a "hardware strap";
   - nothing in 3.60 uses `0xA4130000` (the "free-running counter"); ETMU is `0xA44D0030`+;
   - the frame push uses DMA channel **0**, not 2;
   - the 3.60 syscall table is at `0x80687CD0` (`re/syscall360.py`), not 3.80's `0x806A2014`.
3. Tools for checking 3.60 without Ghidra: `re/sh4dis.py --image os/flash_dump/os.bin A B`,
   `re/syscall360.py <num>`, `re/boot_compare.py`, `re/irq_scan360.py`,
   `re/irq_ilram_check.py`, `re/find_timer_refs.py`, `re/wiki_keysc_check.py`.
4. All OS addresses are OS 3.60 runtime addresses (`0x80xxxxxx`).
5. No firmware bytes, no long disassembly listings, no screenshots of the OS UI.
6. Plain English, short sentences, explain terms, tables for registers. End each page with
   "In the emulator" (source files + tests).
7. Never ask the user to run a probe that sleeps or touches the CPG (one reset the calculator).

## Pages

Done (2026-10-08): Home, Memory map, CPU, Interrupts, Keyboard, Display, Timers, Battery ADC,
BCD ALU, Boot and power-off, How facts are verified. **Completed 2026-10-09** (the audit's gaps):
CPU (no FPU, register banks, exception vectors + the OS's error screen, delay slots, the two
`sleep` paths, In the emulator), Memory map (flash layout, **fls0 starts at physical
`0xC80000`**, VRAM, RAM variables, the six `0xFE200000` blocks, In the emulator), Boot
(main loop, power-off = a `sleep` inside PowerOff `0x802AEA24`, restarts and the restart flag
`0xFD8017DC`, first-boot setup's four screens, In the emulator), Display (panel off/on and the
probable backlight bit `0xA405012E` bit 5), Interrupts (`0x620`/`0x640` = IRQ1/IRQ2 pins,
`0xA20` = USB, `0xC00` = SCIF; `0xF00` still unknown), Keyboard (`+0x1A`/`+0x1C` values,
single-key check through port pins), Timers (idle path chosen by the USB pin `0xA4050162` bit 1,
FRQCR decoded, the `0xA44C0000` unit), Verification (probe table: source, results, facts).

Planned, not yet written (in suggested order; sources in brackets):

1. **MMU and add-in launch** — UTLB, `ldtlb`, TLB-miss handler `0x8002C918`, add-in code
   paged in from flash, what the OS clears at each launch (RECON cont.18q, the RAM survey in
   the cont.18z RESUME block; `emu_go/mmu.go`, `mmu_test.go`).
2. **Flash and storage memory** — NOR command sequences, DQ7/DQ3 polling, why the flash
   routines run from IL RAM, fls0, 32 MB layout, where fls0 starts (RECON cont.6/7, cont.18w;
   `emu_go/memory.go` flash section, `flash_test.go`, `fls0_trace_test.go`).
3. **Syscalls** — the 3.60 table and how to resolve a number; link WikiPrizm for the list
   (`re/syscall360.py`).
4. **Number format** — the BCD number representation (e.g. 98765 = `10 49 87 65`) and the
   formatter chain to the screen (RECON cont.17b/18b/18c).
5. **Writing probe add-ins** — fxSDK in Docker (`tools/fxsdk-docker/`), fxlink capture (the
   macOS "data overrun" fix: use a named transfer), what is safe (memory note
   `calc-probe-safety`), the probes in `tools/` and results in `os/devic_probes/`.
6. **Open questions** — collect every "Unconfirmed" item worth a probe: size of `0xFE200000`,
   where fls0 starts, FPU or not, what `0xFF000024` reads at reset, `0xFF2F0004`, KEYSC
   latching, `+0x12`/`+0x1E`, ADC conversion time in sleep, R64CNT rate, the `0xA44A0000`
   unit's clock, devices behind INTEVT `0x620/0x640/0xA20/0xC00/0xF00`.
7. Maybe: **UBC** as its own page (now a section of CPU), **DMAC**, **CPG/clocks**, **PFC**.
8. User's call: a **firmware** page (how the 3.80 updater is packed). Not written on purpose:
   it is effectively instructions for extracting Casio's firmware.

Still open in existing pages (needs hardware, or more tracing):

- BCD ALU: link to the Number format page once it exists.
- Memory map: what the `CASIOMEMDATA` blocks at `0xB80000` hold (code near `0x802AF800`); the
  real size of the `0xFE200000` blocks (a read-only stepped dump).
- Boot: what power-off writes to flash `0xB80000`-`0xB9FFFF`; what triggers the three restart
  routines (`0x8014EEA8`, `0x8035F4DC`, syscall `0x11ED`); `0x8035FCE8`, `0x801504DA`.
- Interrupts: the device behind `0xF00`; bit 5 of `0xA4150020`.
- Keyboard: `+0x0E`, `+0x16`, `+0x18`, `+0x1E`, PFC byte `0xA40501C6`.
- Timers: the clocks of `0xA44A0000` and `0xA44C0000`; FLLFRQ (`0xA4150050`, a CPG **read**:
  the user's call).
- Safe probes worth running: read `0xA4050162` with/without USB cable (which sleep path a real
  calc takes); read `0xA4140024`; read `0xA405012E`; KEYSC `+0x1A`=`+0x1C`=1 test with the key
  interrupt masked; an unaligned read and an illegal opcode (error-screen text); one DSP `F…`
  opcode; `mov.l @(disp,PC)` in an `rts` slot.

## TODO: emulator issues the research found

Left for a later session on purpose (user, 2026-10-08). Fixing any of them means changing
`emu_go/` (and `emu/` where the oracle has it): regenerate goldens and keep `go -C emu_go test .`
green, per CLAUDE.md. Then update the matching wiki page's "In the emulator" section.

**Fixed 2026-10-09** (both cores where the oracle has it; conformance 86/86, goldens regenerated,
boot golden unchanged; tests in `emu_go/fidelity_test.go`):

- Exceptions: undefined opcode → EXPEVT `0x180`, slot illegal → `0x1A0` (SPC = branch),
  `trapa` → `0x160` + TRA; with SR.BL = 1 still a halt (the chip resets).
- Missing instructions: `movca.l` and the GBR byte ops `tst.b/and.b/xor.b/or.b` were silent
  no-ops; `mac.w` (S = 1 saturation per the manual, unverified), `movua.l`, `movli.l`/`movco.l`.
- `setSR` banks follow RB only with MD = 1.
- IL RAM mirrored every 16 KB.
- INTREQ00 (`0xA4140024`) reads 0 (was `0x40`, "key-scan ready" = a misreading), writes 0-clear.
- FRQCR starts at `0x0F011112`, KICK reads 0.
- KEYSC `+0x1A`/`+0x1C` column/row enables (1/1 = AC/ON only, as while off); `+0x12` reads 2.
- Panel dark when R007 bit 8 or PFC `0xA405012E` bit 5 is clear (power-off was white).
- Stale comments: DMA channel 0 (`lcd.go`, `mmio.py`), key repeat (`keysc.go`), fls0 start
  (`memory.go`, `tools/flash_dump/dump.c`); `0xA44C0000` labelled "A44C" (was "PORTL").

**Still open:**

- `F…` (DSP) opcodes counted and skipped in both cores: an add-in using them computes wrong
  results silently. Modelling the SH4AL-DSP instruction set is its own project.
- SR bit 12: the calculator shows it set in an add-in; the emulator never sets it. Unknown
  whether it is fixed at 1 or set by some routine (the OS start-up loads SR = `0x500000F0`).
  Settle with an add-in probe: `ldc` a value with bit 12 clear, `stc` it back (safe).
- KEYSC single-key check (syscall `0x127E`, port pins `0xA4050136/38` → rows `0xA405016C`):
  needs the port-column → key mapping; a probe reading `0xA405016C` while driving one column
  with a key held would give it.
- The USB pin (`0xA4050162` bit 1) reads 0, so the OS always takes the deeper sleep path.
  The real level without a cable is unknown (read it from an add-in, with and without cable).
- Nothing models `0xA44C0000` (unknown unit); IRQ pins / USB / SCIF raise no interrupts
  (correct with nothing plugged in).
- All 2 MB from `0xFE200000` is RAM (the six blocks' real sizes need a stepped dump).
- A stripe push with y1 > 0 sets DAR = `0x14000000` + 1536·y1; `mmio.go` streams to the LCD
  only when DAR == `0x14000000`, so such pushes would bypass the panel. Unknown whether the
  OS ever does it.
- Interrupt levels: the emulator raises `0x560` at 8 (OS programs 12) and the RTC `0xAA0` at
  9 (OS programs 8).
- KEYSC: key-data words are live, the hardware seems to latch the last scan; `+0x1E` not
  modelled.
- Battery ADC: `+0x88`/`+0x8A` merged (separate on hardware), no `+0x90` mirror, `+0x8E` not
  modelled.
- The emulator's FRC region at `0xA4130000` (64 KB, also covers the RTC page) is a
  leftover from the 3.80 bring-up.
- Found 2026-10-10/11 by the add-in work (Wolf3D, FastGraph, CodeView):
    - CPG `PLLCR` (`0xA4150024`) reads `0x4000`: PLLE but no FLLE (bit 12). On the calculator
      the FLL always runs, and gint's `clock_freq()` computes Pφ = 29.49 MHz there. In the
      emulator it gets about 65 kHz, so every gint timer delay given in µs is wrong. A fix
      changes what the boot sees, so the Python oracle and the goldens need it too.
    - `Bdisp_PutDisp_DD_stripe` (syscall `0x0260`) for rows 12-203 shows garbled and
      blue-tinted on the panel, while VRAM is correct. The full `Bdisp_PutDisp_DD` (`0x025F`)
      is fine.
