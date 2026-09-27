#!/usr/bin/env python
"""Phone build / install / measure helper for the Android app (one `python re/phone.py <cmd>`
per step, so no ad-hoc shell commands are needed).

  python re/phone.py golib               cross-compile emu_go -> jniLibs (android/build_go_lib.ps1)
  python re/phone.py apk                 gradle :app:installDebug --offline (JAVA_HOME set)
  python re/phone.py build               golib + apk
  python re/phone.py start [noadpf] [original|fast]   force-stop + start (no ADPF hints / set the speed pref)
  python re/phone.py perf [secs]         tail the cg50-perf logcat lines for secs (default 5)
  python re/phone.py shot [out.png]      screenshot -> re/_phone.png (find key coordinates)
  python re/phone.py hold X Y [ms] [n]   touch-hold (x,y) for ms (default 1200) n times (default 3),
                                         report LCD pushes (= menu moves) per hold from cg50-perf
  python re/phone.py tap X Y             single tap
"""
import os
import re
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ADB = r"D:\files\Android_SDK\platform-tools\adb.exe"
JAVA_HOME = r"D:\Installations\Java\jdk-26.0.1"
SERIAL = "adb-584c917b-jFkRGG._adb-tls-connect._tcp"
PKG = "com.hexbinoct.cg50"


def adb(*args, capture=False, check=True):
    cmd = [ADB, "-s", SERIAL, *args]
    if capture:
        return subprocess.run(cmd, capture_output=True, text=True, encoding="utf-8",
                              errors="replace", check=check).stdout
    return subprocess.run(cmd, check=check)


def golib():
    subprocess.run(["pwsh", "-File", os.path.join(ROOT, "android", "build_go_lib.ps1")], check=True)


def apk():
    env = dict(os.environ, JAVA_HOME=JAVA_HOME)
    subprocess.run([os.path.join(ROOT, "android", "gradlew.bat"), "-p", os.path.join(ROOT, "android"),
                    ":app:installDebug", "--offline", "-q"], env=env, check=True)


def start(adpf=True, speed=None):
    # HOME first: onPause snapshots the session, so the restart resumes the CURRENT machine
    # (a force-stop alone would resume whatever the last real pause saved).
    adb("shell", "input", "keyevent", "3")
    time.sleep(1.5)
    adb("shell", "am", "force-stop", PKG)
    adb("logcat", "-c")
    extra = ["--es", "speed", speed] if speed else []  # persists in the app's prefs
    adb("shell", "am", "start", "-n", f"{PKG}/.MainActivity", "--ez", "adpf", "true" if adpf else "false", *extra)
    time.sleep(3)
    out = adb("logcat", "-d", "-s", "cg50", "cg50-perf", "AndroidRuntime", "DEBUG", capture=True)
    print(out[-3000:])


def perf_lines():
    out = adb("logcat", "-d", "-s", "cg50-perf", capture=True)
    return [l for l in out.splitlines() if "emulated=" in l or "render:" in l]


def perf(secs=5):
    adb("logcat", "-c")
    time.sleep(secs)
    for l in perf_lines():
        print(l)


def shot(out=None):
    out = out or os.path.join(ROOT, "re", "_phone.png")
    png = subprocess.run([ADB, "-s", SERIAL, "exec-out", "screencap", "-p"], capture_output=True,
                         check=True).stdout
    with open(out, "wb") as f:
        f.write(png)
    size = adb("shell", "wm", "size", capture=True).strip()
    print(f"wrote {out} ({len(png)} bytes); {size}")


def pushes_in(lines):
    return sum(int(m.group(1)) for l in lines for m in [re.search(r"pushes=(\d+)", l)] if m)


