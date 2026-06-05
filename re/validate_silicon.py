#!/usr/bin/env python3
"""Validate the Python CPU oracle against REAL fx-CG50 (SH7305) silicon.

The conformance suite (emu/conformance.json) only checks Go==Python — it is blind to a
bug implemented identically in BOTH emulators (the exact concern from cont.18). This
script closes that gap: it replays the on-device CPU conformance ground truth captured by
the shtest add-in (os/devic_probes/alusweep_shtest/, PART B of the .md) through the Python
oracle and diffs against the HARDWARE results. Any mismatch = a real CPU-core bug.

Run:  python re/validate_silicon.py
Confirmed vectors should then be folded into emu/conformance_gen.py as silicon-anchored
cases so the Go port is held to hardware truth too.
"""
import os, sys
EMU = os.path.join(os.path.dirname(__file__), "..", "emu")
sys.path.insert(0, EMU)
sys.path.insert(0, os.path.dirname(__file__))
from memory import Memory
from mmio import MMIOBus
from cpu import CPU
import conformance_gen as G

CODE = 0x8C001000
DATA = 0x8C010000
U = 0xFFFFFFFF

_fails = []
_npass = 0


_bus = MMIOBus(log=False)
_mem = Memory(b"", _bus)
_cpu = CPU(_mem)


def run(code, r=None, sr=0, steps=None):
    """Execute `code` (list of 16-bit opcodes) from a reset CPU; return the CPU.
    Reuses one CPU/Memory (resetting arch state) so the ~200 cases run in well under
    a second instead of re-allocating 8 MB of DRAM per case."""
    cpu = _cpu
    cpu.r = list(r or [0] * 16)
    cpu._sr = sr & U
    cpu.mach = 0
    cpu.macl = 0
    cpu.pr = 0
    for i, opc in enumerate(code):
        _mem.w16(CODE + 2 * i, opc)
    cpu.pc = CODE
    for _ in range(steps if steps is not None else len(code)):
        cpu.step()
    return cpu


def regs(**kw):
    a = [0] * 16
    for k, v in kw.items():
        a[int(k[1:])] = v & U
    return a


def check(label, got, want):
    global _npass
    if got == want:
        _npass += 1
    else:
        _fails.append((label, got, want))


# ----------------------------------------------------------------------------
# PART B ground truth (transcribed from probe2-sweep-and-cpu-2026-06-05.md).
# ----------------------------------------------------------------------------

def t_addc():
    for a, b, tin, s, to in [
        (0xffffffff, 0x00000001, 0, 0x00000000, 1), (0xffffffff, 0x00000000, 1, 0x00000000, 1),
        (0x7fffffff, 0x00000001, 0, 0x80000000, 0), (0x00000000, 0x00000000, 1, 0x00000001, 0),
        (0x80000000, 0x80000000, 0, 0x00000000, 1), (0x12345678, 0x9abcdef0, 1, 0xacf13569, 0),
        (0xffffffff, 0xffffffff, 1, 0xffffffff, 1)]:
        c = run([G.addc(1, 0)], regs(r0=a, r1=b), tin)
        check(f"addc {a:08x}+{b:08x}+{tin}", (c.r[0], c.sr & 1), (s, to))


def t_subc():
    for a, b, tin, d, to in [
        (0xffffffff, 0x00000001, 0, 0xfffffffe, 0), (0xffffffff, 0x00000000, 1, 0xfffffffe, 0),
        (0x7fffffff, 0x00000001, 0, 0x7ffffffe, 0), (0x00000000, 0x00000000, 1, 0xffffffff, 1),
        (0x80000000, 0x80000000, 0, 0x00000000, 0), (0x12345678, 0x9abcdef0, 1, 0x77777787, 1),
        (0xffffffff, 0xffffffff, 1, 0xffffffff, 1)]:
        c = run([G.subc(1, 0)], regs(r0=a, r1=b), tin)
        check(f"subc {a:08x}-{b:08x}-{tin}", (c.r[0], c.sr & 1), (d, to))


