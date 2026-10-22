//go:build probe

package cg50emu

// Probe (2026-10-18): ~/main/casio/ppl_cg50 (PPL.g3a, the HP Prime PPL interpreter add-in,
// built from its cg50/ folder). Installs test programs into the 32 MB image's storage memory
// (\\fls0\PPL\*.ppl, a binary .hpprgm in the root, and spiro.ppl from the program corpus when
// it is there), launches the add-in and drives it with keys: Home line 1/3*3, programs loaded
// with the file picker, a recursive function, drawing (G0 in the Fit view), the soft-menu taps
// and the pointer (MOUSE), the ON key breaking a loop, the error screens, speed loops, and
// spiro (DRAWMENU + MOUSE + INPUT + CHOOSE). Reads the add-in's stats block at physical
// 0x0C1E0000 (cg50/src/stats.h, ppl_stats_t) and saves a PNG of the panel at every step.
//   PPL_G3A  the .g3a   (default ../../ppl_cg50/cg50/PPL.g3a)
//   PPL_OUT  PNG folder (default ../../ppl_cg50/build/probe)
//   PPL_SPIRO spiro.ppl (default ../../ppl_cg50/ref/corpus/src/spiro.ppl; skipped if missing)
//   go -C emu_go test -tags probe -run 'TestPPL' -count=1 -timeout 60m -v .

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

const (
	pplStats = 0x0C1E0000 // physical: CG50_STATS_ADDR
	pplMagic = 0x50504C31 // "PPL1"
	// word offsets in ppl_stats_t
	pplPhase, pplRuns, pplLastErr, pplLastErrLine, pplArenaUsed, pplArenaPeak, pplArenaSize = 1, 2, 3, 4, 5, 6, 7
	pplKeyEv, pplView, pplPtrMode, pplPtrX, pplPtrY, pplRunMs, pplFiles, pplProgs            = 8, 9, 10, 11, 12, 13, 14, 15
	pplPolls, pplPresents, pplLoadErr, pplLoadLine, pplVMBytes                              = 16, 17, 18, 19, 20
	pplLastIn, pplLastOut, pplMsg                                                           = 96, 160, 224 // byte offsets
	// phases (PH_*)
	pplPhHome, pplPhRun, pplPhInput, pplPhChoose, pplPhMsgbox, pplPhPick, pplPhError, pplPhTerm = 1, 2, 3, 4, 5, 6, 7, 8
	pplPhFreeze, pplPhFuncs, pplPhWait                                                          = 9, 10, 14
	pplIPS                                                                                      = 61_000_000 // the real CG50
)

// CG50 keys (row, col: re/KEYMAP.md)
var pplKey = map[string][2]uint32{
	"ACON": {0, 0}, "0": {6, 1}, ".": {5, 1}, "EXP": {4, 1}, "NEG": {3, 1}, "EXE": {2, 1},
	"1": {6, 2}, "2": {5, 2}, "3": {4, 2}, "+": {3, 2}, "-": {2, 2},
	"4": {6, 3}, "5": {5, 3}, "6": {4, 3}, "*": {3, 3}, "/": {2, 3},
	"7": {6, 4}, "8": {5, 4}, "9": {4, 4}, "DEL": {3, 4},
	"FRAC": {6, 5}, "FD": {5, 5}, "(": {4, 5}, ")": {3, 5}, ",": {2, 5}, "ARROW": {1, 5},
	"XOT": {6, 6}, "LOG": {5, 6}, "LN": {4, 6}, "SIN": {3, 6}, "COS": {2, 6}, "TAN": {1, 6},
	"ALPHA": {6, 7}, "SQUARE": {5, 7}, "^": {4, 7}, "EXIT": {3, 7}, "DOWN": {2, 7}, "RIGHT": {1, 7},
	"SHIFT": {6, 8}, "OPTN": {5, 8}, "VARS": {4, 8}, "MENU": {3, 8}, "LEFT": {2, 8}, "UP": {1, 8},
	"F1": {6, 9}, "F2": {5, 9}, "F3": {4, 9}, "F4": {3, 9}, "F5": {2, 9}, "F6": {1, 9},
}

