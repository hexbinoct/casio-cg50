package main

import "sort"

// SH-4 / SH-4A integer + system core. Faithful port of emu/cpu.py.
// FPU ops are stubbed (no-ops). Delayed branches modelled correctly.

const mask32 = 0xFFFFFFFF

// SR bit masks
const (
	srT     = 0x00000001
	srS     = 0x00000002
	srQ     = 0x00000100
	srM     = 0x00000200
	srFD    = 0x00008000
	srBL    = 0x10000000
	srRB    = 0x20000000
	srMD    = 0x40000000
	srIMASK = 0x000000F0
)

func s8(x uint32) int32  { return int32(int8(uint8(x))) }
func s16(x uint32) int32 { return int32(int16(uint16(x))) }
func s32(x uint32) int32 { return int32(x) }

type IllegalInstruction struct{ op, pc uint32 }

func (e IllegalInstruction) Error() string { return "illegal instruction" }

type pend struct{ level, intevt uint32 }

type CPU struct {
	mem     *Memory
	r       [16]uint32
	rbank1  [8]uint32
	pc      uint32
	pr      uint32
	gbr     uint32
	vbr     uint32
	ssr     uint32
	spc     uint32
	sgr     uint32
	mach    uint32
	macl    uint32
	fpul    uint32
	fpscr   uint32
	sr      uint32
	cycles  uint64
	pending []pend
	irqCnt  uint64
	fpuOps  uint64
}

func NewCPU(mem *Memory) *CPU {
	c := &CPU{mem: mem}
	c.setSR(srMD | srRB | srBL | 0xF0)
	return c
}

func (c *CPU) setSR(val uint32) {
	oldRB := c.sr & srRB
	newRB := val & srRB
	if oldRB != newRB {
		for i := 0; i < 8; i++ {
			c.r[i], c.rbank1[i] = c.rbank1[i], c.r[i]
		}
	}
	c.sr = val
}

func (c *CPU) setT(cond bool) {
	if cond {
		c.sr |= srT
	} else {
		c.sr &^= srT
	}
}

func (c *CPU) raiseIRQ(intevt, level uint32) {
	for _, p := range c.pending {
		if p.intevt == intevt {
			return
		}
	}
	c.pending = append(c.pending, pend{level, intevt})
}

func (c *CPU) acceptInterrupt() bool {
	if len(c.pending) == 0 || (c.sr&srBL) != 0 {
		return false
	}
	imask := (c.sr & srIMASK) >> 4
	sort.SliceStable(c.pending, func(i, j int) bool {
		if c.pending[i].level != c.pending[j].level {
			return c.pending[i].level < c.pending[j].level
		}
		return c.pending[i].intevt < c.pending[j].intevt
	})
	top := c.pending[len(c.pending)-1]
	if top.level <= imask {
		return false
	}
	c.pending = c.pending[:len(c.pending)-1]
	c.ssr = c.sr
	c.spc = c.pc
	c.sgr = c.r[15]
	c.mem.Write(0xFF000028, 4, top.intevt)
	c.setSR(c.sr | srMD | srRB | srBL)
	c.pc = c.vbr + 0x600
	c.irqCnt++
	return true
}

func (c *CPU) step() {
	c.acceptInterrupt()
	op := c.mem.R16(c.pc)
	c.pc += 2
	c.execute(op)
	c.cycles++
}

// callInject runs the OS function at `addr` as a subroutine with args in r4..r7,
// then restores ALL architectural registers so normal execution resumes exactly
// where it left off. Interrupts are masked and the timer is not ticked during the
// call, so it executes atomically with respect to the rest of the run. The call
// runs on a stack lowered 0x40 below the live sp (the caller must place any
// pointer args ABOVE that, e.g. at sp-8, so the callee's pushes don't clobber it).
// Returns r0 at the function's rts. Used to inject a keypress by calling the very
// enqueue routine the hardware matrix-scanner uses (FUN_801e684c).
func (c *CPU) callInject(addr uint32, args ...uint32) uint32 {
	// snapshot architectural state
	r0, rb0 := c.r, c.rbank1
	pc0, pr0, sr0 := c.pc, c.pr, c.sr
	gbr0, vbr0, ssr0, spc0, sgr0 := c.gbr, c.vbr, c.ssr, c.spc, c.sgr
	mach0, macl0, fpul0, fpscr0 := c.mach, c.macl, c.fpul, c.fpscr
	pend0 := c.pending

	const sentinel = 0xDEAD0000
	c.pending = nil
	c.r[15] = (r0[15] - 0x40) &^ 3 // lowered, aligned call stack
	for i, a := range args {
		if i < 4 {
			c.r[4+i] = a
		}
	}
	c.pr = sentinel
	c.pc = addr
	c.sr |= srBL // mask interrupts for the duration of the call
	for guard := 0; c.pc != sentinel && guard < 5_000_000; guard++ {
		c.step()
	}
	ret := c.r[0]

	// restore
	c.r, c.rbank1 = r0, rb0
	c.pc, c.pr, c.sr = pc0, pr0, sr0
	c.gbr, c.vbr, c.ssr, c.spc, c.sgr = gbr0, vbr0, ssr0, spc0, sgr0
	c.mach, c.macl, c.fpul, c.fpscr = mach0, macl0, fpul0, fpscr0
	c.pending = pend0
	return ret
}

