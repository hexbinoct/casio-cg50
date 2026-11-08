#!/usr/bin/env python3
"""Build the data of the wiki's syscall reference (wiki/docs/os/syscall-reference.md).

Reads the OS 3.60 image (os/flash_dump/os.bin) and the OS 3.80 image
(os/os_image/cg50_os_3.80.plain.bin), both mapped at 0x80000000, plus the names gathered by
re/syscall_names.py (re/syscall_names.json) and the hand-written docs in re/syscall_docs.json
("Layer 2", optional), and writes wiki/docs/assets/syscalls360.json.

The JSON holds addresses, counts, names and our findings only, never firmware bytes, except
for at most one short message (<= 40 characters) per syscall that its code points at.

How each fact is derived (all of it is a heuristic over static code; nothing is executed):

- Table: the base is read from the trampoline at 0x80020070 (mov.l @(disp,pc),r2) in each
  image; entries are counted while they look like code pointers.
- Function extent: recursive descent from the entry. Conditional and unconditional branches
  inside [entry, next known function start) are followed; rts/rte end a path; jmp @Rn / braf
  with an unknown target end it too (the size is then flagged "uncertain"). Known function
  starts are all syscall targets plus every direct call target found (bsr, jsr/jmp through a
  register loaded from the literal pool). End = the furthest of the last instruction reached
  and the last literal-pool word the code reads (mov.l/mov.w @(disp,pc)), capped at the next
  known start.
- Same in 3.80: the 3.80 function is walked the same way. Identical bytes over the extent
  = "same". Otherwise both must have instructions at the same offsets, and each pair must be
  equal, except the position-dependent parts, which are masked: a bsr's displacement (the
  callee moved), a mova's, and a mov.l @(disp,pc)'s displacement when the two values it loads
  are equal or are addresses of the same kind (both in the OS image, both in RAM, both in
  on-chip RAM); a mov.w @(disp,pc) must load the same constant. Then "same, moved".
  Codes: 0 = identical bytes, 1 = same after masking, 2 = changed (same size),
  3 = changed (different size).
- Likely arguments: forward "must be written" data flow over the function's paths. A read of
  r4-r7 on a path where the register was not written first marks it as an argument. A call
  counts as reading the callee's arguments (those not yet written) and then clobbers r0-r7;
  tail jumps likewise. Iterated over the call graph until stable.
- Calls: bsr targets, and jsr/jmp/bsrf through a register whose value comes from the literal
  pool (or mov #imm / mova) earlier on the same path.
- Hardware: 32-bit literal-pool values in the function that fall in a peripheral's register
  range, mapped to a category (the ranges are the ones the wiki documents; USB and SPU bases
  are gint's names for the SH7305). Also the categories reached through calls (depth <= 2).
- Message: the first literal-pool value that points into the image at a NUL-terminated
  printable ASCII string of 4..40 characters.
Usage: python3 re/syscall_ref.py"""
import json
import os
import struct
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
IMG360 = os.path.join(ROOT, "os", "flash_dump", "os.bin")
IMG380 = os.path.join(ROOT, "os", "os_image", "cg50_os_3.80.plain.bin")
NAMES = os.path.join(HERE, "syscall_names.json")
DOCS = os.path.join(HERE, "syscall_docs.json")
OUT = os.path.join(ROOT, "wiki", "docs", "assets", "syscalls360.json")
TRAMP = 0x80020070

# ---------------------------------------------------------------------------------------------
# Instruction semantics: kind, registers read, registers written (bit masks over r0-r15)
K_NORM, K_INV, K_RTS, K_RTE, K_BRA, K_BSR, K_BT, K_BF, K_BTS, K_BFS, K_JMP, K_JSR, K_BRAF, \
    K_BSRF, K_MOVL, K_MOVW, K_MOVA, K_MOVI, K_MOV, K_ADDI = range(20)
DELAYED = {K_RTS, K_RTE, K_BRA, K_BSR, K_BTS, K_BFS, K_JMP, K_JSR, K_BRAF, K_BSRF}
BRANCHES = DELAYED | {K_BT, K_BF}
ARGS = 0xF0          # r4-r7
CLOBBER = 0xFF       # r0-r7: caller-saved in the SH calling convention