// the ALPHA letters as printed on the CG50's keys
var pplAlpha = map[rune]string{
	'A': "XOT", 'B': "LOG", 'C': "LN", 'D': "SIN", 'E': "COS", 'F': "TAN", 'G': "FRAC", 'H': "FD",
	'I': "(", 'J': ")", 'K': ",", 'L': "ARROW", 'M': "7", 'N': "8", 'O': "9", 'P': "4", 'Q': "5",
	'R': "6", 'S': "*", 'T': "/", 'U': "1", 'V': "2", 'W': "3", 'X': "+", 'Y': "-", 'Z': "0",
}

type pplSession struct {
	t   *testing.T
	e   *Emulator
	out string
}

func (s *pplSession) word(i int) uint32 {
	return binary.BigEndian.Uint32(s.e.mem.dram[pplStats-DramBase+4*i:])
}

func (s *pplSession) str(off int) string {
	b := s.e.mem.dram[pplStats-DramBase+off:][:64]
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func (s *pplSession) cycles() uint64 { return s.e.cpu.cycles }

func (s *pplSession) secs(d float64) {
	end := s.cycles() + uint64(d*pplIPS)
	for s.cycles() < end {
		s.e.Step(5_000_000)
		if f := s.e.Fault(); f != "" {
			s.shot("fault")
			s.t.Fatal(f)
		}
	}
}

func (s *pplSession) until(what string, max float64, cond func() bool) float64 {
	start := s.cycles()
	for !cond() {
		if s.cycles()-start > uint64(max*pplIPS) {
			s.shot("timeout")
			s.t.Fatalf("%s: not after %.0f s (phase %d, runs %d, out %q, msg %q)", what, max,
				s.word(pplPhase), s.word(pplRuns), s.str(pplLastOut), s.str(pplMsg))
		}
		s.e.Step(1_000_000)
		if f := s.e.Fault(); f != "" {
			s.shot("fault")
			s.t.Fatal(f)
		}
	}
	return float64(s.cycles()-start) / pplIPS
}

func (s *pplSession) tap(keys ...string) {
	for _, k := range keys {
		c, ok := pplKey[k]
		if !ok {
			s.t.Fatalf("no key %q", k)
		}
		s.e.InjectKey(c[0], c[1])
		s.secs(0.25)
	}
}

// typeText types s on the Home line: digits and operators directly, capitals with ALPHA.
func (s *pplSession) typeText(text string) {
	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
			s.tap("ALPHA", pplAlpha[r])
		case r >= '0' && r <= '9', strings.ContainsRune("+-*/(),.^", r):
			s.tap(string(r))
		default:
			s.t.Fatalf("typeText: no key for %q", r)
		}
	}
}