def t_negc():
    # negc Rm,Rn: Rn = 0 - Rm - T.  negc(1,0): Rm=r1=a, result r0.
    for a, tin, n, to in [
        (0xffffffff, 0, 0x00000001, 1), (0xffffffff, 1, 0x00000000, 1), (0x7fffffff, 0, 0x80000001, 1),
        (0x00000000, 1, 0xffffffff, 1), (0x80000000, 0, 0x80000000, 1), (0x12345678, 1, 0xedcba987, 1),
        (0xffffffff, 1, 0x00000000, 1)]:
        c = run([G.negc(1, 0)], regs(r1=a), tin)
        check(f"negc -{a:08x}-{tin}", (c.r[0], c.sr & 1), (n, to))


def t_addv():
    for a, b, s, ov in [
        (0x7fffffff, 0x00000001, 0x80000000, 1), (0x80000000, 0xffffffff, 0x7fffffff, 1),
        (0x7fffffff, 0x7fffffff, 0xfffffffe, 1), (0x00000000, 0x00000000, 0x00000000, 0),
        (0x40000000, 0x40000000, 0x80000000, 1), (0x80000000, 0x80000000, 0x00000000, 1)]:
        c = run([G.addv(1, 0)], regs(r0=a, r1=b))
        check(f"addv {a:08x}+{b:08x}", (c.r[0], c.sr & 1), (s, ov))


def t_subv():
    for a, b, d, ov in [
        (0x7fffffff, 0x00000001, 0x7ffffffe, 0), (0x80000000, 0xffffffff, 0x80000001, 0),
        (0x7fffffff, 0x7fffffff, 0x00000000, 0), (0x00000000, 0x00000000, 0x00000000, 0),
        (0x40000000, 0x40000000, 0x00000000, 0), (0x80000000, 0x80000000, 0x00000000, 0)]:
        c = run([G.subv(1, 0)], regs(r0=a, r1=b))
        check(f"subv {a:08x}-{b:08x}", (c.r[0], c.sr & 1), (d, ov))


def t_rotcl():
    for a, tin, res, to in [
        (0x80000000, 0, 0x00000000, 1), (0x00000001, 1, 0x00000003, 0),
        (0x12345678, 1, 0x2468acf1, 0), (0xffffffff, 0, 0xfffffffe, 1)]:
        c = run([G.rotcl(0)], regs(r0=a), tin)
        check(f"rotcl {a:08x} T={tin}", (c.r[0], c.sr & 1), (res, to))


def t_rotcr():
    for a, tin, res, to in [
        (0x80000000, 0, 0x40000000, 0), (0x00000001, 1, 0x80000000, 1),
        (0x12345678, 1, 0x891a2b3c, 0), (0xffffffff, 0, 0x7fffffff, 1)]:
        c = run([G.rotcr(0)], regs(r0=a), tin)
        check(f"rotcr {a:08x} T={tin}", (c.r[0], c.sr & 1), (res, to))


# shad/shld: n given as signed; column order 4,-4,31,-31,1,-1,0,-32
_NS = [4, -4, 31, -31, 1, -1, 0, -32]

def t_shad():
    table = {
        0x12345678: [0x23456780, 0x01234567, 0x00000000, 0x00000000, 0x2468acf0, 0x091a2b3c, 0x12345678, 0x00000000],
        0x80000000: [0x00000000, 0xf8000000, 0x00000000, 0xffffffff, 0x00000000, 0xc0000000, 0x80000000, 0xffffffff],
        0x00000001: [0x00000010, 0x00000000, 0x80000000, 0x00000000, 0x00000002, 0x00000000, 0x00000001, 0x00000000],
        0xffffffff: [0xfffffff0, 0xffffffff, 0x80000000, 0xffffffff, 0xfffffffe, 0xffffffff, 0xffffffff, 0xffffffff],
    }
    for v, outs in table.items():
        for n, want in zip(_NS, outs):
            c = run([G.shad(1, 0)], regs(r0=v, r1=n & U))
            check(f"shad {v:08x} n={n}", c.r[0], want)


