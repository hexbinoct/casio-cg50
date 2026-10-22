//go:build probe

package cg50emu

// Probe (2026-10-18): ~/main/casio/dl_cg50, dynamic libraries for gint add-ins. Installs the
// demo add-in DLDEMO.g3a and its libraries (LIBA, LIBB, LIBV, LIBBAD, LIBBIG .dl files) into the
// 32 MB image's storage memory, launches DLDEMO and steps through its four screens with EXE:
//   1. LIBA through dl_open/dl_sym (data, BSS, host malloc/qsort/gint calls, callbacks)
//   2. plain-name calls through generated stubs: LIBB loads lazily and shares LIBA
//   3. errors: missing library, wrong gint stamp, API stamp mismatch in a lazy stub
//   4. LIBBIG (~1 MB): load time, checksum, unload + reload
//   5. a failing lazy stub with the loader's default handler: error screen, a key quits
// DLDEMO writes its results to a status block at physical 0x0C7F0000 (dl_cg50/demo/src/main.c,
// struct status); a PNG of the panel is saved after each screen.
//   DL_DIR  the dl_cg50 folder (default ../../dl_cg50); DL_OUT  PNG folder (default $DL_DIR/out/shots)
//   go -C emu_go test -tags probe -run 'TestDL' -count=1 -v .
// The emulator has no cache model (ocbp/ocbwb/icbi are no-ops, every write is seen by the next
// fetch at once), so this checks the loader's logic, not its cache maintenance.

import (
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	dlStatus = 0x0C7F0000 // physical: ARENA_END in demo/src/main.c
	dlMagic  = 0x444C444D // "DLDM"
	// word offsets in struct status
	dlStep, dlPass, dlFail, dlLibaUs, dlLibbUs, dlBigUs, dlBigBytes, dlBigFile, dlBigRelocs = 1, 2, 3, 4, 5, 6, 7, 8, 9
	dlNmap, dlMap                                                                           = 10, 11
	dlMapWords                                                                              = 13 // struct dl_map_entry
	dlMsg                                                                                   = (dlMap + 6*dlMapWords) * 4
)

var dlEXE = [2]uint32{2, 1} // EXE (row, col)

// the checks, in the order of the enum in main.c
var dlChecks = []string{
	"dl_open LIBA", "init + version string", ".data counter", ".bss zeroed", "one heap (lib malloc, host free)",
	"host callback", "lib static fn pointers", "host qsort -> lib comparator", "data export",
	"pointer table in .data", "private libgcc division", "LIBB not loaded before 1st call",
	"lazy load on 1st call", "LIBA stubs = dl_open copy", "LIBA state shared", "LIBB uses host snprintf + LIBA",
	"LIBB callback via LIBA", "missing library error", "wrong-gint error", "API stamp mismatch caught",
	"LIBBIG checksum", "close + reload same address", "dl_addr", "load map", "images written back (P1 == P2)",
}

type dlSession struct {
	t   *testing.T
	e   *Emulator
	out string
}

func (s *dlSession) word(i int) uint32 {
	return binary.BigEndian.Uint32(s.e.mem.dram[dlStatus-DramBase+4*i:])
}

