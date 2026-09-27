package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Register-level protocol of the KEYSC model, exercised the way the OS driver does
// (init FUN_801e51c8, ISR FUN_801e4c00, sync scan FUN_801e5700). No OS image needed.
func TestKeyscProtocol(t *testing.T) {
	mmio := NewMMIOBus()
	mem := NewMemory(make([]byte, 0x1000), mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	k := mmio.keysc
	k.scanPeriod = 100
	const B = 0xA44B0000

	// Not enabled yet: a held key never raises anything.
	k.press(2, 7)
	cpu.cycles = 1000
	k.tick(cpu)
	if k.flags != 0 || len(cpu.pending) != 0 {
		t.Fatalf("disabled unit raised: flags=%#x pending=%v", k.flags, cpu.pending)
	}
	k.releaseAll()

	// OS init sequence.
	mem.Write(B+0x0C, 2, 0x8000)
	mem.Write(B+0x14, 2, 0x00FF) // clear all flags
	mem.Write(B+0x14, 2, 0x4800) // arm detect (bit3) + bit6
	mem.Write(B+0x0E, 2, 0x8042)
	mem.Write(B+0x18, 2, 0xC8)
	mem.Write(B+0x10, 2, 0x200) // normal mode
	if got := mem.Read(B+0x14, 2); got != 0x4800 {
		t.Fatalf("status after init = %#x, want 0x4800", got)
	}
	cpu.cycles = 2000
	k.tick(cpu)
	if k.flags != 0 || len(cpu.pending) != 0 {
		t.Fatalf("idle scan raised: flags=%#x", k.flags)
	}

	// Press DOWN (row 2, col 7): word 3 bit 10. Next scan -> detect + scan flags, IRQ 0xBE0.
	k.press(2, 7)
	cpu.cycles = 2100
	k.tick(cpu)
	if got := mem.Read(B+6, 2); got != 1<<10 {
		t.Fatalf("word3 = %#x, want %#x", got, 1<<10)
	}
	if got := mem.Read(B+0x14, 2); got != 0x480A {
		t.Fatalf("status after press = %#x, want 0x480A (detect+scan)", got)
	}
	if len(cpu.pending) != 1 || cpu.pending[0].intevt != KeyscINTEVT || cpu.pending[0].level != KeyscLevel {
		t.Fatalf("pending = %+v, want INTEVT 0xBE0 level 13", cpu.pending)
	}
	// ISR acks what it read and arms the "held" mask 0x76.
	mem.Write(B+0x14, 2, 0x760A)
	if got := mem.Read(B+0x14, 2); got != 0x7600 {
		t.Fatalf("status after ack = %#x, want 0x7600", got)
	}
	cpu.pending = nil
	// Still held: each scan reports scan-complete (bit1) only, and re-raises.
	cpu.cycles = 2200
	k.tick(cpu)
	if got := mem.Read(B+0x14, 2); got != 0x7602 {
		t.Fatalf("status while held = %#x, want 0x7602", got)
	}
	if len(cpu.pending) != 1 {
		t.Fatalf("no IRQ while held")
	}
	mem.Write(B+0x14, 2, 0x7602)
	cpu.pending = nil
	// Release: two more scans report scan-complete with an empty matrix, then silence.
	k.release(2, 7)
	for i, want := range []uint32{0x7602, 0x7602, 0x7600} {
		cpu.cycles += 100
		k.tick(cpu)
		got := mem.Read(B+0x14, 2)
		if got != want {
			t.Fatalf("scan %d after release: status=%#x want %#x", i, got, want)
		}
		mem.Write(B+0x14, 2, got)
		cpu.pending = nil
	}
	if got := mem.Read(B+6, 2); got != 0 {
		t.Fatalf("word3 after release = %#x", got)
	}
	// Back to idle mask; a second press produces a fresh detect edge.
	mem.Write(B+0x14, 2, 0x4800)
	k.press(0, 0) // AC/ON = word0 bit0
	cpu.cycles += 100
	k.tick(cpu)
	if got := mem.Read(B+0, 2); got != 1 {
		t.Fatalf("word0 = %#x, want 1 (AC/ON)", got)
	}
	if got := mem.Read(B+0x14, 2); got&0x8 == 0 {
		t.Fatalf("no detect edge on second press: %#x", got)
	}
	k.releaseAll()

	// Sync scan (FUN_801e5700): mode 0x800 reports scan-complete every period, key or not.
	mem.Write(B+0x14, 2, 0x00FF)
	mem.Write(B+0x10, 2, 0x800)
	cpu.cycles += 300
	k.tick(cpu)
	if got := mem.Read(B+0x14, 2); got&0x2 == 0 {
		t.Fatalf("mode 0x800 did not report scan-complete: %#x", got)
	}
	if got := mem.Read(B+0x12, 2); got != 0 {
		t.Fatalf("busy bit set: %#x", got)
	}
}

// Matrix bit layout for every key in re/KEYMAP.md's grid: word = col>>1, bit = row+8*(col&1).
func TestKeyscMatrixLayout(t *testing.T) {
	k := newKeysc()
	cases := []struct{ row, col, word, bit uint32 }{
		{0, 0, 0, 0},  // AC/ON
		{2, 7, 3, 10}, // DOWN
		{1, 7, 3, 9},  // RIGHT
		{1, 8, 4, 1},  // UP
		{2, 8, 4, 2},  // LEFT
		{6, 9, 4, 14}, // F1
		{2, 1, 0, 10}, // EXE
		{6, 1, 0, 14}, // 0
	}
	for _, c := range cases {
		k.releaseAll()
		k.press(c.row, c.col)
		for w := range k.held {
			want := uint16(0)
			if uint32(w) == c.word {
				want = 1 << c.bit
			}
			if k.held[w] != want {
				t.Errorf("key (%d,%d): word%d = %#x, want %#x", c.row, c.col, w, k.held[w], want)
			}
		}
	}
}

// Oracle parity: replay the fixed op script of emu/keysc_selftest.py against the Go model and
// require a byte-identical transcript to the Python-generated emu/keysc_golden.txt.
func TestKeyscOracleTranscript(t *testing.T) {
	want, err := os.ReadFile("../emu/keysc_golden.txt")
	if err != nil {
		t.Skipf("no transcript golden (%v) — run: python emu/keysc_selftest.py", err)
	}
	type op struct {
		kind string
		a, b uint32
	}
	script := []op{
		{"press", 2, 7}, {"tick", 100, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"release", 2, 7},
		{"w", 0x0C, 0x8000}, {"w", 0x14, 0x00FF}, {"w", 0x14, 0x4800},
		{"w", 0x0E, 0x8042}, {"w", 0x18, 0xC8}, {"w", 0x10, 0x200},
		{"r", 0x0C, 2}, {"r", 0x0E, 2}, {"r", 0x10, 2}, {"r", 0x12, 2}, {"r", 0x14, 2},
		{"tick", 200, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"press", 2, 7}, {"press", 6, 9}, {"tick", 300, 0},
		{"r", 0, 4}, {"r", 6, 2}, {"r", 6, 1}, {"r", 7, 1}, {"r", 8, 2}, {"r", 8, 4}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"w", 0x14, 0x760A}, {"r", 0x14, 2},
		{"tick", 350, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"tick", 400, 0}, {"r", 0x14, 2}, {"irq", 0, 0}, {"w", 0x14, 0x7602},
		{"release", 2, 7}, {"tick", 500, 0}, {"r", 6, 2}, {"r", 0x14, 2}, {"irq", 0, 0}, {"w", 0x14, 0x7602},
		{"release", 6, 9}, {"tick", 600, 0}, {"r", 0x14, 2}, {"irq", 0, 0}, {"w", 0x14, 0x7602},
		{"tick", 700, 0}, {"r", 0x14, 2}, {"irq", 0, 0}, {"w", 0x14, 0x7602},
		{"tick", 800, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"w", 0x14, 0x4800}, {"press", 0, 0}, {"tick", 900, 0}, {"r", 0, 2}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"release", 0, 0}, {"w", 0x14, 0x00FF},
		{"w", 0x10, 0x800}, {"tick", 1000, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"w", 0x14, 0x00FF}, {"w", 0x10, 0x400}, {"press", 1, 8}, {"tick", 1100, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"tick", 1200, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"w", 0x10, 0}, {"tick", 1300, 0}, {"r", 0x14, 2}, {"irq", 0, 0},
		{"release", 1, 8}, {"press", 9, 0}, {"press", 0, 12}, {"r", 0, 2}, {"r", 0xA, 2},
		{"w", 0x1A, 0x0FFF}, {"r", 0x1A, 2},
	}
	mmio := NewMMIOBus()
	mem := NewMemory(make([]byte, 0x1000), mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	k := mmio.keysc
	k.scanPeriod = 100
	const B = 0xA44B0000
	var sb bytes.Buffer
	for _, o := range script {
		switch o.kind {
		case "press":
			k.press(o.a, o.b)
			fmt.Fprintf(&sb, "press %d %d\n", o.a, o.b)
		case "release":
			k.release(o.a, o.b)
			fmt.Fprintf(&sb, "release %d %d\n", o.a, o.b)
		case "w":
			k.write(B+o.a, 2, o.b)
			fmt.Fprintf(&sb, "w %03x %04x\n", o.a, o.b)
		case "r":
			fmt.Fprintf(&sb, "r %03x/%d = %x\n", o.a, o.b, k.read(B+o.a, o.b))
		case "tick":
			cpu.cycles = uint64(o.a)
			k.tick(cpu)
			fmt.Fprintf(&sb, "tick %d\n", o.a)
		case "irq":
			sb.WriteString("irq")
			for _, p := range cpu.pending {
				fmt.Fprintf(&sb, " %x/%d", p.intevt, p.level)
			}
			sb.WriteString("\n")
			cpu.pending = nil
		}
	}
	if got := sb.String(); got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := range gl {
			if i >= len(wl) || gl[i] != wl[i] {
				w := "<eof>"
				if i < len(wl) {
					w = wl[i]
				}
				t.Fatalf("transcript diverges at line %d:\n  go:     %q\n  oracle: %q", i+1, gl[i], w)
			}
		}
		t.Fatalf("transcript length differs: go %d lines, oracle %d", len(gl), len(wl))
	}
}

