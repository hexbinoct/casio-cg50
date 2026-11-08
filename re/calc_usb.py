#!/usr/bin/env python3
"""Is the calculator on USB, and what can talk to it here? (Windows)

Lists the PnP devices with Casio's vendor ID (07CF), the drive letter of the calculator's
USB Flash mode if it is mounted, and whether usbipd / WSL are installed (fxlink on Windows
runs inside WSL with the device passed through by usbipd-win).

  python re/calc_usb.py
"""
import os
import shutil
import string
import subprocess

PS = ["powershell", "-NoProfile", "-Command"]


def ps(cmd):
    r = subprocess.run(PS + [cmd], capture_output=True, text=True)
    return (r.stdout + r.stderr).strip()


print("== Casio USB devices (VID 07CF) ==")
print(ps("Get-PnpDevice -PresentOnly | Where-Object { $_.InstanceId -match 'VID_07CF' } | "
         "Format-Table -AutoSize Status,Class,FriendlyName,InstanceId | Out-String -Width 220") or "  none")

print("\n== Removable drives ==")
print(ps("Get-Volume | Where-Object { $_.DriveType -eq 'Removable' } | "
         "Format-Table -AutoSize DriveLetter,FileSystemLabel,FileSystem,Size,SizeRemaining | Out-String -Width 200") or "  none")
for d in string.ascii_uppercase:
    root = f"{d}:\\"
    try:
        names = os.listdir(root)
    except OSError:
        continue
    g3a = [n for n in names if n.lower().endswith(".g3a")]
    if g3a:
        print(f"  {root} has add-ins: {', '.join(sorted(g3a))}")

print("\n== Tools ==")
for t in ("usbipd", "wsl", "fxlink", "docker"):
    print(f"  {t}: {shutil.which(t) or 'not found'}")
if shutil.which("wsl"):
    r = subprocess.run(["wsl", "-l", "-v"], capture_output=True)
    print("  wsl -l -v:", r.stdout.decode("utf-16-le", "replace").strip().replace("\n", "\n    "))

g3a = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "tools", "flash_dump", "FLASHDMP.g3a")
print("\nFLASHDMP.g3a here:", os.path.getsize(g3a) if os.path.isfile(g3a) else "missing", "bytes")
