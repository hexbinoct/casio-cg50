package main

// ubc.go — the SH7305's User Break Controller (UBC, SH-4A) at 0xFF200000 and its use of the
// debug base register DBR (cpu.go), for the on-device debugger (cg50_addons notes/debugger.md).
// The OS never touches either; gint has a driver (<gint/ubc.h>, used by its GDB stub).
//
// Registers (the SH7724 layout of gint's <gint/mpu/ubc.h>): channel 0 CBR0 +0x00, CRR0 +0x04,
// CAR0 +0x08, CAMR0 +0x0C; channel 1 CBR1 +0x20, CRR1 +0x24, CAR1 +0x28, CAMR1 +0x2C, CDR1 +0x30,
// CDMR1 +0x34, CETR1 +0x38; CCMFR +0x600 (match flags MF0 bit 0, MF1 bit 1); CBCR +0x620 (UBDE
// bit 0). CBR: CE bit 0 (enable), RW 2:1 (01 read, 10 write, 00/11 either), ID 5:4 (01
// instruction fetch, 10 operand access, 00/11 either), SZ 14:12 (0 any, 1 byte, 2 word, 3 long),
// ETBE bit 11 and DBE bit 15 (channel 1 only: execution count, data value), AIE bit 30 + AIV
// 23:16 (match only under that ASID), MFE bit 31 + MFI 29:24 (match only while CCMFR flag MFI
// is set: a sequential break). CRR: BIE bit 0 (break on a match, else only set the flag), PCB
// bit 1 (instruction fetch: 0 = before, 1 = after execution); bit 13 always reads 1.
// CAMR/CDMR1: 1 bits are left out of the compare.
//
// Model:
//   - a match sets its CCMFR flag; with BIE the CPU takes a user break: SSR = SR, SPC, SGR = R15,
//     SR.MD/RB/BL = 1, EXPEVT = 0x1E0, PC = DBR if CBCR.UBDE, else VBR+0x100;
//   - matches are decided at the instruction's fetch (a break after it is taken even if the
//     instruction disables the channel: DASM stepping through its own UBC code, on the calculator);
//   - instruction fetch, before: SPC = the matched instruction, not executed; set on a delay slot,
//     the break comes before its branch. The break re-triggers when the handler returns to it
//     (a debugger steps over it with its other channel, as gint's GDB stub does). After: SPC =
//     the next instruction to run; on a delayed branch or its slot, after both (the target,
//     or for a bt/s / bf/s not taken the instruction after the slot);
//   - operand access: the logical address (and size, direction, channel 1's data value) of each
//     CPU access; the break is taken after the instruction (SPC = the next one). DMA and other
//     bus masters are not seen. An instruction that faults (MMU) takes no break;
//   - nothing matches while SR.BL = 1 (exception and interrupt handlers before they unblock, the
//     break handler itself) or while MSTPCR0.UDB (bit 17) stops the module;
//   - channel 1's execution count (ETBE): each match decrements CETR1 until the match that finds
//     it at 1, which breaks (so CETR1 = n breaks on the n-th match).
// Cost: nothing while no channel is enabled. Enabling one makes the CPU take stepUBC, a slower
// step with the checks, until all channels are disabled again (CPU.ubcOn).

const (
	ubcBase  = 0xFF200000
	ubcCDR1  = 0x30
	ubcCDMR1 = 0x34
	ubcCETR1 = 0x38
	ubcCCMFR = 0x600
	ubcCBCR  = 0x620

	cbrCE   = 1 << 0
	cbrETBE = 1 << 11
	cbrDBE  = 1 << 15
	cbrAIE  = 1 << 30
	cbrMFE  = 1 << 31
	crrBIE  = 1 << 0
	crrPCB  = 1 << 1

	mstpcr0UDB = 1 << 17 // CPG MSTPCR0 (0xA4150030): UBC module stopped
	expevtUBC  = 0x1E0
)

type ubcChan struct{ cbr, crr, car, camr uint32 }

func (k *ubcChan) addr(a uint32) bool { return (a^k.car)&^k.camr == 0 }

