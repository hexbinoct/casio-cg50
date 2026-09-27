package main

import (
	"bytes"
	"os"
	"testing"
)

// End to end (cont.18q): the user's add-in (menu icon J, "helomelo2") runs through the MMU.
// The OS maps one code page + 8 RAM pages with ldtlb, enables MMUCR.AT and jumps to
// 0x00300000; further code pages arrive via TLB-miss exceptions. Without the MMU the add-in
// executed OS bytes at phys 0x00300000 (blank screen, then a fault that closed the Android app).
func TestAddinRunsThroughMMU(t *testing.T) {
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
	menu := make([]byte, fbBytes)
	e.FramebufferRGB565(menu)
	e.InjectKey(3, 5) // ')' selects icon J
	e.Step(40_000_000)
	if !e.mmio.ccn.at {
		t.Skip("icon J did not start an add-in (different flash dump?)")
	}
	shown := make([]byte, fbBytes)
	e.FramebufferRGB565(shown)
	if e.Fault() != "" || bytes.Equal(shown, menu) || !e.cpu.sleeping {
		t.Fatalf("add-in did not reach its prompt: fault=%q sleeping=%v", e.Fault(), e.cpu.sleeping)
	}
	prompt := append([]byte(nil), shown...)
	e.InjectKey(6, 2) // '1'
	e.Step(40_000_000)
	e.FramebufferRGB565(shown)
	if bytes.Equal(shown, prompt) {
		t.Fatal("add-in did not react to a key")
	}
	e.InjectKey(3, 8) // MENU
	e.Step(40_000_000)
	if f := e.Fault(); f != "" {
		t.Fatal(f)
	}
	if ev := e.mmio.ccn.regs[ccnEXPEVT]; ev == 0x0A0 || ev == 0x0C0 || ev == 0x080 {
		t.Fatalf("an MMU violation (EXPEVT %#x, TEA %#x) was raised", ev, e.mmio.ccn.regs[ccnTEA])
	}
	e.FramebufferRGB565(shown)
	// back at the MAIN MENU: its title bar (rows 0..23) matches the menu seen at resume
	if !bytes.Equal(shown[:24*FbWidth*2], menu[:24*FbWidth*2]) {
		t.Fatal("MENU from the add-in did not return to the main menu")
	}
	// and the OS is still healthy afterwards: the menu cursor moves and a full frame is pushed
	back := append([]byte(nil), shown...)
	p0 := e.Pushes()
	e.InjectKey(1, 8) // UP
	e.Step(20_000_000)
	e.FramebufferRGB565(shown)
	if e.Pushes() == p0 || bytes.Equal(shown, back) {
		t.Fatal("main menu stopped responding to keys after the add-in exited")
	}
}
