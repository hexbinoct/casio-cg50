//go:build probe

package main

// Probe (2026-10-04, on-device debugger stage 2a): cg50_addons/ramchk (RAMCHK.g3a) checks on the
// real calculator that physical DRAM 0x0C4E0000-0x0C7FFFFF stays untouched while it is used
// normally (the planned home of the debugger's resident monitor; ramfree_probe_test.go found it
// never accessed in the emulator). This runs the same session in the emulator: on the 32 MB dump
// at the MAIN MENU, install RAMCHK through the OS, start it from its new (last) icon, F1 = ARM,
// MENU out, use Run-Matrix, Geometry and DASM, start RAMCHK again, F2 = CHECK. Go checks the
// pattern in emulated DRAM directly after ARM and at the end, and reads the add-in's own
// verdict from its header. Screenshots: $TMPDIR/cg50_ramchk_*.png (+ _full = gint's 396x224).
//
//   TMPDIR=<dir> go -C emu_go test -tags probe -run TestRamchk -count=1 -v .
//   (RAMCHK_G3A=<path>, default ../../cg50_addons/ramchk/RAMCHK.g3a)

import (
	"encoding/binary"
	"os"
	"testing"
)

// the add-in's layout (ramchk/src/main.c)
const (
	rcLo, rcHi = 0x0C4E0000, 0x0C800000
	rcHdrWords = 16
	rcK        = 0x5AC3A53C
	rcMagic0   = 0x524D434B
	// header word indices
	rcHArms, rcHChecks, rcHCchg = 3, 4, 9
)

// keys (row-col, re/KEYMAP.md)
var (
	rcMENU, rcEXIT, rcEXE = [2]uint32{3, 8}, [2]uint32{3, 7}, [2]uint32{2, 1}
	rcUP, rcDOWN, rcLEFT  = [2]uint32{1, 8}, [2]uint32{2, 7}, [2]uint32{2, 8}
	rcRIGHT, rcF1, rcF2   = [2]uint32{1, 7}, [2]uint32{6, 9}, [2]uint32{5, 9}
	rcPOW, rcDIV          = [2]uint32{4, 7}, [2]uint32{2, 3}
	rc0, rc1, rc2, rc7    = [2]uint32{6, 1}, [2]uint32{6, 2}, [2]uint32{5, 2}, [2]uint32{6, 4}
)

type rcSession struct {
	t *testing.T
	e *Emulator
}

func (s *rcSession) tap(ks ...[2]uint32) {
	e := s.e
	for _, k := range ks {
		e.InjectKey(k[0], k[1])
		e.Step(30_000_000)
		for n := 0; n < 600 && !e.cpu.sleeping; n++ {
			e.Step(10_000_000)
		}
		if f := e.Fault(); f != "" {
			s.t.Fatal(f)
		}
	}
}

func (s *rcSession) shot(name string, addin bool) {
	savePNG(s.t, s.e, "ramchk_"+name)
	if addin {
		saveAddinFrame(s.t, s.e, "ramchk_"+name+"_full")
	}
}

// toMenu presses MENU (then EXIT, MENU if needed) until the MAIN MENU is up.
func (s *rcSession) toMenu(what string) {
	s.tap(rcMENU)
	for n := 0; n < 3 && !s.e.atMainMenu(); n++ {
		s.tap(rcEXIT, rcMENU)
	}
	if !s.e.atMainMenu() {
		s.shot("notmenu", false)
		s.t.Fatalf("%s: not at the MAIN MENU", what)
	}
}

// home puts the menu cursor on the first icon: '1' opens Run-Matrix, MENU comes back on it.
func (s *rcSession) home() {
	s.tap(rc1)
	s.toMenu("home")
}

// launch starts the icon at index i (rows of 4; -1 = the last one, LEFT from the first wraps).
func (s *rcSession) launch(i int) {
	s.home()
	press := func(k [2]uint32) { s.e.InjectKey(k[0], k[1]); s.e.Step(30_000_000) }
	if i < 0 {
		press(rcLEFT)
	} else {
		for n := 0; n < i/4; n++ {
			press(rcDOWN)
		}
		for n := 0; n < i%4; n++ {
			press(rcRIGHT)
		}
	}
	s.e.InjectKey(rcEXE[0], rcEXE[1])
}

// launchAddin starts an add-in icon and waits for its entry point (pc = 0x00300000).
func (s *rcSession) launchAddin(i int, name string) {
	s.launch(i)
	for n := 0; s.e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || s.e.Fault() != "" {
			s.t.Fatalf("%s never started (%s)", name, s.e.Fault())
		}
		s.e.Step(1)
	}
	s.e.Step(150_000_000)
	s.tap(rcUP) // the first key after a launch is swallowed
}

func rcWord(e *Emulator, phys uint32) uint32 {
	return binary.BigEndian.Uint32(e.mem.dram[phys-DramBase:])
}

// rcVerify checks the pattern in emulated DRAM: number of changed words, first one.
func rcVerify(e *Emulator) (changed int, first uint32) {
	for a := uint32(rcLo + 4*rcHdrWords); a < rcHi; a += 4 {
		if rcWord(e, a) != a^rcK {
			if changed == 0 {
				first = a
			}
			changed++
		}
	}
	return
}

