#!/usr/bin/env python3
"""Facts for the wiki's Syscalls page (wiki/docs/os/syscalls.md), checked in the images.

- the trampoline at 0x80020070 and the table base in 3.60 (os/flash_dump/os.bin) and 3.80
  (os/os_image/cg50_os_3.80.plain.bin);
- how many entries each table has (entries that point into the OS code);
- where the syscalls the wiki names lead in 3.60;
- whether a number means the same function in both versions (handler code compared);
- how add-ins reach the trampoline (the stubs in the .g3a files in this repo).
Prints addresses and counts only (no firmware bytes).  Usage: python re/syscalls_wiki.py"""
import os
import struct
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import sh4dis  # noqa: E402

ROOT = os.path.dirname(HERE)
IMG360 = os.path.join(ROOT, "os", "flash_dump", "os.bin")
IMG380 = os.path.join(ROOT, "os", "os_image", "cg50_os_3.80.plain.bin")
TRAMP = 0x80020070


class Image:
    def __init__(self, path):
        self.d = open(path, "rb").read()

    def ok(self, va):
        return 0x80000000 <= va < 0x80000000 + len(self.d) - 4

    def u16(self, va):
        o = va & 0x0FFFFFFF
        return struct.unpack(">H", self.d[o:o + 2])[0]

    def u32(self, va):
        o = va & 0x0FFFFFFF
        return struct.unpack(">I", self.d[o:o + 4])[0]

    def table(self):
        for a in range(TRAMP, TRAMP + 0x10, 2):
            op = self.u16(a)
            if op & 0xFF00 == 0xD200:
                return self.u32((a & ~3) + 4 + (op & 0xFF) * 4)
        raise SystemExit("trampoline not found")

    def size(self, base):
        """Entries from the base that look like code pointers (even, inside the OS image)."""
        n = 0
        while True:
            v = self.u32(base + n * 4)
            if not (self.ok(v) and v % 2 == 0):
                return n, v
            n += 1

    def lit_count(self, val):
        b = struct.pack(">I", val)
        return sum(1 for o in range(0, len(self.d) - 3, 4) if self.d[o:o + 4] == b)


def main():
    a, b = Image(IMG360), Image(IMG380)

    sh4dis.set_image(a.d)
    print("3.60 trampoline:")
    for va in range(TRAMP, TRAMP + 10, 2):
        print(f"  {va:08x}: {sh4dis.decode(a.u16(va), va)}")
    print("  (no range check on r0 before the table lookup)")

    t60, t80 = a.table(), b.table()
    n60, end60 = a.size(t60)
    n80, end80 = b.size(t80)
    print(f"3.60 table 0x{t60:08x}: {n60} entries = ids 0x0000-0x{n60 - 1:04x}; "
          f"next word 0x{end60:08x} at 0x{t60 + n60 * 4:08x}")
    print(f"3.80 table 0x{t80:08x}: {n80} entries = ids 0x0000-0x{n80 - 1:04x}; "
          f"next word 0x{end80:08x} at 0x{t80 + n80 * 4:08x}")
    dup = len({a.u32(t60 + i * 4) for i in range(n60)})
    print(f"3.60: {dup} distinct handler addresses among {n60} entries")
    print(f"literal 0x80020070 words: 3.60 {a.lit_count(TRAMP)}, 3.80 {b.lit_count(TRAMP)}")

    named = {
        0x0017: "add-in table rebuild (install.go)",
        0x025F: "Bdisp_PutDisp_DD", 0x0260: "Bdisp_PutDisp_DD_stripe",
        0x11ED: "standby loop function (boot.md)", 0x127E: "single-key check (keyboard.md)",
        0x128F: "OpCodeToStr", 0x1839: "PowerOff",
        0x1DA3: "Bfile_OpenFile_OS", 0x1DA4: "Bfile_CloseFile_OS",
        0x1DAE: "Bfile_CreateEntry_OS", 0x1DAF: "Bfile_WriteFile_OS",
        0x1DB4: "Bfile_DeleteEntry", 0x1F42: "sys_free", 0x1F44: "sys_malloc",
    }

    def same(i, k=32):
        ha, hb = a.u32(t60 + i * 4), b.u32(t80 + i * 4)
        return a.d[ha & 0x0FFFFFFF:(ha & 0x0FFFFFFF) + k] == b.d[hb & 0x0FFFFFFF:(hb & 0x0FFFFFFF) + k]

    print("named syscalls (3.60 handler / 3.80 handler / first 32 code bytes equal?):")
    for i, name in named.items():
        print(f"  0x{i:04x} {name:36s} 0x{a.u32(t60 + i * 4):08x}  0x{b.u32(t80 + i * 4):08x}  {same(i)}")

    common = min(n60, n80)
    eq = sum(same(i) for i in range(common))
    eq8 = sum(same(i, 8) for i in range(common))
    print(f"ids 0..0x{common - 1:04x}: handler's first 32 bytes equal in {eq}/{common}, "
          f"first 8 bytes in {eq8}/{common}")

    # add-in stubs: a .g3a's code starts at file offset 0x7000 and runs at 0x00300000
    g3as = [os.path.join(ROOT, "emu_go", "testdata", "aluprobe.g3a"),
            os.path.join(ROOT, "tools", "keyprobe", "KEYPROBE.g3a"),
            os.path.join(ROOT, "F:/ru/myprojects/september/cg50_addons/dasm/DASM.g3a")]
    for g in g3as:
        if not os.path.exists(g):
            continue
        code = open(g, "rb").read()[0x7000:]
        img = b"\0" * 0x300000 + code
        sh4dis.set_image(img)
        lits = [0x300000 + o for o in range(0, len(code) - 3, 4)
                if code[o:o + 4] == struct.pack(">I", TRAMP)]
        users = []
        for o in range(0, len(code) - 1, 2):
            op = struct.unpack(">H", code[o:o + 2])[0]
            if op & 0xF000 == 0xD000:
                va = 0x300000 + o
                if ((va & ~3) + 4 + (op & 0xFF) * 4) in lits:
                    users.append(va)
        print(f"{os.path.basename(g)}: {len(lits)} literal(s) 0x80020070, {len(users)} loader(s)")
        for va in users[:1] + users[-1:]:
            for x in range(va - 4, va + 8, 2):
                print(f"    {x:08x}: {sh4dis.decode(struct.unpack('>H', img[x:x + 2])[0], x)}")
            print("    --")


if __name__ == "__main__":
    main()
