//go:build probe

package cg50emu

// Probe (2026-11-01) for ~/main/casio/codeview_cg50: screenshots of CodeView with chosen
// sources. Installs the add-in, writes Casio BASIC programs into main memory and files into
// the storage root, launches CodeView, plays CV_KEYS and saves the add-in's frames.
//   CV_G3A    the .g3a (default ../../codeview_cg50/CodeView.g3a)
//   CV_PROGS  a directory of <NAME>.bin program items (10-byte header, tokens, 0x00, padding:
//             basic_cg50/tools/tokenize.py), written to MCS "system" as PRGM programs
//   CV_FILES  comma list of host files to copy to \\fls0\ (e.g. .py scripts)
//   CV_KEYS   comma list: "row-col" taps, "wait:N" (N x 100 M instructions), "shot"
//   frames: $TMPDIR/cg50_codeview_NN.png (the whole 396x224 frame)
//   go -C emu_go test -tags probe -run TestCodeViewShots -count=1 -v .

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// cvPrograms writes every dir/*.bin into MCS "system" and types the items as programs.
func cvPrograms(t *testing.T, e *Emulator, dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.bin"))
	sort.Strings(files)
	if len(files) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.osIdle() {
		t.Fatal("OS not idle")
	}
	names := map[string]bool{}
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
		names[name] = true
	}
	sd := cvScratch(t, e, 32)
	for i, c := range []byte("system") {
		e.mem.W8(sd+uint32(i), uint32(c))
	}
	if r, _ := e.syscall(sysMCSSearchDirectory, codeviewBudget, sd, sd+16, sd+20); r == 0 {
		d := e.mem.R32(sd + 16)
		items, count := e.mem.R32(d+8), e.mem.R16(d+12)
		var have []string
		for i := uint32(0); i < count; i++ {
			it := items + 20*i
			n := string(cvString(e, it, 8))
			// MCSPutVar2 stores a plain data item (d4); the OS's own programs are c7
			if names[n] && e.mem.R8(it+16) == 0xD4 {
				e.mem.W8(it+16, 0xC7)
			}
			have = append(have, n)
		}
		t.Logf("system: %v", have)
	}
}

func TestCodeViewShots(t *testing.T) {
	path := os.Getenv("CV_G3A")
	if path == "" {
		path = "../../codeview_cg50/CodeView.g3a"
	}
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no CodeView.g3a: ", err)
	}
	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("CodeView.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	for _, f := range strings.Split(os.Getenv("CV_FILES"), ",") {
		if f == "" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.InstallFile(filepath.Base(f), data); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, e, filepath.Base(f), len(data)); string(got) != string(data) {
			for i := range data {
				if got[i] != data[i] {
					t.Logf("%s reads back different from byte %d: % x", f, i, got[i:min(i+16, len(got))])
					break
				}
			}
		}
	}
	e.Step(20_000_000)
	if d := os.Getenv("CV_PROGS"); d != "" {
		cvPrograms(t, e, d)
	}
	s.launchAddin(-1, "CodeView")
	shot := 0
	save := func() {
		e.Step(50_000_000)
		saveAddinFrame(t, e, fmt.Sprintf("codeview_%02d", shot))
		shot++
	}
	save()
	for _, k := range strings.Split(os.Getenv("CV_KEYS"), ",") {
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		var r, c, n uint32
		switch {
		case k == "":
		case k == "shot":
			save()
		case strings.HasPrefix(k, "wait:"):
			fmt.Sscanf(k, "wait:%d", &n)
			for i := uint32(0); i < n; i++ {
				e.Step(100_000_000)
			}
		default:
			if _, err := fmt.Sscanf(k, "%d-%d", &r, &c); err == nil {
				s.tap([2]uint32{r, c})
			}
		}
	}
	if f := e.Fault(); f != "" {
		t.Fatal(f)
	}
}
