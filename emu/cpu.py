#!/usr/bin/env python3
"""
SH-4 / SH-4A CPU core (interpreter) for the fx-CG50 emulator.

Scope of this skeleton: the integer + system instruction subset exercised by the
OS boot path and main loop (enough to execute reset_entry and beyond). FPU ops
are stubbed (logged no-ops) until something needs them. Delayed branches are
modelled correctly (the delay-slot instruction executes before the transfer).

Validation target: running this from PC=0x80000000 should reproduce the MMIO
writes we documented in RECON_NOTES.md (SR/CCR/MMUCR, CPG, WDT, PFC, BSC...).
"""

MASK32 = 0xFFFFFFFF


def u32(x): return x & MASK32
def s32(x):
    x &= MASK32
    return x - (1 << 32) if x & 0x80000000 else x
def s8(x):  return (x & 0xFF) - 0x100 if x & 0x80 else x & 0xFF
def s16(x): return (x & 0xFFFF) - 0x10000 if x & 0x8000 else x & 0xFFFF


class IllegalInstruction(Exception):
    pass


class CPU:
    # SR bit masks
    T = 0x00000001
    S = 0x00000002
    Q = 0x00000100
    M = 0x00000200
    FD = 0x00008000
    BL = 0x10000000
    RB = 0x20000000
    MD = 0x40000000
    IMASK = 0x000000F0

    def __init__(self, mem):
        self.mem = mem
        self.r = [0] * 16
        self.rbank1 = [0] * 8          # the *inactive* bank's r0-r7
        self.pc = 0
        self.pr = 0
        self.gbr = 0
        self.vbr = 0
        self.ssr = 0
        self.spc = 0
        self.sgr = 0
        self.mach = 0
        self.macl = 0
        self.fpul = 0
        self.fpscr = 0
        self._sr = 0
        self.set_sr(self.MD | self.RB | self.BL | 0xF0)   # reset state
        self.cycles = 0
        self.trace = False
        self.halted = False
        self.pending = []        # list of (level, intevt_code) interrupt requests
        self.irq_count = 0

    # ---- SR with register-bank handling ----
    @property
    def sr(self):
        return self._sr

    def set_sr(self, val):
        val = u32(val)
        old_rb = self._sr & self.RB if hasattr(self, "_sr") else 0
        # initialise path
        if not hasattr(self, "_sr_init"):
            self._sr = val
            self._sr_init = True
            return
        new_rb = val & self.RB
        if old_rb != new_rb:
            # swap active r0-7 with the saved bank
            self.r[0:8], self.rbank1 = self.rbank1, self.r[0:8]
        self._sr = val

    # ---- interrupts ----
    def raise_irq(self, intevt, level):
        """Request a hardware interrupt (INTEVT code + priority level 1..15).
        Deduped so a periodic source can't grow the queue while masked/blocked."""
        if not any(p[1] == intevt for p in self.pending):
            self.pending.append((level, intevt))

    def _accept_interrupt(self):
        """If an unmasked IRQ is pending and SR.BL==0, perform hardware exception
        entry to VBR+0x600 (the software dispatcher we decoded reads INTEVT there)."""
        if not self.pending or (self._sr & self.BL):
            return False
        imask = (self._sr & self.IMASK) >> 4
        self.pending.sort()                 # by level ascending
        level, intevt = self.pending[-1]
        if level <= imask:
            return False
        self.pending.pop()
        # hardware interrupt sequence (SH-4)
        self.ssr = self._sr
        self.spc = self.pc
        self.sgr = self.r[15]
        self.mem.write(0xFF000028, 4, intevt)        # INTEVT <- code
        self.set_sr(self._sr | self.MD | self.RB | self.BL)   # privileged, bank1, blocked
        self.pc = u32(self.vbr + 0x600)
        self.irq_count += 1
        return True

    # ---- fetch/exec ----
    def step(self):
        self._accept_interrupt()
        op = self.mem.r16(self.pc)
        self.pc = u32(self.pc + 2)
        self.execute(op)
        self.cycles += 1

    def _branch_delayed(self, target):
        """Execute the delay-slot instruction, then jump to target."""
        slot = self.mem.r16(self.pc)
        self.pc = u32(self.pc + 2)
        self.execute(slot)              # delay slot (assumed non-branch)
        self.pc = u32(target)

    # ---- the decoder/executor ----
    def execute(self, op):
        n = (op >> 8) & 0xF
        m = (op >> 4) & 0xF
        d4 = op & 0xF
        d8 = op & 0xFF
        d12 = op & 0xFFF
        hi = op >> 12
        r = self.r
        mem = self.mem

        if op == 0x0009:                          # nop
            return
        if op == 0x000B:                          # rts
            self._branch_delayed(self.pr); return
        if op == 0x002B:                          # rte
            tgt = self.spc
            self.set_sr(self.ssr)
            self._branch_delayed(tgt); return
        if op == 0x0008: self._sr &= ~self.T; return   # clrt
        if op == 0x0018: self._sr |= self.T; return    # sett
        if op == 0x0019: self._sr &= ~self.Q & ~self.M; return  # div0u (clears M/Q)
        if op == 0x0028: self.mach = self.macl = 0; return       # clrmac
        if op == 0x0048: self._sr &= ~self.S; return   # clrs
        if op == 0x0058: self._sr |= self.S; return    # sets
        if op in (0x0038,): return                # ldtlb -> nop (TLB modelled elsewhere)
        if op in (0x00AB, 0x0093, 0x001B): return  # synco / (rsv) / sleep -> nop for now

        # ---- 0x0 group ----
        if hi == 0x0:
            if d8 == 0x02: r[n] = self.sr; return
            if d8 == 0x12: r[n] = self.gbr; return
            if d8 == 0x22: r[n] = self.vbr; return
            if d8 == 0x32: r[n] = self.ssr; return
            if d8 == 0x42: r[n] = self.spc; return
            if d8 == 0x0A: r[n] = self.mach; return
            if d8 == 0x1A: r[n] = self.macl; return
            if d8 == 0x2A: r[n] = self.pr; return
            if (op & 0x8F) == 0x82:               # stc Rm_bank,Rn
                r[n] = self.rbank1[(op >> 4) & 7]; return
            if d8 == 0x03:                        # bsrf Rn
                self.pr = u32(self.pc + 2); self._branch_delayed(u32(self.pc + 2 + r[n])); return
            if d8 == 0x23:                        # braf Rn
                self._branch_delayed(u32(self.pc + 2 + r[n])); return
            if d8 == 0x29: r[n] = self.sr & self.T; return     # movt
            if d8 in (0x83, 0x93, 0xA3, 0xB3, 0xC3, 0xD3, 0xE3):
                return                            # pref/ocbi/ocbp/ocbwb/movca/prefi/icbi -> nop
            if d4 == 0x4: mem.w8(u32(r[0] + r[n]), r[m] & 0xFF); return
            if d4 == 0x5: mem.w16(u32(r[0] + r[n]), r[m] & 0xFFFF); return
            if d4 == 0x6: mem.w32(u32(r[0] + r[n]), r[m]); return
            if d4 == 0x7: self.macl = u32(s32(r[n]) * s32(r[m])); return   # mul.l
            if d4 == 0xF:                          # mac.l @Rm+,@Rn+
                tn = s32(mem.r32(r[n])); r[n] = u32(r[n] + 4)
                tm = s32(mem.r32(r[m])); r[m] = u32(r[m] + 4)
                mac = (self.mach << 32) | self.macl
                if mac & (1 << 63): mac -= (1 << 64)   # sign-extend the 64-bit accumulator
                mac += tn * tm
                if self._sr & self.S:                  # S=1 -> saturate to 48-bit signed
                    hi = (1 << 47) - 1; lo = -(1 << 47)
                    mac = hi if mac > hi else lo if mac < lo else mac
                mac &= (1 << 64) - 1
                self.mach = u32(mac >> 32); self.macl = u32(mac); return
            if d4 == 0xC: r[n] = s8(mem.r8(u32(r[0] + r[m]))) & MASK32; return
            if d4 == 0xD: r[n] = s16(mem.r16(u32(r[0] + r[m]))) & MASK32; return
            if d4 == 0xE: r[n] = mem.r32(u32(r[0] + r[m])); return
            raise IllegalInstruction(f"0x{op:04x}")

        if hi == 0x1:                              # mov.l Rm,@(disp,Rn)
            mem.w32(u32(r[n] + d4 * 4), r[m]); return

        if hi == 0x2:
            if d4 == 0x0: mem.w8(r[n], r[m] & 0xFF); return
            if d4 == 0x1: mem.w16(r[n], r[m] & 0xFFFF); return
            if d4 == 0x2: mem.w32(r[n], r[m]); return
            if d4 == 0x4: r[n] = u32(r[n] - 1); mem.w8(r[n], r[m] & 0xFF); return
            if d4 == 0x5: r[n] = u32(r[n] - 2); mem.w16(r[n], r[m] & 0xFFFF); return
            if d4 == 0x6: r[n] = u32(r[n] - 4); mem.w32(r[n], r[m]); return
            if d4 == 0x7:                          # div0s
                self._sr = (self._sr & ~self.Q) | (self.Q if r[n] & 0x80000000 else 0)
                self._sr = (self._sr & ~self.M) | (self.M if r[m] & 0x80000000 else 0)
                q = 1 if r[n] & 0x80000000 else 0
                mb = 1 if r[m] & 0x80000000 else 0
                self._set_t(q ^ mb); return
            if d4 == 0x8: self._set_t((r[n] & r[m]) == 0); return            # tst
            if d4 == 0x9: r[n] = r[n] & r[m]; return                         # and
            if d4 == 0xA: r[n] = r[n] ^ r[m]; return                         # xor
            if d4 == 0xB: r[n] = r[n] | r[m]; return                         # or
            if d4 == 0xC:                          # cmp/str
                t = any(((r[n] >> (8 * i)) & 0xFF) == ((r[m] >> (8 * i)) & 0xFF) for i in range(4))
                self._set_t(t); return
            if d4 == 0xD: r[n] = u32(((r[m] & 0xFFFF) << 16) | (r[n] >> 16)); return  # xtrct
            if d4 == 0xE: self.macl = u32((r[n] & 0xFFFF) * (r[m] & 0xFFFF)); return   # mulu.w
            if d4 == 0xF: self.macl = u32(s16(r[n]) * s16(r[m])); return     # muls.w
            raise IllegalInstruction(f"0x{op:04x}")

        if hi == 0x3:
            if d4 == 0x0: self._set_t(r[n] == r[m]); return                  # cmp/eq
            if d4 == 0x2: self._set_t(u32(r[n]) >= u32(r[m])); return        # cmp/hs
            if d4 == 0x3: self._set_t(s32(r[n]) >= s32(r[m])); return        # cmp/ge
            if d4 == 0x6: self._set_t(u32(r[n]) > u32(r[m])); return         # cmp/hi
            if d4 == 0x7: self._set_t(s32(r[n]) > s32(r[m])); return         # cmp/gt
            if d4 == 0x8: r[n] = u32(r[n] - r[m]); return                    # sub
            if d4 == 0xC: r[n] = u32(r[n] + r[m]); return                    # add
            if d4 == 0xA:                                                    # subc
                t = self.sr & self.T
                res = r[n] - r[m] - t
                self._set_t(res < 0); r[n] = u32(res); return
            if d4 == 0xE:                                                    # addc
                t = self.sr & self.T
                res = r[n] + r[m] + t
                self._set_t(res > MASK32); r[n] = u32(res); return
            if d4 == 0xB:                                                    # subv
                res = s32(r[n]) - s32(r[m]); self._set_t(not -0x80000000 <= res <= 0x7FFFFFFF)
                r[n] = u32(res); return
            if d4 == 0xF:                                                    # addv
                res = s32(r[n]) + s32(r[m]); self._set_t(not -0x80000000 <= res <= 0x7FFFFFFF)
                r[n] = u32(res); return
            if d4 == 0x4: self._div1(n, m); return
            if d4 == 0x5:                                                    # dmulu.l
                res = u32(r[n]) * u32(r[m]); self.mach = u32(res >> 32); self.macl = u32(res); return
            if d4 == 0xD:                                                    # dmuls.l
                res = s32(r[n]) * s32(r[m]); self.mach = u32(res >> 32); self.macl = u32(res); return
            raise IllegalInstruction(f"0x{op:04x}")

        if hi == 0x4:
            return self._exec_4(op, n, m, d8)

        if hi == 0x5:                              # mov.l @(disp,Rm),Rn
            r[n] = mem.r32(u32(r[m] + d4 * 4)); return

        if hi == 0x6:
            if d4 == 0x0: r[n] = s8(mem.r8(r[m])) & MASK32; return
            if d4 == 0x1: r[n] = s16(mem.r16(r[m])) & MASK32; return
            if d4 == 0x2: r[n] = mem.r32(r[m]); return
            if d4 == 0x3: r[n] = r[m]; return
            if d4 == 0x4: r[n] = s8(mem.r8(r[m])) & MASK32; r[m] = u32(r[m] + 1); return
            if d4 == 0x5: r[n] = s16(mem.r16(r[m])) & MASK32; r[m] = u32(r[m] + 2); return
            if d4 == 0x6: r[n] = mem.r32(r[m]); r[m] = u32(r[m] + 4); return
            if d4 == 0x7: r[n] = u32(~r[m]); return
            if d4 == 0x8: r[n] = u32(((r[m] & 0xFF) << 8) | ((r[m] >> 8) & 0xFF) | (r[m] & 0xFFFF0000)); return
            if d4 == 0x9: r[n] = u32(((r[m] & 0xFFFF) << 16) | ((r[m] >> 16) & 0xFFFF)); return
            if d4 == 0xA:                          # negc
                t = self.sr & self.T; res = 0 - r[m] - t
                self._set_t(res < 0); r[n] = u32(res); return
            if d4 == 0xB: r[n] = u32(-s32(r[m])); return
            if d4 == 0xC: r[n] = r[m] & 0xFF; return
            if d4 == 0xD: r[n] = r[m] & 0xFFFF; return
            if d4 == 0xE: r[n] = s8(r[m]) & MASK32; return
            if d4 == 0xF: r[n] = s16(r[m]) & MASK32; return

        if hi == 0x7:                              # add #imm,Rn
            r[n] = u32(r[n] + s8(d8)); return

        if hi == 0x8:
            sub = (op >> 8) & 0xF
            if sub == 0x0: mem.w8(u32(r[m] + d4), r[0] & 0xFF); return
            if sub == 0x1: mem.w16(u32(r[m] + d4 * 2), r[0] & 0xFFFF); return
            if sub == 0x4: r[0] = s8(mem.r8(u32(r[m] + d4))) & MASK32; return
            if sub == 0x5: r[0] = s16(mem.r16(u32(r[m] + d4 * 2))) & MASK32; return
            if sub == 0x8: self._set_t((r[0] & MASK32) == (s8(d8) & MASK32)); return   # cmp/eq #imm,r0
            if sub == 0x9:                          # bt
                if self.sr & self.T: self.pc = u32(self.pc + 2 + s8(d8) * 2)
                return
            if sub == 0xB:                          # bf
                if not self.sr & self.T: self.pc = u32(self.pc + 2 + s8(d8) * 2)
                return
            if sub == 0xD:                          # bt/s
                if self.sr & self.T: self._branch_delayed(u32(self.pc + 2 + s8(d8) * 2))
                return
            if sub == 0xF:                          # bf/s
                if not self.sr & self.T: self._branch_delayed(u32(self.pc + 2 + s8(d8) * 2))
                return

        if hi == 0x9:                              # mov.w @(disp,PC),Rn
            ea = u32(self.pc + 2 + d8 * 2)
            r[n] = s16(mem.r16(ea)) & MASK32; return

        if hi == 0xA:                              # bra
            disp = d12 - 0x1000 if d12 & 0x800 else d12
            self._branch_delayed(u32(self.pc + 2 + disp * 2)); return

        if hi == 0xB:                              # bsr
            disp = d12 - 0x1000 if d12 & 0x800 else d12
            self.pr = u32(self.pc + 2)             # return addr (after delay slot)
            self._branch_delayed(u32(self.pc + 2 + disp * 2)); return

        if hi == 0xC:
            sub = (op >> 8) & 0xF
            if sub == 0x0: mem.w8(u32(self.gbr + d8), r[0] & 0xFF); return
            if sub == 0x1: mem.w16(u32(self.gbr + d8 * 2), r[0] & 0xFFFF); return
            if sub == 0x2: mem.w32(u32(self.gbr + d8 * 4), r[0]); return
            if sub == 0x3: raise IllegalInstruction(f"trapa #{d8:#x} @0x{u32(self.pc-2):08x}")
            if sub == 0x4: r[0] = s8(mem.r8(u32(self.gbr + d8))) & MASK32; return
            if sub == 0x5: r[0] = s16(mem.r16(u32(self.gbr + d8 * 2))) & MASK32; return
            if sub == 0x6: r[0] = mem.r32(u32(self.gbr + d8 * 4)); return
            if sub == 0x7: r[0] = u32(((self.pc + 2) & ~3) + d8 * 4); return   # mova
            if sub == 0x8: self._set_t((r[0] & d8) == 0); return            # tst #imm,r0
            if sub == 0x9: r[0] = r[0] & d8; return
            if sub == 0xA: r[0] = r[0] ^ d8; return
            if sub == 0xB: r[0] = r[0] | d8; return

        if hi == 0xD:                              # mov.l @(disp,PC),Rn
            ea = u32((self.pc + 2 & ~3) + d8 * 4)
            r[n] = mem.r32(ea); return

        if hi == 0xE:                              # mov #imm,Rn
            r[n] = s8(d8) & MASK32; return

        if hi == 0xF:                              # FPU — stub
            if self.trace:
                print(f"  [cpu] FPU op 0x{op:04x} @0x{u32(self.pc-2):08x} (stubbed nop)")
            return

        raise IllegalInstruction(f"0x{op:04x} @0x{u32(self.pc-2):08x}")

    # ---- 0x4 group (shifts / system control) ----
    def _exec_4(self, op, n, m, d8):
        r = self.r; mem = self.mem
        if d8 == 0x00: self._set_t(r[n] >> 31); r[n] = u32(r[n] << 1); return   # shll
        if d8 == 0x01: self._set_t(r[n] & 1); r[n] = r[n] >> 1; return          # shlr
        if d8 == 0x04:                                                          # rotl
            t = r[n] >> 31; r[n] = u32((r[n] << 1) | t); self._set_t(t); return
        if d8 == 0x05:                                                          # rotr
            t = r[n] & 1; r[n] = u32((r[n] >> 1) | (t << 31)); self._set_t(t); return
        if d8 == 0x08: r[n] = u32(r[n] << 2); return                           # shll2
        if d8 == 0x09: r[n] = r[n] >> 2; return                                # shlr2
        if d8 == 0x18: r[n] = u32(r[n] << 8); return                           # shll8
        if d8 == 0x19: r[n] = r[n] >> 8; return                                # shlr8
        if d8 == 0x28: r[n] = u32(r[n] << 16); return                          # shll16
        if d8 == 0x29: r[n] = r[n] >> 16; return                               # shlr16
        if d8 == 0x20: self._set_t(r[n] >> 31); r[n] = u32(r[n] << 1); return  # shal
        if d8 == 0x21: self._set_t(r[n] & 1); r[n] = u32(s32(r[n]) >> 1); return  # shar
        if d8 == 0x24:                                                         # rotcl
            t = r[n] >> 31; r[n] = u32((r[n] << 1) | (self.sr & self.T)); self._set_t(t); return
        if d8 == 0x25:                                                         # rotcr
            t = r[n] & 1; r[n] = u32((r[n] >> 1) | ((self.sr & self.T) << 31)); self._set_t(t); return
        if d8 == 0x10: r[n] = u32(r[n] - 1); self._set_t(r[n] == 0); return     # dt
        if d8 == 0x11: self._set_t(s32(r[n]) >= 0); return                      # cmp/pz
        if d8 == 0x15: self._set_t(s32(r[n]) > 0); return                       # cmp/pl
        if d8 == 0x0B:                                                          # jsr @Rn
            self.pr = u32(self.pc + 2); self._branch_delayed(r[n]); return
        if d8 == 0x2B: self._branch_delayed(r[n]); return                       # jmp @Rn
        if d8 == 0x0E: self.set_sr(r[n]); return                                # ldc Rn,SR
        if d8 == 0x1E: self.gbr = r[n]; return                                  # ldc Rn,GBR
        if d8 == 0x2E: self.vbr = r[n]; return                                  # ldc Rn,VBR
        if d8 == 0x3E: self.ssr = r[n]; return                                  # ldc Rn,SSR
        if d8 == 0x4E: self.spc = r[n]; return                                  # ldc Rn,SPC
        if d8 == 0x0A: self.mach = r[n]; return                                 # lds Rn,MACH
        if d8 == 0x1A: self.macl = r[n]; return                                 # lds Rn,MACL
        if d8 == 0x2A: self.pr = r[n]; return                                   # lds Rn,PR
        if d8 == 0x5A: self.fpul = r[n]; return                                 # lds Rn,FPUL
        if d8 == 0x6A: self.fpscr = r[n]; return                                # lds Rn,FPSCR
        if (op & 0x8F) == 0x8E:                    # ldc Rn,Rm_bank
            self.rbank1[(op >> 4) & 7] = r[n]; return
        # control-register stack ops (ldc.l / stc.l) with post-inc / pre-dec on Rn
        if d8 == 0x07: self.set_sr(mem.r32(r[n])); r[n] = u32(r[n] + 4); return  # ldc.l @Rn+,SR
        if d8 == 0x17: self.gbr = mem.r32(r[n]); r[n] = u32(r[n] + 4); return    # @Rn+,GBR
        if d8 == 0x27: self.vbr = mem.r32(r[n]); r[n] = u32(r[n] + 4); return    # @Rn+,VBR
        if d8 == 0x37: self.ssr = mem.r32(r[n]); r[n] = u32(r[n] + 4); return    # @Rn+,SSR
        if d8 == 0x47: self.spc = mem.r32(r[n]); r[n] = u32(r[n] + 4); return    # @Rn+,SPC
        if d8 == 0x06: self.mach = mem.r32(r[n]); r[n] = u32(r[n] + 4); return   # @Rn+,MACH
        if d8 == 0x16: self.macl = mem.r32(r[n]); r[n] = u32(r[n] + 4); return   # @Rn+,MACL
        if d8 == 0x26: self.pr = mem.r32(r[n]); r[n] = u32(r[n] + 4); return     # @Rn+,PR
        if d8 == 0x56: self.fpul = mem.r32(r[n]); r[n] = u32(r[n] + 4); return   # @Rn+,FPUL
        if d8 == 0x66: self.fpscr = mem.r32(r[n]); r[n] = u32(r[n] + 4); return  # @Rn+,FPSCR
        if (op & 0x8F) == 0x87:                    # ldc.l @Rn+,Rm_bank
            self.rbank1[(op >> 4) & 7] = mem.r32(r[n]); r[n] = u32(r[n] + 4); return
        if d8 == 0x03: r[n] = u32(r[n] - 4); mem.w32(r[n], self.sr); return      # stc.l SR,@-Rn
        if d8 == 0x13: r[n] = u32(r[n] - 4); mem.w32(r[n], self.gbr); return     # GBR
        if d8 == 0x23: r[n] = u32(r[n] - 4); mem.w32(r[n], self.vbr); return     # VBR
        if d8 == 0x33: r[n] = u32(r[n] - 4); mem.w32(r[n], self.ssr); return     # SSR
        if d8 == 0x43: r[n] = u32(r[n] - 4); mem.w32(r[n], self.spc); return     # SPC
        if d8 == 0x02: r[n] = u32(r[n] - 4); mem.w32(r[n], self.mach); return    # MACH
        if d8 == 0x12: r[n] = u32(r[n] - 4); mem.w32(r[n], self.macl); return    # MACL
        if d8 == 0x22: r[n] = u32(r[n] - 4); mem.w32(r[n], self.pr); return      # PR
        if d8 == 0x52: r[n] = u32(r[n] - 4); mem.w32(r[n], self.fpul); return    # FPUL
        if d8 == 0x62: r[n] = u32(r[n] - 4); mem.w32(r[n], self.fpscr); return   # FPSCR
        if (op & 0x8F) == 0x83:                    # stc.l Rm_bank,@-Rn
            r[n] = u32(r[n] - 4); mem.w32(r[n], self.rbank1[(op >> 4) & 7]); return
        if d8 == 0x1B:                             # tas.b @Rn
            v = mem.r8(r[n]); self._set_t(v == 0); mem.w8(r[n], v | 0x80); return
        d4 = op & 0xF
        if d4 == 0xC:                              # shad Rm,Rn
            sh = s32(r[m])
            if sh >= 0: r[n] = u32(r[n] << (sh & 0x1F))
            else: r[n] = u32(s32(r[n]) >> ((-sh) & 0x1F if sh != -32 else 31))
            return
        if d4 == 0xD:                              # shld Rm,Rn
            sh = s32(r[m])
            if sh >= 0: r[n] = u32(r[n] << (sh & 0x1F))
            else: r[n] = r[n] >> (((-sh) & 0x1F) if sh != -32 else 32) if sh != -32 else 0
            return
        raise IllegalInstruction(f"0x{op:04x} @0x{u32(self.pc-2):08x}")

    # bsrf/jsr/bsr set PR; handle the two that need PR (jsr/bsrf) here via wrappers
    def _set_t(self, cond):
        if cond: self._sr |= self.T
        else: self._sr &= ~self.T

    def _div1(self, n, m):
        # One-step divide (SH-4 software manual). Rn=dividend/remainder, Rm=divisor.
        r = self.r
        Rm = r[m]
        Rn = r[n]
        old_q = 1 if self._sr & self.Q else 0
        M = 1 if self._sr & self.M else 0
        T = 1 if self._sr & self.T else 0
        Q = (Rn >> 31) & 1
        Rn = ((Rn << 1) & MASK32) | T
        if old_q == 0:
            if M == 0:
                tmp0 = Rn
                Rn = (Rn - Rm) & MASK32
                tmp1 = 1 if Rn > tmp0 else 0
                Q = tmp1 if Q == 0 else (tmp1 ^ 1)
            else:
                tmp0 = Rn
                Rn = (Rn + Rm) & MASK32
                tmp1 = 1 if Rn < tmp0 else 0
                Q = (tmp1 ^ 1) if Q == 0 else tmp1
        else:
            if M == 0:
                tmp0 = Rn
                Rn = (Rn + Rm) & MASK32
                tmp1 = 1 if Rn < tmp0 else 0
                Q = tmp1 if Q == 0 else (tmp1 ^ 1)
            else:
                tmp0 = Rn
                Rn = (Rn - Rm) & MASK32
                tmp1 = 1 if Rn > tmp0 else 0
                Q = (tmp1 ^ 1) if Q == 0 else tmp1
        r[n] = Rn
        if Q: self._sr |= self.Q
        else: self._sr &= ~self.Q
        self._set_t(Q == M)
