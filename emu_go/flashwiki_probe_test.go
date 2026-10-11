//go:build probe

package main

// Probe (2026-10-11, wiki flash.md): the OS's flash driver set-up as the emulator sees it.
//
//  1. Cold boot of the 16 MB image from reset: when the copy of the flash routines into IL RAM
//     (0x801DF4A4, syscall 0x11C9) runs, what the chip check 0xFD800380 returns, and the
//     program size the OS then keeps at 0x8C0A1160 (512 if the check returned 1, else 32).
//  2. From the saved state at the MAIN MENU: how often the copy runs when Run-Matrix is opened
//     and left with MENU.
//
//   go -C emu_go test -tags probe -run 'TestFlashWiki' -count=1 -v .

import (
	"os"
	"testing"
)

func TestFlashWikiColdBoot(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	e := NewEmulator(flash)
	c := e.cpu
	copies, checks := 0, 0
	var retAddr uint32
	last := uint32(0)
	done := uint64(0)
	for c.cycles < 400_000_000 && (done == 0 || c.cycles < done+1_000_000) {
		if v := e.mem.R32(0x8C0A1160); v != last {
			t.Logf("0x8C0A1160 = %#x at %d instr, pc=%08x", v, c.cycles, c.pc)
			last = v
		}
		switch c.pc {
		case 0x801DF4A4:
			copies++
			t.Logf("copy of the flash routines at %d instr, pr=%08x", c.cycles, c.pr)
		case 0xFD800380:
			checks++
			retAddr = c.pr
			t.Logf("chip check at %d instr: flash words 0x000=%04x 0x002=%04x 0x050=%04x 0x054=%04x",
				c.cycles, e.mem.R16(0xA0000000), e.mem.R16(0xA0000002), e.mem.R16(0xA0000050),
				e.mem.R16(0xA0000054))
		}
		if retAddr != 0 && c.pc == retAddr {
			t.Logf("chip check returned %d", int32(c.r[0]))
			retAddr = 0
			done = c.cycles
		}
		e.Step(1)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
	}
	t.Logf("after %d instr: copies=%d checks=%d, 0x8C0A1160 = %#x", c.cycles, copies, checks,
		e.mem.R32(0x8C0A1160))
}

// TestFlashWikiSR: which IL RAM routines read the flash (their status polls) while the OS
// writes a file to the storage memory and while it switches off, and with what SR (BL bit,
// interrupt mask) they run.
func TestFlashWikiSR(t *testing.T) {
	e := fwState(t)
	type key struct{ pc, sr uint32 }
	seen := map[key]int{}
	var order []key
	e.mem.flashRdHook = func(phys, size uint32) {
		pc := e.cpu.pc
		if pc < 0xFD800000 || pc >= 0xFD801000 {
			return
		}
		k := key{pc, e.cpu.sr & 0x100000F0}
		if seen[k] == 0 {
			order = append(order, k)
		}
		seen[k]++
	}
	report := func(what string) {
		for _, k := range order {
			t.Logf("%s: read at pc=%08x (phys %s) SR.BL=%d IMASK=%d  x%d", what, k.pc, "", k.sr>>28&1,
				k.sr>>4&15, seen[k])
		}
		seen, order = map[key]int{}, nil
	}
	flash0 := append([]byte(nil), e.mem.flash...)
	e.mu.Lock()
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte(i * 13)
	}
	if !e.osIdle() {
		e.tapAndSettle(6, 2, 0)
	}
	err := e.writeFile("FLASHWK.BIN", data)
	e.mu.Unlock()
	t.Logf("writeFile: %v", err)
	report("file write")
	fwDiff(t, flash0, e.mem.flash)

	flash0 = append([]byte(nil), e.mem.flash...)
	e.mu.Lock()
	e.tapAndSettle(3, 8, 1) // MENU back to the main menu
	e.mu.Unlock()
	e.InjectKey(6, 8) // SHIFT
	e.Step(50_000_000)
	e.InjectKey(0, 0) // AC/ON with SHIFT = OFF
	e.Step(300_000_000)
	report("power-off")
	fwDiff(t, flash0, e.mem.flash)
	e.mem.flashRdHook = nil
}

// fwDiff logs the 128 KB sectors whose contents changed.
func fwDiff(t *testing.T, a, b []byte) {
	for s := 0; s < len(a); s += 0x20000 {
		n, lo, hi := 0, -1, 0
		for i := s; i < s+0x20000; i++ {
			if a[i] != b[i] {
				if lo < 0 {
					lo = i
				}
				hi = i
				n++
			}
		}
		if n > 0 {
			t.Logf("  sector %#08x: %d bytes changed in %#x-%#x", s, n, lo, hi)
		}
	}
}

func fwState(t *testing.T) *Emulator {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(20_000_000)
	return e
}

func TestFlashWikiAppLaunch(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(20_000_000)
	t.Logf("at the menu: 0x8C0A1160 = %#x, atMainMenu=%v", e.mem.R32(0x8C0A1160), e.atMainMenu())
	copies := 0
	e.cpu.hlePC = 0x801DF4A4
	e.cpu.hle = func() bool {
		copies++
		t.Logf("copy, pr=%08x", e.cpu.pr)
		return false
	}
	e.mu.Lock()
	e.tapAndSettle(6, 2, 0) // 1: Run-Matrix
	t.Logf("after opening Run-Matrix: copies=%d", copies)
	e.tapAndSettle(3, 8, 1) // MENU
	e.mu.Unlock()
	t.Logf("after MENU: copies=%d", copies)
}
