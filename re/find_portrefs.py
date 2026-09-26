"""Locate literal-pool references to the keyboard-scan port registers in os.bin (3.60,
rebased 0x80000000) and the PCs of the `mov.l @(disp,pc),Rn` instructions that load them.
Usage: python re/find_portrefs.py [hexconst ...]"""
import struct, sys
OS = "F:/ru/myprojects/may/cg50/os/flash_dump/os.bin"
BASE = 0x80000000
d = open(OS, "rb").read()
consts = [int(a, 0) for a in sys.argv[1:]] or [
    0xA4050100, 0xA4050120, 0xA405013A, 0xA405014E, 0xA4050162, 0xA405013C,
    0xA44C0000, 0xA44C0020, 0xA44B0000, 0xA44B0004, 0xA44B0008, 0xA44B000C,
    0xA44B0090, 0xA44B00D0]
# index all mov.l @(disp,pc),Rn : opcode 0xDnDD -> target = (pc&~3) + 4 + disp*4
loads = {}
for pc in range(0, len(d) - 1, 2):
    op = (d[pc] << 8) | d[pc + 1]
    if op >> 12 == 0xD:
        tgt = ((pc & ~3) + 4 + (op & 0xFF) * 4)
        loads.setdefault(tgt, []).append((pc, (op >> 8) & 0xF))
for c in consts:
    pat = struct.pack(">I", c)
    offs = [i for i in range(0, len(d) - 3, 2) if d[i:i+4] == pat]
    print(f"0x{c:08X}: {len(offs)} literal(s)")
    for o in offs:
        refs = loads.get(o, [])
        s = ", ".join(f"pc 0x{BASE+p:08x} -> r{r}" for p, r in refs)
        print(f"   lit @0x{BASE+o:08x}  loaded by: {s or '-'}")
