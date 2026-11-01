# sh4: disassembler and code analysis for the fx-CG50's CPU

Package `cg50emu/sh4` decodes and analyses SH4AL-DSP code: the CPU of the fx-CG50's SH7305. That
covers the SH-4A integer and system instructions, the SH-4A additions (movli/movco, movua, synco,
icbi, prefi, banked registers) and the DSP set, including the 32-bit parallel-processing forms.
The SH7305 has no FPU: the 0xF--- space holds the DSP instructions, and the system-register slots
that the SH-4 uses for fpul/fpscr name the DSP registers.

It is a Go port of [DASM](https://github.com/hexbinoct/cg50-dasm), our on-device disassembler
(MIT), and it does not depend on the emulator. Memory is read through a callback or an `Image`,
so the same code serves a listing of the running emulator, a dump on disk or a `.g3a` file. It
is meant for CG50 Studio's disassembly, memory and debugger views.

## What is there

| File | |
|---|---|
| `decode.go`, `dsp.go` | `Decode` / `DecodeWord` → `Instr`: address, words, size (2, or 4 for a DSP parallel instruction), text in DASM's style (`Mnemonic()`, `Operands()`), `Kind` (normal, illegal, branch, conditional branch, call, jump/call through a register, return, rte, trapa, literal load, mova), delay slot, static target, and the literal-pool address and value of `mov.w`/`mov.l @(disp,pc)` and `mova` |
| `image.go` | `Image`: bytes at an address, big-endian reads; `G3AImage` maps an add-in the way the OS does (code at 0x00300000) |
| `analysis.go` | `Analyze`: function detection from entry points, with progress and cancellation; queries `FuncAt`, `FuncContaining`, `FuncBefore`, `Classify` (code, literal pool, padding), and `AddFunction` for incremental use |
| `xrefs.go` | an index of every cross-reference (calls, jumps, branches, literal loads, mova, pointers in data): `XrefsTo`, `XrefsFrom`, `Callers`, `Callees` |
| `resolve.go` | `ResolveIndirect`: where a jmp/jsr/braf/bsrf goes, found by scanning back for the register load, with add-in calls through the syscall trampoline resolved to the syscall; `FuncName`; DASM's listing `Comment` |
| `syscalls.go`, `syscall_names.go` | `FindSyscalls` reads the running OS's own syscall table (via the trampoline at 0x80020070), so handler addresses fit whatever OS version it is; 273 syscall names |
| `strings.go` | `FindStrings`: ASCII, Casio multibyte text and big-endian UTF-16, with a minimum length, an optional NUL terminator and a text test |

```go
img := sh4.NewImage(sh4.ROMBase, flash)            // a flash dump, mapped at 0x80000000
sys := sh4.FindSyscalls(img)
an, err := sh4.Analyze(ctx, img, sh4.OSOptions(img, sys))
in, _ := img.Decode(0x80020070)
fmt.Println(in.Text, an.Comment(&in), an.FuncName(0x80020070))
for _, x := range an.XrefsTo(addr) { ... }
```

An add-in: `img, _ := sh4.G3AImage(file)` and `sh4.AddinOptions(img, sys)`. The entry points are
the add-in's entry (0x00300000), or for an OS image the reset vector, every syscall handler and
the exception and interrupt vectors (VBR + 0x100, 0x400, 0x600).

### What the analysis does

This is DASM's method. Code is followed from the entry points: fall-through, bt/bf/bra, and gcc
jump tables (mova + mov.b/mov.w @(r0,Rn) + braf, bounded by the cmp/hi before them), up to rts or
jmp. bsr targets, and jsr/jmp targets whose register was loaded from a literal pool, become
functions. Two rules then find code that nothing calls directly:
- right after a function's literal pool and padding, a prologue (a register push or
  `sts.l pr,@-r15` within the first three instructions) starts a function;
- an address held in a literal pool or an aligned data word starts one if it has that
  prologue, or if it sits right after a function and explores cleanly (every path returns, and
  there are no erased or zero words).

The words between a function's code and the end of its pool are literals (`Classify`), shown as
`.long`.

Beyond DASM, the OS preset (`OSOptions`) does three more things:
- **P2Mirror** folds 0xA0000000-0xBFFFFFFF onto the region, because the boot code runs from
  the uncached mirror of the flash.
- **VBR** evaluates the `ldc Rn,vbr` operands and adds the vectors. In 3.60, VBR = 0x80020F00
  (and 0x80001454).
- **AllHandlers** adds every syscall handler as an entry point.

The OS image gets 12,043 functions this way, against DASM's 12,016. With these three switched
off the result is DASM's exactly.

