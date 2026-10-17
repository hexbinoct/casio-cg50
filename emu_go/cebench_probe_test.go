//go:build probe

package cg50emu

// Probe (2026-10-15): ~/main/casio/ce_cg50/addin (CEbench.g3a: CEmu's TI-84 Plus CE core as a
// gint add-in). Installs it on the 32 MB image, launches it and runs CEBENCH_MS emulated CE
// milliseconds, reading the add-in's stats block at physical 0x0C1E0000 (ce_cg50/addin/src/
// main.c, cg50_stats_t) to get SH-4 instructions per eZ80 dispatch and per emulated ms, per
// emulated second. Executed() is split by the block's `phase` word (2 = inside emu_run, 3 =
// the add-in's own UI) at every chunk of ~4000 instructions; the PC at each chunk end is a
// profile sample, mapped to the add-in's ELF symbols.
//   CEBENCH_G3A  the .g3a   (default ../../ce_cg50/addin/CEbench.g3a)
//   CEBENCH_ELF  its ELF    (default ../../ce_cg50/addin/build-cg/cebench)
//   CEBENCH_MS   emulated ms to run (default 4000)
//   CEBENCH_OUT  where to save a CE-screen PNG after every emulated second (default: none)
//   go -C emu_go test -tags probe -run TestCEbench -count=1 -timeout 60m -v .

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
	"strconv"
	"testing"
)

const (
	cbStats = 0x0C1E0000 // physical: CG50_STATS_ADDR
	cbMagic = 0x4345424E // "CEBN"
	// word offsets in cg50_stats_t
	cbPhase, cbEmuMs, cbOpsFlash, cbOpsRAM, cbBli, cbHalt, cbRate, cbFrames = 1, 2, 3, 4, 5, 6, 7, 8
	cbCoreUs, cbUiUs, cbDrawMs, cbLoadUs                                    = 9, 10, 11, 12
	cbScenStep                                                              = 15
	cbPhCore, cbPhUI                                                        = 2, 3
)

func cbWord(e *Emulator, i int) uint32 {
	return binary.BigEndian.Uint32(e.mem.dram[cbStats-DramBase+4*i:])
}

func cbSetWord(e *Emulator, i int, v uint32) {
	binary.BigEndian.PutUint32(e.mem.dram[cbStats-DramBase+4*i:], v)
}

type cbSym struct {
	addr, size uint64
	name       string
}

