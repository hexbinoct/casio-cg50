# Syscalls

A **syscall** is how an add-in calls a function of the OS: draw VRAM to the screen, read a
key, open a file, allocate memory. Each function has a number, and one small routine in the
OS turns the number into an address. This page shows that routine, the table behind it, how
to look a number up in OS 3.60, and the syscalls the other pages of this site mention.

The full list of numbers, with names and arguments, is on
[WikiPrizm](https://prizm.cemetech.net/) and in the PrizmSDK/libfxcg headers. It is not
repeated here.

## The call

Every syscall goes through the routine at **`0x80020070`**, in flash. In OS 3.60 it is five
instructions:

| Address | Instruction | What it does |
|---|---|---|
| `0x80020070` | `mov.l @(disp,pc),r2` | `r2` = the table's address, `0x80687CD0` |
| `0x80020072` | `shll2 r0` | `r0` = the number × 4 |
| `0x80020074` | `mov.l @(r0,r2),r0` | `r0` = the table entry: the function's address |
| `0x80020076` | `jmp @r0` | jump to the function |
| `0x80020078` | `nop` | the delay slot |

So the caller puts the syscall number in `r0` and jumps to `0x80020070`. The arguments are
already in place by then, as for any C function: the first four in `r4`–`r7`, the rest on the
stack, and the result comes back in `r0` (the SH calling convention that gcc uses).

The routine uses `jmp`, not `jsr`, so it leaves PR alone. The OS function's final `rts`
therefore returns straight to whoever called the syscall, as if the add-in had called the
function directly. There is no exception, no mode switch and no copy of the arguments: the OS
function runs on the add-in's stack, in the add-in's context. `trapa` is not involved (the OS
3.60 never reads TRA, so it doesn't use `trapa` at all; see [Exceptions](../cpu.md#exceptions-and-their-vectors)).

The routine does **not check the number**. A number past the end of the table reads whatever
follows it in flash and jumps there.

!!! info "Verified in the emulator"
    The routine and the table are read from the 3.60 image by `re/syscall360.py` and
    `re/syscalls_wiki.py`. The emulator calls syscalls the same way to install add-ins
    (`TestInstallAddin`, below), and the add-ins it runs reach the OS only this way.

### What an add-in's code looks like

An add-in built with the usual tools (PrizmSDK, fxSDK, gint) has one small stub per syscall
it uses, each four instructions:

1. `mov.l` the address `0x80020070` into `r2`;
2. `mov.l` the syscall number into `r0`;
3. `jmp @r2`;
4. `nop`.

The stub's caller used `jsr` to reach the stub, so PR holds the caller's return address, and
the OS function returns there. The start-up code of the same add-ins also calls the routine
directly with `jsr @r2`, setting `r0` in the delay slot. All the `.g3a` files in this
repository (`aluprobe`, `KEYPROBE`, DASM) do it these two ways.

No word in the OS image itself holds `0x80020070`: the OS calls its own functions directly
and never goes through the table.

## The table

In OS 3.60 the table is at **`0x80687CD0`** and has **8,040 entries**, numbers `0x0000` to
`0x1F67`. Each entry is a 4-byte address in the OS code. The word after the last entry
(`0x8068FA70`) is not a code address. The 8,040 entries hold 8,031 different addresses: a few
numbers share one function.

To find where a syscall leads in 3.60, read the entry: address = the word at
`0x80687CD0` + number × 4. The script does this:

```
python re/syscall360.py 0x1839            # one number
python re/syscall360.py 0x1DA3 0x1DB4 --range
python re/syscall360.py --rev 0x802AEA24  # which numbers lead to (or near) an address
```

It takes the table address from the routine at `0x80020070`, so it does not depend on a
fixed table address.

## OS versions

The numbers are the interface; the addresses are not. The OS 3.80 table is at `0x806A2014`
(its routine at `0x80020070` is the same five instructions) and also has 8,040 entries. For
every number, the first 32 bytes of the function's code were compared between 3.60 and 3.80:

- they match for 7,415 of the 8,040 numbers, and the first 8 bytes for 7,844;
- they match for every syscall named on this page;
- but most functions have moved: `PowerOff` is at `0x802AEA24` in 3.60 and `0x802C0AAC` in 3.80.

So an add-in that calls by number works on both versions, while an address taken from one
version is wrong in the other. Most of the mismatches are probably functions whose code
changed slightly or whose PC-relative constants moved, not renumbered syscalls, but that has not
been checked one by one.

!!! warning "Unconfirmed"
    Whether any number means a **different** function in 3.80 than in 3.60. The comparison
    only shows that at least 7,415 numbers start with the same code; the rest were not
    examined.

## Syscalls on this site

These are the syscalls the other pages mention most, with their 3.60 addresses (read from the
table with `re/syscalls_wiki.py`):

| Number | Name | 3.60 address | Where it is described |
|---|---|---|---|
| `0x0017` | Rebuild the list of add-ins | `0x8002D1A4` | [MMU and add-in launch](mmu-and-addins.md); the emulator calls it after installing a file |
| `0x01E6` | `GetVRAMAddress` | `0x8004F6A2` | [Display: VRAM](../peripherals/display.md#vram) |
| `0x025F` | `Bdisp_PutDisp_DD` | `0x8005552C` | [Display: pushing a frame](../peripherals/display.md#pushing-a-frame) |
| `0x0260` | `Bdisp_PutDisp_DD_stripe` | `0x80055532` | [Display: pushing a frame](../peripherals/display.md#pushing-a-frame) |
| `0x02C1` | `RTC_GetTicks` | `0x80057DBC` | [Timers: ticks](../peripherals/timers.md#ticks) |
| `0x0EAB` | `GetKey` | `0x801951A6` | [Boot: waiting for a key](boot.md#waiting-for-a-key) |
| `0x1187` | Restart the OS through the reset vector | `0x801DE698` | [Boot: restarts](boot.md#restarts) |
| `0x11ED` | A low-power routine | `0x801DFE90` | [Boot: restarts](boot.md#restarts) |
| `0x127E` | Is one given key held? | `0x801E3BA8` | [Keyboard: checking one key](../peripherals/keyboard.md#checking-one-key-without-the-unit) |
| `0x128F` | `OpCodeToStr` (a BASIC token's text) | `0x801E4630` | — |
| `0x1839` | `PowerOff` | `0x802AEA24` | [Boot: power-off](boot.md#power-off) |
| `0x1DA3` | `Bfile_OpenFile_OS` | `0x803571F4` | — |
| `0x1DA4` | `Bfile_CloseFile_OS` | `0x80357372` | — |
| `0x1DAE` | `Bfile_CreateEntry_OS` | `0x80357814` | — |
| `0x1DAF` | `Bfile_WriteFile_OS` | `0x803578C2` | — |
| `0x1DB4` | `Bfile_DeleteEntry` | `0x80357B36` | — |
| `0x1F42` | `sys_free` | `0x80384718` | — |
| `0x1F44` | `sys_malloc` | `0x8038494C` | — |

The names come from WikiPrizm and libfxcg, except where the table describes what the
function does instead.

## In the emulator

- The emulator does nothing special for syscalls: an add-in's stub jumps to `0x80020070` in
  the flash image and the CPU runs the OS's own code, like the calculator.
- The emulator can also **call a syscall itself**, from Go, to use the OS's own code for a
  job. [`emu_go/oscall.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/oscall.go):
    - `syscallAddr(n)` reads the table address from the routine at `0x80020070` and returns
      entry `n`, so it works with any OS version.
    - `e.syscall(n, budget, args...)` calls it with up to four arguments in `r4`–`r7` and
      returns `r0`. It only runs while the OS is idle (asleep in its key loop, no add-in
      running). It saves every register, sets the stack 256 bytes below the OS's
      (`(r15 - 0x100) & ~3`), runs the function with interrupts and devices live, catches its
      return, then puts everything back so the OS goes on sleeping where it was. A caller with
      a fifth or sixth argument writes it at that stack address first.
    - Installing an add-in uses it
      ([`install.go`](https://github.com/hexbinoct/casio-cg50/blob/main/emu_go/install.go)):
      `sys_malloc`, the `Bfile_*_OS` calls to create and write the file in the storage
      memory, `sys_free`, then `0x0017` to rebuild the add-in list. The OS's own file system
      code writes the file, so the storage memory stays as consistent as on the calculator.
- Tests: `TestInstallAddin` and its neighbours in `emu_go/install_test.go` install a `.g3a`
  through these syscalls. The probe tests in `codeview_probe_test.go` and
  `basic_bench_probe_test.go` (`-tags probe`) call `OpCodeToStr` and the main-memory
  syscalls this way.
- Scripts: `re/syscall360.py` resolves numbers in 3.60 (`re/syscall.py` in 3.80);
  `re/syscalls_wiki.py` checks the facts on this page.