// End-to-end through the real OS (needs the flash image + menu save-state; skipped without):
// a DOWN tap at the MAIN MENU must run the keyboard ISR, be decoded by the app's getkey, and
// move the cursor — with no injection shortcut and no retry timeout. Also logs the latency
// (was ~20M instructions with the old flushed-then-retried injector).
func TestKeyscMenuTap(t *testing.T) {
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
	e.Step(1_000_000) // settle in the idle getkey loop
	before := make([]byte, fbBytes)
	e.FramebufferRGB565(before)

	e.InjectKey(2, 7) // DOWN
	isrAt, decodeAt, fbAt := -1, -1, -1
	fb := make([]byte, fbBytes)
	const limit = 6_000_000
	for i := 0; i < limit; i++ {
		e.Step(1)
		pc := e.cpu.pc
		if isrAt < 0 && pc == 0x801e4c00 {
			isrAt = i
		}
		if decodeAt < 0 && pc == 0x801952cc {
			decodeAt = i
		}
		if fbAt < 0 && i%2000 == 0 {
			e.FramebufferRGB565(fb)
			if !bytes.Equal(fb, before) {
				fbAt = i
			}
		}
		if isrAt >= 0 && decodeAt >= 0 && fbAt >= 0 {
			break
		}
	}
	t.Logf("DOWN tap: ISR after %d instr, getkey decode after %d, framebuffer changed after %d", isrAt, decodeAt, fbAt)
	if isrAt < 0 {
		t.Fatal("keyboard ISR FUN_801e4c00 never ran: KEYSC IRQ not delivered")
	}
	if decodeAt < 0 {
		t.Fatal("app key decoder FUN_801952cc never ran: key not enqueued by the ISR")
	}
	if fbAt < 0 {
		t.Fatal("framebuffer never changed: menu cursor did not move")
	}
	if fbAt > 3_000_000 {
		t.Errorf("first redraw took %d instr; expected well under 3M", fbAt)
	}
	// the redraw starts before the tap's hold (3 scans) is over: let the tap finish
	for i := 0; i < 10_000_000 && e.tap.active; i++ {
		e.Step(1)
	}
	if e.tap.active || len(e.tap.queue) != 0 {
		t.Errorf("tap not finished: %+v", e.tap)
	}
}

