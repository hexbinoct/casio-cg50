//go:build probe && ti68k

package cg50emu

// Probe (2026-11-09): ti68k_cg50/app (TI68k.g3a, the TI-89 / TI-92 Plus emulator
// add-in; no ROM inside). Untracked, own build tags (`probe ti68k`) so that other agents'
// `-tags probe` runs never compile it. Installs the user's ROM file + the add-in, boots AMS,
// types on the CG50 keyboard (2+3, 5->a, "archive a" with the ALPHA letter lock), compares the
// TI's LCD (read from the add-in's DRAM) with reference frames from the desktop session
// (ti68k_cg50/bench/tiexp, TI68K_REF), saves with "Save state now", quits, relaunches (state
// restored: the history must be identical, a+1 = 6, VAR-LINK shows a archived), goes to the main
// menu with MENU and back (resumed, memory intact), turns the TI off (2nd+ON: the add-in sleeps)
// and on, and finally starts with a damaged state file (the fresh-boot question).
//   TI68K_APP  the .g3a   (default ../../ti68k_cg50/app/TI68k.g3a)
//   TI68K_ROM  the TI-89 ROM file (required: the tests skip without it)
//   TI68K_REF  reference LCDs (default ../../ti68k_cg50/out/ref89)
//   TI68K_OUT  PNG folder (default ../../ti68k_cg50/out/app89)
//   go -C emu_go test -tags 'probe ti68k' -run 'TestTI68kApp' -count=1 -timeout 60m -v .

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

const (
	taStats = 0x0C1E0000 // physical: MEM_STATS
	taMagic = 0x5436384B // "T68K"
	taTIRAM = 0x0C400000 // the TI's RAM (MEM_RAM)
	// word offsets in stats_t (app/src/stats.h)
	taPhase, taEmuMs, taRealMs, taFrames, taSpeed, taLoad, taRomMs, taRestoreMs    = 1, 2, 3, 4, 5, 6, 7, 8
	taSaveMs, taStateBytes, taRestored, taNBlocks, taCoreUs, taModel, taTIOff      = 9, 10, 11, 12, 13, 14, 15
	taKeyEv, taMods, taLayer, taResumes, taDramCheck, taInstr, taSaves, taAmsKnown = 16, 17, 18, 19, 20, 21, 22, 23
	taMsg                                                                          = 24
	// phases
	taPhLoad, taPhRestore, taPhRun, taPhMenu, taPhSave, taPhQuit, taPhOff, taPhChoose, taPhAsk, taPhOsMenu = 2, 3, 4, 5, 6, 7, 8, 10, 11, 12
	taIPS                                                                                                  = 61_000_000
)

// CG50 keys: inject (row, col), re/KEYMAP.md
var taKey = map[string][2]uint32{
	"ACON": {0, 0}, "0": {6, 1}, ".": {5, 1}, "EXP": {4, 1}, "NEG": {3, 1}, "EXE": {2, 1},
	"1": {6, 2}, "2": {5, 2}, "3": {4, 2}, "+": {3, 2}, "-": {2, 2},
	"4": {6, 3}, "5": {5, 3}, "6": {4, 3}, "*": {3, 3}, "/": {2, 3},
	"7": {6, 4}, "8": {5, 4}, "9": {4, 4}, "DEL": {3, 4},
	"FRAC": {6, 5}, "SD": {5, 5}, "(": {4, 5}, ")": {3, 5}, ",": {2, 5}, "ARROW": {1, 5},
	"XOT": {6, 6}, "log": {5, 6}, "ln": {4, 6}, "sin": {3, 6}, "cos": {2, 6}, "tan": {1, 6},
	"ALPHA": {6, 7}, "SQUARE": {5, 7}, "^": {4, 7}, "EXIT": {3, 7}, "DOWN": {2, 7}, "RIGHT": {1, 7},
	"SHIFT": {6, 8}, "OPTN": {5, 8}, "VARS": {4, 8}, "MENU": {3, 8}, "LEFT": {2, 8}, "UP": {1, 8},
	"F1": {6, 9}, "F2": {5, 9}, "F3": {4, 9}, "F4": {3, 9}, "F5": {2, 9}, "F6": {1, 9},
}

