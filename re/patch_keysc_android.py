"""Second wiring patch (cont.18l): key-down/up through the Android JNI shim + Kotlin keypad
(touch down/up instead of click, so held arrows auto-repeat), host-set scan period, and the
Python oracle's default scan period. Exact-string edits, assert on mismatch, re-runnable."""
ROOT = "F:/ru/myprojects/may/cg50/"


def patch(path, edits):
    p = ROOT + path
    s = open(p, encoding="utf-8", newline="").read()
    for old, new in edits:
        if new in s and old not in s:
            print(f"  [skip] {path}: already applied: {new[:50]!r}")
            continue
        n = s.count(old)
        assert n == 1, f"{path}: expected 1 match, got {n} for {old[:70]!r}"
        s = s.replace(old, new)
    open(p, "w", encoding="utf-8", newline="").write(s)
    print(f"  [ok] {path}")


patch("emu/mmio.py", [
    ("    DEFAULT_SCAN_PERIOD = 30000\n", "    DEFAULT_SCAN_PERIOD = 500000\n"),
])

patch("android/app/src/main/cpp/native-lib.cpp", [
    ("void     EmuInjectKey(int row, int col);\n",
     "void     EmuInjectKey(int row, int col);\n"
     "void     EmuKeyDown(int row, int col);\n"
     "void     EmuKeyUp(int row, int col);\n"
     "void     EmuReleaseAllKeys(void);\n"
     "void     EmuSetKeyScanPeriod(long long instr);\n"),
    ("""extern "C" JNIEXPORT void JNICALL NB(injectKey)(JNIEnv *, jobject, jint row, jint col) {
    EmuInjectKey(row, col);
}
""",
     """extern "C" JNIEXPORT void JNICALL NB(injectKey)(JNIEnv *, jobject, jint row, jint col) {
    EmuInjectKey(row, col);
}

// Physical-style press/release: the key stays down in the emulated matrix until keyUp, so the
// OS's own key-repeat runs while an on-screen button is held.
extern "C" JNIEXPORT void JNICALL NB(keyDown)(JNIEnv *, jobject, jint row, jint col) {
    EmuKeyDown(row, col);
}
extern "C" JNIEXPORT void JNICALL NB(keyUp)(JNIEnv *, jobject, jint row, jint col) {
    EmuKeyUp(row, col);
}
extern "C" JNIEXPORT void JNICALL NB(releaseAllKeys)(JNIEnv *, jobject) { EmuReleaseAllKeys(); }
extern "C" JNIEXPORT void JNICALL NB(setKeyScanPeriod)(JNIEnv *, jobject, jlong instr) {
    EmuSetKeyScanPeriod(instr);
}
"""),
])

patch("android/app/src/main/java/com/hexbinoct/cg50/NativeBridge.kt", [
    ("""    /** Enqueue a matrix key press, 0-based (row,col); see re/KEYMAP.md. */
    external fun injectKey(row: Int, col: Int)
""",
     """    /** Tap a matrix key (press, hold a few scans, release), 0-based (row,col); see re/KEYMAP.md. */
    external fun injectKey(row: Int, col: Int)

    /** Hold / release a matrix key like a finger on the real keypad (OS key-repeat works). */
    external fun keyDown(row: Int, col: Int)
    external fun keyUp(row: Int, col: Int)

    /** Release every key and drop queued taps (call when the activity pauses). */
    external fun releaseAllKeys()

    /** KEYSC scan interval in emulated instructions (= the OS key-repeat clock); ~20 ms worth. */
    external fun setKeyScanPeriod(instr: Long)
"""),
])

patch("android/app/src/main/java/com/hexbinoct/cg50/MainActivity.kt", [
    ("""                    setOnClickListener { NativeBridge.injectKey(key.row, key.col) }
""",
     """                    // Touch down/up = matrix press/release, so holding an arrow auto-repeats
                    // exactly as on the calculator (the OS runs its own repeat timing).
                    setOnTouchListener { v, ev ->
                        when (ev.actionMasked) {
                            MotionEvent.ACTION_DOWN -> { v.isPressed = true; NativeBridge.keyDown(key.row, key.col) }
                            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                                v.isPressed = false; NativeBridge.keyUp(key.row, key.col)
                                if (ev.actionMasked == MotionEvent.ACTION_UP) v.performClick()
                            }
                        }
                        true
                    }
"""),
    ("""        binding.screenView.pauseRendering()
""",
     """        binding.screenView.pauseRendering()
        NativeBridge.releaseAllKeys()
"""),
])

# MotionEvent import (only if missing)
p = ROOT + "android/app/src/main/java/com/hexbinoct/cg50/MainActivity.kt"
s = open(p, encoding="utf-8", newline="").read()
if "import android.view.MotionEvent" not in s:
    s = s.replace("import android.view.ViewGroup\n", "import android.view.MotionEvent\nimport android.view.ViewGroup\n", 1)
    assert "import android.view.MotionEvent" in s, "MainActivity.kt: could not add MotionEvent import"
    open(p, "w", encoding="utf-8", newline="").write(s)
    print("  [ok] MainActivity.kt import")
print("done")
