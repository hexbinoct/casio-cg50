# Running the emulator on Android

The Go core (`emu_go/`) is portable and already factored behind a small host-facing facade,
so an Android app is a thin shell: a `SurfaceView`/`Bitmap` for the 384×216 framebuffer and
an on-screen keypad that maps button taps to matrix `(row,col)` injections. No firmware ships
in this repo — the app must load the user's own flash dump (and ideally a provisioned
save-state) at runtime.

## Architecture

```
 Kotlin/Java (UI: keypad + Bitmap)        — your app
        │  JNI
        ▼
 libcg50.so  (Go core, -buildmode=c-shared)
        │
   Emulator facade (emu_go/emulator.go)    — Run / InjectKey / Framebuffer / Snapshot
        │
   CPU + Memory + MMIO (SH7305)            — the validated core
```

The whole interaction model is already proven by the desktop **web UI** (`go -C emu_go run .
0 30000 web` → framebuffer + keystrokes in a browser) and by the **`rtbench`** mode, which
drives the facade's real-time loop (measured: target 20 M ips → 19.95 M, a steady 60 fps).

## The host API (C ABI)

`emu_go/android_bridge.go` (build tag `android`) exports these via cgo `//export`. The NDK
build emits `libcg50.h` with the matching declarations:

| Symbol | Purpose |
|--------|---------|
| `EmuInit(uint8* flash, int n)` | create the machine from a flash-dump image |
| `EmuResume(uint8* blob, int n) -> int` | restore a save-state (e.g. provisioned to the MAIN MENU); 0 = ok |
| `EmuStep(int n)` | advance n instructions (call each frame from your run thread) |
| `EmuInjectKey(int row, int col)` | enqueue a matrix press (0-based; see `re/KEYMAP.md`) |
| `EmuFramebufferRGBA(uint8* dst, int cap) -> int` | fill `Width*Height*4` RGBA bytes for a Bitmap |
| `EmuSnapshot(int* outLen) -> uint8*` | malloc a gzip save-state blob (free with `EmuFree`) |
| `EmuFree(uint8*)` | free an `EmuSnapshot` pointer |
| `EmuWidth() / EmuHeight()` | framebuffer dimensions (384 × 216) |

The same Go `Emulator` type (`emu_go/emulator.go`) also offers `RunRealtime(targetIPS,
frameHz, frame, stop)` if you prefer to pace inside Go rather than from the Kotlin thread.

## Building the shared library

Install the Android NDK and, per ABI, build a c-shared lib:

```sh
NDK=$ANDROID_NDK_HOME
TOOLS=$NDK/toolchains/llvm/prebuilt/<host>/bin    # e.g. linux-x86_64, windows-x86_64

# arm64-v8a (most phones)
CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
  CC=$TOOLS/aarch64-linux-android24-clang \
  go build -C emu_go -buildmode=c-shared -o ../android/app/src/main/jniLibs/arm64-v8a/libcg50.so .

# armeabi-v7a:  GOARCH=arm  CC=armv7a-linux-androideabi24-clang
# x86_64:       GOARCH=amd64 CC=x86_64-linux-android24-clang   (emulator/dev)
```

Put each `libcg50.so` under `jniLibs/<abi>/`. (Alternatively, `gomobile bind` is possible if
the core is first moved out of `package main` into an importable package — the cgo `c-shared`
route above avoids that refactor.)

## Kotlin glue (sketch)

```kotlin
object Cg50 {
    init { System.loadLibrary("cg50") }
    external fun init(flash: ByteArray)
    external fun resume(blob: ByteArray): Int
    external fun step(n: Int)
    external fun injectKey(row: Int, col: Int)
    external fun framebufferRGBA(dst: ByteArray): Int   // dst = 384*216*4
    external fun snapshot(): ByteArray
    external fun width(): Int
    external fun height(): Int
}
// (Write a tiny C/JNI shim, or use the @CriticalNative/RegisterNatives pattern, to bind the
//  Kotlin `external` methods to the EmuXxx symbols in libcg50.so.)

// Run thread: pace to ~real time and blit each frame.
val fb = ByteArray(Cg50.width() * Cg50.height() * 4)
val bmp = Bitmap.createBitmap(Cg50.width(), Cg50.height(), Bitmap.Config.ARGB_8888)
thread {
    val sliceNs = 1_000_000_000L / 60
    val instrPerFrame = 20_000_000 / 60
    while (running) {
        val t = System.nanoTime()
        Cg50.step(instrPerFrame)
        Cg50.framebufferRGBA(fb)
        bmp.copyPixelsFromBuffer(ByteBuffer.wrap(fb))
        surface.post { imageView.setImageBitmap(bmp) }   // or draw to a SurfaceView canvas
        val sleep = sliceNs - (System.nanoTime() - t)
        if (sleep > 0) Thread.sleep(sleep / 1_000_000, (sleep % 1_000_000).toInt())
    }
}

// Keypad: each on-screen button carries its matrix (row,col) from re/KEYMAP.md.
fun onKeyTap(row: Int, col: Int) = Cg50.injectKey(row, col)
// SHIFT/ALPHA are real keys: injectKey their coords before the target key.
```

## Provisioning (skip first-boot setup)

A fresh boot lands in the language/setup wizard. Provision once and ship/store the resulting
save-state so the app resumes instantly at the MAIN MENU:

```sh
go -C emu_go run . 450000000 30000 provision   # writes os/flash_dump/cg50_state.bin (~85 KB)
```

In the app: `EmuInit(flash)` then `EmuResume(stateBlob)`. Let the user re-snapshot
(`EmuSnapshot`) on pause so their own work/settings persist across launches — this models the
real calculator's backup-battery RAM. (Flash-only persistence is insufficient: the fls0 mount
is coupled to that RAM; see RECON_NOTES cont.18f.)

## Status / TODO

- ✅ Core, facade, real-time loop, save-state, full keymap are done and validated on desktop.
- ⏳ Not done here (needs the Android toolchain): the actual NDK build, the JNI shim, the
  Android Studio project, and an on-device performance pass (desktop is ~70 M instr/s on x86;
  measure on an ARM phone and tune `targetIPS`).