type taSession struct {
	t        *testing.T
	e        *Emulator
	out, ref string
}

func (s *taSession) word(i int) uint32 {
	return binary.BigEndian.Uint32(s.e.mem.dram[taStats-DramBase+4*i:])
}

func (s *taSession) msg() string {
	b := s.e.mem.dram[taStats-DramBase+4*taMsg:][:64]
	return string(bytes.TrimRight(b, "\x00"))
}

func (s *taSession) cycles() uint64 { return s.e.cpu.cycles }

func (s *taSession) secs(d float64) {
	end := s.cycles() + uint64(d*taIPS)
	for s.cycles() < end {
		s.e.Step(5_000_000)
		if f := s.e.Fault(); f != "" {
			s.shot("fault")
			s.t.Fatal(f)
		}
	}
}

func (s *taSession) until(what string, max float64, cond func() bool) float64 {
	start := s.cycles()
	for !cond() {
		if s.cycles()-start > uint64(max*taIPS) {
			s.shot("timeout")
			s.t.Fatalf("%s: not after %.0f s (phase %#x, emu ms %d, keys %d, layer %d, mods %#x, menus %d, last req %#x, menu keys %#x, msg %q)", what, max, s.word(taPhase), s.word(taEmuMs), s.word(taKeyEv), s.word(taLayer), s.word(taMods), s.word(40), s.word(41), s.word(42), s.msg())
		}
		s.e.Step(2_000_000)
		if f := s.e.Fault(); f != "" {
			s.shot("fault")
			s.t.Fatal(f)
		}
	}
	return float64(s.cycles()-start) / taIPS
}

func (s *taSession) tap(keys ...string) {
	for _, k := range keys {
		c, ok := taKey[k]
		if !ok {
			s.t.Fatalf("no key %q", k)
		}
		s.e.InjectKey(c[0], c[1])
		s.secs(0.35)
	}
}

func (s *taSession) shot(name string) {
	buf := make([]byte, PanelWidth*PanelHeight*4)
	s.e.FramebufferRGBA(buf)
	img := &image.RGBA{Pix: buf, Stride: PanelWidth * 4, Rect: image.Rect(0, 0, PanelWidth, PanelHeight)}
	path := filepath.Join(s.out, "ti68k_"+name+".png")
	f, err := os.Create(path)
	if err != nil {
		s.t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
	s.t.Logf("saved %s", path)
}

// lcd returns the TI's LCD frame (30 x 128 bytes) from the add-in's DRAM (HW2: RAM 0x4C00).
func (s *taSession) lcd() []byte {
	off := taTIRAM - DramBase + 0x4C00
	return append([]byte(nil), s.e.mem.dram[off:off+30*128]...)
}

// compare checks LCD rows y0..y1 (TI-89: 160 pixels each, TI-92 Plus: 240) against a reference dump.
func (s *taSession) compare(what, ref string, y0, y1 int) {
	want, err := os.ReadFile(filepath.Join(s.ref, ref+".lcd"))
	if err != nil {
		s.t.Errorf("%s: no reference %s (%v)", what, ref, err)
		return
	}
	got := s.lcd()
	bad, w := 0, 20
	if s.word(taModel) == 92 {
		w = 30
	}
	for y := y0; y <= y1; y++ {
		if !bytes.Equal(got[30*y:30*y+w], want[30*y:30*y+w]) {
			bad++
		}
	}
	if bad != 0 {
		s.t.Errorf("%s: %d of %d LCD rows differ from the desktop reference %s", what, bad, y1-y0+1, ref)
	} else {
		s.t.Logf("%s: LCD rows %d-%d identical to the desktop reference %s", what, y0, y1, ref)
	}
}

func (s *taSession) stats(what string) {
	s.t.Logf("%-24s emu %6d ms  speed %5.1f%%  CPU %5.1f%%  frames %d  keys %d  mods %#x layer %d  instr %d",
		what, s.word(taEmuMs), float64(s.word(taSpeed))/10, float64(s.word(taLoad))/10, s.word(taFrames),
		s.word(taKeyEv), s.word(taMods), s.word(taLayer), s.word(taInstr))
}

func (s *taSession) launch() {
	rs := &rcSession{s.t, s.e}
	rs.launch(-1)
	for n := 0; s.e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || s.e.Fault() != "" {
			s.t.Fatalf("TI68k never started (%s)", s.e.Fault())
		}
		s.e.Step(1)
	}
}

