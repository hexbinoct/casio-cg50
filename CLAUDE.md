# fx-CG50 → Android emulator project

See `RECON_NOTES.md` for full reverse-engineering state and the running plan.

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
  `python emu/conformance_gen.py && python emu/gen_golden.py` — and add new conformance cases
  for any new/edge instruction behaviour; (2) prove the Go port still matches with
  `go -C emu_go test .` (conformance 53/53 + 2M-instr golden boot). Never validate the port by
  ad-hoc running and eyeballing.
- Run the Go emulator: `go -C "F:/ru/myprojects/may/cg50/emu_go" run . [maxIns] [timerPeriod] [mode]`
  (no leading `cd`; the `-C` flag keeps the allowlist token = `go`). `mode=ftl` probes flash-FTL returns.
- Boots the real **3.60** full flash (`os/flash_dump/flash_full.bin`); reaches system idle. See
  `RECON_NOTES.md` RESUME block for the open render-gate.

## Ghidra (this project)

- **3.60 `os/flash_dump/os.bin` is loaded, rebased to 0x80000000** (use the runtime 0x80xxxxxx
  addresses directly). The 3.80 updater image is the other program.
- GhidraMCP is our **fork at `F:\ru\myprojects\may\lwired`** supporting multiple open programs:
  `list_open_programs` / `get_current_program` + an optional `program` arg on every tool. These
  appear ONLY after the MCP reconnects at session start — if they're missing, the bridge needs a
  Claude Code restart / MCP re-add. Without them, tools act on the currently-focused program.
