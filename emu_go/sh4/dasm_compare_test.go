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
