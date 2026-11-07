//go:build probe

package cg50emu

// TestCE84Speed (2026-11-04): the speed benchmark of CE84 (ce_cg50/ce84). The same five jobs as
// ce_cg50's bench/cespeed.c (CEmu on the Mac = a real CE): CE84 runs flat out (stats word
// flat_out) and times each job itself with its TMU (stats words job_*): from the key going
// down in the CE to the last emulated ms in which the CE's CPU was awake for more than half of
// it, followed by 300 emulated ms of idle. Speed = the reference ms (cespeed) / the CG50 ms.
//   CE84_G3A, CE84_ROM, CE84_OUT as TestCE84; CE84_SPEED_VARS = make_speed_vars.py's folder
//   CE84_SPEED_PROF=1: a PC profile per job (CE84_ELF, default ce84/build-cg/ce84)
//   CE84_SPEED_PCS=<prefix>: every sampled PC with its count, per job, to <prefix>.<job>.txt
//   go -C emu_go test -tags probe -run 'TestCE84Speed$' -count=1 -timeout 60m -v .

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

const c8FlatOut, c8BusyMs, c8LastBusy, c8KeyDownMs, c8JobCount, c8JobUs, c8JobEmuMs, c8JobBusy = 42, 43, 44, 45, 46, 47, 48, 49

// the reference: build/cespeed ../ti/roms/ti-84ce.rom (2026-11-04, CEmu 7c4aa0b, rev I)
var c8SpeedRef = map[string]float64{"graph": 4997, "zoom6": 5028, "basloop": 4274, "gfx": 1270, "logic": 2919}