def t_shld():
    table = {
        0x12345678: [0x23456780, 0x01234567, 0x00000000, 0x00000000, 0x2468acf0, 0x091a2b3c, 0x12345678, 0x00000000],
        0x80000000: [0x00000000, 0x08000000, 0x00000000, 0x00000001, 0x00000000, 0x40000000, 0x80000000, 0x00000000],
        0x00000001: [0x00000010, 0x00000000, 0x80000000, 0x00000000, 0x00000002, 0x00000000, 0x00000001, 0x00000000],
        0xffffffff: [0xfffffff0, 0x0fffffff, 0x80000000, 0x00000001, 0xfffffffe, 0x7fffffff, 0xffffffff, 0x00000000],
    }
    for v, outs in table.items():
        for n, want in zip(_NS, outs):
            c = run([G.shld(1, 0)], regs(r0=v, r1=n & U))
            check(f"shld {v:08x} n={n}", c.r[0], want)


def t_dmulu():
    for a, b, mh, ml in [
        (0xffffffff, 0xffffffff, 0xfffffffe, 0x00000001), (0x12345678, 0x00000010, 0x00000001, 0x23456780),
        (0x7fffffff, 0x00000002, 0x00000000, 0xfffffffe), (0x00000002, 0x00000003, 0x00000000, 0x00000006),
        (0x80000000, 0x00000002, 0x00000001, 0x00000000)]:
        c = run([G.dmulu(1, 0)], regs(r0=a, r1=b))
        check(f"dmulu {a:08x}*{b:08x}", (c.mach & U, c.macl & U), (mh, ml))


def t_dmuls():
    for a, b, mh, ml in [
        (0xffffffff, 0xffffffff, 0x00000000, 0x00000001), (0x12345678, 0x00000010, 0x00000001, 0x23456780),
        (0x7fffffff, 0x00000002, 0x00000000, 0xfffffffe), (0x00000002, 0x00000003, 0x00000000, 0x00000006),
        (0x80000000, 0x00000002, 0xffffffff, 0x00000000)]:
        c = run([G.dmuls(1, 0)], regs(r0=a, r1=b))
        check(f"dmuls {a:08x}*{b:08x}", (c.mach & U, c.macl & U), (mh, ml))


def t_div1():
    # Classic SH4 unsigned 32/32 divide: div0u + 32x(rotcl r1; div1 r0,r2) + a FINAL
    # rotcl r1 to shift in the last quotient bit. r0=divisor, r1=dividend, r2=0 -> quotient
    # in r1. (Omitting the final rotcl yields floor(q/2) — that was a skeleton bug, not a
    # div1 bug.) Validates the div1 instruction via the quotient; the probe's own remainder
    # column is unreliable per its caveat, so we check the quotient only.
    code = [G.DIV0U]
    for _ in range(32):
        code += [G.rotcl(1), G.div1(0, 2)]
    code += [G.rotcl(1)]
    for dvd, dvs, q in [
        (0x00000064, 0x00000007, 0x0000000e), (0xffffffff, 0x00000003, 0x55555555),
        (0x000f4240, 0x000003e8, 0x000003e8), (0x00000007, 0x00000002, 0x00000003),
        (0x80000000, 0x00000003, 0x2aaaaaaa), (0x00003039, 0x00000001, 0x00003039),
        (0x00000000, 0x00000005, 0x00000000), (0x7fffffff, 0xffffffff, 0x00000000)]:
        c = run(code, regs(r0=dvs, r1=dvd, r2=0), steps=len(code))
        check(f"div1 {dvd:08x}/{dvs:08x}", c.r[1] & U, q)


