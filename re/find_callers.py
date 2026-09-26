"""List call/branch sites targeting an address in os.bin (3.60 @0x80000000):
literal-pool loads (mov.l @(disp,pc)) of the address and bsr/bra with a matching
12-bit displacement. Usage: python re/find_callers.py 0x801e684c [more...]"""
import struct, sys
OS = "F:/ru/myprojects/may/cg50/os/flash_dump/os.bin"
BASE = 0x80000000
d = open(OS, "rb").read()
loads = {}
for pc in range(0, len(d) - 1, 2):
    op = (d[pc] << 8) | d[pc + 1]
    if op >> 12 == 0xD:
        loads.setdefault((pc & ~3) + 4 + (op & 0xFF) * 4, []).append(pc)
for a in sys.argv[1:]:
    tgt = int(a, 0)
    off = tgt - BASE
    print(f"== target 0x{tgt:08x}")
    pat = struct.pack(">I", tgt)
    for o in range(0, len(d) - 3, 2):
        if d[o:o+4] == pat:
            s = ", ".join(f"0x{BASE+p:08x}" for p in loads.get(o, []))
            print(f"  literal @0x{BASE+o:08x} loaded at: {s or '-'}")
    for pc in range(max(0, off - 0x1002), min(len(d) - 1, off + 0x1002), 2):
        op = (d[pc] << 8) | d[pc + 1]
        if op >> 12 in (0xA, 0xB):
            disp = op & 0xFFF
            if disp & 0x800:
                disp -= 0x1000
            if pc + 4 + disp * 2 == off:
                print(f"  {'bsr' if op>>12==0xB else 'bra'} at 0x{BASE+pc:08x}")
