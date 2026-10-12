//go:build probe

package cg50emu

// Probe (2026-10-10): ~/main/casio/graph_cg50 (FastGraph.g3a, a gint function grapher).
// Installs it on the 32 MB image, launches it, plays GRAPH_KEYS and saves the add-in's frames.
//   GRAPH_G3A   the .g3a (default ../../graph_cg50/FastGraph.g3a)
//   GRAPH_KEYS  comma list: "row-col" taps (re/KEYMAP.md inject coords), "wait:N" (N x 100 M
//               instructions), "shot"
//   frames: $TMPDIR/cg50_graph_NN.png (the whole 396x224 frame gint draws)
//   go -C emu_go test -tags probe -run TestGraph -count=1 -v .

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestGraph(t *testing.T) {
	path := os.Getenv("GRAPH_G3A")
	if path == "" {
		path = "../../graph_cg50/FastGraph.g3a"
	}
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no FastGraph.g3a: ", err)
	}
	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("FastGraph.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launchAddin(-1, "FastGraph")
	shot := 0
	save := func() {
		e.Step(50_000_000)
		saveAddinFrame(t, e, fmt.Sprintf("graph_%02d", shot))
		shot++
	}
	save()
	t.Logf("CPG: FRQCR %08x PLLCR %08x FLLFRQ %08x", e.mem.Read(0xA4150000, 4), e.mem.Read(0xA4150024, 4), e.mem.Read(0xA4150050, 4))
	for _, k := range strings.Split(os.Getenv("GRAPH_KEYS"), ",") {
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