func TestRamchk(t *testing.T) {
	path := os.Getenv("RAMCHK_G3A")
	if path == "" {
		path = "../../cg50_addons/ramchk/RAMCHK.g3a"
	}
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no RAMCHK.g3a: ", err)
	}
	e := installState(t)
	s := &rcSession{t, e}
	icons := menuIcons(t, e)
	if err := e.InstallAddin("RAMCHK.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	if n := menuIcons(t, e); n != icons+1 {
		t.Fatalf("menu icons %d -> %d, want +1", icons, n)
	}
	e.Step(20_000_000)

	s.launchAddin(-1, "RAMCHK")
	s.shot("00_start", true)
	s.tap(rcF1) // ARM
	s.shot("01_armed", true)
	if m := rcWord(e, rcLo); m != rcMagic0 {
		t.Fatalf("after ARM: magic %#08x, want %#08x", m, uint32(rcMagic0))
	}
	if n, a := rcVerify(e); n != 0 {
		t.Fatalf("after ARM: %d words differ from the pattern, first at %#08x", n, a)
	}
	t.Logf("armed #%d", rcWord(e, rcLo+4*rcHArms))

	// use the calculator: Run-Matrix, Geometry (Casio add-in, icon J = 18), DASM (icon Z = 34)
	s.toMenu("leave RAMCHK")
	s.launch(0)
	e.Step(100_000_000)
	s.tap(rc2, rcPOW, rc1, rc0, rcEXE, rc1, rcDIV, rc7, rcEXE)
	s.shot("02_runmat", false)
	s.toMenu("after Run-Matrix")

	s.launch(18)
	e.Step(300_000_000)
	s.tap(rcEXE, rcF1, rcEXIT, rcF2, rcEXIT, rcDOWN, rcEXE)
	s.shot("03_geometry", false)
	s.toMenu("after Geometry")

	s.launchAddin(34, "DASM")
	s.tap(rcDOWN, rcDOWN, rcEXE) // open a file
	s.shot("04_dasm", true)
	s.toMenu("after DASM")
	e.Step(300_000_000) // MAIN MENU idle

	s.launchAddin(-1, "RAMCHK")
	s.tap(rcF2) // CHECK
	s.shot("05_check", true)

	if m := rcWord(e, rcLo); m != rcMagic0 {
		t.Errorf("after the session: magic %#08x, want %#08x", m, uint32(rcMagic0))
	}
	if n, a := rcVerify(e); n != 0 {
		t.Errorf("after the session: %d words differ from the pattern, first at %#08x", n, a)
	}
	checks, cchg := rcWord(e, rcLo+4*rcHChecks), rcWord(e, rcLo+4*rcHCchg)
	if checks != 1 || cchg != 0 {
		t.Errorf("RAMCHK's own header: checks %d (want 1), changed words %d (want 0)", checks, cchg)
	}
	nz := 0
	for a := uint32(0xFD8026C0); a < 0xFD803F40; a += 4 {
		if binary.BigEndian.Uint32(e.mem.ilram[a-IlramBase:]) != 0 {
			nz++
		}
	}
	t.Logf("CHECK: checks %d, changed %d; IL RAM FD8026C0-FD803F3F nonzero words: %d", checks, cchg, nz)

	// F1 while armed asks first; EXIT cancels (and doesn't quit)
	s.tap(rcF1)
	s.shot("06_rearm_prompt", true)
	s.tap(rcEXIT)
	if !e.mem.mmuAt || rcWord(e, rcLo+4*rcHArms) != 1 {
		t.Fatal("EXIT at the re-arm prompt: RAMCHK quit or re-armed")
	}

	// self-test of CHECK's reporting: damage the range (and the OS IL RAM area it watches)
	// behind its back: 16 words at 0x0C500000 (one range), 2 words 32 bytes apart at
	// 0x0C6ABCD0 (merged into one range), 1 word at the very end
	poke := func(a, v uint32) { binary.BigEndian.PutUint32(e.mem.dram[a-DramBase:], v) }
	for a := uint32(0x0C500000); a < 0x0C500040; a += 4 {
		poke(a, 0)
	}
	poke(0x0C6ABCD0, 0x12345678)
	poke(0x0C6ABCF0, 0x9ABCDEF0)
	poke(rcHi-4, 0xFFFFFFFF)
	il := e.mem.ilram[0xFD803000-IlramBase:]
	save := binary.BigEndian.Uint32(il)
	binary.BigEndian.PutUint32(il, 0xCAFEF00D)
	s.tap(rcF2)
	s.shot("07_check_damaged", true)
	binary.BigEndian.PutUint32(il, save)
	if checks, cchg := rcWord(e, rcLo+4*rcHChecks), rcWord(e, rcLo+4*rcHCchg); checks != 2 || cchg != 19 {
		t.Errorf("after the damage: checks %d (want 2), changed words %d (want 19)", checks, cchg)
	}

	// magic wiped (as after a power loss): CHECK says so
	poke(rcLo, 0)
	s.tap(rcF2)
	s.shot("08_check_wiped", true)
}