def _sem(x):
    n, m, lo4, lo8 = (x >> 8) & 15, (x >> 4) & 15, x & 15, x & 0xFF
    N, M, R0 = 1 << n, 1 << m, 1
    hi = x >> 12
    fixed = {0x0008: K_NORM, 0x0009: K_NORM, 0x0018: K_NORM, 0x0019: K_NORM, 0x001B: K_NORM,
             0x0028: K_NORM, 0x0038: K_NORM, 0x0048: K_NORM, 0x0058: K_NORM, 0x00AB: K_NORM}
    if x == 0x000B:
        return K_RTS, 0, 0
    if x == 0x002B:
        return K_RTE, 0, 0
    if x in fixed:
        return fixed[x], 0, 0
    if hi == 0x0:
        if lo8 in (0x02, 0x12, 0x22, 0x32, 0x42, 0x3A, 0xFA) or (lo8 & 0x8F) == 0x82:
            return K_NORM, 0, N                      # stc
        if lo8 == 0x03:
            return K_BSRF, N, 0
        if lo8 == 0x23:
            return K_BRAF, N, 0
        if lo8 in (0x29, 0x0A, 0x1A, 0x2A, 0x5A, 0x6A):
            return K_NORM, 0, N                      # movt, sts
        if lo8 in (0x83, 0x93, 0xA3, 0xB3, 0xD3, 0xE3):
            return K_NORM, N, 0                      # pref, ocb*, prefi, icbi
        if lo8 == 0xC3:
            return K_NORM, N | R0, 0                 # movca.l
        if lo8 == 0x63:
            return K_NORM, N, R0                     # movli.l @Rm,r0 (Rm in the n field)
        if lo8 == 0x73:
            return K_NORM, N | R0, 0                 # movco.l
        if lo4 in (4, 5, 6):
            return K_NORM, M | R0 | N, 0
        if lo4 == 7:
            return K_NORM, M | N, 0
        if lo4 in (0xC, 0xD, 0xE):
            return K_NORM, R0 | M, N
        if lo4 == 0xF:
            return K_NORM, M | N, M | N
        return K_INV, 0, 0
    if hi == 0x1:
        return K_NORM, M | N, 0
    if hi == 0x2:
        if lo4 in (0, 1, 2):
            return K_NORM, M | N, 0
        if lo4 in (4, 5, 6):
            return K_NORM, M | N, N
        if lo4 in (7, 8, 0xC, 0xE, 0xF):
            return K_NORM, M | N, 0
        if lo4 in (9, 0xA, 0xB, 0xD):
            return K_NORM, M | N, N
        return K_INV, 0, 0
    if hi == 0x3:
        if lo4 in (0, 2, 3, 5, 6, 7, 0xD):
            return K_NORM, M | N, 0
        if lo4 in (4, 8, 0xA, 0xB, 0xC, 0xE, 0xF):
            return K_NORM, M | N, N
        return K_INV, 0, 0
    if hi == 0x4:
        if lo8 in (0x00, 0x01, 0x04, 0x05, 0x08, 0x09, 0x10, 0x18, 0x19, 0x20, 0x21, 0x24,
                   0x25, 0x28, 0x29):
            return K_NORM, N, N
        if lo8 in (0x11, 0x15, 0x1B):
            return K_NORM, N, 0
        if lo8 in (0x0A, 0x1A, 0x2A, 0x5A, 0x6A, 0x0E, 0x1E, 0x2E, 0x3E, 0x4E, 0xFA, 0x3A) or \
                (lo8 & 0x8F) == 0x8E:
            return K_NORM, N, 0                      # lds/ldc
        if lo8 in (0x06, 0x07, 0x16, 0x17, 0x26, 0x27, 0x37, 0x47, 0x56, 0x66, 0xF6, 0x36) or \
                (lo8 & 0x8F) == 0x87:
            return K_NORM, N, N                      # lds.l/ldc.l @Rn+
        if lo8 in (0x02, 0x03, 0x12, 0x13, 0x22, 0x23, 0x33, 0x43, 0x52, 0x62, 0xF2, 0x32) or \
                (lo8 & 0x8F) == 0x83:
            return K_NORM, N, N                      # sts.l/stc.l @-Rn
        if lo8 == 0x0B:
            return K_JSR, N, 0
        if lo8 == 0x2B:
            return K_JMP, N, 0
        if lo8 == 0xA9:
            return K_NORM, N, R0                     # movua.l @Rm,r0
        if lo8 == 0xE9:
            return K_NORM, N, N | R0                 # movua.l @Rm+,r0
        if lo4 in (0xC, 0xD):
            return K_NORM, M | N, N                  # shad, shld
        if lo4 == 0xF:
            return K_NORM, M | N, M | N              # mac.w
        return K_INV, 0, 0
    if hi == 0x5:
        return K_NORM, M, N
    if hi == 0x6:
        if lo4 == 3:
            return K_MOV, M, N
        if lo4 in (4, 5, 6):
            return K_NORM, M, M | N
        return K_NORM, M, N
    if hi == 0x7:
        return K_ADDI, N, N
    if hi == 0x8:
        sub = n
        if sub in (0, 1):
            return K_NORM, R0 | M, 0
        if sub in (4, 5):
            return K_NORM, M, R0
        if sub == 8:
            return K_NORM, R0, 0
        return {9: (K_BT, 0, 0), 0xB: (K_BF, 0, 0), 0xD: (K_BTS, 0, 0),
                0xF: (K_BFS, 0, 0)}.get(sub, (K_INV, 0, 0))
    if hi == 0x9:
        return K_MOVW, 0, N
    if hi == 0xA:
        return K_BRA, 0, 0
    if hi == 0xB:
        return K_BSR, 0, 0
    if hi == 0xC:
        sub = n
        if sub in (0, 1, 2, 8, 0xC, 0xD, 0xE, 0xF):
            return K_NORM, R0, 0
        if sub == 3:
            return K_NORM, 0, 0                      # trapa (unused by the OS)
        if sub in (4, 5, 6):
            return K_NORM, 0, R0
        if sub == 7:
            return K_MOVA, 0, R0
        return K_NORM, R0, R0                        # and/xor/or #imm,r0
    if hi == 0xD:
        return K_MOVL, 0, N
    if hi == 0xE:
        return K_MOVI, 0, N
    return K_INV, 0, 0                               # 0xF: FPU / DSP, not in this OS


