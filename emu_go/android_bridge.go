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

// EmuInjectKey enqueues a matrix press (0-based row,col; see re/KEYMAP.md). SHIFT/ALPHA are
// keys too — inject the modifier before the target.
//
//export EmuInjectKey
func EmuInjectKey(row, col C.int) {
	if gEmu != nil {
		gEmu.InjectKey(uint32(row), uint32(col))
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
