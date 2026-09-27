package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"testing"
)

// Event-scheduled device ticking (Emulator.Step) must be exactly equivalent to polling every
// device before every instruction: run the same scripted session both ways — menu, Run-Matrix,
// typing, a held key with OS auto-repeat, a fast tap, idle with the cursor blinking, MENU,
// the user's add-in — and require bit-identical machines.
func TestScheduledStepMatchesExact(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no menu save-state")
	}
	type result struct {
		regs          []uint32
		cycles, push  uint64
		dram, ilram   [32]byte
		frame         []byte
		sleeping, mmu bool
	}
	run := func(exact bool) result {
		e := NewEmulator(flash)
		if err := e.Resume(st); err != nil {
			t.Fatal(err)
		}
		e.exactTick = exact
		e.SetInstrPerSecond(30_000_000)
		frames := func(n int) { // host-style: many small slices
			for i := 0; i < n; i++ {
				e.Step(500_000)
			}
		}
		frames(4)
		e.InjectKey(2, 1) // EXE -> Run-Matrix
		frames(40)
		for _, k := range [][2]uint32{{6, 2}, {5, 2}, {3, 2}, {4, 2}} { // 1 2 + 3
			e.InjectKey(k[0], k[1])
		}
		frames(60)
		e.KeyDown(2, 8) // hold LEFT: initial delay then OS auto-repeat
		frames(80)
		e.KeyUp(2, 8)
		e.KeyDown(1, 7) // fast tap RIGHT (release deferred to the minimum hold)
		e.KeyUp(1, 7)
		frames(20)
		e.SetInstrPerSecond(45_000_000) // host speed change re-times RTC/KEYSC
		frames(120)                     // idle: RTC 2 Hz cursor blink, sleep
		e.InjectKey(3, 8)               // MENU
		frames(60)
		e.InjectKey(3, 5) // icon J (add-in, if present in this dump)
		frames(80)
		e.InjectKey(6, 2)
		frames(60)
		e.InjectKey(3, 8)
		frames(60)
		if f := e.Fault(); f != "" {
			t.Fatalf("exact=%v: %s", exact, f)
		}
		r := result{regs: e.cpu.snapshotRegs(), cycles: e.cpu.cycles, push: e.pushes,
			dram: sha256.Sum256(e.mem.dram), ilram: sha256.Sum256(e.mem.ilram),
			frame: make([]byte, fbBytes), sleeping: e.cpu.sleeping, mmu: e.mmio.ccn.at}
		e.FramebufferRGB565(r.frame)
		return r
	}
	a, b := run(true), run(false)
	if a.cycles != b.cycles || a.push != b.push || a.dram != b.dram || a.ilram != b.ilram ||
		!bytes.Equal(a.frame, b.frame) || a.sleeping != b.sleeping || a.mmu != b.mmu {
		t.Fatalf("scheduled run diverged: cycles %d/%d pushes %d/%d dram-equal %v ilram-equal %v frame-equal %v",
			a.cycles, b.cycles, a.push, b.push, a.dram == b.dram, a.ilram == b.ilram, bytes.Equal(a.frame, b.frame))
	}
	for i := range a.regs {
		if a.regs[i] != b.regs[i] {
			t.Fatalf("register %d differs: %#x vs %#x", i, a.regs[i], b.regs[i])
		}
	}
	t.Logf("identical after %d cycles, %d frame pushes", a.cycles, a.push)
}
