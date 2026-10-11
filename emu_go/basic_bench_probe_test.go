//go:build probe

package main

// Probe (2026-10-11, ~/main/casio/basic_cg50): Casio BASIC speed on the 32 MB dump — the OS's
// own PRGM app, and later C.Basic, running the same benchmark programs.
//
// The programs are tokenized by basic_cg50/tools/build_bench.py into bench/out/<NAME>.bin (MCS
// item data: 10 zero bytes, tokens, 0x00, padding) and written into main memory's "system"
// directory with the OS's MCSPutVar2 (0x154D), as the PRGM editor would store them. Names start
// with "A0".."A9", so they come first in the PRGM list.
//
//   BENCH_KEYS=row-col,...  explore: press keys after the programs are written (screenshot each)
//   TestBasicBench          time each program in the PRGM app: from EXE until the CPU sleeps
//                           continuously (the OS waits for a key after "Done"); emulated
//                           seconds = cycles / 70 M. The emulator is not cycle-accurate: compare
//                           ratios, and time the real calculator for absolute numbers.
//   go -C emu_go test -tags probe -run TestBasicBench -count=1 -v .
// Screenshots: $TMPDIR/cg50_bench_*.png.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const sysMCSPutVar2 = 0x154D // int MCSPutVar2(dir, item, len, buffer)

func benchDir() string {
	if d := os.Getenv("BENCH_DIR"); d != "" {
		return d
	}
	return "../../basic_cg50/bench/out"
}

// benchPrograms writes every bench/out/*.bin into MCS "system"; returns the names, sorted.
func benchPrograms(t *testing.T, e *Emulator) []string {
	files, _ := filepath.Glob(filepath.Join(benchDir(), "*.bin"))
	if len(files) == 0 {
		t.Skip("no benchmark programs: run basic_cg50/tools/build_bench.py")
	}
	sort.Strings(files)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.osIdle() {
		t.Fatal("OS not idle")
	}
	var names []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".bin")
		s := cvScratch(t, e, uint32(32+len(data)))
		for i, c := range []byte("system") {
			e.mem.W8(s+uint32(i), uint32(c))
		}
		for i, c := range []byte(name) {
			e.mem.W8(s+12+uint32(i), uint32(c))
		}
		for i, b := range data {
			e.mem.W8(s+32+uint32(i), uint32(b))
		}
		r, err := e.syscall(sysMCSPutVar2, codeviewBudget, s, s+12, uint32(len(data)), s+32)
		if err != nil || r != 0 {
			t.Fatalf("MCSPutVar2(%s) = %#x, %v", name, r, err)
		}
		names = append(names, name)
	}
	t.Logf("wrote %d programs into main memory: %v", len(names), names)
	// what is in "system" now (TMainMemoryDirectoryEntry: name[8], items, count u16)
	sd := cvScratch(t, e, 32)
	for i, c := range []byte("system") {
		e.mem.W8(sd+uint32(i), uint32(c))
	}
	if r, _ := e.syscall(sysMCSSearchDirectory, codeviewBudget, sd, sd+16, sd+20); r == 0 {
		dir := e.mem.R32(sd + 16)
		items, count := e.mem.R32(dir+8), e.mem.R16(dir+12)
		var have []string
		for i := uint32(0); i < count; i++ {
			it := items + 20*i
			// MCSPutVar2 stores a plain data item (flags d4...); the PRGM app lists only items
			// typed as programs (c7..., as the OS's own): set that byte on ours
			if strings.HasPrefix(string(cvString(e, it, 8)), "A") && e.mem.R8(it+16) == 0xD4 {
				for _, n := range names {
					if string(cvString(e, it, 8)) == n {
						e.mem.W8(it+16, 0xC7)
					}
				}
			}
			have = append(have, fmt.Sprintf("%s(%d,%02x%02x%02x%02x)", cvString(e, it, 8), e.mem.R32(it+12),
				e.mem.R8(it+16), e.mem.R8(it+17), e.mem.R8(it+18), e.mem.R8(it+19)))
		}
		if testing.Verbose() && os.Getenv("BENCH_LIST") != "" {
			t.Logf("system: %v", have)
		}
	}
	return names
}

func benchKey(t *testing.T, e *Emulator, row, col uint32, n int) {
	e.InjectKey(row, col)
	e.Step(n)
	if f := e.Fault(); f != "" {
		t.Fatal(f)
	}
}

func parseKeys(s string) [][2]uint32 {
	var keys [][2]uint32
	for _, k := range strings.Split(s, ",") {
		rc := strings.Split(strings.TrimSpace(k), "-")
		if len(rc) != 2 {
			continue
		}
		r, _ := strconv.Atoi(rc[0])
		c, _ := strconv.Atoi(rc[1])
		keys = append(keys, [2]uint32{uint32(r), uint32(c)})
	}
	return keys
}