// A host tap so fast that KeyUp arrives before the core runs a single instruction (seen on
// the phone: down+up delivered while Step held the mutex for a whole slice) must still be
// seen by the scanner: the release is deferred by the minimum hold.
func TestKeyscFastTapStillLands(t *testing.T) {
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
	e.Step(1_000_000)
	e.KeyDown(2, 7)
	e.KeyUp(2, 7) // immediately, no instructions in between
	if len(e.pendingUp) != 1 || !e.mmio.keysc.anyHeld() {
		t.Fatalf("release not deferred: pending=%v held=%v", e.pendingUp, e.mmio.keysc.held)
	}
	decodes := 0
	for i := 0; i < 6_000_000; i++ {
		e.Step(1)
		if e.cpu.pc == 0x801952cc {
			decodes++
		}
	}
	if decodes != 1 {
		t.Fatalf("fast tap decoded %d times, want exactly 1", decodes)
	}
	if e.mmio.keysc.anyHeld() || len(e.pendingUp) != 0 {
		t.Fatalf("key still held after minimum hold: held=%v pending=%v", e.mmio.keysc.held, e.pendingUp)
	}
}

// Holding an arrow (KeyDown, no KeyUp) must auto-repeat through the OS's own repeat logic:
// the menu keeps moving, and stops once released.
func TestKeyscHoldRepeats(t *testing.T) {
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
	e.Step(1_000_000)
	// A "move" = the app's getkey decoding a key (FUN_801952cc); the framebuffer itself
	// changes hundreds of times per redraw (icon by icon), so it is not a usable move counter.
	moves := func(n int) int {
		c := 0
		for i := 0; i < n; i++ {
			e.Step(1)
			if e.cpu.pc == 0x801952cc {
				c++
			}
		}
		return c
	}
	e.KeyDown(1, 7) // RIGHT held
	held := moves(40_000_000)
	e.KeyUp(1, 7)
	released := moves(20_000_000)
	t.Logf("key decodes: %d while RIGHT held for 40M instr, %d after release", held, released)
	if held < 3 {
		t.Errorf("held arrow decoded only %d times; expected auto-repeat", held)
	}
	if released > 2 {
		t.Errorf("keys kept arriving after release (%d decodes)", released)
	}
}
