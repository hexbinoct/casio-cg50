package main

import (
	"bytes"
	"os"
	"testing"
)

// Frame presentation: the host sees VRAM only when the OS pushes it to the LCD (DMA to area
// 5), never a redraw in progress. From the menu save-state: idle produces no pushes (so no
// spurious changes), a DOWN tap produces pushes, and the presented frame differs from live
// VRAM at some point during the redraw (i.e. it really is decoupled).
func TestPresentOnLCDPush(t *testing.T) {
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
	e.Step(3_000_000)
	idle := e.Pushes()
	shown := make([]byte, fbBytes)
	live := make([]byte, fbBytes)
	e.FramebufferRGB565(shown)
	e.VRAMRGB565(live)
	if !bytes.Equal(shown, live) {
		t.Fatalf("at idle the presented frame should equal VRAM")
	}
	e.InjectKey(2, 7) // DOWN
	decoupled := false
	for i := 0; i < 4_000_000; i++ {
		e.Step(1)
		if i%1000 == 0 {
			e.FramebufferRGB565(shown)
			e.VRAMRGB565(live)
			if !bytes.Equal(shown, live) {
				decoupled = true
			}
		}
	}
	pushes := e.Pushes() - idle
	t.Logf("pushes while idle 3M instr: %d; pushes during the DOWN redraw: %d; presented != live seen: %v", idle, pushes, decoupled)
	if pushes == 0 {
		t.Fatal("no VRAM->LCD push detected during a menu move: DMA hook condition wrong")
	}
	if !decoupled {
		t.Error("presented frame tracked live VRAM throughout; presentation is not decoupled")
	}
	e.Step(2_000_000)
	e.FramebufferRGB565(shown)
	e.VRAMRGB565(live)
	if !bytes.Equal(shown, live) {
		t.Error("after the redraw settled, presented frame should equal VRAM again")
	}
}
