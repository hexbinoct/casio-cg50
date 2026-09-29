package main

import "testing"

// The INTC gate: a source is requested only with a nonzero priority field and a clear mask
// bit; registers read back; mask-clear writes unmask; the OS's sources keep their levels,
// add-in-only sources use the priority field.
func TestINTCGate(t *testing.T) {
	mmio, mem, cpu := newTimerRig(1_000_000)
	// the OS's tick: IPRB[15:12], IMR4 bit3 (the sequence its ISR performs)
	mem.Write(0xA4080090, 1, 0x08)      // mask
	mem.Write(0xA4080004, 2, 0x0000)    // IPRB = 0
	mem.Write(0xA4080004, 2, 0xC000)    // priority 12
	mmio.raise(cpu, TimerINTEVT, TimerLevel)
	if hasIRQ(cpu, TimerINTEVT) {
		t.Fatal("masked source was requested")
	}
	mem.Write(0xA40800D0, 1, 0x08) // mask clear
	if v := mem.Read(0xA4080090, 1); v != 0 {
		t.Fatalf("IMR4 = %#x after mask-clear", v)
	}
	mmio.raise(cpu, TimerINTEVT, TimerLevel)
	if !hasIRQ(cpu, TimerINTEVT) || cpu.pending[len(cpu.pending)-1].level != TimerLevel {
		t.Fatalf("tick not requested at its level: %+v", cpu.pending)
	}
	cpu.pending = nil
	mem.Write(0xA4080004, 2, 0x0000) // gint zeroes the priority
	mmio.raise(cpu, TimerINTEVT, TimerLevel)
	if hasIRQ(cpu, TimerINTEVT) {
		t.Fatal("priority 0 source was requested")
	}
	if v := mem.Read(0xA4080004, 2); v != 0 {
		t.Fatalf("IPRB reads %#x", v)
	}
	// ETMU0 (IPRJ[15:12], IMR6 bit3) as gint programs it: priority field = level
	mem.Write(0xA4080024, 2, 0x7000)
	mem.Write(0xA4080098, 1, 0x08)
	mmio.raise(cpu, 0x9e0, 0)
	if hasIRQ(cpu, 0x9e0) {
		t.Fatal("masked ETMU0 requested")
	}
	mem.Write(0xA40800D8, 1, 0x08)
	mmio.raise(cpu, 0x9e0, 0)
	if !hasIRQ(cpu, 0x9e0) || cpu.pending[len(cpu.pending)-1].level != 7 {
		t.Fatalf("ETMU0 request = %+v, want level 7", cpu.pending)
	}
	// an unknown source is not gated
	cpu.pending = nil
	mmio.raise(cpu, 0x123, 5)
	if !hasIRQ(cpu, 0x123) {
		t.Fatal("unknown source dropped")
	}
	// legacy snapshot: no INTC state -> the OS's three sources are enabled
	mmio.intc.restoreState(map[uint32]uint32{})
	for _, ev := range []uint32{TimerINTEVT, KeyscINTEVT, RTCINTEVT} {
		if ok, _ := mmio.intc.enabled(ev); !ok {
			t.Fatalf("legacy defaults leave %#x disabled", ev)
		}
	}
	// state round-trip keeps the registers
	mem.Write(0xA4080028, 2, 0x5000)
	mem.Write(0xA40800A8, 1, 0x02)
	m := mmio.intc.stateMap()
	mmio.intc.restoreState(map[uint32]uint32{})
	mmio.intc.restoreState(m)
	if mem.Read(0xA4080028, 2) != 0x5000 || mem.Read(0xA40800A8, 1) != 0x02 {
		t.Fatal("state round-trip lost registers")
	}
	if ok, _ := mmio.intc.enabled(RTCINTEVT); ok {
		t.Fatal("RTC should be masked by IMR10 bit1 after the round-trip")
	}
}

// Requests are gated again when the CPU accepts them (2026-09-29): gint's ETMU5 key-scan tick
// (INTEVT 0xfa0: IPRL[15:12], IMR8 bit1) raised just before a world switch must not reach the
// OS once gint has restored the OS's INTC state (priority 0) — the OS's handler table has no
// entry for it (DASM crashed in the emulator after thousands of BFile world switches). A
// masked source's request waits instead, and is taken once unmasked.
func TestINTCGateAtAcceptance(t *testing.T) {
	mmio, mem, cpu := newTimerRig(1_000_000)
	cpu.sr &^= srBL | srIMASK
	mem.Write(0xA408002C, 2, 0x7000) // IPRL: ETMU5 priority 7
	mem.Write(0xA40800E0, 1, 0x02)   // MSKCLR8: unmask
	mmio.raise(cpu, 0xfa0, 0)
	if !hasIRQ(cpu, 0xfa0) {
		t.Fatal("ETMU5 not requested")
	}
	mem.Write(0xA408002C, 2, 0x0000) // the world switch restores the OS's IPRL
	if cpu.acceptInterrupt() {
		t.Fatalf("disabled source accepted (INTEVT %#x)", mem.Read(0xFF000028, 4))
	}
	if hasIRQ(cpu, 0xfa0) {
		t.Fatal("a disabled source's request stays pending")
	}
	// masked: waits, then goes through once unmasked
	mem.Write(0xA408002C, 2, 0x7000)
	mmio.raise(cpu, 0xfa0, 0)
	mem.Write(0xA40800A0, 1, 0x02) // IMR8: mask
	if cpu.acceptInterrupt() || !hasIRQ(cpu, 0xfa0) {
		t.Fatal("masked request accepted or lost")
	}
	cpu.sleeping = true
	cpu.step()
	if !cpu.sleeping {
		t.Fatal("a masked request woke the CPU")
	}
	cpu.sleeping = false
	mem.Write(0xA40800E0, 1, 0x02) // unmask
	if !cpu.acceptInterrupt() || mem.Read(0xFF000028, 4) != 0xfa0 {
		t.Fatal("unmasked request not accepted")
	}
}