SEM = [_sem(x) for x in range(0x10000)]


def disp8(x):
    d = x & 0xFF
    return d - 256 if d & 0x80 else d


def disp12(x):
    d = x & 0xFFF
    return d - 0x1000 if d & 0x800 else d


# ---------------------------------------------------------------------------------------------
class Image:
    def __init__(self, path, label):
        self.label = label
        self.d = open(path, "rb").read()
        self.n = len(self.d)
        self.end = 0x80000000 + self.n

    def ok(self, va, k=2):
        o = va & 0x1FFFFFFF
        return (va >> 29) in (4, 5) and o + k <= self.n

    def u16(self, va):
        o = va & 0x1FFFFFFF
        return (self.d[o] << 8) | self.d[o + 1]

    def u32(self, va):
        o = va & 0x1FFFFFFF
        return struct.unpack_from(">I", self.d, o)[0]

    def table(self):
        for a in range(TRAMP, TRAMP + 0x10, 2):
            op = self.u16(a)
            if op & 0xFF00 == 0xD200:
                base = self.u32((a & ~3) + 4 + (op & 0xFF) * 4)
                n = 0
                while True:
                    v = self.u32(base + n * 4)
                    if not (0x80020000 <= v < self.end and v % 2 == 0):
                        return base, [self.u32(base + i * 4) for i in range(n)]
                    n += 1
        raise SystemExit(f"{self.label}: trampoline not found")

    def cstring(self, va):
        """A printable ASCII string of 4..40 characters at va, NUL-terminated, or None."""
        if not self.ok(va, 1):
            return None
        o = va & 0x1FFFFFFF
        end = self.d.find(b"\0", o, o + 42)
        if end < 0 or end - o < 4 or end - o > 40:
            return None
        s = self.d[o:end]
        if not all(0x20 <= c < 0x7F for c in s):
            return None
        if sum(chr(c).isalpha() for c in s) < 3:
            return None
        return s.decode("ascii")


class Func:
    __slots__ = ("entry", "bound", "insns", "lits", "calls", "tails", "events", "exposed",
                 "unresolved", "invalid", "end", "litvals", "switches")


