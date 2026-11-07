# Making a flash dump of your fx-CG50

The emulator (CG50 Studio on the desktop, the Android app) runs the calculator's real operating
system. That OS belongs to Casio, so the emulator doesn't include it: you give it a **flash
dump**, a copy of your own calculator's flash memory chip. This page shows how to make one with
**FlashDump**, a small add-in in this folder.

A flash dump is **not** the OS update file Casio publishes (the ~70 MB updater for OS 3.80 and
so on). That is an installer for a PC; the emulator needs the chip's contents, which only your
calculator has.

FlashDump only reads: it never writes to the flash, and it can't harm the calculator.

## 0. Check your OS version first

On the calculator: **MENU → System → F4 (Version)**. The first line is the OS, e.g. `03.60.0202`.

The emulator is made and tested for **OS 3.60**. With another version the dump still works:
in our tests a 3.80 OS boots, runs the built-in apps and add-ins, and installs add-ins, but
nobody has tried a real 3.80 calculator's dump yet, and the apps will tell you so when you
open it. FlashDump shows your version too, before it copies anything.

## What you need

- the calculator and its USB cable
- **FlashDump**: a ready-made `FLASHDMP.g3a` is coming to the project's
  [Releases page](https://github.com/hexbinoct/casio-cg50/releases) once it has been tested
  on a calculator; until then, build it as shown in [For developers](#for-developers)
- **fxlink** on the computer, the program that receives the dump. It comes with the
  [fxSDK](https://gitea.planet-casio.com/Lephenixnoir/fxsdk) and runs on **Linux** and
  **macOS**; on Windows, use it inside WSL with the calculator passed through by
  [usbipd-win](https://github.com/dorssel/usbipd-win) (we haven't tried that route ourselves).
  The fxSDK's installer, GiteaPC:

  ```sh
  git clone https://gitea.planet-casio.com/Lephenixnoir/GiteaPC
  cd GiteaPC && python3 giteapc.py install Lephenixnoir/fxsdk
  fxlink --help        # check that it works
  ```

## 1. Put FlashDump on the calculator

1. Connect the calculator to the computer. On the calculator's connection screen choose
   **USB Flash** (F1): it appears on the computer like a USB stick.
2. Copy `FLASHDMP.g3a` onto it (the top folder is fine).
3. Eject it on the computer, then unplug. **FlashDump** is now in the MAIN MENU.

## 2. Make the dump

1. On the computer, open a terminal in the folder where you want the file, and run

   ```sh
   fxlink -i
   ```

   It waits for the calculator.
2. On the calculator, open **FlashDump**. It shows your OS version, then the choices:

   | Key | Dump | Size | |
   |---|---|---|---|
   | **F1** | the whole flash | 32 MB | **recommended**: the OS and the storage memory, so your files and add-ins come along |
   | F2 | the first half | 16 MB | boots and runs, but leaves most of the storage memory out: your installed add-ins and most files are missing (you can still install add-ins in the emulator) |
   | F6 | flash and RAM | 32 MB + 8 MB + 64 KB | for people working on the emulator itself |

3. Press **F1**, then connect the USB cable. The calculator shows the progress; the whole
   32 MB takes about **3 minutes**. Keep the cable in until it says **Done**.
4. fxlink saves the dump as `fxlink-flashdump-flash32-<date>.bin`. Check the size: exactly
   **33,554,432 bytes** (32 MB), or 16,777,216 for the first half. Rename it if you like.

## 3. Open it in the emulator

- **CG50 Studio**: *Open flash dump…*, or drop the file on the window.
- **Android**: copy the file to the phone, start the app, *Choose flash dump…*.

With the whole 32 MB the calculator starts at its own MAIN MENU, as you left it. A 16 MB dump
starts with the first-boot setup (language, display, power, battery: press F6 / EXE through it).

## Keep the dump to yourself

The dump is a copy of everything on the calculator. It holds Casio's OS, which isn't yours to
share. It also holds your own things: the storage memory (files, pictures, add-ins) and the
main memory (programs, variables, lists, Run-Matrix history, settings). Don't post it or send
it to anyone.

If you'd rather not have your own files in the emulator, reset them on the calculator first
(**System → F5 Reset**; back them up over USB before you do), then make the dump. Don't use
the 16 MB dump for this: its first half still holds part of the storage memory.

## Troubleshooting

- **fxlink doesn't see the calculator.** Start fxlink first, then open FlashDump and press F1,
  then connect the cable. On Linux, fxlink may need permission to use the USB device (run it
  with `sudo`, or add a udev rule for the calculator, Casio vendor ID `07cf`).
- **The file is smaller than 32 MB.** The transfer was cut off (cable, sleep). Run it again.
- **The app says the OS version isn't 3.60.** That's the warning from step 0: the dump still
  opens and runs.

## For developers

FlashDump is a [gint](https://gitea.planet-casio.com/Lephenixnoir/gint) add-in (`src/main.c`)
that streams memory-mapped regions to fxlink over USB. Build it with the fxSDK, here in the
project's Docker image ([`tools/fxsdk-docker`](../fxsdk-docker/Dockerfile)):

```sh
docker build -t fxsdk:latest tools/fxsdk-docker            # once, ~30 min
docker run --rm -v "$PWD/tools/flash_dump:/work" fxsdk:latest fxsdk build-cg   # -> FLASHDMP.g3a
python tools/flash_dump/make_icons.py                       # redraws assets-cg/ (optional)
```

What it sends:

| Name | Address | Size | |
|---|---|---|---|
| `flash32` | `0x80000000` | 32 MB | the whole flash: boot code, OS, system area and main memory, the storage memory (fls0, from physical `0x00C80000`) |
| `flash16` | `0x80000000` | 16 MB | its first half |
| `dram` | `0x8C000000` | 8 MB | RAM, as it is while FlashDump runs (F6) |
| `ilram` | `0xFD800000` | 64 KB | the on-chip IL memory (F6) |

The OS version comes from the OS's header in flash: `CASIOWIN` at `0x80020000`, the version
string at `0x80020020`, the place the emulator reads it too (`emu_go/osversion.go`).

Reading past the end of the 32 MB flash raises a bus error. The fx-CG50's flash is 32 MB
(`0x02000000`), checked with a full dump: the upper 16 MB is real storage memory, not a mirror.
