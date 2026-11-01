package sh4

import (
	"context"
	"testing"
)

// resolveAt decodes the jump at addr of words placed at AddinCode and resolves it.
func resolveAt(t *testing.T, sys *Syscalls, addr uint32, words ...uint16) (Indirect, bool, string) {
	t.Helper()
	s := newAsm(AddinCode, 0x40)
	s.w(AddinCode, words...)
	img := NewImage(AddinCode, s.data)
	in, ok := img.Decode(addr)
	if !ok || (in.Kind != KindJumpReg && in.Kind != KindCallReg) {
		t.Fatalf("%08x: %q is no indirect jump", addr, in.Text)
	}
	r, ok := ResolveIndirect(img.Word, sys, &in)
	return r, ok, Comment(img.Word, sys, &in)
}

// the trampoline's address as two words
const trampHi, trampLo = uint16(SyscallTrampoline >> 16), uint16(SyscallTrampoline & 0xFFFF)

// TestResolveSlot: the jump's own register is read before the delay slot runs, so a load in
// the slot does not count for it; r0, the syscall number, is read after the slot.
func TestResolveSlot(t *testing.T) {
	const B = AddinCode
	// gcc: mov.w L,r2; braf r2; mov #0,r2; nop; L: .word 0x40 -> B+2+4+0x40, not B+6
	r, ok, c := resolveAt(t, nil, B+2, 0x9202, 0x0223, 0xe200, 0x0009, 0x0040)
	if !ok || r.Target != B+0x46 || c != "-> 0x00300046" {
		t.Errorf("braf: %+v %v %q", r, ok, c)
	}
	// jsr @r1 with r1 loaded only in the slot: not resolved
	if r, ok, c := resolveAt(t, nil, B, 0x410b, 0xd101, 0x0009, 0x0009, 0x0030, 0x1234); ok || c != "" {
		t.Errorf("slot-only load resolved: %+v %q", r, c)
	}
	// mov.l L1,r1; jsr @r1; mov.l L2,r1: the call goes to L1
	r, ok, _ = resolveAt(t, nil, B+2, 0xd101, 0x410b, 0xd101, 0x0009, 0x0030, 0x1234, 0x0030, 0x5678)
	if !ok || r.Target != 0x00301234 {
		t.Errorf("jsr: %+v %v", r, ok)
	}

	sys := FindSyscalls(testROM())
	// mov.l L1,r2; jmp @r2; mov.l L2,r0 (in the slot); nop; L1: trampoline; L2: 0x1f44
	r, ok, c = resolveAt(t, sys, B+2, 0xd201, 0x422b, 0xd001, 0x0009,
		trampHi, trampLo, 0, 0x1f44)
	if !ok || r.Name != "sys_malloc" || r.Syscall != 0x1f44 || r.Target != 0x80001000 || c != "-> sys_malloc" {
		t.Errorf("syscall in the slot: %+v %v %q", r, ok, c)
	}
	// an unnamed syscall (mov #3,r0 in the slot): DASM shows its handler
	r, ok, c = resolveAt(t, sys, B+2, 0xd201, 0x422b, 0xe003, 0x0009,
		trampHi, trampLo)
	if !ok || r.Name != "" || r.Syscall != 3 || r.Target != 0x80001010 || c != "-> 0x80001010" {
		t.Errorf("unnamed syscall: %+v %v %q", r, ok, c)
	}
	// no number found: "syscall", the trampoline
	r, ok, c = resolveAt(t, sys, B+2, 0xd201, 0x422b, 0x0009, 0x0009,
		trampHi, trampLo)
	if !ok || r.Name != "syscall" || r.Syscall != -2 || r.Target != SyscallTrampoline || c != "-> syscall" {
		t.Errorf("syscall without a number: %+v %v %q", r, ok, c)
	}
}

// TestResolveJumpTable: a gcc jump table reloads the register from the table and doubles it
// before the braf; the scan stops there instead of taking an older literal load.
func TestResolveJumpTable(t *testing.T) {
	const B = AddinCode
	lit := []uint16{0x0009, 0x0009, 0x0030, 0x1234} // nops, then L at B+0xc
	// mov.l L,r1; mov.w @(r0,r2),r1; add r1,r1; braf r1
	words := append([]uint16{0xd102, 0x012d, 0x311c, 0x0123}, lit...)
	if r, ok, c := resolveAt(t, nil, B+6, words...); ok || c != "" {
		t.Errorf("jump table resolved: %+v %q", r, c)
	}
	// mov.l L,r1; mov.w @(r0,r2),r1; nop; braf r1
	words = append([]uint16{0xd102, 0x012d, 0x0009, 0x0123}, lit...)
	if r, ok, _ := resolveAt(t, nil, B+6, words...); ok {
		t.Errorf("load through r0 ignored: %+v", r)
	}
	// mov.l L,r1; nop; nop; braf r1: L + pc + 4
	words = append([]uint16{0xd102, 0x0009, 0x0009, 0x0123}, lit...)
	if r, ok, _ := resolveAt(t, nil, B+6, words...); !ok || r.Target != 0x00301234+B+10 {
		t.Errorf("braf: %+v %v", r, ok)
	}
}

func TestWritesReg(t *testing.T) {
	for _, c := range []struct {
		op   uint16
		r    int
		want bool
	}{
		{0x4300, 3, true},  // shll r3
		{0x432b, 3, false}, // jmp @r3
		{0x430b, 3, false}, // jsr @r3
		{0x2316, 3, true},  // mov.l r1,@-r3
		{0x2316, 1, false},
		{0x2312, 3, false}, // mov.l r1,@r3
		{0x3310, 3, false}, // cmp/eq r1,r3
		{0x331c, 3, true},  // add r1,r3
		{0x331d, 3, false}, // dmuls.l r1,r3
		{0x0302, 3, true},  // stc sr,r3
		{0x0329, 3, true},  // movt r3
		{0x032d, 3, true},  // mov.w @(r0,r2),r3
		{0x0323, 3, false}, // braf r3
		{0x8401, 0, true},  // mov.b @(1,r0),r0
		{0xc701, 0, true},  // mova
		{0xcb01, 0, true},  // or #1,r0
		{0xc801, 0, false}, // tst #1,r0
		{0x0163, 0, true},  // movli.l @r1,r0
		{0xf000, 7, true},  // a DSP transfer
		{0xf000, 8, false},
		{0x6123, 1, true}, // mov r2,r1
		{0x6123, 2, false},
	} {
		if g := writesReg(uint32(c.op), c.r); g != c.want {
			t.Errorf("writesReg(%04x, r%d) = %v", c.op, c.r, g)
		}
	}
}

// TestFuncNameOSStub: in the OS a function that only forwards to a handler keeps its sub_
// name (DASM looks for stubs in add-ins only).
func TestFuncNameOSStub(t *testing.T) {
	s := newAsm(ROMBase, 0x40000)
	copy(s.data, testROM().Data)
	s.w(0x80002000, 0xd201, 0x422b, 0x0009, 0x0009) // mov.l L,r2; jmp @r2; nop
	s.l(0x80002008, 0x80001000)
	img := NewImage(ROMBase, s.data)
	sys := FindSyscalls(img)
	an, err := Analyze(context.Background(), img, OSOptions(img, sys))
	if err != nil {
		t.Fatal(err)
	}
	in, _ := img.Decode(0x80002002)
	if c := an.Comment(&in); c != "-> sys_free" {
		t.Errorf("Comment %q", c)
	}
	if n := an.FuncName(0x80002000); n != "sub_80002000" {
		t.Errorf("FuncName %q", n)
	}
}