def _consts_step(img, pc, x, consts, lits=None, litvals=None):
    """Track register constants through one non-branch instruction."""
    kind, R, W = SEM[x]
    n = (x >> 8) & 15
    if kind == K_MOVL:
        ea = (pc & ~3) + 4 + (x & 0xFF) * 4
        if img.ok(ea, 4):
            v = img.u32(ea)
            consts[n] = v
            if lits is not None:
                lits[ea] = 4
                litvals[pc] = v
        else:
            consts.pop(n, None)
    elif kind == K_MOVW:
        ea = pc + 4 + (x & 0xFF) * 2
        if img.ok(ea, 2):
            v = img.u16(ea)
            consts[n] = (v - 0x10000 if v & 0x8000 else v) & 0xFFFFFFFF
            if lits is not None:
                lits.setdefault(ea, 2)
        else:
            consts.pop(n, None)
    elif kind == K_MOVA:
        consts[0] = (pc & ~3) + 4 + (x & 0xFF) * 4
    elif kind == K_MOVI:
        consts[n] = disp8(x) & 0xFFFFFFFF
    elif kind == K_MOV:
        m = (x >> 4) & 15
        if m in consts:
            consts[n] = consts[m]
        else:
            consts.pop(n, None)
    elif kind == K_ADDI:
        if n in consts:
            consts[n] = (consts[n] + disp8(x)) & 0xFFFFFFFF
    elif W:
        for r in range(16):
            if W >> r & 1:
                consts.pop(r, None)


def _switch(img, pc, entry, bound):
    """braf r0 after `mova T,r0; mov.w @(r0,rI),r0`: the case targets read from table T.
    The table runs until the first case's code starts (or 256 entries). Returns (targets,
    table start, table end) or None."""
    if not (img.u16(pc) & 0xF0FF) == 0x0023:
        return None
    r = (img.u16(pc) >> 8) & 15
    mw, mv = img.u16(pc - 2), img.u16(pc - 4)
    if not (mw & 0xF00F) == 0x000D or (mw >> 8) & 15 != r or mv >> 8 != 0xC7:
        return None
    T = ((pc - 4) & ~3) + 4 + (mv & 0xFF) * 4
    base = pc + 4
    targets, i, lowest = [], 0, bound
    while i < 256:
        ea = T + 2 * i
        if ea >= lowest:
            break
        v = img.u16(ea)
        t = (base + (v - 0x10000 if v & 0x8000 else v)) & 0xFFFFFFFF
        if not (entry <= t < bound) or t & 1:
            break
        targets.append(t)
        if t > T:
            lowest = min(lowest, t)
        i += 1
    if not targets:
        return None
    return targets, T, T + 2 * len(targets)


