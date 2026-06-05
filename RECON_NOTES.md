# fx-CG50 → Android emulator — recon notes

> ## ⏯ RESUME HERE (last session end: 2026-06-05 cont.18f)
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