type ubcUnit struct {
	base               // the registers by offset (save-states use this map)
	ch      [2]ubcChan // decoded copy of the channel registers (update)
	opArmed bool       // an enabled channel watches operand accesses
	opHit   bool       // an operand access in the current instruction asked for a break
	cpu     *CPU
	cpg     *cpg // MSTPCR0
	breaks  uint64
}

func newUBC(cpg *cpg) *ubcUnit {
	u := &ubcUnit{base: newBase("UBC", ubcBase, 0x1000), cpg: cpg}
	// reset values (the MFI field's reset value is a reserved one: gint rewrites it)
	u.regs[0x00], u.regs[0x20] = 0x20000000, 0x20000000
	u.update()
	return u
}

func (u *ubcUnit) read(va, size uint32) uint32 {
	off := va - u.bs
	v := u.regs[off]
	if off == 0x04 || off == 0x24 {
		v |= 0x2000
	}
	return v
}

func (u *ubcUnit) write(va, size, val uint32) {
	u.regs[va-u.bs] = val
	u.update()
}

func (u *ubcUnit) restore(m map[uint32]uint32) {
	for k := range u.regs {
		delete(u.regs, k)
	}
	for k, v := range m {
		u.regs[k] = v
	}
	u.update()
}

// update re-reads the channel registers and tells the CPU whether any channel is enabled.
func (u *ubcUnit) update() {
	fetch, op := false, false
	for i := range u.ch {
		o := uint32(i) * 0x20
		k := ubcChan{u.regs[o], u.regs[o+4], u.regs[o+8], u.regs[o+0xC]}
		u.ch[i] = k
		if k.cbr&cbrCE != 0 {
			id := (k.cbr >> 4) & 3
			fetch = fetch || id != 2
			op = op || id != 1
		}
	}
	u.opArmed = op
	if u.cpu != nil {
		u.cpu.ubcOn = fetch || op
	}
}

func (u *ubcUnit) powered() bool {
	return u.cpg == nil || u.cpg.regs[0x30]&mstpcr0UDB == 0
}

// cond checks a channel's ASID and match-flag conditions.
func (u *ubcUnit) cond(i int) bool {
	cbr := u.ch[i].cbr
	if cbr&cbrAIE != 0 {
		if u.cpu == nil || u.cpu.mem.mmio == nil || u.cpu.mem.mmio.ccn.regs[ccnPTEH]&0xFF != (cbr>>16)&0xFF {
			return false
		}
	}
	if cbr&cbrMFE != 0 && u.regs[ubcCCMFR]&(1<<((cbr>>24)&0x3F)) == 0 {
		return false
	}
	return true
}

// hit records a match of channel i and reports whether it requests a break.
func (u *ubcUnit) hit(i int) bool {
	u.regs[ubcCCMFR] |= 1 << i
	if i == 1 && u.ch[1].cbr&cbrETBE != 0 {
		if cet := u.regs[ubcCETR1] & 0xFFF; cet > 1 {
			u.regs[ubcCETR1] = cet - 1
			return false
		}
	}
	return u.ch[i].crr&crrBIE != 0
}

// fetchMatch checks the instruction at pc against the instruction-fetch channels that break
// before (after = false) or after it; a delayed branch also matches on its delay slot.
func (u *ubcUnit) fetchMatch(c *CPU, pc uint32, after bool) bool {
	brk := false
	for i := range u.ch {
		k := &u.ch[i]
		if k.cbr&cbrCE == 0 || (k.cbr>>4)&3 == 2 || (k.cbr>>1)&3 == 2 || (k.crr&crrPCB != 0) != after {
			continue
		}
		if !k.addr(pc) && !(k.addr(pc+2) && c.delayedAt(pc)) {
			continue
		}
		if u.cond(i) && u.hit(i) {
			brk = true
		}
	}
	return brk
}

