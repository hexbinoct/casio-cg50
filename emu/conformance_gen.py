#!/usr/bin/env python3
"""Cross-language CPU conformance suite (Python oracle -> frozen golden -> Go test).

Defines curated, edge-case-heavy instruction cases, runs each on the (reference)
Python CPU, and freezes inputs + expected outputs into emu/conformance.json. Both
emu/test_cpu.py and emu_go/conformance_test.go replay the SAME frozen cases against
their CPU and assert the outputs match. So porting/refactoring is validated by
`pytest` / `go test`, not by ad-hoc whole-program runs.

Run:  python emu/conformance_gen.py        # regenerate conformance.json
"""
import os, sys, json
sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))
from memory import Memory
from mmio import MMIOBus
from cpu import CPU

OUT = os.path.join(os.path.dirname(__file__), "conformance.json")
CODE = 0x8C001000
DATA = 0x8C010000
VBR = 0x8C002000

# ---- SH-4 instruction encoders (16-bit) ----
def mov_i(n, i):   return 0xE000 | (n << 8) | (i & 0xFF)
def mov(m, n):     return 0x6003 | (n << 8) | (m << 4)
def add(m, n):     return 0x300C | (n << 8) | (m << 4)
def addi(n, i):    return 0x7000 | (n << 8) | (i & 0xFF)
def addc(m, n):    return 0x300E | (n << 8) | (m << 4)
def addv(m, n):    return 0x300F | (n << 8) | (m << 4)
def sub(m, n):     return 0x3008 | (n << 8) | (m << 4)
def subc(m, n):    return 0x300A | (n << 8) | (m << 4)
def subv(m, n):    return 0x300B | (n << 8) | (m << 4)
def neg(m, n):     return 0x600B | (n << 8) | (m << 4)
def negc(m, n):    return 0x600A | (n << 8) | (m << 4)
def div0s(m, n):   return 0x2007 | (n << 8) | (m << 4)
DIV0U = 0x0019
def div1(m, n):    return 0x3004 | (n << 8) | (m << 4)
def cmpeq(m, n):   return 0x3000 | (n << 8) | (m << 4)
def cmphs(m, n):   return 0x3002 | (n << 8) | (m << 4)
def cmpge(m, n):   return 0x3003 | (n << 8) | (m << 4)
def cmphi(m, n):   return 0x3006 | (n << 8) | (m << 4)
def cmpgt(m, n):   return 0x3007 | (n << 8) | (m << 4)
def cmppz(n):      return 0x4011 | (n << 8)
def cmppl(n):      return 0x4015 | (n << 8)
def cmpstr(m, n):  return 0x200C | (n << 8) | (m << 4)
def cmpim(i):      return 0x8800 | (i & 0xFF)
def tst(m, n):     return 0x2008 | (n << 8) | (m << 4)
def and_(m, n):    return 0x2009 | (n << 8) | (m << 4)
def or_(m, n):     return 0x200B | (n << 8) | (m << 4)
def xor(m, n):     return 0x200A | (n << 8) | (m << 4)
def shll(n):       return 0x4000 | (n << 8)
def shlr(n):       return 0x4001 | (n << 8)
def shal(n):       return 0x4020 | (n << 8)
def shar(n):       return 0x4021 | (n << 8)
def shll2(n):      return 0x4008 | (n << 8)
def shlr2(n):      return 0x4009 | (n << 8)
def shll8(n):      return 0x4018 | (n << 8)
def shlr8(n):      return 0x4019 | (n << 8)
def shll16(n):     return 0x4028 | (n << 8)
def shlr16(n):     return 0x4029 | (n << 8)
def rotl(n):       return 0x4004 | (n << 8)
def rotr(n):       return 0x4005 | (n << 8)
def rotcl(n):      return 0x4024 | (n << 8)
def rotcr(n):      return 0x4025 | (n << 8)
def shad(m, n):    return 0x400C | (n << 8) | (m << 4)
def shld(m, n):    return 0x400D | (n << 8) | (m << 4)
def dt(n):         return 0x4010 | (n << 8)
def movt(n):       return 0x0029 | (n << 8)
SETT = 0x0018
CLRT = 0x0008
def extub(m, n):   return 0x600C | (n << 8) | (m << 4)
def extuw(m, n):   return 0x600D | (n << 8) | (m << 4)
def extsb(m, n):   return 0x600E | (n << 8) | (m << 4)
def extsw(m, n):   return 0x600F | (n << 8) | (m << 4)
def swapb(m, n):   return 0x6008 | (n << 8) | (m << 4)
def swapw(m, n):   return 0x6009 | (n << 8) | (m << 4)
def xtrct(m, n):   return 0x200D | (n << 8) | (m << 4)
def not_(m, n):    return 0x6007 | (n << 8) | (m << 4)
def mull(m, n):    return 0x0007 | (n << 8) | (m << 4)
def mulsw(m, n):   return 0x200F | (n << 8) | (m << 4)
def muluw(m, n):   return 0x200E | (n << 8) | (m << 4)
def dmuls(m, n):   return 0x300D | (n << 8) | (m << 4)
def dmulu(m, n):   return 0x3005 | (n << 8) | (m << 4)
def sts_macl(n):   return 0x001A | (n << 8)
def sts_mach(n):   return 0x000A | (n << 8)
def sts_pr(n):     return 0x002A | (n << 8)
def ldc_sr(n):     return 0x400E | (n << 8)
def stc_sr(n):     return 0x0002 | (n << 8)
def ldc_bank(n, b):return 0x408E | (n << 8) | (b << 4)   # ldc Rn,Rb_BANK
def stc_bank(b, n):return 0x0082 | (n << 8) | (b << 4)   # stc Rb_BANK,Rn
def movb_ld(m, n): return 0x6000 | (n << 8) | (m << 4)
def movw_ld(m, n): return 0x6001 | (n << 8) | (m << 4)
def movl_ld(m, n): return 0x6002 | (n << 8) | (m << 4)
def movl_st(m, n): return 0x2002 | (n << 8) | (m << 4)
def bra(disp):     return 0xA000 | (disp & 0xFFF)
def bsr(disp):     return 0xB000 | (disp & 0xFFF)
def bt(disp):      return 0x8900 | (disp & 0xFF)
def bf(disp):      return 0x8B00 | (disp & 0xFF)
def bts(disp):     return 0x8D00 | (disp & 0xFF)
def bfs(disp):     return 0x8F00 | (disp & 0xFF)
def jmp(n):        return 0x402B | (n << 8)
def jsr(n):        return 0x400B | (n << 8)
RTS = 0x000B
NOP = 0x0009
def tas(n):        return 0x401B | (n << 8)