def analyze(img, entry, bound):
    """Recursive descent over one function (see the module docstring).
    Phase 1 finds the instructions and basic-block leaders; phase 2 runs the data flow
    (must-written registers, register constants) over the blocks."""
    f = Func()
    f.entry, f.bound = entry, bound
    u16 = img.u16
    invalid = False

    def inside(a):
        return entry <= a < bound and img.ok(a)

    # ---- phase 1: control flow --------------------------------------------------------
    insns, leaders, succ = set(), {entry}, {}   # succ: branch pc -> list of intra targets
    switches = []                                # (table start, table end)
    work = [entry]
    while work:
        pc = work.pop()
        consts = {}
        while True:
            if not inside(pc) or pc in insns:
                break
            insns.add(pc)
            x = u16(pc)
            kind = SEM[x][0]
            if kind == K_INV:
                invalid = True
                break
            if kind not in BRANCHES:
                _consts_step(img, pc, x, consts)
                pc += 2
                continue
            if kind in (K_BT, K_BF):
                t = pc + 4 + disp8(x) * 2
                succ[pc] = [t, pc + 2]
                leaders.update((t, pc + 2))
                work.append(t)
                pc += 2
                continue
            slot = pc + 2
            if not inside(slot) or SEM[u16(slot)][0] in BRANCHES or SEM[u16(slot)][0] == K_INV:
                invalid = True
                break
            insns.add(slot)
            reg = consts.get((x >> 8) & 15)
            _consts_step(img, slot, u16(slot), consts)
            nxt = slot + 2
            if kind in (K_RTS, K_RTE):
                succ[pc] = []
                break
            if kind in (K_BTS, K_BFS):
                t = pc + 4 + disp8(x) * 2
                succ[pc] = [t, nxt]
                leaders.update((t, nxt))
                work.append(t)
                pc = nxt
                continue
            if kind in (K_BSR, K_JSR, K_BSRF):
                succ[pc] = [nxt]
                leaders.add(nxt)
                for r in range(8):
                    consts.pop(r, None)
                pc = nxt
                continue
            if kind == K_BRA:
                t = pc + 4 + disp12(x) * 2
            elif kind == K_JMP:
                t = reg
            else:                                    # braf
                sw = _switch(img, pc, entry, bound)
                if sw:
                    succ[pc] = sw[0]
                    leaders.update(sw[0])
                    work.extend(sw[0])
                    switches.append((sw[1], sw[2]))
                else:
                    succ[pc] = []
                break
            if t is not None and inside(t):
                succ[pc] = [t]
                leaders.add(t)
                pc = t
                continue
            succ[pc] = []
            break

    # ---- phase 2: data flow over blocks ----------------------------------------------
    lits, litvals = {}, {}
    call_at, tail_at = {}, {}      # pc -> (target or None, D)
    exposed = 0
    state = {entry: (0, {})}
    work = [entry]
    queued = {entry}

    def merge(t, D, consts):
        if t not in insns:
            return
        if t in state:
            oD, oc = state[t]
            nD = oD & D
            nc = {k: v for k, v in oc.items() if consts.get(k) == v}
            if nD == oD and len(nc) == len(oc):
                return
            state[t] = (nD, nc)
        else:
            state[t] = (D, dict(consts))
        if t not in queued:
            queued.add(t)
            work.append(t)

    while work:
        pc = work.pop()
        queued.discard(pc)
        D, c0 = state[pc]
        consts = dict(c0)
        first = True
        while True:
            if not first and pc in leaders:
                merge(pc, D, consts)
                break
            first = False
            if pc not in insns:
                if img.ok(pc) and not inside(pc):
                    tail_at[pc - 2] = (pc, D)       # ran into the next function
                break
            x = u16(pc)
            kind, R, W = SEM[x]
            exposed |= R & ~D & ARGS
            if kind not in BRANCHES:
                _consts_step(img, pc, x, consts, lits, litvals)
                D |= W
                pc += 2
                continue
            if kind in (K_BT, K_BF):
                for t in succ.get(pc, []):
                    if not inside(t) and img.ok(t):
                        tail_at[pc] = (t, D)
                    merge(t, D, consts)
                break
            reg = consts.get((x >> 8) & 15)
            slot = pc + 2
            if slot not in insns:
                break
            sx = u16(slot)
            exposed |= SEM[sx][1] & ~D & ARGS
            _consts_step(img, slot, sx, consts, lits, litvals)
            D |= SEM[sx][2]
            if kind in (K_BSR, K_JSR, K_BSRF):
                if kind == K_BSR:
                    t = pc + 4 + disp12(x) * 2
                elif kind == K_JSR:
                    t = reg
                else:
                    t = (pc + 4 + reg) & 0xFFFFFFFF if reg is not None else None
                if t is not None and not (img.ok(t) and t % 2 == 0):
                    t = None
                call_at[pc] = (t, D)
                D |= CLOBBER
                for r in range(8):
                    consts.pop(r, None)
                for t2 in succ.get(pc, []):
                    merge(t2, D, consts)
                break
            if kind in (K_BTS, K_BFS):
                t = pc + 4 + disp8(x) * 2
                if not inside(t) and img.ok(t):
                    tail_at[pc] = (t, D)
            if kind in (K_BRA, K_JMP):
                t = pc + 4 + disp12(x) * 2 if kind == K_BRA else reg
                if t is None:
                    tail_at[pc] = (None, D)
                elif not inside(t):
                    tail_at[pc] = (t if img.ok(t) and t % 2 == 0 else None, D)
            for t in succ.get(pc, []):
                merge(t, D, consts)
            break

    calls = {t for t, _ in call_at.values() if t is not None}
    tails = {t for t, _ in tail_at.values() if t is not None}
    f.events = [(t, D) for t, D in list(call_at.values()) + list(tail_at.values()) if t is not None]
    f.unresolved = any(t is None for t, _ in call_at.values()) or \
        any(t is None for t, _ in tail_at.values()) or \
        any(SEM[u16(p)][0] == K_BRAF and not succ.get(p) for p in succ)
    f.insns, f.lits, f.calls, f.tails = insns, lits, calls, tails
    f.exposed, f.invalid = exposed, invalid
    f.litvals = sorted(litvals.items())
    f.switches = switches
    code_end = max(insns) + 2 if insns else entry
    lit_end = max((a + w for a, w in lits.items() if a >= entry), default=entry)
    sw_end = max((e for _, e in switches), default=entry)
    f.end = min(max(code_end, lit_end, sw_end), bound)
    return f


def next_start(starts, a, cap=0x20000):
    import bisect
    i = bisect.bisect_right(starts, a)
    nxt = starts[i] if i < len(starts) else a + cap
    return min(nxt, a + cap)


