package sh4

// Differential tests against DASM's own C code: they build testdata/dasmref.c with DASM's
// src/ (set DASM_SRC to the src/ directory of a github.com/hexbinoct/cg50-dasm checkout) and the
// host C compiler, run it and compare its output with the Go port line by line. Skipped when
// DASM_SRC is unset; the image comparisons also need a flash dump (see osDumps).

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

var (
	refOnce sync.Once
	refPath string
	refErr  error
)

func dasmRef(t testing.TB) string {
	t.Helper()
	src := os.Getenv("DASM_SRC")
	if src == "" {
		t.Skip("DASM_SRC not set (the src/ directory of DASM, github.com/hexbinoct/cg50-dasm)")
	}
	refOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dasmref")
		if err != nil {
			refErr = err
			return
		}
		cc := os.Getenv("CC")
		if cc == "" {
			cc = "cc"
		}
		refPath = filepath.Join(dir, "dasmref")
		cmd := exec.Command(cc, "-O2", "-I", src, filepath.Join("testdata", "dasmref.c"),
			filepath.Join(src, "sh4dec.c"), filepath.Join(src, "analysis.c"), "-o", refPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			refErr = fmt.Errorf("building dasmref: %v\n%s", err, out)
		}
	})
	if refErr != nil {
		t.Fatal(refErr)
	}
	return refPath
}

func runRef(t testing.TB, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(dasmRef(t), args...).Output()
	if err != nil {
		t.Fatalf("dasmref %v: %v", args, err)
	}
	return out
}

// diffLines compares two line lists, reporting the first differences; returns the count.
func diffLines(t *testing.T, what string, c, g []byte) int {
	t.Helper()
	cl := strings.Split(strings.TrimRight(string(c), "\n"), "\n")
	gl := strings.Split(strings.TrimRight(string(g), "\n"), "\n")
	if len(cl) != len(gl) {
		t.Errorf("%s: C has %d lines, Go %d", what, len(cl), len(gl))
	}
	n := 0
	for i := 0; i < len(cl) && i < len(gl); i++ {
		if cl[i] != gl[i] {
			if n < 20 {
				t.Errorf("%s line %d:\n  C  %s\n  Go %s", what, i, cl[i], gl[i])
			}
			n++
		}
	}
	return n
}

func TestDASMWords(t *testing.T) {
	c := runRef(t, "words", fmt.Sprint(wordTablePC))
	if n := diffLines(t, "words", c, wordTable()); n != 0 {
		t.Fatalf("%d of 65536 words differ", n)
	}
}

func TestDASMDSP(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	c := runRef(t, "dsp")
	if n := diffLines(t, "dsp", c, dspHashes()); n != 0 {
		t.Fatalf("%d of 1024 DSP first words differ (over all 65536 second words each)", n)
	}
}

// TestDASMImage decodes every even address of each dump's OS part in C and in Go.
func TestDASMImage(t *testing.T) {
	ref := dasmRef(t)
	for _, d := range osDumps(t) {
		t.Run(d.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "os*.bin")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			f.Write(d.data[:OSCodeSize])
			f.Close()
			img := NewImage(ROMBase, d.data[:OSCodeSize])
			cmd := exec.Command(ref, "image", f.Name(), fmt.Sprint(ROMBase), fmt.Sprint(ROMBase),
				fmt.Sprint(ROMBase+OSCodeSize))
			out, _ := cmd.StdoutPipe()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			sc := bufio.NewScanner(bufio.NewReaderSize(out, 1<<20))
			pc, diffs, lines := uint32(ROMBase), 0, 0
			kinds := map[Kind]int{}
			for sc.Scan() {
				in, _ := img.Decode(pc)
				kinds[in.Kind]++
				checkKindConsistency(t, &in)
				if g := refLine(&in); !bytes.Equal(sc.Bytes(), []byte(g)) {
					if diffs < 20 {
						t.Errorf("C  %s\nGo %s", sc.Text(), g)
					}
					diffs++
				}
				pc += 2
				lines++
			}
			cmd.Wait()
			if lines != OSCodeSize/2 {
				t.Errorf("C printed %d lines, want %d", lines, OSCodeSize/2)
			}
			t.Logf("%s (%s): %d addresses, %d differences; kinds %v", d.name, imageID(d.data), lines, diffs, kinds)
			if diffs != 0 {
				t.Fatalf("%d differences", diffs)
			}
		})
	}
}

// analysisCase is an image for the analysis comparisons, with DASM's source arguments.
type analysisCase struct {
	name string
	file []byte // what DASM opens (the whole dump or .g3a)
	img  *Image
	opt  Options
	args []string // base entry rom
}

