package main

import "testing"

// The Emulator facade is the API the web UI and the Android bridge drive. These tests cover
// the host-facing surface that doesn't need the (uncommitted) OS image: the key queue, the
// framebuffer decode, and snapshot/resume round-tripping through the facade.
func TestEmulatorFacade(t *testing.T) {
	img := make([]byte, 0x1000)
	e := NewEmulator(img)

	// InjectKey just enqueues (drained during Step at safe points); check it's thread-safe-ish
	// and queues in order without panicking.
	e.InjectKey(2, 1)
	e.InjectKey(6, 4)
	if len(e.keys) != 2 || e.keys[0] != [2]uint32{2, 1} || e.keys[1] != [2]uint32{6, 4} {
		t.Fatalf("key queue = %v", e.keys)
	}

	// Framebuffer decode: write a known RGB565 pixel at (0,0) into DRAM and read it back RGBA.
	// 0xF800 = full red (R=31,G=0,B=0) -> RGBA (0xF8,0,0,0xFF).
	e.mem.dram[0], e.mem.dram[1] = 0xF8, 0x00
	// 0x07E0 = full green at pixel 1.
	e.mem.dram[2], e.mem.dram[3] = 0x07, 0xE0
	rgba := make([]byte, FbWidth*FbHeight*4)
	e.FramebufferRGBA(rgba)
	if rgba[0] != 0xF8 || rgba[1] != 0 || rgba[2] != 0 || rgba[3] != 0xFF {
		t.Errorf("pixel0 RGBA = %v, want red", rgba[0:4])
	}
	if rgba[4] != 0 || rgba[5] != 0xFC || rgba[6] != 0 || rgba[7] != 0xFF {
		t.Errorf("pixel1 RGBA = %v, want green", rgba[4:8])
	}
	rgb565 := make([]byte, fbBytes)
	e.FramebufferRGB565(rgb565)
	if rgb565[0] != 0xF8 || rgb565[3] != 0xE0 {
		t.Errorf("rgb565 raw not copied: %v", rgb565[0:4])
	}

	// Snapshot / Resume through the facade: mutate state, snapshot, mutate again, resume,
	// and confirm the snapshot won.
	e.cpu.r[3] = 0xABCD1234
	e.mem.dram[500] = 0x5A
	blob, err := e.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	e.cpu.r[3] = 0
	e.mem.dram[500] = 0
	if err := e.Resume(blob); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if e.cpu.r[3] != 0xABCD1234 || e.mem.dram[500] != 0x5A {
		t.Errorf("resume didn't restore: r3=%08x dram[500]=%02x", e.cpu.r[3], e.mem.dram[500])
	}
	// Resume clears the key queue and injection state.
	if len(e.keys) != 0 || e.haveKey {
		t.Errorf("resume left key state: keys=%v have=%v", e.keys, e.haveKey)
	}
}
