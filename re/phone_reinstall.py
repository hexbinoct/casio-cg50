#!/usr/bin/env python3
"""Replace the Android app on the phone when the installed one has another signing key
(INSTALL_FAILED_UPDATE_INCOMPATIBLE, e.g. a debug build from the other machine).

1. backs up the app's files folder to os/phone_backup/<date>/ (git-ignored) and checks sizes
2. uninstalls the app (that deletes the folder on the phone)
3. installs the debug APK built here and starts it (no dump inside: the welcome screen)

  python re/phone_reinstall.py            # all three steps
  python re/phone_reinstall.py backup     # step 1 only
"""
import datetime
import os
import subprocess
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
ADB = r"D:\files\Android_SDK\platform-tools\adb.exe"
SERIAL = os.environ.get("CG50_SERIAL", "adb-584c917b-jFkRGG._adb-tls-connect._tcp")
PKG = "com.hexbinoct.cg50"
REMOTE = f"/sdcard/Android/data/{PKG}/files"
APK = os.path.join(ROOT, "android", "app", "build", "outputs", "apk", "debug", "app-debug.apk")


def adb(*args, check=True):
    r = subprocess.run([ADB, "-s", SERIAL, *args], capture_output=True, text=True)
    if check and r.returncode != 0:
        sys.exit(f"adb {' '.join(args)} failed:\n{r.stdout}{r.stderr}")
    return r.stdout


def remote_files():
    out = adb("shell", f"cd {REMOTE} && find . -type f -exec stat -c '%s %n' {{}} +")
    files = {}
    for line in out.splitlines():
        size, name = line.split(" ", 1)
        files[name[2:]] = int(size)
    return files


def backup():
    dst = os.path.join(ROOT, "os", "phone_backup", datetime.date.today().isoformat())
    os.makedirs(dst, exist_ok=True)
    files = remote_files()
    for name, size in sorted(files.items()):
        local = os.path.join(dst, *name.split("/"))
        os.makedirs(os.path.dirname(local), exist_ok=True)
        adb("pull", f"{REMOTE}/{name}", local)
        got = os.path.getsize(local)
        if got != size:
            sys.exit(f"backup of {name}: {got} bytes, phone has {size}: stopping before uninstall")
        print(f"  backed up {name} ({size} bytes)")
    print(f"backup: {len(files)} files -> {os.path.normpath(dst)}")
    return dst


def main():
    if not os.path.isfile(APK):
        sys.exit(f"no APK at {APK}: build it first (gradlew :app:assembleDebug)")
    backup()
    if sys.argv[1:] == ["backup"]:
        return
    print(adb("uninstall", PKG).strip())
    print(adb("install", APK).strip())
    adb("shell", "am", "start", "-n", f"{PKG}/.MainActivity")
    print("started", PKG)


if __name__ == "__main__":
    main()
