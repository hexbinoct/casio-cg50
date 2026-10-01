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
	if len(e.tap.queue) != 2 || e.tap.queue[0] != [2]uint32{2, 1} || e.tap.queue[1] != [2]uint32{6, 4} {
		t.Fatalf("key queue = %v", e.tap.queue)
	}

	// Framebuffer decode: write a known RGB565 pixel at (0,0) into DRAM and read it back RGBA.
	// 0xF800 = full red (R=31,G=0,B=0) -> RGBA (0xF8,0,0,0xFF).
	e.mem.dram[0], e.mem.dram[1] = 0xF8, 0x00
	// 0x07E0 = full green at pixel 1.
	e.mem.dram[2], e.mem.dram[3] = 0x07, 0xE0
	// FramebufferRGBA is the whole panel: OS pixel (0,0) is panel (6,0), the frame (colour
	// 0x001F here) is around it.
	rgba := make([]byte, PanelWidth*PanelHeight*4)
	e.mmio.lcd.seedFromVRAM(e.mem.dram[:fbBytes], 0x001F) // present the poked VRAM (no OS push in this test)
	e.FramebufferRGBA(rgba)
	if p := rgba[6*4:]; p[0] != 0xF8 || p[1] != 0 || p[2] != 0 || p[3] != 0xFF {
		t.Errorf("OS pixel 0 RGBA = %v, want red", p[0:4])
	}
	if p := rgba[7*4:]; p[0] != 0 || p[1] != 0xFC || p[2] != 0 || p[3] != 0xFF {
		t.Errorf("OS pixel 1 RGBA = %v, want green", p[0:4])
	}
	for _, xy := range [][2]int{{0, 0}, {5, 100}, {390, 0}, {395, 215}, {200, 216}, {395, 223}} {
		if p := rgba[(xy[1]*PanelWidth+xy[0])*4:]; p[0] != 0 || p[1] != 0 || p[2] != 0xF8 {
			t.Errorf("frame pixel %v RGBA = %v, want the frame colour (blue)", xy, p[0:4])
		}
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
	if len(e.tap.queue) != 0 || e.tap.active {
		t.Errorf("resume left key state: keys=%v have=%v", e.tap.queue, e.tap.active)
	}
}