def analyze_image(img, targets):
    """Pass A: syscall functions bounded by the next syscall target, to discover call
    targets. Pass B: the closure of everything they call, bounded by any known start."""
    sys_starts = sorted(set(targets))
    found = set()
    for a in sys_starts:
        f = analyze(img, a, next_start(sys_starts, a))
        found |= f.calls | f.tails
    starts = sorted(set(sys_starts) | {a for a in found if 0x80020000 <= a < img.end})
    funcs = {}
    queue = list(sys_starts)
    while queue:
        a = queue.pop()
        if a in funcs:
            continue
        f = funcs[a] = analyze(img, a, next_start(starts, a))
        for t in f.calls | f.tails:
            if t not in funcs and 0x80020000 <= t < img.end:
                queue.append(t)
    # arguments: propagate callees' arguments until stable
    args = {a: f.exposed for a, f in funcs.items()}
    for _ in range(20):
        changed = 0
        for a, f in funcs.items():
            v = args[a]
            for t, D in f.events:
                if t in args:
                    v |= args[t] & ~D & ARGS
            if v != args[a]:
                args[a] = v
                changed += 1
        if not changed:
            break
    return funcs, args


# ---------------------------------------------------------------------------------------------
def addr_class(v):
    """Kind of address, for masking literal-pool words between OS versions (None = not one)."""
    if 0x80000000 <= v < 0x81000000 or 0xA0000000 <= v < 0xA1000000:
        return "rom"
    if 0x88000000 <= v < 0x90000000 or 0xA8000000 <= v < 0xB0000000:
        return "ram"
    if 0xFD800000 <= v < 0xFD810000 or 0xFE200000 <= v < 0xFE400000 or 0xE5000000 <= v < 0xE5300000:
        return "onchip"
    return None


def compare(a, fa, b, fb):
    """0 identical bytes, 1 same code once position-dependent parts are masked,
    2 changed (same size), 3 changed (different size)."""
    sa, sb = fa.end - fa.entry, fb.end - fb.entry
    A, B = fa.entry, fb.entry
    if sa == sb and a.ok(A, sa) and b.ok(B, sb):
        oa, ob = A & 0x1FFFFFFF, B & 0x1FFFFFFF
        if a.d[oa:oa + sa] == b.d[ob:ob + sb]:
            return 0
    # same instructions at the same offsets?
    ia = sorted(x - A for x in fa.insns)
    ib = sorted(x - B for x in fb.insns)
    if ia != ib:
        return 2 if sa == sb else 3
    for off in ia:
        x, y = A + off, B + off
        p, q = a.u16(x), b.u16(y)
        if p == q and p >> 12 not in (0x9, 0xD):
            continue
        if p >> 12 != q >> 12:
            return 2 if sa == sb else 3
        hi = p >> 12
        if hi == 0xB:                                # bsr: the callee moved
            continue
        if p & 0xFF00 == 0xC700 and q & 0xFF00 == 0xC700:   # mova
            continue
        if hi == 0xD and p & 0x0F00 == q & 0x0F00:   # mov.l @(disp,pc),Rn: compare the values
            ea, eb = (x & ~3) + 4 + (p & 0xFF) * 4, (y & ~3) + 4 + (q & 0xFF) * 4
            if a.ok(ea, 4) and b.ok(eb, 4):
                u, v = a.u32(ea), b.u32(eb)
                if u == v:
                    continue
                c = addr_class(u)
                if c and c == addr_class(v):
                    continue
        if hi == 0x9 and p & 0x0F00 == q & 0x0F00:   # mov.w @(disp,pc),Rn: same constant
            ea, eb = x + 4 + (p & 0xFF) * 2, y + 4 + (q & 0xFF) * 2
            if a.ok(ea) and b.ok(eb) and a.u16(ea) == b.u16(eb):
                continue
        return 2 if sa == sb else 3
    return 1