func TestCE84Speed(t *testing.T) {
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	g3a, err := os.ReadFile(env("CE84_G3A", "../../ce_cg50/ce84/CE84.g3a"))
	if err != nil {
		t.Skip("no CE84.g3a: ", err)
	}
	rom, err := os.ReadFile(env("CE84_ROM", "../../ti/roms/ti-84ce.rom"))
	if err != nil {
		t.Skip("no ROM: ", err)
	}
	vars := env("CE84_SPEED_VARS", "../../ce_cg50/out/speed_vars")
	if b, err := exec.Command("python3", "../../ce_cg50/bench/make_speed_vars.py", vars).CombinedOutput(); err != nil {
		t.Skipf("make_speed_vars.py: %v\n%s", err, b)
	}
	out := env("CE84_OUT", "../../ce_cg50/out/ce84")
	os.MkdirAll(out, 0o755)

	var syms []cbSym
	if os.Getenv("CE84_SPEED_PROF") != "" {
		syms = cbSymbols(t, env("CE84_ELF", "../../ce_cg50/ce84/build-cg/ce84"))
	}
	e := installState(t)
	e.SetInstrPerSecond(c8IPS)
	s := &c8Session{t, e, out}
	if err := e.InstallFile("TI84CE.rom", rom); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"BASLOOP.8xp", "GFX.8xp", "LOGIC.8xp"} {
		b, err := os.ReadFile(filepath.Join(vars, n))
		if err != nil {
			t.Fatal(err)
		}
		e.Step(20_000_000)
		if err := e.InstallFile(n, b); err != nil {
			t.Fatal(err)
		}
	}
	e.Step(20_000_000)
	if err := e.InstallAddin("CE84.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)

	s.launch()
	s.startup("start")
	s.until("TI-OS boot", 120, func() bool { return c8Word(e, c8EmuMs) >= 4000 })
	s.secs(1)
	// the three programs into the CE's RAM (the list: BASLOOP, GFX, LOGIC)
	s.pickerTo()
	s.tap("F1", "DOWN", "F1", "DOWN", "F1")
	s.sendWait("3 programs", "")
	if r := c8Word(e, c8SendResult); r != 1 {
		t.Fatalf("send: result %d, %q", r, c8Message(e))
	}
	s.tap("EXE")
	s.until("CE runs", 10, func() bool { return c8Word(e, c8Phase) == c8PhRun })
	cbSetWordAt(e, c8Stats, c8FlatOut, 1)
	s.secs(1)

	type res struct {
		name     string
		cg50, ce float64
	}
	var results []res
	job := func(name string, setup []string, goKey string) {
		s.tap(setup...)
		s.secs(1)
		c := c8Key[goKey]
		n0 := c8Word(e, c8JobCount)
		e.InjectKey(c[0], c[1])
		t0 := s.cycles()
		prof := map[string]int{}
		var pcs map[uint32]int
		if os.Getenv("CE84_SPEED_PCS") != "" {
			pcs = map[uint32]int{}
			defer func() {
				f, err := os.Create(fmt.Sprintf("%s.%s.txt", os.Getenv("CE84_SPEED_PCS"), name))
				if err != nil {
					t.Log(err)
					return
				}
				defer f.Close()
				for pc, n := range pcs {
					fmt.Fprintf(f, "%08x %d\n", pc, n)
				}
			}()
		}
		for c8Word(e, c8JobCount) == n0 {
			for k := 0; k < 50; k++ {
				e.Step(20_000)
				if syms != nil {
					prof[cbLookup(syms, e.cpu.pc)]++
				}
				if pcs != nil {
					pcs[e.cpu.pc]++
				}
			}
			if f := e.Fault(); f != "" {
				s.shot("speed_fault")
				t.Fatal(f)
			}
			if s.cycles()-t0 > 120*c8IPS {
				s.shot("speed_timeout")
				t.Fatalf("%s: not idle after 120 s", name)
			}
		}
		// the emulator's TMU (Pphi/4) counts every ips/rate instructions, an integer: at 61 M
		// that is 8 instead of 8.27, so its microseconds run 3.4 % fast; undo that
		rate := float64(TMUPphi / 4)
		cg := float64(c8Word(e, c8JobUs)) / 1000 * float64(uint64(c8IPS)/uint64(rate)) / (c8IPS / rate)
		ref := c8SpeedRef[name]
		t.Logf("%-8s CG50 %7.0f ms  CE ref %5.0f ms  speed %5.1f %%  (CE84 emulated %d ms, busy %d; probe %.0f ms incl. the idle tail)",
			name, cg, ref, 100*ref/cg, c8Word(e, c8JobEmuMs), c8Word(e, c8JobBusy), float64(s.cycles()-t0)/c8IPS*1000)
		results = append(results, res{name, cg, ref})
		if syms != nil {
			type kv struct {
				k string
				v int
			}
			var top []kv
			n := 0
			for k, v := range prof {
				top = append(top, kv{k, v})
				n += v
			}
			sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
			line := ""
			for i := 0; i < len(top) && i < 25; i++ {
				line += fmt.Sprintf(" %s %.1f%%", top[i].k, 100*float64(top[i].v)/float64(n))
			}
			t.Logf("  profile:%s", line)
		}
		s.shot("speed_" + name)
	}
	job("graph", []string{"F1", "EXIT", "sin", "XOT", ")"}, "F5")
	job("zoom6", []string{"F3"}, "6")
	job("basloop", []string{"SHIFT", "F6", "EXIT", "EXIT", "SD", "2"}, "EXE")
	job("gfx", []string{"EXIT", "SD", "3"}, "EXE")
	job("logic", []string{"EXIT", "SD", "4"}, "EXE")
	for _, r := range results {
		t.Logf("SPEED %-8s %5.1f %%", r.name, 100*r.ce/r.cg50)
	}
}

func cbSetWordAt(e *Emulator, base uint32, i int, v uint32) {
	off := base - DramBase + uint32(4*i)
	e.mem.dram[off], e.mem.dram[off+1], e.mem.dram[off+2], e.mem.dram[off+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

func init() {
	c8Key["F3"] = [2]uint32{4, 9}
	c8Key["6"] = [2]uint32{4, 3}
}
