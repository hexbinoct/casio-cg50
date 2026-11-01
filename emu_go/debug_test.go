package cg50emu

// Tests of the host debugger (debug.go): small hand-assembled programs in DRAM, and the real
// OS for the parts that need it (interrupts, the syscall table, host calls, equivalence of the
// debug path with the plain one).

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

const (
	dbgCode = 0x8C001000
	dbgVBR  = 0x8C003000
	dbgSP   = 0x8C0F0000
)

// newDbgRig builds a machine with code at dbgCode, `rte; nop` at the three exception and
// interrupt vectors, privileged mode, bank 0, interrupts masked (IMASK 15).
func newDbgRig(t *testing.T, code ...uint32) *Emulator {
	t.Helper()
	e := NewEmulator(make([]byte, 0x1000))
	for i, op := range code {
		e.mem.W16(dbgCode+uint32(2*i), op)
	}
	for _, v := range []uint32{0x100, 0x400, 0x600} {
		e.mem.W16(dbgVBR+v, 0x002B)
		e.mem.W16(dbgVBR+v+2, 0x0009)
	}
	c := e.cpu
	c.setSR(srMD | srIMASK)
	c.pc, c.vbr, c.r[15] = dbgCode, dbgVBR, dbgSP
	return e
}

// branch encodings (target = the branch's address + 4 + disp*2)
func bsrTo(from, to uint32) uint32 { return 0xB000 | ((to-from-4)/2)&0xFFF }
func braTo(from, to uint32) uint32 { return 0xA000 | ((to-from-4)/2)&0xFFF }
func bfTo(from, to uint32) uint32  { return 0x8B00 | ((to-from-4)/2)&0xFF }
func btTo(from, to uint32) uint32  { return 0x8900 | ((to-from-4)/2)&0xFF }

func wantStop(t *testing.T, s Stop, reason StopReason, pc uint32) {
	t.Helper()
	if s.Reason != reason || s.PC != pc {
		t.Fatalf("stop = %v, want %v at %08x", s, reason, pc)
	}
}

// program A: a call to a leaf function, with work in both delay slots
//
//	1000 mov #1,r1      1002 bsr f       1004 mov #2,r2 (slot)   1006 mov #3,r3
//	1008 bra .          100a nop
//	f: 1020 mov #7,r7   1022 rts         1024 mov #8,r8 (slot)
func progCall() []uint32 {
	p := make([]uint32, 0x14)
	copy(p, []uint32{0xE101, bsrTo(0x1002, 0x1020), 0xE202, 0xE303, braTo(0x1008, 0x1008), 0x0009})
	copy(p[0x10:], []uint32{0xE707, 0x000B, 0xE808, 0x0009})
	return p
}

func TestDebugStepIntoDelaySlots(t *testing.T) {
	e := newDbgRig(t, progCall()...)
	wantStop(t, e.StepInto(), StopStep, 0x8C001002)
	// the bsr and its slot are one step: at f with r2 = 2 and PR = the instruction after the slot
	s := e.StepInto()
	r := e.Registers()
	wantStop(t, s, StopStep, 0x8C001020)
	if r.R[2] != 2 || r.PR != 0x8C001006 || r.R[1] != 1 {
		t.Fatalf("after bsr: r2=%d pr=%08x r1=%d", r.R[2], r.PR, r.R[1])
	}
	wantStop(t, e.StepInto(), StopStep, 0x8C001022)
	wantStop(t, e.StepInto(), StopStep, 0x8C001006) // rts + slot
	if r := e.Registers(); r.R[7] != 7 || r.R[8] != 8 {
		t.Fatalf("after rts: r7=%d r8=%d", r.R[7], r.R[8])
	}
	// stopped between steps: Step does nothing
	c0 := e.Cycles()
	e.Step(1000)
	if e.Cycles() != c0 {
		t.Fatal("Step ran while stopped")
	}
}

