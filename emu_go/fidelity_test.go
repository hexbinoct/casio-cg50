package cg50emu

import (
	"strings"
	"testing"
)

// Device and CPU behaviour fixed on 2026-10-09 after the wiki research (wiki/README.md, the
// emulator TODO list). No OS image needed.

func fidelityMachine() (*MMIOBus, *Memory, *CPU) {
	mmio := NewMMIOBus()
	mem := NewMemory(make([]byte, 0x1000), mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	return mmio, mem, cpu
}

// KEYSC +0x1A / +0x1C: column and row enables. 1 / 1 (the OS's setting while off) leaves only
// AC/ON (row 0, column 0); a key outside them is neither reported nor detected.
func TestKeyscEnables(t *testing.T) {
	mmio, mem, cpu := fidelityMachine()
	k := mmio.keysc
	k.scanPeriod = 100
	const B = 0xA44B0000
	mem.Write(B+0x0C, 2, 0x8000)
	mem.Write(B+0x14, 2, 0x4800)
	mem.Write(B+0x10, 2, 0x200)

	k.press(2, 1) // EXE: word 0 bit 10
	if got := mem.Read(B, 2); got != 0x0400 {
		t.Fatalf("all enabled (unwritten): word0 = %#x, want 0x400", got)
	}
	mem.Write(B+0x1A, 2, 1)
	mem.Write(B+0x1C, 2, 1)
	if got := mem.Read(B, 2); got != 0 {
		t.Fatalf("1/1 enables: EXE still reported: %#x", got)
	}
	cpu.cycles = 1000
	k.tick(cpu)
	if k.flags != 0 {
		t.Fatalf("1/1 enables: EXE detected: flags=%#x", k.flags)
	}
	k.press(0, 0) // AC/ON
	cpu.cycles = 2000
	k.tick(cpu)
	if got := mem.Read(B, 2); got != 0x0001 {
		t.Fatalf("1/1 enables: word0 = %#x, want AC/ON only (0x1)", got)
	}
	if k.flags&keyscFlagDetect == 0 {
		t.Fatalf("1/1 enables: AC/ON not detected: flags=%#x", k.flags)
	}
	mem.Write(B+0x1A, 2, 0x0FFF)
	mem.Write(B+0x1C, 2, 0xFF)
	if got := mem.Read(B, 2); got != 0x0401 {
		t.Fatalf("normal enables: word0 = %#x, want 0x401", got)
	}
}

// INTREQ00 (0xA4140024): the IRQ1/IRQ2 request flags. Nothing is plugged in, so none is set;
// the OS's standby loop reads a set flag as "woken by a cable".
func TestIRQPinFlagsClear(t *testing.T) {
	_, mem, _ := fidelityMachine()
	if got := mem.Read(0xA4140024, 1); got != 0 {
		t.Fatalf("INTREQ00 = %#x, want 0", got)
	}
	mem.Write(0xA4140024, 1, 0x40) // the OS's acknowledge writes: 1 keeps, 0 clears
	if got := mem.Read(0xA4140024, 1); got != 0 {
		t.Fatalf("INTREQ00 after writing 0x40 = %#x, want 0 (a write can't set a flag)", got)
	}
}

// FRQCR: KICK (bit 31) reads 0, and the OS's write (FRQCR & 0x000F00F0) | 0x8F001102 lands on
// the calculator's 0x0F011112.
func TestFRQCR(t *testing.T) {
	_, mem, _ := fidelityMachine()
	const FRQCR = 0xA4150000
	v := mem.Read(FRQCR, 4)
	mem.Write(FRQCR, 4, v&0x000F00F0|0x8F001102)
	if got := mem.Read(FRQCR, 4); got != 0x0F011112 {
		t.Fatalf("FRQCR = %#x, want 0x0F011112", got)
	}
}

// IL RAM is 16 KB, mirrored every 16 KB through the 64 KB window (the real dump repeats).
func TestILRAMMirror(t *testing.T) {
	_, mem, _ := fidelityMachine()
	mem.Write(0xFD800010, 4, 0xCAFEF00D)
	for _, a := range []uint32{0xFD804010, 0xFD808010, 0xFD80C010} {
		if got := mem.Read(a, 4); got != 0xCAFEF00D {
			t.Fatalf("%#x = %#x, want the mirror of 0xFD800010", a, got)
		}
	}
}

// The panel is dark while R007 has display-on clear or the backlight bit (PFC 0xA405012E bit 5)
// is clear, as at power-off; the frame generation changes so hosts redraw.
func TestPanelDarkWhenOff(t *testing.T) {
	mmio, mem, _ := fidelityMachine()
	l := mmio.lcd
	for i := range l.gram {
		l.gram[i] = 0xFFFF // the all-white frame the OS pushes before switching off
	}
	frame := make([]byte, FbWidth*FbHeight*2)
	lit := func() bool {
		l.renderOS(frame)
		return frame[0] != 0
	}
	if !lit() {
		t.Fatal("fresh machine: panel dark")
	}
	g0 := l.frameGen()
	selectReg := func(idx uint32) {
		mem.Write(0xA405013C, 1, 0)
		mem.Write(0xB4000000, 2, idx)
		mem.Write(0xA405013C, 1, 0x10)
	}
	selectReg(0x007)
	mem.Write(0xB4000000, 2, 0)
	if lit() {
		t.Fatal("R007 = 0: panel still lit")
	}
	if l.frameGen() == g0 {
		t.Fatal("frame generation unchanged after the panel went dark")
	}
	mem.Write(0xB4000000, 2, 0x0100)
	if !lit() {
		t.Fatal("R007 = 0x0100: panel dark")
	}
	mem.Write(0xA405012E, 1, 0)
	if lit() {
		t.Fatal("backlight bit clear: panel still lit")
	}
	mem.Write(0xA405012E, 1, 0x20)
	if !lit() {
		t.Fatal("backlight bit set: panel dark")
	}
	// a version-1 snapshot (before this model) never stored R007 = 0: it must come back lit
	m := l.stateMap()
	m[lcdKeyVer] = 1
	delete(m, lcdKeyReg|0x007)
	l.restoreState(m)
	if !lit() {
		t.Fatal("version-1 snapshot restored dark")
	}
}

// A general exception with SR.BL = 1 resets the real CPU: the emulator halts with the cause.
func TestIllegalWithBLHalts(t *testing.T) {
	_, mem, cpu := fidelityMachine()
	mem.Write(0x8C001000, 2, 0x0000) // undefined
	cpu.pc = 0x8C001000
	cpu.setSR(srMD | srBL)
	defer func() {
		x := recover()
		if s, _ := x.(string); !strings.Contains(s, "SR.BL=1") {
			t.Fatalf("want a halt naming SR.BL=1, got %v", x)
		}
	}()
	cpu.step()
}
