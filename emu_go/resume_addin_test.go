package main

import (
	"os"
	"slices"
	"testing"
)

// A running ETMU/TMU channel survives a save-state: same count, still running, interrupt
// enable kept (gint's keyboard scan is ETMU5 at 128 Hz).
func TestTimerChannelsSaveState(t *testing.T) {
	e := NewEmulator(make([]byte, 0x1000))
	e.SetInstrPerSecond(45_000_000)
	w := func(a, v uint32) { e.mem.Write(a, 4, v) }
	w(0xA44D00D4, 255)        // ETMU5 TCOR
	w(0xA44D00D8, 255)        // TCNT
	w(0xA44D00DC, tcrUNIE)    // TCR: underflow interrupt on
	w(0xA44D00D0, 1)          // TSTR: start
	w(0xA4490008+0xC, 999)    // TMU1 TCOR
	w(0xA4490008+0xC+8, 2)    // TMU1 TCR: prescaler
	w(0xA4490004, 2)          // TSTR: start TMU1
	e.cpu.cycles += 1_000_000 // let them run
	cnt5, cnt1 := e.mem.R32(0xA44D00D8), e.mem.R32(0xA4490008+0xC+4)
	snap, err := e.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	f := NewEmulator(make([]byte, 0x1000))
	if err := f.Resume(snap); err != nil {
		t.Fatal(err)
	}
	f.SetInstrPerSecond(45_000_000)
	if c := f.mmio.etmu2.ch[5]; !c.running || c.tcr&tcrUNIE == 0 || c.tcor != 255 {
		t.Fatalf("ETMU5 after resume: %+v", c)
	}
	if v := f.mem.R32(0xA44D00D8); v != cnt5 {
		t.Errorf("ETMU5 count %d after resume, %d before", v, cnt5)
	}
	if c := f.mmio.tmu.ch[1]; !c.running || c.tcr&7 != 2 || c.tcor != 999 {
		t.Fatalf("TMU1 after resume: %+v", c)
	}
	if v := f.mem.R32(0xA4490008 + 0xC + 4); v != cnt1 {
		t.Errorf("TMU1 count %d after resume, %d before", v, cnt1)
	}
	if f.mem.R32(0xA4490004)&2 == 0 {
		t.Error("TMU1 not running in TSTR after resume")
	}
}

// A snapshot taken while a gint add-in runs (the Android app saves one whenever it pauses)
// resumes into a machine that still answers keys: timers, the X/Y/IL memories and the panel
// come back. Uses DASM, icon Z in the 32 MB dump.
func TestResumeInsideGintAddin(t *testing.T) {
	e := installState(t)
	for _, k := range [][2]uint32{{2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {1, 7}, {1, 7}, {2, 1}} {
		e.InjectKey(k[0], k[1])
		e.Step(30_000_000)
	}
	e.Step(150_000_000)
	if !e.mem.mmuAt {
		t.Fatal("DASM did not start")
	}
	snap, err := e.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	flash, _ := os.ReadFile("../os/flash_dump/flash_full_32mb.bin")
	f := NewEmulator(flash)
	if err := f.Resume(snap); err != nil {
		t.Fatal(err)
	}
	f.SetInstrPerSecond(70_000_000)
	if !slices.Equal(f.mmio.lcd.gram, e.mmio.lcd.gram) {
		t.Error("the panel was not restored")
	}
	if !slices.Equal(f.mem.xyram, e.mem.xyram) {
		t.Error("the X/Y/IL memories were not restored")
	}
	for i, m := range []*Emulator{e, f} {
		x0, g0 := m.Executed(), m.FrameGen()
		m.InjectKey(2, 7) // DOWN: DASM moves its cursor and redraws
		m.Step(60_000_000)
		if m.Executed()-x0 < 100_000 || m.FrameGen() == g0 || m.Fault() != "" {
			t.Fatalf("machine %d (1 = resumed): %d instructions after DOWN, redrawn %v, fault %q",
				i, m.Executed()-x0, m.FrameGen() != g0, m.Fault())
		}
	}
	if !slices.Equal(f.mmio.lcd.gram, e.mmio.lcd.gram) {
		t.Error("after DOWN the resumed machine shows something else than the original")
	}
}

// Upsilon (icon S in the 32 MB dump) sleeps 0 ms early on: gint arms TMU2 with TCOR=0, so it
// fires continuously until the callback stops it. gint's callback dispatcher re-enables
// interrupts and relies on CPUOPM.INTMU (IMASK = the accepted level) to block the same timer;
// without INTMU the interrupt nested until the stack overran gint's data and the add-in died
// (it used to show the first-boot language screen on the phone).
func TestUpsilonStarts(t *testing.T) {
	e := installState(t)
	for _, k := range [][2]uint32{{2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {1, 7}, {1, 7}, {1, 7}, {2, 1}} {
		e.InjectKey(k[0], k[1])
		e.Step(30_000_000)
	}
	e.Step(400_000_000)
	if f := e.Fault(); f != "" || !e.mem.mmuAt {
		t.Fatalf("Upsilon did not keep running: fault %q, mmu %v, pc %#x", f, e.mem.mmuAt, e.cpu.pc)
	}
	if e.mem.R32(0xFF2F0000)&cpuopmINTMU == 0 {
		t.Fatal("gint did not set CPUOPM.INTMU")
	}
	g := e.FrameGen()
	e.InjectKey(2, 7) // DOWN moves Upsilon's home-screen selection
	e.Step(100_000_000)
	if e.Fault() != "" || e.FrameGen() == g {
		t.Fatalf("no reaction to a key: fault %q", e.Fault())
	}
}