func TestDebugStepIntoBtsNotTaken(t *testing.T) {
	// clrt; bt/s +4 (not taken); mov #1,r6 (slot); mov #2,r7
	e := newDbgRig(t, 0x0008, 0x8D02, 0xE601, 0xE702, braTo(0x1008, 0x1008), 0x0009)
	e.StepInto()
	wantStop(t, e.StepInto(), StopStep, 0x8C001006)
	if r := e.Registers(); r.R[6] != 1 || r.R[7] != 0 {
		t.Fatalf("r6=%d r7=%d: the slot is part of the step, the next instruction is not", r.R[6], r.R[7])
	}
}

func TestDebugStepOverCall(t *testing.T) {
	e := newDbgRig(t, progCall()...)
	wantStop(t, e.StepOver(), StopStep, 0x8C001002) // not a call: one step
	s := e.StepOver()
	wantStop(t, s, StopStep, 0x8C001006)
	if r := e.Registers(); r.R[2] != 2 || r.R[7] != 7 || r.R[8] != 8 {
		t.Fatalf("r2=%d r7=%d r8=%d", r.R[2], r.R[7], r.R[8])
	}
}

// program B: main calls rec, which recurses r4 = 5 levels deep
//
//	1000 mov #5,r4    1002 bsr rec    1004 nop    1006 mov #3,r3    1008 bra .   100a nop
//	rec: 1040 sts.l pr,@-r15   1042 dt r4   1044 bt done   1046 bsr rec   1048 nop
//	done: 104a lds.l @r15+,pr  104c rts     104e nop
func progRec() []uint32 {
	p := make([]uint32, 0x28)
	copy(p, []uint32{0xE405, bsrTo(0x1002, 0x1040), 0x0009, 0xE303, braTo(0x1008, 0x1008), 0x0009})
	copy(p[0x20:], []uint32{0x4F22, 0x4410, btTo(0x1044, 0x104A), bsrTo(0x1046, 0x1040), 0x0009, 0x4F26, 0x000B, 0x0009})
	return p
}

func TestDebugStepOverRecursion(t *testing.T) {
	e := newDbgRig(t, progRec()...)
	e.StepInto()
	s := e.StepOver() // the outer call returns once, after all five levels
	wantStop(t, s, StopStep, 0x8C001006)
	if r := e.Registers(); r.R[4] != 0 || r.R[15] != dbgSP {
		t.Fatalf("r4=%d r15=%08x", r.R[4], r.R[15])
	}
}

func TestDebugBreakpointHitsAndStepOut(t *testing.T) {
	e := newDbgRig(t, progRec()...)
	id := e.AddBreakpoint(0x8C001042) // dt r4: once per level
	for level := 1; level <= 5; level++ {
		s := e.Run(10_000)
		wantStop(t, s, StopBreakpoint, 0x8C001042)
		if s.Breakpoint != id || e.Registers().R[4] != uint32(6-level) {
			t.Fatalf("level %d: %v r4=%d", level, s, e.Registers().R[4])
		}
		if level < 5 {
			e.Continue()
		}
	}
	if b := e.Breakpoints(); len(b) != 1 || b[0].Hits != 5 {
		t.Fatalf("breakpoints %+v", b)
	}
	// the call stack at the deepest level: five return addresses saved on the stack
	fr := e.CallStack(0)
	want := []uint32{0x8C001042, 0x8C00104A, 0x8C00104A, 0x8C00104A, 0x8C00104A, 0x8C001006}
	if len(fr) != len(want) {
		t.Fatalf("call stack %+v", fr)
	}
	for i, f := range fr {
		if f.PC != want[i] || (i > 0 && (f.Source != FrameStack || !f.Confirmed)) {
			t.Fatalf("frame %d = %+v, want pc %08x from the stack", i, f, want[i])
		}
	}
	// step out of level 5: back in level 4, after its bsr
	sp := e.Registers().R[15]
	s := e.StepOut()
	wantStop(t, s, StopStep, 0x8C00104A)
	if r := e.Registers(); r.R[15] != sp+4 {
		t.Fatalf("r15=%08x, want %08x (level 5's frame popped)", r.R[15], sp+4)
	}
	e.RemoveBreakpoint(id)
	for i := 0; i < 4; i++ { // levels 4..1
		e.StepOut()
	}
	if s, _ := e.Stopped(); s.PC != 0x8C001006 || e.Registers().R[15] != dbgSP {
		t.Fatalf("after stepping out of every level: %v r15=%08x", s, e.Registers().R[15])
	}
}

