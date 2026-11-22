//go:build probe && ti68k

package cg50emu

// Probe (2026-11-08): ti68k_cg50/addin (TIbench.g3a: Musashi + a TI-89 / TI-92 Plus
// machine as a gint add-in, ROM embedded). Installs it on the 32 MB image, launches it and lets
// it run its key scenario (ti68k_cg50/bench/scenario.c), reading the add-in's stats block at
// physical 0x0C1E0000 (ti68k_cg50/addin/src/main.c, stats_t) to get SH-4 instructions per 68000
// instruction per scenario step. Executed() is split by the block's `phase` word (2 = inside
// ti_run, 3 = the add-in's own UI) at every chunk of ~4000 instructions; the PC at each chunk end
// is a profile sample, mapped to the add-in's ELF symbols.
// Own build tag so that `-tags probe` runs of the other probes never compile this file:
//   TI68K_G3A  the .g3a   (default ../../ti68k_cg50/addin/TIbench.g3a)
//   TI68K_ELF  its ELF    (default ../../ti68k_cg50/addin/build-cg/tibench)
//   TI68K_OUT  where to save a screen PNG after every scenario step (default: none)
//   go -C emu_go test -tags 'probe ti68k' -run TestTI68k -count=1 -timeout 60m -v .

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"sort"
	"testing"
)

const (
	tkStats = 0x0C1E0000 // physical: STATS_ADDR
	tkMagic = 0x54493638 // "TI68"
	// word offsets in stats_t
	tkPhase, tkEmuMs, tkInstr, tkBusyKc, tkStep, tkFrames, tkCoreUs, tkUiUs, tkDrawMs, tkLoadUs, tkModel, tkClock = 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12
	tkPhCore                                                                                                      = 2
)

var tkStepNames = []string{"boot 0-1 s", "boot 1-2 s", "boot 2-3 s", "boot 3-4 s", "boot 4-5 s", "boot 5-6 s",
	"idle at home (5 s)", "2+3 ENTER", "type 7 digits", "CLEAR CLEAR", "type factor(x^3-1)", "ENTER factor(x^3-1)",
	"type integral", "ENTER integral", "type factor(2^64+1)", "ENTER factor(2^64+1)", "open Y= editor",
	"type y1=sin(x)", "GRAPH sin(x)", "ZoomStd redraw", "HOME, idle 5 s"}

func tkWord(e *Emulator, i int) uint32 {
	return binary.BigEndian.Uint32(e.mem.dram[tkStats-DramBase+4*i:])
}

func tkSetWord(e *Emulator, i int, v uint32) {
	binary.BigEndian.PutUint32(e.mem.dram[tkStats-DramBase+4*i:], v)
}

type tkSym struct {
	addr, size uint64
	name       string
}

