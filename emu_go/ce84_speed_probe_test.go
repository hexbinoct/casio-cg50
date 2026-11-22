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
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const c8FlatOut, c8BusyMs, c8LastBusy, c8KeyDownMs, c8JobCount, c8JobUs, c8JobEmuMs, c8JobBusy = 42, 43, 44, 45, 46, 47, 48, 49

// the JIT's counters (ce_cg50 core/jit.c jit_stat, 2026-11-08): words 50-57
const c8Jit = 50

// the reference: build/cespeed <CE ROM> (CEmu 7c4aa0b, rev I). 2026-11-08: the job
// end ignores busy runs under 30 ms after 20 idle ms (the cursor blink; CE84 has the same rule),
// before: basloop 4274, gfx 1270, logic 2919
var c8SpeedRef = map[string]float64{"graph": 4997, "zoom6": 5028, "basloop": 3944, "gfx": 1265, "logic": 2253}

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
	if os.Getenv("CE84_ROM") == "" {
		t.Skip("set CE84_ROM to a TI-84 Plus CE ROM file")
	}
	rom, err := os.ReadFile(os.Getenv("CE84_ROM"))
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
	only := os.Getenv("CE84_SPEED_JOBS") // e.g. "gfx,logic": only these jobs
	job := func(name string, setup []string, goKey string) {
		if only != "" && !strings.Contains(","+only+",", ","+name+",") {
			return
		}
		s.tap(setup...)
		s.secs(1)
		// more idle time before the job's key (default 0.5 s: with the JIT, GFX ends so early in
		// CG50 time that the LOGIC launch once met a still-held key and never ran; 2026-11-08)
		gap := 0.5
		if g, err := strconv.ParseFloat(os.Getenv("CE84_SPEED_GAP"), 64); err == nil {
			gap = g
		}
		s.secs(gap)
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
		trace := os.Getenv("CE84_SPEED_TRACE") != ""
		traceDiv := uint64(20)
		if v, err := strconv.Atoi(os.Getenv("CE84_SPEED_TRACE")); err == nil && v > 1 {
			traceDiv = uint64(v)
		}
		var cpuSym uint32
		if trace {
			if f, err := elf.Open(env("CE84_ELF", "../../ce_cg50/ce84/build-cg/ce84")); err == nil {
				syms, _ := f.Symbols()
				for _, sy := range syms {
					if sy.Name == "_cpu" {
						cpuSym = uint32(sy.Value)
					}
				}
				f.Close()
			}
		}
		lastTrace := s.cycles()
		for c8Word(e, c8JobCount) == n0 {
			if trace && cpuSym != 0 && s.cycles()-lastTrace > c8IPS/traceDiv {
				lastTrace = s.cycles()
				// CEmu's cpu.registers at _cpu (add-in RAM 0x081xxxxx = physical 0x0C160000 + offset)
				phys := cpuSym - 0x08100000 + 0x0C160000 - DramBase
				pc := binary.BigEndian.Uint32(e.mem.dram[phys+48:]) & 0xFFFFFF
				t.Logf("    %6.0f ms: CE PC %06X SP %06X emu %d ms busy %d jit blocks %d inval %d", float64(s.cycles()-t0)/c8IPS*1000,
					pc, binary.BigEndian.Uint32(e.mem.dram[phys+44:])&0xFFFFFF, c8Word(e, c8EmuMs), c8Word(e, c8BusyMs),
					c8Word(e, c8Jit), c8Word(e, c8Jit+3))
			}
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
		var js [8]uint32
		for i := range js {
			js[i] = c8Word(e, c8Jit+i)
		}
		t.Logf("  jit (totals): %d blocks, %d none, %d retries, %d invalidations, %d flushes, %d pages refused, %d code bytes, %d requests",
			js[0], js[1], js[2], js[3], js[4], js[5], js[6], js[7])
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
		if d := os.Getenv("CE84_SPEED_JITDUMP"); d != "" {
			// the JIT's DRAM (CG50_JIT_ADDR 0x8C772000 .. 0x8C800000: code bits, then code)
			os.WriteFile(fmt.Sprintf("%s.%s.bin", d, name), e.mem.dram[0x0C772000-DramBase:0x0C800000-DramBase], 0o644)
		}
		if d := os.Getenv("CE84_SPEED_DUMP"); d != "" {
			// CE RAM (physical 0x0C600000, 0x65800 bytes) after the job
			os.WriteFile(fmt.Sprintf("%s.%s.bin", d, name), e.mem.dram[0x0C600000-DramBase:][:0x65800], 0o644)
		}
		if name == "logic" {
			// LOGIC copies its entity table to plotSScreen (D52C00) at the end: check it against
			// make_8xp.py's model (CE RAM is at physical 0x0C600000)
			out, err := exec.Command("python3", "-c", "import sys; sys.path.insert(0, '../../ce_cg50/bench'); "+
				"from make_8xp import logic_model; print(logic_model(40000).hex())").Output()
			if err != nil {
				t.Errorf("logic_model: %v", err)
			} else {
				want := strings.TrimSpace(string(out))
				got := hex.EncodeToString(e.mem.dram[0x0C600000+0x52C00-DramBase:][:len(want)/2])
				if got != want {
					t.Errorf("LOGIC's result differs from the model:\n got  %s\n want %s", got, want)
				} else {
					t.Logf("  LOGIC's result equals the model")
				}
			}
		}
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