func TestDebugCallStackLeaf(t *testing.T) {
	e := newDbgRig(t, progCall()...)
	e.RunTo(0x8C001022) // inside f, which never saves PR
	fr := e.CallStack(0)
	if len(fr) != 2 || fr[1].PC != 0x8C001006 || fr[1].Source != FramePR || !fr[1].Confirmed {
		t.Fatalf("call stack %+v", fr)
	}
}

// program D: a loop of ten
//
//	1000 mov #10,r4   1002 add #1,r5   1004 dt r4   1006 bf 1002   1008 bra .   100a nop
func progLoop() []uint32 {
	return []uint32{0xE40A, 0x7501, 0x4410, bfTo(0x1006, 0x1002), braTo(0x1008, 0x1008), 0x0009}
}

func TestDebugBreakpointLoop(t *testing.T) {
	e := newDbgRig(t, progLoop()...)
	id := e.AddBreakpoint(0x8C001002)
	n := 0
	for {
		s := e.Run(1000)
		if s.Reason == StopNone {
			break
		}
		wantStop(t, s, StopBreakpoint, 0x8C001002)
		if e.Registers().R[5] != uint32(n) {
			t.Fatalf("hit %d with r5=%d", n, e.Registers().R[5])
		}
		n++
		e.Continue()
	}
	if n != 10 {
		t.Fatalf("%d hits, want 10", n)
	}

	// ignore count and condition
	e = newDbgRig(t, progLoop()...)
	id = e.AddBreakpoint(0x8C001002)
	e.SetBreakpointIgnore(id, 3)
	if s := e.Run(1000); s.Reason != StopBreakpoint || e.Registers().R[5] != 3 {
		t.Fatalf("ignore 3: %v r5=%d", s, e.Registers().R[5])
	}
	e.SetBreakpointCondition(id, func(r *Regs) bool { return r.R[5] == 7 })
	e.Continue()
	if s := e.Run(1000); s.Reason != StopBreakpoint || e.Registers().R[5] != 7 {
		t.Fatalf("condition: %v r5=%d", s, e.Registers().R[5])
	}
	e.EnableBreakpoint(id, false)
	e.Continue()
	if s := e.Run(1000); s.Reason != StopNone || e.Registers().R[5] != 10 {
		t.Fatalf("disabled: %v r5=%d", s, e.Registers().R[5])
	}
}

func TestDebugRunToAndPause(t *testing.T) {
	e := newDbgRig(t, progLoop()...)
	wantStop(t, e.RunTo(0x8C001008), StopStep, 0x8C001008)
	if e.Registers().R[5] != 10 {
		t.Fatal("run-to stopped early")
	}
	e.Continue()
	e.Pause()
	c0 := e.Cycles()
	if s := e.Run(1000); s.Reason != StopPause || e.Cycles() != c0 {
		t.Fatalf("paused: %v, cycles %d -> %d", s, c0, e.Cycles())
	}
	e.Continue()
	if s := e.Run(1000); s.Reason != StopNone || e.Cycles() != c0+1000 {
		t.Fatalf("continued: %v cycles %d", s, e.Cycles()-c0)
	}
}

// program C: a store and a load through r1 = 0x8C010000
//
//	1000 mov.l @(1010),r1   1002 mov #5,r3   1004 mov.l r3,@r1   1006 mov.l @r1,r2
//	1008 bra .  100a nop  100c/e nop  1010: .long 0x8C010000
func progMem() []uint32 {
	return []uint32{0xD103, 0xE305, 0x2132, 0x6212, braTo(0x1008, 0x1008), 0x0009, 0x0009, 0x0009, 0x8C01, 0x0000}
}

