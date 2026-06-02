# casio-cg50

A from-scratch **hardware emulator and reverse-engineering toolkit for the Casio
fx-CG50** (Prizm-family graphing calculator, Renesas **SH7305 / SH-4A** core).

The emulator boots the **real fx-CG50 OS from reset** — through PLL/clock bring-up,
interrupt setup, the battery ADC, the NOR-flash (`fls0`) filesystem mount, and the
LCD render path — to the fully rendered first-boot UI, and can be **driven with
injected keypresses all the way through first-boot setup to the MAIN MENU**.

> ⚠️ **No Casio firmware is included in this repository.** The OS is Casio's
> copyrighted property. To run the emulator you must supply a flash dump of *your
> own* calculator. See [Getting a flash dump](#getting-a-flash-dump).

## What's here

| Path | What |
|------|------|
| `emu_go/` | **The emulator (Go).** SH-4A integer/system core, MMU/memory map, SH7305 MMIO peripheral models, LCD framebuffer decode. ~64–85 M instr/s. This is the primary implementation. |
| `emu/` | **The Python reference emulator** — the *oracle*. Slower, but the authoritative model that the Go port is validated against. |
| `emu/conformance.json` | Cross-language, curated SH-4 instruction conformance suite (synthetic — not derived from any OS). Both cores replay the same frozen cases. |
| `re/` | Reverse-engineering scripts: a small SH-4 disassembler (`sh4dis.py`), VBR/IRQ-table finders, framebuffer/stride explorers, the KEYSC key-matrix table dumper, and the empirical keymap sweepers. |
| `tools/flash_dump/` | A minimal on-calculator flash **dumper** (`dump.c`, gint/fxlink) plus notes — for capturing your own dump. |
| `RECON_NOTES.md` | The full reverse-engineering log: boot path, gates found and closed (ETMU delay, battery ADC, model-strap, writable NOR flash, VRAM uncached-mirror + stride render gate), the interrupt system, the keyboard input path, and the empirically-derived key matrix. |

## Status

- ✅ Boots OS 3.60 from reset to the rendered **first-boot "Message Language"** screen.
- ✅ **Keyboard input solved** — keys are injected by calling the OS's own scan-enqueue
  routine as a subroutine (faithful: the key lands in exactly the queue the UI reads).
- ✅ Driven through the entire first-boot setup (Language → Display → Power → Battery,
  the battery confirm dialog, the post-setup note) to the **MAIN MENU** (3×4 app grid).
- ✅ Go core validated against the Python oracle: a 53-case instruction conformance
  suite + a 2000-checkpoint golden boot trace.
- ⏳ Next: launching an app from the menu; `fls0` settings persistence.

The empirically-mapped key matrix and the exact key sequence that reaches the main
menu are documented in `RECON_NOTES.md` (see the cont.12 RESUME block).

## Getting a flash dump

This repo intentionally ships **no** Casio code. Dump your own fx-CG50:

- Easiest: a gint/fxlink-based dumper — see `tools/flash_dump/README.md` and `dump.c`.
- Place the resulting full flash image at **`os/flash_dump/flash_full.bin`** (16 MB).
  The `os/` directory is git-ignored precisely so firmware never gets committed.

The Go golden-boot test (`emu_go/golden_test.go`) self-skips if the flash image or the
(also un-committed, OS-derived) golden trace is absent. The instruction **conformance**
test needs neither and runs standalone.

## Build & run (Go)

```sh
# from the repo root, with your own flash_full.bin in os/flash_dump/
go -C emu_go run . 360000000 30000

# drive first-boot setup to the MAIN MENU via injected keypresses:
go -C emu_go run . 360000000 30000 seq "1-9,1-9,1-9,1-9,6-9,6-9,1-9,1-9,2-1" 130000000 14000000
```

Run modes are dispatched by the 3rd argument (`drive`, `key`, `seq`, `prof`, `wmap`,
`flashwr`, …); see `emu_go/main.go`.

## Tests

```sh
go -C emu_go test ./...          # conformance (53) + golden boot (2000 checkpoints)
python emu/conformance_gen.py    # regenerate the frozen conformance cases
python emu/test_cpu.py           # replay them on the Python core
```

## Legal

This is an independent, clean-room-style reverse-engineering / emulation project for
**interoperability and education**, using a dump of hardware the author owns. It
contains **no Casio firmware, ROM images, or other Casio intellectual property** —
only original code and original analysis notes. "Casio" and "fx-CG50" are trademarks
of CASIO COMPUTER CO., LTD.; this project is not affiliated with or endorsed by Casio.

## License

[MIT](LICENSE) — covers the original code/docs here only, not any Casio material.