func (c *CPU) branchDelayed(target uint32) {
	slot := c.mem.R16(c.pc)
	c.pc += 2
	c.execute(slot)
	c.pc = target
}

func (c *CPU) execute(op uint32) {
	n := (op >> 8) & 0xF
	m := (op >> 4) & 0xF
	d4 := op & 0xF
	d8 := op & 0xFF
	d12 := op & 0xFFF
	hi := op >> 12
	r := &c.r
	mem := c.mem

	switch op {
	case 0x0009:
		return
	case 0x000B: // rts
		c.branchDelayed(c.pr)
		return
	case 0x002B: // rte
		tgt := c.spc
		c.setSR(c.ssr)
		c.branchDelayed(tgt)
		return
	case 0x0008:
		c.sr &^= srT
		return
	case 0x0018:
		c.sr |= srT
		return
	case 0x0019:
		c.sr &^= (srQ | srM)
		return
	case 0x0028:
		c.mach, c.macl = 0, 0
		return
	case 0x0048:
		c.sr &^= srS
		return
	case 0x0058:
		c.sr |= srS
		return
	case 0x0038, 0x00AB, 0x0093, 0x001B:
		return // ldtlb/synco/(rsv)/sleep -> nop
	}

	switch hi {
	case 0x0:
		switch d8 {
		case 0x02:
			r[n] = c.sr
			return
		case 0x12:
			r[n] = c.gbr
			return
		case 0x22:
			r[n] = c.vbr
			return
		case 0x32:
			r[n] = c.ssr
			return
		case 0x42:
			r[n] = c.spc
			return
		case 0x0A:
			r[n] = c.mach
			return
		case 0x1A:
			r[n] = c.macl
			return
		case 0x2A:
			r[n] = c.pr
			return
		case 0x29:
			r[n] = c.sr & srT
			return
		case 0x03: // bsrf
			c.pr = c.pc + 2
			c.branchDelayed(c.pc + 2 + r[n])
			return
		case 0x23: // braf
			c.branchDelayed(c.pc + 2 + r[n])
			return
		case 0x83, 0x93, 0xA3, 0xB3, 0xC3, 0xD3, 0xE3:
			return // pref/ocbi/... -> nop
		}
		if (op & 0x8F) == 0x82 { // stc Rm_bank,Rn
			r[n] = c.rbank1[(op>>4)&7]
			return
		}
		switch d4 {
		case 0x4:
			mem.W8(r[0]+r[n], r[m]&0xFF)
			return
		case 0x5:
			mem.W16(r[0]+r[n], r[m]&0xFFFF)
			return
		case 0x6:
			mem.W32(r[0]+r[n], r[m])
			return
		case 0x7:
			c.macl = uint32(s32(r[n]) * s32(r[m]))
			return
		case 0xF: // mac.l @Rm+,@Rn+
			tn := int64(s32(mem.R32(r[n])))
			r[n] += 4
			tm := int64(s32(mem.R32(r[m])))
			r[m] += 4
			mac := int64(uint64(c.mach)<<32 | uint64(c.macl))
			mac += tn * tm
			if c.sr&srS != 0 { // S=1 -> saturate to 48-bit signed
				const hi = int64(1)<<47 - 1
				const lo = -(int64(1) << 47)
				if mac > hi {
					mac = hi
				} else if mac < lo {
					mac = lo
				}
			}
			c.mach = uint32(uint64(mac) >> 32)
			c.macl = uint32(uint64(mac))
			return
		case 0xC:
			r[n] = uint32(s8(mem.R8(r[0] + r[m])))
			return
		case 0xD:
			r[n] = uint32(s16(mem.R16(r[0] + r[m])))
			return
		case 0xE:
			r[n] = mem.R32(r[0] + r[m])
			return
		}
		panic(IllegalInstruction{op, c.pc - 2})

	case 0x1: // mov.l Rm,@(disp,Rn)
		mem.W32(r[n]+d4*4, r[m])
		return

	case 0x2:
		switch d4 {
		case 0x0:
			mem.W8(r[n], r[m]&0xFF)
			return
		case 0x1:
			mem.W16(r[n], r[m]&0xFFFF)
			return
		case 0x2:
			mem.W32(r[n], r[m])
			return
		case 0x4:
			r[n] = r[n] - 1
			mem.W8(r[n], r[m]&0xFF)
			return
		case 0x5:
			r[n] = r[n] - 2
			mem.W16(r[n], r[m]&0xFFFF)
			return
		case 0x6:
			r[n] = r[n] - 4
			mem.W32(r[n], r[m])
			return
		case 0x7: // div0s
			if r[n]&0x80000000 != 0 {
				c.sr |= srQ
			} else {
				c.sr &^= srQ
			}
			if r[m]&0x80000000 != 0 {
				c.sr |= srM
			} else {
				c.sr &^= srM
			}
			q := (r[n] >> 31) & 1
			mb := (r[m] >> 31) & 1
			c.setT((q ^ mb) != 0)
			return
		case 0x8:
			c.setT((r[n] & r[m]) == 0)
			return
		case 0x9:
			r[n] = r[n] & r[m]
			return
		case 0xA:
			r[n] = r[n] ^ r[m]
			return
		case 0xB:
			r[n] = r[n] | r[m]
			return
		case 0xC: // cmp/str
			t := false
			for i := uint32(0); i < 4; i++ {
				if ((r[n] >> (8 * i)) & 0xFF) == ((r[m] >> (8 * i)) & 0xFF) {
					t = true
				}
			}
			c.setT(t)
			return
		case 0xD: // xtrct
			r[n] = ((r[m] & 0xFFFF) << 16) | (r[n] >> 16)
			return
		case 0xE: // mulu.w
			c.macl = (r[n] & 0xFFFF) * (r[m] & 0xFFFF)
			return
		case 0xF: // muls.w
			c.macl = uint32(s16(r[n]) * s16(r[m]))
			return
		}
		panic(IllegalInstruction{op, c.pc - 2})

	case 0x3:
		switch d4 {
		case 0x0:
			c.setT(r[n] == r[m])
			return
		case 0x2:
			c.setT(r[n] >= r[m])
			return
		case 0x3:
			c.setT(s32(r[n]) >= s32(r[m]))
			return
		case 0x6:
			c.setT(r[n] > r[m])
			return
		case 0x7:
			c.setT(s32(r[n]) > s32(r[m]))
			return
		case 0x8:
			r[n] = r[n] - r[m]
			return
		case 0xC:
			r[n] = r[n] + r[m]
			return
		case 0xA: // subc
			t := c.sr & srT
			res := int64(r[n]) - int64(r[m]) - int64(t)
			c.setT(res < 0)
			r[n] = uint32(res)
			return
		case 0xE: // addc
			t := c.sr & srT
			res := uint64(r[n]) + uint64(r[m]) + uint64(t)
			c.setT(res > mask32)
			r[n] = uint32(res)
			return
		case 0xB: // subv
			res := int64(s32(r[n])) - int64(s32(r[m]))
			c.setT(res < -0x80000000 || res > 0x7FFFFFFF)
			r[n] = uint32(res)
			return
		case 0xF: // addv
			res := int64(s32(r[n])) + int64(s32(r[m]))
			c.setT(res < -0x80000000 || res > 0x7FFFFFFF)
			r[n] = uint32(res)
			return
		case 0x4:
			c.div1(n, m)
			return
		case 0x5: // dmulu.l
			res := uint64(r[n]) * uint64(r[m])
			c.mach = uint32(res >> 32)
			c.macl = uint32(res)
			return
		case 0xD: // dmuls.l
			res := int64(s32(r[n])) * int64(s32(r[m]))
			c.mach = uint32(uint64(res) >> 32)
			c.macl = uint32(res)
			return
		}
		panic(IllegalInstruction{op, c.pc - 2})

	case 0x4:
		c.exec4(op, n, m, d8)
		return

	case 0x5: // mov.l @(disp,Rm),Rn
		r[n] = mem.R32(r[m] + d4*4)
		return

	case 0x6:
		switch d4 {
		case 0x0:
			r[n] = uint32(s8(mem.R8(r[m])))
			return
		case 0x1:
			r[n] = uint32(s16(mem.R16(r[m])))
			return
		case 0x2:
			r[n] = mem.R32(r[m])
			return
		case 0x3:
			r[n] = r[m]
			return
		case 0x4:
			r[n] = uint32(s8(mem.R8(r[m])))
			r[m] = r[m] + 1
			return
		case 0x5:
			r[n] = uint32(s16(mem.R16(r[m])))
			r[m] = r[m] + 2
			return
		case 0x6:
			r[n] = mem.R32(r[m])
			r[m] = r[m] + 4
			return
		case 0x7:
			r[n] = ^r[m]
			return
		case 0x8:
			r[n] = ((r[m] & 0xFF) << 8) | ((r[m] >> 8) & 0xFF) | (r[m] & 0xFFFF0000)
			return
		case 0x9:
			r[n] = ((r[m] & 0xFFFF) << 16) | ((r[m] >> 16) & 0xFFFF)
			return
		case 0xA: // negc
			t := c.sr & srT
			res := int64(0) - int64(r[m]) - int64(t)
			c.setT(res < 0)
			r[n] = uint32(res)
			return
		case 0xB:
			r[n] = uint32(-s32(r[m]))
			return
		case 0xC:
			r[n] = r[m] & 0xFF
			return
		case 0xD:
			r[n] = r[m] & 0xFFFF
			return
		case 0xE:
			r[n] = uint32(s8(r[m]))
			return
		case 0xF:
			r[n] = uint32(s16(r[m]))
			return
		}
		return

	case 0x7: // add #imm,Rn
		r[n] = r[n] + uint32(s8(d8))
		return

	case 0x8:
		switch n { // (op>>8)&0xF
		case 0x0:
			mem.W8(r[m]+d4, r[0]&0xFF)
			return
		case 0x1:
			mem.W16(r[m]+d4*2, r[0]&0xFFFF)
			return
		case 0x4:
			r[0] = uint32(s8(mem.R8(r[m] + d4)))
			return
		case 0x5:
			r[0] = uint32(s16(mem.R16(r[m] + d4*2)))
			return
		case 0x8:
			c.setT(r[0] == uint32(s8(d8)))
			return
		case 0x9: // bt
			if c.sr&srT != 0 {
				c.pc = c.pc + 2 + uint32(s8(d8))*2
			}
			return
		case 0xB: // bf
			if c.sr&srT == 0 {
				c.pc = c.pc + 2 + uint32(s8(d8))*2
			}
			return
		case 0xD: // bt/s
			if c.sr&srT != 0 {
				c.branchDelayed(c.pc + 2 + uint32(s8(d8))*2)
			}
			return
		case 0xF: // bf/s
			if c.sr&srT == 0 {
				c.branchDelayed(c.pc + 2 + uint32(s8(d8))*2)
			}
			return
		}
		return

	case 0x9: // mov.w @(disp,PC),Rn
		ea := c.pc + 2 + d8*2
		r[n] = uint32(s16(mem.R16(ea)))
		return

	case 0xA: // bra
		disp := d12
		if d12&0x800 != 0 {
			disp = d12 - 0x1000
		}
		c.branchDelayed(c.pc + 2 + disp*2)
		return

	case 0xB: // bsr
		disp := d12
		if d12&0x800 != 0 {
			disp = d12 - 0x1000
		}
		c.pr = c.pc + 2
		c.branchDelayed(c.pc + 2 + disp*2)
		return

	case 0xC:
		switch n { // (op>>8)&0xF
		case 0x0:
			mem.W8(c.gbr+d8, r[0]&0xFF)
			return
		case 0x1:
			mem.W16(c.gbr+d8*2, r[0]&0xFFFF)
			return
		case 0x2:
			mem.W32(c.gbr+d8*4, r[0])
			return
		case 0x3:
			panic(IllegalInstruction{op, c.pc - 2}) // trapa
		case 0x4:
			r[0] = uint32(s8(mem.R8(c.gbr + d8)))
			return
		case 0x5:
			r[0] = uint32(s16(mem.R16(c.gbr + d8*2)))
			return
		case 0x6:
			r[0] = mem.R32(c.gbr + d8*4)
			return
		case 0x7: // mova
			r[0] = ((c.pc + 2) &^ 3) + d8*4
			return
		case 0x8:
			c.setT((r[0] & d8) == 0)
			return
		case 0x9:
			r[0] = r[0] & d8
			return
		case 0xA:
			r[0] = r[0] ^ d8
			return
		case 0xB:
			r[0] = r[0] | d8
			return
		}
		return

	case 0xD: // mov.l @(disp,PC),Rn
		ea := ((c.pc + 2) &^ 3) + d8*4
		r[n] = mem.R32(ea)
		return

	case 0xE: // mov #imm,Rn
		r[n] = uint32(s8(d8))
		return

	case 0xF: // FPU stub
		c.fpuOps++
		return
	}

	panic(IllegalInstruction{op, c.pc - 2})
}

