package com.hexbinoct.cg50

/**
 * Kotlin <-> native bridge. The methods are implemented in app/src/main/cpp/native-lib.cpp
 * (libcg50.so), which forwards to the Go emulator core's C ABI (libcg50core.so, built by
 * android/build_go_lib.ps1). Load order matters: the Go core first, then the JNI shim that
 * depends on it.
 */
object NativeBridge {
    init {
        System.loadLibrary("cg50core") // Go emulator core (EmuInit/Step/... C ABI)
        System.loadLibrary("cg50")     // JNI shim that calls into it
    }

    /** Create the machine from a flash-dump image (the user's own flash_full.bin). */
    external fun init(flash: ByteArray)

    /** Framebuffer dimensions (384 x 216). */
    external fun width(): Int
    external fun height(): Int

    /** Restore a save-state blob (e.g. one provisioned to the MAIN MENU). 0 = ok. */
    external fun resume(blob: ByteArray): Int

    /** Advance the machine by n instructions. */
    external fun step(n: Int)

    /** Tap a matrix key (press, hold a few scans, release), 0-based (row,col); see re/KEYMAP.md. */
    external fun injectKey(row: Int, col: Int)

    /** Hold / release a matrix key like a finger on the real keypad (OS key-repeat works). */
    external fun keyDown(row: Int, col: Int)
    external fun keyUp(row: Int, col: Int)

    /** Release every key and drop queued taps (call when the activity pauses). */
    external fun releaseAllKeys()

    /** KEYSC scan interval in emulated instructions (= the OS key-repeat clock); ~20 ms worth. */
    external fun setKeyScanPeriod(instr: Long)

    /** Host throughput in emulated instructions per real second: anchors RTC/timers/key scan to wall-clock. */
    external fun setInstrPerSec(ips: Long)

    /** Set the emulated calculator clock (unix seconds). */
    external fun setClock(unix: Long)

    /** Instructions actually executed so far (idle cycles excluded) — sizes the frame budget. */
    external fun executed(): Long

    /** VRAM->LCD frame pushes so far (one per OS redraw, e.g. per menu cursor move). */
    external fun pushes(): Long

    /** Changes whenever the panel contents do (any GRAM write) — skip the blit when it hasn't. */
    external fun frameGen(): Long

    /** Fill dst (width*height*4 bytes) with RGBA pixels; returns bytes written or -1. */
    external fun framebufferRGBA(dst: ByteArray): Int

    /** Capture a gzip save-state blob (or null on error). */
    external fun snapshot(): ByteArray?
}