// startup waits for the run loop (or `want` phase), logging the start-up times.
func (s *taSession) startup(what string, want uint32) {
	t0 := s.cycles()
	s.until(what+" start-up", 300, func() bool {
		if s.word(0) == taMagic && s.word(taPhase) >= 0xE0 {
			s.shot(what + "_error")
			s.t.Fatalf("error phase %#x: %q", s.word(taPhase), s.msg())
		}
		return s.word(0) == taMagic && s.word(taPhase) == want
	})
	s.t.Logf("%s: %.2f s CG50 time to phase %d (ROM load %d ms, restore %d ms, restored=%d, %q)", what,
		float64(s.cycles()-t0)/taIPS, want, s.word(taRomMs), s.word(taRestoreMs), s.word(taRestored), s.msg())
}

func (s *taSession) openMenu() {
	sh, mn := taKey["SHIFT"], taKey["MENU"]
	s.e.KeyDown(sh[0], sh[1])
	s.secs(0.1)
	s.e.KeyDown(mn[0], mn[1])
	s.secs(0.1)
	s.e.KeyUp(mn[0], mn[1])
	s.e.KeyUp(sh[0], sh[1])
	s.until("menu", 10, func() bool { return s.word(taPhase) == taPhMenu })
	s.secs(0.3)
}

// pick waits for the ROM browser, checks that the cursor is on `want` (the stats msg holds the
// selected name) and chooses it.
func (s *taSession) pick(shot, want string) {
	s.until("ROM browser", 120, func() bool { return s.word(0) == taMagic && s.word(taPhase) == taPhChoose })
	d := s.until("ROM browser listing", 120, func() bool { return s.msg() != "" })
	s.t.Logf("ROM browser: listed in %.2f s CG50 time, cursor on %q", d, s.msg())
	s.secs(0.3)
	s.shot(shot)
	if s.msg() != want {
		s.t.Fatalf("ROM browser: cursor on %q, want %q", s.msg(), want)
	}
	s.tap("EXE")
}

// menu items (a file build): Resume, Keys, Speed display, Save state now, Choose another ROM,
// Reset, Quit (save), Quit without saving, About. The build with the ROM inside has no
// "Choose another ROM".
func (s *taSession) menuItem(n int) {
	for i := 0; i < n; i++ {
		s.tap("DOWN")
	}
	s.tap("EXE")
}

func (s *taSession) quitSaving(what string) {
	s.openMenu()
	saves := s.word(taSaves)
	c := taKey["UP"] // Quit (save) is third from the end in both builds
	for i := 0; i < 3; i++ {
		s.e.InjectKey(c[0], c[1])
		s.secs(0.35)
	}
	s.shot(what + "_menu_quit")
	c = taKey["EXE"]
	s.e.InjectKey(c[0], c[1])
	t0 := s.cycles()
	s.until("quit", 120, func() bool { return s.word(taPhase) == taPhQuit })
	s.until("MAIN MENU", 60, func() bool { return s.e.atMainMenu() })
	s.t.Logf("%s: save + quit %.2f s CG50 time (add-in: save %d ms), state file %d bytes, %d flash blocks, saves %d->%d",
		what, float64(s.cycles()-t0)/taIPS, s.word(taSaveMs), s.word(taStateBytes), s.word(taNBlocks), saves, s.word(taSaves))
}

