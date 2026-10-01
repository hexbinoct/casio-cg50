package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Register protocol: RS (PFC 0xA405013C bit4) low selects the index, high reads/writes the
// selected register; entry mode R003 reads back (the OS's setORG/getORG depend on it).
func TestLCDRegisterReadback(t *testing.T) {
	b := NewMMIOBus()
	sel := func(i uint32) { b.Write(0xA405013C, 1, 0); b.Write(0xB4000000, 2, i); b.Write(0xA405013C, 1, 0x10) }
	sel(0x003)
	if v := b.Read(0xB4000000, 2); v != 0x00A0 {
		t.Fatalf("R003 after reset = %#x, want boot value 0xA0", v)
	}
	b.Write(0xB4000000, 2, 0x0030)
	sel(0x211)
	sel(0x003)
	if v := b.Read(0xB4000000, 2); v != 0x0030 {
		t.Fatalf("R003 read-back = %#x, want 0x30", v)
	}
}

// Window addressing with ORG=1 and the OS's entry mode (H decrement, V increment): a 3x2
// block written after R200/R201 = 0 fills the window starting at (HEA, VSA).
func TestLCDWindowStream(t *testing.T) {
	b := NewMMIOBus()
	l := b.lcd
	sel := func(i uint32) { b.Write(0xA405013C, 1, 0); b.Write(0xB4000000, 2, i); b.Write(0xA405013C, 1, 0x10) }
	put := func(i, v uint32) { sel(i); b.Write(0xB4000000, 2, v) }
	put(0x003, 0x00A0)
	put(0x210, 10)
	put(0x211, 12)
	put(0x212, 20)
	put(0x213, 21)
	put(0x200, 0)
	put(0x201, 0)
	sel(0x202)
	for i := uint32(1); i <= 6; i++ {
		b.Write(0xB4000000, 2, i)
	}
	want := map[[2]int]uint16{{12, 20}: 1, {11, 20}: 2, {10, 20}: 3, {12, 21}: 4, {11, 21}: 5, {10, 21}: 6}
	for hv, c := range want {
		if got := l.gram[hv[1]*panelW+hv[0]]; got != c {
			t.Errorf("GRAM(H=%d,V=%d) = %d, want %d", hv[0], hv[1], got, c)
		}
	}
}

// DrawFrame (0x800561EE) paints the frame around the OS's area with a DMA fill: a fixed
// source (CHCR.SM=0) of 32 bytes of the frame colour, repeated into the LCD data port
// (FUN_800562A2). The source must not advance into the memory after it, and a fill is not a
// VRAM frame push.
func TestLCDDMAFill(t *testing.T) {
	e := NewEmulator(make([]byte, 0x1000))
	w := func(a, v uint32) { e.mem.Write(a, 2, v) }
	sel := func(i uint32) { e.mem.Write(0xA405013C, 1, 0); w(0xB4000000, i); e.mem.Write(0xA405013C, 1, 0x10) }
	put := func(i, v uint32) { sel(i); w(0xB4000000, v) }
	for i := uint32(0); i < 16; i++ {
		w(0xAC028800+2*i, 0xF800) // the colour block the OS fills
		w(0xAC028820+2*i, 0x1234) // what follows it in memory
	}
	put(0x003, 0x00A0)
	put(0x210, 0) // the left frame strip: H 390..395 is panel x 0..5
	put(0x211, 5)
	put(0x212, 0)
	put(0x213, 215)
	put(0x200, 0)
	put(0x201, 0)
	sel(0x202)
	e.mem.Write(0xFE008020, 4, 0x0C028800)   // SAR0
	e.mem.Write(0xFE008024, 4, 0x14000000)   // DAR0: the LCD data port
	e.mem.Write(0xFE008028, 4, 6*216*2>>5)   // TCR0: 32-byte units
	e.mem.Write(0xFE00802C, 4, 0x00100400|1) // CHCR0: 32-byte units, SM=DM=0, auto, DE
	for v := 0; v < 216; v++ {
		for h := 0; h < 6; h++ {
			if c := e.mmio.lcd.gram[v*panelW+h]; c != 0xF800 {
				t.Fatalf("GRAM(H=%d,V=%d) = %#x, want the fill colour 0xf800", h, v, c)
			}
		}
	}
	if e.pushes != 0 {
		t.Errorf("a fill counted as %d frame pushes", e.pushes)
	}
}

