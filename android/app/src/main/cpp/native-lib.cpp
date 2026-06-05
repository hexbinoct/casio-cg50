// JNI shim: bridges the Kotlin `NativeBridge` methods to the Go emulator core's C ABI
// (libcg50core.so, exported from emu_go/android_bridge.go). Each Java_… function below
// matches an `external fun` in com.hexbinoct.cg50.NativeBridge.
#include <jni.h>
#include <cstdint>

// --- Go core C ABI (see emu_go/android_bridge.go) ---
extern "C" {
void     EmuInit(const uint8_t *flash, int n);
int      EmuWidth();
int      EmuHeight();
int      EmuResume(const uint8_t *blob, int n);
void     EmuStep(int n);
void     EmuInjectKey(int row, int col);
int      EmuFramebufferRGBA(uint8_t *dst, int capacity);
uint8_t *EmuSnapshot(int *outLen);
void     EmuFree(uint8_t *p);
}

#define NB(name) Java_com_hexbinoct_cg50_NativeBridge_##name

extern "C" JNIEXPORT void JNICALL NB(init)(JNIEnv *env, jobject, jbyteArray flash) {
    jsize n = env->GetArrayLength(flash);
    jbyte *p = env->GetByteArrayElements(flash, nullptr);
    EmuInit(reinterpret_cast<uint8_t *>(p), static_cast<int>(n));
    env->ReleaseByteArrayElements(flash, p, JNI_ABORT); // read-only; no copy-back
}

extern "C" JNIEXPORT jint JNICALL NB(width)(JNIEnv *, jobject) { return EmuWidth(); }
extern "C" JNIEXPORT jint JNICALL NB(height)(JNIEnv *, jobject) { return EmuHeight(); }

extern "C" JNIEXPORT jint JNICALL NB(resume)(JNIEnv *env, jobject, jbyteArray blob) {
    jsize n = env->GetArrayLength(blob);
    jbyte *p = env->GetByteArrayElements(blob, nullptr);
    int r = EmuResume(reinterpret_cast<uint8_t *>(p), static_cast<int>(n));
    env->ReleaseByteArrayElements(blob, p, JNI_ABORT);
    return r;
}

extern "C" JNIEXPORT void JNICALL NB(step)(JNIEnv *, jobject, jint n) { EmuStep(n); }

extern "C" JNIEXPORT void JNICALL NB(injectKey)(JNIEnv *, jobject, jint row, jint col) {
    EmuInjectKey(row, col);
}

// Fills the caller's byte[] (Width*Height*4 RGBA). Returns bytes written, or -1 if too small.
extern "C" JNIEXPORT jint JNICALL NB(framebufferRGBA)(JNIEnv *env, jobject, jbyteArray dst) {
    jsize cap = env->GetArrayLength(dst);
    jbyte *p = env->GetByteArrayElements(dst, nullptr);
    int wrote = EmuFramebufferRGBA(reinterpret_cast<uint8_t *>(p), static_cast<int>(cap));
    env->ReleaseByteArrayElements(dst, p, 0); // copy modified pixels back to the JVM array
    return wrote;
}

// Returns a gzip save-state blob (or null). The Go side malloc's it; we copy into a Java
// array and free the native buffer.
extern "C" JNIEXPORT jbyteArray JNICALL NB(snapshot)(JNIEnv *env, jobject) {
    int len = 0;
    uint8_t *b = EmuSnapshot(&len);
    if (b == nullptr || len <= 0) return nullptr;
    jbyteArray arr = env->NewByteArray(len);
    if (arr != nullptr) {
        env->SetByteArrayRegion(arr, 0, len, reinterpret_cast<jbyte *>(b));
    }
    EmuFree(b);
    return arr;
}
