# fx-CG50 emulator — Android app

A thin Android shell over the Go emulator core (`emu_go/`). Kotlin draws the 384×216 screen
(`CalcSurfaceView`) and a calculator keypad (`KeypadView`), and talks to the core through a
JNI shim (`app/src/main/cpp/native-lib.cpp`, built into `libcg50.so`) that forwards to the Go
core's C ABI (`libcg50core.so`, see [`docs/ANDROID.md`](../docs/ANDROID.md)).

```
MainActivity / CalcSurfaceView / KeypadView + KeyMap   (Kotlin)
        │ JNI  (NativeBridge external funs)
        ▼
 libcg50.so      native-lib.cpp  — JNI shim
        │ links
        ▼
 libcg50core.so  emu_go/android_bridge.go (cgo c-shared)  — the emulator
```

No Casio firmware ships here. You supply your own dump at runtime (see below).

## Build & run

1. **Cross-compile the Go core** (needs Go + the NDK; edit the NDK path in the script if your
   version differs). From the repo root:
   ```powershell
   pwsh -File android/build_go_lib.ps1
   ```
   This writes `app/src/main/jniLibs/{arm64-v8a,x86_64}/libcg50core.so`. Re-run it whenever
   `emu_go/` changes. (The `.so`s are git-ignored — they're build output.)

2. **Build the app**, either in Android Studio (open `android/`, run `app`) or from the
   command line:
   ```powershell
   android/gradlew.bat -p android :app:assembleDebug
   adb install -r android/app/build/outputs/apk/debug/app-debug.apk
   ```
   CMake links the JNI shim against the prebuilt `libcg50core.so` for the target ABI.

3. **Provide your files** (the app reads them from its external files dir):
   ```sh
   adb push flash_full.bin /sdcard/Android/data/com.hexbinoct.cg50/files/
   adb push cg50_state.bin /sdcard/Android/data/com.hexbinoct.cg50/files/   # recommended
   ```
   - `flash_full.bin` — your own 16 MB flash dump (required).
   - `cg50_state.bin` — a save-state provisioned to the MAIN MENU, so the app resumes there
     instantly instead of cold-booting into first-boot setup. Generate it on the desktop:
     ```sh
     go -C emu_go run . 450000000 30000 provision   # writes os/flash_dump/cg50_state.bin
     ```
     then push that file. (Without it the app cold-boots; on ARM that takes a while and lands
     in the language wizard.) The app creates the files dir on first launch — start it once
     if `adb push` says the directory doesn't exist.

The app snapshots back to `cg50_state.bin` on pause, so your session persists across launches.

## The app

- **Screen:** the LCD panel's contents (the core models the display controller), kept at the
  panel's 384:216 aspect inside a bezel.
- **Keypad** (`KeypadView.kt`, data in `KeyMap.kt`): the fx-CG50's physical layout — F1–F6;
  SHIFT OPTN VARS MENU and ALPHA x² ^ EXIT beside a round D-pad; the two function rows; the
  number pad. Yellow SHIFT and red ALPHA legends sit above each key as on the faceplate.
  Touch down/up are real key press/release, so holding a key auto-repeats with the OS's own
  timing; multi-touch works; each press gives a haptic tap. SHIFT/ALPHA are real keys: tap
  one, then the target. `python re/audit_keymap.py` checks the labels against the codes the
  OS produces for each matrix position.
- **Time base / speed setting:** an emulated second is always `CalcSurfaceView.ips`
  instructions; the core derives the RTC, the 32.768 kHz counter and the 33 Hz key scan from
  it (`setInstrPerSec`), and `setClock` sets the calendar from the phone's clock. **Long-press
  the screen** for the settings dialog (no button, no screen space): *Original hardware* =
  45 M instr/s, which reproduces the real fx-CG50 (its effective throughput on OS code — NOR
  flash wait states, SDRAM VRAM writes — is about that, measured by a held key: 11-12 menu
  moves in 1.2 s), or *Fast* = the bare 118 MHz clock, ~2×. Persisted in SharedPreferences;
  `am start … --es speed fast|original` sets it from adb.
- **Two threads** (`CalcSurfaceView`): `cg50-emu` runs the core in 5 ms chunks (MAX_IPS/200
  instructions) paced against the wall clock — it steps whenever emulated time is behind real
  time and sleeps only when ahead, so an idle machine (sleeping CPU, fast-forwarded in the
  core) costs almost nothing and a busy one saturates the thread. That saturation is what gets
  it onto a phone's big cores: the scheduler places threads by running utilisation, and the
  old step-then-blit-then-sleep loop looked like a 40 % task that fit a little core. When the
  phone can't keep up the debt is dropped past 20 ms, so the whole machine slows uniformly
  rather than its timers drifting against the CPU. `cg50-render` blits at ~60 fps, skipping
  frames when the panel hasn't changed for 300 ms (`EmuFrameGen`). An ADPF hint session is
  used where the device supports it (`am start ... --ez adpf false` disables it). The
  `cg50-perf` logcat lines show emulated vs executed instr/s, thread busy %, debt drops,
  fps, frames drawn and LCD pushes; `python re/phone.py` builds, installs and measures
  (`menuhold` = held RIGHT on the MAIN MENU, counting pushes = menu moves, with CPU
  frequency / core sampling).
- **Native blitter:** the OS draws bitmaps (menu icons, labels) through one generic per-pixel
  routine (~85 interpreted instructions per pixel, ~75 % of a menu move). The core replaces
  it with a native implementation when the app runs (`emu_go/hle.go`, `EmuSetHLE`), charging
  the same emulated cycles the real code would take so timing is unchanged. It only handles
  the descriptor modes the OS was seen to use and falls back to the real code otherwise;
  `TestHLEBlitMatchesInterpreter` proves it byte-identical against the interpreter over a
  session of menu moves, app screens and text.
- **Add-ins** (`.g3a` in your flash dump) run like on the calculator — select their icon on
  the MAIN MENU.

## Troubleshooting

- **Keys do nothing after the app sat idle:** the calculator auto-powered-off, like the real
  one does after ~10 minutes — press **AC/ON**. (Only part of the display-off is rendered yet.)
- **Wireless adb drops** when the phone's screen sleeps: wake it, then
  `adb kill-server`, `adb mdns services`, and `adb connect <the listed _adb-tls-connect name>`.
- **A resumed session shows garbage or ignores keys:** the saved `cg50_state.bin` was written
  while the machine was in a bad state (e.g. an add-in run on a build without the MMU).
  Force-stop the app, push a clean provisioned `cg50_state.bin`, and start it again.
- **The core hit something it can't execute:** it halts rather than closing the app; the
  reason is logged to logcat under the core's tag (`adb logcat -s cg50 cg50-key`).
- ABIs are limited to `arm64-v8a` (phones) and `x86_64` (emulators) in
  `app/build.gradle.kts` — the two the Go script builds. Add `armeabi-v7a` to both if needed.

## Not done yet

- Annunciator state on the SHIFT/ALPHA keys (the OS shows it in its status bar already).
- A native version of the 1-bpp text renderer (0x8017b75e, ~20 % of a menu move).
- Release signing / a store build.