def t_cmp():
    # eq hs ge hi gt str | pz pl | tst   (pz/pl on a; cmp(a,b) with a in Rn, b in Rm)
    rows = [
        (0x00000005, 0x00000005, [1, 1, 1, 0, 0, 1, 1, 1, 0]),
        (0x00000005, 0x00000007, [0, 0, 0, 0, 0, 1, 1, 1, 0]),
        (0xffffffff, 0x00000001, [0, 1, 0, 1, 0, 0, 0, 0, 0]),
        (0x80000000, 0x7fffffff, [0, 1, 0, 1, 0, 0, 0, 0, 1]),
        (0x00000003, 0x00000003, [1, 1, 1, 0, 0, 1, 1, 1, 0]),
        (0x00000000, 0x00000000, [1, 1, 1, 0, 0, 1, 1, 0, 1]),
        (0x41424344, 0x44434241, [0, 0, 0, 0, 0, 0, 1, 1, 0]),
        (0x00ff00ff, 0xff00ff00, [0, 0, 1, 0, 1, 0, 1, 1, 1]),
    ]
    ops = [("eq", G.cmpeq), ("hs", G.cmphs), ("ge", G.cmpge), ("hi", G.cmphi),
           ("gt", G.cmpgt), ("str", G.cmpstr)]
    for a, b, want in rows:
        for i, (nm, enc) in enumerate(ops):
            c = run([enc(1, 0)], regs(r0=a, r1=b))
            check(f"cmp/{nm} {a:08x},{b:08x}", c.sr & 1, want[i])
        c = run([G.cmppz(0)], regs(r0=a)); check(f"cmp/pz {a:08x}", c.sr & 1, want[6])
        c = run([G.cmppl(0)], regs(r0=a)); check(f"cmp/pl {a:08x}", c.sr & 1, want[7])
        c = run([G.tst(1, 0)], regs(r0=a, r1=b)); check(f"tst {a:08x},{b:08x}", c.sr & 1, want[8])


def t_macl():
    # MAC.L @Rm+,@Rn+ accumulates signed 32x32 products into MACH:MACL. The probe sums
    # 0x40000000^2 *2 + 0x10000^2 + 0x7FFFFFFF^2 (4 ops over [40000000,40000000,10000,
    # 7FFFFFFF], r0 and r1 both walking the array). S=0 = full 64-bit; S=1 saturates to
    # 48-bit signed (0x00007FFF_FFFFFFFF). Both checked against hardware.
    vals = [0x40000000, 0x40000000, 0x00010000, 0x7fffffff]
    code = [G.mac_l(1, 0)] * 4   # mac.l @R1+,@R0+
    for s_bit, mh, ml in [(0, 0x60000000, 0x00000001), (2, 0x00007fff, 0xffffffff)]:
        for i, v in enumerate(vals):
            _mem.w32(DATA + 4 * i, v)
        c = run(code, regs(r0=DATA, r1=DATA), s_bit)
        check(f"mac.l S={1 if s_bit else 0}", (c.mach & U, c.macl & U), (mh, ml))


def t_munge():
    a, b = 0x11223344, 0xaabbccdd
    c = run([G.swapb(1, 0)], regs(r1=a)); check("swap.b", c.r[0], 0x11224433)
    c = run([G.swapw(1, 0)], regs(r1=a)); check("swap.w", c.r[0], 0x33441122)
    c = run([G.xtrct(1, 0)], regs(r1=a, r0=b)); check("xtrct", c.r[0], 0x3344aabb)
    c = run([G.extub(1, 0)], regs(r1=a)); check("extu.b", c.r[0], 0x00000044)
    c = run([G.extsb(1, 0)], regs(r1=0xf0)); check("exts.b", c.r[0], 0xfffffff0)
    c = run([G.extuw(1, 0)], regs(r1=a)); check("extu.w", c.r[0], 0x00003344)
    c = run([G.extsw(1, 0)], regs(r1=0xf000)); check("exts.w", c.r[0], 0xfffff000)


def main():
    for fn in [t_addc, t_subc, t_negc, t_addv, t_subv, t_rotcl, t_rotcr, t_shad, t_shld,
               t_dmulu, t_dmuls, t_div1, t_macl, t_cmp, t_munge]:
        print(f"... {fn.__name__}", flush=True)
        fn()
    total = _npass + len(_fails)
    print(f"silicon validation: {_npass}/{total} oracle outputs match real hardware")
    if _fails:
        print(f"\n*** {len(_fails)} MISMATCH(es) — real CPU-core bug(s) vs silicon ***")
        for label, got, want in _fails:
            print(f"  {label}: oracle={got!r}  hardware={want!r}")
        sys.exit(1)
    print("ALL MATCH — the SH-4A core is faithful to silicon on these vectors.")


if __name__ == "__main__":
    main()