func taSetup(t *testing.T) (*taSession, []byte, []byte) {
	g3aPath := os.Getenv("TI68K_APP")
	if g3aPath == "" {
		g3aPath = "../../ti68k_cg50/app/TI68k.g3a"
	}
	romPath := os.Getenv("TI68K_ROM")
	if romPath == "" {
		t.Skip("set TI68K_ROM to a TI-89 ROM file")
	}
	ref := os.Getenv("TI68K_REF")
	if ref == "" {
		ref = "../../ti68k_cg50/out/ref89"
	}
	out := os.Getenv("TI68K_OUT")
	if out == "" {
		out = "../../ti68k_cg50/out/app89"
	}
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no TI68k.g3a: ", err)
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Skip("no ROM: ", err)
	}
	os.MkdirAll(out, 0o755)
	e := installState(t)
	e.SetInstrPerSecond(taIPS)
	return &taSession{t, e, out, ref}, g3a, rom
}

func TestTI68kApp(t *testing.T) {
	s, g3a, rom := taSetup(t)
	e := s.e
	t0 := s.cycles()
	// any name: the ROM is chosen once in TI68k's file browser
	if err := e.InstallFile("mydump89.bin", rom); err != nil {
		t.Fatal(err)
	}
	t.Logf("ROM file installed (%.1f s CG50 time)", float64(s.cycles()-t0)/taIPS)
	if err := e.InstallAddin("TI68k.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)

	// 1. first start: the ROM browser (cursor on the only 2 MB file), then AMS boots
	s.launch()
	s.pick("00_picker", "mydump89.bin")
	s.startup("first start", taPhRun)
	if s.word(taRestored) != 0 || s.word(taModel) != 89 {
		t.Fatalf("restored=%d model=%d", s.word(taRestored), s.word(taModel))
	}
	t.Logf("AMS modifier flags known: %d", s.word(taAmsKnown))
	s.until("AMS boot", 200, func() bool { return s.word(taEmuMs) >= 7000 })
	s.secs(1)
	s.stats("booted")
	s.shot("01_booted")
	s.secs(3)
	s.stats("idle (home screen)")

	// 2. 2+3 ENTER
	s.tap("2", "+", "3", "EXE")
	s.secs(1)
	s.stats("2+3")
	s.shot("02_add")
	s.compare("2+3", "add", 8, 80)

	// 3. 5->a, then "archive a" typed with the letter lock (SHIFT, ALPHA ... ALPHA)
	s.tap("5", "ARROW", "ALPHA", "XOT", "EXE")
	s.secs(0.5)
	s.tap("SHIFT", "ALPHA")
	if s.word(taLayer) != 2 {
		t.Errorf("SHIFT ALPHA: layer %d, want 2 (lock)", s.word(taLayer))
	}
	s.shot("03_alock")
	s.tap("XOT", "6", "ln", "SD", "(", "2", "cos", ".", "XOT", "ALPHA")
	s.shot("04_typed")
	s.tap("EXE")
	s.secs(2)
	s.stats("archive a")
	s.shot("05_archived")
	s.compare("archive a", "archive", 8, 80)

	// 4. "Save state now" (menu item 3), then quit (save) and relaunch
	s.openMenu()
	s.shot("06_menu")
	s.menuItem(3)
	s.until("saved", 60, func() bool { return s.word(taPhase) == taPhMenu })
	t.Logf("save now: %d bytes, %d blocks, %d ms", s.word(taStateBytes), s.word(taNBlocks), s.word(taSaveMs))
	s.tap("EXIT")
	s.secs(0.5)
	before := s.lcd()
	s.quitSaving("first")
	s.shot("07_main_menu")

	s.launch()
	s.startup("second start", taPhRun)
	if s.word(taRestored) != 1 {
		t.Fatalf("state not restored (restored=%d, %q)", s.word(taRestored), s.msg())
	}
	s.secs(1.5)
	s.shot("08_restored")
	after := s.lcd()
	diff := 0
	for y := 0; y < 92; y++ { // the status line may show BUSY
		if !bytes.Equal(before[30*y:30*y+20], after[30*y:30*y+20]) {
			diff++
		}
	}
	t.Logf("TI screen before quit vs after restore: %d of 92 rows differ (cursor blink)", diff)
	if diff > 9 {
		t.Errorf("the restored screen differs in %d rows", diff)
	}
	s.compare("restored history", "archive", 8, 80)

	// 5. a+1 (a in the archive), VAR-LINK (2nd -)
	s.tap("ALPHA", "XOT", "+", "1", "EXE")
	s.secs(1)
	s.shot("09_aplus1")
	s.compare("a+1", "aplus1", 8, 80)
	s.tap("SHIFT", "-")
	s.secs(1.5)
	s.shot("10_varlink")
	s.compare("VAR-LINK", "varlink", 0, 91)
	s.tap("EXIT")
	s.secs(0.5)

	// 6. the key help screen and the speed display
	s.openMenu()
	s.menuItem(1)
	s.secs(0.5)
	s.shot("11_keys")
	s.tap("EXE", "EXIT")
	s.openMenu()
	s.menuItem(2) // speed display on
	s.tap("EXIT")
	s.secs(2.5)
	s.stats("speed display on")
	s.shot("12_speed")

	// 7. MENU: the CG50's main menu (state saved), then back (resumed)
	saves := s.word(taSaves)
	s.tap("MENU")
	s.until("main menu", 60, func() bool { return e.atMainMenu() })
	if s.word(taSaves) != saves+1 {
		t.Errorf("MENU did not save the state (saves %d -> %d)", saves, s.word(taSaves))
	}
	s.shot("13_mainmenu")
	s.tap("EXE") // the cursor is on TI68k: the suspended add-in resumes
	s.until("resumed", 60, func() bool { return s.word(taResumes) == 1 && s.word(taPhase) == taPhRun })
	s.secs(1)
	t.Logf("back from the main menu: resumes %d, DRAM check %d (1 intact)", s.word(taResumes), s.word(taDramCheck))
	if s.word(taDramCheck) != 1 {
		t.Errorf("DRAM check %d after the main menu", s.word(taDramCheck))
	}
	s.shot("14_resumed")
	s.compare("resumed", "aplus1", 8, 80)

	// 7b. MENU, then another app (Run-Matrix) first: the OS ends the suspended add-in; the next
	// start restores the state saved when MENU was pressed
	s.tap("MENU")
	s.until("main menu", 60, func() bool { return e.atMainMenu() })
	s.launch()
	s.startup("start after another app", taPhRun)
	if s.word(taRestored) != 1 || s.word(taResumes) != 0 {
		t.Errorf("after another app: restored=%d resumes=%d, want a restart from the file", s.word(taRestored), s.word(taResumes))
	}
	s.secs(1.5)
	s.shot("14b_restarted")
	s.compare("restarted", "aplus1", 8, 80)

	// 8. OFF (SHIFT, AC/ON = 2nd ON): the add-in sleeps; AC/ON turns it on again
	s.tap("SHIFT", "ACON")
	s.until("TI off", 30, func() bool { return s.word(taTIOff) == 1 })
	s.secs(1)
	ex0, cy0 := e.Executed(), s.cycles()
	s.secs(3)
	t.Logf("TI off: %.2f M SH-4 instructions executed in 3 s of CG50 time (%.1f %% of %d M/s)",
		float64(e.Executed()-ex0)/1e6, 100*float64(e.Executed()-ex0)/float64(s.cycles()-cy0), taIPS/1_000_000)
	s.shot("15_off")
	s.tap("ACON")
	s.until("TI on", 30, func() bool { return s.word(taTIOff) == 0 && s.word(taPhase) == taPhRun })
	s.secs(2)
	s.shot("16_on_again")
	s.stats("on again")

	// 9. quit without saving the last minutes, keep a copy of the state file
	s.openMenu()
	s.menuItem(7)
	s.until("MAIN MENU", 60, func() bool { return e.atMainMenu() })
	sav := readFile(t, e, "mydump89.sav", int(s.word(taStateBytes)))
	os.WriteFile(filepath.Join(s.out, "mydump89.sav"), sav, 0o644)

	// 10. a damaged state file: the fresh-boot question
	bad := append([]byte(nil), sav...)
	bad[len(bad)/2] ^= 0x55
	if err := e.InstallFile("mydump89.sav", bad); err != nil {
		t.Fatal(err)
	}
	s.launch()
	s.startup("damaged state", taPhAsk)
	s.secs(0.5)
	s.shot("17_damaged")
	t.Logf("damaged state file: %q", s.msg())
	s.tap("EXE")
	s.until("fresh boot", 60, func() bool { return s.word(taPhase) == taPhRun })
	if s.word(taRestored) != 2 {
		t.Errorf("restored=%d after EXE, want 2", s.word(taRestored))
	}
	s.until("AMS boot", 200, func() bool { return s.word(taEmuMs) >= 7000 })
	s.secs(2)
	s.shot("18_fresh")
	s.compare("fresh boot", "boot", 0, 91)
}

// A 2 MB file that isn't a ROM is refused with an explanation; EXIT in the browser quits.
func TestTI68kAppNoROM(t *testing.T) {
	s, g3a, _ := taSetup(t)
	if err := s.e.InstallFile("fake.rom", make([]byte, 2<<20)); err != nil {
		t.Fatal(err)
	}
	if err := s.e.InstallAddin("TI68k.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	s.e.Step(20_000_000)
	s.launch()
	s.pick("00_picker_fake", "fake.rom")
	s.until("not-a-ROM message", 30, func() bool { return s.msg() == "not a ROM" })
	s.secs(0.5)
	s.shot("00_not_a_rom")
	s.tap("EXE")
	s.until("back in the browser", 30, func() bool { return s.msg() == "fake.rom" })
	s.tap("EXIT")
	s.until("MAIN MENU", 30, func() bool { return s.e.atMainMenu() })
}

// The TI-92 Plus: the same add-in, the x1.5 screen, letters typed directly (ALPHA layer).
//
//	TI68K_ROM92 (a TI-92 Plus ROM file, required), references in ../../ti68k_cg50/out/ref92
func TestTI68kApp92(t *testing.T) {
	s, g3a, _ := taSetup(t)
	romPath := os.Getenv("TI68K_ROM92")
	if romPath == "" {
		t.Skip("set TI68K_ROM92 to a TI-92 Plus ROM file")
	}
	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Skip("no TI-92 Plus ROM: ", err)
	}
	s.ref = "../../ti68k_cg50/out/ref92"
	s.out = "../../ti68k_cg50/out/app92"
	os.MkdirAll(s.out, 0o755)
	e := s.e
	if err := e.InstallFile("TI92P.rom", rom); err != nil {
		t.Fatal(err)
	}
	if err := e.InstallAddin("TI68k.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launch()
	s.pick("00_picker", "TI92P.rom")
	s.startup("first start", taPhRun)
	if s.word(taModel) != 92 {
		t.Fatalf("model %d", s.word(taModel))
	}
	s.until("AMS boot", 200, func() bool { return s.word(taEmuMs) >= 8000 })
	s.secs(2)
	s.stats("booted")
	s.shot("01_booted")
	s.tap("2", "+", "3", "EXE")
	s.secs(1)
	s.compare("2+3", "add", 8, 110)
	s.tap("5", "ARROW", "ALPHA", "XOT", "EXE")
	s.secs(0.5)
	s.tap("SHIFT", "ALPHA", "XOT", "6", "ln", "SD", "(", "2", "cos", ".", "XOT", "ALPHA", "EXE")
	s.secs(2)
	s.stats("archive a")
	s.shot("02_archived")
	s.compare("archive a", "archive", 8, 110)
	s.openMenu()
	s.menuItem(1)
	s.secs(0.5)
	s.shot("03_keys")
	s.tap("EXE", "EXIT")
	s.quitSaving("92")
	s.launch()
	s.startup("second start", taPhRun)
	if s.word(taRestored) != 1 {
		t.Fatalf("not restored (%d, %q)", s.word(taRestored), s.msg())
	}
	s.secs(1.5)
	s.shot("04_restored")
	s.compare("restored", "archive", 8, 110)
}

// The personal build with the ROM inside (TI68Krom.g3a, never shared): no .rom file needed.
//
//	TI68K_APPROM (default ../../ti68k_cg50/app/TI68Krom.g3a)
func TestTI68kAppEmbedded(t *testing.T) {
	path := os.Getenv("TI68K_APPROM")
	if path == "" {
		path = "../../ti68k_cg50/app/TI68Krom.g3a"
	}
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no TI68Krom.g3a: ", err)
	}
	s, _, _ := taSetup(t)
	if err := s.e.InstallAddin("TI68Krom.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	s.e.Step(20_000_000)
	s.launch()
	s.startup("embedded ROM", taPhRun)
	s.until("AMS boot", 200, func() bool { return s.word(taEmuMs) >= 7000 })
	s.secs(2)
	s.shot("20_embedded")
	s.compare("embedded boot", "boot", 0, 91)
	s.quitSaving("embedded")
}

// "Choose another ROM": a TI-89 and a TI-92 Plus side by side, each keeping its own state; the
// choice is remembered (TI68k.cfg), so a later start skips the browser.
func TestTI68kAppSwitch(t *testing.T) {
	s, g3a, rom89 := taSetup(t)
	if os.Getenv("TI68K_ROM92") == "" {
		t.Skip("set TI68K_ROM92 to a TI-92 Plus ROM file")
	}
	rom92, err := os.ReadFile(os.Getenv("TI68K_ROM92"))
	if err != nil {
		t.Skip("no TI-92 Plus ROM: ", err)
	}
	s.out = "../../ti68k_cg50/out/switch"
	os.MkdirAll(s.out, 0o755)
	e := s.e
	for _, f := range []struct {
		name string
		data []byte
	}{{"mydump89.bin", rom89}, {"TI92P.rom", rom92}} {
		if err := e.InstallFile(f.name, f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.InstallAddin("TI68k.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launch()
	s.pick("01_picker", "mydump89.bin")
	s.startup("TI-89", taPhRun)
	s.until("AMS boot", 200, func() bool { return s.word(taEmuMs) >= 7000 })
	s.tap("7", "EXE")
	s.secs(1)
	if s.word(taModel) != 89 {
		t.Fatalf("model %d, want 89", s.word(taModel))
	}

	// menu: Choose another ROM; the cursor starts on the other 2 MB file
	s.openMenu()
	s.menuItem(4)
	saves := s.word(taSaves)
	s.pick("02_picker_menu", "TI92P.rom")
	s.until("TI-92 Plus", 120, func() bool { return s.word(taPhase) == taPhRun && s.word(taModel) == 92 })
	if s.word(taSaves) != saves+1 {
		t.Errorf("the TI-89's state wasn't saved before the switch (saves %d -> %d)", saves, s.word(taSaves))
	}
	s.until("AMS boot", 200, func() bool { return s.word(taEmuMs) >= 8000 })
	s.secs(1)
	s.shot("03_ti92")
	s.quitSaving("switch92")

	// a new start goes straight to the TI-92 Plus (remembered), restored
	s.launch()
	s.startup("remembered", taPhRun)
	if s.word(taModel) != 92 || s.word(taRestored) != 1 {
		t.Fatalf("second start: model %d restored %d, want 92 / 1", s.word(taModel), s.word(taRestored))
	}

	// and back to the TI-89: its state (7 in the history) comes back
	s.openMenu()
	s.menuItem(4)
	s.pick("04_picker_back", "mydump89.bin")
	s.until("TI-89 again", 120, func() bool { return s.word(taPhase) == taPhRun && s.word(taModel) == 89 })
	if s.word(taRestored) != 1 {
		t.Fatalf("TI-89 not restored (%d, %q)", s.word(taRestored), s.msg())
	}
	s.secs(1.5)
	s.shot("05_ti89_back")
	s.quitSaving("switch89")
}
