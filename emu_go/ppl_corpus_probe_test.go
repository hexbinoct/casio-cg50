//go:build probe

package cg50emu

// TestPPLCorpus: run programs from the PPL corpus (git-ignored third-party code in
// ppl_cg50/ref/corpus/src) in PPL.g3a for a few seconds each, press some keys, screenshot,
// then break with AC/ON. Finds the ones worth trying on the real calculator.
//
//   go test -tags probe -run TestPPLCorpus -count=1 .
//   PPL_CORPUS=name:FUNC,...  only these (default: the games and demos below); a name with
//                              a "/" is a path to a .ppl file anywhere
//   PPL_SECS=n                 seconds before the first screenshot (default 5)
//   PPL_KEYS="T:12 EXE F6 W:2 S:result"  keys after it (see pplScript); PPL_OUT=dir, PPL_G3A=file

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var pplCorpusDefault = "angryb:ANGRY_BIRDS,blotch:BLOTCH,breakout:breakout,chaos:chaos,cube:D_3D_2," +
	"g2048+shp:CLASSIC,gravity:main,hp2048:HP_2048,hp_prime_pong:PONG,lunar:LUNAR,primecaster3d:PrimeCaster3D," +
	"pvz:game,races:GAME_RACE,resistor:Resistor,slider:SLIDER,snake-pd:Snake,sudoku-ch:Sudoku," +
	"taylorani:Taylorani,tetrisim:TETRIS,tictactoe:TIC_TAC_TOE,towerofhanoi:TOWER1,tpirwheel:TPIRWHEEL," +
	"walrus:WalriiCatch,wzqai:main,pom:pom,csodds:CSODDS,plot3dxyz:Plot3Dxyz,contourmap:CONTOUR," +
	"earthspeed:EARTHSPEED,flashlight:FlashLight"

func TestPPLCorpus(t *testing.T) {
	g3aPath := os.Getenv("PPL_G3A")
	if g3aPath == "" {
		g3aPath = "../../ppl_cg50/cg50/PPL.g3a"
	}
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no PPL.g3a: ", err)
	}
	out := "../../ppl_cg50/build/probe_corpus"
	if o := os.Getenv("PPL_OUT"); o != "" {
		out = o
	}
	os.MkdirAll(out, 0o755)
	list := os.Getenv("PPL_CORPUS")
	if list == "" {
		list = pplCorpusDefault
	}
	type prog struct{ file, fn string }
	var report []string
	for _, it := range strings.Split(list, ",") {
		p := strings.SplitN(it, ":", 2)
		line := pplCorpusOne(t, g3a, out, p[0], p[1])
		report = append(report, line)
		t.Log(line)
		os.WriteFile(filepath.Join(out, "report.txt"), []byte(strings.Join(report, "\n")+"\n"), 0o644)
	}
}

// pplCorpusOne runs one program in a fresh emulator: \\fls0\PPL\file.ppl, its function fn
// from the function list (EXE runs a function without parameters), 5 s, some keys, 3 s, then
// AC/ON / EXIT until Home.
func pplCorpusOne(t *testing.T, g3a []byte, out, file, fn string) string {
	e := installState(t)
	e.SetInstrPerSecond(pplIPS)
	s := &pplSession{t, e, out}
	files := strings.Split(file, "+") // g2048+shp: a program and the library it calls
	file = strings.TrimSuffix(filepath.Base(files[0]), ".ppl")
	for _, f := range files {
		path := filepath.Join("../../ppl_cg50/ref/corpus/src", f+".ppl")
		if strings.Contains(f, "/") { // a path: /dir/name.ppl, installed as name.ppl
			path = f
			f = strings.TrimSuffix(filepath.Base(f), ".ppl")
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return f + ": " + err.Error()
		}
		pplInstallIn(t, e, "PPL", f+".ppl", src)
	}
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
	if s.word(pplLoadErr) != 0 {
		s.shot(file + "_load")
		return file + ": load error " + itoa(int(s.word(pplLoadErr))) + " line " + itoa(int(s.word(pplLoadLine)))
	}
	s.tap("F2")
	s.until("functions", 10, func() bool { return s.word(pplPhase) == pplPhFuncs })
	s.secs(0.3)
	for i := 0; s.str(pplMsg) != fn; i++ {
		if i > 200 {
			s.shot(file + "_nofn")
			return file + ": " + fn + " not in the function list"
		}
		s.tap("DOWN")
	}
	n := s.word(pplRuns)
	s.tap("EXE")
	wait := 5.0
	if w, err := strconv.ParseFloat(os.Getenv("PPL_SECS"), 64); err == nil {
		wait = w
	}
	s.secs(wait)
	s.shot(file + "_a")
	ph := s.word(pplPhase)
	if script := os.Getenv("PPL_KEYS"); script != "" {
		pplScript(s, file, script)
	} else {
		s.tap("RIGHT", "RIGHT", "UP", "EXE", "LEFT", "DOWN")
	}
	s.secs(3)
	s.shot(file + "_b")
	ph2 := s.word(pplPhase)
	for i := 0; i < 12 && s.word(pplPhase) != pplPhHome; i++ {
		switch s.word(pplPhase) {
		case pplPhError, pplPhTerm, pplPhFreeze, pplPhMsgbox, pplPhChoose, pplPhInput:
			s.tap("EXIT")
		default:
			s.tap("ACON")
		}
		s.secs(1)
	}
	st := "home"
	if s.word(pplPhase) != pplPhHome {
		st = "stuck in phase " + itoa(int(s.word(pplPhase)))
		s.shot(file + "_stuck")
	}
	r := s.str(pplLastOut)
	if s.word(pplRuns) == n {
		r = "(no result)"
	}
	return file + ": phases " + itoa(int(ph)) + "," + itoa(int(ph2)) + " err " +
		itoa(int(s.word(pplLastErr))) + " line " + itoa(int(s.word(pplLastErrLine))) +
		" out " + r + " -> " + st + "; arena peak " + itoa(int(s.word(pplArenaPeak)>>10)) + " KB"
}

// pplScript runs PPL_KEYS after the first screenshot: space-separated CG50 key names
// (EXE, F6, DOWN, NEG, ACON...), T:text (types digits, capitals and + - * / ( ) , . ^),
// W:seconds (waits), S:name (screenshot ppl_<file>_<name>.png). A function with
// parameters was pasted as "NAME(": type its arguments with T:... then ")" EXE.
func pplScript(s *pplSession, file, script string) {
	for _, tok := range strings.Fields(script) {
		switch {
		case strings.HasPrefix(tok, "T:"):
			s.typeText(tok[2:])
		case strings.HasPrefix(tok, "W:"):
			if w, err := strconv.ParseFloat(tok[2:], 64); err == nil {
				s.secs(w)
			}
		case strings.HasPrefix(tok, "S:"):
			s.shot(file + "_" + tok[2:])
		default:
			s.tap(tok)
		}
	}
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}
