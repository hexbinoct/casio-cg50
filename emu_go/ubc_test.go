package main

import "testing"

// UBC (ubc.go) unit tests: small programs in DRAM, the break handler at DBR is `rte; nop`.
const (
	ubcCode    = 0x8C001000
	ubcData    = 0x8C010000
	ubcHandler = 0x8C002000 // DBR
	ubcVBR     = 0x8C003000
)

func newUBCRig(t *testing.T, code ...uint32) (*Memory, *CPU) {
	t.Helper()
	mmio := NewMMIOBus()
	mem := NewMemory(make([]byte, 0x1000), mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	for i, op := range code {
		mem.W16(ubcCode+uint32(2*i), op)
	}
	for _, h := range []uint32{ubcHandler, ubcVBR + 0x100} {
		mem.W16(h, 0x002B) // rte
		mem.W16(h+2, 0x0009)
	}
	cpu.pc, cpu.sr, cpu.vbr, cpu.dbr = ubcCode, srMD, ubcVBR, ubcHandler
	cpu.r[15] = 0x8C0F0000
	mem.Write(ubcBase+ubcCBCR, 4, 1) // UBDE: breaks go to DBR
	return mem, cpu
}

// fetchBreak programs channel ch as gint's ubc_set_breakpoint does.
func fetchBreak(mem *Memory, ch int, addr uint32, after bool) {
	o := ubcBase + uint32(ch)*0x20
	crr := uint32(crrBIE)
	if after {
		crr |= crrPCB
	}
	mem.Write(o+4, 4, crr)
	mem.Write(o+8, 4, addr)
	mem.Write(o+0xC, 4, 0)
	mem.Write(o, 4, 1<<4|1<<1|cbrCE) // ID = fetch, RW = read
}

func steps(cpu *CPU, n int) {
	for i := 0; i < n; i++ {
		cpu.step()
	}
}

func wantBreak(t *testing.T, cpu *CPU, mem *Memory, spc uint32) {
	t.Helper()
	if cpu.pc != ubcHandler || cpu.spc != spc {
		t.Fatalf("pc=%#x spc=%#x, want a break to %#x with spc %#x", cpu.pc, cpu.spc, ubcHandler, spc)
	}
	if cpu.sr&(srMD|srRB|srBL) != srMD|srRB|srBL || cpu.sgr != cpu.r[15] {
		t.Fatalf("sr=%#x sgr=%#x", cpu.sr, cpu.sgr)
	}
	if v := mem.Read(0xFF000024, 4); v != expevtUBC {
		t.Fatalf("EXPEVT=%#x", v)
	}
}

func TestUBCBreakBefore(t *testing.T) {
	// mov #1,r0; mov #2,r1; add r1,r0; nop
	mem, cpu := newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	fetchBreak(mem, 0, ubcCode+4, false)
	if !cpu.ubcOn || mem.Read(ubcBase+4, 4)&0x2000 == 0 {
		t.Fatalf("ubcOn=%v CRR0=%#x", cpu.ubcOn, mem.Read(ubcBase+4, 4))
	}
	steps(cpu, 3)
	wantBreak(t, cpu, mem, ubcCode+4)
	if cpu.ssr != srMD || mem.Read(ubcBase+ubcCCMFR, 4) != 1 || cpu.rbank1[0] != 1 {
		t.Fatalf("ssr=%#x CCMFR=%#x r0=%#x (add must not have run)", cpu.ssr, mem.Read(ubcBase+ubcCCMFR, 4), cpu.rbank1[0])
	}
	// the handler returns to the breakpoint: it breaks again (a debugger steps over it)
	steps(cpu, 2)
	wantBreak(t, cpu, mem, ubcCode+4)
	if cpu.ubc.breaks != 2 {
		t.Fatalf("breaks=%d", cpu.ubc.breaks)
	}
	mem.Write(ubcBase, 4, 0) // channel off (CE=0): the CPU leaves the slow path
	if cpu.ubcOn {
		t.Fatal("ubcOn after disabling the channel")
	}
	steps(cpu, 2)
	if cpu.pc != ubcCode+6 || cpu.r[0] != 3 || cpu.sr != srMD {
		t.Fatalf("after continue: pc=%#x r0=%d sr=%#x", cpu.pc, cpu.r[0], cpu.sr)
	}
}

func TestUBCBreakAfter(t *testing.T) {
	mem, cpu := newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	fetchBreak(mem, 1, ubcCode+2, true)
	steps(cpu, 2)
	wantBreak(t, cpu, mem, ubcCode+4)
	steps(cpu, 1) // rte
	if cpu.pc != ubcCode+4 || cpu.r[1] != 2 {
		t.Fatalf("pc=%#x r1=%d", cpu.pc, cpu.r[1])
	}
	steps(cpu, 1) // add: no re-break (the matched instruction is behind)
	if cpu.pc != ubcCode+6 || cpu.r[0] != 3 {
		t.Fatalf("pc=%#x r0=%d", cpu.pc, cpu.r[0])
	}
}

func TestUBCDelaySlot(t *testing.T) {
	// mov #1,r0; bra +8 (to ubcCode+8); mov #5,r2 (slot); nop; mov #7,r3
	code := []uint32{0xE001, 0xA001, 0xE205, 0x0009, 0xE307, 0x0009}
	for _, tc := range []struct {
		name      string
		addr      uint32
		after     bool
		spc, slot uint32 // r2 = 5 once the slot ran
	}{
		{"before the slot = before the branch", ubcCode + 4, false, ubcCode + 2, 0},
		{"after the branch = at its target", ubcCode + 2, true, ubcCode + 8, 5},
		{"after the slot = at the target", ubcCode + 4, true, ubcCode + 8, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem, cpu := newUBCRig(t, code...)
			fetchBreak(mem, 0, tc.addr, tc.after)
			steps(cpu, 2)
			wantBreak(t, cpu, mem, tc.spc)
			if cpu.rbank1[2] != tc.slot {
				t.Fatalf("r2=%d, want %d", cpu.rbank1[2], tc.slot)
			}
		})
	}
}