func TestDebugWatchReadWrite(t *testing.T) {
	e := newDbgRig(t, progMem()...)
	e.AddWatchpoint(Watchpoint{Addr: 0x8C010000, Len: 4, Kind: WatchWrite, Enabled: true})
	s := e.Run(100)
	wantStop(t, s, StopWatchpoint, 0x8C001006) // after the store
	if w := s.Watch; !w.Write || w.PC != 0x8C001004 || w.Value != 5 || w.Size != 4 || w.Bus != 0x0C010000 {
		t.Fatalf("write hit %+v", w)
	}
	e.Continue()
	if s := e.Run(100); s.Reason != StopNone {
		t.Fatalf("the load stopped a write watchpoint: %v", s)
	}

	e = newDbgRig(t, progMem()...)
	e.AddWatchpoint(Watchpoint{Addr: 0x8C010000, Len: 4, Kind: WatchRead, Enabled: true})
	s = e.Run(100)
	wantStop(t, s, StopWatchpoint, 0x8C001008)
	if w := s.Watch; w.Write || w.PC != 0x8C001006 || w.Value != 5 {
		t.Fatalf("read hit %+v", w)
	}

	// a PC-relative literal load is a data read too
	e = newDbgRig(t, progMem()...)
	e.AddWatchpoint(Watchpoint{Addr: 0x8C001010, Len: 4, Kind: WatchRead, Enabled: true})
	if s := e.Run(100); s.Reason != StopWatchpoint || s.Watch.PC != 0x8C001000 || s.Watch.Value != 0x8C010000 {
		t.Fatalf("literal load: %v", s)
	}
}

func TestDebugWatchPhysAndValue(t *testing.T) {
	// one byte of the word, by bus address: the P1 access at 0x8C010000 is seen
	e := newDbgRig(t, progMem()...)
	e.AddWatchpoint(Watchpoint{Addr: 0x0C010002, Len: 1, Kind: WatchWrite, Phys: true, Enabled: true})
	if s := e.Run(100); s.Reason != StopWatchpoint || s.Watch.PC != 0x8C001004 {
		t.Fatalf("phys: %v", s)
	}
	// a virtual watch on the P2 alias does not see the P1 access
	e = newDbgRig(t, progMem()...)
	e.AddWatchpoint(Watchpoint{Addr: 0xAC010000, Len: 4, Enabled: true})
	if s := e.Run(100); s.Reason != StopNone {
		t.Fatalf("virtual alias: %v", s)
	}
	// value match
	e = newDbgRig(t, progMem()...)
	id := e.AddWatchpoint(Watchpoint{Addr: 0x8C010000, Len: 4, MatchValue: true, Value: 6, Enabled: true})
	if s := e.Run(100); s.Reason != StopNone {
		t.Fatalf("value 6: %v", s)
	}
	e = newDbgRig(t, progMem()...)
	e.AddWatchpoint(Watchpoint{Addr: 0x8C010000, Len: 4, MatchValue: true, Value: 0x105, Mask: 0xFF, Enabled: true})
	if s := e.Run(100); s.Reason != StopWatchpoint || !s.Watch.Write {
		t.Fatalf("value 5 under mask 0xFF: %v", s)
	}
	if w := e.Watchpoints(); len(w) != 1 || w[0].Hits != 1 || id != 1 {
		t.Fatalf("watchpoints %+v (id %d)", w, id)
	}
	// removing the last one switches the debug path off again
	e.ClearWatchpoints()
	if e.debug.on {
		t.Fatal("debugger still on with nothing armed")
	}
}

// syscallRig adds an OS-style syscall routine at 0x80020070 (table at 0x8C005000) to the
// rig's flash, syscall 3 = `mov #42,r0; rts; nop` at 0x8C006000, and a caller:
//
//	1000 mov.l @(1010),r2   1002 mov #3,r0   1004 jsr @r2   1006 nop   1008 mov r0,r9
//	100a bra .   100c nop   100e nop   1010: .long 0x80020070
func syscallRig(t *testing.T) *Emulator {
	e := newDbgRig(t, 0xD203, 0xE003, 0x420B, 0x0009, 0x6903, braTo(0x100A, 0x100A), 0x0009, 0x0009, 0x8002, 0x0070)
	routine := []byte{0xD2, 0x03, 0x40, 0x08, 0x00, 0x2E, 0x40, 0x2B, 0x00, 0x09, 0, 0, 0, 0, 0, 0, 0x8C, 0x00, 0x50, 0x00}
	if n, err := e.WriteMemory(0x00020070, routine, true); err != nil || n != len(routine) {
		t.Fatal(n, err)
	}
	e.mem.W32(0x8C00500C, 0x8C006000)
	e.mem.W16(0x8C006000, 0xE02A)
	e.mem.W16(0x8C006002, 0x000B)
	e.mem.W16(0x8C006004, 0x0009)
	return e
}

