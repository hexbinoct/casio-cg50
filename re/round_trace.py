#!/usr/bin/env python3
"""
Round-leaf tracer (cont.18b): the value 98765 (BCD 10 49 87 65) is zeroed inside the
round-to-N-sig-digits leaf FUN_8005dc06 (=PTR_FUN_8004b494). That leaf does:
    de1c(temp)           PTR_FUN_8005de1c = 0x8005c6e8   init temp
    de2c(temp, output)   PTR_FUN_8005de2c = 0x80079dbc
    de24(temp, value)    PTR_FUN_8005de24 = 0x8005c84e   <- cont.17c's FUN_8005c84e
FUN_8005c84e does memset(value,0,0xc) then repacks temp->value ONLY if temp's header
matches (else returns leaving value zeroed, or tags it special). So we dump temp / value /
output at each sub-call boundary to see whether temp is malformed (root upstream in de1c/de2c)
or the repack path is wrongly skipped.

Run:  python re/round_trace.py
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
LEAF = 0x8005dc06
DE1C, DE2C, DE24 = 0x8005c6e8, 0x80079dbc, 0x8005c84e


def build():
    mmio = MMIOBus(log=False)
    mem = Memory(open(FLASH, "rb").read(), mmio)
    cpu = CPU(mem)
    blob = open(SNAP, "rb").read()
    regs = struct.unpack_from("<36I", blob, 0); off = 36 * 4
    cpu.r = list(regs[0:16]); cpu.rbank1 = list(regs[16:24])
    (cpu.pc, cpu.pr, cpu.gbr, cpu.vbr, cpu.ssr, cpu.spc, cpu.sgr,
     cpu.mach, cpu.macl, cpu.fpul, cpu.fpscr, sr) = regs[24:36]
    cpu._sr = sr
    mem.dram[:] = blob[off:off + DRAM_SIZE]; off += DRAM_SIZE
    mem.ilram[:] = blob[off:off + ILRAM_SIZE]; off += ILRAM_SIZE
    mem.ocram[:] = blob[off:off + OCRAM_SIZE]
    return cpu, mem


def hx(mem, a, n):
    if a is None or not (0x80000000 <= (a & 0xFFFFFFFF)):
        return "-"
    return " ".join(f"{mem.r8((a + i) & 0xFFFFFFFF):02x}" for i in range(n))


def main():
    cpu, mem = build()
    entry_pr = cpu.pr
    entry_sp = cpu.r[15]
    # 1) run formatter until we ENTER the round leaf FUN_8005dc06
    steps = 0
    while steps < 5_000_000:
        if cpu.pc == LEAF:
            break
        op = mem.r16(cpu.pc); cpu.pc = (cpu.pc + 2) & 0xFFFFFFFF; cpu.execute(op); cpu.cycles += 1
        steps += 1
    if cpu.pc != LEAF:
        print("never reached FUN_8005dc06"); return
    vptr, optr, prec = cpu.r[4], cpu.r[5], cpu.r[6]
    leaf_pr, leaf_sp = cpu.pr, cpu.r[15]
    print(f"ENTER FUN_8005dc06 @{steps} steps: r4(value)=0x{vptr:08x} r5(out)=0x{optr:08x} r6(prec)={prec}")
    print(f"  value : {hx(mem, vptr, 12)}")
    print(f"  output: {hx(mem, optr, 12)}")

    # 2) step the leaf; dump all scratch buffers at each sub-call boundary (calls run in
    #    the order de1c, c84e#1, de2c, c84e#2). Buffers: f44,f74,f90 scratch + value + output.
    bufs = [("f44", 0x8c185f44), ("f74", 0x8c185f74), ("f90", 0x8c185f90),
            ("val", vptr), ("out", optr)]
    def dumpbufs(tag):
        print(f"\n[{tag}]")
        for nm, a in bufs:
            print(f"    {nm} 0x{a:08x}: {hx(mem, a, 12)}")
    seq = [(DE1C, "BEFORE de1c (=leaf entry)"),
           (DE24, "BEFORE c84e#1 (=after de1c)"),
           (DE2C, "BEFORE de2c (=after c84e#1)"),
           (DE24, "BEFORE c84e#2 (=after de2c)  <-- c84e reads f90 as input")]
    # de2c=FUN_80079dbc internals: expand f78=0x80072c14, round f84=0x80073f38, copyback f80=0x80072c2e
    F78, F84, F80 = 0x80072c14, 0x80073f38, 0x80072c2e
    as24 = [None]
    si = 0
    lsteps = 0
    while lsteps < 2_000_000:
        pc = cpu.pc
        if si < len(seq) and pc == seq[si][0]:
            print(f"  call @0x{pc:08x} r4=0x{cpu.r[4]:08x} r5=0x{cpu.r[5]:08x} r6=0x{cpu.r[6]:08x}")
            dumpbufs(seq[si][1])
            si += 1
        # capture r11 (value word0) right after load, at the jsr, and after it returns
        if pc in (0x80073f64, 0x80073f6c, 0x80073f70):
            tgt = mem.r32(0x80074014)
            print(f"  [r11watch] pc=0x{pc:08x} r4=0x{cpu.r[4]:08x} r11=0x{cpu.r[11]:08x} "
                  f"(*0x80074014=0x{tgt:08x}) [buf0]=0x{mem.r32(cpu.r[4] if pc!=0x80073f70 else as24[0] or cpu.r[4]):08x}")
        # FUN_80073f38 mask-round internals: capture mask + value words at key PCs
        if pc in (0x80073f72, 0x80073f7c, 0x80073f82, 0x80073f84):
            print(f"  [maskround] pc=0x{pc:08x} r7=0x{cpu.r[7]:08x} r8=0x{cpu.r[8]:08x} r9=0x{cpu.r[9]:08x} "
                  f"r10=0x{cpu.r[10]:08x} r11=0x{cpu.r[11]:08x} r12=0x{cpu.r[12]:08x} r13=0x{cpu.r[13]:08x}")
        if pc == F78 and as24[0] is None:
            as24[0] = cpu.r[5]
            print(f"\n  >> de2c.expand f78 @0x{pc:08x}: in f90=0x{cpu.r[4]:08x} buf=0x{cpu.r[5]:08x}")
            print(f"     f90 before expand: {hx(mem, cpu.r[4], 12)}")
        elif pc == F84 and as24[0] is not None:
            print(f"  >> de2c.ROUND  f84 @0x{pc:08x}: buf=0x{cpu.r[4]:08x} ndig=r5={cpu.r[5]}")
            print(f"     buf BEFORE round: {hx(mem, as24[0], 16)}")
        elif pc == F80 and as24[0] is not None:
            print(f"  >> de2c.copyback f80 @0x{pc:08x}: buf=0x{cpu.r[4]:08x} -> f90=0x{cpu.r[5]:08x}")
            print(f"     buf AFTER round : {hx(mem, as24[0], 16)}")
        if lsteps > 0 and cpu.pc == leaf_pr and cpu.r[15] >= leaf_sp:
            break
        op = mem.r16(cpu.pc); cpu.pc = (cpu.pc + 2) & 0xFFFFFFFF; cpu.execute(op); cpu.cycles += 1
        lsteps += 1
    dumpbufs(f"AFTER leaf returns ({lsteps} steps), r0=0x{cpu.r[0]:08x}")


if __name__ == "__main__":
    main()