U = 0xFFFFFFFF
SR_T = 1


def case(name, code, steps=1, setup=None, data=None, check=None, irq=None, pc=CODE):
    return {"name": name, "code": code, "steps": steps, "setup": setup or {},
            "data": data or {}, "check": check or [], "irq": irq, "pc": pc}


def build_cases():
    C = []
    # --- carries / overflow ---
    C.append(case("addc_carry_out", [addc(1, 0)],
                  setup={"r": reg(r0=0xFFFFFFFF, r1=1), "sr": 0}))           # 0xFFFFFFFF+1 -> 0, T=1
    C.append(case("addc_carry_in", [addc(1, 0)],
                  setup={"r": reg(r0=1, r1=1), "sr": SR_T}))                  # 1+1+T -> 3, T=0
    C.append(case("subc_borrow", [subc(1, 0)],
                  setup={"r": reg(r0=0, r1=1), "sr": 0}))                     # 0-1 -> 0xFFFFFFFF, T=1
    C.append(case("subc_borrow_in", [subc(1, 0)],
                  setup={"r": reg(r0=5, r1=2), "sr": SR_T}))                  # 5-2-1
    C.append(case("addv_overflow", [addv(1, 0)],
                  setup={"r": reg(r0=0x7FFFFFFF, r1=1), "sr": 0}))            # INT_MAX+1 -> overflow T=1
    C.append(case("addv_no_overflow", [addv(1, 0)],
                  setup={"r": reg(r0=0x7FFFFFFE, r1=1), "sr": 0}))
    C.append(case("subv_overflow", [subv(1, 0)],
                  setup={"r": reg(r0=0x80000000, r1=1), "sr": 0}))           # INT_MIN-1 -> overflow
    C.append(case("negc", [negc(1, 0)], setup={"r": reg(r1=1), "sr": 0}))     # 0-1-0
    C.append(case("negc_with_t", [negc(1, 0)], setup={"r": reg(r1=0), "sr": SR_T}))

    # --- signed/unsigned compares straddling 0x80000000 ---
    big = 0x80000000
    C.append(case("cmphs_unsigned", [cmphs(1, 0)], setup={"r": reg(r0=big, r1=1)}))   # big>=1 unsigned T=1
    C.append(case("cmpge_signed", [cmpge(1, 0)], setup={"r": reg(r0=big, r1=1)}))     # big(-)<1 signed T=0
    C.append(case("cmphi_eq", [cmphi(1, 0)], setup={"r": reg(r0=5, r1=5)}))           # not strictly > T=0
    C.append(case("cmpgt_signed", [cmpgt(1, 0)], setup={"r": reg(r0=1, r1=U)}))       # 1 > -1 signed T=1
    C.append(case("cmppz_neg", [cmppz(0)], setup={"r": reg(r0=big)}))                 # <0 -> T=0
    C.append(case("cmppl_zero", [cmppl(0)], setup={"r": reg(r0=0)}))                  # not >0 -> T=0
    C.append(case("cmpstr_match", [cmpstr(1, 0)], setup={"r": reg(r0=0x11223344, r1=0x55663377)}))  # byte 0 differs?
    C.append(case("cmpim", [cmpim(0xFF)], setup={"r": reg(r0=0xFFFFFFFF)}))           # r0 == sext(0xFF)=-1 T=1

    # --- shifts ---
    C.append(case("shar_neg", [shar(0)], setup={"r": reg(r0=0x80000001)}))           # arith >>1 keeps sign, T=1
    C.append(case("shlr_lsb", [shlr(0)], setup={"r": reg(r0=0x00000001)}))           # ->0 T=1
    C.append(case("rotcl", [rotcl(0)], setup={"r": reg(r0=0x80000000), "sr": SR_T})) # in T=1 -> bit0; out T=1
    C.append(case("rotcr", [rotcr(0)], setup={"r": reg(r0=0x00000001), "sr": 0}))    # out T=1, bit31<-Tin=0
    C.append(case("rotl_msb", [rotl(0)], setup={"r": reg(r0=0x80000000)}))
    C.append(case("rotr_lsb", [rotr(0)], setup={"r": reg(r0=0x00000001)}))
    C.append(case("shad_pos", [shad(1, 0)], setup={"r": reg(r0=0x00000001, r1=4)}))   # <<4
    C.append(case("shad_neg", [shad(1, 0)], setup={"r": reg(r0=0x80000000, r1=U-3)})) # >>4 arithmetic (r1=-4)
    C.append(case("shad_neg32", [shad(1, 0)], setup={"r": reg(r0=0x80000000, r1=0xFFFFFFE0)}))  # -32 -> >>31 arith
    C.append(case("shld_pos", [shld(1, 0)], setup={"r": reg(r0=0x00000001, r1=8)}))
    C.append(case("shld_neg", [shld(1, 0)], setup={"r": reg(r0=0x80000000, r1=U-3)})) # logical >>4
    C.append(case("shld_neg32", [shld(1, 0)], setup={"r": reg(r0=0xFFFFFFFF, r1=0xFFFFFFE0)}))  # -32 -> 0

    # --- multiply (signed/unsigned) ---
    C.append(case("mull_neg", [mull(1, 0), sts_macl(2)],
                  setup={"r": reg(r0=U, r1=2)}, steps=2))                              # -1 * 2 low32
    C.append(case("dmuls_neg", [dmuls(1, 0), sts_mach(2), sts_macl(3)],
                  setup={"r": reg(r0=U, r1=U)}, steps=3))                              # -1 * -1 = 1
    C.append(case("dmulu_big", [dmulu(1, 0), sts_mach(2), sts_macl(3)],
                  setup={"r": reg(r0=0xFFFFFFFF, r1=0xFFFFFFFF)}, steps=3))
    C.append(case("mulsw_neg", [mulsw(1, 0), sts_macl(2)],
                  setup={"r": reg(r0=0xFFFF, r1=0x0002)}, steps=2))                    # (-1)*2
    C.append(case("muluw", [muluw(1, 0), sts_macl(2)],
                  setup={"r": reg(r0=0xFFFF, r1=0x0002)}, steps=2))

    # --- div1 conformance via the classic unsigned-divide skeleton (exercises
    #     div1 x32 with evolving Q/M/T; we only assert Go==Python, not the quotient) ---
    code = [DIV0U]
    for _ in range(32):
        code += [rotcl(1), div1(0, 2)]
    # set up: r0=divisor, r1=dividend(lo), r2=0(workspace upper). result quotient in r1.
    C.append(case("udiv_17_by_5", code,
                  setup={"r": reg(r0=5, r1=17, r2=0)}, steps=len(code)))

    # --- sign-extension loads ---
    C.append(case("movb_ld_neg", [movb_ld(1, 0)],
                  setup={"r": reg(r1=DATA)}, data={str(DATA): 0x80000000}))            # byte 0x80 -> 0xFFFFFF80
    C.append(case("movw_ld_neg", [movw_ld(1, 0)],
                  setup={"r": reg(r1=DATA)}, data={str(DATA): 0x8123FFFF}))            # halfword 0x8123 -> sext
    C.append(case("extsb", [extsb(1, 0)], setup={"r": reg(r1=0x000000FF)}))
    C.append(case("extub", [extub(1, 0)], setup={"r": reg(r1=0xFFFFFFFF)}))
    C.append(case("extsw", [extsw(1, 0)], setup={"r": reg(r1=0x0000FFFF)}))
    C.append(case("swapb", [swapb(1, 0)], setup={"r": reg(r1=0x11223344)}))
    C.append(case("swapw", [swapw(1, 0)], setup={"r": reg(r1=0x11223344)}))
    C.append(case("xtrct", [xtrct(1, 0)], setup={"r": reg(r0=0xAAAABBBB, r1=0xCCCCDDDD)}))
    C.append(case("not", [not_(1, 0)], setup={"r": reg(r1=0x0F0F0F0F)}))
    C.append(case("dt_to_zero", [dt(0)], setup={"r": reg(r0=1)}))                      # ->0, T=1
    C.append(case("dt_nonzero", [dt(0)], setup={"r": reg(r0=5)}))

    # --- store to memory (verify write path & byte order) ---
    C.append(case("movl_store", [movl_st(1, 0)],
                  setup={"r": reg(r0=DATA, r1=0xDEADBEEF)}, check=[DATA]))

    # --- SR register-bank swap on ldc Rn,SR ---
    # start RB=0, r0..r7 = A; rbank1 = B; load SR with RB=1 -> active becomes B.
    C.append(case("sr_bank_swap", [ldc_sr(8)],
                  setup={"r": reg(r0=0xA0, r1=0xA1, r8=0x20000000 | 0x40000000),       # SR with RB|MD
                         "rbank1": [0xB0, 0xB1, 0, 0, 0, 0, 0, 0]}))
    C.append(case("stc_bank", [stc_bank(3, 0)],
                  setup={"r": reg(), "rbank1": [0, 0, 0, 0xBEEF, 0, 0, 0, 0]}))         # r0 <- rbank1[3]

    # --- delayed branch executes the delay slot ---
    # bra +0 over the next; delay slot adds 1 to r0; after, pc lands at target.
    C.append(case("bsr_delay_slot", [bsr(2), addi(0, 1), addi(0, 0x10), addi(0, 0x20)],
                  setup={"r": reg(r0=0)}, steps=1))   # 1 step: bsr + its delay slot; check r0,pr,pc
    C.append(case("bt_taken", [bt(1), addi(0, 0x10), addi(0, 0x20)],
                  setup={"r": reg(r0=0), "sr": SR_T}, steps=1))   # bt taken skips next (non-delayed)
    C.append(case("bf_not_taken", [bf(1), addi(0, 0x10)],
                  setup={"r": reg(r0=0), "sr": SR_T}, steps=2))   # T=1 -> bf falls through to addi

    # --- interrupt entry: pending IRQ, BL=0 -> vector to VBR+0x600 ---
    C.append(case("irq_entry", [NOP], steps=1,
                  setup={"r": reg(r0=0x11), "rbank1": [0x99, 0, 0, 0, 0, 0, 0, 0],
                         "sr": 0x00000000, "vbr": VBR},   # BL=0, IMASK=0, RB=0
                  data={str(VBR + 0x600): (NOP << 16) | NOP},
                  irq=[0x560, 8]))   # (intevt, level)

    return C