func TestDebugSyscallBreak(t *testing.T) {
	e := syscallRig(t)
	e.BreakOnSyscall(true, 4)
	if s := e.Run(100); s.Reason != StopNone || e.Registers().R[9] != 42 {
		t.Fatalf("another number: %v r9=%d", s, e.Registers().R[9])
	}
	e = syscallRig(t)
	e.BreakOnSyscall(true)
	s := e.Run(100)
	wantStop(t, s, StopSyscall, syscallTrampoline)
	if s.Syscall != 3 || s.Handler != 0x8C006000 {
		t.Fatalf("syscall stop %v", s)
	}
	// step out of the syscall: through the routine's jmp and the function's rts
	s = e.StepOut()
	wantStop(t, s, StopStep, 0x8C001008)
	if e.Registers().R[0] != 42 {
		t.Fatalf("r0=%d", e.Registers().R[0])
	}
}

func TestDebugExceptionBreak(t *testing.T) {
	// nop; an undefined opcode (0x0000): an illegal instruction exception
	e := newDbgRig(t, 0x0009, 0x0000, 0xC305)
	e.BreakOnException(true)
	s := e.Run(100)
	wantStop(t, s, StopException, dbgVBR+0x100)
	if s.Code != 0x180 || s.From != 0x8C001002 {
		t.Fatalf("exception stop %v", s)
	}
	// a filter by code: only trapa (#5 at 0x1004) stops
	e = newDbgRig(t, 0x0009, 0x0009, 0xC305)
	e.BreakOnException(true, 0x160)
	s = e.Run(100)
	if s.Reason != StopException || s.Code != 0x160 || s.From != 0x8C001006 {
		t.Fatalf("trapa stop %v", s)
	}
	if r := e.Registers(); !r.Flags.BL || !r.Flags.MD || r.SPC != 0x8C001006 {
		t.Fatalf("handler state %+v", r.Flags)
	}
}

func TestDebugInterruptBreakAndStepInto(t *testing.T) {
	e := newDbgRig(t, progLoop()...)
	e.SetRegister("sr", srMD|0x40) // IMASK 4
	e.cpu.raiseIRQ(0xF00, 9)       // a source the INTC model does not gate
	e.BreakOnInterrupt(true, 0xF00)
	s := e.Run(100)
	wantStop(t, s, StopInterrupt, dbgVBR+0x600)
	if s.Code != 0xF00 || s.From != dbgCode {
		t.Fatalf("interrupt stop %v", s)
	}
	// step into lands at a handler's entry when an interrupt comes first
	e = newDbgRig(t, progLoop()...)
	e.SetRegister("sr", srMD|0x40)
	e.cpu.raiseIRQ(0xF00, 9)
	wantStop(t, e.StepInto(), StopStep, dbgVBR+0x600)
	wantStop(t, e.StepInto(), StopStep, dbgCode) // rte back
}

