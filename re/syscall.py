#!/usr/bin/env python3
"""
fx-CG50 OS 3.80 syscall-table resolver.

The syscall dispatcher @0x80020070 is:
    mov.l #0x806A2014, r2     ; SYSCALL_TABLE base
    shll2 r0                  ; r0 = id*4   (r0 = syscall id, set by caller)
    mov.l @(r0,r2), r0        ; r0 = table[id]
    jmp  @r0
So syscall handler address = *(0x806A2014 + id*4).  ~8040 entries.

libfxcg (github.com/Jonimoose/libfxcg) documents the Prizm/fx-CG syscall numbers;
this lets us jump straight to any OS API impl. Usage:
    python syscall.py 0x25f          # resolve one id
    python syscall.py 0x100 0x110    # dump a range
"""
import struct
import sys

IMG = r"F:\ru\myprojects\may\cg50\os\os_image\cg50_os_3.80.plain.bin"
TABLE = 0x806A2014
_data = open(IMG, "rb").read()


def u32(va):
    o = va & 0x0FFFFFFF
    return struct.unpack(">I", _data[o:o + 4])[0]


def resolve(sid):
    return u32(TABLE + sid * 4)


# A few confirmed/known libfxcg numbers (extend as we verify them).
KNOWN = {
    0x025F: "Bdisp_PutDisp_DD",
    0x0260: "Bdisp_PutDisp_DD_stripe",
    0x0270: "Bdisp_SetPoint_VRAM",
    0x0272: "Bdisp_AllClr_VRAM",
    0x0921: "Bdisp_PutDispArea_DD",
    0x090F: "GetKey",
    0x0EAB: "PutKeyCode",
    0x1E50: "memset",
    0x1163: "malloc",
    0x0E6B: "RTC_GetTicks",
}

if __name__ == "__main__":
    args = [int(a, 0) for a in sys.argv[1:]]
    if len(args) == 0:
        for sid, name in sorted(KNOWN.items()):
            print(f"  sc 0x{sid:04x} {name:24s} -> 0x{resolve(sid):08x}")
    elif len(args) == 1:
        sid = args[0]
        print(f"sc 0x{sid:04x} ({KNOWN.get(sid,'?')}) -> 0x{resolve(sid):08x}")
    else:
        for sid in range(args[0], args[1] + 1):
            print(f"  sc 0x{sid:04x} {KNOWN.get(sid,''):20s} -> 0x{resolve(sid):08x}")