Differences from DASM, on purpose:
- `ResolveIndirect` resolves `jmp @Rn` and `braf Rn`. DASM's backward scan starts in the delay
  slot, so the jump itself is its second step and trips the "stop at a jump" rule. As a result
  DASM only resolves jsr and bsrf.
- The reference index drops DASM's limit of 64 remembered pool words. In DASM a pool word read
  by more than 64 loads is listed again as a data reference.
- It does not index data words holding 0 or 0xFFFFFFFF.
- `FuncName` also names unnamed syscall handlers (`syscall_XXXX`).

## How it was verified

The decoder and the analysis were compared with DASM's own C code, built on the host:
`testdata/dasmref.c` is a small harness around DASM's `src/sh4dec.c` and `src/analysis.c`.
`dasm_compare_test.go` builds it and compares the output line by line, covering every field of
every instruction (text, kind, delay slot, register, target, literal address, size, value and
readability):

| What | Compared | Differences |
|---|---|---|
| every 16-bit word 0x0000-0xFFFF, over synthetic memory (with unreadable literals) | 65,536 | 0 |
| every 32-bit DSP form: first word 0xF800-0xFBFF × every second word | 67,108,864 (as 1,024 hashes) | 0 |
| every even address of the 3.60 OS image (12 MB), 16 MB dump and 32 MB dump | 2 × 6,291,456 | 0 |
| function table (address, size, pool) with DASM's options: the OS of both dumps; add-ins of 0.7 MB, 2.1 MB and 0.17 MB (DASM itself) | 12,016 / 12,016 / 1,114 / 4,165 / 317 | 0 |
| cross-references to ~200 targets per image (spread function starts + the most referenced) | 29,298 / 29,293 / 4,969 / 62,455 / 4,230 | 0 |

The cross-reference index was also checked against a line-by-line port of DASM's per-target
scan for the same targets, without the 64-word limit: identical.

Committed goldens are made by this package after it matched the C code, so the decoder is
checked without C as well. None of them contains firmware:
- `testdata/words.golden.gz`: all 65,536 words, every field.
- `testdata/dsp32.golden`: one FNV hash per DSP first word, over all its second words.
- `testdata/os_listing.golden`: SHA-256 hashes of the OS listing, per MB, for known images.
- `testdata/analysis.golden`: counts and a hash of the function table, for known images.

Run them:

```
go -C emu_go test ./sh4                     # unit tests, goldens; the OS tests need a dump
DASM_SRC=<cg50-dasm>/src SH4_ADDINS=a.g3a:b.g3a go -C emu_go test ./sh4   # + the C comparisons
go -C emu_go test ./sh4 -run Golden -update # rewrite the goldens
```

The OS tests look for `os/flash_dump/flash_full.bin` and `flash_full_32mb.bin` (git-ignored, from
your own calculator), or `CG50_FLASH`. A dump the goldens don't know (another calculator's, or
another OS version) skips the golden checks; the C comparisons still apply.

## Numbers

Apple M4 Pro, Go 1.26 (`go -C emu_go test ./sh4 -run X -bench .`):

| | |
|---|---|
| `DecodeWord` with text | 26-35 ns: about 30 M instructions/s (one allocation, the text) |
| decode without text (what the analysis uses) | 18 ns: 55 M/s |
| functions of the OS (12 MB) | 50 ms, 12,043 functions |
| reference index of the OS | 60 ms, 1.36 M references |
| a 0.7 MB add-in: functions + references | 7.5 ms, 1,114 functions |
| a 2.1 MB add-in: functions + references | 27 ms, 4,165 functions |
| DASM.g3a (0.17 MB) | 1.7 ms, 317 functions |
| the whole 32 MB flash as one region: functions + references | 0.56 s, 17,290 functions |
| strings in 32 MB | 0.12 s (290 MB/s) |

On the calculator DASM needs about 15 s for the OS.

## Syscall names

The 273 names come from [libfxcg](https://github.com/Jonimoose/libfxcg). DASM's
`tools/fetch_syscalls.py` collected them from libfxcg's syscall stubs, and `syscall_names.go` is
generated from DASM's table. The numbers are the OS API and stay the same across OS versions;
the handler addresses come from the OS image's own table.

## Not done yet

- Listing helpers beyond `Comment`/`Classify`/`FuncName`: user labels and comments, saving them,
  renaming.
- Symbols from an add-in's ELF.
- Search by instruction pattern.
- A call graph beyond `Callers`/`Callees`.
- In the string scanner, a glyph map for the Casio two-byte characters (they are given as
  `\xHH\xHH`).
