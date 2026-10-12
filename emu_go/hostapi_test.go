package cg50emu

import (
	"bytes"
	"testing"
)

// The host-facing calls added for the desktop app (app/): FlashImage and SetWarmBoot.
func TestFlashImage(t *testing.T) {
	img := bytes.Repeat([]byte{0x12, 0x34}, 0x1000)
	e := NewEmulator(img)
	got := e.FlashImage()
	if len(got) != FlashMutTop {
		t.Fatalf("FlashImage is %#x bytes, want %#x", len(got), FlashMutTop)
	}
	if !bytes.Equal(got[:len(img)], img) || got[len(img)] != 0xFF || got[len(got)-1] != 0xFF {
		t.Fatal("FlashImage is not the image followed by erased flash")
	}
	got[0] ^= 0xFF
	if e.FlashImage()[0] != img[0] {
		t.Fatal("FlashImage returned the live array, not a copy")
	}
}

func TestSetWarmBoot(t *testing.T) {
	e := NewEmulator(make([]byte, 0x1000))
	e.SetWarmBoot()
	if v := e.mem.R32(warmFlagAddr); v != 1 {
		t.Fatalf("IL RAM %#x = %d after SetWarmBoot, want 1", warmFlagAddr, v)
	}
}

// A burst of taps delivered faster than one minimum hold (down, up, down, up with no Step in
// between, as a browser sends quick typing) must reach the matrix as separate presses: each
// held tapHoldScans scans, in order, never two at once (the OS ignores a two-key press).
func TestKeyBurstNoOverlap(t *testing.T) {
	// a CPU that only sleeps: "sleep; bra 0; nop" at the reset vector
	img := make([]byte, 0x1000)
	copy(img, []byte{0x00, 0x1B, 0xAF, 0xFD, 0x00, 0x09})
	e := NewEmulator(img)
	scan := e.mmio.keysc.scanPeriod
	keys := [][2]uint32{{6, 2}, {5, 2}, {4, 2}, {2, 1}} // 1 2 3 EXE
	for _, k := range keys {
		e.KeyDown(k[0], k[1])
		e.KeyUp(k[0], k[1])
	}
	isDown := func(k [2]uint32) bool {
		return e.mmio.keysc.held[k[1]>>1]&(1<<(k[0]+8*(k[1]&1))) != 0
	}
	step := scan / 8
	held := make([]uint64, len(keys)) // instructions each key was seen down
	first := make([]int, len(keys))
	for i := range first {
		first[i] = -1
	}
	for n := 0; n < 40*8; n++ {
		e.Step(int(step))
		down := 0
		for i, k := range keys {
			if isDown(k) {
				down++
				held[i] += step
				if first[i] < 0 {
					first[i] = n
				}
			}
		}
		if down > 1 {
			t.Fatalf("after %d steps %d keys are down at once", n, down)
		}
	}
	for i := range keys {
		if held[i] < tapHoldScans*scan-step {
			t.Errorf("key %d held %d instructions, want at least %d", i, held[i], tapHoldScans*scan)
		}
		if i > 0 && first[i] <= first[i-1] {
			t.Errorf("key %d went down before key %d", i, i-1)
		}
	}
	if len(e.pendingDown) != 0 || len(e.pendingUp) != 0 {
		t.Errorf("still pending: %d presses, %d releases", len(e.pendingDown), len(e.pendingUp))
	}
}