func (c *CPU) exec4(op, n, m, d8 uint32) {
	r := &c.r
	mem := c.mem
	switch d8 {
	case 0x00: // shll
		c.setT(r[n]>>31 != 0)
		r[n] = r[n] << 1
		return
	case 0x01: // shlr
		c.setT(r[n]&1 != 0)
		r[n] = r[n] >> 1
		return
	case 0x04: // rotl
		t := r[n] >> 31
		r[n] = (r[n] << 1) | t
		c.setT(t != 0)
		return
	case 0x05: // rotr
		t := r[n] & 1
		r[n] = (r[n] >> 1) | (t << 31)
		c.setT(t != 0)
		return
	case 0x08:
		r[n] = r[n] << 2
		return
	case 0x09:
		r[n] = r[n] >> 2
		return
	case 0x18:
		r[n] = r[n] << 8
		return
	case 0x19:
		r[n] = r[n] >> 8
		return
	case 0x28:
		r[n] = r[n] << 16
		return
	case 0x29:
		r[n] = r[n] >> 16
		return
	case 0x20: // shal
		c.setT(r[n]>>31 != 0)
		r[n] = r[n] << 1
		return
	case 0x21: // shar
		c.setT(r[n]&1 != 0)
		r[n] = uint32(s32(r[n]) >> 1)
		return
	case 0x24: // rotcl
		t := r[n] >> 31
		r[n] = (r[n] << 1) | (c.sr & srT)
		c.setT(t != 0)
		return
	case 0x25: // rotcr
		t := r[n] & 1
		r[n] = (r[n] >> 1) | ((c.sr & srT) << 31)
		c.setT(t != 0)
		return
	case 0x10: // dt
		r[n] = r[n] - 1
		c.setT(r[n] == 0)
		return
	case 0x11:
		c.setT(s32(r[n]) >= 0)
		return
	case 0x15:
		c.setT(s32(r[n]) > 0)
		return
	case 0x0B: // jsr
		c.pr = c.pc + 2
		c.branchDelayed(r[n])
		return
	case 0x2B: // jmp
		c.branchDelayed(r[n])
		return
	case 0x0E:
		c.setSR(r[n])
		return
	case 0x1E:
		c.gbr = r[n]
		return
	case 0x2E:
		c.vbr = r[n]
		return
	case 0x3E:
		c.ssr = r[n]
		return
	case 0x4E:
		c.spc = r[n]
		return
	case 0x0A:
		c.mach = r[n]
		return
	case 0x1A:
		c.macl = r[n]
		return
	case 0x2A:
		c.pr = r[n]
		return
	case 0x5A:
		c.fpul = r[n]
		return
	case 0x6A:
		c.fpscr = r[n]
		return
	case 0x07:
		c.setSR(mem.R32(r[n]))
		r[n] += 4
		return
	case 0x17:
		c.gbr = mem.R32(r[n])
		r[n] += 4
		return
	case 0x27:
		c.vbr = mem.R32(r[n])
		r[n] += 4
		return
	case 0x37:
		c.ssr = mem.R32(r[n])
		r[n] += 4
		return
	case 0x47:
		c.spc = mem.R32(r[n])
		r[n] += 4
		return
	case 0x06:
		c.mach = mem.R32(r[n])
		r[n] += 4
		return
	case 0x16:
		c.macl = mem.R32(r[n])
		r[n] += 4
		return
	case 0x26:
		c.pr = mem.R32(r[n])
		r[n] += 4
		return
	case 0x56:
		c.fpul = mem.R32(r[n])
		r[n] += 4
		return
	case 0x66:
		c.fpscr = mem.R32(r[n])
		r[n] += 4
		return
	case 0x03:
		r[n] -= 4
		mem.W32(r[n], c.sr)
		return
	case 0x13:
		r[n] -= 4
		mem.W32(r[n], c.gbr)
		return
	case 0x23:
		r[n] -= 4
		mem.W32(r[n], c.vbr)
		return
	case 0x33:
		r[n] -= 4
		mem.W32(r[n], c.ssr)
		return
	case 0x43:
		r[n] -= 4
		mem.W32(r[n], c.spc)
		return
	case 0x02:
		r[n] -= 4
		mem.W32(r[n], c.mach)
		return
	case 0x12:
		r[n] -= 4
		mem.W32(r[n], c.macl)
		return
	case 0x22:
		r[n] -= 4
		mem.W32(r[n], c.pr)
		return
	case 0x52:
		r[n] -= 4
		mem.W32(r[n], c.fpul)
		return
	case 0x62:
		r[n] -= 4
		mem.W32(r[n], c.fpscr)
		return
	case 0x1B: // tas.b @Rn
		v := mem.R8(r[n])
		c.setT(v == 0)
		mem.W8(r[n], v|0x80)
		return
	}
	if (op & 0x8F) == 0x8E { // ldc Rn,Rm_bank
		c.rbank1[(op>>4)&7] = r[n]
		return
	}
	if (op & 0x8F) == 0x87 { // ldc.l @Rn+,Rm_bank
		c.rbank1[(op>>4)&7] = mem.R32(r[n])
		r[n] += 4
		return
	}
	if (op & 0x8F) == 0x83 { // stc.l Rm_bank,@-Rn
		r[n] -= 4
		mem.W32(r[n], c.rbank1[(op>>4)&7])
		return
	}
	d4 := op & 0xF
	if d4 == 0xC { // shad
		sh := s32(r[m])
		if sh >= 0 {
			r[n] = r[n] << (uint32(sh) & 0x1F)
		} else {
			sa := uint32(-sh) & 0x1F
			if sh == -32 {
				sa = 31
			}
			r[n] = uint32(s32(r[n]) >> sa)
		}
		return
	}
	if d4 == 0xD { // shld
		sh := s32(r[m])
		if sh >= 0 {
			r[n] = r[n] << (uint32(sh) & 0x1F)
		} else if sh == -32 {
			r[n] = 0
		} else {
			r[n] = r[n] >> (uint32(-sh) & 0x1F)
		}
		return
	}
	panic(IllegalInstruction{op, c.pc - 2})
}