func TestUBCVectorAndGating(t *testing.T) {
	mem, cpu := newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	mem.Write(ubcBase+ubcCBCR, 4, 0) // UBDE=0: the general exception vector
	fetchBreak(mem, 0, ubcCode+2, false)
	steps(cpu, 2)
	if cpu.pc != ubcVBR+0x100 || cpu.spc != ubcCode+2 || mem.Read(0xFF000024, 4) != expevtUBC {
		t.Fatalf("pc=%#x spc=%#x", cpu.pc, cpu.spc)
	}

	// SR.BL=1: no match at all
	mem, cpu = newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	cpu.sr |= srBL
	fetchBreak(mem, 0, ubcCode+2, false)
	steps(cpu, 3)
	if cpu.pc != ubcCode+6 || mem.Read(ubcBase+ubcCCMFR, 4) != 0 {
		t.Fatalf("BL=1: pc=%#x CCMFR=%#x", cpu.pc, mem.Read(ubcBase+ubcCCMFR, 4))
	}

	// module stopped (MSTPCR0.UDB)
	mem, cpu = newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	mem.Write(0xA4150030, 4, mstpcr0UDB)
	fetchBreak(mem, 0, ubcCode+2, false)
	steps(cpu, 3)
	if cpu.pc != ubcCode+6 {
		t.Fatalf("UDB stopped: pc=%#x", cpu.pc)
	}

	// BIE=0: the flag is set, no break
	mem, cpu = newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	fetchBreak(mem, 1, ubcCode+2, false)
	mem.Write(ubcBase+0x24, 4, 0)
	steps(cpu, 3)
	if cpu.pc != ubcCode+6 || mem.Read(ubcBase+ubcCCMFR, 4) != 2 {
		t.Fatalf("BIE=0: pc=%#x CCMFR=%#x", cpu.pc, mem.Read(ubcBase+ubcCCMFR, 4))
	}
}

