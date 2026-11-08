#!/usr/bin/env python3
"""Receive a FlashDump dump on Windows: usbipd-win passes the calculator into WSL, fxlink runs
in the fxSDK Docker image (the "Windows: WSL + usbipd" route of tools/flash_dump/README.md).

  python re/calc_fxlink.py bind      # once, admin (UAC): share the calculator's USB port
  python re/calc_fxlink.py run       # attach + fxlink -i; stops when a full dump has arrived
  python re/calc_fxlink.py unbind    # admin (UAC): give the port back to Windows (USB Flash mode)

A bound port stays with Windows until something attaches it, so binding while the calculator
is in USB Flash mode is harmless. `run` attaches with --auto-attach, so start it, then on the
calculator open FlashDump, press F1 and plug the cable in.

Dumps land in os/flash_dump/fxlink_<date>/ (git-ignored).
"""
import datetime
import os
import subprocess
import sys
import time

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
USBIPD = r"C:\Program Files\usbipd-win\usbipd.exe"
VIDPID = "07cf:6102"
CONTAINER = "cg50-fxlink"
SIZES = {"32mb": 32 << 20, "16mb": 16 << 20, "dram": 8 << 20, "ilram": 64 << 10}


def usbipd(*args):
    r = subprocess.run([USBIPD, *args], capture_output=True, text=True)
    return r.returncode, (r.stdout + r.stderr).strip()


def busid():
    _, out = usbipd("list")
    for line in out.splitlines():
        parts = line.split()
        if len(parts) > 1 and parts[1] == VIDPID:
            return parts[0], line
    return None, out


def elevated(*args):
    """Run usbipd as admin (UAC prompt) and wait for it."""
    arglist = ",".join(f"'{a}'" for a in args)
    cmd = (f"$p = Start-Process -FilePath '{USBIPD}' -ArgumentList {arglist} -Verb RunAs "
           f"-WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode")
    r = subprocess.run(["powershell", "-NoProfile", "-Command", cmd], capture_output=True, text=True)
    print(f"usbipd {' '.join(args)} (admin): exit {r.returncode} {r.stderr.strip()}")


def run():
    bid, line = busid()
    if not bid:
        # unplugged (the usual case: plug in after pressing F1): use the port given or 2-1
        bid = sys.argv[2] if len(sys.argv) > 2 else "2-1"
        line = f"not connected yet; waiting on port {bid}"
    print("device:", line)
    out = os.path.join(ROOT, "os", "flash_dump", f"fxlink_{datetime.date.today().isoformat()}")
    os.makedirs(out, exist_ok=True)
    before = set(os.listdir(out))
    log = open(os.path.join(out, "run.log"), "a")
    subprocess.run(["docker", "rm", "-f", CONTAINER], capture_output=True)
    print("now: on the calculator open FlashDump, press F1, plug the cable in")
    fx, seen = None, None
    t0, last, stable = time.time(), {}, 0
    try:
        while time.time() - t0 < 20 * 60:
            time.sleep(2 if fx is None else 5)
            if fx is None:
                # usbipd can only attach a device that is there: wait for it. The calculator in
                # USB Flash mode (a drive for Windows) has the same VID:PID as a gint add-in, so
                # attach only when Windows doesn't see it as mass storage: FlashDump after F1.
                b, line = busid()
                if b != bid:
                    seen = None
                    continue
                if "Mass Storage" in line:
                    if seen != "msc":
                        print(f"  {int(time.time() - t0):4}s  calculator is in USB Flash mode: leaving it to "
                              f"Windows (unplug, open FlashDump, F1, then plug in)")
                        seen = "msc"
                    continue
                if "Not shared" in line:
                    if seen != "unshared":
                        print(f"  {int(time.time() - t0):4}s  {line.strip()}: run `python re/calc_fxlink.py bind`")
                        seen = "unshared"
                    continue
                print(f"  {int(time.time() - t0):4}s  {line.strip()}: attaching to WSL")
                rc, msg = usbipd("attach", "--wsl", "--busid", bid)
                log.write(msg + "\n")
                log.flush()
                if rc != 0:
                    print("         attach failed:", msg.splitlines()[-1] if msg else rc)
                    continue
                # fxlink in Docker doesn't notice a device that arrives later (no hotplug events),
                # but finds one that is already there: start it now
                time.sleep(2)
                fx = subprocess.Popen(["docker", "run", "--rm", "--name", CONTAINER, "--privileged",
                                       "-v", "/dev/bus/usb:/dev/bus/usb", "-v", f"{out}:/out", "-w", "/out",
                                       "fxsdk:latest", "fxlink", "-i", "-w", "30"],
                                      stdout=log, stderr=subprocess.STDOUT)
                print(f"  {int(time.time() - t0):4}s  attached; fxlink -i receiving into {out}")
                continue
            new = {n: os.path.getsize(os.path.join(out, n)) for n in os.listdir(out)
                   if n not in before and n.endswith(".bin")}
            if new != last:
                for n, s in sorted(new.items()):
                    print(f"  {int(time.time() - t0):4}s  {n}  {s} bytes")
                last, stable = new, 0
            if fx.poll() is not None:
                print("fxlink exited:", fx.returncode)
                break
    finally:
        subprocess.run(["docker", "stop", "-t", "2", CONTAINER], capture_output=True)
        if fx is not None:
            print("detach:", usbipd("detach", "--busid", bid)[1] or "ok")
        log.close()
    for n in sorted(set(os.listdir(out)) - before):
        size = os.path.getsize(os.path.join(out, n))
        want = [v for k, v in SIZES.items() if f"-{k}-" in n]
        print(f"received {n}: {size} bytes", "(complete)" if want and want[0] == size else "")
    print("--- run.log (tail) ---")
    print("".join(open(os.path.join(out, "run.log"), errors="replace").readlines()[-25:]))


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "run"
    if cmd in ("bind", "unbind"):
        bid, line = busid()
        if not bid:
            sys.exit(f"calculator ({VIDPID}) not connected?\n{line}")
        elevated(cmd, "--busid", bid)
        print(usbipd("list")[1])
    elif cmd == "run":
        run()
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
