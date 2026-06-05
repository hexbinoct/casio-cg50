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

    /** Enqueue a matrix key press, 0-based (row,col); see re/KEYMAP.md. */
    external fun injectKey(row: Int, col: Int)

    /** Fill dst (width*height*4 bytes) with RGBA pixels; returns bytes written or -1. */
    external fun framebufferRGBA(dst: ByteArray): Int

    /** Capture a gzip save-state blob (or null on error). */
    external fun snapshot(): ByteArray?
}
