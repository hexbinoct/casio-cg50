#!/usr/bin/env python3
"""
Formatter probe (cont.18): run the result formatter FUN_800fc5a4 from the captured
snapshot (emu_go/fmt_snapshot.bin) in the Python reference CPU and observe what it
produces, so we can answer: does the formatter ITSELF emit "0" (config/data bug), or
does it build a correct display object that a LATER renderer mis-draws?

We:
  - load the full machine snapshot (regs + dram/ilram/ocram),
  - dump the formatter's inputs (r4 = value-BCD ptr, r5/r6) and its literal pool
    (the DAT_800fc7xx/8xx pointers + constants it indexes),
  - run from pc=0x800fc5a4 until it RETURNS (pc == entry PR with stack unwound),
  - log every memory WRITE the formatter makes into the output display object, and
  - dump the output object before/after + the return value.

Run:  python re/fmt_probe.py
"""
import os, sys, struct

HERE = os.path.dirname(__file__)
sys.path.insert(0, os.path.join(HERE, "..", "emu"))
sys.path.insert(0, HERE)

from memory import Memory, DRAM_SIZE, ILRAM_SIZE, OCRAM_SIZE
from mmio import MMIOBus
from cpu import CPU
try:
    import sh4dis
    def disasm(op, pc): return sh4dis.decode(op, pc)
except Exception:
    def disasm(op, pc): return f"0x{op:04x}"

FLASH = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")
SNAP = os.path.join(HERE, "..", "emu_go", "fmt_snapshot.bin")
FMT_ENTRY = 0x800fc5a4


def build():
    image = open(FLASH, "rb").read()
    mmio = MMIOBus(log=False)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    with open(SNAP, "rb") as f:
        blob = f.read()
    regs = struct.unpack_from("<36I", blob, 0)
    off = 36 * 4
    cpu.r = list(regs[0:16])
    cpu.rbank1 = list(regs[16:24])
    (cpu.pc, cpu.pr, cpu.gbr, cpu.vbr, cpu.ssr, cpu.spc, cpu.sgr,
     cpu.mach, cpu.macl, cpu.fpul, cpu.fpscr, sr) = regs[24:36]
    cpu._sr = sr
    mem.dram[:] = blob[off:off + DRAM_SIZE]; off += DRAM_SIZE
    mem.ilram[:] = blob[off:off + ILRAM_SIZE]; off += ILRAM_SIZE
    mem.ocram[:] = blob[off:off + OCRAM_SIZE]; off += OCRAM_SIZE
    return cpu, mem


def hexdump(mem, base, n, label):
    print(f"  {label} @0x{base:08x}:")
    for r in range(0, n, 16):
        bs = [mem.r8(base + r + i) for i in range(min(16, n - r))]
        hexs = " ".join(f"{b:02x}" for b in bs)
        asc = "".join(chr(b) if 0x20 <= b < 0x7f else "." for b in bs)
        print(f"    0x{base + r:08x}: {hexs:<48} |{asc}|")