# Hardware categories: key, label, wiki page (relative to the site root), ranges, alignment.
# A literal-pool value in a range (and aligned) counts as touching that hardware. Ranges are
# the ones the wiki documents; USB and SPU are gint's names for SH7305 blocks. The CPU-core
# ranges need 4-byte alignment so that masks such as 0xFF0000FF do not count.
CATS = [
    ("key", "Keyboard (KEYSC)", "peripherals/keyboard/", [(0xA44B0000, 0xA44B0100)], 1),
    ("lcd", "Display controller (R61524)", "peripherals/display/", [(0xB4000000, 0xB5000000)], 1),
    ("vram", "VRAM (0xAC000000)", "peripherals/display/", [(0xAC000000, 0xAC028800)], 1),
    ("dma", "DMA controller", "peripherals/display/", [(0xFE008000, 0xFE010000)], 1),
    ("bcd", "BCD ALU (0xA4CB0000)", "peripherals/bcd-alu/", [(0xA4CB0000, 0xA4CC0000)], 1),
    ("tmr", "Timers (TMU, ETMU, one-shot unit)", "peripherals/timers/",
     [(0xA4490000, 0xA4490100), (0xA44A0000, 0xA44A0100), (0xA44D0000, 0xA44D0100)], 1),
    ("rtc", "RTC", "peripherals/timers/", [(0xA413FEC0, 0xA413FF00)], 1),
    ("intc", "Interrupt controller", "peripherals/interrupts/",
     [(0xA4080000, 0xA4080100), (0xA4140000, 0xA4140100)], 1),
    ("cpg", "Clock and power (CPG)", "memory-map/", [(0xA4150000, 0xA4150100)], 1),
    ("pfc", "Ports (PFC)", "memory-map/", [(0xA4050000, 0xA4050200)], 1),
    ("adc", "Battery ADC", "peripherals/battery-adc/", [(0xA4610000, 0xA4610100)], 1),
    ("ser", "Serial (SCIF, 3-pin)", "peripherals/interrupts/", [(0xA4410000, 0xA4410100)], 1),
    ("usb", "USB", "", [(0xA4D80000, 0xA4D90000)], 1),
    ("wdt", "Watchdog", "memory-map/", [(0xA4520000, 0xA4520100)], 1),
    # P2 (uncached) flash outside the OS code: boot area / command addresses, and from the
    # language data on (CASIOMEMDATA, the storage memory)
    ("fla", "Flash (uncached, outside the OS code)", "flash/",
     [(0xA0000000, 0xA0020000), (0xA0B40000, 0xA2000000)], 1),
    ("mmu", "MMU, TLB and caches", "os/mmu-and-addins/",
     [(0xFF000000, 0xFF000100), (0xF0000000, 0xF0010000), (0xF1000000, 0xF1010000),
      (0xF2000000, 0xF2001000), (0xF3000000, 0xF3001000), (0xF4000000, 0xF4010000),
      (0xF5000000, 0xF5010000), (0xF6000000, 0xF6004000), (0xF7000000, 0xF7004000)], 4),
    ("cpu", "CPU control (UBC, CPUOPM)", "cpu/", [(0xFF200000, 0xFF300000)], 4),
    ("bsc", "Bus controller (BSC)", "memory-map/", [(0xFEC10000, 0xFEC20000)], 1),
    ("spu", "SPU registers (gint's name)", "memory-map/",
     [(0xFE2FFC00, 0xFE300000), (0xFE3FFC00, 0xFE400000)], 1),
    ("io", "Other I/O registers (0xA4xxxxxx)", "memory-map/", [(0xA4000000, 0xA5000000)], 1),
]


def category(v):
    for i, (_, _, _, ranges, align) in enumerate(CATS):
        if v % align:
            continue
        for lo, hi in ranges:
            if lo <= v < hi:
                return i
    return None


# ---------------------------------------------------------------------------------------------
def load_docs():
    """Layer 2: hand-written docs keyed by "0xNNNN" (see re/syscall_docs.json)."""
    if not os.path.exists(DOCS):
        return {}
    d = json.load(open(DOCS, encoding="utf-8"))
    out = {}
    for k, v in d.get("syscalls", {}).items():
        n = int(k, 16)
        bad = set(v) - {"name", "prototype", "description", "confidence", "notes"}
        if bad:
            sys.exit(f"syscall_docs.json {k}: unknown fields {sorted(bad)}")
        if v.get("confidence") not in (None, "tested", "read", "guessed"):
            sys.exit(f"syscall_docs.json {k}: confidence must be tested, read or guessed")
        out[n] = {kk: v[kk] for kk in ("name", "prototype", "description", "confidence") if kk in v}
    return out


