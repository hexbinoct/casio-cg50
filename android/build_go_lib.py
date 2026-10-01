#!/usr/bin/env python3
"""Cross-compile the Go emulator core (emu_go/) into libcg50core.so for Android, one per ABI,
into app/src/main/jniLibs/<abi>/ where Gradle packages them. The cgo C ABI lives in
emu_go/android_bridge.go (build tag `android`, set by GOOS=android). Run it whenever the Go
core changes, then build the APK (re/phone.py apk, or Android Studio).

    python3 android/build_go_lib.py

Works on the Mac and on Windows (same job as build_go_lib.ps1). The NDK is found from
$ANDROID_NDK_HOME, else the newest one under the SDK ($ANDROID_HOME, ~/Library/Android/sdk,
%LOCALAPPDATA%/Android/Sdk, D:/files/Android_SDK).
"""
import glob
import os
import platform
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
EMU = os.path.join(HERE, "..", "emu_go")
JNI = os.path.join(HERE, "app", "src", "main", "jniLibs")
API = 30  # must be <= the app's minSdk (30)
TARGETS = [("arm64-v8a", "arm64", "aarch64"), ("x86_64", "amd64", "x86_64")]


def find_ndk():
    if os.environ.get("ANDROID_NDK_HOME"):
        return os.environ["ANDROID_NDK_HOME"]
    sdks = [os.environ.get("ANDROID_HOME", ""), os.path.expanduser("~/Library/Android/sdk"),
            os.path.join(os.environ.get("LOCALAPPDATA", ""), "Android", "Sdk"), "D:/files/Android_SDK"]
    for sdk in sdks:
        ndks = sorted(glob.glob(os.path.join(sdk, "ndk", "*"))) if sdk else []
        if ndks:
            return ndks[-1]
    sys.exit("build_go_lib: no Android NDK found (set ANDROID_NDK_HOME)")


def main():
    ndk = find_ndk()
    host = {"Darwin": "darwin-x86_64", "Linux": "linux-x86_64", "Windows": "windows-x86_64"}[platform.system()]
    bin_dir = os.path.join(ndk, "toolchains", "llvm", "prebuilt", host, "bin")
    ext = ".cmd" if platform.system() == "Windows" else ""
    for abi, goarch, triple in TARGETS:
        cc = os.path.join(bin_dir, f"{triple}-linux-android{API}-clang{ext}")
        if not os.path.exists(cc):
            sys.exit(f"build_go_lib: NDK clang not found: {cc}")
        out = os.path.join(JNI, abi)
        os.makedirs(out, exist_ok=True)
        env = dict(os.environ, CGO_ENABLED="1", GOOS="android", GOARCH=goarch, CC=cc)
        print(f"Building libcg50core.so for {abi} (GOARCH={goarch}) ...")
        # The SONAME makes the JNI shim record "libcg50core.so" (basename) in DT_NEEDED rather than
        # the absolute build path CMake passes; without it, dlopen fails on-device.
        subprocess.run(["go", "build", "-C", EMU, "-buildmode=c-shared",
                        "-ldflags=-extldflags=-Wl,-soname,libcg50core.so",
                        "-o", os.path.join(out, "libcg50core.so"), "."], env=env, check=True)
        # the c-shared header isn't needed (native-lib.cpp declares the prototypes)
        h = os.path.join(out, "libcg50core.h")
        if os.path.exists(h):
            os.remove(h)
        print(f"  -> {out}/libcg50core.so")
    print("Done. Build the APK to repackage the .so files.")


if __name__ == "__main__":
    main()