func (s *dlSession) str(off, max int) string {
	b := s.e.mem.dram[dlStatus-DramBase+off:][:max]
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func (s *dlSession) step(n int) {
	s.e.Step(n)
	if f := s.e.Fault(); f != "" {
		s.shot("fault")
		s.t.Fatal(f)
	}
}

// until runs until cond holds, at most max instructions; returns the instructions it took.
func (s *dlSession) until(what string, max uint64, cond func() bool) uint64 {
	start := s.e.cpu.cycles
	for !cond() {
		if s.e.cpu.cycles-start > max {
			s.shot("timeout")
			s.t.Fatalf("%s: not after %d M instructions (step %#x)", what, max/1_000_000, s.word(dlStep))
		}
		s.step(1_000_000)
	}
	return s.e.cpu.cycles - start
}

func (s *dlSession) shot(name string) {
	buf := make([]byte, PanelWidth*PanelHeight*4)
	s.e.FramebufferRGBA(buf)
	img := &image.RGBA{Pix: buf, Stride: PanelWidth * 4, Rect: image.Rect(0, 0, PanelWidth, PanelHeight)}
	path := filepath.Join(s.out, "dl_"+name+".png")
	f, err := os.Create(path)
	if err != nil {
		s.t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
	s.t.Logf("saved %s", path)
}

func (s *dlSession) pixel(x, y int) (r, g, b byte) {
	buf := make([]byte, PanelWidth*PanelHeight*4)
	s.e.FramebufferRGBA(buf)
	p := buf[(y*PanelWidth+x)*4:]
	return p[0], p[1], p[2]
}

// press presses EXE until DLDEMO leaves the "waiting after step k" state (the first key after
// a launch can be swallowed).
func (s *dlSession) press(k uint32) {
	exe := dlEXE
	for tries := 0; s.word(dlStep) == 0x10|k; tries++ {
		if tries > 5 {
			s.t.Fatalf("EXE not taken after step %d", k)
		}
		s.e.InjectKey(exe[0], exe[1])
		for i := 0; i < 40 && s.word(dlStep) == 0x10|k; i++ {
			s.step(1_000_000)
		}
	}
}

// next presses EXE after step k, then waits for the state want; returns the instructions.
func (s *dlSession) next(k uint32, want uint32) uint64 {
	start := s.e.cpu.cycles
	s.press(k)
	s.until("step done", 3_000_000_000, func() bool { return s.word(dlStep) == want })
	s.step(5_000_000) // the frame reaches the panel
	return s.e.cpu.cycles - start
}

func (s *dlSession) logMap() {
	n := int(s.word(dlNmap))
	for i := 0; i < n && i < 6; i++ {
		w := dlMap + i*dlMapWords
		name := s.str(4*w, 16)
		base, end, textEnd := s.word(w+4), s.word(w+5), s.word(w+6)
		s.t.Logf("  map: %-6s %08x-%08x (text %d, %d bytes) api %08x file %d relocs %d imports %d load %d us refs %d",
			name, base, end, textEnd-base, end-base, s.word(w+7), s.word(w+8), s.word(w+9), s.word(w+10),
			s.word(w+11), s.word(w+12))
	}
}

func TestDL(t *testing.T) {
	dir := os.Getenv("DL_DIR")
	if dir == "" {
		dir = "../../dl_cg50"
	}
	out := os.Getenv("DL_OUT")
	if out == "" {
		out = filepath.Join(dir, "out", "shots")
	}
	g3a, err := os.ReadFile(filepath.Join(dir, "demo", "DLDEMO.g3a"))
	if err != nil {
		t.Skip("no DLDEMO.g3a (run dl_cg50/demo/build.sh): ", err)
	}
	os.MkdirAll(out, 0o755)

	e := installState(t)
	s := &dlSession{t, e, out}
	for _, lib := range []string{"LIBA", "LIBB", "LIBV", "LIBBAD", "LIBBIG"} {
		data, err := os.ReadFile(filepath.Join(dir, "out", lib+".dl"))
		if err != nil {
			t.Fatal(err)
		}
		if err := e.InstallFile(lib+".dl", data); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.InstallAddin("DLDEMO.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)

	rs := &rcSession{t, e}
	rs.launch(-1)
	s.until("DLDEMO start screen", 500_000_000, func() bool { return s.word(0) == dlMagic && s.word(dlStep) == 0x10 })
	s.step(5_000_000)
	s.shot("0_start")

	n1 := s.next(0, 0x11)
	s.shot("1_liba_explicit")
	if r, g, b := s.pixel(255, 176); !(b > 150 && r < 80 && g < 140) {
		t.Errorf("LIBA's panel not on screen: pixel (255,176) = %d,%d,%d", r, g, b)
	}
	n2 := s.next(1, 0x12)
	s.shot("2_stubs_lazy")
	s.logMap()
	n3 := s.next(2, 0x13)
	s.shot("3_errors")
	for i := 0; i < 3; i++ {
		t.Logf("  error %d: %s", i, s.str(dlMsg+112*i, 112))
	}
	// step 4, counting the instructions of dl_open("LIBBIG") between DLDEMO's markers
	start4 := e.cpu.cycles
	s.press(3)
	fine := func(want uint32) uint64 {
		for n := 0; s.word(dlStep) != want; n++ {
			if n > 1_000_000 {
				t.Fatalf("no marker %#x", want)
			}
			s.step(10_000)
		}
		return e.cpu.cycles
	}
	m0 := fine(0x40)
	bigIns := fine(0x41) - m0
	s.until("step 4", 3_000_000_000, func() bool { return s.word(dlStep) == 0x14 })
	s.step(5_000_000)
	n4 := e.cpu.cycles - start4
	s.shot("4_libbig")
	s.logMap()
	// 5. a failing lazy stub with dl's default handler: error screen, then any key quits
	s.next(4, 0xFE)
	s.step(30_000_000)
	s.shot("5_default_fatal")

	t.Logf("instructions: step 1 %.1f M (LIBA dl_open %d us TMU), step 2 %.1f M (LIBB lazy %d us), step 3 %.1f M, step 4 %.1f M",
		float64(n1)/1e6, s.word(dlLibaUs), float64(n2)/1e6, s.word(dlLibbUs), float64(n3)/1e6, float64(n4)/1e6)
	bigUs, bigFile := s.word(dlBigUs), s.word(dlBigFile)
	if bigFile > 0 {
		mb := float64(bigFile) / (1 << 20)
		t.Logf("LIBBIG: image %d bytes, file %d bytes, %d relocations; dl_open %.2f M instructions = %.2f M per MB of file; TMU %d us = %.0f ms per MB (emulated time)",
			s.word(dlBigBytes), bigFile, s.word(dlBigRelocs), float64(bigIns)/1e6, float64(bigIns)/1e6/mb, bigUs, float64(bigUs)/1000/mb)
	}

	pass, fail := s.word(dlPass), s.word(dlFail)
	var failed []string
	for i, name := range dlChecks {
		switch {
		case fail&(1<<i) != 0:
			failed = append(failed, name)
		case pass&(1<<i) == 0:
			failed = append(failed, name+" (not run)")
		}
	}
	t.Logf("checks: pass %#x fail %#x", pass, fail)
	if len(failed) > 0 {
		t.Errorf("failed: %s", strings.Join(failed, "; "))
	}

	// a key on the error screen quits back to the MAIN MENU
	exe := dlEXE
	e.InjectKey(exe[0], exe[1])
	s.until("MAIN MENU", 1_000_000_000, func() bool { return e.atMainMenu() })
}
