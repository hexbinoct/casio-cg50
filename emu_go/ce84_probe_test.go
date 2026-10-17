//go:build probe

package cg50emu

// Probe (2026-10-16): ~/main/casio/ce_cg50/ce84 (CE84.g3a, the TI-84 Plus CE emulator add-in).
// Installs the ROM file and the add-in into the 32 MB image's storage memory, launches CE84,
// lets TI-OS boot, types 2+2 ENTER, graphs Y1=sin(X), opens CE84's menu (SHIFT+MENU), quits
// (state saved to \\fls0\CE84.sav), relaunches (state restored: the calculation must still be
// on the TI-OS home screen) and quits again. Reads CE84's stats block at physical 0x0C1E0000
// (ce84/src/main.c, ce84_stats_t) and saves a PNG of the whole panel at every step.
//   CE84_G3A  the .g3a   (default ../../ce_cg50/ce84/CE84.g3a)
//   CE84_ROM  the ROM    (default ../../ti/roms/ti-84ce.rom)
//   CE84_OUT  PNG folder (default ../../ce_cg50/out/ce84)
//   CE84_SAV  if set, a copy of the state file saved at the first quit goes there
//   go -C emu_go test -tags probe -run 'TestCE84$' -count=1 -timeout 60m -v .

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

const (
	c8Stats = 0x0C1E0000 // physical: CG50_STATS_ADDR
	c8Magic = 0x43453834 // "CE84"
	// word offsets in ce84_stats_t
	c8Phase, c8EmuMs, c8RealMs, c8Frames, c8Speed, c8Load, c8RomMs, c8RestoreMs = 1, 2, 3, 4, 5, 6, 7, 8
	c8SaveMs, c8StateBytes, c8Restored, c8Fps, c8Sectors, c8CoreUs, c8View      = 9, 10, 11, 12, 13, 14, 15
	c8LcdCtl, c8LcdBase, c8Potential, c8KeyEv                                   = 16, 17, 18, 19
	c8Msg                                                                       = 20
	// phases
	c8PhFind, c8PhLoad, c8PhRestore, c8PhRun, c8PhMenu, c8PhSave, c8PhQuit = 1, 2, 3, 4, 5, 6, 7
	c8IPS                                                                  = 61_000_000 // the real CG50 on this code
)

// CG50 keys (row, col: re/KEYMAP.md)
var (
	c8Key = map[string][2]uint32{
		"2": {5, 2}, "+": {3, 2}, "EXE": {2, 1}, "F1": {6, 9}, "F5": {2, 9}, "F6": {1, 9},
		"sin": {3, 6}, "XOT": {6, 6}, ")": {3, 5}, "SHIFT": {6, 8}, "MENU": {3, 8},
		"UP": {1, 8}, "DOWN": {2, 7}, "EXIT": {3, 7},
	}
)

func c8Word(e *Emulator, i int) uint32 {
	return binary.BigEndian.Uint32(e.mem.dram[c8Stats-DramBase+4*i:])
}

