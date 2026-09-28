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
