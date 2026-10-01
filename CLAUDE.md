# fx-CG50 → Android emulator project

## 👉 FIRST STEP EVERY SESSION (do this before any emulator/Android/RE work)
**Read the `RECON_NOTES.md` "⏯ RESUME HERE" block at the top of the file.** It is the canonical
running state: what was just done, what is shipped vs in-progress, the current open task, and the
exact entry points (addresses/files/next steps) to continue. Do this without being asked — it is how
you reconstruct context across sessions (you do not retain prior conversations). `RECON_NOTES.md` holds
the full reverse-engineering history below that block.

## Two machines
| | Office (Windows) | Home (Mac, Apple Silicon) |
|---|---|---|
| this repo | `F:\ru\myprojects\may\cg50` | `~/main/casio/casio-cg50` |
| sibling add-in projects | `F:\ru\myprojects\september\cg50_addons` | `~/main/casio/cg50_addons` |
| firmware in git-ignored `os/` | the originals | copied by hand from the office (+ the 32 MB dump made on the Mac) |
| Python | `python` | `python3` (`/usr/local/bin/python3` has Pillow) |
| Ghidra | GUI + GhidraMCP fork (below) | headless: `~/main/casio/ghidra_cg50` (`decomp.sh <addr>…`, `refs.sh <value>…`) |
| full suite | ~80 s | ~21 s |

Paths below are written for the office; on the Mac use the equivalents.

## Working style (IMPORTANT — minimizes approval prompts)

The user's permission allowlist matches on the **leading program token**, and almost every
ad-hoc shell command is different, so per-command "always allow" never sticks and creates
constant approval prompts. Therefore:

- **Do NOT** drive multi-step work with one-off PowerShell/Bash commands (no ad-hoc `cd`,
  file writes via shell, `Expand-Archive`, byte-twiddling one-liners, etc.).
- **Instead: put the logic in a Python script** (write/edit it with the Write/Edit tools,
  which don't need shell approval) and **run it with a single consistent invocation:
  `python <script.py>`**. Once the user allows `python` once, every later run is approved.
- Keep reusable scripts under `F:\ru\myprojects\may\cg50\re\`. Have scripts do their own
  file I/O, directory creation, extraction, parsing, and print a clear summary — so a whole
  step = one `python` call, not a dozen prompts.
- Use the Write/Edit/Read/Glob/Grep tools (not shell `echo`/`cat`/`Get-Content`) for files.

## Emulator: Go is primary, Python is the oracle — ALWAYS keep tests green

The emulator was rewritten in **Go** (`emu_go/`, ~64–85 M instr/s, ~1000x the Python core).
The **Python emulator (`emu/`) is the reference oracle**, not dead code.

- **Always write/update tests.** When you change CPU/MMIO behaviour in `emu/cpu.py` or
  `emu/mmio.py`: (1) regenerate the frozen goldens —
  `python emu/conformance_gen.py && python emu/gen_golden.py`, plus `python emu/keysc_selftest.py`,
  `emu/lcd_selftest.py`, `emu/adc_selftest.py` for those devices — and add new conformance cases for
  any new/edge instruction behaviour; (2) prove the Go port still matches with `go -C emu_go test .`
  (conformance 68/68 incl. MMU cases, 2M-instr golden boot, the three device transcripts, and the
  scheduled-vs-exact equivalence test). Never validate the port by ad-hoc running and eyeballing.
- Run the Go emulator: `go -C "F:/ru/myprojects/may/cg50/emu_go" run . [maxIns] [timerPeriod] [mode]`
  (no leading `cd`; the `-C` flag keeps the allowlist token = `go`). `mode=ftl` probes flash-FTL returns.
- Boots the real **3.60** flash. Two images in `os/flash_dump/`: `flash_full.bin` (the June 16 MB dump:
  the goldens, the suite and `cg50_state.bin` are built on it — keep it) and `flash_full_32mb.bin` (the
  whole 32 MB flash, incl. all of the storage memory fls0) with its own `cg50_state_32mb.bin`, booted
  with `CG50_FLASH` / `CG50_STATE` and `CG50_WARM=1` (a power-on from "off": the OS skips first-boot
  setup only if IL RAM 0xFD8017DC == 1, which its power-off routine sets) — RECON_NOTES cont.18w.
  Runs every built-in app and add-ins incl. gint ones; the Android app (`android/`) runs it on the
  user's phone. The current open task is in the `RECON_NOTES.md` RESUME block (direction since
  2026-09-30: the emulator as the everyday test device, desktop + web/WASM — `docs/DESKTOP_AND_WEB.md`).
- The gint-support devices (cont.18v: ETMU/TMU timers, a gating INTC, DMAC memory-to-memory, X/Y/IL
  RAM; cont.18x: requests gated again when accepted) exist **only in Go**; the Python oracle's
  `INTCStub` does not gate. The OS's boot doesn't depend on it, so the goldens are unaffected.

## Ghidra (this project — the office machine; on the Mac see "Two machines")

- **3.60 `os/flash_dump/os.bin` is loaded, rebased to 0x80000000** (use the runtime 0x80xxxxxx
  addresses directly). The 3.80 updater image is the other program.
- GhidraMCP is our **fork at `F:\ru\myprojects\may\lwired`** supporting multiple open programs:
  `list_open_programs` / `get_current_program` + an optional `program` arg on every tool. These
  appear ONLY after the MCP reconnects at session start — if they're missing, the bridge needs a
  Claude Code restart / MCP re-add. Without them, tools act on the currently-focused program.