func TestUBCOperandWatch(t *testing.T) {
	// mov.l r1,@r2; mov.l @r2,r3; nop; nop
	code := []uint32{0x2212, 0x6322, 0x0009, 0x0009}
	// channel 1: operand access (ID=10) at ubcData, long (SZ=3), with RW and data value per case
	watch := func(mem *Memory, rw uint32, dbe bool, cdr uint32) {
		cbr := uint32(2<<4 | rw<<1 | 3<<12 | cbrCE)
		if dbe {
			cbr |= cbrDBE
		}
		mem.Write(ubcBase+0x24, 4, crrBIE)
		mem.Write(ubcBase+0x28, 4, ubcData)
		mem.Write(ubcBase+ubcCDR1, 4, cdr)
		mem.Write(ubcBase+0x20, 4, cbr)
	}
	run := func(rw uint32, dbe bool, cdr, r1 uint32) (*Memory, *CPU) {
		mem, cpu := newUBCRig(t, code...)
		cpu.r[1], cpu.r[2] = r1, ubcData
		watch(mem, rw, dbe, cdr)
		return mem, cpu
	}

	mem, cpu := run(2, true, 0x1234, 0x1234) // write of the watched value: break after it
	steps(cpu, 1)
	wantBreak(t, cpu, mem, ubcCode+2)
	if mem.Read(ubcData, 4) != 0x1234 {
		t.Fatal("the write must have completed")
	}

	_, cpu = run(2, true, 0x1234, 0x9999) // another value: no break
	steps(cpu, 3)
	if cpu.pc != ubcCode+6 {
		t.Fatalf("data mismatch broke: pc=%#x", cpu.pc)
	}

	mem, cpu = run(1, false, 0, 0x55) // read watch: the write passes, the read breaks
	steps(cpu, 2)
	wantBreak(t, cpu, mem, ubcCode+4)
	if cpu.rbank1[3] != 0x55 {
		t.Fatalf("r3=%#x", cpu.rbank1[3])
	}

	// size condition: a byte access to the address doesn't match SZ=long
	mem, cpu = newUBCRig(t, 0x2210, 0x0009) // mov.b r1,@r2
	cpu.r[2] = ubcData
	watch(mem, 0, false, 0)
	steps(cpu, 2)
	if cpu.pc != ubcCode+4 {
		t.Fatalf("byte access broke a long watch: pc=%#x", cpu.pc)
	}
}

func TestUBCExecutionCount(t *testing.T) {
	// loop: add #1,r4; bra loop; nop
	mem, cpu := newUBCRig(t, 0x7401, 0xAFFD, 0x0009)
	mem.Write(ubcBase+ubcCETR1, 4, 3)
	fetchBreak(mem, 1, ubcCode, false)
	mem.Write(ubcBase+0x20, 4, mem.Read(ubcBase+0x20, 4)|cbrETBE)
	steps(cpu, 5) // two passes, then the third arrival breaks
	wantBreak(t, cpu, mem, ubcCode)
	if cpu.rbank1[4] != 2 {
		t.Fatalf("r4=%d, want 2", cpu.rbank1[4])
	}
}

func TestUBCSaveState(t *testing.T) {
	mem, cpu := newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	fetchBreak(mem, 0, ubcCode+4, false)
	regs, ubcRegs := cpu.snapshotRegs(), cpu.mem.mmio.regionRegs()

	mem2, cpu2 := newUBCRig(t, 0xE001, 0xE102, 0x301C, 0x0009)
	cpu2.dbr = 0
	cpu2.restoreRegs(regs)
	mem2.mmio.restoreRegionRegs(ubcRegs)
	if cpu2.dbr != ubcHandler || !cpu2.ubcOn {
		t.Fatalf("dbr=%#x ubcOn=%v", cpu2.dbr, cpu2.ubcOn)
	}
	steps(cpu2, 3)
	wantBreak(t, cpu2, mem2, ubcCode+4)

	cpu2.restoreRegs(regs[:36]) // a state from before DBR was saved
	if cpu2.dbr != 0 {
		t.Fatalf("dbr=%#x from a 36-register state", cpu2.dbr)
	}
}

func TestUBCConditionalDelayNotTaken(t *testing.T) {
	// clrt; bt/s +8 (not taken: T=0); mov #5,r2 (its slot); mov #7,r3
	for _, addr := range []uint32{ubcCode + 2, ubcCode + 4} {
		mem, cpu := newUBCRig(t, 0x0008, 0x8D01, 0xE205, 0xE307, 0x0009)
		fetchBreak(mem, 0, addr, true)
		steps(cpu, 2)
		wantBreak(t, cpu, mem, ubcCode+6) // after the branch AND its slot
		if cpu.rbank1[2] != 5 || cpu.rbank1[3] != 0 {
			t.Fatalf("break after %#x: r2=%d r3=%d", addr, cpu.rbank1[2], cpu.rbank1[3])
		}
	}
}

func TestUBCAfterBreakDecidedAtFetch(t *testing.T) {
	// mov.l r1,@r2 with r2 = CBR1, r1 = 0: the stepped instruction disables its own channel;
	// the break after it still happens (as on the real calculator)
	mem, cpu := newUBCRig(t, 0x2212, 0x0009, 0x0009)
	cpu.r[1], cpu.r[2] = 0, ubcBase+0x20
	fetchBreak(mem, 1, ubcCode, true)
	steps(cpu, 1)
	wantBreak(t, cpu, mem, ubcCode+2)
	if cpu.ubcOn {
		t.Fatal("the channel should be off now")
	}
}