func c8Message(e *Emulator) string {
	b := e.mem.dram[c8Stats-DramBase+4*c8Msg:][:64]
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

type c8Session struct {
	t   *testing.T
	e   *Emulator
	out string
}

func (s *c8Session) cycles() uint64 { return s.e.cpu.cycles }

// secs runs the CG50 for d seconds of its time (at c8IPS).
func (s *c8Session) secs(d float64) {
	end := s.cycles() + uint64(d*c8IPS)
	for s.cycles() < end {
		s.e.Step(5_000_000)
		if f := s.e.Fault(); f != "" {
			s.shot("fault")
			s.t.Fatal(f)
		}
	}
}

// until runs until cond holds (checked every 2 M cycles), at most max seconds of CG50 time.
func (s *c8Session) until(what string, max float64, cond func() bool) float64 {
	start := s.cycles()
	for !cond() {
		if s.cycles()-start > uint64(max*c8IPS) {
			s.shot("timeout")
			s.t.Fatalf("%s: not after %.0f s (phase %#x, emu ms %d, msg %q)", what, max,
				c8Word(s.e, c8Phase), c8Word(s.e, c8EmuMs), c8Message(s.e))
		}
		s.e.Step(2_000_000)
		if f := s.e.Fault(); f != "" {
			s.shot("fault")
			s.t.Fatal(f)
		}
	}
	return float64(s.cycles()-start) / c8IPS
}

func (s *c8Session) tap(keys ...string) {
	for _, k := range keys {
		c, ok := c8Key[k]
		if !ok {
			s.t.Fatalf("no key %q", k)
		}
		s.e.InjectKey(c[0], c[1])
		s.secs(0.35)
	}
}

func (s *c8Session) shot(name string) {
	buf := make([]byte, PanelWidth*PanelHeight*4)
	s.e.FramebufferRGBA(buf)
	img := &image.RGBA{Pix: buf, Stride: PanelWidth * 4, Rect: image.Rect(0, 0, PanelWidth, PanelHeight)}
	path := filepath.Join(s.out, "ce84_"+name+".png")
	f, err := os.Create(path)
	if err != nil {
		s.t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
	s.t.Logf("saved %s", path)
}

// ceArea returns the CE part (columns 0-319) of the panel, for comparing two screens.
func (s *c8Session) ceArea() []byte {
	buf := make([]byte, PanelWidth*PanelHeight*4)
	s.e.FramebufferRGBA(buf)
	out := make([]byte, 0, 320*PanelHeight*4)
	for y := 0; y < PanelHeight; y++ {
		out = append(out, buf[y*PanelWidth*4:][:320*4]...)
	}
	return out
}

func (s *c8Session) stats(what string) {
	e := s.e
	s.t.Logf("%-22s emu %6d ms  speed %5.1f%%  CPU %5.1f%%  max %6.1f%%  fps %4.1f  frames %d  keys %d",
		what, c8Word(e, c8EmuMs), float64(c8Word(e, c8Speed))/10, float64(c8Word(e, c8Load))/10,
		float64(c8Word(e, c8Potential))/10, float64(c8Word(e, c8Fps))/10, c8Word(e, c8Frames),
		c8Word(e, c8KeyEv))
}

// launch starts the last MAIN MENU icon (CE84) and waits for its entry point.
func (s *c8Session) launch() {
	rs := &rcSession{s.t, s.e}
	rs.launch(-1)
	for n := 0; s.e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || s.e.Fault() != "" {
			s.t.Fatalf("CE84 never started (%s)", s.e.Fault())
		}
		s.e.Step(1)
	}
}

// openMenu presses SHIFT+MENU (SHIFT held while MENU goes down).
func (s *c8Session) openMenu() {
	sh, mn := c8Key["SHIFT"], c8Key["MENU"]
	s.e.KeyDown(sh[0], sh[1])
	s.secs(0.1)
	s.e.KeyDown(mn[0], mn[1])
	s.secs(0.1)
	s.e.KeyUp(mn[0], mn[1])
	s.e.KeyUp(sh[0], sh[1])
	s.until("CE84 menu", 10, func() bool { return c8Word(s.e, c8Phase) == c8PhMenu })
	s.secs(0.3)
}

// quitSaving picks "Quit (save state)" (two above "Resume", wrapping) and waits for the MAIN MENU.
func (s *c8Session) quitSaving(what string) {
	s.tap("UP", "UP")
	s.shot(what + "_menu_quit")
	c := c8Key["EXE"]
	s.e.InjectKey(c[0], c[1])
	t0 := s.cycles()
	s.until("save", 60, func() bool { return c8Word(s.e, c8Phase) == c8PhSave })
	tSave := s.cycles()
	s.until("quit", 120, func() bool { return c8Word(s.e, c8Phase) == c8PhQuit })
	tQuit := s.cycles()
	s.until("MAIN MENU", 60, func() bool { return s.e.atMainMenu() })
	s.t.Logf("%s: save %.2f s CG50 time (EXE->quit %.2f s; add-in's RTC says %d ms), state file %d bytes, %d flash sectors",
		what, float64(tQuit-tSave)/c8IPS, float64(tQuit-t0)/c8IPS, c8Word(s.e, c8SaveMs),
		c8Word(s.e, c8StateBytes), c8Word(s.e, c8Sectors))
}

// startup waits for CE84 to run, logging the time of each start-up phase.
func (s *c8Session) startup(what string) {
	e := s.e
	t0 := s.cycles()
	marks := map[uint32]uint64{}
	for c8Word(e, 0) != c8Magic || c8Word(e, c8Phase) < c8PhRun || c8Word(e, c8Phase) >= 0xE0 {
		if ph := c8Word(e, c8Phase); c8Word(e, 0) == c8Magic {
			if ph >= 0xE0 {
				s.shot(what + "_error")
				s.t.Fatalf("CE84 error phase %#x: %q", ph, c8Message(e))
			}
			if _, ok := marks[ph]; !ok {
				marks[ph] = s.cycles()
			}
		}
		if s.cycles()-t0 > 300*c8IPS || e.Fault() != "" {
			s.shot(what + "_stuck")
			s.t.Fatalf("CE84 start-up stuck (phase %#x, %s, %q)", c8Word(e, c8Phase), e.Fault(), c8Message(e))
		}
		e.Step(1_000_000)
	}
	marks[c8PhRun] = s.cycles()
	sec := func(a, b uint32) float64 {
		ta, oka := marks[a]
		tb, okb := marks[b]
		if !oka || !okb {
			return -1
		}
		return float64(tb-ta) / c8IPS
	}
	s.t.Logf("%s: launch->ROM loading %.2f s, ROM load %.2f s (RTC %d ms), restore %.2f s (RTC %d ms, restored=%d, %q)",
		what, float64(marks[c8PhLoad]-t0)/c8IPS, sec(c8PhLoad, c8PhRestore),
		c8Word(e, c8RomMs), sec(c8PhRestore, c8PhRun), c8Word(e, c8RestoreMs), c8Word(e, c8Restored), c8Message(e))
}

func TestCE84(t *testing.T) {
	g3aPath := os.Getenv("CE84_G3A")
	if g3aPath == "" {
		g3aPath = "../../ce_cg50/ce84/CE84.g3a"
	}
	romPath := os.Getenv("CE84_ROM")
	if romPath == "" {
		romPath = "../../ti/roms/ti-84ce.rom"
	}
	out := os.Getenv("CE84_OUT")
	if out == "" {
		out = "../../ce_cg50/out/ce84"
	}
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no CE84.g3a: ", err)
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Skip("no ROM: ", err)
	}
	os.MkdirAll(out, 0o755)

	e := installState(t)
	e.SetInstrPerSecond(c8IPS)
	s := &c8Session{t, e, out}
	t0 := s.cycles()
	if err := e.InstallFile("TI84CE.rom", rom); err != nil {
		t.Fatal(err)
	}
	t.Logf("ROM file installed (%.1f s CG50 time)", float64(s.cycles()-t0)/c8IPS)
	if err := e.InstallAddin("CE84.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)

	// 1. start, TI-OS boots
	s.launch()
	s.startup("first start")
	if c8Word(e, c8Restored) != 0 {
		t.Fatalf("first start restored a state (%d)", c8Word(e, c8Restored))
	}
	s.until("TI-OS boot", 120, func() bool { return c8Word(e, c8EmuMs) >= 4000 })
	s.secs(1)
	s.stats("booted")
	s.shot("01_booted")
	s.secs(3)
	s.stats("idle (home screen)")

	// 2. 2+2 ENTER
	s.tap("2", "+", "2", "EXE")
	s.secs(1)
	s.stats("2+2")
	s.shot("02_2plus2")

	// 3. Y1=sin(X), GRAPH
	s.tap("F1")
	s.secs(0.5)
	s.tap("sin", "XOT", ")")
	s.secs(0.5)
	s.shot("03_yequ")
	s.tap("F5")
	busy := 0
	for i := 0; i < 40; i++ {
		s.secs(1)
		s.stats(fmt.Sprintf("graph +%ds", i+1))
		if i == 1 {
			s.shot("04_graphing")
		}
		if c8Word(e, c8Load) < 300 && i >= 2 {
			break
		}
		busy++
	}
	t.Logf("graph: ~%d s CG50 time with the CE busy", busy)
	s.shot("05_graph")

	// 4. back to the home screen (2nd MODE = QUIT), the menu, quit with a save
	s.tap("SHIFT", "F6")
	s.secs(1)
	s.shot("06_home")
	home := s.ceArea()
	s.openMenu()
	s.shot("07_menu")
	s.quitSaving("first")
	s.shot("08_main_menu")
	if p := os.Getenv("CE84_SAV"); p != "" { // CE84_SAV=<file>: a copy of \\fls0\CE84.sav
		os.WriteFile(p, readFile(t, e, "CE84.sav", int(c8Word(e, c8StateBytes))), 0o644)
	}

	// 5. relaunch: the state comes back
	s.launch()
	s.startup("second start")
	if c8Word(e, c8Restored) != 1 {
		t.Fatalf("state not restored (restored=%d, %q)", c8Word(e, c8Restored), c8Message(e))
	}
	s.secs(1.5)
	s.stats("after restore")
	s.shot("09_restored")
	again := s.ceArea()
	diff := 0
	for i := 0; i < len(home); i += 4 {
		if home[i] != again[i] || home[i+1] != again[i+1] || home[i+2] != again[i+2] {
			diff++
		}
	}
	t.Logf("home screen before quit vs after restore: %d of %d CE-area pixels differ (cursor blink)", diff, len(home)/4)
	if diff > 2000 {
		t.Errorf("the restored home screen differs in %d pixels", diff)
	}
	s.secs(2)
	s.stats("idle after restore")

	// 6. the views and the key map, from the menu
	for _, v := range []string{"11_view_top", "12_view_low"} {
		s.openMenu()
		s.tap("DOWN", "EXE", "EXIT")
		s.secs(0.5)
		s.shot(v)
	}
	s.openMenu()
	s.tap("DOWN", "EXE", "DOWN", "DOWN", "EXE") // view back to Fit, then "Key map"
	s.shot("13_keymap")
	s.tap("EXE", "EXIT")

	s.openMenu()
	s.quitSaving("second")
	s.shot("10_final")
}

// Without a ROM file CE84 says what to do and goes back to the MAIN MENU on a key.
func TestCE84NoROM(t *testing.T) {
	g3aPath := os.Getenv("CE84_G3A")
	if g3aPath == "" {
		g3aPath = "../../ce_cg50/ce84/CE84.g3a"
	}
	out := os.Getenv("CE84_OUT")
	if out == "" {
		out = "../../ce_cg50/out/ce84"
	}
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no CE84.g3a: ", err)
	}
	os.MkdirAll(out, 0o755)
	e := installState(t)
	e.SetInstrPerSecond(c8IPS)
	s := &c8Session{t, e, out}
	if err := e.InstallAddin("CE84.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launch()
	s.until("no-ROM message", 30, func() bool { return c8Word(e, c8Phase) == 0xE3 })
	s.secs(0.5)
	s.shot("00_no_rom")
	s.tap("EXE")
	s.until("MAIN MENU", 30, func() bool { return e.atMainMenu() })
}