func cbSymbols(t *testing.T, path string) []cbSym {
	f, err := elf.Open(path)
	if err != nil {
		t.Logf("no symbols (%v): profile by address only", err)
		return nil
	}
	defer f.Close()
	syms, _ := f.Symbols()
	var out []cbSym
	for _, s := range syms {
		if elf.ST_TYPE(s.Info) == elf.STT_FUNC || (elf.ST_TYPE(s.Info) == elf.STT_NOTYPE && s.Section != elf.SHN_UNDEF && s.Value >= 0x300000 && s.Value < 0x500000) {
			out = append(out, cbSym{s.Value, s.Size, s.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].addr < out[j].addr })
	return out
}

func cbLookup(syms []cbSym, pc uint32) string {
	if pc >= 0x80000000 {
		return fmt.Sprintf("OS %08x", pc&^0xFF)
	}
	i := sort.Search(len(syms), func(i int) bool { return syms[i].addr > uint64(pc) }) - 1
	if i < 0 {
		return fmt.Sprintf("? %08x", pc)
	}
	return syms[i].name
}

// cbSaveFrame writes the CE part (left 320 columns) of the frame gint last pushed.
func cbSaveFrame(t *testing.T, e *Emulator, path string) {
	const w, h, cw = 396, 224, 320
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
	t.Logf("saved %s", path)
}

func TestCEbench(t *testing.T) {
	g3aPath := os.Getenv("CEBENCH_G3A")
	if g3aPath == "" {
		g3aPath = "../../ce_cg50/addin/CEbench.g3a"
	}
	elfPath := os.Getenv("CEBENCH_ELF")
	if elfPath == "" {
		elfPath = "../../ce_cg50/addin/build-cg/cebench"
	}
	totalMs := uint32(4000)
	if v, err := strconv.Atoi(os.Getenv("CEBENCH_MS")); err == nil && v > 0 {
		totalMs = uint32(v)
	}
	outDir := os.Getenv("CEBENCH_OUT")
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no CEbench.g3a: ", err)
	}
	syms := cbSymbols(t, elfPath)

	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("CEbench.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launch(-1)
	for n := 0; e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || e.Fault() != "" {
			t.Fatalf("CEbench never started (%s)", e.Fault())
		}
		e.Step(1)
	}
	launched := e.Executed()
	// wait for the stats block and the first emulated ms
	for cbWord(e, 0) != cbMagic || cbWord(e, cbEmuMs) == 0 {
		if e.Fault() != "" || e.Executed()-launched > 2_000_000_000 {
			t.Fatalf("no stats block (magic %08x phase %x, %s)", cbWord(e, 0), cbWord(e, cbPhase), e.Fault())
		}
		e.Step(10_000)
	}
	cbSetWord(e, cbDrawMs, 0) // no drawing unless asked below
	t.Logf("start-up to the first emulated ms: %.1f M SH-4 instructions (flash rebuild + reset: %d us by the TMU)",
		float64(e.Executed()-launched)/1e6, cbWord(e, cbLoadUs))

	type snap struct {
		ms, disp, flash, bli, halt uint32
		core, ui                   uint64
	}
	take := func(core, ui uint64) snap {
		return snap{cbWord(e, cbEmuMs), cbWord(e, cbOpsFlash) + cbWord(e, cbOpsRAM), cbWord(e, cbOpsFlash),
			cbWord(e, cbBli), cbWord(e, cbHalt), core, ui}
	}
	var core, ui uint64
	// CEBENCH_PCS=<file>: every core PC sample, one hex address per line (for addr2line);
	// CEBENCH_PCS_STEP=<n>: only while the key scenario is at stats step n (7 = the graph)
	pcStep := -1
	if v := os.Getenv("CEBENCH_PCS_STEP"); v != "" {
		fmt.Sscan(v, &pcStep)
	}
	var pcDump *os.File
	if p := os.Getenv("CEBENCH_PCS"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		pcDump = f
	}
	// the key scenario's steps (stats word 15): SH-4 core instructions, emulated ms and busy ms per step
	type stepAcc struct {
		core     uint64
		ms, halt uint32
		started  bool
		ms0, h0  uint32
		core0    uint64
	}
	steps := map[uint32]*stepAcc{}
	var stepOrder []uint32
	lastStep := uint32(0xFFFFFFFF)
	prev := take(0, 0)
	t.Logf("%-12s %8s %8s %10s %10s %12s %10s %9s", "emu ms", "busy ms", "disp", "SH4 core M", "SH4/disp",
		"SH4/emu ms", "UI SH4 M", "%CE@45M")
	// profiles: boot (emulated ms < 2000) and after (TI-OS idle at the home screen)
	profs := [2]map[string]int{{}, {}}
	samples := [2]int{}
	perDisp := [2]float64{}
	var bootEnd snap
	rnd := rand.New(rand.NewSource(1))
	nextSec := (prev.ms/1000 + 1) * 1000
	all := prev
	for prev.ms < totalMs {
		before := e.Executed()
		e.Step(3000 + rnd.Intn(2000))
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		d := e.Executed() - before
		switch cbWord(e, cbPhase) {
		case cbPhCore:
			core += d
			w := 0
			if prev.ms >= 2000 {
				w = 1
			}
			profs[w][cbLookup(syms, e.PC())]++
			samples[w]++
			if pcDump != nil && (pcStep < 0 || int(cbWord(e, cbScenStep)) == pcStep) {
				fmt.Fprintf(pcDump, "%x\n", e.PC())
			}
		default:
			ui += d
		}
		if st := cbWord(e, cbScenStep); st != lastStep {
			now := cbWord(e, cbEmuMs)
			if a := steps[lastStep]; a != nil {
				a.core += core - a.core0
				a.ms += now - a.ms0
				a.halt += cbWord(e, cbHalt) - a.h0
			}
			a := steps[st]
			if a == nil {
				a = &stepAcc{}
				steps[st] = a
				stepOrder = append(stepOrder, st)
			}
			a.core0, a.ms0, a.h0 = core, now, cbWord(e, cbHalt)
			lastStep = st
		}
		if ms := cbWord(e, cbEmuMs); ms >= nextSec {
			cur := take(core, ui)
			disp := cur.disp - prev.disp
			c := float64(cur.core - prev.core)
			elapsed := float64(cur.ms - prev.ms)
			// the halted clocks at the CPU clock now (TI-OS sets 48 MHz early in the boot)
			busy := elapsed - float64(cur.halt-prev.halt)/float64(cbWord(e, cbRate))*1000
			pd := c / float64(disp)
			pct := 100 * (45e6 / c) * (elapsed / 1000) // emulated s per SH-4 s at 45 M instr/s
			t.Logf("%5d-%-6d %8.0f %8d %10.1f %10.1f %12.0f %10.1f %8.1f%%", prev.ms, cur.ms, busy, disp, c/1e6, pd,
				c/elapsed, float64(cur.ui-prev.ui)/1e6, pct)
			if outDir != "" {
				cbSetWord(e, cbDrawMs, 1)
				for f := cbWord(e, cbFrames); cbWord(e, cbFrames) == f; {
					e.Step(100_000)
				}
				cbSetWord(e, cbDrawMs, 0)
				cbSaveFrame(t, e, fmt.Sprintf("%s/cebench_%05dms.png", outDir, cur.ms))
			}
			prev = take(core, ui)
			if prev.ms >= 2000 && bootEnd.ms == 0 {
				bootEnd = prev
			}
			nextSec = (prev.ms/1000 + 1) * 1000
		}
	}
	if a := steps[lastStep]; a != nil {
		a.core += core - a.core0
		a.ms += cbWord(e, cbEmuMs) - a.ms0
		a.halt += cbWord(e, cbHalt) - a.h0
	}
	if len(stepOrder) > 1 {
		names := []string{"boot", "2+2 ENTER", "7 digits", "CATALOG", "hold DOWN 3 s", "clear", "Y1=sin(X) GRAPH",
			"ZOOM 6", "quit, 1 ENTER", "sin(Ans) ENTER", "ENTER x40"}
		t.Logf("%-18s %8s %8s %10s %12s %10s", "scenario step", "emu ms", "busy ms", "SH4 M", "SH4/busy ms", "%CE@61M")
		for _, st := range stepOrder {
			a := steps[st]
			name := fmt.Sprint(st)
			if int(st) < len(names) {
				name = names[st]
			} else if st == 100 {
				name = "done (idle)"
			}
			busy := float64(a.ms) - float64(a.halt)/float64(cbWord(e, cbRate))*1000
			perBusy := 0.0
			if busy > 0 {
				perBusy = float64(a.core) / busy
			}
			pct := 0.0
			if a.core > 0 {
				pct = 100 * (61e6 / float64(a.core)) * (float64(a.ms) / 1000)
			}
			t.Logf("%-18s %8d %8.0f %10.1f %12.0f %9.1f%%", name, a.ms, busy, float64(a.core)/1e6, perBusy, pct)
		}
	}
	end := take(core, ui)
	disp := end.disp - all.disp
	t.Logf("all %d-%d ms: %d dispatches (%d from flash), %d block iterations, core %.1f M SH-4 = %.1f per dispatch, %.0f per emulated ms",
		all.ms, end.ms, disp, end.flash-all.flash, end.bli-all.bli, float64(end.core-all.core)/1e6,
		float64(end.core-all.core)/float64(disp), float64(end.core-all.core)/float64(end.ms-all.ms))

	if bootEnd.ms != 0 {
		perDisp[0] = float64(bootEnd.core-all.core) / float64(bootEnd.disp-all.disp)
		perDisp[1] = float64(end.core-bootEnd.core) / float64(end.disp-bootEnd.disp)
		t.Logf("boot %d-%d ms: %.1f SH-4 per dispatch; after %d-%d ms: %.1f SH-4 per dispatch, %.0f per emulated ms",
			all.ms, bootEnd.ms, perDisp[0], bootEnd.ms, end.ms, perDisp[1],
			float64(end.core-bootEnd.core)/float64(end.ms-bootEnd.ms))
	}
	for w, name := range []string{"boot (emulated ms < 2000)", "after 2000 ms (idle at the home screen)"} {
		type kv struct {
			k string
			v int
		}
		var top []kv
		for k, v := range profs[w] {
			top = append(top, kv{k, v})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
		t.Logf("profile, %s: %d PC samples inside emu_run (share, SH-4 instructions per dispatch)", name, samples[w])
		for i, p := range top {
			if i == 25 {
				break
			}
			f := float64(p.v) / float64(samples[w])
			t.Logf("  %5.1f%% %6.1f  %s", 100*f, f*perDisp[w], p.k)
		}
	}
}