def div0u():
    return DIV0U


def reg(**kw):
    r = [0] * 16
    for k, v in kw.items():
        r[int(k[1:])] = v & U
    return r


def replay(case):
    mmio = MMIOBus(log=False)
    mem = Memory(b"", mmio)
    cpu = CPU(mem)
    s = case["setup"]
    cpu.r = list(s.get("r", [0] * 16))
    cpu.rbank1 = list(s.get("rbank1", [0] * 8))
    cpu._sr = s.get("sr", 0) & U
    cpu.pr = s.get("pr", 0); cpu.gbr = s.get("gbr", 0); cpu.vbr = s.get("vbr", 0)
    cpu.ssr = s.get("ssr", 0); cpu.spc = s.get("spc", 0)
    cpu.mach = s.get("mach", 0); cpu.macl = s.get("macl", 0)
    cpu.fpul = s.get("fpul", 0); cpu.fpscr = s.get("fpscr", 0)
    pc = case["pc"]
    for i, opc in enumerate(case["code"]):
        mem.w16(pc + 2 * i, opc)
    for addr, val in case["data"].items():
        mem.w32(int(addr), val & U)
    if case["irq"]:
        cpu.raise_irq(case["irq"][0], case["irq"][1])
    cpu.cycles = 0
    cpu.pc = pc
    for _ in range(case["steps"]):
        cpu.step()
    return state(cpu, mem, case["check"])


def state(cpu, mem, check):
    return {
        "pc": cpu.pc & U, "sr": cpu.sr & U,
        "r": [x & U for x in cpu.r], "rbank1": [x & U for x in cpu.rbank1],
        "pr": cpu.pr & U, "gbr": cpu.gbr & U, "vbr": cpu.vbr & U,
        "ssr": cpu.ssr & U, "spc": cpu.spc & U,
        "mach": cpu.mach & U, "macl": cpu.macl & U,
        "data": {str(a): mem.r32(a) for a in check},
    }


def main():
    cases = build_cases()
    out = []
    for c in cases:
        try:
            exp = replay(c)
        except Exception as e:
            print(f"case {c['name']!r} failed: {type(e).__name__}: {e}")
            raise
        out.append({"name": c["name"], "code": c["code"], "steps": c["steps"],
                    "setup": c["setup"], "data": c["data"], "check": c["check"],
                    "irq": c["irq"], "pc": c["pc"], "expect": exp})
    with open(OUT, "w") as f:
        json.dump({"version": 1, "cases": out}, f, indent=1)
    print(f"wrote {OUT}: {len(out)} conformance cases")


if __name__ == "__main__":
    main()