func TestDebugMemoryAccess(t *testing.T) {
	e := newDbgRig(t, progCall()...)
	buf := make([]byte, 8)
	var me *MemError
	if n, err := e.ReadMemory(0x90000000, buf, false); n != 0 || !errors.As(err, &me) || me.Kind != MemUnmapped {
		t.Fatalf("unmapped: %d %v", n, err)
	}
	if _, err := e.ReadMemory(0xA4150030, buf, false); !errors.As(err, &me) || me.Kind != MemMMIO {
		t.Fatalf("MMIO: %v", err)
	}
	if _, err := e.ReadMemory(0xFF000024, buf, false); !errors.As(err, &me) || me.Kind != MemMMIO {
		t.Fatalf("P4 MMIO: %v", err)
	}
	// a read across the end of DRAM stops there
	if n, err := e.ReadMemory(0x8C7FFFFC, buf, false); n != 4 || err == nil {
		t.Fatalf("DRAM end: %d %v", n, err)
	}
	// flash beyond the 32 MB array: 0xFF, not writable
	if n, err := e.ReadMemory(0x02000000, buf, true); n != 8 || err != nil || buf[0] != 0xFF {
		t.Fatalf("flash tail: %d %v %x", n, err, buf)
	}
	if _, err := e.WriteMemory(0x02000000, buf, true); !errors.As(err, &me) || me.Kind != MemReadOnly {
		t.Fatalf("flash tail write: %v", err)
	}
	// on-chip IL RAM through its mirror
	e.WriteMemory(0xFD800010, []byte{1, 2, 3, 4}, false)
	if v, _ := e.Peek32(0xFD804010, false); v != 0x01020304 {
		t.Fatalf("IL RAM mirror %08x", v)
	}
	// a sector erase in progress: the debugger reads the array, the status reads stay
	e.mem.flash[0x40] = 0x5A
	e.mem.eraseBusy, e.mem.eraseSect = 3, 0
	if n, _ := e.ReadMemory(0xA0000040, buf[:1], false); n != 1 || buf[0] != 0x5A || e.mem.eraseBusy != 3 {
		t.Fatalf("erase: %x busy=%d", buf[0], e.mem.eraseBusy)
	}
	e.mem.eraseBusy = 0

	// the MMU: a TLB miss until the page is mapped, then the DRAM behind it
	e.mem.Write(0xFF000010, 4, 1) // MMUCR.AT
	if _, err := e.ReadMemory(0x00300010, buf, false); !errors.As(err, &me) || me.Kind != MemTLBMiss {
		t.Fatalf("TLB miss: %v", err)
	}
	ccn := e.mmio.ccn
	ccn.regs[ccnPTEH], ccn.regs[ccnPTEL] = 0x00300000, 0x0C020000|0x100|0x10|0x60|0x4 // V, 4 KB, PR=3, D
	ccn.ldtlb()
	e.WriteMemory(0x8C020010, []byte{0xCA, 0xFE}, false)
	if n, err := e.ReadMemory(0x00300010, buf[:2], false); n != 2 || err != nil || buf[0] != 0xCA || buf[1] != 0xFE {
		t.Fatalf("mapped read: %d %v %x", n, err, buf[:2])
	}
	if info, err := e.Translate(0x00300FFE); err != nil || !info.Translated || info.Bus != 0x0C020FFE || info.Region != "dram" {
		t.Fatalf("translate %+v %v", info, err)
	}
	if n, _ := e.ReadMemory(0x00300FFE, buf[:4], false); n != 2 { // the next page is not mapped
		t.Fatalf("read across the page end: %d", n)
	}
}

func TestDebugWritesReachExecution(t *testing.T) {
	e := newDbgRig(t, progCall()...)
	e.WriteMemory(dbgCode, []byte{0xE1, 0x09}, false) // mov #1,r1 -> mov #9,r1
	e.StepInto()
	if r := e.Registers(); r.R[1] != 9 {
		t.Fatalf("r1=%d", r.R[1])
	}
	// code patched into flash runs from there
	e.WriteMemory(0x00010000, []byte{0xE5, 0x33, 0xAF, 0xFE, 0x00, 0x09}, true) // mov #0x33,r5; bra .; nop
	e.SetRegister("pc", 0x80010000)
	e.StepInto()
	if r := e.Registers(); r.R[5] != 0x33 || r.PC != 0x80010002 {
		t.Fatalf("flash patch: r5=%x pc=%08x", r.R[5], r.PC)
	}
}

