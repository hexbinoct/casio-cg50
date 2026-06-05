# fx-CG50 emulator — Android app

A thin Android shell over the Go emulator core (`emu_go/`). Kotlin UI (a `SurfaceView` for
the 384×216 screen + an on-screen keypad) calls a JNI shim (`app/src/main/cpp/native-lib.cpp`,
built into `libcg50.so`) which forwards to the Go core's C ABI (`libcg50core.so`).

```
MainActivity / CalcSurfaceView / KeyMap  (Kotlin)
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

2. **Open `android/` in Android Studio** and let Gradle sync. Build/run the app (`app`).
   CMake links the JNI shim against the prebuilt `libcg50core.so` for the target ABI.

3. **Provide your files** (the app reads them from its external files dir):
   ```sh
   adb push flash_full.bin /sdcard/Android/data/com.hexbinoct.cg50/files/
   adb push cg50_state.bin /sdcard/Android/data/com.hexbinoct.cg50/files/   # optional, recommended
   ```
   - `flash_full.bin` — your own 16 MB flash dump (required).
   - `cg50_state.bin` — a save-state provisioned to the MAIN MENU, so the app resumes there
     instantly instead of cold-booting into first-boot setup. Generate it on the desktop:
     ```sh
     go -C emu_go run . 450000000 30000 provision   # writes os/flash_dump/cg50_state.bin
     ```
     then push that file. (Without it the app cold-boots; on ARM that takes a while and lands
     in the language wizard.)

The app snapshots back to `cg50_state.bin` on pause, so your session persists across launches.

## Notes / tuning

- ABIs are limited to `arm64-v8a` (phones) and `x86_64` (Studio emulator) in
  `app/build.gradle.kts` — the two the Go script builds. Add `armeabi-v7a` to both if needed.
- `CalcSurfaceView.instrPerFrame` (default 333 333 ≈ 20 M instr/s at 60 fps) is the pacing
  knob; measure on your device and adjust.
- Keypad layout + matrix coordinates live in `KeyMap.kt` (sourced from `re/KEYMAP.md`).
- This is Phase 1: screen + keypad + resume/snapshot. SHIFT/ALPHA work as real keys (tap then
  the target). Annunciators, long-press repeat, and a polished layout are follow-ups.