def main():
    a, b = Image(IMG360, "3.60"), Image(IMG380, "3.80")
    t60, e60 = a.table()
    t80, e80 = b.table()
    print(f"3.60 table 0x{t60:08X}: {len(e60)} entries; 3.80 table 0x{t80:08X}: {len(e80)} entries")
    fa, args_a = analyze_image(a, e60)
    print(f"3.60: {len(fa)} functions analysed (syscalls and everything they call)")
    fb, _ = analyze_image(b, e80)
    print(f"3.80: {len(fb)} functions analysed")

    num_of = {}
    for i, t in enumerate(e60):
        num_of.setdefault(t, i)

    # direct categories for every analysed 3.60 function, then via calls (depth <= 2)
    direct = {}
    for addr, f in fa.items():
        m = 0
        for _, v in f.litvals:
            c = category(v)
            if c is not None:
                m |= 1 << c
        direct[addr] = m

    def via(addr, depth=2):
        m, seen, frontier = 0, {addr}, [addr]
        for _ in range(depth):
            nxt = []
            for x in frontier:
                for t in fa[x].calls | fa[x].tails:
                    if t in fa and t not in seen:
                        seen.add(t)
                        m |= direct[t]
                        nxt.append(t)
            frontier = nxt
        return m

    names = json.load(open(NAMES, encoding="utf-8")) if os.path.exists(NAMES) else {}
    docs = load_docs()

    rows = []
    stats = {"cmp": [0, 0, 0, 0], "stub": 0, "const": 0, "uncertain": 0, "invalid": 0, "msg": 0,
             "cat": [0] * len(CATS), "args": [0] * 5}
    for i, t in enumerate(e60):
        f = fa[t]
        t2 = e80[i] if i < len(e80) else None
        cmpc = compare(a, f, b, fb[t2]) if t2 is not None else 3
        stats["cmp"][cmpc] += 1
        size = f.end - f.entry
        size2 = fb[t2].end - fb[t2].entry if t2 is not None else 0
        flags = 0
        k = None
        if a.u16(t) == 0x000B:
            s = a.u16(t + 2)
            if s == 0x0009:
                flags |= 1
            elif s >> 8 == 0xE0:                     # rts; mov #k,r0
                flags |= 2
                k = disp8(s)
            elif s == 0x6043:                        # rts; mov r4,r0
                flags |= 16
        if f.unresolved:
            flags |= 4
        if f.invalid:
            flags |= 8
        stats["stub"] += flags & 1
        stats["const"] += (flags >> 1) & 1
        stats["uncertain"] += (flags >> 2) & 1
        stats["invalid"] += (flags >> 3) & 1
        am = args_a[t] >> 4                       # bit 0 = r4 ... bit 3 = r7
        stats["args"][am.bit_length()] += 1
        calls = []
        for c in sorted(f.calls | f.tails):
            calls.append(num_of[c] if c in num_of else c)
        mmio = sorted({v for _, v in f.litvals if category(v) is not None})
        cat = direct[t]
        cv = via(t) & ~cat
        for c in range(len(CATS)):
            if cat >> c & 1:
                stats["cat"][c] += 1
        msg = ""
        for _, v in sorted(f.litvals):
            s = a.cstring(v)
            if s:
                msg = s
                break
        if msg:
            stats["msg"] += 1
        row = [t, t2 or 0, cmpc, size, size2, flags, am, calls[:80], mmio[:16], cat, cv, msg]
        if k is not None:
            row.append(k)
        rows.append(row)

    nm = {}
    for key, d in names.items():
        n = int(key, 16)
        if n >= len(e60):
            continue
        e = {"n": d.get("name", "")}
        for s in ("libfxcg", "gint", "wp", "ours", "pages"):
            if s in d:
                e[s] = d[s]
        nm[n] = e
    for n, d in docs.items():
        e = nm.setdefault(n, {"n": ""})
        e["doc"] = d
        if d.get("name"):
            e["n"] = d["name"]

    out = {
        "v": 1,
        "t60": t60, "t80": t80,
        "cats": [[k, label, page] for k, label, page, _, _ in CATS],
        "rows": rows,
        "names": {str(n): nm[n] for n in sorted(nm)},
    }
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    with open(OUT, "w", encoding="utf-8") as fh:
        json.dump(out, fh, separators=(",", ":"), ensure_ascii=False)
    sz = os.path.getsize(OUT)
    c = stats["cmp"]
    print(f"3.80: identical bytes {c[0]}, same after masking {c[1]}, changed same size {c[2]}, "
          f"different size {c[3]}")
    print(f"empty stubs {stats['stub']}, return-a-constant {stats['const']}, size uncertain "
          f"{stats['uncertain']}, ran into an invalid word {stats['invalid']}, with a message {stats['msg']}")
    print(f"likely argument count 0..4: {stats['args']}")
    print("categories (direct): " + ", ".join(f"{CATS[i][0]} {n}" for i, n in enumerate(stats["cat"]) if n))
    print(f"distinct targets {len(set(e60))}; named {sum(1 for e in nm.values() if e['n'])}; "
          f"docs {len(docs)}")
    print(f"wrote {os.path.relpath(OUT, ROOT)} ({sz} bytes)")


if __name__ == "__main__":
    main()