def main():
    cpu, mem = build()
    print(f"snapshot: pc=0x{cpu.pc:08x} pr=0x{cpu.pr:08x} sp(r15)=0x{cpu.r[15]:08x}")
    print(f"args: r4=0x{cpu.r[4]:08x} r5=0x{cpu.r[5]:08x} r6=0x{cpu.r[6]:08x} r7=0x{cpu.r[7]:08x}")
    hexdump(mem, cpu.r[4], 16, "value BCD (param_1 @r4)")

    print("\nliteral pool (longs) 0x800fc720..0x800fcb50:")
    for a in list(range(0x800fc720, 0x800fc844, 4)) + list(range(0x800fcb28, 0x800fcb50, 4)):
        v = mem.r32(a)
        tag = ""
        if 0x80000000 <= v < 0x80800000:
            tag = " -> code/data"
        elif v < 0x100:
            tag = " (small const/offset)"
        print(f"    [0x{a:08x}] = 0x{v:08x}{tag}")

    scratch = mem.r32(0x800fc828)          # 0x8c08b8a0 attr-scratch
    dispobj = cpu.r[5]                      # param_2 = the display object actually filled/rendered
    print(f"\nscratch DAT_800fc828 -> 0x{scratch:08x}; display object param_2(r5) -> 0x{dispobj:08x}")
    hexdump(mem, dispobj, 32, "display object (param_2) BEFORE")

    # ---- run formatter to return; hook the BCD->digits converter calls ----
    entry_pr = cpu.pr
    entry_sp = cpu.r[15]
    CONV = 0x8028dd38           # PTR_FUN_800fc840 = BCD->decimal-digits converter
    CALL_SITES = {0x800fc648, 0x800fc676, 0x800fc694}  # jsr sites that call CONV
    do_lo, do_hi = dispobj & 0x1FFFFFFF, (dispobj + 0x20) & 0x1FFFFFFF
    dwrites = []
    orig_write = mem.write
    def traced_write(va, size, val):
        if do_lo <= (va & 0x1FFFFFFF) < do_hi:
            dwrites.append((cpu.pc, va, size, val))
        orig_write(va, size, val)
    mem.write = traced_write

    # interesting jsr sites: BCD->digits converter + the general-number-branch helpers
    SITES = {
        0x800fc648: "CONV(fc840)", 0x800fc676: "CONV(fc840)", 0x800fc694: "CONV(fc840)",
        0x800fc8de: "fcb34", 0x800fc8ec: "fcb38(99e,9a0)", 0x800fc8fc: "fcb3c",
        0x800fc906: "fcb38(99e,0xd)", 0x800fc928: "fcb40", 0x800fc92e: "fcb40",
        0x800fc944: "fcb40(glyphcnt?)", 0x800fc950: "fcb40(glyphcnt?)",
    }
    # FUN_8004c21a (Norm renderer) value-decode tracer: B = frame base after prologue.
    c21a_B = None
    c21a_dumps = []
    def grab(label, addr, n):
        bs = [mem.r8((addr + i) & 0xFFFFFFFF) for i in range(n)]
        c21a_dumps.append((label, addr, " ".join(f"{b:02x}" for b in bs)))

    pending = None
    evlog = []
    steps = 0
    MAX = 5_000_000
    while steps < MAX:
        if steps > 0 and cpu.pc == entry_pr and cpu.r[15] >= entry_sp:
            break
        pc = cpu.pc
        if pc == 0x8004c220:
            c21a_B = cpu.r[15]
        if c21a_B is not None:
            if pc == 0x8004c25a:    # after 24-byte copy of the value into local_60
                grab("local_60 after copy   ", c21a_B, 24)
            elif pc == 0x8004c262:  # after PTR_FUN_8004c374 normalize
                grab("local_60 after norm   ", c21a_B, 24)
            elif pc == 0x8004c28c:  # after extractor PTR_FUN_8004c380
                grab("extractor out B+0x18  ", c21a_B + 0x18, 0x18)
            elif pc == 0x8004c402:  # at the FUN_8004c69c (digit emitter) call
                grab("local_60 @emit        ", c21a_B, 24)
        # FUN_8004c69c value working-copy tracer (auStack_2c = B2)
        if pc == 0x8004c6a0:
            c21a_B2 = cpu.r[15]
            globals()['_B2'] = c21a_B2
        _b2 = globals().get('_B2')
        if _b2 is not None:
            if pc == 0x8004c6b2:
                grab("c69c value after copy ", _b2, 24)
            elif pc == 0x8004c6b8:
                c21a_dumps.append(("c69c FUN_8004c654 ret ", 0, f"r0=0x{cpu.r[0]:08x}"))
                grab("c69c value AFTER c654 ", _b2, 24)
            elif pc == 0x8004c716:
                grab("c69c value AFTER c810 ", _b2, 24)
            elif pc == 0x8004c738:
                grab("c69c value BEFORE c85c", _b2, 24)
            elif pc == 0x8004c73c:
                grab("c69c value AFTER  c85c", _b2, 24)
            elif pc == 0x8004c772:
                grab("c69c value INTO c828  ", _b2, 24)
        if pc in SITES and pending is None:
            r5 = cpu.r[5]
            valbytes = " ".join(f"{mem.r8((r5 + i) & 0xFFFFFFFF):02x}" for i in range(8)) if 0x80000000 <= r5 else "-"
            pending = (pc, cpu.r[6])
            evlog.append(("CALL", pc, SITES[pc], cpu.r[4], cpu.r[5], cpu.r[6], valbytes))
        elif pending is not None and pc == (pending[0] + 4):
            buf = pending[1]
            ob = "-"
            if 0x80000000 <= buf:
                ob = " ".join(f"{mem.r8((buf + i) & 0xFFFFFFFF):02x}" for i in range(16))
            evlog.append(("RET ", pending[0], SITES[pending[0]], cpu.r[0], buf, 0, ob))
            pending = None
        op = mem.r16(cpu.pc)
        cpu.pc = (cpu.pc + 2) & 0xFFFFFFFF
        cpu.execute(op)
        cpu.cycles += 1
        steps += 1
    mem.write = orig_write

    print(f"\nformatter returned after {steps} steps: r0=0x{cpu.r[0]:08x} pc=0x{cpu.pc:08x}")
    print(f"\nNorm renderer FUN_8004c21a value-decode (B=0x{(c21a_B or 0):08x}):")
    for label, addr, hx in c21a_dumps:
        print(f"  {label} @0x{addr:08x}: {hx}")
    print("\nhelper calls (converter + general-number branch):")
    for tag, pc, name, a, b, c, by in evlog:
        if tag == "CALL":
            print(f"  CALL@0x{pc:08x} {name:18s} r4=0x{a:08x} r5=0x{b:08x} r6=0x{c:08x}  [r5]={by}")
        else:
            print(f"  RET @0x{pc:08x} {name:18s} r0=0x{a:08x} (={a if a < 0x80000000 else 0}) buf=0x{b:08x} {by}")
    print()
    hexdump(mem, 0x8c186290, 0x20, "converter output region (0x8c18629c)")
    hexdump(mem, 0x8c1866e0, 0x30, "fcb34/fcb40 formatted-string region (0x8c1866ef)")
    hexdump(mem, dispobj, 32, "display object (param_2) AFTER")
    print(f"\nwrites into display object 0x{dispobj:08x} ({len(dwrites)}):")
    for pc, va, size, val in dwrites[:40]:
        print(f"    pc=0x{pc:08x} {disasm(mem.r16(pc-2), pc-2):<22} -> [0x{va:08x}].{size} = 0x{val:0{size*2}x}")


if __name__ == "__main__":
    main()