// operand checks one CPU data access (va = its logical address; val = the value read or
// written) against the operand channels.
func (u *ubcUnit) operand(va, size, val uint32, write bool) {
	for i := range u.ch {
		k := &u.ch[i]
		if k.cbr&cbrCE == 0 || (k.cbr>>4)&3 == 1 {
			continue
		}
		if rw := (k.cbr >> 1) & 3; (rw == 1 && write) || (rw == 2 && !write) {
			continue
		}
		if sz := (k.cbr >> 12) & 7; sz != 0 && (sz > 3 || size != 1<<(sz-1)) {
			continue
		}
		if !k.addr(va) {
			continue
		}
		if i == 1 && k.cbr&cbrDBE != 0 {
			mask := uint32(1)<<(size*8) - 1
			if size == 4 {
				mask = 0xFFFFFFFF
			}
			if (val^u.regs[ubcCDR1])&^u.regs[ubcCDMR1]&mask != 0 {
				continue
			}
		}
		if u.cond(i) && u.hit(i) {
			u.opHit = true
		}
	}
}

// isDelayedBranch: rts, rte, bsrf, braf, jsr, jmp, bra, bsr, bt/s, bf/s.
func isDelayedBranch(op uint32) bool {
	switch {
	case op == 0x000B || op == 0x002B:
		return true
	case op&0xF0FF == 0x0003 || op&0xF0FF == 0x0023 || op&0xF0FF == 0x400B || op&0xF0FF == 0x402B:
		return true
	case op&0xE000 == 0xA000 || op&0xFD00 == 0x8D00:
		return true
	}
	return false
}

// delayedAt reports whether the instruction at pc is a delayed branch (false if it can't be
// fetched: the fetch's own MMU exception comes first).
func (c *CPU) delayedAt(pc uint32) bool {
	return isDelayedBranch(c.peekOp(pc))
}

// peekOp reads the instruction at pc, or 0 (a nop-like non-branch) if it can't be fetched.
func (c *CPU) peekOp(pc uint32) (op uint32) {
	ops := c.mem.ubcOps
	c.mem.ubcOps = false
	defer func() {
		c.mem.ubcOps = ops
		if recover() != nil {
			op = 0
		}
	}()
	return c.mem.fetch16(pc)
}

// stepUBC executes one instruction with the user-break checks: the step taken while a UBC
// channel is enabled.
func (c *CPU) stepUBC() {
	u := c.ubc
	pc0 := c.pc
	live := c.sr&srBL == 0 && u.powered()
	if live && u.fetchMatch(c, pc0, false) {
		c.userBreak(pc0)
		return
	}
	// a break after the instruction is decided when it is fetched: the instruction itself can
	// disable the channel (a debugger stepping its own UBC code) and still breaks (the real
	// calculator: DASM's single steps carry on through its own `CBR1 = 0`)
	after := live && u.fetchMatch(c, pc0, true)
	u.opHit = false
	c.faulted = false
	c.mem.ubcOps = live && u.opArmed
	defer func() { c.mem.ubcOps = false }()
	if c.mem.mmuAt {
		c.stepMMU()
	} else {
		op := c.mem.fetch16(c.pc)
		c.pc += 2
		c.execute(op)
	}
	if c.faulted {
		return
	}
	if op := c.peekOp(pc0); op&0xFD00 == 0x8D00 && c.pc == pc0+2 {
		// a bt/s or bf/s not taken still runs its slot as a delay slot: one step with it (the
		// plain step runs the slot as the next instruction, which only matters here)
		if c.mem.mmuAt {
			c.stepMMU()
		} else {
			slot := c.mem.fetch16(c.pc)
			c.pc += 2
			c.execute(slot)
		}
	}
	c.mem.ubcOps = false
	if !live || c.faulted || c.sleeping {
		return
	}
	if after || u.opHit {
		c.userBreak(c.pc)
	}
}

// userBreak enters the user-break handler, resuming later at spc.
func (c *CPU) userBreak(spc uint32) {
	c.ssr, c.spc, c.sgr = c.sr, spc, c.r[15]
	c.mem.Write(0xFF000024, 4, expevtUBC)
	c.setSR(c.sr | srMD | srRB | srBL)
	if c.ubc.regs[ubcCBCR]&1 != 0 {
		c.pc = c.dbr
	} else {
		c.pc = c.vbr + 0x100
	}
	c.ubc.breaks++
}
