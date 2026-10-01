package main

import (
	"bytes"
	"os"
	"testing"
)

// A sector erase is not instant: the OS's erase routine (IL RAM 0xFD800BB6) reads status right
// after the command and reports a failure unless the chip says "busy, erase started" (DQ7=0,
// DQ3=1), then polls until DQ7=1. The whole 128 KB sector goes to 0xFF.
func TestFlashSectorEraseStatus(t *testing.T) {
	m := NewMemory(make([]byte, 0x80000), NewMMIOBus())
	const sect = 0x40000 // second half of a 128 KB sector: erases 0x40000..0x5ffff
	m.flash[0x3ffff], m.flash[0x40000], m.flash[0x5fffe], m.flash[0x60000] = 0x11, 0x22, 0x33, 0x44
	for _, c := range [][2]uint32{{0xAAA, 0xAA}, {0x554, 0x55}, {0xAAA, 0x80}, {0xAAA, 0xAA}, {0x554, 0x55}, {sect + 0x10000, 0x30}} {
		m.Write(0xA0000000|c[0], 2, c[1])
	}
	var prevTog uint32
	for i := 0; i < eraseStatusReads; i++ {
		st := m.Read(0xA0000000|sect+0x1234, 2)
		if st&0x80 != 0 || st&0x08 == 0 || st&0x20 != 0 {
			t.Fatalf("status read %d = %#04x: want DQ7=0 (busy), DQ3=1 (started), DQ5=0", i, st)
		}
		if i > 0 && st&0x40 == prevTog {
			t.Errorf("status read %d: DQ6 did not toggle", i)
		}
		prevTog = st & 0x40
	}
	if v := m.Read(0xA0000000|sect, 2); v != 0xFFFF {
		t.Fatalf("after the erase: %#x, want 0xffff", v)
	}
	if m.flash[0x40000] != 0xFF || m.flash[0x5fffe] != 0xFF {
		t.Error("the sector was not erased")
	}
	if m.flash[0x3ffff] != 0x11 || m.flash[0x60000] != 0x44 {
		t.Error("the erase went past the 128 KB sector")
	}
	// reads elsewhere during an erase are array data (code keeps running from other sectors)
	m.Write(0xA0000000|0xAAA, 2, 0xAA)
	for _, c := range [][2]uint32{{0x554, 0x55}, {0xAAA, 0x80}, {0xAAA, 0xAA}, {0x554, 0x55}, {sect, 0x30}} {
		m.Write(0xA0000000|c[0], 2, c[1])
	}
	if v := m.Read(0xA0000000|0x60000, 1); v != 0x44 {
		t.Errorf("read outside the erasing sector = %#x, want array data 0x44", v)
	}
}

