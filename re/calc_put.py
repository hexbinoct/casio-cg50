#!/usr/bin/env python3
"""Copy an add-in onto the calculator's USB Flash drive and check it landed intact.

  python re/calc_put.py                       # show the add-ins on the drive (name, size, menu title)
  python re/calc_put.py tools/flash_dump/FLASHDMP.g3a

The drive is found by looking for .g3a files at a drive root (the calculator in USB Flash mode).
Nothing on the calculator is deleted or overwritten: an existing file with the same name stops it.
"""
import datetime
import hashlib
import os
import shutil
import string
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")


def calc_drive():
    for d in string.ascii_uppercase:
        root = f"{d}:\\"
        try:
            if any(n.lower().endswith(".g3a") for n in os.listdir(root)):
                return root
        except OSError:
            pass
    sys.exit("no calculator drive (USB Flash mode) found")


def g3a_info(path):
    b = open(path, "rb").read(0x100)
    internal = b[0x60:0x6B].split(b"\0")[0].decode("ascii", "replace")
    title = b[0x6B:0x6B + 0x24].split(b"\0")[0].decode("ascii", "replace")
    return internal, title


def sha(path):
    return hashlib.sha256(open(path, "rb").read()).hexdigest()


def main():
    drive = calc_drive()
    for n in sorted(os.listdir(drive)):
        if n.lower().endswith(".g3a"):
            p = os.path.join(drive, n)
            st = os.stat(p)
            internal, title = g3a_info(p)
            when = datetime.datetime.fromtimestamp(st.st_mtime).strftime("%Y-%m-%d")
            print(f"  {n:16} {st.st_size:7} B  {when}  {internal:12} menu: {title}")
    total, used, free = shutil.disk_usage(drive)
    print(f"{drive}: {free} bytes free of {total}")
    if len(sys.argv) < 2:
        return
    src = os.path.join(ROOT, sys.argv[1]) if not os.path.isabs(sys.argv[1]) else sys.argv[1]
    dst = os.path.join(drive, os.path.basename(src))
    if len(sys.argv) > 3 and sys.argv[2] == "--replace":
        # replace an older add-in with the same internal name: back it up first
        old = os.path.join(drive, sys.argv[3])
        bdir = os.path.join(ROOT, "os", "calc_backup", datetime.date.today().isoformat())
        os.makedirs(bdir, exist_ok=True)
        bak = os.path.join(bdir, sys.argv[3])
        i = 1
        while os.path.exists(bak):  # never overwrite an earlier backup
            stem, ext = os.path.splitext(sys.argv[3])
            bak = os.path.join(bdir, f"{stem}.{i}{ext}")
            i += 1
        shutil.copyfile(old, bak)
        if sha(old) != sha(bak):
            sys.exit("backup differs: stopping")
        print(f"backed up {old} -> {os.path.normpath(bak)}")
        os.remove(old)
        free += os.path.getsize(bak)
        dst = old
    if os.path.exists(dst):
        sys.exit(f"{dst} already exists: not overwriting")
    if os.path.getsize(src) > free:
        sys.exit(f"not enough room: {os.path.getsize(src)} > {free}")
    shutil.copyfile(src, dst)
    ok = sha(src) == sha(dst)
    print(f"copied {os.path.basename(src)} -> {dst} ({os.path.getsize(dst)} B), hash {'matches' if ok else 'DIFFERS'}")
    print("menu title:", g3a_info(dst)[1])


if __name__ == "__main__":
    main()
