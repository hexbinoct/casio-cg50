# fx-CG50 → Android emulator — recon notes

<!-- ============================================================================
  NEW SESSION? READ THE "⏯ RESUME HERE" BLOCK DIRECTLY BELOW *FIRST*.
  It is the current state + the next task with exact entry points. Each session
  appends a new "⏯ RESUME HERE (last session end: …contNN)" block at the TOP; the
  most recent one is authoritative, older blocks below are history. When you finish
  a session's work, ADD a new RESUME block here summarizing what changed + what's
  pending, so the next session can continue without being reminded.
============================================================================= -->

> ## ⏯ RESUME HERE (last session end: 2026-10-04, cont.18z = on-device debugger stage 0, home Mac)
>
> **Session in one paragraph:** the user chose approach A for the on-device debugger
> (`cg50_addons/notes/debugger.md`: a resident monitor + the UBC, in stages). **Stage 0 is done
> here:** the **UBC + DBR are modelled** (below) and a **free-RAM survey** found **DRAM
> 0x0C4E0000–0x0C7FFFFF (3200 KB) never written or read** by the OS, any app, add-in launch or the
> boot (power-up noise in the real June dump too): the monitor's home. Stage 1 (DASM breaks in its
> own code and shows the registers) then ran in the emulator **and on the real calculator** (user,
> 2026-10-04: "it worked great"); a crash the user hit there (stepping into gint's
> `ubc_disable_channel` → the break handler re-broke on itself → stack ran through all OS RAM →
> reset to the language screen) **reproduces exactly in the emulator** (the model behaves like the
> hardware) and is fixed on the DASM side. Not committed yet.
>
> **cont.18z — UBC (User Break Controller) + DBR.** `emu_go/ubc.go` (header = the full model),
> 0xFF200000, the SH7724 layout of gint's `<gint/mpu/ubc.h>`: 2 channels (CBR/CRR/CAR/CAMR, ch1
> CDR1/CDMR1/CETR1), CCMFR, CBCR. Break before/after an instruction fetch (delay-slot rules: before
> a slot = before its branch; after a branch or slot = at the target; a not-taken `bt/s`/`bf/s` runs
> its slot inside the same UBC step), operand watchpoints (logical address, size, R/W, ch1 data
> value; the break is taken after the instruction; DMA not seen), CAMR masks, ASID (AIE),
> sequential flags (MFE/MFI), ch1 execution count; break = SSR/SPC/SGR, MD/RB/BL=1, EXPEVT 0x1E0,
> PC = **DBR** if CBCR.UBDE else VBR+0x100. No match while SR.BL=1 or MSTPCR0.UDB (bit 17) stops
> the module. **Matches are decided at the instruction's fetch:** a break-after fires even when
> that instruction disables its own channel (real calculator, 2026-10-04: DASM single-stepped 79
> times through its own `CBR1 = 0` and on into `dbg_done`; the emulator, deciding after execution,
> stopped at step 19 — now 79 too). A before-break re-fires after the handler's rte (debuggers step over it first, like
> gint's GDB stub). **Cost: zero while no channel is enabled** (`CPU.ubcOn` selects `stepUBC`;
> the operand watch is a bool test at the top of `Memory.Read/Write`; MenuHold benchmark = HEAD
> within noise). New instructions `ldc/stc(.l)` **DBR** and **SGR** in Go + the Python oracle
> (conformance 70/70: `ldc_stc_sgr_dbr`, `ldcl_stcl_sgr_dbr`; boot golden byte-identical). DBR is
> saved as a 37th word of the counted register block (older states → 0, older builds ignore it);
> the UBC registers are a region map in the MMIO section. The OS never touches the UBC (its one
> pool hit for 0xFF200000 is an unrelated constant) and never executes DBR instructions.
> Tests `ubc_test.go`: before/after, delay slots, not-taken bt/s, vector without UBDE, BL/UDB/BIE
> gating, operand watch (data value, read vs write, size), execution count, save-state.
> **Hardware facts confirmed by the real calculator:** the OS leaves the UBC stopped
> (MSTPCR0.UDB=1) at add-in start, so gint's driver powers it on and sets DBR = its `ubc_dbh`
> (DASM showed "DBR 0x0030d77c UBDE 1", same as the emulator). **SR bit 12 is set (0x40001101)
> on the real CPU: the SH7305 is SH4AL-DSP-like and bit 12 is the DSP bit** — don't treat SR's
> "reserved" SH-4A bits as reserved (a mask I tried this session was wrong and was reverted).
> **Free-RAM survey:** `emu_go/ramfree_probe_test.go` (tag probe) + a nil-checked
> `Memory.accHook` (memory.go); full findings in the next paragraph.
>
> **Debugger stage 0 — which physical RAM stays free while the OS and gint add-ins run (2026-10-04).**
> Probe `emu_go/ramfree_probe_test.go` (tag `probe`) + a nil-checked `Memory.accHook` (memory.go:
> every DRAM read/write by physical address, every access at 0xE0000000.. (IL/XY/OC RAM, P4
> address), DRAM instruction fetches and DMA source reads; off = one nil test on those paths).
> One 3.5 G-instruction session on the 32 MB dump from `cg50_state_32mb.bin`, 64-byte blocks, a bit
> per phase + first reader/writer PC: MAIN MENU idle (2 G) → Run-Matrix (2^100, sin, ln, a big
> product, OPTN) → Graph (Y=X², DRAW) → Statistics (list + 1-VAR) → Python (MicroPython shell,
> 2**1000, 1/7) → Casio's Geometry and 3D Graph add-ins → `InstallAddin` of the current DASM build
> (the OS writes the file) → DASM (Conv.g3a: listing, VARS, X,θ,T, hex; OS ROM: VARS = the whole-ROM
> analysis, references; khicas50.g3a 2 MB: VARS via BFile) → MAIN MENU idle with DASM suspended →
> Upsilon (Calculation: 1+2, 2^64, sin 1) → Upsilon idle. Plus `RAMFREE_SESSION=warm`: a warm
> power-on from reset to the MAIN MENU. Add-in launches are their own phases (`X-launch` = the OS
> until pc = 0x00300000). Run: `TMPDIR=<dir> go -C emu_go test -tags probe -run 'TestRamFree$'
> -count=1 -v .` (~90 s; maps + meta.json in `<dir>/ramfree`). Cross-checked against the **real
> calculator's** June dumps `os/flash_dump/dram.bin` / `ilram.bin` (taken while FlashDump, a gint
> add-in, ran).
>
> **DRAM (0x0C000000, 8 MB) — the answer: 0x0C4E0000–0x0C7FFFFF (3200 KB) is never written and
> never read** — not by any app, add-in launch, Python, file install, DASM, Upsilon, nor the boot.
> On the **real calculator** that range holds DRAM power-up noise (every 64 KB: ~6.6 bits/byte
> entropy, no 0x00 bytes at all, ~11% 0xFF, bits biased to 1), i.e. nothing ever wrote it there
> either, and the boundary is exactly 0x0C4E0000 in both (the dump goes from all-zero to noise at
> that address). It holds a monitor (KBs) *and* a 512 KB copy of DASM's RAM several times over.
> Everything below it is the OS's:
> - **Every app launch, built-in or add-in, rewrites ~4.4 MB**: 0x0C0E0000–0x0C4DFFFF fully, plus
>   much of 0x0C000000–0x0C0DFFFF. 0x0C1E0000–0x0C4DFFFF (3 MB) is zero-filled by the OS at each
>   launch (first writer pc 0x80362FF2; all 0x00 in the real dump too) and then left alone while a
>   gint add-in runs: scratch space *after* the target is launched, never across a launch.
> - 0x0C160000–0x0C1DFFFF (512 KB) = the add-in RAM (below); 0x0C0F0000–0x0C147800 = gint's `_ostk`.
> - The other never-written DRAM ranges in the session are OS data the **boot** initialises
>   (0x0C028840–0x0C04CEBF 145 KB, 0x0C06D940–0x0C08B6FF 119 KB, 0x0C0D8980–0x0C0E2FFF 41 KB,
>   0x0C0CEE00–0x0C0D1DFF 12 KB, + ~70 ranges ≤ 7 KB; mostly 0x00) — unusable (other apps may use
>   them). Untouched by session *and* boot, besides the 3200 KB: only 0x0C048BC0–0x0C04A57F (6.4 KB),
>   0x0C0D2000–0x0C0D35BF (5.4 KB), 0x0C04CA40–0x0C04CE7F (1 KB), all zero — not worth the risk.
> - While gint add-ins run (after launch), never written: 0x0C1E0000–0x0C7FFFFF (6272 KB, never read),
>   the free tail of `_uram` 0x0C1A8AC0–0x0C1DBFBF (205 KB) and of `_ostk` 0x0C11B600–0x0C1477BF
>   (176 KB) (DASM's usage; a heavier target can allocate them), 0x0C147800–0x0C1600FF (98 KB).
>
> **On-chip memories.**
> - **OS "IL RAM" 0xFD800000: really 16 KB, mirrored every 16 KB** (ilram.bin repeats with a 16 KB
>   period; the emulator models 64 KB). The OS writes ~5 KB in 0xFD800000–0xFD8024BF (its flash
>   routines, 4 KB at 0xFD800000, recopied at every app launch by pc 0x801DF4B4; variables); the
>   boot uses 0xFD803FC0–0xFD803FFF as a stack. Never touched in the session: **0xFD8024C0–0xFD803FBF
>   (6.8 KB)**, all 0x00. In the real dump that range is zero except 0xFD8026A0–0xFD8026BF (32 B) and
>   0xFD803F40–0xFD803FFF (192 B, a deeper boot/standby stack than the emulator's): **usable ~6 KB at
>   0xFD8026C0–0xFD803F3F**. gint never touches 0xFD80xxxx, it is P4 (no MMU, survives every world
>   switch): the natural home of a DBR entry stub that jumps to the monitor in DRAM.
> - **X/Y memory 0xE500E000–0xE5011FFF (16 KB) and IL memory 0xE5200000–0xE5200FFF (4 KB)** (gint's
>   `xyram`/`ilram` linker regions): the OS never touches them (no access in any OS phase or the
>   boot); gint takes them: DASM's build memsets all 20 KB at start (`memset_align`, pc 0x30DC2E),
>   reads the DMA fill pattern at 0xE5200000 (`dma_transfer_async`) and runs its interrupt-callback
>   trampoline there; Upsilon writes 0xE5200000–0xE520007F. **Not usable** by a monitor that must
>   survive a gint target. (The rest of the emulator's flat 0xE5000000–0xE520FFFF model is not real
>   memory.)
> - **0xFE200000 "OC RAM"** (emulator: 2 MB): the OS writes 0xFE200000–0xFE227FFF (160 KB) at every
>   app launch + 0xFE240000–0xFE24007F, 0xFE241000–0xFE2411BF; the boot writes ~590 KB more
>   (0xFE24xxxx–0xFE3Cxxxx); never touched by either: e.g. 0xFE28C000–0xFE2FFBFF (463 KB),
>   0xFE228000–0xFE23FFFF (96 KB) and the same offsets +1 MB (0xFE328000, 0xFE36A000: the boot's
>   pattern repeats per MB — a mirror?). **The real size/existence of this memory is unknown** (no
>   real dump): not a candidate until dumped on the calculator.
>
> **Add-in mappings (UTLB) while DASM / Upsilon / Casio's add-ins run:** RAM 0x08100000–0x0817FFFF =
> 8 × 64 KB entries (slots 55–62, ASID 0, not shared) → phys **0x0C160000–0x0C1DFFFF, the same 512 KB
> for every add-in**; slot 63 = VPN 0 → phys 0 (64 KB); code 0x00300000.. = 4 KB pages demand-mapped
> from the OS table 0x8C04CF0C straight onto the .g3a file in flash (fragmented by the FTL: DASM
> 0x00300000 → 0x00FE2000 … 0x0031A000 → 0x0132F000; Upsilon 0x00300000 → 0x019D1000; Geometry
> 0x00300000 → 0x01D70000), read-only. So a debugger cannot keep DASM's RAM in place while the target
> runs: copy it out (0x0C4E0000+ has room) and back.
>
> **gint 2.11 placement (DASM, symbols from its ELF):** VBR = 0x8C160000 (start of the add-in RAM;
> handlers at VBR+0x100/0x400/0x600 in the 5 KB before .data/.bss at 0x08101400); `.bss/.data` up to
> `_euram` (0x0C1686E0 in this build); **`_uram` arena 0x8C1686E0–0x8C1DC000 (462 KB, phys
> 0x0C1686E0)** — DASM: 7 live blocks, peak 10, 405 KB allocated in total (744 KB in another
> session); **`_ostk` arena 0x8C0F0000–0x8C147800 (350 KB, phys 0x0C0F0000, "the OS stack")** holds
> the **only VRAM: 0xAC0F00A0, 396×224×2 = 177 KB (vram_1 = vram_2: no triple buffering)** — Upsilon's
> LCD DMA source is the same 0x0C0F00A0; world buffers `gint_world_os/addin` at 0x0C168744 /
> 0x0C1688D0 (in `_uram`); stack: the top 16 KB of the add-in RAM 0x0C1DC000–0x0C1DFFFF
> (`gint_stack_top` = 0x8C1DC000 = end of `_uram`; r15 at 0x8C1DFEB8 in DASM, 0x8C1DFE10 in Upsilon).
>
> **Recommendation:** monitor code + data at **0x8C4E0000+** (P1; or 0xAC4E0000 uncached), a 512 KB
> save area for DASM's RAM right after it, a small DBR entry stub in IL RAM **0xFD8026C0–0xFD803F3F**
> if it helps. Before relying on it on hardware: a probe add-in that checks 0x8C4E0000–0x8C7FFFFF is
> still "noise"/unchanged across a real session (or a canary + CRC kept by the monitor), and a
> FlashDump-style read of 0xFE200000–0xFE3FFFFF to settle the OC RAM question.
> **Caveats:** the emulator run is not exhaustive (no eActivity, Spreadsheet, Picture Plot, Physium,
> E-CON, Memory manager, USB link / mass storage, big Python programs or a full Run-Matrix memory,
> OS update); other OS versions may differ (3.60 only); other add-ins may use the upper RAM on purpose
> (non-gint add-ins can reach physical memory through P1/P2; KhiCAS could not be measured: it stops
> with "unable to load ram part khicas50.882", a file this calculator lacks); the real-dump evidence
> is one snapshot (power-up noise proves nothing wrote there since the DRAM last lost power, not that
> nothing ever could); the emulator's zero-initialised DRAM says nothing about real initial contents.
>
> **Later the same day — debugger stage 2 works on the real calculator** (cg50_addons
> `notes/debugger.md`): DASM arms the UBC from inside the OS world and opens the MAIN MENU; the
> next add-in stops at 0x00300000 on a resident monitor at 0x8C4E0000 (SR.BL=1 throughout).
> Emulator additions: `monitor_probe_test.go` (the launch-break experiment: the OS's own
> MSTPCR0 = 0xA3086 has UDB=1 at the menu, but it only read-modify-writes it, so a UBC powered on
> stays on through the launch, and DBR is never touched), `monitor_dasm_probe_test.go` (the full
> flow with Geometry and a gint add-in), `ramchk_probe_test.go`; **an MMU exception taken with
> SR.BL=1 now halts the core** ("the real CPU resets") — it found that our September KeyProbe can
> reset the calculator (OS file code run inside gint's world: the OS flushes the whole TLB at
> 0x8002C842, rebuilds the code-page table, reloads the RAM entries only at the end
> (FUN_8002e60a); a gint timer interrupt in between touches add-in RAM with BL=1).
> **Emulator gap found on the calculator:** KEYSC data words should latch the LAST scan, and the
> unit only scans when its mode/the OS's ISR drive it; the model serves the live matrix (the
> monitor's first version hung on the real calc waiting for the launching EXE to be "released";
> fixed in the monitor by doing synchronous scans the OS's way, 0x801E56FA).
> **NEXT:** (1) model the KEYSC latch (Go + Python oracle + keysc golden); (2) debugger stage 3
> (cg50_addons); (3) commit/push this session's rest;
> (3)-(5) as before: shared Android debug keystore, desktop app, WASM.
> Emulator gaps noticed: a not-taken bt/s/bf/s outside the UBC path still runs its slot as a
> separate step (an interrupt can land in between; only timing, the golden boot depends on the
> current counting); Upsilon can't return to the MAIN MENU in the emulator (MENU/EXIT/AC do
> nothing) — unverified whether that's real.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-10-04, cont.18y + the Upsilon fix, home Mac)
>
> **Session 2026-10-01…04 in one paragraph:** any `.g3a` can be installed into the emulated
> storage by the OS itself (`InstallAddin`), which needed a flash-erase fix; the Android app
> installs add-ins (file picker / Open with / adb), shows the whole 396x224 panel, and its
> save-states survive inside gint add-ins; Upsilon runs (CPUOPM.INTMU). All verified on the
> POCO X3 and committed + pushed (`98a70f2`, `75734dd`, `a6df938`). Details per item below.
> The add-in side (DASM, a planned on-device debugger, "Explain") is in the sibling repo
> `cg50_addons` (`notes/dasm.md`, `notes/debugger.md`): the debugger's stage 0 is emulator
> work in this repo (see NEXT).
>
> **cont.18y — install any `.g3a` (DESKTOP_AND_WEB step 1) + the phone app installs add-ins.**
> **Done, verified:** `Emulator.InstallAddin(name, g3a)` (`emu_go/install.go`) has the *OS itself*
> write `\\fls0\NAME.g3a`. No installer add-in was needed: `oscall.go callOS` borrows the OS's main
> context while it sleeps in GetKey (all registers saved; the call runs with interrupts and devices
> live, returns to the trap address 0xDEAD0000 caught by the HLE hook, then everything is restored
> and the OS sleeps on). Sequence: sys_malloc 0x1F44 → Bfile_DeleteEntry 0x1DB4 →
> Bfile_CreateEntry_OS 0x1DAE → Bfile_OpenFile_OS 0x1DA3 (2=WRITE) → Bfile_WriteFile_OS 0x1DAF in
> 32 KB chunks → close → syscall **0x0017** = FUN_8002d1a4 (scan `\\fls0\*.g3a`, rebuild the add-in
> table 0x8C0CB878; ≤ 99 add-ins, ≤ 2 MB, header checksum at 0x20 must equal the file's last 4 bytes).
> **The MAIN MENU counts its icons only when entered** (FUN_8036427a → FUN_8036421e), so at the menu
> (flag byte **0x8C0A3043** == 1, OS 3.60; fingerprinted via the literal at 0x80364448) the installer
> taps `1` (Run-Matrix) *before* writing and MENU after (`tapAndSettle`): the fresh menu shows the
> icon, and an add-in suspended under the menu (MENU inside an add-in keeps it mapped, MMU on) is
> ended first, so even its own file can be replaced. Inside a running add-in: refused (errNotIdle).
> **Emulator bug found + fixed (flash):** sector erase was instant and 64 KB. The OS's erase routine
> (IL RAM 0xFD800BB6 → 0xFD800CA8) requires a status read with DQ7=0 then DQ3=1 right after the 0x30
> command, then polls DQ7 (0xFD800D0E); an already-finished erase = -3 → FTL GC fails (-7/-12) →
> Bfile_WriteFile_OS -31. Now (Go `memory.go` + Python `emu/memory.py`): **128 KB sectors** (the OS
> masks 0xFFFE0000) and `eraseStatusReads`=4 data reads of the erasing sector return status (DQ7=0,
> DQ6/DQ2 toggle, DQ3=1). Oracle transcript `emu/flash_selftest.py` → `emu/flash_golden.txt`, replayed
> by `TestFlashGolden`; `TestFlashSectorEraseStatus`. Earlier "fls0 writes work" (first-boot wizard)
> only programmed, never erased.
> **Tests:** `install_test.go` (32 MB image; skips without it): install → read back via Bfile byte-
> identical, menu icons +1, the add-in starts from its icon; reinstall replaces; over a suspended DASM
> (icon Z). Fixture: our own `testdata/aluprobe.g3a`. Full suite green (24 s Mac).
> **Android:** `EmuInstallAddin` (android_bridge.go, returns NULL or an error string) → JNI
> `installAddin` → `NativeBridge.installAddin` (+ `busy` flag: the keypad skips native calls while the
> machine is locked). MainActivity: prefers `flash_full_32mb.bin` + `cg50_state_32mb.bin` (EmuInit
> seeds the warm-boot flag for >16 MB images), long-press → **Install add-in…** (SAF OpenDocument),
> VIEW/SEND intent filters (application/octet-stream), `--es install NAME` (file in the files dir),
> saves the state after each install. **Verified on the Mac's Android emulator** (AVD
> Medium_Phone_API_36.1, arm64) with the real 32 MB dump: adb install (~5 s round trip), picker
> install over a suspended DASM, icons persist across force-stop. **Then on the POCO X3** (Mac paired
> for wireless debugging; serial `192.168.100.3:38977`): adb install of DASM in 3.7 s, new icon on
> the MAIN MENU, launches. The office-signed APK had to be uninstalled first (different debug keys
> per machine); the phone's old files are backed up in git-ignored `os/phone_backup_2026-10-01/`
> and were pushed back. "Open with"/Share still untested on a device.
> **Mac Android build now works:** `python3 android/build_go_lib.py` (new, cross-platform; finds the
> NDK), `android/local.properties` (`sdk.dir`, git-ignored), Android Studio's JBR as JAVA_HOME;
> `re/phone.py` runs on both machines (`CG50_SERIAL`, new `install X.g3a` and `firmware` commands).
> **Same session, phone fixes (user: "the last line in DASM is hiding"):**
> - **Whole panel on the phone:** gint add-ins draw 396x224; the OS uses panel columns 6..389, rows
>   0..215 (H = 389 - x, V = y) and paints the rest with **DrawFrame** (syscall 0x02A8 →
>   0x800561EE: 6 columns left/right + 8 rows at the bottom in the frame colour, IL RAM 0xFD8019E8,
>   FrameColor 0x02A3, default white). `FramebufferRGBA`/`EmuWidth`/`EmuHeight` are now the whole
>   panel (`lcd.renderPanel`, `PanelWidth/Height`); the Android view keeps 396:224. Desktop
>   (`FramebufferRGB565`, webui, savePNG) still shows the OS's 384x216.
> - **DMA fill bug:** DrawFrame fills via DMAC ch0 with a *fixed* 32-byte source (CHCR 0x00100400,
>   SM=0, FUN_800562A2); LCD-bound DMA always streamed an incrementing source → junk in the frame.
>   Fixed in `mmio.go` (fixed source repeats; a fill is not counted as a frame push).
>   `TestLCDDMAFill`.
> - **Save-states inside a gint add-in were dead** (the app snapshots on every pause): timers were
>   restored as registers only (channels stopped, so gint's ETMU5 key scan never ran) and the
>   X/Y/IL memories weren't saved. Now: timer channel state in the region maps (`timerKey`
>   0xF0000000|ch<<8|field, gtimer.go), and optional trailing sections **XYRM** (xyram) and **GRAM**
>   (the panel, so a resume shows exactly the screen) after MMIO; older states still load
>   (no GRAM → seeded from VRAM inside the OS frame colour). `TestTimerChannelsSaveState`,
>   `TestResumeInsideGintAddin`, `TestSaveStateRoundtrip` extended. Python oracle untouched
>   (presentation/save-state only).
> - Verified on the POCO X3: DASM full screen incl. the F-key bar; HOME → force-stop → restart
>   resumes inside DASM and keys work. The phone's dead snapshot is kept as
>   `cg50_state_32mb.dead-in-dasm.bin`. Wireless adb serial: use the mDNS name
>   `adb-584c917b-jFkRGG._adb-tls-connect._tcp` (the port changes when the phone sleeps).
> **FIXED (2026-10-02): Upsilon crashed** (user: blank screen, then the first-boot language
> screen). Cause: **CPUOPM.INTMU** (0xFF2F0000 bit 3) was not modelled. gint sets it at start
> (`cpu.c`: "On the fx-CG 50 emulator it is available but ignored", i.e. Casio's fx-CG Manager
> has the same gap) and its callback dispatcher `gint_inth_callback` (IL memory 0xE5200000)
> clears SR.BL relying on IMASK = the accepted interrupt's level. Upsilon sleeps 0 ms early on
> (0x37C1FC: ms*1000 → gint sleep_us → `timer_configure(TIMER_ANY, 0, ...)`), so TMU2 runs with
> TCOR=0 and fires continuously; with IMASK left at 0 the interrupt nested in its own callback
> until the stack (r15 0x8C1A3Exx) overwrote gint's `gint_inth_callback` pointer at 0x08143EC8
> (phys 0x0C1A3EC8) → jsr to 0x40001101 (a saved SR) → TLB miss → gint's panic screen →
> illegal instruction at 0xC6 → core halt. **Fix:** CPUOPM region (Go `MMIOBus.cpuopm`, Python
> `MMIOBus.cpuopm`); `acceptInterrupt` sets IMASK to the accepted level when INTMU is set (Go
> cpu.go + Python cpu.py). The OS never touches CPUOPM, so OS runs/goldens are unchanged.
> Tests: `TestCPUOPMIntmuSetsIMASK`, `TestUpsilonStarts` (32 MB dump, icon S: home screen up,
> keys work, Probability opens). **Verified on the POCO X3** (2026-10-02): Upsilon starts, its
> Calculation app computes 1+1.
> **Practical state (2026-10-04):**
> - Phone: wireless adb paired with this Mac; find it with `adb mdns services` (the port changes
>   whenever the phone sleeps; a stuck adb needs `adb kill-server`) and use the mDNS name
>   `adb-584c917b-jFkRGG._adb-tls-connect._tcp` as `CG50_SERIAL`. The phone's app is signed
>   with the **Mac's** debug key now (an office build won't install over it without uninstalling,
>   which wipes its files: back them up first, as in `os/phone_backup_2026-10-01/`). It holds
>   the 32 MB dump + state, Upsilon and DASM (DASMNEW) installed.
> - Mac Android emulator (AVD `Medium_Phone_API_36.1`, headless: `emulator -avd ... -no-window`)
>   is out of storage (~270 MB free): wipe it (`-wipe-data`) or enlarge it before reuse.
> - Side fact: 0xFF2F0004 is **EXPMASK** (next to CPUOPM), referenced by the OS (0x8002041C).
> **NEXT:** (1) if the user picks approach A for the on-device debugger (`cg50_addons/notes/
> debugger.md`; awaiting their choice): stage 0 here = model the **UBC** (User Break Controller:
> 2 channels, break before/after an instruction or on an operand access) + **DBR** (debug
> handler base), and measure which physical RAM stays free while the OS and a gint add-in run
> (DRAM write-watch over a long session); (2) a shared debug keystore in the repo so office and
> Mac builds install over each other (explained to the user 2026-10-01: commit the Mac's
> `~/.android/debug.keystore`, point `signingConfigs.debug` at it; not asked for yet);
> (3) DESKTOP_AND_WEB step 2: the desktop app (webui.go upgrade + drag-and-drop calling
> InstallAddin); (4) WASM. Small: a desktop CLI `install` mode that writes into
> `cg50_state_32mb.bin`, so probes can use real installs instead of `DASM_SWAP`.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-10-01 cont.18x, home Mac)
>
> **Session 2026-09-28…10-01 in one paragraph:** the repo was cloned to the home Mac (firmware copied
> into the git-ignored `os/` by hand); the full **32 MB flash** was dumped (cont.18w; the 16 MB dump
> missed most of fls0) and boots to the calculator's own MAIN MENU with a **warm boot** (`CG50_WARM=1`);
> the emulator **INTC now gates requests at acceptance** (cont.18x); probes run a fresh add-in build
> from its real menu icon (`dasm_real_test.go`). The sibling DASM add-in gained function detection +
> cross-references (verified on the real calculator). `CLAUDE.md` now covers both machines.
> **NEXT for this repo:** `docs/DESKTOP_AND_WEB.md` step 1 — an *installer* add-in that writes a `.g3a`
> into fls0 through the OS's own BFile (so any build really exists in storage), then the desktop app
> (upgraded `webui.go` front-end with the Android skin/keypad, drag-and-drop install), then the WASM
> build (`GOOS=js GOARCH=wasm` already compiles; firmware stays local in the browser). The held-key
> scrolling speed on the phone (cont.18u) is still open but no longer the main line.
>
> **Direction (2026-09-30):** make the emulator the everyday test device instead of the calculator:
> a good-looking desktop app (calculator skin, drop a `.g3a` to install + run it) and the same app in
> the browser via WASM (`GOOS=js GOARCH=wasm` already builds). Plan + order in
> `docs/DESKTOP_AND_WEB.md`; step 1 = an installer add-in that writes the `.g3a` through the OS's own
> BFile so the file really exists in fls0.
>
> ### ⚡ cont.18x — interrupts are gated again when accepted (not only when raised)
> **Bug:** DASM (cg50_addons) crashed on the emulator after ~1.3 s of whole-file analysis of a 2 MB
> add-in, i.e. thousands of BFile reads = gint world switches: pc 0xf0f0f0f0, jumped to by the OS's
> interrupt entry (0x80021552: handler = *(0xFD8010C8 + (INTEVT-0x40)>>3)) for **INTEVT 0xFA0 = ETMU5**,
> gint's 128 Hz key-scan timer, whose slot in the OS's table is garbage. `MMIOBus.raise` checked the
> INTC gate (priority field, mask bit) only when a request was raised; a request pending at the moment
> gint restored the OS's INTC state (ETMU5 priority 0) was still delivered. Real hardware evaluates at
> acceptance. **Fix (cpu.go):** `gatePending()` — when the CPU accepts (and when a request would wake it
> from sleep), requests whose source now has priority 0 are dropped; masked ones wait (not accepted, do
> not wake) until unmasked. Selection order unchanged (highest level, then INTEVT). `intc.go gate()`.
> Test `TestINTCGateAtAcceptance` (fails on the old code); full suite + goldens green (21 s).
> **Probes:** `dasm_real_test.go` DASM_LAUNCH=1 (start DASM from its menu icon Z on the 32 MB state),
> DASM_SWAP=<g3a> (swap in a fresh build's code pages at launch, UTLB entries for 0x3xxxxx dropped),
> per-key full 396x224 add-in frame (`saveAddinFrame`, from the LCD DMA source), long keys timed,
> DASM_TRACE=<n> (single-step key n, dump the last 256 pc/pr/sp on a fault). `dasm_swap_test.go`
> DASM_KEYS also saves the full frame. The panel screenshots (`savePNG`) show only the OS's 384x216
> window: a gint add-in's first column and its bottom rows are cut there.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-28 cont.18w, home Mac)
>
> ### 🔋 cont.18w — full 32 MB flash + warm boot: the emulator runs the calc's REAL storage
> **Why add-in file reads failed (cont.18v "Open"):** not the emulator. `flash_full.bin` is a 16 MB dump;
> fls0 continues at phys 0x01000000+, so most file clusters were missing (the OS's FTL maps them to
> nothing and fills 0xFF without a flash read; files whose clusters sat below 16 MB — ProbSim, graph3dp,
> text, helloWorld — read fine). **New dump:** `os/flash_dump/flash_full_32mb.bin` (FlashDump streaming
> one 0x02000000 region, sha256 95ffe867…e052; 0..0xB80000 == June os.bin). `flash_full.bin` stays the
> 16 MB image for goldens/suites/`cg50_state.bin`.
> **Why a cold boot of it still ran first-boot setup:** `fls0_open` returns 0 with the full dump. The
> wizard FUN_8035e1be runs from the tail of FUN_80365238 unless `*(u32*)0xFD8017DC == 1` and model
> `*0xFD8018D4 == 0xCA02` (or flash 0xA0000300 is erased). 0xFD8017DC is an IL RAM word the power-off
> routines FUN_801dfe4a / FUN_801504e0 set to 1 (not when it is 2 = setup running); the boot clears it.
> Our cold boot zeroes IL RAM = a dead battery. (The cont.18f "-6 gate" was for the 16 MB image.)
> **Shipped:** `main.go`/`state.go` — env `CG50_FLASH` (image), `CG50_STATE` (save-state), `CG50_WARM=1`
> (seed `warmFlagAddr` = power-on from "off"); defaults unchanged. State for the 32 MB image:
> `CG50_FLASH=../os/flash_dump/flash_full_32mb.bin CG50_STATE=../os/flash_dump/cg50_state_32mb.bin
> CG50_WARM=1 go -C emu_go run . 600000000 30000 provision none` → MAIN MENU with the calc's real icons
> (FlashDump, probes, DASM = icon Z). Web UI on it: same two paths + `go -C emu_go run . 0 30000 web`.
> Probes (tag probe): `fls0_trace_test.go` (call tree + returns under any function: FLS0_FN, FLS0_DEPTH,
> FLS0_IMG), `warm_boot_test.go`, `dasm_real_test.go` (resume the 32 MB state, press DASM_KEYS, one
> screenshot per key), `dasm_swap_test.go` (+ Mac path, DASM_PICK, second frame). Suite + vet green.
> Headless Ghidra on the Mac: project `casio/ghidra_cg50` (3.60 os.bin @0x80000000), `decomp.sh <addr>…`,
> `refs.sh <value>…` (literal-pool refs + the instructions that load them).
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-28 cont.18v)
>
> ### 🧩 cont.18v — gint add-ins run on the emulator (for `september/cg50_addons/dasm`). All suites green.
> **What:** the sibling project's first add-in (DASM, an on-device SH-4A disassembler built on gint 2.11)
> ran into everything the OS never used. Added, each with unit tests, OS behaviour/goldens unchanged:
> · **`gtimer.go`** — ETMU0-5 (0xA44D0030+0x20·i: TSTR/TCOR/TCNT/TCR, 32.768 kHz, TCR bit0 = UNIE,
>   bit1 = UNF; INTEVT 0x9e0 0xc20 0xc40 0x900 0xd00 0xfa0) and TMU0-2 (0xA4490004 TSTR, ch at
>   +0x08+0xC·i, Pφ/4·4^TPSC with Pφ = 29.4912 MHz, TCR bit5 UNIE bit8 UNF; 0x400/0x420/0x440). Counters
>   are computed lazily, underflows are scheduled (`Emulator.nextDue`). The legacy free-counter view of
>   +0xD8/+0xC8 stays until software programs ETMU5 (the golden was frozen with it). gint's driver init
>   writes every register and spins until it reads back — the old stub never echoed, hence the hang.
>   gint's `timer_configure(TIMER_ANY)` picks the highest free id → the keyboard scanner (128 Hz) runs on
>   **ETMU5**.
> · **`intc.go`** — the INTC (0xA4080000) is a real unit now: IPRA-L (+0x00..+0x2C, 16-bit), IMR0-12
>   (+0x80.., write-1-sets), MSKCLR (+0xC0.., write-1-clears), reads return what was written (they read 0
>   before, so the OS's RMWs kept only the last field). Every source goes through `MMIOBus.raise`: requested
>   only with a nonzero priority field and a clear mask bit. Field positions: gint's table for TMU/ETMU/DMAC/
>   RTC; the OS's own reprogramming (probe `TestDumpINTC`) for its tick **0x560 = IPRB[15:12] prio 12,
>   IMR4 bit3** and **KEYSC 0xBE0 = IPRF[15:12] prio 13, IMR5 bit7**; the cold boot sets **RTC IPRK = 0x8000,
>   MSKCLR10 = 3** at 5.3M instr. OS sources keep their old levels; add-in-only sources use the field. The
>   unit is seeded with those OS defaults (a cold boot zeroes and reprograms everything itself; the menu
>   snapshot has no INTC state). gint zeroes/masks all of it at start and restores on the world switch —
>   without this the OS's tick landed in gint's empty vector gate → "illegal instruction at 0xe5200022".
> · **DMAC**: real channel map (SH7724): ch0-3 at +0x20/+0x30/+0x40/+0x50, DMAOR +0x60, ch4-5 +0x70/+0x80
>   (the OS's LCD push is **channel 0 at 0xFE008020**, not "ch2"). Memory-to-memory transfers are performed
>   (SM/DM fixed/inc/dec, TS units; gint's `dma_memset` clears VRAM from a 32-byte pattern in IL memory);
>   transfer-end IRQ (0x800+0x20·ch, 0xb80/0xba0) when CHCR.IE; **TE is tracked**: a write starts a
>   transfer only with DE=1 and TE=0, completion sets TE — gint's world switch restores saved CHCRs with
>   DE|TE set, which must not re-run the last transfer (it re-cleared VRAM and pushed half-drawn frames
>   through the OS's 384-wide window → the smeared screens).
> · **Memory**: X/Y + IL on-chip memories at 0xE5000000-0xE520FFFF (`Memory.xyram`, not in save-states);
>   gint relocates its interrupt callback to 0xE5200020 and keeps its DMA pattern buffer at 0xE5200000.
> · **Running a fresh .g3a without a Fugue writer:** `dasm_swap_test.go` (tag probe, `DASM_MODE=late`):
>   resume the menu snapshot, launch icon J (helomelo2, header 0x00db4000, 9 pages, code 0x00dbb000/
>   0x00dbc000), then overwrite those two code pages and put the rest at erased 0x007c0000.., extending the
>   OS's add-in page table 0x8c04cf0c (the miss handler 0x8002c918 only rejects zero entries). Writing the
>   file into flash *before* launch fails: the OS re-validates add-ins when it rebuilds the menu (checksums
>   at 0x16 / 0x20 and more) and drops the icon. Screenshots `%TEMP%\cg50_dasm_*.png`; the whole scripted
>   session (open picker → ROM listing → follow/back → hex → strings → header → open file → MENU back to the
>   main menu) runs in 3.6 s.
> **Open:** file content reads from an add-in fail on the emulator: BFile_FindFirst/Open/Size work (DASM's
> picker lists all 11 add-ins with sizes), BFile_Open returns handle 0x01000000, but BFile_Seek/BFile_Read
> return 0 and the OS reads **no storage flash page at all** during the call (`Memory.flashRdHook` trace at
> the end of `TestDasmSwap`; the add-in's cache page ends up 0xFF). Either the FTL/Fugue read path needs
> RAM/peripheral state the snapshot lacks, or a handle-table quirk. **The ordered task list lives in
> `september/cg50_addons/dasm/NOTES.md` ("NEXT — do these one by one"):** Task 1 = the user runs DASM on
> the real calc (title bar `rd4096` means file reads are fine there and this emulator gap is Task 2);
> Task 2 = fix it here: (2a) does the OS's own Memory app read file contents on the emulator
> (`flashRdHook`)? (2b) syscall-entry hook (0x80020070, r0 = number) / pc sampling inside the add-in's
> BFile_Open/Read to find where they bail; (2c) fix + test, rerun `TestDasmSwap` until the file listing
> shows real code.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-27 cont.18u)

>
> **Sibling directory (2026-09-27): `F:\ru\myprojects\september\cg50_addons\`** — new add-in projects
> (first: `dasm/`, an on-device SH-4A disassembler; then a field/contour plotter; later our own runtime
> instead of gint). Its `README.md` lists what to reuse from here (emulator, `re/sh4dis.py`, syscall
> tables, KEYMAP, fxSDK Docker, `tools/tickprobe` skeleton). This project stays the toolkit/emulator.
>
> ### 🖌 cont.18u — Native (HLE) OS blitter shipped. Phone menu hold now at the OS's 33 Hz cap (22/1.2 s).
> **What:** `emu_go/hle.go` `CPU.hleBlit` replaces the OS bitmap blitter **0x80056900** (3.60) natively. The
> CPU loop (`step`/`run`) checks `pc == hlePC` (0 = off); `Emulator.EnableHLE` / bridge `EmuSetHLE`; **on in
> `EmuInit` (Android), off by default** so conformance/goldens run the real code. Descriptor at r4 (0x2c
> bytes, BE): +0 x, +4 y (wrappers 0x80055ea0/0x80055eec add the 24-row status bar; 0x80055ee6 = `jmp` tail
> wrapper with r5=1), +8/+c xo/yo, +0x10/+0x14 w/h, +0x18 format (1 = 4 bpp palette, 2 = RGB565 BE,
> 3 = 1 bpp), +0x1c data, +0x20/+0x21 fg/bg palette idx (1 bpp), +0x22 transparent idx (0xff none), +0x24
> rop (2 not, 3 dither), +0x25 mode2 (2/3/4 = or/and/xor VRAM), +0x28 blend (>0 → 0x8004eb54); r5 = writer:
> 1 = VRAM 0xAC000000+y*768+x*2 (0x8005575c), 2 = direct to panel (0x800557fa) → not handled. Palette = 16
> RGB565 words at 0x80399d30. Clipping/stride rules in the hle.go header. The OS was seen to use ONLY
> rop=1/mode2=1/blend=0 with all three formats ± transparent (`blit_probe_test.go`, tag probe).
> **Proof:** `hle_test.go TestHLEBlitMatchesInterpreter` (normal suite): every blitter call in a session (menu
> moves, EXE into Run-Matrix, typing, MENU, EXIT) run both ways from the same state → DRAM byte-identical
> (VRAM; the callee's dead stack frame below sp excluded), r8-r15/pr/macl/return pc identical; 77 native, 1
> fallback (the r5=2 call on EXIT). Cycle charge (base 90 + 30/row + per-pixel 85/83/94 drawn, 55/53/64
> transparent) = 1.001× the interpreter's count → timing preserved. Full suite green, goldens untouched.
> **Speed:** desktop 3-s menu hold 5873 → 1785 ms host (3.3×). **Phone 1.2 s RIGHT hold: 21, 22, 22, 22, 22**
> (cont.18t: 9-13; before: 6-7), `drops=0`, emu thread ~20 % busy → the phone now runs the menu at full
> emulated speed = the OS's 33 Hz repeat cap (20-scan initial delay + 0.6 s × 33 Hz).
> **Finding — the 100M time base is ~2× the real calc:** the real fx-CG50 makes 11+ moves in 1.2 s;
> `TestRepeatRateProbe` gives 17.8 moves/s @44M ips → 1 + 0.6 × 17.8 ≈ 11.7. So the calc's *effective*
> throughput on OS code (NOR-flash wait states, SDRAM VRAM writes) is ≈ 45M instr/s, not the 118 MHz clock's
> 100M. **DECIDED (user): a Speed setting** — *Original hardware* (`CalcSurfaceView.IPS_ORIGINAL` = 45M,
> default) / *Fast* (`IPS_FAST` = 100M, "the full clock, ~2×"). No button: **long-press the calculator
> screen** → AlertDialog (MainActivity.showSettings); SharedPreferences "cg50"/"speed"; the status line under
> the screen reads "hold the screen for settings"; `am start --es speed original|fast` sets it from adb
> (`re/phone.py start original|fast`). `CalcSurfaceView.ips` is a runtime var (chunk = ips/200).
> **Bug found & fixed while measuring — emulated time inflated with the HLE:** a native blit charges its
> cycles in one lump, so `Step(chunk)` overshoots and the next chunk started from the overshoot; the app
> paced by requested chunks, so time ran fast (25 pushes @45M instead of 13; the "22 @100M" above was
> inflated too). Fix: `EmuCycles()` export; the emu thread advances `due` by the core's real cycle delta
> (`ran * 1e9 / ips`; CHUNK_NS if 0 so a halted core can't spin). **Final phone numbers (1.2 s hold):
> original 11, 12, 11, 12, 11 (= the calc); fast 18, 17, 18 (= desktop exact timing @100M).** At 45M the
> little cores still drop 2-5 debts/s during a burst (emulated 39-44M/s) — interpreted text rendering.
> **Gotcha found — the emulated calc AUTO-POWERS-OFF like the real one:** after ~10 min idle the OS enters
> its power-off wait (stack: 0x800c48c8 writes marker 0xa55a5aa5 → 0x802aec00 loop: sleep + poll
> 0x8c090c1c/0x8c090c20 + 0x801df0de "AC/ON pressed?"); every key except AC/ON is ignored, on the phone AND
> in the desktop with that snapshot (`re/phone.py pull` → `stuck_probe_test.go`). Looked like a dead machine
> ("executed 0.01M/s, 0 pushes"). AC/ON wakes it. The display-off is only partly visible (a black band at the
> bottom; the OS blanks via the direct-panel path / LCD display-control regs we don't render) → TODO: model the
> panel's display-off so the screen goes blank like the real one (then AC/ON is obvious).
> Measuring gotchas: `re/phone.py start` now presses HOME first so onPause snapshots the CURRENT machine
> (force-stop alone resumes the last saved snapshot); a settings dialog left open swallows `input` taps
> (`phone.py back`); `go test` caches results — use `-count=1` when only a data file changed.
> **Tools:** `re/phone.py` (build/start/menuhold/pull/keylog/back/shot), `blit_probe_test.go` (TestBlitProbe
> descriptors + instr/px; TestHLESpeed; TestSpeedSettingProbe = the phone's chunk flow at several ips),
> `stuck_probe_test.go`, `hle_test.go`. Ghidra was not running — `re/sh4dis.py --image os/flash_dump/os.bin`
> was enough for the RE.
> **NEXT:** (1) LCD display-off (power-off → blank screen; find the OS's panel-off writes with mmioHook in
> the power-off sequence); (2) optional second HLE: the 1-bpp glyph renderer 0x8017b75e (22.7 % of a menu
> move; stack args at +0x54.. fg/bg words +0x6a/+0x6e, flags r7: bit2 invert, bit 0x40 clip height, calls
> 0x8005575c or 0x8004f64a when stack+0x70 != 0) — same probe/differential-test pattern; (3) skin polish
> (SHIFT/ALPHA annunciators), release signing.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-27 cont.18t)
>
> ### 🧵 cont.18t — Phone: emu thread decoupled from rendering. 1.2 s RIGHT hold 6-7 → 9-13 moves (calc 11+).
> **ADPF is a dead end on this phone:** POCO X3 NFC (sm6150, Android 12 / MIUI 14, schedutil) —
> `PerformanceHintManager.createHintSession` returns null (vendor power HAL has no hint sessions). The code
> stays in (`CalcSurfaceView.createHintSession`, works on devices that have it; `--ez adpf false` disables).
> **Real cause, measured** (`re/phone.py menuhold`: `input swipe` hold + logcat pushes + a 50 ms sampler of
> cpu0/cpu6 freq and the thread's cpu): the run-loop thread lived on the LITTLE A55 cores (cpu0-5, 1.0-1.8
> GHz) for the whole burst; big cores idle at 652 MHz; it only touched cpu6/7 for ~200 ms of touch boost.
> EAS places by *running* utilisation and the old loop (step ~10 ms → block in lockCanvas ~8 ms → sleep) was a
> ~40 % task that "fits" a little core. **Fix:** `cg50-emu` thread steps CHUNK=MAX_IPS/200 (5 ms) whenever
> emulated time ≤ wall clock, sleeps only when ahead, drops debt > 20 ms (= slows uniformly, same policy as
> before; `drops` in the log); `cg50-render` blits at 60 fps, only while the panel changed in the last 300 ms
> (`lcd.gen` → `Emulator.FrameGen` → `EmuFrameGen`; sparse draws cost 20-30 ms each, so a warm window). Idle:
> emu busy 7 %, 0 draws/s (Run-Matrix cursor blink = 2 draws/s). Bursts: executed 9→22-25M instr/s.
> **Measured 1.2 s holds (pushes):** before 7,6,6 → after 12,11,12 / 13,8,12 / 13,9,11,9,11. Spread = core
> placement (13 when the hold rides the touch boost on cpu6/7; 9-11 on little cores at 1.8 GHz — the emu
> thread still isn't up-migrated reliably). Go tests all green (lcd.gen is presentation-only, not in
> save-state); `EmuPushes` also exported for measuring.
> **NEXT:** (1) if more speed is wanted: try `sched_setaffinity` to cpu6-7 for the emu thread during bursts
> (JNI, own thread — allowed without root) and measure with `menuhold`; (2) the native (HLE) blitter (below,
> #2) — less work per move helps on any core; (3) skin polish, release signing. Phone has this build.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-27 cont.18s)
>
> ### 🔋 cont.18s — 0x560 is the BATTERY ADC; conversion-driven model shipped. Idle ≈ free. Scroll speed still ~⅔ of the calc.
> **What changed (Go `emu_go/mmio.go periphIRQ` + Python `emu/mmio.py PeriphIRQ`, identical):** INTEVT 0x560's
> source is the battery ADC at 0xA4610000 (+0x82/+0x84 result = 0x7140 modelled battery voltage, +0x88/+0x8A
> control with end flags bits 14/15 — the 0x88..0x8B word is one register in the model, +0x8C bit15 start).
> Two ways the OS starts a conversion, both now modelled:
>   · **software start** `+0x88 |= 0x2000` — battery monitor 0x801de54a (start 0x801de64a) then busy-waits,
>     CPU running, for the ISR's event 1 or `+0x88` bit15 → completes after ips/10000 (100 µs), awake or not;
>   · **idle-routine start** `+0x8C = 0x8000` (0x802ae87a, right before `sleep`) → completes only after
>     ips/64 cycles of CPU *sleep* since the start (counted in the new `cpu.idle`), per the on-device probes.
>   Either sets the end flags and raises 0x560 once. The old periodic tick (timerPeriod, every 30k instr) still
>   runs until the OS first starts a conversion (fresh boot was proven with it) — after that `adcMode`.
>   Scheduler: `nextDue` includes the software deadline, and the ADC sleep deadline while sleeping (Step
>   recomputes `due` when entering sleep). Save-state keys 0x10000.. in the PERIPH_IRQ map (mode, armed + left).
>   `Emulator.Resume` zeroes cycles/idle BEFORE restoring (deadlines are relative). Python: `upgrade_bus` adds
>   the new attrs; `CPU.idle`.
> **Tests (all green):** 68/68 conformance, golden boot byte-identical (pure boot never starts the ADC), all
> goldens regenerated identical, NEW `emu/adc_selftest.py` → `emu/adc_golden.txt` ↔ `TestADCOracleTranscript`
> (legacy tick, software start, sleep-only completion, re-arm/cancel), `TestScheduledStepMatchesExact` still
> bit-identical, cursor blink / RTC 2 Hz / keys / MENU / add-in e2e all pass.
> **Measured:** idle work at the 100M time base 11.4M → **0.23M instr executed per emulated s** (desktop and
> phone; phone step 9 ms → 1 ms per frame). Desktop moves/s (TestRepeatRateProbe, emulated time) unchanged or
> slightly better (9.1 @22M, 17.8 @44M, 24.5 @66M, 29 @100M). **Phone 1.2 s RIGHT hold from Run-Matrix: 6, 6,
> 7 moves (cont.18r build: 9; real calc 11+).** Cause: host DVFS — with idle nearly free Android clocks the
> CPU down, so the first few hundred ms of a burst run on slow cores (blit went 5 → 9-10 ms/frame on the same
> work). Not an emulation regression.
> **On-device probes (`tools/tickprobe`, results `os/devic_probes/tickprobe_v1..3_2026-09-27.txt`):** see
> cont.18r notes below. ⚠ v4 (sleep test) RESET the calculator to first-boot setup — compiled out behind
> TICKPROBE_SLEEP_TEST. **Never execute `sleep` / write CPG 0xA4150020 from a probe add-in.** Probes are
> deleted from the calc after use (none left on it now).
> **Phone state:** new APK installed (MD5-verified) — it has everything through cont.18s.
>
> **NEXT (in this order):**
>   1. **Android performance hints (ADPF)** — `PerformanceHintManager` (API 31+; POCO X3 is Android 12+):
>      create a hint session for the render thread with target 16.7 ms and `reportActualWorkDuration(step+blit)`
>      each frame, so clocks ramp as soon as the OS gets busy. Re-measure the 1.2 s hold (goal ≥ 9, then 11+).
>      Optional: skip the blit when the displayed frame is unchanged (LCD GRAM dirty flag) — idle battery.
>   2. **Native (HLE) OS bitmap blitter** 0x80056a40-0x80056bc0 region (find the function entry; ~100 instr per
>      pixel; ~70% of a menu move) + the ~20% region 0x8017b920-0x8017ba40. Implement in Go with a
>      differential test vs the interpreter (same inputs → identical memory/regs), host-side only.
>   3. Skin polish (S/A annunciator state on SHIFT/ALPHA keys), release signing.
> Tools this session: probes (tag `probe`) TestRepeatRateProbe, TestMenuMoveHotspots, TestIdleCostProbe,
> TestADCStuckProbe, bench `bench_probe_test.go` (MenuHold exact vs scheduled). Windows box has 9+ days uptime
> with hibernations → absolute desktop benchmarks are noisy; compare within one run or ask for a reboot.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-27 cont.18r)
>
> ### ⚡ cont.18r — SPEED: menu auto-repeat was ~3.4× slower than the real calc. Partly fixed.
> User: holding RIGHT on the MAIN MENU is ~3× slower on the phone than on the calc. **Measured (probe
> TestRepeatRateProbe, desktop):** moves/s with RIGHT held = 8.6 @22M instr/s (old phone speed), 17.4 @44M,
> 24.5 @66M, 29 @100M, 33 @150M (= the OS's 33 Hz repeat cap). Timing logic is right; it's throughput:
> one menu move ≈ 1.7-2.5M instr, ~70% in the OS's generic per-pixel bitmap blitter (0x80056a40-0x80056bc0,
> ~100 instr/pixel, calls setpixel 0x8005575c) and ~20% in 0x8017b920-0x8017ba40. Real calc: FRQCR =
> 0x0F011112 (keyprobe) = stock ~118 MHz CPU → ~100M instr/s assumed (MAX_IPS).
> **Done:** (1) `Emulator.Step` event scheduling — devices (KEYSC scan, timer, RTC periodic, key tapper)
> only run at their next event (`nextDue`), MMIO writes set `MMIOBus.dirty` to force a recompute; a sleeping
> CPU with nothing pending fast-forwards to the next event. `TestScheduledStepMatchesExact` proves it
> bit-identical to per-instruction polling (`exactTick`) over a 292M-cycle mixed session. (2) `CPU.run`
> tight loop + `Memory.fetch16` direct fetch for untranslated flash/DRAM. Desktop menu-hold benchmark
> (`bench_probe_test.go`): 2.55× faster. (3) `Emulator.Executed()` / `EmuExecuted` (idle excluded).
> (4) Android: **constant time base = MAX_IPS 100M** (set once) + step-time-adaptive frame budget
> (±25%/frame, cap MAX_IPS/60). Lesson: feeding a measured 1-s ips back (old setInstrPerSec loop) or
> sizing from "capacity" both fail — idle seconds inflate the rate so key repeat runs slow while busy, and
> phone DVFS clocks down under light budgets. Constant time base = the machine just slows uniformly.
> **Phone result:** 1.2 s RIGHT hold = 9 moves (was ~5; real calc 11+). Phone executes ~28M real instr/s.
> **Open, biggest first:** (a) the synthetic 0x560 "timer" is really the OS-armed unit at 0xA4610000
> (idle routine 0x802ae87a: +0x8A&=~0x2000, +0x8C=0, +0x88=0x42, +0x8C=0x8000 start, +0x8A=0x01CF; ISR
> 0x801ded94 clears flags bits14/15 of +0x88/+0x8A). We fire it every 30k instr = ~3.3 kHz at 100M → ~11M
> instr/s of ISR work even idle (≈40% of phone work during a hold). Real period unknown → write a probe
> add-in (arm it like the OS, poll the flags vs the 32 kHz counter 0xA44D00D8) for the user to run.
> (b) native (HLE) version of the blitter 0x80056xxx with a differential test vs the interpreter.
> Windows box: last real boot 9 days ago + hibernations → absolute benchmarks unreliable (ask for reboot).
>
> **0x560 on the real calc — `tools/tickprobe` (results `os/devic_probes/tickprobe_v*_2026-09-27.txt`):**
> v1 invalid (the 32 kHz counter 0xA44D00D8 doesn't run in gint's world). v2 (gint world, 1 ms TMU,
> 1001 ms/RTC s): armed like the OS the unit never completes within 10 s; +0x84 held 0x8D80 → it's an
> A/D result: the unit is the **battery ADC** (+0x8C bit15 = start, +0x84 = result, bits 14/15 of
> +0x88/+0x8A = end flags; block mirrored at +0x10). v3 (OS world via gint_world_switch, 32768 counts/s
> verified): still **no completion within 2 s of a start while the CPU runs** (8/8), same MSTPCR as gint.
> Yet +0x84 changes between runs (8D00/8D80/8D40) → the OS does get readings → conversion apparently only
> progresses while the CPU sleeps (the OS arms it right before `sleep`). v4 tried to test that by replaying
> the idle routine (RCR2 PES=1/2 s, BL=1, CPG 0xA4150020=0, sleep) → **it RESET the calculator** to first-boot
> setup (storage fine). The real idle routine first calls 0x801df084 and may use another sleep path
> (0xa0020926); the sleep test is now compiled out (TICKPROBE_SLEEP_TEST). Do NOT sleep from probes.
> **Conclusion: 0x560 is not a periodic tick** — it's an occasional battery-ADC completion; our 3.3 kHz
> synthetic tick is far too frequent. Proposed model: one completion IRQ per OS start (+0x8C ← 0x8000),
> only after the CPU has been asleep for a while (latency unknown — pick a conservative value, e.g. the
> RTC period), result = last real reading (0x8D40), flags bits 14/15.
>
> ### ✅ cont.18q — ADD-INS RUN: SH-4A MMU (UTLB, ldtlb, translation, precise TLB exceptions).
> User: their add-in **helomelo2** (menu icon J; cont.18h misread it as "heronics2"/"howdy") runs on the
> calc but crashed the Android app. **Root cause: no MMU.** `ldtlb` was a nop and nothing translated, so
> the add-in's entry 0x00300000 executed OS bytes at phys 0x00300000 (desktop: blank spin; phone: garbage
> eventually hit an unmapped address → Go panic → app killed). Built-in apps never translate (P1/P2).
> **How the 3.60 OS runs an add-in** (probe `TestAddinMMUProbe`): launch path 0x8002c84c/0x8002c8a4 writes
> PTEH/PTEL and `ldtlb` with MMUCR.URC choosing the slot — ONE 4 KB code page 0x00300000→0x00dbb000
> (.g3a in fls0), eight 64 KB RAM pages 0x0810_0000-0x0817_ffff→DRAM 0x0C16_0000.. (slots 55-62), slot 63
> = VPN 0 (64 KB, needed: OS memcpys from low addresses during MENU); MMUCR=0x00fc0101 (AT, SV, URB=63);
> jump 0x00300000. **Every further code page is demand-paged:** TLB miss → VBR+0x400 → dispatcher
> 0x80021554 (reads EXPEVT 0xFF000024, table 0xFD8010C8) → handler 0x8002c918: page = PTEH&~0xFFF; if
> ≥0x300000 → phys = 0x8c04cf0c[(page-0x300000)>>12] → map via 0x8002c84c into its own round-robin slot
> (counter 0x8c04d70c, slots 0..54). Protection codes 0xA0/0xC0 → OS "System ERROR / TLB ERROR" screen.
> **Implemented (Go `emu_go/mmu.go` + Python `emu/mmio.py CCN/UTLBArrays`, memory.py, cpu.py — identical):**
> 64-entry UTLB in the CCN block; ldtlb; MMUCR TI/AT/SV/URC (no auto URC increment — OS sets it); AT=1
> translates P0/U0 + P3; 1K/4K/64K/1M pages; ASID unless SH or SV&MD; **only V=1 entries match** (else
> zeroed/flushed entries alias VPN 0 — that bug produced a fake "TLB ERROR TARGET=5C" on MENU); precise
> exceptions via `stepMMU` rollback (SPC = instr, or the branch for a delay slot); miss → +0x400, prot /
> initial-page-write → +0x100; TEA, PTEH.VPN, EXPEVT (reset value still the 0x0A02 model strap until the
> first exception); UTLB arrays 0xF6/0xF7; UTLB in save-state (CCN map keys 0x100000|i<<4|{0,4,8}).
> `Emulator.Step` now halts with `Fault()` (logged via dbg → logcat) instead of panicking the host.
> Perf: MMU-off overhead ~3-4% raw (hot flag `Memory.mmuAt`); fine vs the pending interpreter fast paths.
> **Tests:** 68/68 conformance (+11 MMU cases: ldtlb/load/store, miss read, predec-store rollback, initial
> page write, priv/user protection, delay-slot miss SPC, TI flush, V=0 no-match, UTLB arrays), golden boot
> byte-identical (boot never enables AT), `TestAddinRunsThroughMMU` (launch J → prompt → key → output →
> MENU → main menu, no violation), save-state round-trip covers the UTLB. Verified visually: "press a key
> ...", then its number grid + "y i am done", MENU → MAIN MENU (icon J "hello :)").
> Probes (tag `probe`): TestAddinProbe / TestAddinInputProbe / TestAddinMenuProbe / TestAddinMMUProbe /
> TestExcTableProbe; `savePNG` writes `%TEMP%/cg50_<name>.png`.
> **Phone: VERIFIED (POCO X3)** — `)` → "press a key ...", `1` → number grid + "y i am done", MENU → MAIN
> MENU (J selected), UP moves the cursor. The icon has a selected ("hello :)") and unselected (yellow "howdy")
> variant. Gotcha: the phone's saved state had been written after helomelo2 ran on the OLD (MMU-less) build,
> which executed random OS bytes and corrupted OS RAM — resumed, it showed a black band and ignored keys. Fixed
> by pushing the clean desktop `cg50_state.bin`; the broken one is kept on the phone as
> `files/cg50_state.broken-oldbuild.bin`. A snapshot taken during a garbage run is poisoned: replace it.
> Unmapped but harmless: 0xA44C0000/0xA44C0020 (timer polled by getkey 0x801e6d40/0x801e6dc4) — hit
> thousands of times inside the add-in; reads 0 and nothing hangs. Model it if timing issues show.
>
> **NEXT:** 1. Key-by-key check with the
> user. 2. PERF. 3. Skin polish. (cont.18p items below still apply.)
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-27 cont.18p)
>
> ### ✅ cont.18p — MENU-from-app SOLVED (MENU/EXIT were swapped) + keymap audit + new Android skin.
> **Root cause:** not the OS, not storage — our **MENU and EXIT keys were swapped**. Matrix (3,7)
> yields getkey **0x7532 = KEY_CTRL_EXIT** (SHIFT → 0x754d QUIT) and (3,8) yields **0x7533 =
> KEY_CTRL_MENU** (SHIFT → 0x7555 SETUP) — codes straight from the OS remap tables, names from libfxcg;
> gint's matrix grouping agrees (EXIT beside DOWN/RIGHT, MENU beside LEFT/UP). So "MENU" sent EXIT,
> which Run-Matrix handles itself (cursor off, re-evaluate + redraw the last answer — the "AC-like"
> symptom). Found via `TestMenuDiff` (probe tag: first-call diff of MENU vs a digit key → the MENU run
> went into Run-Matrix's own handler 0x80194ebc + the BCD evaluate/format chain, no app switch).
> Pressing (3,8) returns to a frame pixel-identical to the MAIN MENU.
> **Fixed everywhere:** `re/dump_keymap.py` labels (+ it now also emits the KEYSC-word section that was
> hand-appended to KEYMAP.md and got lost on regen), regenerated `re/KEYMAP.md`, web UI (Esc=EXIT (3,7),
> Home=MENU (3,8)), Android keypad. Test: `TestMenuReturnsFromApp` (keysc_test.go).
> **Keymap audit:** new `re/audit_keymap.py` checks every label in KEYMAP.md and Android `KeyMap.kt`
> against libfxcg's name for the OS code (fetches `re/libfxcg_keyboard.h`). After the fix: **0
> mismatches, all 50 physical keys present on distinct matrix positions.** User reports "some keys
> don't work" — the label audit rules out wrong mappings; still to do: press every key on the phone
> and note which ones misbehave (could be app-context, touch targets, or OS-side behaviour).
> **Android skin (new):** `KeypadView.kt` draws the whole keyboard in the real fx-CG50 order (F1-F6;
> SHIFT OPTN VARS MENU / ALPHA x² ^ EXIT beside a round D-pad; X,θ,T log ln sin cos tan; a/b S⇔D ( ) , →;
> 7 8 9 DEL AC/ON; 4 5 6 × ÷; 1 2 3 + −; 0 . ×10ˣ (−) EXE) with gradient caps, pressed state, yellow
> SHIFT / red ALPHA legends above each key, multi-touch, haptic tap. `KeyMap.kt` = data (label, row,
> col, shift, alpha, cap style). Screen now keeps the 384:216 aspect (it was stretched vertically) in a
> bezel; no action bar; graphite body. Verified on the POCO X3 (MENU from Run-Matrix → MAIN MENU) and
> the KeyStudio_API31 AVD. (Do NOT use the Medium_Phone AVD — it hangs at boot; user's call.)
> Wireless adb on the phone drops when its screen sleeps: `adb kill-server`, `adb mdns services`,
> `adb connect adb-584c917b-jFkRGG._adb-tls-connect._tcp` once the user wakes it.
>
> **NEXT:** 1. Key-by-key check on the phone with the user (which keys "don't work"). 2. Skin polish
> from user feedback (annunciator S/A state on SHIFT/ALPHA caps, colours). 3. PERF (interpreter fast
> paths, memcmp/memset HLE). 4. Later: release APK signing; real source of the synthetic 0x560 tick.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-27 cont.18o)
>
> ### ✅ cont.18o — CURSOR BLINKS: real R61524 LCD controller model (GRAM is the screen).
> **Root cause (two bugs):** (1) the OS draws the text cursor **straight into the LCD controller's
> GRAM**, pixel by pixel, never into VRAM and without a DMA push — our "present a VRAM copy on each
> DMA push" model could never show it; (2) **LCD register reads were wrong**: `0xB4000000` returned the
> last word written (i.e. the index), so the OS's read-modify-write of R003 (entry mode) produced
> 0x0083 instead of the real 0x00A0 and getORG (bit7) read 0 → the OS took the absolute-address
> fallback instead of the real **window-addressing path** (R210-R213 window + R200/R201 = 0,0).
> **Cursor path (all static+dynamic RE this session):** cursor syscalls resolve (3.60 table
> **0x80687cd0**, new `re/syscall360.py`) to a module at 0x800c3d..: Cursor_SetFlashOn sc 0x8C7 =
> 0x800c3eb8, Cursor_SetFlashOff 0x8C8 = 0x800c3f36, Keyboard_CursorFlash 0x8CA = 0x800c3f68,
> SetCursorFlashToggle 0x8D2 = 0x800c434a (getToggle 0x800c4344), toggle+draw 0x800c3fc8 →
> **draw 0x800c4006** (saves VRAM rect via 0x801f0f58, builds rows, LCD window write) / **erase
> 0x800c4266** (per-pixel restore from VRAM via 0x800564cc). Flags: 0x8c04fb3a (flash on), 0x8c04fb44.
> Driven at 2 Hz from the RTC event: sc 0x8CC (0x800c3fa8) called from 0x801e6228. **It was already
> running before this fix** — only the panel was missing. (0x80150508, the "cursor overlay" in the
> cont.18n notes, is actually a 16x22 status-bar icon blit into VRAM — not the cursor.)
> **LCD protocol:** all accesses 16-bit at +0; RS = **PFC 0xA405013C bit4** (0 = index, 1 = data);
> helper 0x8004e272 (select), setORG 0x8004e3e8, getORG 0x8004e440, window/pixel setup 0x8004e2aa
> (H = 0x18B − (x+6), V = y). Boot (0x8004dd3a, ~5.27M instr) sets R003=0x00A0 (ORG=1, I/D1=1 V-inc,
> I/D0=0 H-dec, AM=0) and window 0..395 × 0..223 (panel 396x224; OS 384x216 area at H=389−x, V=y).
> Full push = R200/R201 + select R202, then DMAC ch2 CHCR 0x00101400 (TS=0100 = 32-byte units,
> TCR 0x1440 = one frame).
> **Implemented:** `emu_go/lcd.go` (index/register file with read-back, ORG/window/I-D/AM address
> counter, GRAM 396x224, DMA streaming); DMAC streams any LCD-bound transfer through it
> (`dmaUnit`, `Memory.span`); `Emulator.Framebuffer*` + web UI `/frame` render **GRAM** (next-step #3
> done); save-state MMIO section carries the LCD regs (`lcd.stateMap`), GRAM re-seeded from VRAM on
> every resume (`ResumeBytes`), legacy/pre-LCD snapshots get the boot registers. Python oracle mirrors
> the CPU-visible part (`emu/mmio.py LCD`: index, register read-back, GRAM read = 0) + transcript
> parity `emu/lcd_selftest.py` → `emu/lcd_golden.txt` ↔ `TestLCDOracleTranscript`.
> **Tests (all green):** 57/57 conformance, 2M golden boot byte-identical (boot never reaches LCD
> init), new `lcd_test.go` (read-back, window stream, oracle transcript, **TestCursorBlinks**: 4
> displayed-frame toggles in 2 s, 0 pushes, VRAM untouched, changes confined to x36-38 y168-189 after
> "12"), save-state round-trip + legacy extended for the LCD. Visually confirmed ("12|" ↔ "12").
> Probes kept behind a build tag: `go -C emu_go test -tags probe -run TestCursorProbe|TestLCDInitProbe|TestMenuFromAppProbe -v`
> (shadow call stack, `Memory.wrHook` VRAM write-watch, `Memory.mmioHook`).
> **Re-tested MENU-from-app after the fix: still broken** (MENU in Run-Matrix → stays in Run-Matrix;
> only the 44 caret pixels toggle). Not caused by the LCD reads.
> **Committed** as `740ad33` (not pushed). Android `.so` rebuilt (`build_go_lib.ps1`) and APK built
> (`:app:assembleDebug`, `android/app/build/outputs/apk/debug/app-debug.apk`) — **NOT installed**: no
> adb device (USB or wireless mDNS) was reachable. Install with
> `adb install -r android/app/build/outputs/apk/debug/app-debug.apk`, then check the blink in Run-Matrix.
>
> **NEXT (in this order):** 1. MENU-from-app (old #2 below; use `TestMenuFromAppProbe` as the harness,
> then shadow-stack the MENU decode 0x801952cc → app-switch). 2. Rebuild + install the Android build and
> confirm the blink on the phone. 3. PERF (old #4). 4. Skin (old #5). 5. Later items (old #6).
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-09-27 cont.18n)
>
> ### 🏆 cont.18l — REAL KEYBOARD PATH SHIPPED: KEYSC key-scan unit modelled, injection hack deleted.
> Keys now reach the OS exactly as on the calculator: the host sets bits in the emulated **KEYSC/KIU
> matrix at 0xA44B0000**, the unit raises **INTEVT 0xBE0**, and the OS's own keyboard ISR scans,
> debounces, repeats and enqueues. The enqueue-shortcut injector (FUN_801e684c + decode-confirm +
> 20M-cycle retry timeout = the menu "reversal hang") is GONE from `emulator.go`/`webui.go`
> (`main.go` keeps `injectKey` only for the old RE harness modes).
> **Measured (TestKeyscMenuTap): DOWN tap at the menu → keyboard ISR after 39 instr (key-detect is
> edge-triggered), framebuffer changes after 48k instr. Before: 20,000,000 instr (first injection
> flushed by the redraw, re-injected only after the 20M-cycle timeout).**
>
> **What the RE established (all static, from os.bin via `re/disasm_static.py`; Ghidra was down):**
>   · cont.18k's "PFC port scan" lead was WRONG: 0xA4050100/120/14e/162 + 0xA44C00xx traffic at idle is
>     pin-function setup + an ETMU timer, and `mmio.go`'s old "KEYSC @0xA4080000" was really the **INTC**
>     (IPRA..IPRL +0x00.., IMR +0x80.., IMCR +0xC0..; IPRF[15:12]=13 = KEYSC priority, IMR5 bit7 = its mask).
>   · **KEYSC @0xA44B0000** (now `emu_go/keysc.go` / `emu/mmio.py KeyScan`, byte-identical models):
>     +0x00..0x0B six 16-bit key words — **word = col>>1, bit = row + 8*(col&1)** (0-based KEYMAP grid;
>     AC/ON=(0,0)=word0 bit0, DOWN=(2,7)=word3 bit10, UP=(1,8)=word4 bit1); +0x0C ctrl (bit15 enable);
>     +0x10 mode (0x200 normal = detect+auto-scan, 0x400 detect-only, 0x800 scan-now, 0 off); +0x12 busy;
>     +0x14 = [15:8] IRQ-enable per flag | [7:0] flags W1C: **bit3 key-detect, bit1 scan-complete**.
>     OS init (FUN_801e51c8, values confirmed by a reset boot dump): 0x8000 / 0x8042 / mode 0x200 /
>     IE 0x48 / 0xC8 / 0xFFF / 0xFF. ISR = **FUN_801e4c00** (via wrapper 0x801dedcc): state machine at
>     0x8c090c00 (0 idle → 1 pressed → 4 released → 0); idle waits for flag3, pressed/released wait for
>     flag1; decoder **FUN_801e49bc** → (row,col) → table **0x8068fa70[col*8+row] = (row+1)<<8|(col+1)**
>     (matches every KEYMAP.md entry); events posted via FUN_801e504c(type 1 press/2 repeat/3 release).
>     Repeat = OS logic: first repeat after **20 scans** (*0x8c08ba38), then **one per scan** (*0x8c08ba3c=0).
>     Sync scan FUN_801e5700 = mode 0x800 + wait flag1. IRQ disable/enable helpers 0x801dedd2/0x801dedda.
>   · **3.60 interrupt dispatcher** (VBR 0x80020f00 → 0x80021500): handler = *(0xFD8010C8 +
>     ((INTEVT-0x40)>>3)) [byte offset], IMASK byte at 0xFD8012C8+((INTEVT-0x40)>>5). Timer 0x560 →
>     0x801ded94; KEYSC 0xBE0 → 0x801dedcc; also 0x1b8/0x1c0/0x2d8 → 0x801deebe/0x801def3a/0x801dfc6c.
>   · Idle profile: 64% of idle instructions are memcmp(0x80384a42)/memset(0x803851b8); `sleep` executes
>     at 0xa00208e8 (treated as nop). Menu cursor move redraw = ~1.3M instr.
>
> **Model behaviour** (`keysc.go` header has the full contract): every `scanPeriod` instr: press edge →
> flag3 (a press into an empty matrix scans at the very next tick); while held (+2 scans after
> release) → flag1; mode 0x800 → flag1 always; IRQ 0xBE0 level 13
> while flags&IE. `keyTapper` turns queued taps into hold-3-scans/gap-3-scans edges. `Emulator` API:
> InjectKey (tap), **KeyDown/KeyUp** (real hold → OS auto-repeat), ReleaseAllKeys, SetKeyScanPeriod.
> Resume/LoadState call `keysc.resumeDefaults()` (peripheral regs aren't in the save-state).
> **scanPeriod = the OS key-repeat clock** (instruction-based like all our time): default 500k
> (≈20 ms @25M ips); Android sets it to 20 ms of its budget (`MainActivity` → setKeyScanPeriod).
> Android: `NativeBridge.keyDown/keyUp/releaseAllKeys/setKeyScanPeriod` + JNI + Go exports; keypad
> buttons now use touch down/up (held arrows auto-repeat). `.so` rebuilt (build_go_lib.ps1), APK
> built with `:app:assembleDebug` — NOT yet installed/verified on the phone (no adb device in the office).
>
> **Tests (all green):** 57/57 conformance + 2M golden boot **byte-identical after regen** (boot never
> presses keys) + `keysc_test.go`: protocol, matrix layout, **oracle transcript parity**
> (`emu/keysc_selftest.py` → `emu/keysc_golden.txt`), menu-tap e2e, hold-repeat e2e. Tools added:
> `re/find_portrefs.py`, `re/find_callers.py`, `re/patch_keysc_wiring.py`, `re/patch_keysc_android.py`.
>
> **ON-DEVICE (POCO X3, same session, later):** APK installed + launched fine (22M ips, blit 2.5 ms). Two
> findings: (a) a **fast tap** delivers touch down+up while `Step` holds the mutex for a whole 500k slice,
> so the matrix was pressed+released before one instruction ran → FIXED: `KeyDown/KeyUp` enforce a minimum
> hold of tapHoldScans (deferred release, `pendingUp`, test `TestKeyscFastTapStillLands`); Kotlin logs
> `cg50-key` "ui down/up" with JNI µs. (b) **MENU from inside Run-Matrix does NOT return to the main menu**
> (user-observed; reproduced on desktop from the phone's snapshot AND from the June `cg50_state.bin`:
> EXE→Run-Matrix, MENU → screen clears then Run-Matrix redraws with the last answer, AC/ON-like). It is
> NOT the new key path: the OLD enqueue injector fails identically, even with KEYSC reads stubbed to 0
> (June behaviour). Notes cont.15/18h claim "MENU 3-7 (back to menu)" worked — probably from a fresh
> scripted boot or via load-state, not from within an app after Resume. Lead: the MENU app-switch path
> aborts and redraws — suspect a storage/FTL or peripheral dependency after Resume (fls0 mount state, cf.
> cont.18f) or an unmodelled register the switch reads. (Also: adb `input tap` works on this MIUI 14 phone
> when the app has focus; earlier "no reaction" was Settings' pairing dialog holding focus.)
>
> ### 🖥 2026-09-27 (cont.18m) — REAL FRAME PRESENTATION + MMIO IN SAVE-STATES; keyprobe MEASURED.
> **Display root cause (user: "screen draws like a PC without graphics drivers"):** the app showed live
> VRAM every frame. Worse: after ANY Resume the OS had stopped pushing VRAM→LCD entirely, because both
> push routines (3.60: `0x800554a6` area push, `0x8005552c` full Bdisp_PutDisp_DD; DMAC ch2
> `0xFE008020`, DMAOR `0xFE008060`, LCD `0xB4000000`) gate on **PFC 0xA405013C bit4 (display enable)**
> then **CHCR2.DE==0**, and save-states held NO MMIO registers → bit4 read 0 → early return forever.
> Fixes: (1) `dmac.onLCDPush` fires when CHCR (+0xC) is written with DE=1 and DAR==0x14000000; the
> Emulator copies VRAM(SAR) into a `presented` buffer then — `FramebufferRGBA/RGB565` now return the
> presented frame (`VRAMRGB565` = live VRAM for diagnostics); (2) save-state gained an optional
> trailing **"MMIO" section** (all region register maps + KEYSC ctrl/mode/ie); legacy snapshots (the
> June cg50_state.bin, the phone's) get `applyLegacyResumeDefaults()` = PFC bit4 + KEYSC defaults.
> Tests: `present_test.go` (0 pushes idle, ≥1 per menu move, presented≠live mid-redraw),
> `TestLegacyStateDefaults`, round-trip extended with PFC/DMAC/KEYSC regs. All green.
> **keyprobe (real calc, os/devic_probes/keyprobe-results-2026-09-27.md):** KEYSC scan = **30.3 ms
> (33 Hz) while held, 0 scans idle**, key words exactly as modelled (RIGHT = word3 bit9), regs
> 8000/8042/0200/7600/00c8/0fff/00ff as RE'd → `DefaultKeyScanPeriod` 750k, Android divisor 60/33.
> Timer NOT measured: TSTR=0 (OS tick is not TMU; ETMU suspected), 0xFD8017D0 static. `timerprobe`
> (diffs timer blocks + RTC + all ILRAM over RTC seconds) is built and on the calc — run it next.
> **Toolchain on Windows:** `docker build -t fxsdk:latest tools/fxsdk-docker` (done, image exists);
> build any add-in with `docker run --rm -v "F:\...	ools\<name>:/work" fxsdk:latest fxsdk build-cg`
> (use a Windows-style path from PowerShell; Git-Bash mangles the mount). Calc mounts as `G:` in USB
> Flash mode; add-ins write results to `\fls0\*.TXT` (BFile) → read from G:.
> Still open: MENU-from-app (below), web UI still shows live VRAM (uses mem.dram directly).
>
> ### ⏱ 2026-09-27 (cont.18n) — TIME BASE: RTC periodic IRQ + real `sleep` + host ips; cursor blink still open.
> User asked why the cursor never blinks / whether the OS gets its time base. It did not: the only
> interrupt we ever delivered was the synthetic 0x560 tick (30k instr) + KEYSC. Found and modelled:
> · **RTC @0xA413FEC0** (`emu_go/rtc.go`, `emu/mmio.py RTC`): calendar (BCD, fixed 2010 epoch,
>   `SetClock`), R64CNT (128 Hz increments), RCR1/RCR2. **The OS idle path 0x802ae742 arms RCR2.PES=1/2 s
>   right before `sleep` and clears it on wake; the periodic ISR (INTEVT 0xAA0 → 0x801dfc6c) clears
>   RCR2[7:4] and posts main-loop event 0x80** — a 2 Hz heartbeat. Periodic events are phase-aligned
>   to the RTC clock (re-arming does not restart the period). Verified: ISR runs 6× per 3 emulated s.
> · **`sleep` is real** (Go+Python): halts, cycles still advance (devices tick), ANY interrupt REQUEST
>   wakes it even with SR.BL=1 (the OS sleeps with BL set, clears BL after; SH-4 semantics), serviced
>   once unmasked. Side effect: idle is now nearly free → phone battery. `TestSleepWakesOnRequest`.
> · **Host time base**: `MMIOBus.SetInstrPerSecond(ips)` (Emulator/`EmuSetInstrPerSec`, Kotlin feeds
>   the measured ips every second; `EmuSetClock` sets the calendar) derives KEYSC scan (ips/33), the
>   32.768 kHz free counter (0xA44D00D8/C8, now 32-bit, `countDiv=ips/32768`; measured on the calc:
>   65,533 counts/2 s) and the RTC. Goldens run with `SetInstrPerSecond(1_000_000)` (gen_golden.py +
>   golden_test.go) so the boot's R64CNT-timed wait at 0x80000b5c stays short; the pure-boot mode
>   (timerPeriod=0) still raises no IRQs. Goldens regenerated (final PC 0x801df46e).
> · Save-state MMIO section now also carries RCR1/RCR2.
> **Cursor blink: STILL NOT FOUND.** Ruled out (with evidence): the 6-slot software-timer subsystem
> (ETMU0 @25 ms, Timer_Install 0x800c4978 / Start 0x800c4ac2 / Stop 0x800c4a40; only an inactive
> 8250-tick APO slot exists; the 250 ms timer 0x80150108 is a USB/power monitor gated by 0x802b0ad2
> = USB status word 0x95ff), the RTC periodic event (fires, no push follows), the 32.768 kHz counter
> scaling, the ETMU channels (all TSTR=0 on the real calc too when an add-in launches), and the
> "tick word" 0xFD8017D0 (= the auto-power-off countdown, minutes×30). In Run-Matrix the only pushes
> are the full push 0x8005552c from the getkey redraw 0x801951d8 (after keys); the cursor overlay is
> drawn by 0x80150508(r4=0/1, buf) around it, gated by 0x80150050 (= *0xFD801D28, a power/backlight
> flag written only by 0x8014ff16..0x80150030). NEXT for the blink: watch VRAM writes at the caret
> rectangle after a keypress to catch the cursor-draw routine's PC, then its callers/timer.
> **timerprobe (real calc):** only 0xA44D00C8/D8 moved (32.768 kHz); TMU/ETMU stopped; no ILRAM word
> changed in 3 s — the world switch does not run OS ISRs, so OS timer activity can't be observed that
> way. Both probes + results: `tools/keyprobe`, `tools/timerprobe`, `os/devic_probes/*2026-09-27*`.
>
> **STATE at session end (2026-09-27 13:10, home, calc + phone on USB):** everything above is committed
> and pushed (`2ac318f`); the phone runs that build. Tree clean. fxSDK Docker image `fxsdk:latest` exists;
> both probes built; calc mounts as `G:` in USB Flash mode (unplug/replug + F1 to re-enter it).
>
> **NEXT (in this order):**
>   1. **Cursor blink** (user asked; still not blinking). Method: in Run-Matrix after a keypress, watch
>      DRAM writes inside the caret rectangle (bottom-left input line, VRAM phys 0x0C000000, RGB565
>      384×216) to catch the cursor-draw routine's PC (add a temporary write-watch in `memory.go`
>      Write for a phys range, like the existing rdPC read-watch); then find its callers and the
>      timer/event that toggles it. Everything already ruled out is listed under cont.18n — do not
>      re-check those. Expect the toggle to be driven by the RTC 2 Hz event 0x80 (now delivered) or
>      by the 0xA44C0000 timer (still unmapped: `0x801e6d40` arms it with a table value from
>      0x8068fb48, `0x801e6dc4` polls bit0 of 0xA44C0020 — model it if the blink path uses it).
>   2. **MENU-from-app**: trace after the MENU decode (0x801952cc → app-switch) on the June state: PC
>      histogram + unmapped-MMIO reads + flash-write attempts between the screen clear and the redraw.
>      Now that MMIO regs persist and `sleep`/RTC exist, re-test first — it may already behave differently.
>   3. Web UI (`webui.go`) still shows live VRAM (uses mem.dram) — switch it to the presented frame.
>   4. PERF: interpreter fast paths (decode table, direct DRAM fetch, batched IRQ/timer checks), then
>      memcmp/memset HLE (60% of instructions). Idle is now cheap (real `sleep`).
>   5. Skin: real CG50 keycap layout with SHIFT (yellow)/ALPHA (red) legends, annunciators, haptics.
>   6. Later: release APK signing; the synthetic 0x560 tick's real source (0xA4610000 block) and rate.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-06-10 cont.18k)
>
> ### 🚀 cont.18k — Android PERF win shipped + input-latency ROOT CAUSE found (matrix scan = PFC ports).
> Two threads this session; the perf wins are solid, the input fix is scoped + grounded but NOT yet built.
>
> **(1) PERF — SHIPPED, ~1.5× faster menu.** The Android render loop (`CalcSurfaceView.run()`) was
> wasting ~42% of every frame on a SOFTWARE framebuffer upscale. On-device measurement (new `cg50-perf`
> logcat line: achieved ips / fps / step-ms / blit-ms per 1s) showed **16.5M ips, blit 8.3ms/f**. Fix:
> `holder.setFixedSize(w,h)` → render at native 384×216 and let the **GPU compositor** scale to the view
> (drawBitmap now 1:1). Plus raised `instrPerFrame` 333k→500k. Result measured: **~25M ips, blit 2.5ms/f**
> (~1.5×; raw core on this arm64 phone is ~29M/s). The "watch it draw" partial-frame artifact is gone.
> Both changes are pure Android/Kotlin (no Go core, no tests affected). **These are worth committing.**
>
> **(2) INPUT LATENCY — root cause FOUND, proper fix SCOPED (not built).** The menu "reversal hang"
> (press up after downs → ~1.5–3.2s freeze) is NOT throughput; it's the key-injection scheme. We inject
> via the OS enqueue shortcut `FUN_801e684c` then wait for decode `FUN_801952cc`; a key injected mid-redraw
> is FLUSHED and never decoded, so the old state machine waited a fixed **40M-cycle (~1.6s) timeout** before
> re-injecting (double timeout = ~3.2s). On-device `cg50-key` logging proved it: good presses land in
> ~1.5–12M cyc (60–500ms), flushed ones eat the full timeout. Tried & REVERTED two heuristics: (a) re-inject
> when OS key queue (`keyQueueCount`, *0x801e6a1c) drains — WRONG: that raw queue is drained by an ISR in
> ~10–30k cyc regardless of consume, a false "landed" signal → every key marked done without acting (dead UI);
> (b) treat qc-drain as landed → on the BIGNUM "press F1" screen (which consumes keys via a NON-`FUN_801952cc`
> path) it 12×-re-injected then gave up = looked stuck. **Current shipped state = decode-confirm with timeout
> lowered 40M→20M** (`emu_go/emulator.go` keyDecodeTimeout): menu works, hang ~halved, but still bimodal
> (~half of presses ~0.13s, ~half ~1.5s). A bad save-state can get snapshotted mid-screen on the phone; if the
> app looks stuck, force-stop + `adb push os/flash_dump/cg50_state.bin` (clean menu state) before relaunch.
>
> **THE PROPER FIX (next session): model the real hardware matrix scan so injected keys flow through the OS's
> NATIVE scan — works on every screen, no flush, no retry, deletes the whole enqueue/decode-confirm hack.**
> Built a `scancap` mode (`go -C emu_go run . 0 30000 scancap`; resumes to menu, idles, logs every
> KEYSC/KIU/PFC/PORTL access → `re/scancap.txt`). KEY RESULT: the OS scans the matrix via **PFC port
> registers, NOT KEYSC/KIU** (those stay 0). Idle "any-key?" poll routine @**0x801e6d40** region
> (accesses repeat at PC 0x801e6da2–0x801e6dbc). Registers: **0xA4050100 / 0xA4050120 (16-bit port data,
> column select), 0xA405014e (ROW-READ — returns 0x100 = no key; this is the reg that yields pressed-key
> bits for the selected column), 0xA4050162**, plus byte strobe/clock pins at **0xA44C0000 / 0xA44C0020**.
> It's a multi-pin strobe protocol (why the original author skipped it). NEXT STEPS, in order:
>   1. Capture the scan WITH a key held — only possible on real HW (emu stubs the port to 0). This is the
>      perfect **on-device probe** (like the cont.18e BCD-ALU probe): a g3a add-in that reads 0xA405014e +
>      the select state per key → exact column-select value & row-bit for every matrix (col,row). OR fully
>      RE the scan routine @0x801e6d40 statically (it + KEYMAP.md's logical matrix may suffice without a probe).
>   2. Implement the port scan in `emu_go/mmio.go` (keysc→a real `pfcKeyMatrix` region for 0xA4050100/14e/162
>      + 0xA44C pins) and mirror in `emu/mmio.py`; a "held key" sets the right row bit when its column is
>      selected. Replace the `InjectKey` enqueue-shortcut with setting matrix state (held N scans, then released).
>   3. Regen goldens (boot never presses keys → unaffected) + add conformance for the new port reads.
> Tools added this session: `re/find_kiu.py` (reg-constant locator), `emu_go` scancap mode, `re/scancap.txt`.
> ⚠ Uncommitted as of session end: Android perf (CalcSurfaceView), perf+key diagnostics, 40M→20M timeout,
> scancap mode + mmio capture. Do NOT kill TCP :8080 (GhidraMCP); Ghidra decompiler was timing out (5s) so
> used `re/disasm_static.py` (local SH4 disasm) instead — works great for this.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-06-05 cont.18j)
>
> ### 🏆🏆🏆 cont.18j — IT RUNS ON ANDROID. Native app boots the OS on the EMULATOR **and a physical phone**.
> The Android app (Route A: Go core cross-compiled to a c-shared `.so` + a JNI shim, see cont.18i) is
> WORKING: it loads the user's flash dump, RESUMES from the save-state to the MAIN MENU, renders the
> framebuffer, and takes keypad input — **calculations work on the real phone** (M2007J20CG "surya", arm64).
> Verified on both `emulator-5554` (x86_64) and the phone via logcat: `cg50: init ok; core 384x216` +
> `cg50: resume returned 0`, no crash. This is the project's end goal reached end-to-end.
>
> **THE FULL BUILD+DEPLOY RECIPE (reproduce from a clean session — all paths are this machine's):**
>   1. Build the Go core libs:  `pwsh -File android/build_go_lib.ps1`  → writes
>      `android/app/src/main/jniLibs/{arm64-v8a,x86_64}/libcg50core.so` (git-ignored). The script sets the
>      SONAME (critical — see fix below). Re-run whenever `emu_go/` changes.
>   2. Build+install the APK (Gradle needs JDK 17+, NOT the PATH's JDK 11):
>      `$env:JAVA_HOME="D:\Installations\Java\jdk-26.0.1"; & "F:\ru\myprojects\may\cg50\android\gradlew.bat" -p "F:\ru\myprojects\may\cg50\android" :app:installDebug --offline`
>      (CMake re-links the JNI shim against the prebuilt lib for each ABI.)
>   3. Push the user's files to the device's app dir (USE POWERSHELL not Bash — Git-Bash mangles the
>      `/sdcard/...` path into a Windows path!):
>      `& $adb -s <serial> shell mkdir -p /sdcard/Android/data/com.hexbinoct.cg50/files/`
>      then `& $adb -s <serial> push os\flash_dump\flash_full.bin <thatdir>` and same for `cg50_state.bin`.
>   4. Launch + check:  `& $adb -s <serial> shell am force-stop com.hexbinoct.cg50;`
>      `& $adb -s <serial> shell am start -n com.hexbinoct.cg50/.MainActivity;`  then
>      `& $adb -s <serial> logcat -d | Select-String "cg50    :|FATAL|UnsatisfiedLink"`.
>   ENV/paths: SDK `D:\files\Android_SDK`; NDK `28.2.13676358`; ADB `D:\files\Android_SDK\platform-tools\adb.exe`;
>   JDK `D:\Installations\Java\jdk-26.0.1`; pkg `com.hexbinoct.cg50`; phone serial
>   `adb-584c917b-jFkRGG._adb-tls-connect._tcp` (WiFi); emulator `emulator-5554`. App reads flash_full.bin +
>   cg50_state.bin from `getExternalFilesDir` = `/sdcard/Android/data/com.hexbinoct.cg50/files/`.
>
> **KEY FIX this session (cont.18i had a latent on-device crash):** the JNI shim's DT_NEEDED recorded the
> ABSOLUTE WINDOWS BUILD PATH of libcg50core.so (→ `UnsatisfiedLinkError: ...jniLibs/x86_64/libcg50core.so not
> found`) because the Go lib had NO SONAME. FIX in build_go_lib.ps1: build with
> `-ldflags=-extldflags=-Wl,-soname,libcg50core.so` so it records the basename; loader then finds it in jniLibs.
>
> **OPEN / NEXT (all "later"):**
>   1. **PERF — the MENU is slow on the phone** (calculations are fine). Likely emulation throughput on ARM vs
>      our pacing: `CalcSurfaceView.instrPerFrame`=333k @60fps=20M/s; if the phone can't sustain that in 16ms,
>      frames stretch. NEXT: measure real phone instr/s (add a Log of achieved ips in the render loop, like
>      `rtbench`), then tune instrPerFrame; consider a release APK (minor), CPU hot-path optimization, and not
>      stepping a fixed budget but a time-bounded one (step until ~12ms elapsed). The menu also redraws heavily.
>   2. **Release APK**: `:app:installRelease` (needs a signing config) for the non-debuggable build.
>   3. **Visuals**: user reports it looks good; if colors ever look red/blue-swapped → flip RGBA byte order in
>      Emulator.FramebufferRGBA / native-lib; aspect is currently stretch-to-fill (add letterbox if wanted).
>   4. Polish: SHIFT/ALPHA annunciators, long-press key repeat, nicer keypad styling, in-app flash-import UI
>      (instead of adb push), armeabi-v7a ABI if needed.
>   5. (pre-existing, low pri) the one third-party add-in "heronics2"/"howdy" renders blank (cont.18h).
> ⚠ Vision note: late in cont.18j the image API refused new screenshots (cumulative per-session limit) — a
> FRESH session resets this; use `adb exec-out screencap` + Read, or just ask the user, to see the screen.
> ⚠ Do NOT kill TCP :8080 (GhidraMCP). Android app lives in `android/`; see `android/README.md` + docs/ANDROID.md.
>
> ### ✅ cont.18f — PERSISTENCE: provision once, RESUME at the MAIN MENU (no first-boot setup).
> Goal: stop re-running the language/setup wizard every cold boot (the #1 Android UX blocker).
> **Why flash-only persistence is NOT enough:** the wizard is gated by `fls0_open` returning -6
> (FS not mountable) in `FUN_80365238` (verified in Ghidra 3.60 — the wizard `FUN_8035e1be` is called
> from the `iVar6 == -6` format branch @0x80365448). Snapshotting just the flash pages the OS wrote
> during setup and reloading them did NOT make fls0 mount (still -6 → wizard): the fls0/FTL mount state
> is coupled to battery-backed RAM the real calc keeps alive, which our boot zeroes.
> **Solution shipped — a full machine SAVE-STATE** (the right primitive for Android anyway: instant resume):
>   · `emu_go/state.go` — `SaveState`/`LoadState`: gzip snapshot of CPU regs + DRAM + ILRAM + OCRAM +
>     flash-delta (only flash pages differing from the image+0xFF baseline, so no OS image in the file).
>   · `emu_go/memory.go` — flash delta refactored to `flashDeltaBytes`/`applyFlashDelta` (+ standalone
>     `SaveFlashDelta`/`LoadFlashDelta`); `flashPersistPage`=0x1000.
>   · `emu_go/main.go` — mode **`provision`** boots fresh, drives first-boot to the MENU, and snapshots to
>     `os/flash_dump/cg50_state.bin` (git-ignored, OS-derived). EVERY OTHER boot auto-`LoadState`s and
>     RESUMES from the saved PC (cycles reset to 0; OS uses timer deltas so the counter reset is invisible).
> **VERIFIED:** `go -C emu_go run . 450000000 30000 provision` → snapshot @pc=0x801e535e, 85KB gzip.
> Then `go -C emu_go run . 60000000 30000` → boot_final.png = the **MAIN MENU** (full 4×3 app grid) in
> ~54M instr, NO setup keys. And `... seq "2-1*w60000000" 25000000 14000000` (resume + EXE) launches
> Run-Matrix live → the resumed machine is fully interactive, not a static frame. Tests 53/53→57/57 +
> golden + vet still green (persistence is main()/runtime-only; goldens boot fresh).
> NEXT: for a hands-free Android resume, snapshot-on-pause + resume-on-launch is exactly this; also worth
> a `savestate` hotkey in `web` mode so the user can snapshot AFTER doing their own setup/work.
> ⚠ Do NOT kill TCP :8080 (GhidraMCP).
>
> ### ✅ cont.18g — web save-state hotkey + Emulator FACADE + real-time loop + Android (cgo) BRIDGE.
> Three pieces toward the Android app, all validated on desktop:
> **(1) Web save-state hotkey.** `web` mode now auto-resumes from the save-state (skips the scripted
> first-boot drive when `resumed`) and exposes **F9=Save / F10=Reload** (buttons + keys → `/save`,`/load`
> endpoints; the CPU goroutine performs the op at a step boundary so there's no race). Tested live via curl:
> `/save`→"state saved", `/load`→"state reloaded", frame still served after (machine stays live).
> **(2) `Emulator` facade (`emu_go/emulator.go`)** — the single host-facing API the web UI and the bridge
> drive: `NewEmulator/Resume/Snapshot` (in-memory via new `SnapshotBytes`/`ResumeBytes` in state.go),
> `InjectKey` (thread-safe queue + the decode-confirmed injector), `Step`, `FramebufferRGBA`/`RGB565`, and
> `RunRealtime(targetIPS,frameHz,frame,stop)`. Unit-tested (`emulator_test.go`, `state_test.go`) — key queue,
> RGBA decode, snapshot/resume round-trip; no OS image needed.
> **(3) Real-time loop PROVEN** via new `rtbench` mode (`go -C emu_go run . 0 30000 rtbench`): resumes,
> paces to a target and reports — **target 20M ips → 19.95M achieved, 180 frames in 3.01s = 60 fps** (the
> desktop core does ~70M/s, so pacing genuinely caps + yields). On real HW (~118MHz, ~60-100M instr/s) we're
> already ~real-time; the loop's job is the cap + per-frame yield (battery/timer cadence) and a host blit hook.
> **(4) Android cgo BRIDGE (`emu_go/android_bridge.go`, build tag `android`)** — C ABI `EmuInit/Resume/Step/
> InjectKey/FramebufferRGBA/Snapshot/Free/Width/Height` wrapping the facade; build per-ABI with the NDK as
> `-buildmode=c-shared` → `libcg50.so`. Full build + Kotlin glue (run thread, Bitmap blit, keypad→InjectKey,
> provisioning) in **`docs/ANDROID.md`**. (Not built here — needs the Android NDK; the file is `android`-tagged
> so the desktop build/tests never touch cgo.) Tests stay green (57 conformance + golden + facade/state units),
> vet+gofmt clean. NEXT (needs Android tooling): NDK build + JNI shim + Studio project + on-ARM perf/tuning pass.
> ⚠ Do NOT kill TCP :8080 (GhidraMCP).
>
> ### ✅ cont.18h — FULL MENU APP SWEEP: all 18 standard apps work; 1 third-party add-in renders blank.
> Using the save-state resume (instant menu) + direct icon-key launch (pressing an icon's number/letter key
> launches it — no EXE needed; digits 1-9 and bare letter keys A=X,θ,T(6-6), B=log(5-6), C=ln(4-6), D=sin(3-6),
> E=cos(2-6), F=tan(1-6), G=a b/c(6-5), H=S↔D(5-5), I=`(`(4-5), J=`)`(3-5)), launched every menu app and
> screenshotted. **ALL 18 standard Casio apps launch + render correctly:** Run-Matrix (computes), Statistics,
> eActivity, Spreadsheet, Graph (+DRAW plots), Dyna Graph, Table, Recursion, Conic Graphs, Equation, Program
> (lists real progs), Financial, E-CON4, Link (Communication), Memory, System, Python (lists real .py files),
> Distribution. Menu is two pages (DOWN scrolls); nav EXE/DOWN/RIGHT + number/letter select all work.
> **ONE problem app:** the user's third-party CUSTOM add-in **J ("heronics2"/yellow "howdy" icon)** launches but
> the framebuffer goes blank-white immediately (seq_00=menu → seq_01=blank) and it does NOT respond to MENU
> (3-7). The emulator does NOT fault — it executes 260M+ instr fine — so it's the add-in itself hanging/looping
> or rendering to something we don't capture (different VRAM target / unmodeled feature), NOT an emulator crash.
> Non-standard add-in, low priority. To diagnose later: PC-histogram the J session (tight loop = hang vs varied
> = polling), and check for unmapped-MMIO reads during it. Bottom line: the emulator robustly runs the entire
> standard fx-CG50 app suite; only one custom third-party add-in misbehaves.
> ⚠ Do NOT kill TCP :8080 (GhidraMCP).
>
> ### ✅ cont.18i — ANDROID APP SCAFFOLD (Route A: cgo c-shared + JNI). Go→.so pipeline PROVEN here.
> User created an Android Studio "Native C++" project at `android/` (pkg `com.hexbinoct.cg50`, minSdk 30,
> AGP 9.2.1, NDK 28.2.13676358, CMake 3.22.1). Wired it all (Go core UNCHANGED):
>   · `android/build_go_lib.ps1` — cross-compiles emu_go → `libcg50core.so` per ABI (arm64-v8a + x86_64) into
>     app/src/main/jniLibs/ via the NDK clang. **RAN IT HERE: both ABIs build; arm64 .so = 9.1MB exporting all 9
>     Emu* symbols (llvm-nm verified).** The cgo bridge compiles for Android — biggest risk retired.
>   · `app/src/main/cpp/{CMakeLists.txt,native-lib.cpp}` — JNI lib `cg50` imports prebuilt `cg50core` and forwards
>     Java_com_hexbinoct_cg50_NativeBridge_* → Emu*. (JNI lib=libcg50.so, Go lib=libcg50core.so to dodge the name
>     clash; Kotlin loads cg50core then cg50.)
>   · Kotlin: `NativeBridge` (externals), `CalcSurfaceView` (render thread step+framebufferRGBA→Bitmap→canvas,
>     ~60fps, instrPerFrame=333k≈20M/s tunable), `KeyMap` (keypad from re/KEYMAP.md), `MainActivity` (loads
>     flash_full.bin + cg50_state.bin from getExternalFilesDir, init/resume, builds keypad, snapshots on onPause).
>     Layout SurfaceView(w3)+status+keypad(w5); abiFilters pinned to the 2 built ABIs; build artifacts git-ignored;
>     `android/README.md` documents build + adb-push + provision.
> CANNOT build the APK here (no Studio build env). NEXT: user runs build_go_lib.ps1, opens android/ in Studio,
> adb-pushes flash_full.bin (+ a desktop-`provision`ed cg50_state.bin), hits Run, reports — then iterate UI
> (annunciators, key-repeat, aspect/letterbox, perf) + maybe armeabi-v7a. ⚠ Do NOT kill TCP :8080.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-06-05 cont.18e)
>
> ### 🏆 cont.18e — ON-DEVICE PROBES LANDED: cmd4 solved + BCD model finalized + CPU core validated vs SILICON
> The user ran the probe add-ins on the real fx-CG50 (Mac/gint) and brought back the captures
> (`os/devic_probes/aluprobe_2/` + `os/devic_probes/alusweep_shtest/`, each with a `.md` analysis).
> Two big results, both now folded into the emulator:
>
> **(1) 0xA4CB0000 BCD-ALU — command set FULLY confirmed; cmd4 = A−B−1 (was provisional passthrough).**
> Final decode (16-bit cmd; bit3 ignored so 8..15 mirror 0..7):
>   `op = (cmd&1)?BCD-add:BCD-sub` ; `flag_in = (cmd&4)?1 : (cmd&2)?latch : 0` ; flag_out=carry/borrow.
>   ⇒ 0=A−B  1=A+B  2=A−B−flag  3=A+B+flag  **4=A−B−1**  5=A+B+1 (OS uses 0..4).
> CRITICAL on-device finding: there is **ONE SHARED carry/borrow latch** (probe C5: a sub's borrow-out
> feeds the next add as carry-in: after cmd4 sets borrow, cmd3 does 5+3+1=9). Our old model had two
> separate latches — **unified to a single `flag`**. Also: operands are sticky, result valid immediately
> (no busy bit), the 4-word block aliases every 0x10, and the shared flag is observable in the +00 status
> word (SET=0x10040000 / CLR=0x00010000 — modeled, optional). IMPLEMENTED in BOTH `emu/mmio.py` (`BCDALU`,
> single `flag`, `_compute`) and `emu_go/mmio.go` (`bcdALU.compute`). Tests: conformance 53/53 + 2M golden
> boot byte-identical (final PC 0x801df466, boot never touches the peripheral) + rewrote the cmd4/cmd5/
> shared-latch unit tests in `emu_go/bcdalu_test.go` to the hardware truth table; `go vet` clean.
>
> **(2) SH-4A CPU core VALIDATED AGAINST REAL SILICON — 202/202.** The shtest add-in captured real-hardware
> ground truth for addc/subc/negc/addv/subv, rotcl/rotcr, shad/shld, dmulu/dmuls, div1, cmp(all),
> munge (PART B of `alusweep_shtest/probe2-...md`). New `re/validate_silicon.py` replays every vector
> through the Python oracle and diffs vs hardware: **202/202 MATCH.** This closes the cont.18 "shared CPU
> bug" blind spot (lockstep only proves Go==Python; this proves Python==silicon) for the whole integer
> ALU/shift/mul/div/cmp set. (The only initial diffs were 6 div1 quotients = floor(q/2): a bug in the
> TEST's divide skeleton — it omitted the FINAL `rotcl` that shifts in the last quotient bit — NOT a div1
> bug. Fixed in the script; note the same harmless omission exists in conformance's `udiv` case, which is
> still a valid Go==Python check.) NOT yet covered by silicon vectors: mac.l S-bit saturation (probe has
> the data: S=0→60000000:00000001, S=1 saturates to 00007fff:ffffffff — fold in next).
>
> **NEXT-SESSION FIRST ACTIONS:** — ALL THREE DONE in cont.18e-2 (below).
> ⚠ Do NOT kill TCP :8080 (GhidraMCP). Tools added this session: `re/validate_silicon.py`.
>
> ### ✅ cont.18e-2 — the three follow-ups are DONE.
> **(a) mac.l IMPLEMENTED + silicon-validated.** The oracle had NO `mac.l` (it fell to IllegalInstruction).
> Implemented `mac.l @Rm+,@Rn+` with the S-bit 48-bit signed saturation in BOTH `emu/cpu.py` and
> `emu_go/cpu.go` (0x0-group d4==0xF). `re/validate_silicon.py` now also checks the probe's mac.l pair →
> **204/204 vs hardware** (S=0 → 60000000:00000001 full 64-bit; S=1 → 00007fff:ffffffff saturated).
> **(b) Silicon-anchored conformance cases folded in.** `emu/conformance_gen.py` +4 cases (now **57**):
> `div1_full_quotient_100_7` / `_ffffffff_3` (the FULL-quotient skeleton with the final rotcl — the older
> `udiv` case stays as a Go==Python-only check), `macl_no_saturation`, `macl_saturate48`. Their frozen
> expected outputs = the oracle's, which `validate_silicon` proves == real hardware, so the GO port is now
> held to silicon truth on these. Goldens regenerated (boot unchanged, final PC 0x801df466). Tests: go
> conformance 57/57 + 2M golden + vet clean; python 57/57; silicon 204/204.
> **(c) Arithmetic battery — typed into the running emulator, answers CORRECT.** With cmd4 now = A−B−1,
> `seq` typing into Run-Matrix renders: **`7×8` → 56**, **`100−37` → 63**, and the leftover `56÷36+3.14`
> → exact fraction **`2113/450`** (=4.69555…). Multiply, multi-digit subtract (borrow), and divide→fraction
> all compute correctly end-to-end through the real OS + our BCD-ALU model. (fpu_ops=0, all integer/BCD.)
> Repro: `go -C emu_go run . 850000000 30000 seq "<launch prefix>,<digit/op coords>" 130000000 14000000`
> with operators `+`=3-2 `-`=2-2 `*`=3-3 `/`=2-3 (full coord table re/KEYMAP.md), `*w` decode-confirmed pacing.
>
> ### ✅ cont.18e-3 — MULTIPLE APPS launch & run (menu nav verified in all directions).
> From the MAIN MENU (reach it with the launch prefix ending in ONE `2-1`, settle ~45M, then nav + EXE
> with `*w` pacing), three distinct apps were launched and render correctly:
>   · **Run-Matrix** (EXE on default top-left cursor) — computes (cont.18e-2).
>   · **Graph** (DOWN `2-7` → EXE `2-1`) — renders the `Y=` editor; pressing **F6=DRAW (`1-9`)** PLOTS the
>     function: axes + origin + the Y1=sin x curve. (Curve looks near-flat because the status bar is **Deg**
>     mode + default window ±6.3, so sin(6.3°)≈0.11 — a FAITHFUL render; it'd be a full wave in Rad.)
>   · **Statistics** (RIGHT `1-7` → EXE) — renders the List editor (List1-3 w/ data + SUB headers) + the
>     GRAPH/CALC/TEST/INTR/DIST softkeys.
> So menu nav (EXE / DOWN / RIGHT) + app launch + per-app rendering all work generically — not just
> Run-Matrix. The function plotter (trig + V-Window + pixel draw) works too. NEXT for breadth: sweep the
> rest of the grid (Equation/Spreadsheet/eActivity/Program/…) to find any app that hangs on unmodeled HW.
>
> ### 📱 ANDROID READINESS (assessment, 2026-06-05)
> Core is architecturally ready; the `web` mode already proves the exact interaction model (live framebuffer +
> injected keystrokes). The remaining work is a PORT, not research: (1) a **real-time run loop** (today it's
> batch `run N instr`; need continuous run with the timer IRQ paced to wall-clock + ~60fps blit); (2) a
> **gomobile/JNI bridge** exposing start/stop, injectKey(row,col), getFramebuffer() to Kotlin; (3) **fls0
> persistence** (or a saved RAM/flash snapshot) so cold start resumes at the MENU instead of first-boot setup —
> the real UX blocker; (4) **broader app/peripheral coverage** so apps beyond the 3 tested don't hang on
> unmodeled HW; (5) an **ARM perf check** (desktop is 64-85 M instr/s on x86). KEYS: all 47 keys + their
> SHIFT/ALPHA/A-LOCK secondaries are mapped authoritatively from the OS tables (re/KEYMAP.md); most primary
> keys + the SHIFT/ALPHA mechanism are verified live; a handful (AC/ON, a b/c, S↔D, →, VARS, EXIT, F2-F5,
> ALPHA B-Z) have correct codes but aren't individually click-tested yet — worth a quick verify sweep.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-06-04 cont.18d)
>
> ### 🏆 cont.18d — "RESULTS SHOW 0" FIXED & SHIPPED. The calculator now COMPUTES correctly.
> Implemented the 0xA4CB0000 hardware BCD ALU (cont.18c-2 model) in BOTH `emu/mmio.py` (oracle,
> class `BCDALU` + `_bcd_add`/`_bcd_sub`) and `emu_go/mmio.go` (`bcdALU` + `bcdAdd`/`bcdSub`),
> registered at 0xA4CB0000. **End-to-end verified:** typing `98765`+EXE in Run-Matrix now renders
> `98765`, and the leftover history `56÷36+3.14` now renders `4.695555556` (both were `0` before) —
> see seq_final.png. Tests: conformance 53/53 (unchanged) + 2M golden boot (byte-identical; boot never
> touches the peripheral) + 3 new unit tests in `emu_go/bcdalu_test.go`; `go vet` clean; goldens
> regenerated identical. Command model implemented: cmd1/3 = BCD add first/continue (latched carry),
> cmd0/2 = BCD sub first/continue (latched borrow), cmd4 = provisional passthrough.
> **STILL OPEN (minor):** cmd4 exact semantics + carry/overflow edge cases — awaiting the on-device probe.
> Probe build/run spec was pushed to the noted API: **read id 10** (the updated v2 with the "already
> fixed, now refinement" banner; id 9 is the stale original). User runs it on real HW, reports the truth
> table next session. Then refine cmd4 + edges in BOTH `emu/mmio.py` BCDALU and `emu_go/mmio.go` bcdALU
> (keep identical), regen goldens, `go -C emu_go test .`, re-verify Run-Matrix.
> **NEXT-SESSION FIRST ACTIONS:**
>   1. (no device needed) Broaden-verify the model: type an arithmetic battery into the emulator and check
>      answers — e.g. `go -C emu_go run . <budget> 30000 seq "<launch RunMatrix>+digits"` for `7*8`,
>      `100-37`, `2÷3` (round-half!), `999999*999999`, a negative result. Whatever's right = model
>      confirmed; whatever's wrong tells us which cmd/edge is off (and whether cmd4 is hit). The
>      reliable-typing recipe + digit inject coords are in cont.17/cont.14 (digits 9=`4-4` 8=`5-4` 7=`6-4`
>      6=`4-3` 5=`5-3`, EXE=`2-1`; +,-,×,÷ via KEYMAP.md). Self-test can't *derive* cmd4 — only the probe can.
>   2. When the probe truth table arrives, lock down cmd4 + edges (task #6) and re-verify.
> Everything else for number DISPLAY already works (98765, 4.695555556 render correctly).
> Probe-validation tool: `re/test_alu_hypothesis.py` (monkeypatches the ALU into the oracle off
> `fmt_snapshot.bin`); RE tools: `re/find_periph.py`, `re/periph_cmds.py`, `re/round_trace.py`.
> ⚠ Do NOT kill TCP :8080 (GhidraMCP).
>
> ## ⏯ (cont.18c) ROOT CAUSE — see below; fix shipped in cont.18d above
>
> ### 🎯🎯🎯 cont.18c — ROOT CAUSE of "results show 0" FOUND: an UNIMPLEMENTED on-chip peripheral @0xA4CB0000
> The "0" is **NOT** a CPU bug, **NOT** data/config, **NOT** the FPU. The Casio number-rounding path drives
> a **hardware BCD/arithmetic peripheral at 0xA4CB0010-0xA4CB001C that the emulator does not implement**, so
> its result register reads 0 and every formatted number collapses to 0. Found by tracing the full render
> chain in the oracle from `fmt_snapshot.bin` (tools: `re/fmt_probe.py`, `re/cfg_diff.py`, `re/round_trace.py`):
>   screen "0" ⇐ `FUN_800fc5a4`→`FUN_800f790e`(Norm)→`FUN_8004c21a`→`FUN_8004c69c`→`FUN_8004b270`→
>   `FUN_8004b2b0`(round-to-N-digits)→`FUN_8005dc06`→ **`de2c`=`FUN_80079dbc`** → **`FUN_80073f38`**
>   (mask-round: `value & keepmask`) → **`FUN_80072fc8`** = the PERIPHERAL ACCESSOR.
> PROOF (round_trace.py): the value `10 49 87 65` decodes correctly in software (`FUN_8004b580` → exp 4,
> mantissa 9,8,7,6,5); it is unpacked to `09 87 65 …` and rounding to 11 sig-digits SHOULD be a no-op, but
> `FUN_80073f38` loads value word0 `r11=0x09876500`, calls `FUN_80072fc8`, and **`r11` comes back `0`** — the
> accessor failed to return the operand because the HW result register read 0. Then `mask & r11 = 0` zeroes
> the mantissa, which propagates back out as the displayed result.
> **THE PERIPHERAL (FUN_80072fc8 @0x80072fc8):** stores operand words to `*0xA4CB0014`(A) + `*0xA4CB0018`(B),
> writes a **command** (`mov.w`, values seen = 1 then 3,3) to `*0xA4CB0010`, then **reads the result from
> `*0xA4CB001C`** back into r13/r12/r11. It processes the value's three 32-bit words. The reg pointers are
> literal-pool constants at 0x80073000/04/08/0c (= 0xA4CB0014/18/10/1C). **0xA4CB00xx is referenced NOWHERE
> in emu_go/mmio.go, emu/mmio.py, or these notes — completely unmodelled.** This OVERTURNS cont.16's
> "software BCD / fpu=0 so it's all software" conclusion (there IS a HW math/BCD unit) and cont.18b's
> "shared CPU semantic bug" guess (it's a missing peripheral, which is exactly why Go==oracle for 2M steps).
> **NEXT (task #5):** reverse-engineer the 0xA4CB0000 unit's command set + per-command semantics (RE every
> caller of `FUN_80072fc8` and every ref to 0xA4CB00xx; decode what cmd 1 vs 3 compute on opA with opB),
> then IMPLEMENT it in BOTH `emu_go/mmio.go` and `emu/mmio.py`, regen goldens (`python emu/conformance_gen.py
> && python emu/gen_golden.py`), prove the Go port matches, and add a conformance case. **Fastest RE path:
> a real-device probe** — a tiny add-in that writes known opA/opB + each command to 0xA4CB0010/14/18 and
> reads 0xA4CB001C — would reveal the operation directly (this is the high-value use of a re-dump/on-device
> run the user offered). ⚠ Do NOT kill TCP :8080 (GhidraMCP).
>
> ### ✅ cont.18c-2 — COMMAND SET REVERSE-ENGINEERED + VALIDATED (oracle renders "98765"). Probe note sent.
> Register map (flash scan, 137 refs): **0xA4CB0010**=command(16-bit w), **0xA4CB0014**=opA(32-bit),
> **0xA4CB0018**=opB(32-bit), **0xA4CB001C**=result(32-bit r). It is a **multi-word BCD ALU** (packed BCD,
> 2 digits/byte; mantissa = 3 words, val[0]=MSW, processed LSW-first with carry/borrow latched in the unit
> between words). Shifts/masks are PURE SOFTWARE (SHLD, e.g. FUN_8007350a), NOT this peripheral — the unit
> does only the add/subtract core. **Command set {0,1,2,3,4}; encoding: cmd bit1 = first(0)/continue(1)
> word, cmd bit0 = sub(0)/add(1):**
>   · cmd 1 = BCD ADD first (carry=0)   · cmd 3 = BCD ADD continue (latched carry)
>   · cmd 0 = BCD SUB first (borrow=0)  · cmd 2 = BCD SUB continue (latched borrow)
>   · cmd 4 = third op, SINGLE use @0x800737f2 — only remaining unknown (passthrough guess works on this path)
> EVIDENCE: accessors group as (1,3,3)/(0,2,2); the (0,2,2) accessor FUN_8007306e writes operands SWAPPED
> vs the (1,3,3) adder FUN_80072f80 (A<-r9/B<-r13 vs A<-r13/B<-r9) — the non-commutative signature of
> SUBTRACT. The (1,3,3) op is the magnitude core of FUN_80072f2a, called from FUN_80072e78 = a classic
> float-combine (compare exps/signs, then add-or-copy). **VALIDATION (re/test_alu_hypothesis.py, NO device):**
> monkeypatched this ALU into the Python oracle, re-ran the formatter from fmt_snapshot.bin → the OS now
> emits ASCII **"98765"** (string `39 38 37 36 35` @0x8c1866f0; display width 90px=5 glyphs). Model CORRECT
> for the display path. **Probe note PUSHED to noted API (id 9)** for on-device confirmation of cmd4 +
> carry/overflow edges (user runs on real HW, reports next session).
> **NEXT:** implement 0xA4CB0000 in BOTH emu_go/mmio.go + emu/mmio.py (cmd0-3 = BCD sub/add w/ latched
> carry-borrow; cmd4 = passthrough until probe), regen goldens (boot unaffected — peripheral only used in
> number formatting), prove Go==oracle, add a conformance case, run the full emulator and confirm Run-Matrix
> shows 98765. Refine cmd4/edges from probe data. Tools: re/find_periph.py, re/periph_cmds.py,
> re/test_alu_hypothesis.py, re/alu_probe_note.md.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-06-04 cont.18)
>
> ### 🧭 cont.18 PIVOT (read FIRST — overturns cont.17c's "mis-emulated instruction" lead)
> Built a Go→Python **oracle lockstep diff** for the formatter and ran it: **ZERO divergence over
> 2,000,000 steps** from the formatter entry (FUN_800fc5a4) through the MathIO redraw (final pc
> 0x80056eaa, i.e. PAST glyph rendering). So the Go core and the independent Python oracle compute
> the whole formatter+redraw **bit-for-bit identically, and BOTH render "0"**. ⇒ The rendered "0" is
> **NOT a Go port error** — the OS code faithfully emits "0" given this machine state. The remaining
> bug is therefore EITHER (a) a **data/config global** the formatter reads that is wrong in our boot
> (display Norm/Sci/Fix mode, digit count, an uninitialised display-config field — note uVar17 =
> (short)puVar18[3]>>8&0xff gates the format branch; for BCD 10 49 87 65 00 00 puVar18[3]=0x0000 so
> uVar17=0 → normal-number branch, as expected), OR (b) a **shared CPU semantic bug** implemented
> IDENTICALLY in both emulators (a lockstep diff is blind to a bug present in both — it only catches
> PORT mismatches). Since eval/arith STORE the correct result (cont.17b) and the formatter receives
> the correct BCD in r4, (a) is the leading hypothesis.
> **TOOLING ADDED (harness-only, tests stay 53/53+2000/2000, vet clean):**
>   · `captureFormatter()` in emu_go/main.go — at the first armed entry to FUN_800fc5a4 with r4 BCD
>     == 0x10498765, dumps FULL machine state (36 regs + dram + ilram + ocram) to
>     `emu_go/fmt_snapshot.bin`, then PURELY single-steps K (=2,000,000) instrs (no MMIO tick, no IRQ)
>     logging arch state to `emu_go/fmt_trace_go.txt`. Trigger gated in the `bcdseq` loop.
>   · `re/oracle_diff.py` — loads the snapshot into the Python reference CPU, steps the SAME pure way,
>     diffs line-by-line; first divergence would pin the mis-emulated instruction. (Currently: none.)
> Reproduce snapshot: `go -C emu_go run . 850000000 30000 bcdseq "<98765+EXE seq, see cont.17>" 130000000 14000000`
> **cont.18b — FULL RENDER CHAIN TRACED; bug pinned to the BCD ROUNDING leaf. Value & config both PROVEN
> correct.** Using `re/fmt_probe.py` (runs the formatter from `fmt_snapshot.bin` in the oracle, freely
> instrumented) + `re/cfg_diff.py` (resolves flash literal-pool pointers; diffs config vs real dumps).
> The "0" comes from this exact chain (all fed the CORRECT value, all Go==oracle):
>   `FUN_800fc5a4`(top fmt) → `FUN_800f790e`(0x800f790e, mode dispatch; mode byte *(DAT_800f79f8+6)=1=Norm)
>   → `FUN_8004c21a`(0x8004c21a, Norm renderer) → `FUN_8004c69c`(0x8004c69c, digit emit)
>   → **`PTR_FUN_8004c810`=`FUN_8004b270`(0x8004b270)** → **`FUN_8004b2b0`(0x8004b2b0, round-to-N-digits)**
>   → **`PTR_FUN_8004b494`=`FUN_8005dc06`(0x8005dc06)** ⇐ THE LEAF that zeroes the value.
> KEY PROOFS:
>   · **Value is CORRECT.** The decoder `FUN_8004b580`(0x8004b580) decodes our BCD `10 49 87 65` by hand
>     to: class nibble=1, **exponent = (0x49>>4) = 4**, mantissa nibbles **9,8,7,6,5** = 9.8765×10⁴ ✓,
>     and returns success. So eval/store/parse are all fine — overturns any "value is wrong" worry.
>   · **Config/mode is byte-IDENTICAL to the real device.** `DAT_800f79f8`→0x8c08b8a0 (mode *(cfg+6)=0x01
>     =Norm) and `DAT_800f7a08`→0x8c08b8ac are identical in our snapshot vs `os/flash_dump/dram.bin`.
>   · **The zeroing is localized.** In `FUN_8004c69c`, the value working-copy `auStack_2c` is `10 49 87 65`
>     right after the 0x18-byte copy and SURVIVES `FUN_8004c654`, then is **ALL-ZERO immediately after the
>     `FUN_8004b270`→`FUN_8004b2b0` call** (Norm path calls it with precision arg = hardcoded 0). Inside
>     `FUN_8004b2b0` the decode is correct (exp 4, digits 98765, no leading zeros, Norm⇒round to 11 sig
>     digits), then `PTR_FUN_8004b494`(&value, out, 11) writes a ZERO result, copied back via
>     `PTR_FUN_8004b498/49c`. So **rounding 98765 to 11 sig-digits yields 0** — that is the bug.
>   · **`FUN_8005dc06` is in the 0x8005c000-0x8005f000 BCD module cont.17c independently flagged** (its
>     sub-calls `PTR_FUN_8005de1c/de24/de2c`; `PTR_FUN_8004b498`=0x8005ed3a ≈ cont.17c's NaN-check
>     FUN_8005ed50; cont.17c's digit-loop FUN_8005c84e is in the same module). Chain now fully connected.
> SINCE Go==oracle for all 2M steps AND the decoder is provably correct, the fault is almost certainly a
> **SHARED CPU semantic bug** in an instruction the BCD rounding loop uses (implemented identically-wrong
> in BOTH emulators, so the lockstep diff is blind to it) — NOT a port error and NOT data/config.
> **NEXT (cont.18b hand-off):** single-step `FUN_8005dc06`+`PTR_FUN_8005de24/de2c` (and `FUN_8005c84e`) in
> the oracle on this value+precision-11, find the exact instruction where the mantissa accumulator → 0,
> then verify THAT opcode's semantics against the SH7305/SH-4A manual (suspects: a BCD-relevant op the
> rounding uses that arith/decode don't — e.g. a shift/`mac`/`div1`/`rotc`/`negc`/`clip` edge case). Fix
> in BOTH emu/cpu.py (oracle) and emu_go/cpu.go, regen goldens, add a conformance case for it.
> Tools this session: `re/fmt_probe.py`, `re/cfg_diff.py` (both run off `fmt_snapshot.bin`; need the
> real dumps for cfg_diff). ⚠ fmt_trace_go.txt is ~400MB — regenerate on demand; keep the 10MB snapshot.
> **Real-dump note:** `os/flash_dump/{dram,ilram}.bin` are REAL-device RAM but were captured under the
> gint dumper add-in (not Run-Matrix), so volatile per-number state isn't comparable; a fresh dump taken
> WHILE the calc displays a known result (e.g. type 98765 EXE then dump) would give a canonical value+
> display-context to diff against — worth asking the user for if the shared-bug hunt stalls.
> ⚠ Do NOT kill TCP :8080 in cleanup — that's the GhidraMCP plugin's port.
>
> ## ⏯ (prev) RESUME HERE (last session end: 2026-06-03)
>
> ### ★ STATE IN ONE LINE (authoritative — cont.16/15/14/13 below supersede ALL older "open gate" notes)
> The Go emulator boots the REAL fx-CG50 OS 3.60 from reset, **drives first-boot setup to the MAIN
> MENU, launches Run-Matrix, and is fully keyboard-driven** (verified keymap `re/KEYMAP.md`; live
> browser UI via `go -C emu_go run . 0 30000 web`). Reach the app from reset with:
> `go -C emu_go run . 420000000 30000 seq "1-9,1-9,1-9,1-9,6-9,6-9,1-9,1-9,2-1,2-1,2-1" 130000000 14000000`
> → seq_final.png = Run-Matrix app. (Drop the last two `2-1` and use 360000000 to stop at MAIN MENU.)
> Tests 53/53 + 2000/2000 green, go vet clean, Python oracle in sync.
> **DONE (cont.14):** full keymap verified by typing (decode-confirmed pacing). **(cont.15):** interactive
> web UI. **(cont.16):** DIAGNOSED "results show 0" = the number EVALUATOR yields 0 for all arithmetic
> (NOT a display bug, NOT the FPU) — parser/dispatch/error-detection work, but every value/op resolves
> to 0. Proof = domain-error tests (`1÷(5-2)`→err so 5-2=0; `√(2-5)`/`√(-5)`→no err so operands=0).
> **NEXT OPEN ITEM:** (a) **"results show 0" is a BCD→DISPLAY FORMATTER bug — RESOLVED (cont.17b), cont.16
> overturned.** Arithmetic & value path WORK: typed `2+3` stores the correct result `5` (BCD `10 05 00` at
> 0x0c0d83f0/8658, found by all-DRAM scan) and `98765` stores `10 49 87 65` at 0x0c0d8088 — both display `0`.
> So parse/string→BCD/arith/store all work; the BCD→glyph FORMATTER renders a correct BCD as "0". **Formatter
> LOCATED (cont.17c): `FUN_800fc5a4`** (BCD→MathIO display obj, called from 0x800fcc90 with the correct BCD;
> low-level BCD module 0x8005c000-0x8005f000). FPU ruled out (fpu_ops=0). NEXT: single-step FUN_800fc5a4 (or
> diff vs Python oracle) to pin the instruction/global where `10 49 87 65`→"0". (b) fls0 persistence. (c) other apps.
> ⚠ Do NOT kill TCP :8080 in cleanup — that's the GhidraMCP plugin's port.
> Read cont.17/cont.16/cont.15/cont.14/cont.13 first, then cont.12/cont.11/cont.10.
>
> ### ⌨ AUTHORITATIVE KEYMAP: `re/KEYMAP.md` (generated by `python re/dump_keymap.py`)
> Full physical-key → inject-coord → {primary/SHIFT/ALPHA/A-LOCK} code table for ALL 47 keys, dumped
> straight from the OS tables (matrix `DAT_805ff7ec`; remap `FUN_80194dda`/`FUN_80194e3c`) + the
> empirical verifications below. Supersedes the partial keymap in cont.12. Inject grid (C,R) as
> `"{R-1}-{C-1}"`. SHIFT(`6-8`)/ALPHA(`6-7`) are real keys you inject *before* the target; the OS runs
> its own modifier state machine. Verified live this session: arrows UP `1-8`/DOWN `2-7`/LEFT `2-8`/
> RIGHT `1-7`, EXE `2-1` (launch), MENU `3-7` (back to menu), F1`6-9`..F6`1-9`, SHIFT/ALPHA annunciators.
>
> ### 🔢 2026-06-03 (cont.16) — "results show 0" DIAGNOSED: the number evaluator yields 0 (NOT a display bug)
> Run-Matrix shows every result as 0. Investigated and pinned the SYMPTOM precisely:
> - **NOT the FPU.** Instrumented an FPU-op counter (cpu.fpuOps on the 0xF-opcode stub): **fpu=0**
>   across the whole boot+eval — the OS uses zero floating-point; Casio math is software BCD/integer.
> - **NOT (only) the display.** Proved the computed VALUES are genuinely 0 with domain-error tests that
>   don't depend on reading the result glyphs (the eval RAISES a real "Ma ERROR" dialog on bad domains):
>     · `1÷0` → Ma ERROR (eval runs; div-zero check works)
>     · `1÷(5-2)` → Ma ERROR  ⇒ `5-2` evaluated to **0** (else 1÷3, no error)
>     · `√(2-5)` → NO error    ⇒ `2-5` is **0/non-negative**, not -3 (else √neg → Ma ERROR in Real mode)
>     · `√(-5)`  → NO error    ⇒ operand resolves to **0** (even a literal+unary-minus)
>   So parsing + dispatch + error-detection WORK, but every numeric value/operation resolves to 0.
> - **Red herring:** an ASCII "33333" appears in DRAM (0x0c0d53xx) after `11111+22222`, which briefly
>   looked like a correct result, but the error-tests above override it — that region is eval scratch.
> - Memory note: a literal like `98765` is tokenised to BCD `49 87 65` (exp nibble 4 + mantissa) at
>   ~0x0c186xxx / 0x0c0d80xx / ilram — so the PARSER works; it's the eval's number path that gives 0.
> NEXT: find the BCD operand-load / arithmetic-core routine (eval runs but reads/produces 0). Profiling
> the eval window is swamped by the MathIO redraw (0x80055-57 gfx = FUN_80056d7c bounds 0x180×0xd8;
> 0x8004e272 = LCD push to _DAT_b4000000); need a read-watch on the parsed literal's BCD address to find
> the operand-fetch PC, then decompile it. All diagnostics were reverted; tests 53/53+2000/2000 green.
> ⚠ Don't kill TCP :8080 in cleanup — that's the GhidraMCP plugin's HTTP port (briefly disrupted it).
>
> ### 🔬 2026-06-04 (cont.17) — BCD operand read-watch BUILT; parse→BCD proven OK, fault is downstream
> Built the read-watch the cont.16 plan called for and pinned the operand. Findings:
> - **New harness mode `bcdseq`** (emu_go/main.go, gated by `watch` arg to runSeq; emu_go/memory.go has
>   a gated DRAM read-watch `rdLo/rdHi/rdPC/cpu`, nil = off so goldens/normal runs are unaffected —
>   tests still 53/53 + 2000/2000, vet clean). It scans DRAM for a typed literal's BCD bytes, excludes
>   pre-existing copies, locks a tight watch window, and histograms the PCs that READ it.
> - **RELIABLE TYPING recipe (important, supersedes ad-hoc):** to type INTO Run-Matrix you must use the
>   FULL confirmed launch prefix with **three** `2-1` EXEs (`…,2-1,2-1,2-1`) THEN the digits — the 3rd
>   EXE is what actually lands in Run-Matrix. With only two `2-1`, the digit keys land on the STILL-OPEN
>   MAIN MENU (a digit jumps to a numbered icon) and you launch the wrong app (saw Conic Graphs). After
>   the prefix add a ~40M settle gap, then type each digit decode-confirmed `*w`. Working command:
>   `go -C emu_go run . 850000000 30000 bcdseq "1-9,1-9,1-9,1-9,6-9,6-9,1-9,1-9,2-1,2-1,2-1*40000000,4-4*w18000000,5-4*w18000000,6-4*w18000000,4-3*w18000000,5-3*w18000000,2-1*w30000000" 130000000 14000000`
>   → types **98765** then EXE; seq_final shows `98765` with result column `0` (bug reproduced cleanly).
>   Digit inject coords: 9=`4-4` 8=`5-4` 7=`6-4` 6=`4-3` 5=`5-3`, EXE=`2-1`. Run-Matrix getkey = 0x801de9e4.
> - **Operand located:** literal 98765 → BCD `49 87 65` appears at phys **0x0c186009** (+0x15,+0xe9 copies,
>   and a far copy ~0x0c18722d) during eval — so the OS DOES produce the correct BCD; PARSE is fine.
>   (Note: 5-digit literals pack neatly to 3 bytes `[exp|d1][d2 d3][d4 d5]`, e.g. 98765=9.8765e4→`49 87 65`;
>   6-digit 999999=9.99999e5→`59 99 99 9_` does NOT give a clean `99 99 99`, so use 5-digit literals.)
> - **Readers of the BCD operand (tight window 0x0c186000-0x0c186100, eval-time):** dominated by
>   **FUN_801db382 = memcmp** (~400 reads, parse/compare), then 8-byte BCD routines **FUN_802104e4**
>   (copies 8 bytes via PTR_FUN_8021068c/90/94) and **FUN_8020ecda** (normalize loop, count<0x94). The
>   BCD math library lives ~**0x8020e000–0x80211000**. The wide-window noise (0x80384a4x, 0x80056xxx,
>   0x8004exxx) is the MathIO redraw/LCD-push reading the adjacent display struct at ~0x0c1862xx — ignore.
> - **CONCLUSION:** the fault is NOT in parse/tokenise (correct BCD `49 87 65` exists) — it's in the BCD
>   VALUE path (copy/normalize/arith → result). NEXT: single-step the eval window through 0x8020e000–
>   0x80211000, watch where the 8-byte BCD value turns to 0, and diff that instruction against emu/cpu.py.
>
> ### ✅ 2026-06-04 (cont.17b) — RESOLVED: it's a BCD→DISPLAY FORMATTER bug. Arithmetic & value path WORK. cont.16 WRONG.
> Built a call-tree tracer + read-watch on the Ans region + an end-of-run ALL-DRAM BCD scan. The all-DRAM scan
> is the decider (a too-narrow numlib whitelist had briefly produced a false "2+3 stores no result" scare —
> disregard that intermediate worry; the adder runs outside the whitelist):
> - **DECISIVE: typing `2+3` (on-screen result `0`) DOES compute & store the correct result.** End-of-run
>   ALL-DRAM scan finds operand `2`=BCD `10 02 00` (10 hits), operand `3`=`10 03 00` (13 hits), and **result
>   `5`=`10 05 00` present at 0x0c0d83f0 / 0x0c0d8658 / 0x0c0da824** (the Ans/result region). Value computed
>   correctly; only the on-screen rendering is `0`.
> - **Confirming:** typing `98765` leaves correct BCD `10 49 87 65` (sign 0x10, exp 4, mantissa 98765) at phys
>   0x0c0d8088, read back intact — yet displays `0`.
> - **THEREFORE: parse, string→BCD (FUN_801dab86), arithmetic, and result-storage ALL WORK.** The bug is the
>   BCD→glyph FORMATTER (renders the result column in the MathIO redraw): it reads a correct BCD and emits "0".
>   cont.16's "number evaluator yields 0" is OVERTURNED; its `1÷(5-2)`→err evidence was a mistyped-input
>   artifact of the OLD unreliable typing.
> - **BCD value format (confirmed):** 8 bytes = [sign/flags 0x10][exp_nibble | mantissa nibbles…]; e.g.
>   5→`10 05 00…`, 98765(=9.8765e4)→`10 49 87 65 00…`. Result/Ans values live in DRAM ~0x0c0d6000-0x0c0daxxx;
>   editor echo/edit-line ~0x0c0d8080 & 0x0c186xxx.
> - **NEXT (task #7):** find the BCD→glyph formatter — watch who reads the result BCD (e.g. 0x0c0d83f0 for the
>   `5`) during the MathIO redraw, decompile it, find why a correct BCD renders as "0" (likely a mis-emulated
>   instruction or a wrong field/offset read IN THE FORMATTER). This is THE remaining bug for correct results.
> Tools added: `bcdseq` call-tracer (eval_calls.txt) + Ans-region hexdump + all-DRAM BCD scan + re/analyze_eval.py
> + re/read_ptrs.py. Tests stay 53/53 + 2000/2000 green, vet clean (all instrumentation gated behind `watch`).
>
> ### 🎯 2026-06-04 (cont.17c) — FORMATTER LOCATED: FUN_800fc5a4 (fed correct BCD, emits "0"). FPU ruled out.
> Broadened the call-tracer whitelist to the calc/format (0x800e..0x80100000) + render (0x80050..0x80060000)
> modules and dereffed args to find the call that RECEIVES the result BCD. Found the formatter chain:
> - **The result FORMATTER is `FUN_800fc5a4`** (BCD-real → MathIO display object), called from `0x800fcc90`
>   with the correct result BCD in r4 (verified: `[451625840] 0x800fcc90 -> 0x800fc5a4 r4=0x8c18721c
>   [1049876500000000] r6/r7=output buffers`). It returns the (wrong) display object that renders "0".
> - Low-level BCD digit/normalize work is a module at **0x8005c000-0x8005f000** (e.g. FUN_8005c84e =
>   BCD normalize/repack reading exp nibbles + copying 8 bytes; FUN_8005ed50 = "byte&0xf0==0xf0?" special/NaN
>   check). The mantissa IS extracted correctly mid-way (saw r7=0x09876500 in FUN_8005c84e), so digit
>   extraction works — the loss is later (exponent/digit-count/decimal-placement or a wrong global/config).
> - **FPU RULED OUT:** instrumented fpu_ops over the whole eval+format window = **0**. Formatter is pure
>   integer/BCD; the bug is a non-FPU instruction the formatter uses (that arith/parse don't) OR a wrong
>   DAT_ global/config it reads (FUN_800fc5a4 is heavy on PTR_FUN_800fc8xx indirection + DAT_800fc7xx fields,
>   incl. reads of display attrs 0x60/0x62 and a Norm/Sci/Fix-ish mode field puVar18[3]>>8).
> - **LEAD (digit extraction truncates early):** in FUN_800fc5a4's BCD→decimal loop (driver ~0x8005d136/
>   0x8005d15c calling FUN_8005c84e + memset 0x80385178), the mantissa accumulator (seen in r7) goes
>   0x9876500 → 0x8765000 (one BCD-digit left-shift, correct) → **0x0** — jumping to 0 after ~2 digits instead
>   of peeling all 5 (9,8,7,6,5). Looks like a digit-shift/peel step zeroing early → renders "0". NOT yet
>   confirmed at instruction level (r7 may not be the true accumulator; these BCD fns are dense + indirection-
>   heavy). **NEXT:** single-step FUN_8005c84e's digit loop (PC-windowed full PC+reg trace over 0x8005c000-
>   0x8005f000 for the ONE format invocation @~451.6M) to pin the instruction where the mantissa→0; or diff vs
>   the Python oracle. Caution: real-HW results (leftover 4.6956) ALSO render as 0, so it's our emulation
>   diverging (mis-emulated instr OR accumulated-wrong-state), not the data.
>
> ### 🖥 2026-06-03 (cont.15) — INTERACTIVE WEB UI: play the calc live in a browser
> New emu_go mode **`web`** (emu_go/webui.go): `go -C emu_go run . 0 30000 web [port]` boots + auto-
> drives first-boot setup to the MAIN MENU, then serves the live framebuffer + accepts keystrokes so
> you can USE the calc. Zero external deps (stdlib net/http + image/png; the browser is the window).
> Open the printed URL (defaults 127.0.0.1:8080, falls back to 8123/8973/OS-assigned — Windows
> excludes 8080's range → "forbidden" bind, the fallback handles it). PC→matrix keymap in webui.go
> (digits/ops/`.`/`( ) ,`, Enter=EXE, Backspace=DEL, Esc=EXIT, Home=MENU, arrows, F1-F6, Tab=SHIFT,
> backtick=ALPHA, a-z=ALPHA+letter). User keypresses inject via the SAME decode-confirmed path
> (re-inject until FUN_801952cc runs) so presses don't drop. Framebuffer read straight from the DRAM
> byte slice (phys 0x0c000000=DramBase) — benign race, no map. Harness-only; tests stay green.
>
> ### ⌨🏆 2026-06-03 (cont.14) — RELIABLE TYPING + every key class verified in Run-Matrix
> Built decode-confirmed key pacing and used it to type real expressions into Run-Matrix, verifying
> the keymap end-to-end. Key findings:
> - **Decode-confirmed pacing** (`seq` token `r-c*wGAP`): a key injected while the app is REDRAWING is
>   flushed (app clears pending input before its next getkey) and never decoded — fixed delays
>   phase-lock onto the redraw and silently drop keystrokes (saw 0/10 .. 8/10). FIX: after injecting,
>   wait until the OS decode `FUN_801952cc` (0x801952cc) actually runs for the key (re-inject if it
>   doesn't fire within 40M instr), then settle. With this, full `1234567890` and `8+5-2*3/4` type
>   cleanly. Plain fixed `interval` still used for setup/menu nav. (emu_go/main.go runSeq + keySafe +
>   keyQueueCount; harness-only, tests stay 53/53+2000/2000.)
> - **Decimal-point key FOUND:** matrix cell C2R6 holds raw code **0x00** (my dumper had skipped raw 0
>   as "empty"); it's the real `.` key → inject `5-1`. Codes match its labels exactly: primary 0x2e `.`,
>   SHIFT 0x3d `=`, ALPHA 0x20 space. `re/dump_keymap.py` now special-cases it (REAL_RAW0).
> - **Verified by typing/observing:** digits 0-9, `.`, `+ - * /`, `(-)`, `x10^x`, `EXE` eval, `sin cos
>   tan log ln`, `X,θ,T x² ^ ( ) ,`, `DEL`, `OPTN` (opens LIST/MAT-VCT/… softkeys), `MENU` (→ menu),
>   arrows, F1/F6. **Modifiers proven:** SHIFT+`sin`→`sin⁻¹`, ALPHA+`X,θ,T`→`A` (inject the modifier
>   key before the target; OS state machine applies the yellow/red meaning).
> - `re/KEYMAP.md` regenerated with the `ver ✓` column + the dot. NOTE: a fresh Run-Matrix shows
>   leftover history "sin 8 / 56÷36+3.14" and results render as "0" — uninitialised history RAM /
>   possible math-eval gap, not chased yet.
>
> ### 🏆🏆🏆 2026-06-03 (cont.13) — APP LAUNCH SOLVED: Run-Matrix runs from the MAIN MENU
> The open item from cont.12 is done — we launch an app from the menu. It was NOT a new gate; it
> just needed (a) more instruction budget and (b) an EXE press AFTER the menu has fully rendered.
> Sequence (extends cont.12's menu sequence by two `2-1`/EXE presses, budget 360M→420M):
> `go -C emu_go run . 420000000 30000 seq "1-9,1-9,1-9,1-9,6-9,6-9,1-9,1-9,2-1,2-1,2-1" 130000000 14000000`
> Key timeline (pressAt 130M, 14M spacing):
>   - key#8 EXE @242M = dismisses the "Add-ins installed. Press:[EXE]" note → MAIN MENU renders
>     (seq_09 = menu, FBHASH ef2bd5e7165378fc).
>   - key#9 EXE @256M = launches the highlighted **Run-Matrix** (cursor defaults top-left on the
>     menu) → app shell takes over (seq_11 = Run-Matrix edit screen drawing).
>   - key#10 EXE @270M = harmless EXE inside the app (evaluates the empty entry line).
> seq_final.png = Run-Matrix: status bar `Math Deg Norm1 [d/c] Real`, entry cursor box, softkeys
> `JUMP DELETE ►MAT/VCT MATH`; FBHASH 5307839d3bd21ba4. (Two demo-ish lines "sin 8" / "56÷36+3.14"
> appear in the history area — likely uninitialised history RAM or placeholder; not yet chased.)
> So the EXE→app-launch path through the menu shell works generically; pressing EXE on any selected
> icon should launch that app (Run-Matrix verified). NO code change — harness args only; tests stay
> 53/53 + 2000/2000 green, oracle unaffected. NEXT: number-pad matrix coords to type into Run-Matrix.
>
> ### 🏆🏆🏆 2026-06-02 (cont.12) — KEYBOARD INPUT SOLVED: driven from reset to the MAIN MENU
> Keyboard injection works and we drove first-boot setup to completion. HOW (the faithful path):
> instead of modelling the KEYSC IRQ + matrix-data-reg format, we **call the OS's OWN scan-enqueue
> routine `FUN_801e684c` as a subroutine** from the harness at a safe idle point. New CPU primitive
> `cpu.callInject(addr, args…)` (emu_go/cpu.go): snapshots ALL arch regs, runs the fn on a stack
> lowered 0x40 below sp with interrupts masked (sentinel pr=0xDEAD0000), restores everything — so it
> executes atomically and normal execution resumes untouched. `injectKey(row,col)` writes a 2-byte
> {row,col} scratch at sp-8 and calls it. The key lands in EXACTLY the queue the UI consumes
> (verified: count 0→1, row/col stored as +1, then the OS consumes it → count back to 0). It is NOT
> used by step()/normal runs, so goldens are unaffected.
> Harness modes added to emu_go/main.go:
>   - `key <row> <col> <pressAt>` — inject one matrix key, dump frames + queue state + FBHASH.
>   - `seq "<r-c,r-c,…>" <pressAt> <interval>` — inject a SEQUENCE (0-based matrix coords) spaced
>     `interval` instr apart from `pressAt`, dumping a PNG per key. This is the UI driver.
> Queue layout (3.60 kbd driver): pointers at 0x801e6a1c=&count, 0x801e6a20=&writeIdx,
> 0x801e6a24=rowBuf, 0x801e6a28=colBuf, 0x801e6a2c=modBuf, 0x801e6a40=&readIdx (all runtime ptrs;
> rowBuf/colBuf live ~0x8c090c48). Enqueue stores row+1,col+1. Consumer FUN_801e6994 peeks; the
> decode FUN_801952cc maps via table DAT_805ff7ec[col*0x1c + row*4] (queued col,row → grid).
> **KEYMAP (empirically swept on the live UI; re/key_sweep*.py). To hit grid (C,R) inject
> row=R-1, col=C-1:**
>   - EXE        = grid C2 R3  (code 0x1f) → inject "2-1"
>   - DOWN       = grid C8 R3  (code 0x32) → inject "2-7"
>   - SHIFT      = grid C9 R7  (code 0x21);  ALPHA = grid C8 R7 (code 0x22)
>   - F1..F6     = grid column C10, rows R7..R2 = codes 0x24,0x25,0x26,0x27,0x28,0x29
>                  → F1="6-9"(0x24)  F2="5-9"  F3="4-9"  F4="3-9"  F5="2-9"  F6="1-9"(0x29)
>   - DIAGNOSTIC trigger = grid C12 R4 (code 0x3c) — AVOID.
> SETUP FLOW that reaches the menu (each setup screen's softkeys): Language→Display→Power→Battery
> all advance on **F6=Next ("1-9")**; on **Battery Settings**: **F1=SELECT ("6-9")** raises a
> "WARNING! … OK? Yes:[F1] No:[F6]" confirm → **F1=Yes ("6-9")** → back to Battery → **F6=Finish
> ("1-9")** → "Note: Add-ins deleted by Reset1 are installed. Press:[EXE]" → **EXE ("2-1")** →
> MAIN MENU. (Timing: pressAt 130M, 14M spacing; some presses land during a screen transition and
> are absorbed, so the working sequence uses 4 Nexts to reach Battery.)
>
> ### 🏆🏆🏆 2026-06-02 (cont.10) — IT'S ALIVE: emulator RENDERS the real fx-CG50 boot screen!
> RENDER GATE SOLVED. Two fixes:
>   1. **VRAM uncached-mirror routing.** The OS draws VRAM via **0xAC000000** (P2 uncached mirror of
>      phys 0x0C000000). memory.go/.py routed ALL of 0xA4000000-0xC0000000 to MMIO, so VRAM draws were
>      DROPPED (showed as unmapped writes to 0xac0xxxxx). FIX: in Read AND Write, route any va whose
>      `phys = va&0x1FFFFFFF` is in DRAM range to DRAM BEFORE the 0xA4..0xC0 MMIO check (covers P0/P1/P2
>      incl uncached 0xAC). Done in emu_go/memory.go + emu/memory.py. VRAM @0x0c000000 then jumped from
>      ~23% (noise) to **88% nonzero** = a real frame.
>   2. **Framebuffer stride = 384, not 396.** Panel is 396x224 but the OS framebuffer/usable area is
>      **384x216** (768-byte rows; cf. strip-blit FUN_80150508 stepping 0x300/row). Reading at 396 skewed
>      it diagonally; at 384 it's pixel-perfect. dumpFB now 384x216.
> RESULT: `go run . 200000000 30000` -> fb_0c000000.png shows the **"Message Language" first-boot
> screen** ([English]/English/Espanol/Deutsch/Francais/Portugues list + "Hello" globe bubble + SELECT/
> Next). The emulator boots the REAL 3.60 OS from reset to its interactive language-selection UI.
> Goldens regenerated (final PC 0x801df466), Go tests 53/53 + 2000/2000 green, Python oracle in sync.
> ### ⏭ NEXT: drive the UI to the MAIN MENU (keyboard input — INPUT PATH FULLY MAPPED, injection TODO)
> Built foundation this session: emu_go `drive` mode = per-frame PNG dumper (dumpFB 0x8C000000 384x216
> every 15M instr) + KEYSC injection harness (mmio.kbReg/kbVal/kbStart/kbEnd; bus Read override of the
> KEYSC region during a cycle window). `go run . <N> 30000 drive [kbReg_hex] [kbVal_hex] [pressCycle]`.
> INPUT PATH (3.60), fully traced:
>  - The OS scans **KEYSC @0xA4080000** (NOT KIU 0xA44B0000 — that's read only ~7x at init). During the
>    idle wait it polls control/status 0xA4080090/0x04/0xD0 (~4317x each) via FUN_801de504 (trigger+clear;
>    it reads 0x04 &0x0fff and DISCARDS — just a scan trigger; also does the battery ADC at 0xA4610088).
>  - The 12 matrix DATA regs 0xA4080000..0x16 are **NOT read while idle** -> they're read only on a KEYSC
>    keypress IRQ. Decode chain (setup screen): FUN_8035d234 -> FUN_801951a6/d2/234 -> **FUN_801952cc**
>    (reads raw row/col via **FUN_801e5f9a** from a key-event QUEUE: FUN_801e6994 reads *DAT_801e6a1c=count,
>    buffer *DAT_801e6a40 + row@DAT_801e6a24/col@DAT_801e6a28), then maps via key table
>    **DAT_805ff7ec[col*0x1c + row*4]** (col 1..12,row 1..7; dumped by re/dump_keytable.py -> codes 0x01-0x3c)
>    then FUN_80194ea8/ebc to final codes.
>  - EMPIRICAL: naive KEYSC injection (data regs / status 0x04 / all-0xFFFF) does NOT register -> a key
>    needs the KEYSC keypress IRQ delivered so the ISR reads the data regs & fills the queue (+ likely
>    debounce/edge). **NEXT: model a KEYSC press = set data regs + raise its INTEVT so the ISR enqueues;
>    OR inject directly into the event queue (*DAT_801e6a1c count + row/col entry) — read those ptrs first.**
> Then drive: language screen wants nav + SELECT(F1)/Next(F6)/EXE; after setup (language/region/clock,
> a few screens) fls0 is written & you reach MAIN MENU FUN_8036427a (3x4 icon grid; EXE/letter launches).
> ALT (skip setup): seed a pre-initialized fls0. The 16MB flash_full.bin lacks the fls0 storage tail
> (phys 0x01000000+ reads 0xFF) -> that's WHY it's first-boot; a fuller dump lands on the configured menu.
> Tools added: emu_go `drive` mode + KEYSC inject; re/dump_keytable.py.
>
> ### 🟢 2026-06-02 (cont.8) — MODEL CODE FIXED (fx-CG50 0xca02); next gate = first-boot flash-init poll
> The reset stub @0x80000040 reads HW-strap **0xFF000024** and selects model: low16 0x0000->0xCA00,
> 0x0020->0xCA01, **0x0A02->0xCA02 (fx-CG50)**. Our CCN returned 0 -> model 0xCA00 (wrong). FIX:
> CCN.read returns **0x0A02 at +0x24** (emu_go/mmio.go `ccn` type + emu/mmio.py `CCN` class). Now
> *0xfd8018d4 = *0x8c04ca24 = 0x0000ca02. Regenerated goldens (model change shifts boot path
> slightly: final PC 0x801df468 -> **0x801df466**); Go tests 53/53 + 2000/2000 green; oracle synced.
> ### 🧭 cont.9 — the "hang" is an INTERACTIVE FIRST-BOOT SCREEN waiting for KEY INPUT (not a crash)
> Drilled FUN_8035e1be's sub-fns: the one that never returns is **FUN_8035d234** (=PTR_FUN_8035e1f8,
> called with 1). It is a large **interactive list/menu UI**: key-event loop (PTR_FUN_8035d504 get-key,
> PTR_FUN_8035d508 process -> code in DAT_8035d4d2), cursor nav (codes 1/2/3/4/5 = up/down/scroll/
> select/exit), draws items via FUN_8035d70c, uses a 64KB stack list-buffer (acStack_10020). With
> KEYSC=0 (no key) it spins forever in the get-key loop (that's the 31418x is_erased — a per-frame
> check, NOT the gate). wmap during the wait shows NO full-framebuffer fill (only stack 0x8c158000 +
> modest 0x8c088000/0x8c090000 list state) -> it drew once then polls; screen stays BLACK.
> **So the boot is NOT crashing — it reaches an interactive screen and waits for input.** Almost
> certainly **first-boot SETUP/selection** (language/region/initialize), shown because our fls0 is
> blank (formatted-from-empty). On a real, already-set-up calc this screen is skipped (fls0 has the
> config) and you go straight to the MAIN MENU (FUN_8036427a).
> **TWO REMAINING PIECES (next session):**
>  1. **Why blank fls0 / first-boot:** phys 0x01000000+ (fls0 storage) is PAST the 16MB flash_full.bin
>     so it reads 0xFF=blank. Options: (a) the real flash is >16MB and the dump lacks the storage tail
>     — check the dump / re-dump with the storage region; (b) seed/inject a minimal valid fls0 so the
>     OS skips first-boot setup; (c) drive the setup screen by injecting the expected keypresses.
>  2. **Render gate (still open):** the screen is BLACK — even this setup screen (and earlier the menu
>     draw FUN_803647e0) write to VRAM but the visible buffer (0x8c000000/0x8c028800) stays empty.
>     Pin down the real VRAM the OS draws into vs what FUN_8005552c pushes (SAR), or a draw primitive
>     targeting the wrong base. dumpFB any candidate buffer to SEE it.
> Useful: to get past the input-wait quickly, model KEYSC/KIU to return a key (e.g. the setup's
> select/EXIT code) — but first fix rendering so we can see what screen it is.
>
> ### ⏭ (cont.8) fls0_init reaches FUN_8035e1be flash-init -> FUN_8035d234 (see cont.9 above)
> fls0_init (FUN_80365238) still doesn't return: after mount+enumerate it calls **FUN_8035e1be**
> (-> sub-fns PTR_FUN_8035e250(7)/8035e1f8/8035e1f0/8035e1f4/8035e204) which spins polling
> **is_erased = FUN_801de9ca = memcmp(flash@0x300, 0xFFFFFFFF, 4)==0** (31418x, always 0). Runtime:
> flash@0x300 = 0x38313041 ("810A"), *0x806827a4 = 0xFFFFFFFF -> not erased -> loop never exits.
> The model-code check (alt loop-exit *(0xfd8018d4)==0xca02) is now satisfied but only reached if a
> sibling check (FUN_801deca4) returns 1 (it returns 0). NO flash-erase command (0x80/0x30) is seen
> in flashwr during the hang -> nothing erases 0x300.
> ALSO FOUND: **FUN_80150680 = factory DIAGNOSTIC/SERVICE mode** (strings "DIAGNOSTIC MODE","Factory
> Use Only","Delete all data?","VER SUM CLEAR","ABS Mark NG","BaseROM/MAIN"), gated by is_erased
> (flash@0x300 blank -> enter diag). We correctly DON'T enter it (0x300 not blank).
> **HYPOTHESIS (next):** our fls0 storage region (phys 0x01000000+) starts BLANK 0xFF (it's PAST the
> 16MB flash_full.bin), so the OS treats the device as UNINITIALIZED/first-boot and runs a flash-init
> path that erase-polls; on real HW fls0 is pre-populated so this is skipped. So either (a) the flash
> is >16MB and the dump lacks the storage tail (need real fls0 content), or (b) FUN_8035e1be issues
> an erase our NOR model doesn't recognize. NEXT: decompile FUN_8035e1be's sub-fns (esp. the one
> calling is_erased in a loop — via FUN_80150680?) to see the exact erase it expects; check if the
> dump has storage content at a different phys; consider seeding the FS region or handling the erase.
> Probe: `go run . <N> 30000 flashwr` (flash cmds), `gate` (edit names[]), report prints model +
> is_erased cmp. dumpFB still BLACK.
>
> ### 🟢🟢🟢 2026-06-02 (cont.7) — WRITABLE NOR FLASH IMPLEMENTED → fls0 MOUNTS; boot far deeper
> Implemented the NOR-flash write model (the cont.6 fix) in BOTH emu_go/memory.go + emu/memory.py:
>   - mutable `flash[]` array [0, FlashMutTop=0x02000000) = image copy then 0xFF; reads come from it.
>   - JEDEC/Spansion command state machine: unlock 0xAA@*0xAAA / 0x55@*0x554, then 0xA0 word-program
>     (AND), 0x80..0x30 sector-erase(64KB->0xFF), 0x25/count/data/0x29 BUFFERED-program, 0xF0/0x90/
>     0x98 = no array change (so code-fetch reads stay valid). Only program/erase mutate the array.
>   - Also mapped ON-CHIP RAM **0xFE200000-0xFE400000 (2MB, `ocram`)** — the OS keeps kernel linked
>     lists there (a list head @0xFE224000); unmapped before -> garbage-pointer fault @0x801e3ff8.
>   Regenerated goldens (UNCHANGED, final PC 0x801df468 — 2M boot doesn't program flash/use ocram);
>   Go tests 53/53 + 2000/2000 green; Python oracle kept in sync.
> RESULT: **fls0_open (FUN_80358b1e) now returns 0 (SUCCESS)** (was -6) — the FS MOUNTS. Boot runs
> 400M with NO fault and progresses WAY past the old wall: mount -> FS enumeration COMPLETES
> (next_entry FUN_8020ff3e returns 0 = empty FS, exits) -> most of the post-mount init chain
> (0x803653cc: 0x801e68d2, 0x800476d6, 0x8002ce08, 0x802e23d0, 0x80355xxx display-init...).
> ### ⏭ CURRENT GATE (cont.7): flash-signature / model-code verify spin-loop @0x80365418
> fls0_init (FUN_80365238) STILL doesn't return — now hangs in a poll loop @0x80365418 calling
> **FUN_801de9ca 90,969x (always 0)**. FUN_801de9ca = `memcmp(flash@0x300, &local, 4)==0` where
> local is loaded from DAT_806827a4 (=0xFFFFFFFF). flash@0x300 = "810A" (0x38313041), so it's
> checking "is flash@0x300 ERASED (0xFFFFFFFF)?" -> no -> returns 0. Loop's two exits: (1) that
> memcmp == erased (never), (2) **`*(0xfd8018d4) == 0xca02`** (model code; 0xca02 = fx-CG50). Neither
> fires. LIKELY FIX: our emulated MODEL CODE isn't 0xca02 — boot derives it from HW-strap
> **0xFF000024** (our CCN mmio prob returns 0 -> wrong model). NEXT: check what 0xFF000024 returns
> & the strap->model map; make it select 0xCA02 so *(0xfd8018d4)==0xca02 and the loop exits. (Alt:
> the loop body 0x801decaa/0x800aa9e2/0x8035e1be may be meant to write/erase flash@0x300 — verify
> it isn't a NOR-model gap.) Then fls0_init returns -> FUN_80363114 do-loop -> FUN_80363d64 -> MENU.
> Probe: `go run . <N> 30000 gate` (edit names[] to target fns); dumpFB PNG (still black for now).
>
> ### 🧩 2026-06-02 (cont.6) — 3rd GATE ROOT-CAUSED: emulator IGNORES NOR-flash writes → fls0 can't format
> Definitive: instrumented flash writes (emu_go `flashwr` mode + Memory.fwrites/fwLog). The FS
> mount/format IS issuing **JEDEC/CFI NOR-flash command sequences that we silently drop**:
> unlock `0xAA->*0xaaa`,`0x55->*0x554` then cmds `0x90`(autoselect/read-ID), `0x98`(CFI),
> `0xF0`(reset), **`0x25`/`0x29` (buffered-program load/confirm)**; plus actual data programming at
> **phys 0x01000000-0x01098000** (the fls0 storage region) — ~12k writes/200M concentrated in pages
> 0x01040000/0x01060000/0x01080000. memory.go currently does `if phys<FlashSize { ignore }`, so the
> FS's format/journal writes never persist; reads return stale image data -> mount sees an
> un-formatted/blank FS -> fls0_open=-6 -> infinite recovery -> menu never runs.
> NOTE: phys 0x01000000 == 16MB == JUST PAST the end of flash_full.bin (16MB) -> the FS storage
> region currently reads 0xFF (blank), which is WHY the OS tries to format it.
> **THE FIX (next session, sizable): model writable NOR flash.** Need a JEDEC/CFI command state
> machine + RAM-backed flash so program/erase take effect and reads reflect them:
>   - read-ID (0x90) + CFI (0x98) must return plausible manufacturer/device/CFI so the FTL accepts
>     the chip (else it may reject -> -6 regardless of writes);
>   - sector-erase (0x80..0x30 -> 0xFFFF), word-program (0xA0 -> AND), buffered-program (0x25/count/
>     data/0x29), reset (0xF0) back to array-read;
>   - back it with a mutable buffer covering at least phys 0..~0x01100000 (the image 0..16MB stays
>     as data; command writes must NOT corrupt array data — only program/erase modify);
>   - do it in BOTH emu/memory.py (oracle) + emu_go/memory.go, then regen goldens (verify the 2M
>     boot is unaffected — flash writes start ~shell time, well past 2M) and run Go tests.
>   Once flash writes persist, fls0 should format/mount -> FUN_80365238 returns -> FUN_80363114
>   reaches FUN_80363d64 -> the MENU app (FUN_8036427a) runs & draws (FUN_803647e0 -> push
>   FUN_8005552c). Probe to confirm: `dumpFB` PNG should go from black to the white menu.
>   Tools added this session: emu_go modes flashwr/wmap + Memory.fwrites/wpages; re/find_const.py.
>
> ### 🎯 2026-06-02 (cont.5) — 3rd GATE LOCALIZED: fls0 filesystem MOUNT fails (-6) → boot stalls in recovery
> Drilled all the way to the current blocker. After the battery fix the boot reaches the real
> top-level driver **FUN_80363114** = `{ init...; do { state=3; FUN_80363d64(); } while(1); }`.
> But **FUN_80363d64 (the per-frame dispatcher) is NEVER called** (gate probe: 0) — we're stuck in
> FUN_80363114's INIT, specifically in **FUN_80365238** (a boot fls0-mount/init; refs strings
> "fls0","CASIOWIN","E-CON2"). It calls **fls0_open = FUN_80358b1e → PTR_FUN_80358bb8() which
> returns -6** ("FS not mountable"). That trips FUN_80365238's recovery/format branch
> (`if(==-6){ FUN_80365780(0); ...format "fls0"...; FUN_803658c4(1); }`) which then churns FOREVER
> in memcmp(0x80384a40, 29%) + memset(0x80385180, 15%) — boot never finishes → FUN_80363d64 / the
> MENU app never run → screen stays black.
> KEY: the MENU app IS fully reverse-engineered now — **FUN_8036427a = main menu** (3x4 icon grid
> nav via *DAT_80364428, key->appID map 0x95->0x42.., ENTER launches via PTR_FUN_80364650), draws
> via **FUN_803647e0** (12 icons via FUN_80364f88 @0x80364f88) then push **FUN_8005552c** (DMAC
> LCD push, the Bdisp_PutDisp_DD equiv). None of these run yet (blocked by the fls0 mount).
> Low-level flash reads PASS ECC (FTL probe) but the higher-level MOUNT (FUN_80358bb8) returns -6.
> **NEXT STEP:** decompile **FUN_80358bb8** (the real mount worker under fls0_open) — find why it
> returns -6 (what flash region / FS superblock / magic / RAM mount-state it checks that our
> flash_full.bin presentation doesn't satisfy). Likely we mis-present the FS storage tail or a
> mount needs RAM state we don't init. Once fls0 mounts, FUN_80363114 should reach FUN_80363d64 →
> menu. Probe: `go run . <N> 30000 gate` with the FS-init addrs; `wmap` (DRAM write pages, found
> menu draws NOT to 0x8c000000); `dumpFB`->PNG (screen is BLACK = menu never painted).
> New tools: emu_go/main.go modes wmap + dumpFB PNGs + Memory.wpages; re/find_const.py,
> re/probe_delaygate.py (now dumps fls0-init call targets).
>
> ### 🟢🟢 2026-06-02 (cont.3) — BATTERY-ADC GATE FOUND & FIXED (2nd major fix); menu un-skipped
> Traced the render gate to an UNMODELED BATTERY-VOLTAGE ADC. Chain (all verified empirically
> via emu_go `gate`/`shelltrace` modes): the 3.60 os_main_loop @0x801e36a8 calls shell
> FUN_802aea26; inside, the event poll PTR_FUN_802aedf0 = **FUN_801e6b1e** returns 1 (→ local_44=1
> → SKIP the menu-body app-dispatch → idle pump). FUN_801e6b1e returns 1 iff FUN_801de858()==4
> (true) AND **FUN_801e6bbc()==0x12**. FUN_801e6bbc buckets a battery-ADC read (FUN_801de54a,
> averages 2 samples >>6) against thresholds ~347-475; **a 0 reading → lowest bucket 0x12**.
> The ADC data reg is **0xA4610082/0xA4610084** (control 0xA4610088, all in the 0xA4610000
> PERIPH block we modeled as periphIRQ returning 0). **FIX: periphIRQ.read returns 0x7140 at
> +0x82/+0x84** (raw>>6 = 453 → bucket 2 "normal"), in BOTH emu/mmio.py (oracle) + emu_go/mmio.go.
> Regenerated goldens (UNCHANGED — ADC not read in the 2M boot; final PC identical) → Go tests
> 53/53 + 2000/2000 green. AFTER fix (verified): adc_read 0→453, bucket 0x12→2, event_chk 1→0;
> mainloop_iter 51→1 (shell stops idle-pumping, goes DEEP into app/draw code); **a NEW LCD push
> from the full-buffer base SAR=0x0c000000 appears @85M** (boot only ever pushed partial 0x0c028800).
> ### ⏭ REMAINING GATE (cont.4): OS pushes frames but the buffer is BLACK — menu content not generated
> After the battery fix the shell (FUN_802aea26) is NO LONGER re-entered (shelltrace: 0 entries
> over 120M) — control diverged into a NEW subsystem (stable stack: 0x80195xxx / 0x802b4xxx /
> 0x802abbcc / 0x8018be42 / 0x801e5f06-module). Investigated leads:
>  - Unmapped-MMIO hunt (added report dump + per-PC reader watch, mmio.watchBase): hottest were
>    **0xA44C0020 (67k) / 0xA44C0000 (33k)** = another ETMU-style timer. Modeled it (bit0 elapsed
>    @+0x20) → ZERO effect; the only reader (0x801e6d96) is the CLEAR/reset path, read_flag
>    (0x801e6dc4) is never called → nothing WAITS on it → NOT a gate. Reverted that model.
>  - **VISUAL GROUND TRUTH (added dumpFB → PNG in report):** dumped FB @0x8c000000 (post-fix push
>    SAR), @0x8c028800 (boot SAR), and densest window. All essentially **BLACK** with only tiny
>    scattered status text in corners. Real CG50 menu = WHITE bg + icons. So the push (0x8005552c)
>    fires but pushes an empty buffer.
>  - The active redraw loop @0x801951xx-0x80195230 calls 0x80150508, 0x80355b10, and **0x8005552c
>    (LCD push, in the 0x8005xxxx display driver) ×2** — i.e. it DOES push frames; the missing
>    piece is the **VRAM content generation** (0x80150508 / the menu app's paint) before the push.
> **CONCLUSION:** boot now reaches a real display-redraw loop that pushes frames, but the menu
> BODY is never painted into VRAM (screen black, not white). NEXT: decompile **0x80150508** and
> **0x8005552c** (3.60 display push); find the menu/app paint routine and why it produces an empty
> (black) buffer — likely the menu APP still isn't launched, or its paint is gated, or a draw
> primitive writes to the wrong VRAM base. Tools: emu_go/main.go modes prof/stack/gate/draw/
> shelltrace + dumpFB PNGs + mmio.watchBase reader-PC + unmapped-MMIO dump; re/ probe_delaygate.py,
> find_framebuffer.py, find_const.py, disasm_static.py.
>
> ### 🟢 2026-06-02 (continued) — ETMU-DELAY GATE FOUND & FIXED; emulator now 10x deeper
> The "parks in ETMU busy-delay FUN_803742f8" stall was a REAL EMULATOR BUG, not OS logic.
> Chain: shell FUN_802aea26 → FUN_80318d9c(20) → **FUN_803742f8** = `start=*ctr; do{now=*ctr}
> while(((start-now)&0xFFFFFF)<0x21)` where `ctr` = `*0x80374380` = **0xA44D00D8** (ETMU
> down-counter; verified via re/probe_delaygate.py). The counter model in mmio.go/mmio.py
> returns `-(cpu.cycles>>2)&0xFFFFFF` ONLY when `bus.cpu` is set — but **emu_go/main.go never
> did `mmio.cpu = cpu`** (the Python *runtime* probes all do; the Go runner didn't). So the
> counter was stuck at 0, delta always 0, delay spun forever. **FIX: one line in main.go
> `mmio.cpu = cpu`** (after NewCPU). Tests untouched/green (golden + conformance run cpu-unwired
> by design, and the 2M golden boot is well before the ~12M shell delay, so the golden is still
> valid — confirmed 53/53 + 2000/2000). After the fix the emulator blows past the delay and
> runs into real varied subsystem code (FTL 0x80370/71xxx, 0x80385xxx, 0x8036xxxx, 0x8015xxxx,
> app-region 0x805f4730). STILL only the 1 initial screen-clear LCD push (vram_nz~96, no menu).
>
> ### 🔎 DOWNSTREAM "FTL gate" RULED OUT — we are now in the REAL running main loop
> Profiled the post-fix steady state (added `prof` mode + block-arg histogram to emu_go/main.go;
> `go run . <N> 30000 prof` / `... ftl`). Findings over 400M instr:
>  - Hot PCs are all FS/FTL (0x80370cc0 9%, 0x803717c0 6%=ECC, 0x801df440 9%, ilram 0xfd800b40 9%).
>  - **All flash reads PASS**: block_read 9658→0, rec_verify 49958→0, ECC_verify 49958→0 (clean).
>  - block_read touches **180 distinct FS blocks, each ~52×** (re-scans, blk# up to ~0x12a6).
>  - Climbed the scan stack: block_read←FUN_8036fbb8←FUN_8036df54←FUN_8036ff1a←FUN_8017d59c←
>    FUN_8018879c (parse/validate a record: byte-swaps fields, checks type==0x1d, flag==1, a
>    u32==0) ←FUN_801885f2 (**load_setup**: builds 2 filenames, validates the record).
>  - **DECISIVE:** load_setup FUN_801885f2 is called **51×** (≈once per scan pass) and the
>    validator FUN_8018879c is called **once and returns 1 = SUCCESS**. So validation does NOT
>    fail; the repeated FS scans are just the **shell main loop iterating normally** (~51 iters /
>    400M ≈ 7.8M instr each). The "FTL gate" was a red herring — flash/FS works.
> **CONCLUSION:** the ETMU fix put us INTO the real steady-state event loop (it cycles cleanly);
> the menu is still never RENDERED INTO VRAM (vram_nz flat ~96, no CPU fill, no 2nd DMAC push).
> So the gate is a **conditional render / app-launch decision inside the loop** that's never
> taken — back to the original hypothesis, but now the loop actually runs.
>
> ### 🔬 2026-06-02 (cont.2) — MAIN LOOP FOUND; status bar drawn, MENU BODY never rendered
> Added emu_go/main.go probe modes `stack` (stack return-addr histogram), `gate` (entry/return
> counts for os_main_loop funcs), `draw` (watch FB for changes + log writer PC). Findings:
>  - **3.60 os_main_loop = function @ 0x801e36a8** (tail-jumps 0x80363114; a CALLER loops it).
>    Per iter it services 0x800204d0/0x801de81a(1)/0x801d0df8/0x802eeb4c/0x801ded40, then
>    `SR &= 0xEFFFFF0F` (enable IRQs — SAME mask as 3.80 main loop slot 0x3740), then the FS
>    driver 0x800c1888, then conditional calls to the SHELL **FUN_802aea26** (the app-dispatch
>    fn we decompiled): @0x801e370c `jsr 0x802aea24`(r4=0) if 0x801e6b5c==1; @0x801e3754
>    `jsr 0x802aea26`(r4=1,r5=0) gated by 0x802b0e22 / 0x801deaae.
>  - **The shell FUN_802aea26 IS called ~once per loop iter** (gate probe: mainloop=51,
>    shell=50). So the menu-drawing shell RUNS every iteration; the gate is INSIDE it, not
>    "shell never called." (Per-site gate attribution is muddy — these fns have many callers.)
>  - **`draw` probe (FB @0x8c028800):** the FB IS written every loop iter but only **nz≈16–66
>    out of ~88704 px (<0.1%)** — a tiny element drawn+partly-cleared periodically (status bar /
>    cursor). Writers: blit loop **0x803851xx** (hot in prof too) called from a **0x8073xxxx /
>    0x80740xxx draw module** (pr=0x8073b8da/0x807409ae/0x80744024). The **MENU BODY (nz~80000)
>    is NEVER rendered** anywhere (DRAM-wide densest window still only ~22%).
>  - dram.bin is NOT a menu oracle: it was dumped by gint/fxlink RUNNING on the calc, so its
>    framebuffer is the dump tool's screen, not the OS menu (re/find_framebuffer.py found only
>    noise/blank windows; emulator's 0x28800 region is blank in the real dump too).
> **CONCLUSION:** system chrome (status bar) draws fine; the **main-menu APPLICATION never
> draws its body** → the app-launch/"current app draw" step inside the shell is skipped.
> **NEXT STEP:** find the menu-app launch + its body-draw call inside FUN_802aea26's do/while
> (the app-dispatch block reached when local_44==0: PTR_FUN_802af040/044/048 …) and the
> 0x8073xxxx draw module's higher-level caller; determine the condition that skips the body
> draw (suspects: held-key/boot-mode, an "app already shown" flag, an event the menu waits on).
> Hook INSIDE FUN_802aea26 (which branch of the do/while it takes; whether local_44 ever==0).
> Tools added this session: emu_go/main.go modes `prof`/`stack`/`gate`/`draw` + block-arg
> histogram in `ftl`; re/probe_delaygate.py, re/find_framebuffer.py.
>
> ### One-line state
> **The emulator boots the REAL fx-CG50 OS 3.60 all the way to its system idle/event loop**
> (interrupts, timer, keyboard scan, flash translation layer w/ ECC all working). The only
> thing missing for a live screen: **the shell never launches/draws the main menu** — gate
> isolated to a high-level app-launch/event condition (everything else ruled out, see below).
>
> ### ✅ EMULATOR REWRITTEN IN GO (emu_go/) — ~1000x faster, test-validated
> Python (~45k instr/s) was too slow for boot-to-menu (tens of millions of instr). Ported the
> core to **Go**: `emu_go/{memory,mmio,cpu,main}.go`. Measured **~64–85 M instr/s** (500M-instr
> run in 5.8s; the 40M boot-to-alive that took ~10 min in Python now ~0.6s). Run:
> `go -C emu_go run . [maxIns] [timerPeriod] [mode]`  (mode `ftl` = flash-FTL return probe).
> **Python emulator (emu/) is kept as the reference ORACLE.** ⚠️ RULE: whenever cpu.py/mmio.py
> change, refreeze goldens (`python emu/conformance_gen.py && python emu/gen_golden.py`) and run
> `go -C emu_go test .` — never validate the port by ad-hoc running. ALWAYS write/update tests.
> Test harness (both Python + Go consume the SAME frozen goldens):
>  - `emu/conformance_gen.py` -> `emu/conformance.json` (53 edge-case instr cases) ; checked by
>    `emu/test_cpu.py` (Python, 53/53) and `emu_go/conformance_test.go` (Go, 53/53).
>  - `emu/gen_golden.py` -> `emu/golden_boot.bin` (full CPU state every 1000 instr over 2M-instr
>    boot) ; checked by `emu_go/golden_test.go` (2000 checkpoints exact).
>
> ### ✅ 3.60 OS NOW IN GHIDRA + multi-tab MCP fork
> `os/flash_dump/os.bin` (the physical 3.60 OS) is loaded in Ghidra, **rebased to 0x80000000**
> (mirror moved to 0x20000000), auto-analyzed — decompiler works on the code we actually run.
> Our GhidraMCP is a CUSTOM FORK at **F:\ru\myprojects\may\lwired** that supports MULTIPLE open
> programs: tools `list_open_programs` / `get_current_program` + an optional `program` arg on
> every tool (target a binary by name/path without switching tabs). ⚠️ Those new tools were NOT
> exposed in this session (deferred-tool registry is fixed at session start) — **restart Claude
> Code / reconnect the MCP to get `list_open_programs` and the `program` arg**. Then we can keep
> 3.80 AND 3.60 loaded and query either. (This session used the focused/current program = 3.60.)
>
> ### 🎯 RENDER-GATE INVESTIGATION — menu never drawn; gate = app-launch logic
> Boots to system idle loop (call chain returns through ~0x802af4xx, parks in ETMU busy-delay
> FUN_803742f8). Screen blank: only ONE LCD DMA push ever (initial screen-clear, SAR=0x0c028800).
> **Flash-FTL hypothesis TESTED & DISPROVEN (strong evidence):** `go run . N 30000 ftl` shows
> block_read FUN_80370ff0 = 366/366 ret 0 (OK), ECC_verify FUN_80371718 = 1916/1916 ret 0 (clean).
> DRAM-wide framebuffer scan: densest 396x224x2 window only ~22% nonzero = NOT a rendered menu
> (a real menu is a ~95%+ near-white field) -> menu **never drawn anywhere** (not "drawn-not-pushed").
> **ELIMINATED:** runtime (500M instr flat), FPU (fpu_ops==0 over boot-to-idle), modeled
> peripherals, flash-FTL/ECC, draw-but-no-push. **REMAINING (one layer):** the shell reaches
> SYSTEM IDLE without launching/drawing the menu app — a higher-level app-launch / event-trigger
> gate. Untested suspects: an RTC we don't model, a boot event the shell waits on, a boot-mode/
> held-key check.
> **NEXT STEP:** trace WHY the menu app isn't launched — decompile the idle/event loop (~0x802af4xx)
> in Ghidra 3.60 and walk UP the steady-state call chain to the launch decision. Idle call-chain
> entry points (from Go stack dump): **0x802aebc6, 0x801e523c, 0x8018c1ea, 0x801de5cc, 0x801e3712,
> 0x801e5f06**. Alt: diff our boot vs Heath123/casio-emu `os` branch (known-good) to find divergence.
> Fast-iter snapshots (Python): `emu/idle_state.pkl` (@14.5M), `emu/alive_state.pkl` (@26.5M).
> Modeled-this-session MMIO: timer INTEVT **0x560** (not 0x188), ETMU down-counter @0xA44D00D8,
> KIU key-data @0xA44B0000, INTX scan-ready bit6 @0xA4140024. (All in emu/mmio.py + emu_go/mmio.go.)
>
> ### Probe/tool scripts added this session (emu/ and re/)
> emu/: run_full.py, run_idle_probe.py, run_live.py, run_alive.py, run_dump.py, probe_etmu.py,
> probe_wait.py, probe_delay.py, probe_storage.py, trace_isr.py, trace_outer.py, dump_irqtable.py,
> test_candidates.py, gen_golden.py, conformance_gen.py, test_cpu.py.  re/: disasm_static.py
> (static SH4 disasm of any 3.60 vaddr, base-independent), probe_flashdump.py, probe_dump_detail.py.
>
> ---
> ### (earlier same session 2026-06-02) PHYSICAL FLASH DUMP ACQUIRED & VERIFIED — `os/flash_dump/`
> gint/fxlink dump off the real calc. **All 4 blobs SHA256-verified intact** (see
> `SHA256SUMS.txt`; the USB errors in `recv.log` are post-save disconnect noise).
> Probes: `re/probe_flashdump.py`, `re/probe_dump_detail.py`.
>  - `flash_full.bin` 16MB = full NOR; OS at off 0 + ~4MB storage/FS tail. No separate boot ROM.
>  - `os.bin` 12MB = OS region.  `dram.bin` 8MB = **live DRAM snapshot**.
>  - `ilram.bin` 64KB = on-chip **IL fast-RAM holding RELOCATED OS code** (= verbatim copy of
>    `os.bin@0x745c24`). NOT the 0xFD800000 kernel-struct region — earlier guess was wrong.
> **⚠️ KEY FINDING: physical calc runs OS `03.60.0000`, our Ghidra/emulator work is `03.80.0000`.**
>  - boot/reset area `[0..0x20000]` is **100% identical** between 3.60↔3.80 (our entire boot RE
>    transfers unchanged); OS body after 0x20000 diverges (~37% byte match → different version).
> **DECISION (user, 2026-06-02): STAY ON 3.80; use the dump as a version-stable hardware oracle**
> (boot stub, MMIO/peripheral behavior, FS layout, loose live-RAM sanity). NO Ghidra reload.
> 3.60-specific code addresses do NOT line up with our 3.80 Ghidra — don't follow dram/ilram
> pointers into the 3.80 project. High-value next uses: extract a real rendered frame from
> `dram.bin` to diff against the emulator's framebuffer; confirm the IL-RAM code-relocation
> region in the emulator memory map.
>
> ## ⏯ (prev session: 2026-05-31)
>
> ### Where we are in one line
> **OS PACKER SOLVED — plain fx-CG50 OS 3.80 image extracted.** Path 1 (reverse the
> updater's unpacker) succeeded end-to-end. Next: load the plain OS into Ghidra as SH-4A
> big-endian @ 0x80000000 and begin the comprehensive study (memory map, MMIO, syscalls…).
>
> ### ✅ PACKER CRACKED (2026-05-31) — it was gzip all along
> Reversed `cg50_updater.exe` (SetupFile2) via GhidraMCP. The unpacker is `FUN_10004580(id)`:
> it loads RT_RCDATA(0xa) id, **rebuilds a gzip stream**, and calls `FUN_100018d0` =
> a thin **zlib 1.2.3** wrapper (`inflateInit2_(strm,windowBits=0x1f,"1.2.3",0x38)` →
> `inflate(Z_FINISH)` → `inflateEnd`). windowBits 0x1f=31 ⇒ gzip.
> Casio tampered the stored blob so it doesn't look like gzip:
>   1. the **10-byte gzip header is stripped** (updater restores it from DAT_101263a4 =
>      canonical `1F 8B 08 00 00 00 00 00 00 00`);
>   2. **one byte at compressed-stream offset 0x2ff6 is removed**, restored per-image as
>      **0x02** for the OS (3070/3071) or **0x1f** for the bootloader (3069).
> Reconstruct + inflate:  `gziphdr(10) + res[:0x2ff6] + flag + res[0x2ff6:]`, wbits=31.
> → script `re/unpack_os.py`. Output sizes match the updater's own malloc EXACTLY
>   (proof): OS = 0xb60000 (11,927,552 B), bootloader = 0x1077f (67,455 B).
>
> ### ✅ Plain images written to `os/os_image/`
> - **`cg50_os_3.80.plain.bin`** (0xb60000) = **fx-CG50 OS 3.80** ← OUR TARGET.
> - `graph90_os_3.80.plain.bin` (0xb60000) = Graph 90+E (FR) OS.
> - `bootloader_3.80.plain.bin` (0x1077f) = bootloader/preloader (3069).
> Verified real (probe `re/probe_plain.py`): signatures **`CASIOABS/`** @0x338 and
> **`CASIOWIN`**, version string **`3.80`** @0x20021, `GETKEY`/`VER` strings; entropy is
> dense code (~7.0–7.4) for first ~8 MB then flat 0.0 padding (flash tail) — textbook firmware.
> ⚠️ The OLD `os/os_image/cg50_os_3.80.bin` is the mislabeled Physium add-in — ignore; the
> new `.plain.bin` is the genuine OS.
>
> ### ⏭ NEXT — load into Ghidra & start the study
> New flat-binary load: processor **SH-4A**, **big-endian**, base **0x80000000** (mirror
> 0xA0000000). Then produce the deliverables below (memory map → MMIO inventory → syscalls
> → boot/IRQ → display+keyboard). Cross-check against Heath123/casio-emu `os` branch.
> (The x86 updater stays in Ghidra too if we want the USB-flash protocol later.)
>
> ### (archived) Path-1 working state that got us here
> - Updater binary in Ghidra: `os/msi_files/cg50_updater.exe` (clean copy of SetupFile2;
>   PE32 x86, base 0x10000000, entry 0x10101ae4). Staged by `re/prep_unpacker.py`.
> - GhidraMCP registered as `ghidra` for this project in `C:\Users\ab\.claude.json`; live on
>   :8080. Key addrs: unpacker `FUN_10004580`, zlib-inflate wrapper `FUN_100018d0`,
>   gzip-header const `DAT_101263a4`, FindResourceW IAT slot `0x10125210`.
>
> ### (prev) Where we are in one line
> Officially-downloaded fx-CG50 OS 3.80 fully unwrapped. Add-ins extracted & decoded.
> **Main OS located but PACKED** (custom-compressed inside the x86 updater). Next step is
> to get a *plain* OS image, then load it into Ghidra and start the RE study.
>
> ### ✅ DONE / SOLVED
> 1. **Strategy set**: build a real hardware emulator (SH7305 / SH-4A, big-endian) of the
>    CG50 and run it on Android. **Ghidra-first**: reverse the OS into a hardware spec
>    BEFORE writing the emulator. (Unlike hp39gii, no host OS to shim — see body below.)
> 2. **OS acquisition — DONE**: downloaded official OS 3.80 updater (public URL in body),
>    unwrapped `zip → InstallShield exe → MSI → ISSetupFile streams` (all under `os/`).
> 3. **USBPower container format — SOLVED** (scripts in `re/`): header `[0x00:0x40]` plain,
>    payload `[0x40:]` bitwise-inverted. The 5 USBPower segments are the bundled **add-ins**
>    (Geometry, Physium, Picture Plot, 3D Graph, Prob Sim) — extractable cleanly.
> 4. **Main OS — LOCATED**: embedded in `SetupFile2` (12 MB x86 PE) as `.rsrc` RCDATA blobs
>    (extracted to `os/pe2_rsrc/.rsrc/1033/RCDATA/`):
>    - `3070` (4.65 MB) = **fx-CG50 OS** ← our target.  `3071` = Graph 90+E (FR) OS.  `3069`
>      (43 KB) = bootloader.  All **compressed/encrypted** (entropy 7.99, custom packer).
> 5. **Toolchain confirmed reusable** from hp39gii: Ghidra 12.0.4 + GhidraMCP (paths below),
>    7-Zip. New working rule: **drive multi-step work via one `python <script>` run** (see
>    `CLAUDE.md`) to avoid per-command approval prompts.
>
> ### ⏭ NEXT ACTION — a DECISION is pending (was mid-question when session ended)
> How to get past the OS packer to a plain image. User wanted to *clarify* before choosing.
> Two routes (I recommended **Path 1**, possibly both in parallel):
>  - **Path 1 (no hardware): reverse the updater's unpacker.** Load `SetupFile2` (x86 PE)
>    into Ghidra, find the routine that decompresses RCDATA/3070, reimplement it in Python to
>    unpack the OS blob. Self-contained; reuses our x86 Ghidra workflow.
>  - **Path 2 (hardware): dump the physical CG50 over USB** (gint flash dump) for the live,
>    already-unpacked OS + boot ROM. Authoritative; worth doing eventually as a cross-check.
>
> Clarifications the user may want first: compression-vs-encryption confidence & effort;
> exact/safe dump procedure; whether community (casio-emu `os` branch, Simon Lothar, Cemetech
> "Dumping/Finding Syscalls from a CG-50") already documented this packer / OS load layout.
>
> ### After we have a plain OS image
> Load it into Ghidra: flat binary, processor **SH-4A**, **big-endian**, base **0x80000000**
> (mirror 0xA0000000). Then produce the study deliverables (memory map, MMIO register
> inventory, syscall table, interrupt/boot sequence, display+keyboard drivers) = emulator spec.
>
> ### Files & scripts produced this session (all under `F:\ru\myprojects\may\cg50\`)
> - `os/update_380.zip`, `os/extracted/…` — the downloaded updater.
> - `os/exe_unpacked/` — 7-Zip dump of the outer InstallShield exe.
> - `os/msi_files/ISSetupFile.SetupFile1..7` — raw MSI streams (1,2 = PEs; 3–7 = add-ins).
> - `os/decoded/fw3..fw7_*.bin` — whole-file NOT of the add-in streams (header-plain).
> - `os/os_image/cg50_os_3.80.bin` — ⚠️ MISLABELED: this is actually the **Physium add-in**
>   (fw4), not the OS. Ignore/delete; real OS is the packed RCDATA/3070.
> - `os/pe2_rsrc/.rsrc/1033/RCDATA/3069,3070,3071` — the packed OS/bootloader blobs.
> - `re/parse_usbpower.py` — proves payload orientation, dumps container structure.
> - `re/extract_os.py` — extracts a USBPower payload (used on fw4 → Physium).
> - `re/find_os.py` — scans all streams for USBPower magic + OS markers (found OS in PE2).
> - `re/dissect_pe.py` — PE section/overlay parse of SetupFile2.
> - `re/extract_rsrc.py` — 7-Zip-extracts PE2 resources, ranks blobs (found RCDATA 3070/3071).
> - `re/probe_rcdata.py` — entropy + codec probe of the RCDATA blobs (→ custom packer).

Goal: run the Casio fx-CG50 (SH7305 / SuperH SH-4A) firmware on Android via a
from-scratch-ish hardware emulator. Strategy decided with the user: **Ghidra-first**
— reverse-engineer the OS to produce a hardware/contract spec *before* writing the
emulator, so we build to a known contract instead of guess-and-crash.

This is the spiritual successor to `F:\ru\myprojects\april\calc` (hp39gii). Key
difference: the hp39gii was a Windows x86 *app* run under Unicorn + OS shims. The CG50
has **no host OS to shim** — the firmware *is* the OS talking to bare silicon, so we
need a real hardware emulator (SH-4A CPU + MMU + on-chip peripherals), like a console
emulator. Unicorn can't help (no SuperH). QEMU has an SH-4 core but targets SH7751, not
the SH7305.

## Hardware facts (fx-CG50 / SH7305)
- CPU: Renesas SH7305, SuperH **SH-4A** family (SH4AL-DSP), single-precision FPU.
- **Big-endian** (byte-order pin hard-wired BE on Casio calcs).
- Screen 396×224, 16-bit color; display controller **R61524**.
- Ghidra load (community-confirmed): flat binary, processor **SH-4A**, **big-endian**,
  base **0x80000000**, with mirror at **0xA0000000** (P1/P2 cached/uncached mirror).

## Prior art / references (ingest these)
- **Heath123/casio-emu** (https://github.com/Heath123/casio-emu) — WIP open-source CG50
  emulator. Custom SH4 interpreter (C/C++), Qt UI + web port. `os` branch boots the REAL
  OS from a hardware dump (experimental, crashes often). Our reference + validation oracle.
- **gint / fxsdk** (Lephenixnoir, git.planet-casio.com) — bare-metal kernel; its drivers
  are a reverse-engineered SH7305 peripheral map. `fxcg50.ld` = memory layout.
- **WikiPrizm** (prizm.cemetech.net) — peripheral + display docs.
- **Simon Lothar's fxReverse / "Calculators based on the SuperH"** — canonical doc for
  OS load addresses, syscall table, AND the **USBPower OS-file container format** (needed
  next). libfxcg (Jonimoose/libfxcg) has the Prizm syscall list.
- MAME SH-4 core — clean reference CPU implementation.

## Toolchain (reused from april/calc — already installed)
- Ghidra **12.0.4** at `F:\ru\myprojects\may\ghidra_12.0.4_PUBLIC`.
- **GhidraMCP** bridge → lets Claude drive disassembly via `mcp__ghidra__*` tools once a
  binary is open in CodeBrowser with GhidraMCPPlugin enabled (HTTP :8080). See
  `F:\ru\myprojects\april\calc\GHIDRA_SETUP.md`. NOTE: MCP server registered for the
  *april/calc* project — will need re-registering for this project dir (restart Claude
  Code after `claude mcp add`).
- 7-Zip at `C:\Program Files\7-Zip\7z.exe`.

## OS acquisition — DONE (official update route)
Downloaded official **fx-CG50 OS 3.80** Windows updater (public, no hardware needed):
`https://education.casio.co.uk/app/uploads/2023/05/fx-cg50_G90_series_update_380_2b.zip`

Unwrap chain (all under `F:\ru\myprojects\may\cg50\os\`):
1. `update_380.zip` (20 MB) → `extracted/.../*.exe` (InstallShield self-extractor).
2. Running the `.exe` self-extracts its MSI to `%TEMP%\{GUID}\fx-CG50 Series OS Update.msi`
   (it just waits for a calculator at the "connect" screen — can't flash anything with no
   device; we killed it after grabbing the MSI). 7-Zip can also list the exe (`[0]` blob =
   `InstallShield\0` archive, can't open directly — the run-and-grab-MSI route is the one
   that worked).
3. `7z e <msi> "ISSetupFile.SetupFile*"` → `os/msi_files/`. The MSI's ISSetupFile streams:
   - SetupFile1 (64 KB) + SetupFile2 (12 MB) = **PE/MZ Windows exes** (the updater app) — ignore.
   - **SetupFile3–7 = Casio firmware**, stored **bitwise-inverted**. Header `AA AC BD AF
     90 88 9A 8D` == NOT("USBPower"). 5 segments, tags near 0xE0 (CGE1, …).
4. `os/decoded/` = each firmware segment with whole-file bitwise-NOT applied:
   - `fw3_770k.bin`, `fw4_1m8.bin` (largest, has a "VER$" string), `fw5_83k.bin`,
     `fw6_329k.bin`, `fw7_406k.bin`. Each starts with clear-text `USBPower,` after NOT.
   - Exactly ONE `USBPower` marker per file (single header, not repeating records).

## USBPower container — SOLVED
Format (learned empirically, scripts in `re/`):
- `bytes[0x00:0x40]` = USBPower header, PLAIN text (`USBPower,` + fields, mostly 0xFF).
- `bytes[0x40:]` = payload, stored **bitwise-inverted**. (The MSI additionally inverts the
  whole file, so in the raw MSI stream the payload is already plain & the header inverted.)
- To get plain payload: `raw_msi_stream[0x40:]`  ==  `NOT(decoded_file[0x40:])`.

The 5 USBPower segments (fw3–fw7) are all **bundled ADD-INS**, not the OS:
- fw3 = Geometry, fw4 = **Physium** (periodic table, biggest add-in), fw5 = Picture Plot,
  fw6 = 3D Graph, fw7 = Prob Sim. (Each is a .g3a; payload begins with its name table.)

## The main OS — LOCATED, but PACKED (current wall)
The OS is NOT a USBPower file. It's embedded in **SetupFile2** (12 MB x86 PE updater), in
its `.rsrc` (10.5 MB), as RCDATA resources (extracted to `os/pe2_rsrc/.rsrc/1033/RCDATA/`):
- **RCDATA/3070** (4,654,493 B) and **RCDATA/3071** (4,654,460 B) = the OS for the two
  models the "G90 series" updater serves (fx-CG50 intl + Graph 90+E FR). Identical first
  32 bytes, diverging tails.
- **RCDATA/3069** (42,889 B) = likely bootloader/preloader.
- All three: **entropy ~7.99/8.0, all 256 byte values** → compressed or encrypted. Header
  `EC BD 79 5C 5B 47 96 30 ...`. NOT standard zlib/gzip/xz/lz4/bzip2/lzma. Custom packer.

### NEXT — two routes to a plain OS image (decision pending with user)
1. **Reverse the updater's unpacker** (no hardware): load SetupFile2 (x86 PE) into Ghidra
   — same toolchain we used on the hp39gii — find the routine that consumes RCDATA 3070/3069
   and decompresses/decrypts it before USB-flashing; reimplement it to unpack the blobs.
   Self-contained RE puzzle; gives the OS now.
2. **Dump the physical CG50** (route B): gint USB flash dump → the live, already-unpacked OS
   (plus boot ROM region the update lacks). Authoritative; needs the calculator + USB setup.
   Recommended as a later cross-check regardless.

Once unpacked: load plain OS @ 0x80000000, SH-4A, big-endian → begin the comprehensive study.

## Comprehensive-study deliverables (the emulator's spec sheet)
1. Memory map (RAM/flash/MMIO, P0–P4 regions, cached/uncached mirrors).
2. MMIO register inventory — every peripheral register the OS touches (→ what we must emulate).
3. Syscall table.
4. Interrupt vectors + boot/reset sequence.
5. Driver deep-dives: R61524 display, keyboard matrix (the first "it's alive" milestones).

## Study findings (live log) — started 2026-05-31

Plain OS loaded in Ghidra: SuperH4 (SH-4), big-endian, base **0x80000000** (block
80000000–80b5ffff, 0xb60000). Rebase done; absolute 0x8xxxxxxx refs now resolve.
Workflow: Ghidra/MCP for code; `re/dump_header.py` reads the plain image directly
(file off = vaddr − 0x80000000) for data/headers.

### Ghidra setup notes (so the trace works)
- Added a **byte-mapped P2 mirror block at 0xA0000000 → 0x80000000** (len 0xb60000) so the
  boot code's uncached (0xa0xxxxxx) jumps/refs resolve. Marked 0x80000000 as code (D/F);
  re-ran full analysis. Boot functions now auto-follow.
- Renamed: reset_entry(0x80000000), boot_pfc_wdt_init(0xa0000670),
  boot_cpg_pll_init(0xa000069a), boot_bsc_sdram_init(0xa000063c),
  boot_os_startup(0xa00006cc). (0x80003550 = OS main loop — not yet a defined fn.)

### Boot/reset sequence — MAPPED (entry @ 0x80000000 = reset_entry)
Fully traced from the SH-4 reset stub through hardware bring-up into the OS:
1. **CPU state:** SP ← 0xFD804000 (on-chip RAM); SR ← 0x700000F0 (MD=1,RB=1,BL=1,IMASK=15).
2. **Cache/MMU:** CCR(0xFF00001C) ← 0x800; `icbi @0xA0000000`; MMUCR(0xFF000010) ← 4 (TLB flush).
   Reads HW-strap 0xFF000024 → picks **model code 0xCA00/0xCA01/0xCA02** (fx-CG10/20/50
   variants) and stores it to RAM global **0x8C04CA24**.
3. **boot_pfc_wdt_init (a0000670):** PFC pin-mux writes @0xA4050184 (4 port-ctrl regs);
   WDT @0xA4520000 key writes (0x5A00 WTCNT, 0xA5xx WTCSR).
4. **boot_cpg_pll_init (a000069a):** CPG @0xA4150000 — FRQCR RMW (&0x000F00F0 | 0x8F001102),
   PLL regs @+0x24/+0x50, **poll ready bit0 @ 0xA4150060**. (reset stub also pre-pokes
   0xA4150020/30/38.)
5. **boot_bsc_sdram_init (a000063c):** memory/bus controller @0xFEC10000 — 16-register
   timing block (vals 0x36DA0400, 0x36DA3400, 0x36DB4400, 0x17DF0400, 0x34D30200, …) + 0xFEC10040.
6. **boot_os_startup (a00006cc):** zeroes globals (0xFF2F0004, 0x8C04CA34), calls a chain of
   init fn-ptrs, then loops forever. Hands off to **cached OS** code:
   - early/uncached: a0000a3e, a0000aec, a0000634(arg 0x8C160000), a000085c, a00004b0→int,
     cond a0020008.
   - **cached OS init:** 0x8000495a(0, *(0x80001554)-0x100), 0x80002600, 0x80009E04.
   - **MAIN LOOP:** `do { (*0x80003550)(); } while(true)`  ← **0x80003550 = OS main loop**.

### OS main loop & input/event core — MAPPED (os_main_loop @ 0x80003550)
boot_os_startup calls os_main_loop(0x80003550) forever. One iteration dispatches ~12
subsystem handlers via an interleaved pointer/const table @0x8000372c..0x80003764:
| slot | value | role |
|------|-------|------|
| 0x372c | 0xA000085C | early service (uncached) |
| 0x3730 | 0x800029F4 | handler(1) |
| 0x3734 | 0x80002600 | (big init/service fn — has unrecovered jumptable @0x800026EA) |
| 0x3738 | 0x80009E04 | table refresh (→0x80009DE4, 128-entry copy) |
| 0x373c | 0x80002C38 | handler |
| 0x3740 | 0xEFFFFF0F | **SR mask** (clears BL + IRQ bits), applied → arg of 0x80001d34 |
| 0x3744 | 0x80001D34 | set IRQ mask / SR |
| 0x3748 | 0xA4050138 | **PFC port reg**: `*0xA4050138 \|= 0x10` each loop (pin strobe) |
| 0x374c | 0x80002A32 | **kbd_state_get_f8** (also the low-level key getter) |
| 0x3750 | 0x80002B5A | status poll → int |
| 0x3754 | 0x800066D0 | **key_event_read** → int (gate) |
| 0x3758 | 0x8000936E | **event_dispatch**(0) |
| 0x375c | 0x80002E00 | handler |
| 0x3760 | 0xA00008EE | handler (uncached) |

Input/event core (named in Ghidra):
- **key_event_read (0x800066d0)** → **key_event_poll (0x80006692)**: zeroes an 8-byte event
  struct, calls low-level getter `(*0x80006868 = 0x80002A32)()`; on result==4 records press
  (debounce wait via 0x80002ff6(100)), checks code via func 0x80006708.
- **kbd_state_get_f8 (0x80002a32)**: `return *(*0x80002bd8 + 8)` — reads field +8 of the
  **keyboard-state struct @ 0xFD8007D0** (on-chip RAM). The matrix SCAN that fills this
  struct is elsewhere — almost certainly a **timer ISR** (→ next target, ties to interrupts).
- **event_dispatch (0x8000936e)**: modal message-pump (services handlers, `*flag &= 0xf`,
  loops on 0x800094ac until an event).

### Interrupt system — MAPPED (VBR + dispatcher + handler table)
- **VBR = 0x800014D4** (set by exception_vbr_mmu_init @0x800034a0; early boot used 0x80001554).
  Vector table region 0x800014D4..~0x80001C00; SH-4 layout (general@+0x100, TLB-miss@+0x400,
  interrupt@+0x600). Tool: `re/sh4dis.py` (our SH-4 disassembler) reads vector stubs the
  decompiler won't (vectors are reachable only via VBR).
- **Interrupt dispatcher @0x80001A54** (fully decoded):
  saves SSR/SPC/PR/r0-7_bank; `r1 = INTEVT(*0xFF000028)`; `idx = (INTEVT-0x40)>>3`;
  `handler = *(0xFD8004D0 + idx*4)`; `prio = *(0xFD8006D0 + (INTEVT-0x40)>>5)` (byte);
  `SSR = (SR & 0xCFFFFF0F) | prio`; `SPC = handler`; `PR = 0x80001574` (common restore
  trampoline); `rte`. → emulator interrupt contract.
- **Handler table** copied from ROM **0x80001300** → on-chip RAM **0xFD8004D0** (119 entries),
  priority table from **0x800014DC** → **0xFD8006D0**, by FUN_80009DE4 (=0x80009E04, boot init
  + main-loop slot 0x3738). Default/spurious handler = **0x80003B20** (`rts`). Real ISRs:
  ⚠️ **INTEVT CODES IN THIS TABLE ARE SUSPECT** — see correction below. The dispatcher
  indexes a 4-byte table by `(INTEVT-0x40)>>3`, so a real INTEVT must be a multiple of 0x20;
  several codes here (0x188, 0x270, 0x338, 0x328, 0x330, 0x2D8…) are not, so they were
  mis-derived. **The genuine timer-tick INTEVT is `0x560`** (verified on the 3.60 live dump:
  emu/dump_irqtable.py + emu/test_candidates.py — handler 0x801ded94 there; the 3.80 handler
  for slot 0x560 is the 0x80002C8x timer ISR). The *handler addresses* below are still 3.80;
  only the INTEVT labels need re-derivation (INTEVT = 0x40 + table_byte_offset*8).
  | INTEVT | handler | notes |
  |--------|---------|-------|
  | **0x560** (was "0x188") | 0x80002C8C (3.80) | timer/periph ISR: calls 0x8000279A + helper 0x8000955E(1), **acks IRQ @0xA4610088** (clr bits14/15), tail 0x800027AE. Drives the keyboard scan (KEYSC). ← verified as the working tick in the emulator |
  | (0x0a0 idx12) | 0x80002C5C | rts (disabled) |
  | 0x2D8 | 0x80003144 | |
  | 0x270/338/340/370 | 0x80009740 | shared (×4) |
  | 0x2A8/3F0 | 0x80009694 | shared |
  | 0x2B8 | 0x8000AC18 | |
  | 0x328 | 0x80002CC4 | keyboard module |
  | 0x330 | 0x8000A756 | |
  Shared ISR helper **0x8000955E(1)** (post-event/tick); a 2nd ISR @0x80002C70 calls
  0x8000303C then 0x8000955E then 0x80003112.

### Memory map (confirmed so far)
- **0x80000000** P1 cached / **0xA0000000** P2 uncached — OS image (mirror), 0xb60000.
- **0x8C000000** = main DRAM (RAM globals: 0x8C04CA24 model code, 0x8C04CA34; buf 0x8C160000).
- **0xFD800000** = on-chip RAM — boot stack (SP 0xFD804000), kernel state structs
  (keyboard-state struct @ 0xFD8007D0), **IRQ handler table @0xFD8004D0 + priority @0xFD8006D0**.

### Keyboard + timer pipeline — MAPPED (deliverable #5, "it's alive" path)
The CG50 does NOT bit-bang the matrix — it uses a hardware key-scan controller:
- **KEYSC @ 0xA4080000** = keyboard matrix controller. 12 halfword matrix-data regs at
  +0x00..0x16; control/scan at +0x04/+0x14/+0x90/+0x94/+0xD4. Pointer cached at *0x80002d1c.
- Driven/timed by **TMU @ 0xA4490000** (channel regs +0x14/+0x18/+0x1C/+0x28) and a 2nd timer
  **ETMU @ 0xA44A0000**.
- **Timer ISR** (INTEVT 0x188 → handler 0x80002C8C): acks IRQ @0xA4610088 (clr bits14/15),
  raises **event flag byte @ 0x8C04DE8C** (DRAM) via helper 0x8000955E(1), bit-twiddles
  0xA4080090/04 via 0x8000279A.
- **Polled servicing** in os_main_loop: FUN_80002C38 (slot 0x373c) clears the 12 KEYSC regs;
  FUN_80002BFC clears+triggers; result cooked into key-state struct @0xFD8007D0; then
  key_event_poll(0x80006692)/key_event_read → event queue → event_dispatch(0x8000936E).
- **Low-level keyboard driver module @ ~0x801E0000** (register table @0x801e03a8 lists KEYSC
  0xA4080094/14/D4, 0xA4610088, and 0xA4140024/64). Functions there undefined in Ghidra
  (data-interleaved) — read via sh4dis.py.

### ★ SYSCALL TABLE — FOUND (the master unlock)
- Dispatcher (syscall stub @0x80020070): `mov.l #0x806A2014,r2; shll2 r0; mov.l @(r0,r2),r0;
  jmp @r0`. So **handler = *(0x806A2014 + id*4)**. **SYSCALL_TABLE base = 0x806A2014**, ~8040
  valid entries. Caller puts syscall id in r0. This resolves ANY documented libfxcg/Prizm
  syscall number → its OS implementation. Tool: `re/syscall.py` (`python syscall.py 0x25f`).
- Verified entries: sc004→0x8002c64a (matches old thunk_EXT_FUN_8002c64a). Display/key syscalls
  below all resolve to real code.

### Display driver (R61524) — MAPPED  ("draw a frame" path)
- **R61524 LCD controller on bus area 5 (CS5)**: command/data port **0xB4000000** (P2 uncached;
  P0 mirror 0x14000000). RS via address line: command @base, data @base+2. The OS reaches it
  via a region-descriptor table @~0x806c1f00 (no hardcoded immediate in most code).
- **VRAM @ 0xAC000000** (uncached DRAM mirror of 0x0C000000 / cached 0x8C000000).
- **Bdisp_PutDisp_DD (sc 0x025F) = 0x80055260**: sets R61524 GRAM window, then **DMAs VRAM→LCD**
  via **DMAC channel 2** (SAR/DAR/DMATCR/CHCR @ **0xFE008020**, DMAOR master @ **0xFE008060**,
  CHCR=0x00101400, count 0x1440), spins on CHCR bit1 (done), then SynchronizeDataOperation.
  Display enable/clock gated via CPG 0xA4150030 + PFC 0xA405013c.
- Resolved display/key syscalls (OS 3.80): Bdisp_PutDisp_DD 0x80055260, _stripe 0x80055266,
  Bdisp_SetPoint_VRAM 0x800555dc, Bdisp_AllClr_VRAM 0x8005563c, Bdisp_PutDispArea_DD 0x800ce722,
  GetKey 0x800c8cb6, PutKeyCode 0x80196586, malloc 0x801dc406, memset 0x80376466.
  (Note: some libfxcg numbers may be off-by-version; verify each by decompiling.)

### MMIO additions
- **0xFE008000** = **DMAC** (SH-4 DMA controller); ch2 @0xFE008020 used for VRAM→LCD; DMAOR @0xFE008060.
- **0xB4000000** = **R61524 LCD** (area 5; P0 mirror 0x14000000). VRAM @ **0xAC000000** (DRAM).
- **0xA4080000** = **KEYSC** keyboard matrix controller (12 data regs +0..0x16, ctrl +0x90/94).
- **0xA4490000** = **TMU** timer unit; **0xA44A0000** = ETMU (extra timer).
- **0xA4610000** = timer/peripheral block; IRQ flag/ack @ **0xA4610088/8A** (ISRs clr bits14/15).
- **0xA4140000** = block used by kbd driver (regs +0x24/+0x64) — identify (INTC? port?).
- (Confirm names/INTEVT codes vs gint SH7305: cpg 0xA4150000, tmu 0xA4490000, keysc 0xA4080000.)

### MMIO register map (deliverable #2 — live)
| base / addr | peripheral | notes |
|-------------|-----------|-------|
| 0xA4150000 | **CPG** (clock) | FRQCR@+0; PLL@+0x24/+0x50; ready bit0@+0x60; +0x20/30/38 in reset |
| 0xA4520000 | **WDT** | 0x5A00 WTCNT / 0xA5xx WTCSR key writes |
| 0xA4050000 | **PFC** (pin function) | port-ctrl @0xA4050184 |
| 0xFEC10000 | **bus/SDRAM ctrl** | 16-reg timing block; also 0xFEC10040 |
| 0xFF000010 | MMUCR | =4 → TLB flush |
| 0xFF00001C | CCR (cache ctrl) | =0x800 |
| 0xFF000024 | HW revision/strap (R) | selects model 0xCA00/01/02 |
| 0xFF2F0004 | (control, zeroed at boot) | TBD |

### OS header @ 0x80020000  (the main CASIOWIN header)
- magic "CASIOWIN" @0x80020000; version string **"03.80.0000"** @0x80020020.
- trampoline @0x80020070 (mov.l/jmp) → 0x806a2014.
- A second "CASIOWIN" @0x80000e98 sits in the reset area (bootloader signature check).

### TODO next
- **Confirm the keyboard scan**: follow the timer ISR's work calls (0x8000279A / 0x8000303C)
  and helper 0x8000955E → find PFC row-select/col-read (0xA4050000) + the writer of struct
  @0xFD8007D0. Identify the 0xA4610000 peripheral and INTEVT→source map vs gint SH7305.
- The region 0x80002600..~0x80003B30 holds many ISRs/keyboard fns but is NOT functionized
  (leftover from the bloat deletion). Either D/F the specific ISR entries or use sh4dis.py.
- Fix the unrecovered jumptable @0x800026EA so 0x80002600 stops bloating (recover switch).
- Find the OS **syscall table** (guess 0x80020070 was wrong — no xrefs). Locate via the
  syscall trampoline pattern / most-referenced 0x8002xxxx constant.
- Cross-check MMIO addrs against gint (SH7305 peripheral map) + WikiPrizm. Confirm 0xFEC10000
  peripheral identity (BSC vs DBSC) and 0xA41500xx CPG register names.

### Functions named in Ghidra so far
reset_entry(0x80000000), boot_pfc_wdt_init(0xa0000670), boot_cpg_pll_init(0xa000069a),
boot_bsc_sdram_init(0xa000063c), boot_os_startup(0xa00006cc), os_main_loop(0x80003550),
key_event_read(0x800066d0), key_event_poll(0x80006692), event_dispatch(0x8000936e),
kbd_state_get_f8(0x80002a32), exception_vbr_mmu_init(0x800034a0), Bdisp_PutDisp_DD(0x80055260).
(syscall_dispatch @0x80020070 is a stub, not a Ghidra function — can't rename.)

### Reusable RE tooling (re/)
- `sh4dis.py` — SH-4/SH-4A big-endian disassembler (`python sh4dis.py <start> <end>`), reads
  vector stubs / undefined regions straight from the image. Integer+system ISA; FPU coarse.
- `syscall.py` — resolve syscall id → handler via table @0x806A2014 (`python syscall.py 0x25f`).
  THE way to find any OS API fn that Ghidra left un-functionized (display, files, etc.).
- `peek.py` / inline `python -c` constant-search — resolve literal pools & find xrefs to a
  32-bit constant in the image (file off = vaddr & 0x0FFFFFFF).
- `find_vbr.py` — scan for VBR/exception opcodes. `dump_header.py` — header/region dumps.

## Emulator build — STARTED (2026-05-31)
Python SH-4A interpreter under `emu/` (see `emu/NOTES.md`). Boots the unpacked OS from
PC=0x80000000 and **reproduces the documented boot MMIO writes exactly** (BSC 0xFEC10000=
0x00010013; CPG 0xA4150020/30/38; CCR=0x800; MMUCR=4; PFC pin-mux; WDT 0x5A00/0xA507) —
the RE is validated by execution. Runs 2000+ instructions into the boot init chain.
Files: emu/memory.py (address space + mirrors), emu/mmio.py (peripheral stubs),
emu/cpu.py (SH-4 core: integer+system ISA, delay slots, SR.RB banking; FPU/div1/MMU stubbed),
emu/run.py (`python emu/run.py [max] [trace]`). Reuses re/sh4dis.py for the trace.
Next core work: stub 0xA413FEC0 poll; real div1; interrupt delivery (INTEVT + 0xFD8004D0
table → handler @0x80001A54) so TMU/KEYSC IRQs fire; then VRAM→LCD capture = first frame.

UPDATE: emulator now **boots the real OS 12.8M instructions to Bdisp_PutDisp_DD** (the
VRAM→LCD frame push). DMAC programmed exactly as RE'd: SAR=0x0C000000 (VRAM), DAR=0x14000000
(LCD), DMATCR=0x1440 — display contract validated by execution. Boot-layer fixes added:
icbi; real div1; free counter @0xA4130000; ETMU elapsed flag @0xA44A0060; DMAC TE-done;
NOR-flash address window (image/0xFF reads, command writes ignored). First frame is a blank
screen-clear; rendering the menu needs INTERRUPT DELIVERY (next) + more runtime (+maybe FPU).

UPDATE (2026-06-02): **cross-version boot validation — the emulator is OS-version-independent.**
Booted the PHYSICAL 3.60 dump (`os/flash_dump/os.bin`) in the same emulator built on 3.80, via
`emu/run_dump.py` (`python emu/run_dump.py [max_ins] [lockstep_cap]`).
 - Phase A (lockstep 3.60 vs 3.80 from reset): **500,000 instructions executed bit-for-bit
   identically, zero divergence** — same boot MMIO writes, same instruction stream. Confirms the
   SH-4A core + peripheral model are faithful (two independent images drive the silicon the same).
 - Phase B (3.60 solo): ran the full **2,000,000 instructions with NO fault** (stopped only at the
   cap), set up its own vectors (**VBR=0x80020f00**, a 3.60-specific addr vs 3.80's 0x800014D4),
   reached PC≈0x801df468 (low-level driver region, ~same neighborhood as 3.80's 0x801E0000 kbd mod).
 - New unmapped-MMIO observation (present in BOTH versions, non-fatal): the OS sweeps a contiguous
   block **0xFE380000–0xFE38BFFC** (+0xFE3C0000, 0xFE3FFD00) during init — likely an on-chip
   RAM/array region we haven't mapped yet. Identify later; bus currently returns a value not a fault.

## Decisions log
- CPU-core strategy: user leans Ghidra-first recon, then build (hybrid: our own SH-4A core
  validated against casio-emu). Revisit fork-vs-scratch after the study.
- OS image source: official update (done). Dump from physical CG50 later for boot-complete
  full-flash image (incl. boot ROM the update lacks) + to cross-check our parsed layout.
