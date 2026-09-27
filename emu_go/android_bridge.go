//go:build android

package main

// C ABI bridge for Android (and any JNI/cgo host). Build a shared library with the NDK:
//
//	CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
//	CC=$NDK/toolchains/llvm/prebuilt/<host>/bin/aarch64-linux-android24-clang \
//	go build -buildmode=c-shared -o libcg50.so .
//
// (Repeat per ABI: arm64-v8a, armeabi-v7a, x86_64.) The generated libcg50.h declares the
// exported symbols below; load the .so via System.loadLibrary and call them through JNI.
// See docs/ANDROID.md for the Kotlin glue (run loop on a thread, blit RGBA into a Bitmap,
// map on-screen buttons to EmuInjectKey using re/KEYMAP.md). This file is build-tagged
// `android` so the normal desktop build/tests never pull in cgo.

/*
#include <stdlib.h>
#include <stdint.h>
#include <android/log.h>
#cgo LDFLAGS: -llog
*/
import "C"

import "unsafe"

// gEmu is the single process-wide machine the host drives (one calculator per app).
var gEmu *Emulator

// keyTag is the logcat tag for key-path diagnostics (allocated once, lives for process life).
var keyTag = C.CString("cg50-key")

//export EmuInit
func EmuInit(flash *C.uint8_t, n C.int) {
	gEmu = NewEmulator(C.GoBytes(unsafe.Pointer(flash), n))
	gEmu.EnableHLE(true) // native OS blitter (hle.go): ~4x less work per menu move
	// Route the key state-machine diagnostics to Android logcat (tag cg50-key).
	gEmu.dbg = func(s string) {
		cs := C.CString(s)
		C.__android_log_write(C.ANDROID_LOG_INFO, keyTag, cs)
		C.free(unsafe.Pointer(cs))
	}
}

//export EmuWidth
func EmuWidth() C.int { return C.int(FbWidth) }

//export EmuHeight
func EmuHeight() C.int { return C.int(FbHeight) }

// EmuResume restores a save-state blob (e.g. one provisioned to the MAIN MENU). 0 = ok.
//
//export EmuResume
func EmuResume(blob *C.uint8_t, n C.int) C.int {
	if gEmu == nil {
		return -1
	}
	if err := gEmu.Resume(C.GoBytes(unsafe.Pointer(blob), n)); err != nil {
		return -2
	}
	return 0
}

// EmuStep advances the machine by n instructions (host calls this each frame from its run
// thread; do its own pacing, or run flat-out for a fresh boot).
//
//export EmuStep
func EmuStep(n C.int) {
	if gEmu != nil {
		gEmu.Step(int(n))
	}
}

// EmuInjectKey taps a matrix key (0-based row,col; see re/KEYMAP.md): pressed for a few
// hardware scans, then released. SHIFT/ALPHA are keys too — tap the modifier before the
// target.
//
//export EmuInjectKey
func EmuInjectKey(row, col C.int) {
	if gEmu != nil {
		gEmu.InjectKey(uint32(row), uint32(col))
	}
}

// EmuKeyDown / EmuKeyUp hold and release a matrix key for as long as the user's finger is on
// the on-screen button; the OS's own key-repeat runs while it is held.
//
//export EmuKeyDown
func EmuKeyDown(row, col C.int) {
	if gEmu != nil {
		gEmu.KeyDown(uint32(row), uint32(col))
	}
}

//export EmuKeyUp
func EmuKeyUp(row, col C.int) {
	if gEmu != nil {
		gEmu.KeyUp(uint32(row), uint32(col))
	}
}

// EmuReleaseAllKeys clears the matrix and queued taps (host lost focus / paused).
//
//export EmuReleaseAllKeys
func EmuReleaseAllKeys() {
	if gEmu != nil {
		gEmu.ReleaseAllKeys()
	}
}