func (c *CPU) div1(n, m uint32) {
	r := &c.r
	Rm := r[m]
	Rn := r[n]
	oldQ := uint32(0)
	if c.sr&srQ != 0 {
		oldQ = 1
	}
	M := uint32(0)
	if c.sr&srM != 0 {
		M = 1
	}
	T := uint32(0)
	if c.sr&srT != 0 {
		T = 1
	}
	Q := (Rn >> 31) & 1
	Rn = (Rn << 1) | T
	var tmp0, tmp1 uint32
	if oldQ == 0 {
		if M == 0 {
			tmp0 = Rn
			Rn = Rn - Rm
			tmp1 = b2u(Rn > tmp0)
			if Q == 0 {
				Q = tmp1
			} else {
				Q = tmp1 ^ 1
			}
		} else {
			tmp0 = Rn
			Rn = Rn + Rm
			tmp1 = b2u(Rn < tmp0)
			if Q == 0 {
				Q = tmp1 ^ 1
			} else {
				Q = tmp1
			}
		}
	} else {
		if M == 0 {
			tmp0 = Rn
			Rn = Rn + Rm
			tmp1 = b2u(Rn < tmp0)
			if Q == 0 {
				Q = tmp1
			} else {
				Q = tmp1 ^ 1
			}
		} else {
			tmp0 = Rn
			Rn = Rn - Rm
			tmp1 = b2u(Rn > tmp0)
			if Q == 0 {
				Q = tmp1 ^ 1
			} else {
				Q = tmp1
			}
		}
	}
	r[n] = Rn
	if Q != 0 {
		c.sr |= srQ
	} else {
		c.sr &^= srQ
	}
	c.setT(Q == M)
}

func b2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
