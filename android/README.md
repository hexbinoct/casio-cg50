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
 libcg50core.so  emu_go/android/bridge.go (cgo c-shared)  — the emulator
```

No Casio firmware ships here, and none is in the APK. You supply a flash dump of your own
calculator at runtime: on the first start the app asks for it (see "The flash dump" below).

## Build & run

1. **Cross-compile the Go core** (needs Go + the NDK). From the repo root, on the Mac or the
   PC (`build_go_lib.py` finds the newest NDK under the SDK; `build_go_lib.ps1` is the older
   Windows-only script with a fixed NDK path):
   ```sh
   python3 android/build_go_lib.py
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

   On the Mac: `android/local.properties` with `sdk.dir=/Users/<you>/Library/Android/sdk`
   (git-ignored) and Android Studio's JDK (`JAVA_HOME=/Applications/Android Studio.app/
   Contents/jbr/Contents/Home`); `python3 re/phone.py build` does both steps and installs.

3. **Provide your dump.** Either start the app and choose the file (below), or push it from the
   PC into the app's external files dir. Preferred: the whole **32 MB** flash, which holds all
   of the storage memory, so add-ins can be installed and read their files
   (`python3 re/phone.py firmware` pushes both):
   ```sh
   adb push flash_full_32mb.bin /sdcard/Android/data/com.hexbinoct.cg50/files/
   adb push cg50_state_32mb.bin /sdcard/Android/data/com.hexbinoct.cg50/files/  # recommended
   ```
   Without the 32 MB dump the app uses the older 16 MB pair below (most of the storage memory
   is missing from it, so installs into it are not supported):
   ```sh
   adb push flash_full.bin /sdcard/Android/data/com.hexbinoct.cg50/files/
   adb push cg50_state.bin /sdcard/Android/data/com.hexbinoct.cg50/files/   # recommended
   ```
   - `flash_full.bin` — your own 16 MB flash dump.
   - `cg50_state.bin` — a save-state provisioned to the MAIN MENU, so the app resumes there
     instantly instead of cold-booting into first-boot setup. Generate it on the desktop:
     ```sh
     go -C emu_go run ./cmd/cg50emu 450000000 30000 provision   # writes os/flash_dump/cg50_state.bin
     ```
     then push that file. (Without it the app cold-boots; on ARM that takes a while and lands
     in the language wizard.) The app creates the files dir on first launch — start it once
     if `adb push` says the directory doesn't exist.

The app snapshots the session on pause (to `sessions/<hash>.state`, below), so it persists
across launches. A pushed `cg50_state_32mb.bin` / `cg50_state.bin` is taken over as the
session of the pushed dump next to it on the next start (the file is moved into `sessions/`),
so pushing a fresh provisioned state still resets the session.

4. **Release build** (signed): put a git-ignored `android/keystore.properties` next to
   `settings.gradle.kts` naming a keystore that lives outside the repo:
   ```properties
   storeFile=/path/outside/the/repo/cg50-emulator-release.jks
   storePassword=...
   keyAlias=cg50
   keyPassword=...
   ```
   then `sh ./gradlew :app:assembleRelease` in `android/` (the first release build needs the
   network for the lint jars; later ones work with `--offline`) →
   `app/build/outputs/apk/release/app-release.apk`. Without the properties file the release
   APK is built unsigned. Never commit the keystore or the properties file (`.gitignore` has
   both). Check that no firmware went in: `unzip -l app-release.apk` lists only code, the two
   `.so` pairs and resources (no `assets/` other than the baseline profile, no `res/raw`).

## The flash dump

- **First start:** without a dump the app shows a screen that explains it needs a flash dump of
  the user's own fx-CG50 (it is not included and cannot be downloaded) and a **Choose flash
  dump…** button, which opens Android's file picker (no storage permission needed).
- **The check** is the desktop app's (`app/store.go` `readDump`), so both accept the same
  files: exactly 16 MB or 32 MB, and "CASIO" within the first 4 KB (the boot code's header).
  A wrong file gets a message saying what is wrong (its size, or the missing header) and the
  picker can be opened again. The size is checked before reading when the provider reports it.
- **Accepted** files are copied to `flash_dump.bin` in the external files dir (the original is
  left alone), then the emulator boots: it resumes that dump's session if there is one, else it
  cold-boots (a 32 MB dump boots straight to the MAIN MENU, a 16 MB one shows first-boot setup).
- **Change flash dump…** (long-press the screen → settings) shows the dump in use and opens the
  picker; the running session is saved first, then the app restarts on the new dump.
- **Which dump runs:** the one chosen in the app; if none was, a pushed `flash_full_32mb.bin`,
  else a pushed `flash_full.bin`. A pushed file is checked the same way.
- **Sessions are per dump:** `sessions/<hash>.state`, where `<hash>` is the first 16 hex digits
  of the dump's SHA-256 (as the desktop app's `sessions/<hash>.state`), so a state is never
  resumed on another dump, and switching back to a dump brings its session back.
- The dumps are excluded from Android's cloud backup and device transfer
  (`res/xml/backup_rules.xml`, `data_extraction_rules.xml`); sessions and settings are not.

Files in `/sdcard/Android/data/com.hexbinoct.cg50/files/`:

| file | what |
|---|---|
| `flash_dump.bin` | the dump chosen in the app |
| `flash_full_32mb.bin`, `flash_full.bin` | dumps pushed with adb (used when none was chosen) |
| `cg50_state_32mb.bin`, `cg50_state.bin` | pushed states, moved into `sessions/` on the next start |
| `sessions/<hash>.state` | each dump's session |

## The app

- **Screen:** the whole LCD panel (the core models the display controller): 396x224, the OS's
  384x216 area plus the frame the OS paints around it, which gint add-ins use as part of their
  screen (status bars, function-key labels). Kept at 396:224 inside a bezel.
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
  the screen** for the settings dialog (no button, no screen space), section *Speed*: *Original hardware* =
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
- **Installing add-ins** (32 MB dump): the emulated OS writes the `.g3a` into its storage
  memory with its own file system calls, as when you copy one to the calculator over USB, and
  the MAIN MENU comes back with the new icon (last place; LEFT from the first icon wraps to
  it). A file of the same name is replaced. Three ways in:
  - **Long-press the screen → Install add-in…** and pick the file (Android's file picker: no
    storage permission needed).
  - **Open with / Share** a `.g3a` from a file manager, Drive or a chat app.
  - From the PC: `python3 re/phone.py install path/to/X.g3a` (pushes it to the files dir and
    starts the app with `--es install X.g3a`; prints the result from logcat).
  It takes a few seconds (the screen pauses meanwhile) and the session is saved right after.
  At the MAIN MENU the app briefly opens Run-Matrix and returns, which is how the menu rebuilds
  its icon list; an add-in suspended under the menu is closed that way too. While an add-in is
  running the install is refused: press MENU first. Core: `emu_go/install.go`.

## Settings and the app icon

**Long-press the calculator screen** for the settings dialog, in sections: *Speed* (below),
*Flash dump* (the dump in use, **Change flash dump…**), *Add-ins* (**Install add-in…**) and
*App icon*.

**App icon…** offers three launcher icons, the current one marked: **Porcelain** (the default: a
porcelain calculator showing a graph, EXE in apricot), **Pixel graph** (the curves as lit pixels
on dark glass) and **Core** (an SH-4 chip with a graph on its die). How it works:

- Each icon is an `<activity-alias>` of `MainActivity` in `AndroidManifest.xml`
  (`.LauncherPorcelain`, `.LauncherPixelGraph`, `.LauncherCore`) with the MAIN/LAUNCHER filter
  and its own `android:icon`; `MainActivity` itself has no LAUNCHER filter (it keeps the
  VIEW/SEND filters for `.g3a` files). Porcelain's alias is `enabled="true"`, the others
  `false`, so a fresh install shows Porcelain.
- Choosing one (`AppIcons.select`) enables its alias, then disables the others, with
  `PackageManager.setComponentEnabledSetting(…, DONT_KILL_APP)`. The enabled alias *is* the
  setting; nothing else is stored, and it survives updates (`adb install -r`).
- Android closes the app's task when the alias it was started from is disabled (the process
  stays), so the app saves the session first and the dialog says the app closes; it is opened
  again from the new icon and resumes where it was. Launchers drop home-screen shortcuts of the
  disabled alias: the user puts the new icon there again.
- Each icon is an adaptive icon (`res/mipmap-anydpi/ic_launcher_<name>.xml`) with a background
  and a foreground VectorDrawable and a `<monochrome>` layer for themed icons (the foreground in
  one colour, scaled to the size of the system's themed glyphs). They are generated, not drawn
  by hand: after changing the designs in `android/tools/make_launcher_icons.py` run
  ```sh
  python3 android/tools/make_launcher_icons.py            # rewrites res/drawable/ic_launcher_* + res/mipmap-anydpi/
  python3 android/tools/make_launcher_icons.py --preview /tmp/icons   # also SVG previews of the same paths
  ```
  The app icon in Settings → Apps and in "Open with" lists is the application's own
  (`@mipmap/ic_launcher_porcelain`), whichever launcher icon is chosen.

## Troubleshooting

- **Keys do nothing after the app sat idle:** the calculator auto-powered-off, like the real
  one does after ~10 minutes — press **AC/ON**. (Only part of the display-off is rendered yet.)
- **Wireless adb drops** when the phone's screen sleeps: wake it, then
  `adb kill-server`, `adb mdns services`, and `adb connect <the listed _adb-tls-connect name>`.
- **A resumed session shows garbage or ignores keys:** the saved session was written while the
  machine was in a bad state (e.g. an add-in run on a build without the MMU). Force-stop the
  app, then either delete `sessions/<hash>.state` (cold boot) or, for a pushed dump, push a
  clean provisioned `cg50_state.bin` / `cg50_state_32mb.bin`, and start it again.
- **The core hit something it can't execute:** it halts rather than closing the app; the
  reason is logged to logcat under the core's tag (`adb logcat -s cg50 cg50-key`).
- ABIs are limited to `arm64-v8a` (phones) and `x86_64` (emulators) in
  `app/build.gradle.kts` — the two the Go script builds. Add `armeabi-v7a` to both if needed.

## Not done yet

- Annunciator state on the SHIFT/ALPHA keys (the OS shows it in its status bar already).
- A native version of the 1-bpp text renderer (0x8017b75e, ~20 % of a menu move).
- A store build (Play listing, app bundle).