func (s *pplSession) shot(name string) {
	buf := make([]byte, PanelWidth*PanelHeight*4)
	s.e.FramebufferRGBA(buf)
	img := &image.RGBA{Pix: buf, Stride: PanelWidth * 4, Rect: image.Rect(0, 0, PanelWidth, PanelHeight)}
	path := filepath.Join(s.out, "ppl_"+name+".png")
	f, err := os.Create(path)
	if err != nil {
		s.t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
	s.t.Logf("saved %s", path)
}

func (s *pplSession) home(what string) {
	s.until(what+": Home", 120, func() bool { return s.word(pplPhase) == pplPhHome })
}

// run types expr, EXE, waits for the evaluation to finish (Home again, or another phase that
// `done` accepts) and returns its result text and the SH-4 instructions it took.
func (s *pplSession) run(expr string, max float64) (string, uint64) {
	n := s.word(pplRuns)
	s.typeText(expr)
	c := pplKey["EXE"]
	s.e.InjectKey(c[0], c[1])
	t0 := s.cycles()
	for s.word(pplRuns) == n { // fine steps: the instruction count is the measurement
		if s.cycles()-t0 > uint64(max*pplIPS) {
			s.shot("timeout")
			s.t.Fatalf("%s: not finished after %.0f s (phase %d)", expr, max, s.word(pplPhase))
		}
		s.e.Step(20_000)
	}
	used := s.cycles() - t0
	s.until(expr+": Home", 10, func() bool { return s.word(pplPhase) == pplPhHome })
	out := s.str(pplLastOut)
	s.t.Logf("%-12s -> %q  (%d SH-4 instructions, %.3f s at 61 M/s; add-in timer %d ms; err %d line %d)",
		expr, out, used, float64(used)/pplIPS, s.word(pplRunMs), s.word(pplLastErr), s.word(pplLastErrLine))
	return out, used
}

// pick opens the file picker (F1 at Home) and loads the file named name.
func (s *pplSession) pick(name string) {
	s.tap("F1")
	s.until("picker", 30, func() bool { return s.word(pplPhase) == pplPhPick && s.str(pplMsg) != "" })
	s.secs(0.3)
	for i := 0; s.str(pplMsg) != name; i++ {
		if i > 60 {
			s.shot("picker_" + name)
			s.t.Fatalf("picker: no %s (at %q, %d files)", name, s.str(pplMsg), s.word(pplFiles))
		}
		s.tap("DOWN")
	}
	s.tap("EXE")
	s.secs(1)
}

func pplInstallIn(t *testing.T, e *Emulator, folder, name string, data []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := 0; i < 50 && !e.cpu.sleeping; i++ {
		e.stepLocked(1_000_000)
	}
	if e.atMainMenu() {
		e.tapAndSettle(6, 2, 0) // 1: Run-Matrix
		defer e.tapAndSettle(3, 8, 1)
	}
	for i := 0; i < 200 && !e.osIdle(); i++ {
		e.stepLocked(1_000_000)
	}
	if !e.osIdle() {
		t.Fatal(errNotIdle)
	}
	p, err := e.syscall(sysMalloc, installBudget, 0x100)
	if err != nil || p == 0 {
		t.Fatalf("malloc: %v", err)
	}
	for i, ch := range `\\fls0\` + folder + "\x00" {
		e.mem.W16(p+uint32(2*i), uint32(ch))
	}
	e.syscall(sysBfileCreate, installBudget, p, 5, 0) // 5 = a folder (exists already: fine)
	e.syscall(sysFree, installBudget, p)
	if err := e.writeFile(folder+`\`+name, data); err != nil {
		t.Fatal(err)
	}
}

// pplHpprgm wraps PPL source in HP's binary .hpprgm container: the magic, then a record
// [u32 length][u32 tag][UTF-16LE text, NUL] (src/hpprgm.c finds the record by its shape).
func pplHpprgm(src string) []byte {
	u := utf16.Encode([]rune(src + "\x00"))
	b := []byte{0x7C, 0x61, 0x8A, 0xB2, 0xFE, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0}
	b = binary.LittleEndian.AppendUint32(b, uint32(4+2*len(u)))
	b = binary.LittleEndian.AppendUint32(b, 0x00C0009B)
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return b
}

const pplFib = `// a recursive function
EXPORT FIB(n)
BEGIN
  IF n<2 THEN RETURN n; END;
  RETURN FIB(n-1)+FIB(n-2);
END;
`

const pplDemo = `// drawing, touches, a loop to break, an error, speed
EXPORT DEMO()
BEGIN
  LOCAL i;
  RECT();
  FOR i FROM 0 TO 16 DO
    LINE_P(0,i*15,319,239-i*15,RGB(255,0,0));
    LINE_P(i*20,0,319-i*20,239,RGB(0,0,255));
  END;
  RECT_P(100,100,220,140,RGB(0,160,0));
  DRAWMENU("A","B","C","D","E","F");
  FREEZE;
END;

EXPORT TAP()
BEGIN
  LOCAL m;
  RECT();
  DRAWMENU("1","2","3","4","5","6");
  REPEAT m:=MOUSE; UNTIL SIZE(m(1))>0;
  RETURN m(1);
END;

EXPORT LOOP()
BEGIN
  LOCAL a:=0;
  WHILE 1 DO a:=a+1; END;
END;

EXPORT ERR()
BEGIN
  LOCAL a:={1,2,3};
  RETURN a(7);
END;

EXPORT BENCHA()
BEGIN
  LOCAL i,s:=0;
  FOR i FROM 1 TO 1000 DO s:=s+i*i; END;
  RETURN s;
END;

EXPORT BENCHB()
BEGIN
  LOCAL i,s:=0;
  FOR i FROM 1 TO 100 DO s:=s+SIN(i); END;
  RETURN s;
END;

EXPORT BENCHC()
BEGIN
  LOCAL i;
  FOR i FROM 1 TO 1000 DO END;
  RETURN i;
END;

EXPORT WAITK()
BEGIN
  RETURN WAIT(-1);
END;

EXPORT ASK()
BEGIN
  LOCAL a,b;
  INPUT({a,b},"Two values",{"a=","b="},{"the first","the second"});
  RETURN a*b;
END;

EXPORT HELLO()
BEGIN
  LOCAL i;
  PRINT();
  PRINT("Hello from PPL");
  FOR i FROM 1 TO 5 DO PRINT(i+"/7 = "+i/7); END;
  MSGBOX("Printed 6 lines");
  RETURN 5;
END;
`

const pplBad = `EXPORT BAD()
BEGIN
  RETURN 1+;
END;
`

const pplHp = `EXPORT TWICE(x)
BEGIN
  RETURN 2*x;
END;
`

func TestPPL(t *testing.T) {
	g3aPath := os.Getenv("PPL_G3A")
	if g3aPath == "" {
		g3aPath = "../../ppl_cg50/cg50/PPL.g3a"
	}
	out := os.Getenv("PPL_OUT")
	if out == "" {
		out = "../../ppl_cg50/build/probe"
	}
	spiroPath := os.Getenv("PPL_SPIRO")
	if spiroPath == "" {
		spiroPath = "../../ppl_cg50/ref/corpus/src/spiro.ppl"
	}
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no PPL.g3a: ", err)
	}
	os.MkdirAll(out, 0o755)
	e := installState(t)
	e.SetInstrPerSecond(pplIPS)
	s := &pplSession{t, e, out}

	pplInstallIn(t, e, "PPL", "FIB.ppl", []byte(pplFib))
	pplInstallIn(t, e, "PPL", "DEMO.ppl", []byte(pplDemo))
	pplInstallIn(t, e, "PPL", "BAD.ppl", []byte(pplBad))
	if err := e.InstallFile("TWICE.hpprgm", pplHpprgm(pplHp)); err != nil {
		t.Fatal(err)
	}
	spiro, spiroErr := os.ReadFile(spiroPath)
	if spiroErr == nil {
		pplInstallIn(t, e, "PPL", "SPIRO.ppl", spiro)
	}
	if err := e.InstallAddin("PPL.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)

	// 1. start: Home
	rs := &rcSession{t, e}
	rs.launch(-1)
	for n := 0; e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || e.Fault() != "" {
			t.Fatalf("PPL never started (%s)", e.Fault())
		}
		e.Step(1)
	}
	s.until("magic", 30, func() bool { return s.word(0) == pplMagic })
	s.home("start")
	s.secs(0.5)
	t.Logf("VM struct %d bytes; arena %d KB", s.word(pplVMBytes), s.word(pplArenaSize)>>10)
	s.shot("01_home")

	// 2. 1/3*3 on the Home line
	if got, _ := s.run("1/3*3", 30); got != "0.999999999999" {
		t.Errorf("1/3*3 = %q, want 0.999999999999", got)
	}
	if got, _ := s.run("2^0.5", 30); got != "1.41421356237" {
		t.Errorf("2^0.5 = %q", got)
	}
	s.shot("02_home_results")

	// 3. the picker: FIB.ppl from \\fls0\PPL\, then FIB(15) from the function list
	s.pick(`PPL\FIB.ppl`)
	s.shot("03_loaded_fib")
	if s.word(pplLoadErr) != 0 {
		t.Fatalf("FIB.ppl: load error %d: %q", s.word(pplLoadErr), s.str(pplMsg))
	}
	s.tap("EXE")
	s.home("after load")
	s.tap("F2")
	s.until("functions", 10, func() bool { return s.word(pplPhase) == pplPhFuncs })
	s.secs(0.3)
	s.shot("04_functions")
	for i := 0; s.str(pplMsg) != "FIB" && i < 10; i++ {
		s.tap("DOWN")
	}
	s.tap("EXE") // pastes FIB(
	s.home("paste")
	if got, _ := s.run("15)", 60); got != "610" {
		t.Errorf("FIB(15) = %q, want 610", got)
	}
	s.shot("05_fib")

	// 4. a binary .hpprgm from the root
	s.pick(`TWICE.hpprgm`)
	if s.word(pplLoadErr) != 0 {
		t.Errorf("TWICE.hpprgm: load error %d: %q", s.word(pplLoadErr), s.str(pplMsg))
	}
	s.tap("EXE")
	s.home("after hpprgm")
	if got, _ := s.run("TWICE(21)", 30); got != "42" {
		t.Errorf("TWICE(21) = %q, want 42", got)
	}

	// 5. DEMO.ppl: drawing on G0 (the gfx stub's commands), shown in the Fit view
	s.pick(`PPL\DEMO.ppl`)
	if s.word(pplLoadErr) != 0 {
		t.Fatalf("DEMO.ppl: load error %d: %q", s.word(pplLoadErr), s.str(pplMsg))
	}
	s.tap("EXE")
	s.home("after DEMO load")
	n := s.word(pplRuns)
	s.typeText("DEMO()")
	s.tap("EXE")
	s.until("DEMO frozen", 60, func() bool { return s.word(pplPhase) == pplPhFreeze })
	s.secs(0.5)
	t.Logf("DEMO drew: %d LCD pushes of changed rows", s.word(pplPresents))
	s.shot("06_demo_fit")
	s.tap("EXE")
	s.until("DEMO end", 10, func() bool { return s.word(pplRuns) != n && s.word(pplPhase) == pplPhHome })

	// 6. touches: F3 taps the third soft-menu button
	n = s.word(pplRuns)
	s.typeText("TAP()")
	s.tap("EXE")
	s.secs(1.5)
	s.shot("07_tap_menu")
	s.tap("F3")
	s.until("TAP end", 20, func() bool { return s.word(pplRuns) != n })
	s.home("TAP")
	if got := s.str(pplLastOut); !strings.HasPrefix(got, "{132,230,132,230,") {
		t.Errorf("TAP() with F3 = %q, want {132,230,132,230,...}", got)
	} else {
		t.Logf("TAP() with F3 = %q", got)
	}
	// the pointer: OPTN, 5 x RIGHT, 3 x DOWN, EXE
	n = s.word(pplRuns)
	s.typeText("TAP()")
	s.tap("EXE")
	s.secs(1.5)
	s.tap("OPTN", "RIGHT", "RIGHT", "RIGHT", "RIGHT", "RIGHT", "DOWN", "DOWN", "DOWN")
	s.secs(0.5)
	s.shot("08_pointer")
	t.Logf("pointer mode %d at %d,%d", s.word(pplPtrMode), s.word(pplPtrX), s.word(pplPtrY))
	px, py := int(s.word(pplPtrX)), int(s.word(pplPtrY))
	s.tap("EXE")
	s.until("TAP (pointer) end", 20, func() bool { return s.word(pplRuns) != n })
	s.home("TAP pointer")
	want := fmt.Sprintf("{%d,%d,%d,%d,", px, py, px, py)
	if got := s.str(pplLastOut); !strings.HasPrefix(got, want) || px <= 160 || py <= 120 {
		t.Errorf("TAP() with the pointer = %q, want %s...} (right and down of 160,120)", got, want)
	} else {
		t.Logf("TAP() with the pointer = %q", got)
	}

	// 7. WAIT(-1): a key, then a touch
	n = s.word(pplRuns)
	s.typeText("WAITK()")
	s.tap("EXE")
	s.until("WAIT(-1) waits", 10, func() bool { return s.word(pplPhase) == pplPhWait })
	s.tap("7")
	s.until("WAITK end", 10, func() bool { return s.word(pplRuns) != n })
	s.home("WAITK")
	if got := s.str(pplLastOut); got != "32" {
		t.Errorf("WAIT(-1) with 7 = %q, want 32", got)
	}
	n = s.word(pplRuns)
	s.typeText("WAITK()")
	s.tap("EXE")
	s.until("WAIT(-1) waits", 10, func() bool { return s.word(pplPhase) == pplPhWait })
	s.tap("F6")
	s.until("WAITK end", 10, func() bool { return s.word(pplRuns) != n })
	s.home("WAITK touch")
	if got := s.str(pplLastOut); got != "{0,291,230}" {
		t.Errorf("WAIT(-1) with F6 = %q, want {0,291,230}", got)
	}

	// 8. AC/ON breaks a loop
	n = s.word(pplRuns)
	s.typeText("LOOP()")
	s.tap("EXE")
	s.secs(2)
	s.shot("09_loop_running")
	s.tap("ACON")
	s.until("break", 10, func() bool { return s.word(pplRuns) != n })
	s.home("break")
	if got := s.str(pplLastOut); got != "Break" {
		t.Errorf("LOOP() + AC/ON = %q, want Break", got)
	}
	s.shot("10_break")

	// 9. a run-time error: the error screen with the line
	n = s.word(pplRuns)
	s.typeText("ERR()")
	s.tap("EXE")
	s.until("error screen", 20, func() bool { return s.word(pplPhase) == pplPhError })
	s.secs(0.3)
	s.shot("11_error")
	if s.word(pplLastErr) != 7 || s.word(pplLastErrLine) != 33 {
		t.Errorf("ERR(): error %d line %d, want 7 (Index outside range) line 33", s.word(pplLastErr), s.word(pplLastErrLine))
	}
	s.tap("EXE")
	s.home("after error")

	// 10. a compile error: the picker's error screen
	s.pick(`PPL\BAD.ppl`)
	s.shot("12_compile_error")
	if s.word(pplLoadErr) == 0 || s.word(pplLoadLine) != 3 {
		t.Errorf("BAD.ppl: load error %d line %d, want a syntax error at line 3", s.word(pplLoadErr), s.word(pplLoadLine))
	}
	s.tap("EXE")
	s.home("after compile error")

	// 11. INPUT
	n = s.word(pplRuns)
	s.typeText("ASK()")
	s.tap("EXE")
	s.until("INPUT", 10, func() bool { return s.word(pplPhase) == pplPhInput })
	s.tap("6", "EXE", "7")
	s.secs(0.3)
	s.shot("13_input")
	s.tap("EXE", "EXE")
	s.until("ASK end", 10, func() bool { return s.word(pplRuns) != n })
	s.home("ASK")
	if got := s.str(pplLastOut); got != "42" {
		t.Errorf("ASK() with 6 and 7 = %q, want 42", got)
	}

	// 12. PRINT: the terminal during the run (MSGBOX over it) and after it, until a key
	n = s.word(pplRuns)
	s.typeText("HELLO()")
	s.tap("EXE")
	s.until("MSGBOX", 20, func() bool { return s.word(pplPhase) == pplPhMsgbox })
	s.secs(0.3)
	s.shot("14_msgbox_over_terminal")
	s.tap("EXE")
	s.until("terminal after the run", 20, func() bool { return s.word(pplPhase) == pplPhTerm })
	s.secs(0.3)
	s.shot("15_terminal")
	s.tap("EXE")
	s.until("HELLO end", 10, func() bool { return s.word(pplRuns) != n })
	s.home("HELLO")
	if got := s.str(pplLastOut); got != "5" {
		t.Errorf("HELLO() = %q, want 5", got)
	}

	// 13. the views: 1:1 low (rows 16-239) from Setup, DEMO again, then Fit again; the key map
	s.tap("F5", "RIGHT", "RIGHT", "EXIT")
	if v := s.word(pplView); v != 2 {
		t.Errorf("view %d after RIGHT RIGHT, want 2 (1:1 low)", v)
	}
	n = s.word(pplRuns)
	s.typeText("DEMO()")
	s.tap("EXE")
	s.until("DEMO frozen (1:1 low)", 60, func() bool { return s.word(pplPhase) == pplPhFreeze })
	s.secs(0.5)
	s.shot("16_demo_1to1_low")
	s.tap("EXE")
	s.until("DEMO end", 10, func() bool { return s.word(pplRuns) != n && s.word(pplPhase) == pplPhHome })
	s.tap("F5", "RIGHT", "DOWN", "DOWN", "EXE")
	s.secs(0.3)
	s.shot("17_keymap")
	s.tap("EXE")
	s.shot("18_keymap_touch")
	s.tap("EXE", "EXIT")
	s.home("after setup")
	if v := s.word(pplView); v != 0 {
		t.Errorf("view %d, want 0 (Fit)", v)
	}

	// 14. speed
	pplSpeed(s)

	// 13. spiro.ppl: DRAWMENU, MOUSE (soft-menu taps), INPUT, CHOOSE, LINE_P, FREEZE, KILL
	if spiroErr != nil {
		t.Logf("no spiro.ppl (%v): skipped", spiroErr)
	} else {
		pplSpiro(s)
	}
	s.shot("99_final")
	t.Logf("arena: %d KB in use, peak %d KB of %d KB; %d VM polls", s.word(pplArenaUsed)>>10,
		s.word(pplArenaPeak)>>10, s.word(pplArenaSize)>>10, s.word(pplPolls))
}

// TestPPLSpeed: only the loop benchmarks of TestPPL (and the PC profile with PPL_PROF=1).
//   go -C emu_go test -tags probe -run 'TestPPLSpeed' -count=1 -v .
func TestPPLSpeed(t *testing.T) {
	g3aPath := os.Getenv("PPL_G3A")
	if g3aPath == "" {
		g3aPath = "../../ppl_cg50/cg50/PPL.g3a"
	}
	out := os.Getenv("PPL_OUT")
	if out == "" {
		out = "../../ppl_cg50/build/probe"
	}
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no PPL.g3a: ", err)
	}
	os.MkdirAll(out, 0o755)
	e := installState(t)
	e.SetInstrPerSecond(pplIPS)
	s := &pplSession{t, e, out}
	pplInstallIn(t, e, "PPL", "DEMO.ppl", []byte(pplDemo))
	if err := e.InstallAddin("PPL.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	rs := &rcSession{t, e}
	rs.launch(-1)
	for n := 0; e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || e.Fault() != "" {
			t.Fatalf("PPL never started (%s)", e.Fault())
		}
		e.Step(1)
	}
	s.until("magic", 30, func() bool { return s.word(0) == pplMagic })
	s.home("start")
	s.secs(0.5)
	pplSpeed(s)
}

// pplSpeed runs the loop benchmarks and reports SH-4 instructions per PPL operation.
func pplSpeed(s *pplSession) {
	_, base := s.run("0", 30) // the cost of EXE, a Home evaluation and the redraws
	s.t.Logf("overhead of a Home evaluation: %d instructions (subtracted below)", base)
	_, empty := s.run("BENCHC()", 120)
	got, add := s.run("BENCHA()", 300)
	empty, add = empty-base, add-base
	if got != "333833500" {
		s.t.Errorf("BENCHA() = %q, want 333833500", got)
	}
	got, sin := s.run("BENCHB()", 300)
	sin -= base
	if os.Getenv("PPL_PROF") != "" {
		s.profile("BENCHA()")
		s.profile("BENCHB()")
	}
	s.t.Logf("BENCHB() = %s", got)
	// per iteration; the empty FOR loop is the loop's own cost
	s.t.Logf("speed: empty FOR iteration %d SH-4 instructions; s:=s+i*i %d per iteration (%d beyond the loop); s:=s+SIN(i) %d per iteration (%d beyond the loop)",
		empty/1000, add/1000, (add-empty)/1000, sin/100, sin/100-empty/1000)
	s.t.Logf("speed at 61 M SH-4 instructions/s: 1000 x s:=s+i*i %.3f s, 100 x SIN %.3f s, one SIN ~%.2f ms",
		float64(add)/pplIPS, float64(sin)/pplIPS, float64(sin/100-empty/1000)/pplIPS*1000)
}

// profile runs expr sampling the PC every 499 instructions and writes "pc count" lines to
// <out>/prof_<expr>.txt (symbolize with sh-elf-nm on cg50/build-cg/ppl).
func (s *pplSession) profile(expr string) {
	n := s.word(pplRuns)
	s.typeText(expr)
	c := pplKey["EXE"]
	s.e.InjectKey(c[0], c[1])
	hist := map[uint32]int{}
	for s.word(pplRuns) == n {
		s.e.Step(499)
		hist[s.e.cpu.pc]++
	}
	s.home(expr)
	var b strings.Builder
	for pc, k := range hist {
		fmt.Fprintf(&b, "%08x %d\n", pc, k)
	}
	name := filepath.Join(s.out, "prof_"+strings.Trim(expr, "()")+".txt")
	os.WriteFile(name, []byte(b.String()), 0o644)
	s.t.Logf("profile of %s: %d samples -> %s", expr, len(hist), name)
}

func pplSpiro(s *pplSession) {
	s.pick(`PPL\SPIRO.ppl`)
	if s.word(pplLoadErr) != 0 {
		s.shot("spiro_load")
		s.t.Fatalf("SPIRO.ppl: load error %d line %d: %q", s.word(pplLoadErr), s.word(pplLoadLine), s.str(pplMsg))
	}
	s.tap("EXE")
	s.home("after SPIRO load")
	n := s.word(pplRuns)
	s.typeText("SPIRO()")
	s.tap("EXE")
	s.secs(2)
	s.shot("20_spiro_menu")
	// F2: Inner -> INPUT R, r, d
	s.tap("F2")
	s.until("spiro INPUT", 20, func() bool { return s.word(pplPhase) == pplPhInput })
	s.tap("5", "EXE", "3", "EXE", "5")
	s.shot("21_spiro_input")
	s.tap("EXE", "EXE")
	t0 := s.cycles()
	s.until("spiro drawing", 600, func() bool {
		ph := s.word(pplPhase)
		return ph == pplPhRun && s.word(pplPresents) > 0 && s.cycles()-t0 > 30*pplIPS/10
	})
	s.secs(1)
	s.shot("22_spiro_drawing")
	// the hypotrochoid: lcm(5,3)/0.1 = 150 steps; wait for the menu loop again (MOUSE)
	s.secs(20)
	s.shot("23_spiro_inner")
	// F5: Color -> CHOOSE, 5 = Red
	s.tap("F5")
	s.until("spiro CHOOSE", 30, func() bool { return s.word(pplPhase) == pplPhChoose })
	s.secs(0.3)
	s.shot("24_spiro_choose")
	s.tap("5")
	s.secs(1)
	// F3: Outer, R=4 r=1 d=2 in red
	s.tap("F3")
	s.until("spiro INPUT 2", 20, func() bool { return s.word(pplPhase) == pplPhInput })
	s.tap("4", "EXE", "1", "EXE", "2", "EXE", "EXE")
	s.secs(30)
	s.shot("25_spiro_outer_red")
	// F6: Exit (PRINT; RECT; KILL)
	s.tap("F6")
	s.until("spiro end", 30, func() bool { return s.word(pplRuns) != n })
	s.home("spiro")
	s.t.Logf("SPIRO() ended: %q (err %d)", s.str(pplLastOut), s.word(pplLastErr))
	s.shot("26_spiro_done")
}