// EmuSetInstrPerSec tells the core the host's measured throughput (instructions per second)
// so RTC/timer/key-scan timing runs at wall-clock speed. Call at start and whenever the
// measured rate drifts.
//
//export EmuSetInstrPerSec
func EmuSetInstrPerSec(ips C.longlong) {
	if gEmu != nil && ips > 0 {
		gEmu.SetInstrPerSecond(uint64(ips))
	}
}

// EmuExecuted returns the instructions actually executed so far (idle cycles excluded); the
// app sizes its per-frame budget from this.
//
//export EmuExecuted
func EmuExecuted() C.longlong {
	if gEmu == nil {
		return 0
	}
	return C.longlong(gEmu.Executed())
}

// EmuCycles returns the machine's cycle counter (emulated time = cycles / instr-per-second).
// The host paces on this rather than on the chunk sizes it asks for: a Step may overshoot
// its budget (a native blit charges its cycles in one go; a key injection runs extra
// instructions), and pacing by request would let emulated time run fast.
//
//export EmuCycles
func EmuCycles() C.longlong {
	if gEmu == nil {
		return 0
	}
	return C.longlong(gEmu.Cycles())
}

// EmuPushes returns the number of VRAM->LCD frame pushes so far (one per OS redraw, e.g. per
// menu cursor move); the app logs it so a held-key burst can be measured from logcat.
//
//export EmuPushes
func EmuPushes() C.longlong {
	if gEmu == nil {
		return 0
	}
	return C.longlong(gEmu.Pushes())
}

// EmuFrameGen changes whenever the panel contents do; the app skips the blit when it hasn't.
//
//export EmuFrameGen
func EmuFrameGen() C.longlong {
	if gEmu == nil {
		return 0
	}
	return C.longlong(gEmu.FrameGen())
}

// EmuSetHLE turns the native blitter on (1) or off (0); on after EmuInit.
//
//export EmuSetHLE
func EmuSetHLE(on C.int) {
	if gEmu != nil {
		gEmu.EnableHLE(on != 0)
	}
}

// EmuSetClock sets the emulated RTC calendar (unix seconds).
//
//export EmuSetClock
func EmuSetClock(unixSec C.longlong) { // not `unix`: Android's compiler predefines that macro
	if gEmu != nil {
		gEmu.SetClock(int64(unixSec))
	}
}

// EmuSetKeyScanPeriod sets the KEYSC scan interval (= OS key-repeat clock) in emulated
// instructions; hosts pass ~20 ms worth of their real throughput. (Superseded by
// EmuSetInstrPerSec, which derives it; kept for compatibility.)
//
//export EmuSetKeyScanPeriod
func EmuSetKeyScanPeriod(instr C.longlong) {
	if gEmu != nil && instr > 0 {
		gEmu.SetKeyScanPeriod(uint64(instr))
	}
}

// EmuFramebufferRGBA fills dst (capacity bytes) with Width*Height*4 RGBA pixels. Returns the
// number of bytes written, or -1 if dst is too small. Host blits this into a Bitmap.
//
//export EmuFramebufferRGBA
func EmuFramebufferRGBA(dst *C.uint8_t, capacity C.int) C.int {
	need := FbWidth * FbHeight * 4
	if gEmu == nil || int(capacity) < need {
		return -1
	}
	gEmu.FramebufferRGBA(unsafe.Slice((*byte)(unsafe.Pointer(dst)), need))
	return C.int(need)
}

// EmuSnapshot returns a malloc'd gzip save-state blob (set *outLen); the host must persist
// it and then call EmuFree on the pointer. Returns NULL on error.
//
//export EmuSnapshot
func EmuSnapshot(outLen *C.int) *C.uint8_t {
	if gEmu == nil {
		return nil
	}
	b, err := gEmu.Snapshot()
	if err != nil {
		return nil
	}
	p := C.malloc(C.size_t(len(b)))
	copy(unsafe.Slice((*byte)(p), len(b)), b)
	*outLen = C.int(len(b))
	return (*C.uint8_t)(p)
}

//export EmuFree
func EmuFree(p *C.uint8_t) { C.free(unsafe.Pointer(p)) }