// End to end: in Run-Matrix the text cursor blinks. The OS draws it straight into GRAM
// (0x800c4006 / 0x800c4266 via the 2 Hz RTC event), never into VRAM and without a DMA push,
// so only a real panel model shows it (cont.18o).
func TestCursorBlinks(t *testing.T) {
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
	const ips = 70_000_000
	e.SetInstrPerSecond(ips)
	e.Step(1_000_000)
	e.InjectKey(2, 1) // EXE -> Run-Matrix
	e.Step(20_000_000)
	e.InjectKey(6, 2) // '1'
	e.InjectKey(5, 2) // '2'
	e.Step(30_000_000)

	shown := make([]byte, fbBytes)
	prev := make([]byte, fbBytes)
	vram0 := make([]byte, fbBytes)
	e.FramebufferRGB565(prev)
	e.VRAMRGB565(vram0)
	p0 := e.Pushes()
	changes := 0
	x0, y0, x1, y1 := FbWidth, FbHeight, -1, -1
	for i := 0; i < 2*ips/1_000_000; i++ { // 2 s, sampled every 1M instr
		e.Step(1_000_000)
		e.FramebufferRGB565(shown)
		if bytes.Equal(shown, prev) {
			continue
		}
		changes++
		for p := 0; p < FbWidth*FbHeight; p++ {
			if shown[2*p] != prev[2*p] || shown[2*p+1] != prev[2*p+1] {
				x, y := p%FbWidth, p/FbWidth
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
		copy(prev, shown)
	}
	live := make([]byte, fbBytes)
	e.VRAMRGB565(live)
	t.Logf("2 s idle: %d frame changes, pushes %d, changed box x[%d..%d] y[%d..%d]", changes,
		e.Pushes()-p0, x0, x1, y0, y1)
	if changes < 3 {
		t.Fatalf("cursor did not blink: %d displayed-frame changes in 2 s (want ~4 at 2 Hz)", changes)
	}
	if e.Pushes() != p0 || !bytes.Equal(live, vram0) {
		t.Errorf("blink should touch only GRAM (pushes %d, VRAM changed %v)", e.Pushes()-p0, !bytes.Equal(live, vram0))
	}
	// "12" = two 18-px glyphs on the input line, so the caret sits at x=36.
	if x0 < 34 || x1 > 42 || y1-y0 > 30 {
		t.Errorf("changes outside the caret cell after \"12\": x[%d..%d] y[%d..%d]", x0, x1, y0, y1)
	}
}

// Go must read exactly what the Python oracle read for the same op sequence
// (emu/lcd_golden.txt, generated by `python emu/lcd_selftest.py`).
func TestLCDOracleTranscript(t *testing.T) {
	raw, err := os.ReadFile("../emu/lcd_golden.txt")
	if err != nil {
		t.Skipf("no transcript golden (%v) — run: python emu/lcd_selftest.py", err)
	}
	b := NewMMIOBus()
	reads := 0
	for n, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("line %d: bad op %q", n+1, line)
		}
		v, err := strconv.ParseUint(f[1], 16, 32)
		if err != nil {
			t.Fatalf("line %d: bad value %q", n+1, line)
		}
		switch f[0] {
		case "pfc":
			b.Write(0xA405013C, 1, uint32(v))
		case "w":
			b.Write(0xB4000000, 2, uint32(v))
		case "r":
			reads++
			if got := b.Read(0xB4000000, 2); got != uint32(v) {
				t.Errorf("line %d: read %#04x, oracle read %#04x", n+1, got, v)
			}
		default:
			t.Fatalf("line %d: unknown op %q", n+1, f[0])
		}
	}
	if reads == 0 {
		t.Fatal("transcript has no reads")
	}
}