# Samples, every 50 ms for `n` samples: little-core (cpu0) and big-core (cpu6) current frequency
# in MHz and which cpu the emu thread is on. Shows whether DVFS/core placement lags a burst.
SAMPLER = r"""
pid=$(pidof {pkg}); tid=
for t in /proc/$pid/task/*; do [ "$(cat $t/comm)" = "cg50-emu" ] && tid=${{t##*/}}; done
[ -z "$tid" ] && {{ echo "NO cg50-emu THREAD (pid=$pid): activity paused or app dead"; exit 0; }}
i=0; while [ $i -lt {n} ]; do
  f0=$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq)
  f6=$(cat /sys/devices/system/cpu/cpu6/cpufreq/scaling_cur_freq)
  c=$(awk '{{print $39}}' /proc/$pid/task/$tid/stat)
  echo "t=$((i*50))ms little=$((f0/1000)) big=$((f6/1000)) render@cpu$c"
  i=$((i+1)); sleep 0.05
done
"""


def hold(x, y, ms=1200, n=3, sample=True):
    results = []
    for i in range(n):
        adb("logcat", "-c")
        sampler = None
        if sample:
            sampler = subprocess.Popen([ADB, "-s", SERIAL, "shell", SAMPLER.format(pkg=PKG, n=50)],
                                       stdout=subprocess.PIPE, text=True)
            time.sleep(0.3)
        t0 = time.time()
        adb("shell", "input", "swipe", str(x), str(y), str(x), str(y), str(ms))
        dt = time.time() - t0
        time.sleep(2.5)  # let the last 1 s perf window close
        lines = perf_lines()
        p = pushes_in(lines)
        results.append(p)
        print(f"hold {i + 1}: {p} pushes (swipe cmd took {dt:.2f}s)")
        for l in lines:
            print("   ", l.split("cg50-perf:")[-1].strip())
        if sampler:
            out, _ = sampler.communicate(timeout=10)
            print("    " + " | ".join(s.replace("render@", "") for s in out.split("\n") if s))
        time.sleep(1.5)
    print(f"pushes per {ms} ms hold: {results}")


def tap(x, y):
    adb("shell", "input", "tap", str(x), str(y))


def keylog():
    """Key events the keypad delivered to the core (KeypadView logs them under cg50-key)."""
    out = adb("logcat", "-d", "-s", "cg50-key", "cg50", capture=True)
    print("\n".join(out.splitlines()[-30:]).encode("ascii", "replace").decode())


def back():
    adb("shell", "input", "keyevent", "4")  # dismisses a dialog left open by `hold` on the screen


def pull(dst=None):
    """Copy the phone's current save-state (written on every pause) to re/_phone_state.bin."""
    dst = dst or os.path.join(ROOT, "re", "_phone_state.bin")
    adb("pull", f"/sdcard/Android/data/{PKG}/files/cg50_state.bin", dst)
    print(f"pulled -> {dst} ({os.path.getsize(dst)} bytes)")


if __name__ == "__main__":
    a = sys.argv[1:]
    if not a:
        print(__doc__)
        sys.exit(1)
    cmd, rest = a[0], a[1:]
    if cmd == "golib":
        golib()
    elif cmd == "apk":
        apk()
    elif cmd == "build":
        golib()
        apk()
    elif cmd == "start":
        start(adpf="noadpf" not in rest, speed=next((s for s in rest if s in ("original", "fast")), None))
    elif cmd == "perf":
        perf(int(rest[0]) if rest else 5)
    elif cmd == "shot":
        shot(rest[0] if rest else None)
    elif cmd == "hold":
        hold(int(rest[0]), int(rest[1]), int(rest[2]) if len(rest) > 2 else 1200,
             int(rest[3]) if len(rest) > 3 else 3)
    elif cmd == "menuhold":  # MENU key, then hold RIGHT (coordinates for the POCO X3 layout)
        tap(608, 1006)
        time.sleep(1.5)
        hold(978, 1074, int(rest[0]) if rest else 1200, int(rest[1]) if len(rest) > 1 else 3)
    elif cmd == "tap":
        tap(int(rest[0]), int(rest[1]))
    elif cmd == "keylog":
        keylog()
    elif cmd == "back":
        back()
    elif cmd == "pull":
        pull(rest[0] if rest else None)
    else:
        print(__doc__)
        sys.exit(1)