func analysisCases(t *testing.T) []analysisCase {
	var cs []analysisCase
	if os.Getenv("CG50_FLASH") != "" || fileExists(filepath.Join("..", "..", "os", "flash_dump", "flash_full.bin")) {
		for _, d := range osDumps(t) {
			img := NewImage(ROMBase, d.data)
			opt := OSOptions(img, FindSyscalls(img))
			opt.VBR, opt.P2Mirror = false, false // DASM does neither
			cs = append(cs, analysisCase{d.name, d.data, img, opt,
				[]string{fmt.Sprint(ROMBase), fmt.Sprint(ROMBase), "1"}})
		}
	}
	if os.Getenv("SH4_ADDINS") != "" {
		for _, d := range addinFiles(t) {
			img, err := G3AImage(d.data)
			if err != nil {
				t.Fatalf("%s: %v", d.name, err)
			}
			cs = append(cs, analysisCase{d.name, d.data, img, AddinOptions(img, nil),
				[]string{fmt.Sprint(AddinCode - G3AHeaderLen), fmt.Sprint(AddinCode), "0"}})
		}
	}
	if len(cs) == 0 {
		t.Skip("no flash dump and no SH4_ADDINS")
	}
	return cs
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func tempFile(t *testing.T, data []byte) string {
	f, err := os.CreateTemp("", "sh4img*.bin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	f.Write(data)
	f.Close()
	return f.Name()
}

func funcTable(an *Analysis) []byte {
	var b bytes.Buffer
	for _, f := range an.Funcs() {
		fmt.Fprintf(&b, "%08x %04x %04x\n", f.Addr, f.Size, f.Pool)
	}
	return b.Bytes()
}

// TestDASMFuncs: the function table of DASM's an_funcs_build and of Analyze.
func TestDASMFuncs(t *testing.T) {
	dasmRef(t)
	for _, c := range analysisCases(t) {
		t.Run(c.name, func(t *testing.T) {
			path := tempFile(t, c.file)
			cOut := runRef(t, append([]string{"funcs", path}, c.args...)...)
			an, err := Analyze(t.Context(), c.img, c.opt)
			if err != nil {
				t.Fatal(err)
			}
			if n := diffLines(t, "funcs", cOut, funcTable(an)); n != 0 {
				t.Fatalf("%d functions differ", n)
			}
			t.Logf("%s: %d functions, identical", c.name, an.NumFuncs())
		})
	}
}

// xrefTargets: a spread of function starts and the most referenced addresses.
func xrefTargets(an *Analysis) []uint32 {
	fs := an.Funcs()
	var ts []uint32
	for i := 0; i < len(fs); i += len(fs)/150 + 1 {
		ts = append(ts, fs[i].Addr)
	}
	count := map[uint32]int{}
	for _, x := range an.index().e {
		if x.kind != XrefData {
			count[x.target]++
		}
	}
	type tc struct {
		t uint32
		n int
	}
	var top []tc
	for t, n := range count {
		top = append(top, tc{t, n})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n || top[i].n == top[j].n && top[i].t < top[j].t })
	for i := 0; i < len(top) && i < 50; i++ {
		ts = append(ts, top[i].t)
	}
	return sortUniq(ts)
}

// TestDASMXrefs: DASM's an_xrefs for ~200 targets against the Go scan with DASM's limit of 64
// remembered pool words, and the index against the scan without that limit.
func TestDASMXrefs(t *testing.T) {
	dasmRef(t)
	for _, c := range analysisCases(t) {
		t.Run(c.name, func(t *testing.T) {
			an, err := Analyze(t.Context(), c.img, c.opt)
			if err != nil {
				t.Fatal(err)
			}
			targets := xrefTargets(an)
			args := append([]string{"xrefs", tempFile(t, c.file)}, c.args...)
			for _, x := range targets {
				args = append(args, fmt.Sprint(x))
			}
			cOut := runRef(t, args...)
			var g bytes.Buffer
			total, capDiff := 0, 0
			for _, x := range targets {
				s := an.xrefsScan(x, 64)
				fmt.Fprintf(&g, "target %08x %d\n", x, len(s))
				for _, r := range s {
					fmt.Fprintf(&g, "%08x %d\n", r.From, r.Kind)
				}
				total += len(s)
				idx := an.XrefsTo(x)
				if x == 0 || x == 0xFFFFFFFF {
					continue
				}
				full := an.xrefsScan(x, -1)
				if fmt.Sprint(idx) != fmt.Sprint(full) {
					t.Errorf("target %08x: index %v\nscan %v", x, idx, full)
				}
				if len(full) != len(s) {
					capDiff++
				}
			}
			if n := diffLines(t, "xrefs", cOut, g.Bytes()); n != 0 {
				t.Fatalf("%d xref lines differ", n)
			}
			t.Logf("%s: %d targets, %d references identical to DASM; index == scan; %d targets "+
				"where DASM's 64-pool limit adds data references", c.name, len(targets), total, capDiff)
		})
	}
}