func TestDebugRegisters(t *testing.T) {
	e := newDbgRig(t)
	if err := e.SetRegister("R3", 7); err != nil {
		t.Fatal(err)
	}
	e.SetRegister("r2_bank1", 0x1111)
	e.SetRegister("r2_bank0", 0x2222)
	e.SetRegister("gbr", 0x33)
	r := e.Registers()
	if r.R[3] != 7 || r.Bank != 0 || r.R[2] != 0x2222 || r.Bank1[2] != 0x1111 || r.Bank0[2] != 0x2222 || r.GBR != 0x33 {
		t.Fatalf("regs %+v", r)
	}
	e.SetRegister("sr", srMD|srRB|srT|0x30) // switch to bank 1, as ldc would
	r = e.Registers()
	if r.Bank != 1 || r.R[2] != 0x1111 || r.Bank0[2] != 0x2222 || !r.Flags.T || r.Flags.IMASK != 3 || !r.Flags.RB {
		t.Fatalf("after RB: %+v", r)
	}
	if e.SetRegister("r16", 0) == nil || e.SetRegister("x", 0) == nil || e.SetRegister("r8_bank0", 0) == nil {
		t.Fatal("bad names accepted")
	}
	for _, n := range RegisterNames() {
		if err := e.SetRegister(n, 0); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
}

func TestDebugTrace(t *testing.T) {
	e := newDbgRig(t, progLoop()...)
	e.SetTrace(4)
	for i := 0; i < 6; i++ {
		e.StepInto()
	}
	pcs, n := e.Trace()
	// mov, add, dt, bf, add, dt: the last four
	want := []uint32{0x8C001004, 0x8C001006, 0x8C001002, 0x8C001004}
	if n != 6 || len(pcs) != 4 {
		t.Fatalf("trace %x (%d)", pcs, n)
	}
	for i := range want {
		if pcs[i] != want[i] {
			t.Fatalf("trace %x, want %x", pcs, want)
		}
	}
	e.SetTrace(0)
	if pcs, _ := e.Trace(); pcs != nil || e.debug.on {
		t.Fatal("trace still on")
	}
}

// osBoot is the 16 MB dump booted from reset (the golden boot's machine, through the facade).
func osBoot(t testing.TB) *Emulator {
	flash, err := os.ReadFile(flashPath)
	if err != nil {
		t.Skip("no flash image")
	}
	return NewEmulator(flash)
}

// The debug path (every instruction through dstep) gives exactly the machine the plain loop
// does: same registers, clock and RAM after 3 M instructions of the real boot.
func TestDebugPathMatchesPlain(t *testing.T) {
	run := func(arm func(e *Emulator)) (*Emulator, Regs) {
		e := osBoot(t)
		if arm != nil {
			arm(e)
		}
		for i := 0; i < 12; i++ {
			if s := e.Run(250_000); s.Reason != StopNone {
				t.Fatalf("stopped: %v", s)
			}
		}
		return e, e.Registers()
	}
	plain, want := run(nil)
	for _, tc := range []struct {
		name string
		arm  func(e *Emulator)
	}{
		{"every check", func(e *Emulator) { // dstep for every instruction
			e.SetTrace(64)
			e.AddBreakpoint(0x00000002) // never reached
			e.AddWatchpoint(Watchpoint{Addr: 0x0C7FFFF0, Len: 16, Phys: true, Enabled: true})
			e.BreakOnSyscall(true)
			e.BreakOnException(true)
			e.BreakOnInterrupt(true, 0x12345)
		}},
		{"stop addresses only", func(e *Emulator) { // debugRun's page-filtered loop
			e.AddBreakpoint(0x80000003) // on the reset code's page, never an instruction
			e.AddBreakpoint(0x8C7FF000)
			e.BreakOnException(true, 0x12345)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbg, got := run(tc.arm)
			if got != want {
				t.Fatalf("registers differ:\n plain %+v\n debug %+v", want, got)
			}
			if !bytes.Equal(plain.mem.dram, dbg.mem.dram) || !bytes.Equal(plain.mem.ilram, dbg.mem.ilram) {
				t.Fatal("RAM differs")
			}
		})
	}
}

// The real OS: interrupts stop at VBR+0x600; the syscall table is read without side effects;
// a host call into the OS ignores the debugger.
func TestDebugOS(t *testing.T) {
	e := osBoot(t)
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no save-state")
	}
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	if h, ok := e.syscallHandler(0x1839); !ok || h != 0x802AEA24 {
		t.Fatalf("PowerOff handler %08x", h)
	}
	e.Step(5_000_000)
	e.BreakOnInterrupt(true)
	s := e.Run(50_000_000)
	r := e.Registers()
	t.Logf("stop %v", s)
	if s.Reason != StopInterrupt || s.PC != r.VBR+0x600 || s.Code == 0 || !r.Flags.BL {
		t.Fatalf("interrupt: %v vbr=%08x", s, r.VBR)
	}
	e.Continue()
	if s := e.Run(50_000_000); s.Reason != StopInterrupt {
		t.Fatalf("second interrupt: %v", s)
	}
	e.BreakOnInterrupt(false)
	e.Continue()
	e.Step(20_000_000) // back to sleep in the key loop
	if !e.osIdle() {
		t.Skip("OS not idle")
	}
	// a breakpoint on sys_malloc and a break on every syscall: the host's own call runs through
	mal := e.syscallAddr(sysMalloc)
	e.AddBreakpoint(mal)
	e.BreakOnSyscall(true)
	e.mu.Lock()
	p, err := e.syscall(sysMalloc, 50_000_000, 64)
	e.mu.Unlock()
	if err != nil || p == 0 {
		t.Fatalf("malloc through the OS: %08x %v", p, err)
	}
	if s, stopped := e.Stopped(); stopped {
		t.Fatalf("host call stopped: %v", s)
	}
}

