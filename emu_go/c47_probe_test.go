//go:build probe

package cg50emu

// Probe (2026-11-23): C47.g3a (fxprgm/c47-cg, the C47/R47 RPN calculator on gint).
// Installs it on a 32 MB image, launches it, plays C47_KEYS and saves the add-in's frames.
//   C47_G3A     the .g3a (default ../../c47-cg/C47.g3a)
//   CG50_FLASH  the 32 MB flash image (default ../os/flash_dump/flash_full_32mb.bin)
//   CG50_STATE  its save-state (default ../os/flash_dump/cg50_state_32mb.bin)
//   C47_KEYS    comma list of CG50 key names (re/KEYMAP.md: 0-9 . EXE + - * / EXIT DEL SHIFT
//               ALPHA F1..F6 UP DOWN LEFT RIGHT OPTN VARS x^2 ^ log ln sin cos tan X ab/c S<->D
//               ( ) , -> (-) x10 MENU AC), "wait:N" (N x 100 M instructions), "shot",
//               "relaunch" (at the MAIN MENU: through Run-Matrix, then start C47 afresh)
//   frames: $TMPDIR/cg50_c47_NN.png, the LCD panel's contents (the add-in sends only the rows
//           that changed, so the last DMA source is not a whole frame)
//   go -C emu_go test -tags probe -run TestC47 -count=1 -v .

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

// c47Panel writes the LCD panel's contents (PanelWidth x PanelHeight).
func c47Panel(t *testing.T, e *Emulator, name string) {
	buf := make([]byte, PanelWidth*PanelHeight*4)
	e.FramebufferRGBA(buf)
	img := &image.RGBA{Pix: buf, Stride: PanelWidth * 4, Rect: image.Rect(0, 0, PanelWidth, PanelHeight)}
	f, err := os.Create(fmt.Sprintf("%s/cg50_%s.png", os.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

// c47Launch starts the last MAIN MENU icon (C47) and lets it boot. rcSession.launchAddin also
// taps UP afterwards (other add-ins swallow their first key), which C47 would act on.
func c47Launch(s *rcSession) {
	s.launch(-1)
	for n := 0; s.e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || s.e.Fault() != "" {
			s.t.Fatalf("C47 never started (%s)", s.e.Fault())
		}
		s.e.Step(1)
	}
	s.e.Step(300_000_000)
}

var c47Keys = map[string][2]uint32{
	"AC": {0, 0}, "0": {6, 1}, ".": {5, 1}, "x10": {4, 1}, "(-)": {3, 1}, "EXE": {2, 1},
	"1": {6, 2}, "2": {5, 2}, "3": {4, 2}, "+": {3, 2}, "-": {2, 2},
	"4": {6, 3}, "5": {5, 3}, "6": {4, 3}, "*": {3, 3}, "/": {2, 3},
	"7": {6, 4}, "8": {5, 4}, "9": {4, 4}, "DEL": {3, 4},
	"ab/c": {6, 5}, "S<->D": {5, 5}, "(": {4, 5}, ")": {3, 5}, ",": {2, 5}, "->": {1, 5},
	"X": {6, 6}, "log": {5, 6}, "ln": {4, 6}, "sin": {3, 6}, "cos": {2, 6}, "tan": {1, 6},
	"ALPHA": {6, 7}, "x^2": {5, 7}, "^": {4, 7}, "EXIT": {3, 7}, "DOWN": {2, 7}, "RIGHT": {1, 7},
	"SHIFT": {6, 8}, "OPTN": {5, 8}, "VARS": {4, 8}, "MENU": {3, 8}, "LEFT": {2, 8}, "UP": {1, 8},
	"F1": {6, 9}, "F2": {5, 9}, "F3": {4, 9}, "F4": {3, 9}, "F5": {2, 9}, "F6": {1, 9},
}

func TestC47(t *testing.T) {
	path := os.Getenv("C47_G3A")
	if path == "" {
		path = "../../c47-cg/C47.g3a"
	}
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no C47.g3a: ", err)
	}
	flash, err := os.ReadFile(envOr("CG50_FLASH", "../os/flash_dump/flash_full_32mb.bin"))
	if err != nil {
		t.Skip("no 32 MB flash image")
	}
	st, err := os.ReadFile(envOr("CG50_STATE", "../os/flash_dump/cg50_state_32mb.bin"))
	if err != nil {
		t.Skip("no 32 MB save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(20_000_000)

	s := &rcSession{t, e}
	if err := e.InstallAddin("C47.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	c47Launch(s)
	shot := 0
	save := func() {
		e.Step(50_000_000)
		c47Panel(t, e, fmt.Sprintf("c47_%02d", shot))
		shot++
	}
	save()
	for _, k := range strings.Split(os.Getenv("C47_KEYS"), ",") {
		if f := e.Fault(); f != "" {
			save()
			t.Fatal(f)
		}
		var n uint32
		switch {
		case k == "":
		case k == "shot":
			save()
		case k == "relaunch":
			// from the MAIN MENU: open Run-Matrix and come back (the OS ends the suspended
			// add-in), then start C47 afresh, which reloads its saved state
			c47Launch(s)
		case strings.HasPrefix(k, "wait:"):
			fmt.Sscanf(k, "wait:%d", &n)
			for i := uint32(0); i < n; i++ {
				e.Step(100_000_000)
			}
		default:
			rc, ok := c47Keys[k]
			if !ok {
				t.Fatalf("unknown key %q", k)
			}
			// hold it ~0.4 s, then let it run. gint reads the keyboard on its own 128 Hz timer,
			// and a tap (InjectKey: a few of the OS's KEYSC scans) can fall between two reads.
			e.KeyDown(rc[0], rc[1])
			e.Step(30_000_000)
			e.KeyUp(rc[0], rc[1])
			e.Step(120_000_000)
		}
	}
	// C47_PEEK=addr,addr...: 32-bit words (virtual addresses), now and after 50 M instructions
	if pk := os.Getenv("C47_PEEK"); pk != "" {
		for pass := 0; pass < 2; pass++ {
			for _, a := range strings.Split(pk, ",") {
				var v uint32
				fmt.Sscanf(a, "%x", &v)
				fmt.Printf("PEEK %d %08x = %08x\n", pass, v, e.mem.Read(v, 4))
			}
			e.Step(50_000_000)
		}
	}
	// C47_STACK=N: N words from r15 (return addresses in 0x003xxxxx: symbolize them)
	if n, _ := fmt.Sscanf(os.Getenv("C47_STACK"), "%d", new(int)); n == 1 {
		var cnt int
		fmt.Sscanf(os.Getenv("C47_STACK"), "%d", &cnt)
		sp := e.cpu.r[15]
		fmt.Printf("SP %08x\n", sp)
		for i := 0; i < cnt; i++ {
			if w := e.mem.Read(sp+uint32(4*i), 4); w>>20 == 0x003 {
				fmt.Printf("STACK %08x\n", w)
			}
		}
	}
	// C47_PCS=N: sample the PC N times, 1 M instructions apart (symbolize with sh-elf-addr2line)
	if n, _ := fmt.Sscanf(os.Getenv("C47_PCS"), "%d", new(int)); n == 1 {
		var cnt int
		fmt.Sscanf(os.Getenv("C47_PCS"), "%d", &cnt)
		for i := 0; i < cnt; i++ {
			e.Step(1_000_000)
			fmt.Printf("PC %08x PR %08x\n", e.cpu.pc, e.cpu.pr)
		}
	}
	if f := e.Fault(); f != "" {
		t.Fatal(f)
	}
}
