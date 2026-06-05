package main

import "testing"

// Unit tests for the 0xA4CB0000 hardware packed-BCD ALU (RE'd cont.18c; command set
// confirmed by the on-device probe cont.18e, os/devic_probes/). Decode: op=(cmd&1)?add:sub,
// flag_in=(cmd&4)?1:(cmd&2)?latch:0, with a SINGLE shared carry/borrow latch. So 0=A-B,
// 1=A+B, 2=A-B-flag, 3=A+B+flag, 4=A-B-1, 5=A+B+1. This is the model that makes the real OS
// formatter render "98765" instead of "0". These tests must mirror emu/mmio.py's BCDALU
// exactly (Go is the port, Python the oracle), and the expected values are hardware truth
// from the probe's raw capture.

func TestBCDAddSubWord(t *testing.T) {
	cases := []struct {
		fn         string
		a, b, cin  uint32
		wantR, wOut uint32
	}{
		{"add", 0x09876500, 0x00000001, 0, 0x09876501, 0}, // simple
		{"add", 0x00000009, 0x00000009, 0, 0x00000018, 0}, // 9+9=18 (BCD), no word carry
		{"add", 0x99999999, 0x00000001, 0, 0x00000000, 1}, // all-9s + 1 -> 0 carry-out
		{"add", 0x00000005, 0x00000005, 1, 0x00000011, 0}, // 5+5+carry_in=11
		{"sub", 0x00000018, 0x00000009, 0, 0x00000009, 0}, // 18-9=9
		{"sub", 0x00000000, 0x00000001, 0, 0x99999999, 1}, // 0-1 -> borrow ripples all nibbles
		{"sub", 0x09876500, 0x00000000, 0, 0x09876500, 0}, // identity
	}
	for _, c := range cases {
		var r, o uint32
		if c.fn == "add" {
			r, o = bcdAdd(c.a, c.b, c.cin)
		} else {
			r, o = bcdSub(c.a, c.b, c.cin)
		}
		if r != c.wantR || o != c.wOut {
			t.Errorf("%s(%08x,%08x,%d) = %08x,%d  want %08x,%d", c.fn, c.a, c.b, c.cin, r, o, c.wantR, c.wOut)
		}
	}
}

// Drive the peripheral through the bus the way the OS does and check the result reg.
func TestBCDALURegisterInterface(t *testing.T) {
	bus := NewMMIOBus()
	op := func(cmd, a, b uint32) uint32 {
		bus.Write(0xA4CB0014, 4, a)
		bus.Write(0xA4CB0018, 4, b)
		bus.Write(0xA4CB0010, 2, cmd)
		return bus.Read(0xA4CB001C, 4)
	}
	if got := op(1, 0x09876500, 0x05000000); got != 0x14876500 { // cmd1 = A+B
		t.Errorf("cmd1 add = %08x want 14876500", got)
	}
	if got := op(0, 0x00000018, 0x00000009); got != 0x00000009 { // cmd0 = A-B
		t.Errorf("cmd0 sub = %08x want 00000009", got)
	}
	// cmd4 = A-B-1 (hardware truth from the probe): 12345678-0-1, 5-3-1, 0-0-1, 99999999-1-1.
	for _, c := range []struct{ a, b, want uint32 }{
		{0x12345678, 0, 0x12345677},
		{0x00000005, 0x00000003, 0x00000001},
		{0x00000000, 0x00000000, 0x99999999}, // 0-0-1 borrows -> ten's complement
		{0x99999999, 0x00000001, 0x99999997},
	} {
		if got := op(4, c.a, c.b); got != c.want {
			t.Errorf("cmd4 (A-B-1) %08x-%08x = %08x want %08x", c.a, c.b, got, c.want)
		}
	}
	if got := op(5, 0x00000005, 0x00000003); got != 0x00000009 { // cmd5 = A+B+1 (probe)
		t.Errorf("cmd5 (A+B+1) = %08x want 00000009", got)
	}
	if got := op(9, 0x00000005, 0x00000003); got != 0x00000008 { // bit3 ignored: cmd9 == cmd1 = A+B
		t.Errorf("cmd9 (== cmd1, A+B) = %08x want 00000008", got)
	}
}

// Shared carry/borrow latch (probe C5): a sub's borrow-out feeds a following add as carry-in.
// cmd4(0,0) sets the shared flag (borrow); the next cmd3 (add-continue) consumes it as carry:
// 5+3+1 = 9. A separate-latch model would wrongly give 8.
func TestBCDALUSharedLatch(t *testing.T) {
	bus := NewMMIOBus()
	op := func(cmd, a, b uint32) uint32 {
		bus.Write(0xA4CB0014, 4, a)
		bus.Write(0xA4CB0018, 4, b)
		bus.Write(0xA4CB0010, 2, cmd)
		return bus.Read(0xA4CB001C, 4)
	}
	op(1, 0x00000001, 0x00000001) // clears flag (1+1=2, no carry)
	op(4, 0x00000000, 0x00000000) // 0-0-1 -> borrow, flag set
	if got := op(3, 0x00000005, 0x00000003); got != 0x00000009 {
		t.Errorf("shared latch: cmd3 after borrow = %08x want 00000009", got)
	}
}

// Multi-word carry latch: cmd1 (first word) seeds carry, cmd3 (continue) consumes it.
// Process a 96-bit value LSW-first like FUN_80072f80 does.
func TestBCDALUCarryLatch(t *testing.T) {
	bus := NewMMIOBus()
	op := func(cmd, a, b uint32) uint32 {
		bus.Write(0xA4CB0014, 4, a)
		bus.Write(0xA4CB0018, 4, b)
		bus.Write(0xA4CB0010, 2, cmd)
		return bus.Read(0xA4CB001C, 4)
	}
	// value = 99999999_99999999 (two words), + 1 in the LSW -> carry ripples to MSW.
	lsw := op(1, 0x99999999, 0x00000001) // first word: 9..9 + 1 = 0, carry out
	msw := op(3, 0x99999999, 0x00000000) // continue: 9..9 + 0 + carry = 0, carry out
	if lsw != 0 || msw != 0 {
		t.Errorf("carry-latch add = lsw %08x msw %08x, want 0/0", lsw, msw)
	}
}