// runUntilIdle steps until the CPU has slept at `need` samples in a row (one sample per
// 200,000 cycles), and returns the cycles from the start to the first of them.
func runUntilIdle(t *testing.T, e *Emulator, maxCycles uint64) (uint64, bool) {
	const chunk, need = 200_000, 25
	start := e.cpu.cycles
	run, first := 0, uint64(0)
	for e.cpu.cycles-start < maxCycles {
		e.Step(chunk)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		if e.cpu.sleeping {
			if run == 0 {
				first = e.cpu.cycles
			}
			if run++; run >= need {
				return first - start, true
			}
		} else {
			run = 0
		}
	}
	return e.cpu.cycles - start, false
}

// cbasicStart: a fresh state with the programs in main memory and C.Basic (BENCH_CBASIC = its
// .g3a) installed and started from its MAIN MENU icon (the last one: LEFT from the first).
func cbasicStart(t *testing.T) (*Emulator, []string) {
	path := os.Getenv("BENCH_CBASIC")
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("BENCH_CBASIC: ", err)
	}
	e := installState(t)
	names := benchPrograms(t, e)
	if err := e.InstallAddin(filepath.Base(path), g3a); err != nil {
		t.Fatal(err)
	}
	benchKey(t, e, 2, 8, 30_000_000) // LEFT: the last icon
	e.InjectKey(2, 1)                // EXE
	for i := 0; i < 300 && e.cpu.pc != 0x00300000; i++ {
		e.Step(1_000_000)
	}
	e.Step(300_000_000)
	if f := e.Fault(); f != "" {
		t.Fatal(f)
	}
	return e, names
}

// TestCBasicBench: the same programs in C.Basic. BENCH_KEYS here = explore after the start.
func TestCBasicBench(t *testing.T) {
	e, names := cbasicStart(t)
	savePanel(t, e, "cbasic_00_start")
	if ks := os.Getenv("BENCH_KEYS"); ks != "" {
		for i, k := range parseKeys(ks) {
			benchKey(t, e, k[0], k[1], 60_000_000)
			savePanel(t, e, fmt.Sprintf("cbasic_explore_%02d", i+1))
		}
		return
	}
	// each program from a fresh state: DOWN to it, F1 (EXE), until C.Basic waits for a key
	tag := os.Getenv("BENCH_TAG") // e.g. "int" with BENCH_DIR=.../out-int
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			e, _ = cbasicStart(t)
		}
		for k := 0; k < i; k++ {
			benchKey(t, e, 2, 7, 30_000_000) // DOWN
		}
		e.InjectKey(6, 9) // F1: EXE
		cyc, done := runUntilIdle(t, e, 70_000_000*600)
		savePanel(t, e, "cbasic_"+tag+name)
		t.Logf("%-8s %8.3f s emulated (%d cycles)%s", name, float64(cyc)/70e6, cyc, map[bool]string{false: "  NOT FINISHED"}[done])
		fmt.Fprintf(&b, "%s\t%d\t%v\n", name, cyc, done)
	}
	os.WriteFile(filepath.Join(os.TempDir(), "cg50_bench_cbasic"+tag+".tsv"), []byte(b.String()), 0o644)
}

func TestBasicBench(t *testing.T) {
	e := installState(t)
	names := benchPrograms(t, e)
	if ks := os.Getenv("BENCH_KEYS"); ks != "" {
		for i, k := range parseKeys(ks) {
			benchKey(t, e, k[0], k[1], 60_000_000)
			savePanel(t, e, fmt.Sprintf("bench_explore_%02d", i+1))
		}
		return
	}
	// each program from a fresh state (after a graphics program EXIT goes to the text screen, not
	// the list: chaining runs desynchronises the keys): PRGM (MAIN MENU key B = log), DOWN to it, F1
	type result struct {
		name   string
		cycles uint64
		done   bool
	}
	var res []result
	for i, name := range names {
		if i > 0 {
			e = installState(t)
			benchPrograms(t, e)
		}
		benchKey(t, e, 5, 6, 60_000_000)
		for k := 0; k < i; k++ {
			benchKey(t, e, 2, 7, 30_000_000) // DOWN
		}
		e.InjectKey(6, 9) // F1: EXE
		cyc, done := runUntilIdle(t, e, 70_000_000*600)
		savePanel(t, e, "bench_stock_"+name)
		res = append(res, result{name, cyc, done})
		t.Logf("%-8s %8.3f s emulated (%d cycles)%s", name, float64(cyc)/70e6, cyc, map[bool]string{false: "  NOT FINISHED"}[done])
	}
	var b strings.Builder
	for _, r := range res {
		fmt.Fprintf(&b, "%s\t%d\t%v\n", r.name, r.cycles, r.done)
	}
	os.WriteFile(filepath.Join(os.TempDir(), "cg50_bench_stock.tsv"), []byte(b.String()), 0o644)
}
