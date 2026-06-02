# Dumping the fx-CG50 flash (and RAM) — guide

Goal: get the **real** flash image off your physical fx-CG50 — not just the OS
(which we already have from the updater), but the whole flash including the
**storage / `fls0` filesystem region** and system area the boot code mounts. This
unblocks the emulator boot and gives us an authoritative validation oracle.

We do this by running a tiny program ON the calculator that reads memory-mapped
flash/RAM and streams it to your PC over USB. The USB "mass storage" mode only
shows the *formatted file view* of fls0 — we need the **raw** bytes, so we need
a small add-in.

> Safety: this is **read-only** (no flash erase/write). Low risk. Use fresh
> batteries (a power loss mid-OS-update is what bricks these; a read won't).
> Don't run the official OS updater. Keep the original updater image as backup.

---

## Method A — gint add-in + fxlink  (recommended; gives raw flash)

### 1. Install the fxSDK + gint toolchain
gint (Lephenixnoir) is the open bare-metal SDK for these calcs; `fxlink` is its
USB host tool. Easiest on Linux (native) or Windows via WSL/MSYS2; macOS works too.

The supported installer is **GiteaPC** (pulls fxSDK, the SuperH GCC, gint, fxlink):
```
# Linux / WSL — see https://www.planet-casio.com/Fr/forums/  &  gitea.planet-casio.com
git clone https://gitea.planet-casio.com/Lephenixnoir/GiteaPC
cd GiteaPC && ./giteapc.py install Lephenixnoir/fxsdk
./giteapc.py install Lephenixnoir/gint
```
Verify: `fxsdk --version` and `fxlink --version` work.

### 2. Build the dumper add-in
`dump.c` (in this folder) is a gint add-in that streams memory regions over USB.
Scaffold a gint project and drop it in:
```
fxsdk new flashdump          # creates a CMake gint add-in project
cd flashdump
cp ../dump.c src/main.c      # replace the generated main with our dumper
fxsdk build-cg               # -> flashdump.g3a
```
(See `dump.c` header for the exact `usb.h` API names — they vary slightly between
gint versions; adjust if the build complains. The dump logic stays the same.)

### 3. Put the .g3a on the calculator
- Connect the calc by USB, choose **"USB Flash"** (mass-storage) on the calc.
- Copy `flashdump.g3a` to the storage drive that appears.
- Safely eject; the add-in now appears in the calc's **Menu**.

### 4. Run it and capture on the PC
- On the PC, start the receiver and **wait**:
  ```
  fxlink -iw -o dump_       # interactive, wait; writes incoming transfers to dump_*.bin
  ```
  (exact flags: `fxlink --help`; you want "receive/log incoming bulk transfers").
- On the calc, **reconnect USB and pick the plain USB/“fxlink” connection** (not
  mass storage), then launch the **flashdump** add-in. It streams each region as a
  separate transfer; fxlink saves them to files.

### 5. Send me the files
You'll get one file per region (named in `dump.c`). The important ones:
`os.bin`, `flash_full.bin` (or `storage.bin`), `dram.bin`, `ilram.bin`.

---

## What regions to dump (and why)

| Name | Virtual addr | Size (start with) | Why |
|------|--------------|-------------------|-----|
| **flash_full** | `0x80000000` | full flash (16–32 MB) | OS **+ fls0 storage + system area** = the missing piece. THE key dump. |
| os | `0x80000000` | `0x00C00000` (12 MB) | cross-check vs our unpacked image (must match!) |
| dram | `0x8C000000` | `0x00800000` (8 MB) | live RAM state = boot/runtime oracle |
| ilram | `0xFD800000` | `0x00010000` (64 KB) | on-chip RAM (IRQ tables @0xFD80xxxx, kernel structs) |

Notes:
- **Flash size**: fx-CG50 flash is 16–32 MB. Dumping past the real end causes a bus
  error (add-in crash) — not harmful, but you lose the transfer. `dump.c` walks
  conservatively; start at 16 MB (`0x01000000`) and bump up if it completes cleanly.
  gint's MPU/linker definitions document the exact `ROM` extent for SH7305 — check
  `<gint/mpu/...>` / the `fxcg50.ld` in your gint install for the precise size.
- The **uncached** mirror `0xA0000000` reads the same flash; use it if cached reads
  look odd. Dump RAM **after** the OS has booted normally for the best oracle.

---

## Method B — Cahute (USB protocol, no add-in)  [investigate]

**Cahute** (Thomas Touhey; successor to libcasio/p7) speaks Casio's USB protocols
and may be able to back up parts of CG flash without writing an add-in:
```
# https://cahuteproject.org/
p7 ...    /  cahute backup ...
```
Worth a look — if it can pull the OS/flash directly it's the least-effort route.
Coverage of the CG50's raw OS flash is uncertain, so treat as a bonus, not primary.

## Method C — existing community dumpers  [search first]

Before building anything, check **Planet Casio** and **Cemetech** for an existing
"memory dump" / "ROM dump" add-in for the fx-CG50 (e.g. Lephenixnoir's test tools,
or `gintctl`). If one exists and streams raw flash, use it and skip Method A's code.

---

## After you have the dumps
Drop the files in `os/flash_dump/`. We'll:
1. Diff `os.bin` against our unpacked `cg50_os_3.80.plain.bin` to confirm the dump
   and find the OS's real flash offset.
2. Locate the `fls0` / system region and map it into the emulator (`emu/memory.py`),
   so the flash driver mounts a real, formatted filesystem and boot proceeds.
3. Use `dram.bin` / `ilram.bin` as a golden state to diff our emulator against.