func tkSymbols(t *testing.T, path string) []tkSym {
	f, err := elf.Open(path)
	if err != nil {
		t.Logf("no symbols (%v): profile by address only", err)
		return nil
	}
	defer f.Close()
	syms, _ := f.Symbols()
	var out []tkSym
	for _, s := range syms {
		if elf.ST_TYPE(s.Info) == elf.STT_FUNC {
			out = append(out, tkSym{s.Value, s.Size, s.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].addr < out[j].addr })
	return out
}

func tkLookup(syms []tkSym, pc uint32) string {
	if pc >= 0x80000000 {
		return fmt.Sprintf("OS %08x", pc&^0xFF)
	}
	i := sort.Search(len(syms), func(i int) bool { return syms[i].addr > uint64(pc) }) - 1
	if i < 0 {
		return fmt.Sprintf("? %08x", pc)
	}
	return syms[i].name
}

func tkSaveFrame(t *testing.T, e *Emulator, path string) {
	const w, h = 396, 224
	sar, ok := e.mmio.FrameSAR()
	if !ok {
		return
	}
	buf := e.mem.span(sar, w*h*2)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for p := 0; p < w*h; p++ {
		c := uint16(buf[2*p])<<8 | uint16(buf[2*p+1])
		img.SetRGBA(p%w, p/w, color.RGBA{uint8(c>>11) << 3, uint8(c>>5&0x3F) << 2, uint8(c&0x1F) << 3, 255})
	}
	f, err := os.Create(path)
	if err != nil {
		t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
}

func TestTI68k(t *testing.T) {
	g3aPath := os.Getenv("TI68K_G3A")
	if g3aPath == "" {
		g3aPath = "../../ti68k_cg50/addin/TIbench.g3a"
	}
	elfPath := os.Getenv("TI68K_ELF")
	if elfPath == "" {
		elfPath = "../../ti68k_cg50/addin/build-cg/tibench"
	}
	outDir := os.Getenv("TI68K_OUT")
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no TIbench.g3a: ", err)
	}
	syms := tkSymbols(t, elfPath)

	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("TIbench.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launch(-1)
	for n := 0; e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || e.Fault() != "" {
			t.Fatalf("TIbench never started (%s)", e.Fault())
		}
		e.Step(1)
	}
	launched := e.Executed()
	for tkWord(e, 0) != tkMagic || tkWord(e, tkEmuMs) == 0 {
		if e.Fault() != "" || e.Executed()-launched > 2_000_000_000 {
			t.Fatalf("no stats block (magic %08x phase %x, %s)", tkWord(e, 0), tkWord(e, tkPhase), e.Fault())
		}
		e.Step(10_000)
	}
	tkSetWord(e, tkDrawMs, 0) // draw only at step ends
	t.Logf("start-up to the first emulated ms: %.1f M SH-4 instructions (ROM unpack + reset: %d us by the TMU), model %d, %d Hz",
		float64(e.Executed()-launched)/1e6, tkWord(e, tkLoadUs), tkWord(e, tkModel), tkWord(e, tkClock))
	clock := float64(tkWord(e, tkClock))

	type acc struct {
		core, ui          uint64
		ms, instr, busyKc uint32
		ms0, in0, b0      uint32
		core0, ui0        uint64
		prof              map[string]int
		samples           int
	}
	steps := map[uint32]*acc{}
	var order []uint32
	var core, ui uint64
	allProf := map[string]int{}
	allSamples := 0
	last := uint32(0xFFFFFFFF)
	rnd := rand.New(rand.NewSource(1))
	close := func(st uint32) {
		if a := steps[st]; a != nil {
			a.core += core - a.core0
			a.ui += ui - a.ui0
			a.ms += tkWord(e, tkEmuMs) - a.ms0
			a.instr += tkWord(e, tkInstr) - a.in0
			a.busyKc += tkWord(e, tkBusyKc) - a.b0
		}
	}
	shotStep, shotFrames := -1, uint32(0)
	start := e.Executed()
	for {
		st := tkWord(e, tkStep)
		if st != last {
			close(last)
			if outDir != "" && last != 0xFFFFFFFF {
				shotStep, shotFrames = int(last), tkWord(e, tkFrames)
			}
			if st == 100 {
				break
			}
			a := steps[st]
			if a == nil {
				a = &acc{prof: map[string]int{}}
				steps[st] = a
				order = append(order, st)
			}
			a.core0, a.ui0, a.ms0, a.in0, a.b0 = core, ui, tkWord(e, tkEmuMs), tkWord(e, tkInstr), tkWord(e, tkBusyKc)
			last = st
		}
		if e.Executed()-start > 60_000_000_000 {
			t.Fatalf("scenario not done after 60 G SH-4 instructions (step %d)", st)
		}
		before := e.Executed()
		e.Step(3000 + rnd.Intn(2000))
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		d := e.Executed() - before
		// the frame drawn at the end of a step: saved once the add-in is back in the core
		if shotStep >= 0 && tkWord(e, tkFrames) != shotFrames && tkWord(e, tkPhase) == tkPhCore {
			tkSaveFrame(t, e, fmt.Sprintf("%s/ti68k_step%02d.png", outDir, shotStep))
			shotStep = -1
		}
		if tkWord(e, tkPhase) == tkPhCore {
			core += d
			name := tkLookup(syms, e.PC())
			steps[last].prof[name]++
			steps[last].samples++
			allProf[name]++
			allSamples++
		} else {
			ui += d
		}
	}

	t.Logf("%-22s %8s %8s %10s %10s %9s %9s %8s %8s", "scenario step", "emu ms", "busy ms", "68k M", "SH4 core M",
		"SH4/68k", "UI SH4 M", "%@61M", "%busy@61M")
	var tot acc
	for _, st := range order {
		a := steps[st]
		name := fmt.Sprint(st)
		if int(st) >= 1 && int(st)-1 < len(tkStepNames) {
			name = tkStepNames[st-1]
		}
		busyMs := float64(a.busyKc) * 1000 / clock * 1000
		per := 0.0
		if a.instr > 0 {
			per = float64(a.core) / float64(a.instr)
		}
		pct := 0.0
		if a.core > 0 {
			pct = 100 * float64(a.ms) / 1000 / (float64(a.core) / 61e6)
		}
		// speed while the 68000 is busy: SH-4 time per busy emulated time (idle time costs little)
		pctBusy := 0.0
		if a.core > 0 && busyMs > 0 {
			pctBusy = 100 * busyMs / 1000 / (float64(a.core) / 61e6)
		}
		t.Logf("%-22s %8d %8.0f %10.3f %10.1f %9.1f %9.1f %7.1f%% %8.1f%%", name, a.ms, busyMs, float64(a.instr)/1e6,
			float64(a.core)/1e6, per, float64(a.ui)/1e6, pct, pctBusy)
		tot.core += a.core
		tot.ui += a.ui
		tot.ms += a.ms
		tot.instr += a.instr
		tot.busyKc += a.busyKc
	}
	t.Logf("all: %d emulated ms, %.1f M 68k instructions, %.1f M SH-4 in the core = %.1f SH-4 per 68k instruction; UI %.1f M",
		tot.ms, float64(tot.instr)/1e6, float64(tot.core)/1e6, float64(tot.core)/float64(tot.instr), float64(tot.ui)/1e6)

	type kv struct {
		k string
		v int
	}
	per := float64(tot.core) / float64(tot.instr)
	var top []kv
	for k, v := range allProf {
		top = append(top, kv{k, v})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
	t.Logf("profile, whole scenario: %d PC samples inside ti_run (share, SH-4 instructions per 68k instruction)", allSamples)
	for i, p := range top {
		if i == 30 {
			break
		}
		f := float64(p.v) / float64(allSamples)
		t.Logf("  %5.1f%% %6.1f  %s", 100*f, f*per, p.k)
	}
}
