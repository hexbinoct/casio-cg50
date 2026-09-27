# Running the emulator on Android

The Go core (`emu_go/`) sits behind a small host-facing facade (`emu_go/emulator.go`), so the
Android app ([`android/`](../android/README.md)) is a thin shell: a `SurfaceView` for the
384×216 screen, a keypad view that turns touches into matrix key press/release, and a JNI shim.
No firmware ships in this repo — the app loads the user's own flash dump (and ideally a
provisioned save-state) at runtime.

## Architecture

```
 Kotlin: MainActivity, CalcSurfaceView (run loop + blit), KeypadView/KeyMap (keys)
        │  JNI — NativeBridge external funs
        ▼
 libcg50.so       android/app/src/main/cpp/native-lib.cpp — JNI shim, forwards to Emu*
        │  links
        ▼
 libcg50core.so   emu_go/android_bridge.go (cgo, -buildmode=c-shared)
        │
   Emulator facade (emu_go/emulator.go) — Step / keys / framebuffer / snapshot, thread-safe
        │
   CPU + MMU + Memory + SH7305 peripherals — the validated core
```

The same facade drives the desktop web UI (`go -C emu_go run . 0 30000 web`) and the
`rtbench` real-time loop, so both hosts exercise identical code.

## The host API (C ABI)

`emu_go/android_bridge.go` (build tag `android`) exports these via cgo `//export`:

| Symbol | Purpose |
|--------|---------|
| `EmuInit(uint8* flash, int n)` | create the machine from a flash-dump image |
| `EmuResume(uint8* blob, int n) -> int` | restore a save-state (e.g. provisioned to the MAIN MENU); 0 = ok |
| `EmuStep(int n)` | advance n instructions (the app calls it once per frame from its render thread) |
| `EmuKeyDown(int row, int col)` / `EmuKeyUp(int row, int col)` | press / release a key in the emulated key-scan matrix (0-based, see `re/KEYMAP.md`); holding a key auto-repeats with the OS's own timing, and a very short tap is held long enough to register |
| `EmuInjectKey(int row, int col)` | a complete tap (press, hold a few scans, release) |
| `EmuReleaseAllKeys()` | release everything (call on pause / focus loss) |
| `EmuSetInstrPerSec(long long ips)` | the host's measured throughput; scales the RTC, the 32.768 kHz counter and the key-scan rate to real time |
| `EmuSetClock(long long unixSec)` | set the calendar clock |
| `EmuSetKeyScanPeriod(long long instr)` | override the key-scan period (normally derived from `EmuSetInstrPerSec`) |
| `EmuFramebufferRGBA(uint8* dst, int cap) -> int` | fill `Width*Height*4` RGBA bytes of the **displayed** frame (the LCD panel, not live VRAM) |
| `EmuSnapshot(int* outLen) -> uint8*` | malloc a gzip save-state blob (free with `EmuFree`) |
| `EmuFree(uint8*)` | free an `EmuSnapshot` pointer |
| `EmuWidth() / EmuHeight()` | framebuffer dimensions (384 × 216) |

If the core ever hits something it cannot execute it halts (the reason goes to logcat) instead
of crashing the process; `EmuResume` clears the halt.

## Building

`android/build_go_lib.ps1` cross-compiles the core per ABI with the NDK's clang (arm64-v8a and
x86_64) into `android/app/src/main/jniLibs/<abi>/libcg50core.so`, setting the SONAME so the JNI
shim's dependency resolves on-device. Then build the app with Gradle / Android Studio. Details
and the file-push steps are in [`android/README.md`](../android/README.md).

## Provisioning (skip first-boot setup)

A fresh boot lands in the language/setup wizard. Provision once and push the resulting
save-state so the app resumes instantly at the MAIN MENU:

```sh
go -C emu_go run . 450000000 30000 provision   # writes os/flash_dump/cg50_state.bin
```

The app does `EmuInit(flash)` then `EmuResume(stateBlob)`, and re-snapshots (`EmuSnapshot`) on
pause so the user's work and settings persist — this models the real calculator's
backup-battery RAM. (Flash-only persistence is insufficient: the fls0 mount is coupled to that
RAM; see RECON_NOTES cont.18f.) Save-states include the peripheral registers, the LCD
controller registers and the MMU's UTLB, so a snapshot taken inside an add-in resumes there.

## Status

- ✅ Runs on a real phone (POCO X3, ~22 M instr/s): all built-in apps, add-ins, keyboard with
  auto-repeat, blinking cursor, calculator-style keypad, save-state on pause.
- ⏳ Interpreter speed-ups (the idle OS already sleeps, so battery use is low when waiting for
  a key), annunciator state on the SHIFT/ALPHA keys, release signing.
