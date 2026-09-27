package main

import (
	"os"
	"testing"
)

// RTC registers: calendar in BCD from the fixed 2010 epoch, the 64 Hz counter, and the
// periodic interrupt at the PES rate, phase-aligned so re-arming does not restart it.
func TestRTCRegisters(t *testing.T) {
	mmio := NewMMIOBus()
	mem := NewMemory(make([]byte, 0x1000), mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	mmio.SetInstrPerSecond(1_000_000)
	if v := mem.Read(0xA413FECE, 2); v != 0x2010 {
		t.Fatalf("RYRCNT = %#x, want 0x2010", v)
	}
	if v := mem.Read(0xA413FEC8, 1); v != 5 {
		t.Fatalf("RWKCNT = %d, want 5 (Friday)", v)
	}
	cpu.cycles = 3661 * 1_000_000 // 1 h 1 min 1 s
	if s, m, h := mem.Read(0xA413FEC2, 1), mem.Read(0xA413FEC4, 1), mem.Read(0xA413FEC6, 1); s != 0x01 || m != 0x01 || h != 0x01 {
		t.Fatalf("time = %02x:%02x:%02x, want 01:01:01", h, m, s)
	}
	cpu.cycles = 3661*1_000_000 + 500_000 // +0.5 s
	if v := mem.Read(0xA413FEC0, 1); v != 64 {
		t.Fatalf("R64CNT at +0.5 s = %d, want 64", v)
	}
	// 31 days later: February 1st
	cpu.cycles = 31 * 86400 * 1_000_000
	if d, mo := mem.Read(0xA413FECA, 1), mem.Read(0xA413FECC, 1); d != 0x01 || mo != 0x02 {
		t.Fatalf("date = %02x/%02x, want 01/02", d, mo)
	}
	// periodic: PES=5 (1/2 s) -> events on the 500k-cycle grid, PEF set, INTEVT 0xAA0
	mmio.timerPeriod = 30000
	cpu.cycles = 2_700_000
	mem.Write(0xA413FEDE, 1, 0x50)
	if mmio.rtc.nextPeriodic != 3_000_000 {
		t.Fatalf("nextPeriodic = %d, want 3000000 (phase-aligned)", mmio.rtc.nextPeriodic)
	}
	mem.Write(0xA413FEDE, 1, 0x50) // re-arm must not move the schedule
	if mmio.rtc.nextPeriodic != 3_000_000 {
		t.Fatalf("re-arm moved nextPeriodic to %d", mmio.rtc.nextPeriodic)
	}
	cpu.cycles = 3_000_000
	mmio.rtc.tick(cpu)
	if v := mem.Read(0xA413FEDE, 1); v != 0xD0 {
		t.Fatalf("RCR2 after event = %#x, want 0xD0 (PEF set)", v)
	}
	if len(cpu.pending) != 1 || cpu.pending[0].intevt != RTCINTEVT {
		t.Fatalf("pending = %+v, want INTEVT 0xAA0", cpu.pending)
	}
	mem.Write(0xA413FEDE, 1, 0x00) // OS clears PES/PEF
	cpu.pending = nil
	cpu.cycles = 4_000_000
	mmio.rtc.tick(cpu)
	if len(cpu.pending) != 0 {
		t.Fatalf("event with PES=0")
	}
}

// sleep halts the CPU until an interrupt REQUEST arrives, even with SR.BL set; the request
// is then serviced once BL clears (the OS idles exactly this way: BL=1, sleep, BL=0).
func TestSleepWakesOnRequest(t *testing.T) {
	img := make([]byte, 0x1000)
	// at 0: sleep ; nop ; nop  (0x001B, 0x0009, 0x0009); a nop at the interrupt vector (VBR+0x600)
	copy(img, []byte{0x00, 0x1B, 0x00, 0x09, 0x00, 0x09})
	copy(img[0x600:], []byte{0x00, 0x09})
	mmio := NewMMIOBus()
	mem := NewMemory(img, mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	cpu.pc = 0x80000000
	cpu.setSR(srMD | srRB | srBL) // blocked, like the OS idle path
	cpu.step()
	if !cpu.sleeping || cpu.pc != 0x80000002 {
		t.Fatalf("sleep not entered: sleeping=%v pc=%#x", cpu.sleeping, cpu.pc)
	}
	for i := 0; i < 10; i++ {
		cpu.step()
	}
	if !cpu.sleeping || cpu.pc != 0x80000002 {
		t.Fatalf("woke without a request: pc=%#x", cpu.pc)
	}
	cpu.raiseIRQ(0x560, 8)
	cpu.step() // request cancels sleep (BL still set: not serviced)
	if cpu.sleeping {
		t.Fatal("request did not cancel sleep")
	}
	cpu.step() // executes the nop at +2 (BL blocks the exception)
	if cpu.pc != 0x80000004 {
		t.Fatalf("pc = %#x, want 0x80000004 (nop executed, IRQ still blocked)", cpu.pc)
	}
	cpu.setSR(srMD | srRB) // OS clears BL
	cpu.step()
	if cpu.pc != cpu.vbr+0x602 {
		t.Fatalf("IRQ not serviced after BL cleared: pc=%#x", cpu.pc)
	}
}

// End to end: in Run-Matrix idle the OS arms the RTC periodic interrupt at 1/2 s before
// sleeping, so its ISR must run at 2 Hz of emulated time.
func TestRTCPeriodicInRunMatrix(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no menu save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	e.InjectKey(2, 1) // EXE -> Run-Matrix
	e.Step(20_000_000)
	isr, sleeps := 0, 0
	was := false
	for i := 0; i < 210_000_000; i++ { // 3 s
		e.Step(1)
		if e.cpu.pc == 0x801dfc6c {
			isr++
		}
		if e.cpu.sleeping && !was {
			sleeps++
		}
		was = e.cpu.sleeping
	}
	t.Logf("3 s idle: RTC periodic ISR entries=%d, sleep entries=%d, RCR2=%#x", isr, sleeps, e.mmio.rtc.rcr2)
	if isr < 5 || isr > 7 {
		t.Errorf("RTC periodic ISR ran %d times in 3 s, want 6 (2 Hz)", isr)
	}
	if sleeps == 0 {
		t.Error("the OS never slept: sleep semantics broken")
	}
}