// installState resumes the 32 MB image at the MAIN MENU (the 16 MB one lacks most of fls0).
func installState(t *testing.T) *Emulator {
	flash, err := os.ReadFile("../os/flash_dump/flash_full_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(20_000_000)
	return e
}

// menuIcons is the MAIN MENU's icon count (built-in apps + add-ins), as the menu computes it on
// entry (OS 3.60 FUN_8036421e).
func menuIcons(t *testing.T, e *Emulator) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n, err := e.callOS(0x8036421e, 50_000_000)
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

// readFile reads \\fls0\<name> back through the OS (Bfile_OpenFile_OS / Bfile_ReadFile_OS).
func readFile(t *testing.T, e *Emulator, name string, size int) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	call := func(n uint32, args ...uint32) int32 {
		r, err := e.syscall(n, installBudget, args...)
		if err != nil {
			t.Fatal(err)
		}
		return int32(r)
	}
	p := uint32(call(sysMalloc, 0x100+installChunk))
	defer call(sysFree, p)
	for i, ch := range `\\fls0\` + name + "\x00" {
		e.mem.W16(p+uint32(2*i), uint32(ch))
	}
	h := call(sysBfileOpen, p, 0, 0) // 0 = READ
	if h < 0 {
		t.Fatalf("open %s: %d", name, h)
	}
	defer call(sysBfileClose, uint32(h))
	var out []byte
	for len(out) < size {
		n := call(0x1dac, uint32(h), p+0x100, uint32(min(installChunk, size-len(out))), uint32(len(out)))
		if n <= 0 {
			t.Fatalf("read %s at %d: %d", name, len(out), n)
		}
		for i := uint32(0); i < uint32(n); i++ {
			out = append(out, byte(e.mem.R8(p+0x100+i)))
		}
	}
	return out
}

// Installing at the MAIN MENU: the OS writes the file (erasing flash sectors as its FTL needs),
// the menu comes back with one more icon, and the new add-in starts from it.
func TestInstallAddin(t *testing.T) {
	e := installState(t)
	g3a, err := os.ReadFile("testdata/aluprobe.g3a")
	if err != nil {
		t.Fatal(err)
	}
	icons := menuIcons(t, e)
	if err := e.InstallAddin("ALUPROB2.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, e, "ALUPROB2.g3a", len(g3a)); !bytes.Equal(got, g3a) {
		t.Fatal("the file read back differs from what was installed")
	}
	if n := menuIcons(t, e); n != icons+1 {
		t.Fatalf("menu icons %d -> %d, want +1", icons, n)
	}
	if e.mem.R8(osInMainMenu) != 1 || !e.osIdle() {
		t.Fatal("not back at the MAIN MENU")
	}
	// the new icon is the last one: LEFT from the first icon wraps to it, EXE starts it
	for _, k := range [][2]uint32{{2, 8}, {2, 1}} {
		e.InjectKey(k[0], k[1])
		e.Step(30_000_000)
	}
	for i := 0; i < 200 && e.cpu.pc != 0x00300000; i++ {
		e.Step(1_000_000)
	}
	if e.cpu.pc != 0x00300000 && !e.mem.mmuAt {
		t.Fatalf("the installed add-in did not start (pc %#x)", e.cpu.pc)
	}

	// installing the same name again replaces the file: same icon count
	e = installState(t)
	icons = menuIcons(t, e)
	g3a2 := bytes.Clone(g3a)
	g3a2[0x7100] ^= 0xFF // a different build (code area: the trailer must keep matching the
	// header checksum at 0x20, or the OS's add-in scan skips the file)
	for _, b := range [][]byte{g3a, g3a2} {
		if err := e.InstallAddin("ALUPROB2.g3a", b); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, e, "ALUPROB2.g3a", len(g3a2)); !bytes.Equal(got, g3a2) {
		t.Fatal("reinstall: the file holds the old build")
	}
	if n := menuIcons(t, e); n != icons+1 {
		t.Fatalf("reinstall: menu icons %d -> %d, want +1", icons, n)
	}
}

func TestInstallAddinRejects(t *testing.T) {
	g3a := make([]byte, 0x8000)
	copy(g3a, invertBytes([]byte("USBPower")))
	for _, c := range []struct {
		name string
		data []byte
	}{
		{"x.bin", g3a}, {`a\b.g3a`, g3a}, {"é.g3a", g3a}, {"x.g3a", make([]byte, 0x8000)},
		{"x.g3a", g3a[:0x100]}, {"big.g3a", append(bytes.Clone(g3a), make([]byte, maxAddinSize)...)},
	} {
		if err := checkAddin(c.name, c.data); err == nil {
			t.Errorf("%q (%d bytes) accepted", c.name, len(c.data))
		}
	}
	if err := checkAddin("ok.g3a", g3a); err != nil {
		t.Error(err)
	}
}

// MENU inside an add-in keeps it suspended under the MAIN MENU, its code still mapped from
// flash (MMU on). Installing then first leaves the menu into Run-Matrix (which ends the add-in),
// so even the suspended add-in's own file can be replaced; MENU afterwards goes back to
// Run-Matrix, never into the replaced add-in. Uses DASM, icon Z in the 32 MB dump (it hands
// MENU to the OS; see dasm_real_test.go).
func TestInstallAddinOverSuspendedAddin(t *testing.T) {
	e := installState(t)
	g3a, err := os.ReadFile("testdata/aluprobe.g3a")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range [][2]uint32{{2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {1, 7}, {1, 7}, {2, 1}} {
		e.InjectKey(k[0], k[1])
		e.Step(30_000_000)
	}
	e.Step(100_000_000)
	e.InjectKey(3, 8) // MENU
	e.Step(100_000_000)
	if !e.mem.mmuAt || !e.atMainMenu() {
		t.Fatalf("want the MAIN MENU over the suspended DASM (mmu %v, menu %d)", e.mem.mmuAt, e.mem.R8(osInMainMenu))
	}
	if err := e.InstallAddin("DASM.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	if e.mem.mmuAt || !e.atMainMenu() {
		t.Fatal("not back at the MAIN MENU with the add-in ended")
	}
	if got := readFile(t, e, "DASM.g3a", len(g3a)); !bytes.Equal(got, g3a) {
		t.Fatal("DASM.g3a was not replaced")
	}
	e.InjectKey(3, 8) // MENU: back to the previous app, which is now Run-Matrix
	e.Step(100_000_000)
	if f := e.Fault(); f != "" || e.mem.mmuAt || e.mem.R8(osInMainMenu) != 0 {
		t.Fatalf("MENU after the install: fault %q, mmu %v", f, e.mem.mmuAt)
	}
}