// The host's watchpoints and the emulated UBC see the same access, each on its own: the UBC
// takes its user break (the guest's debugger), the host stops after the instruction.
func TestDebugWatchWithUBC(t *testing.T) {
	e := newDbgRig(t, progMem()...)
	const handler = 0x8C002000
	e.mem.W16(handler, 0x002B)
	e.mem.W16(handler+2, 0x0009)
	e.cpu.dbr = handler
	e.mem.Write(ubcBase+ubcCBCR, 4, 1)            // UBDE: user breaks go to DBR
	e.mem.Write(ubcBase+0x24, 4, crrBIE)          // channel 1: break...
	e.mem.Write(ubcBase+0x28, 4, 0x8C010000)      // ...on an operand access to the word
	e.mem.Write(ubcBase+0x20, 4, 2<<4|2<<1|cbrCE) // ID = operand, RW = write
	e.AddWatchpoint(Watchpoint{Addr: 0x8C010000, Len: 4, Kind: WatchWrite, Enabled: true})
	s := e.Run(100)
	if s.Reason != StopWatchpoint || s.Watch.PC != 0x8C001004 || s.PC != handler || e.cpu.ubc.breaks != 1 {
		t.Fatalf("stop %v, UBC breaks %d", s, e.cpu.ubc.breaks)
	}
	if e.mem.ops != 0 {
		t.Fatalf("operand hooks left armed: %b", e.mem.ops)
	}
	e.Continue()
	if s := e.Run(100); s.Reason != StopNone || e.Registers().R[2] != 5 {
		t.Fatalf("after: %v r2=%d", s, e.Registers().R[2])
	}
}

// The same with the OS running interrupt-driven work: a held key on the MAIN MENU (key scans,
// timers, redraws, sleep) for 20 M instructions, every instruction through dstep.
func TestDebugPathMatchesPlainOS(t *testing.T) {
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no save-state")
	}
	run := func(arm bool) (*Emulator, Regs) {
		e := osBoot(t)
		if err := e.Resume(st); err != nil {
			t.Fatal(err)
		}
		e.SetInstrPerSecond(22_000_000)
		if arm {
			e.SetTrace(16)
			e.BreakOnInterrupt(true, 0x12345)
		}
		e.KeyDown(1, 7) // RIGHT
		for i := 0; i < 80; i++ {
			if s := e.Run(250_000); s.Reason != StopNone {
				t.Fatalf("stopped: %v", s)
			}
		}
		return e, e.Registers()
	}
	plain, want := run(false)
	dbg, got := run(true)
	if got != want || plain.cpu.irqCnt != dbg.cpu.irqCnt || plain.cpu.irqCnt == 0 {
		t.Fatalf("differ:\n plain %+v (%d irqs)\n debug %+v (%d irqs)", want, plain.cpu.irqCnt, got, dbg.cpu.irqCnt)
	}
	if !bytes.Equal(plain.mem.dram, dbg.mem.dram) || plain.Pushes() != dbg.Pushes() {
		t.Fatal("RAM or frames differ")
	}
	t.Logf("%d interrupts, %d frames pushed, %d instructions executed", plain.cpu.irqCnt, plain.Pushes(), plain.Executed())
}
