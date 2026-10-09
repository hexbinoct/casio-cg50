//go:build probe

package main

// Probe (2026-10-08): the fx-CG50 build of FlowCE (~/main/casio/flowce_cg50/cg50), installed on
// the 32 MB image and launched. Frames go to $TMPDIR.
//   TestFlowCE      a fixed session (1/7, x^2 then F2 antiderivative), cg50_flowce_*.png
//                   (FLOWCE_G3A overrides the .g3a path)
//   TestFlowCEKeys  FLOWCE_KEYS: comma list of webui key names or raw row-col ("6-6" = X,θ,T,
//                   "5-9" = F2); "shot" saves the add-in's whole 396x224 frame, cg50_flowcek_NN.png
//   TestFlowCEBreak FLOWCE_KEYS typed, then single-steps after the last key until a FLOWCE_BP
//                   address (hex list) is hit: logs pc/pr, giac gens in r4/r5 (genString), or
//                   with FLOWCE_ARGS=1 the raw r4..r7 + stack args and FLOWCE_PEEK words;
//                   FLOWCE_RET=1 also runs to the return and decodes the gen returned via r2.
//   go -C emu_go test -tags probe -run 'TestFlowCEKeys$' -v -timeout 30m

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestFlowCE(t *testing.T) {
	path := os.Getenv("FLOWCE_G3A")
	if path == "" {
		path = "../../flowce_cg50/cg50/FlowCE.g3a"
	}
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no FlowCE.g3a: ", err)
	}
	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("FlowCE.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launchAddin(-1, "FlowCE")
	shot := func(name string) { savePNG(t, e, "flowce_"+name) }
	shot("00_start")
	var (
		kXOT, kPOW, k2, k1, k7 = [2]uint32{6, 6}, [2]uint32{4, 7}, [2]uint32{5, 2}, [2]uint32{6, 2}, [2]uint32{6, 4}
		kDIV, kEXE, kF2, kEXIT = [2]uint32{2, 3}, [2]uint32{2, 1}, [2]uint32{5, 9}, [2]uint32{3, 7}
		kRIGHT                 = [2]uint32{1, 7}
	)
	s.tap(k1, kDIV, k7)
	shot("01_typed_1_7")
	s.tap(kEXE)
	e.Step(500_000_000)
	shot("02_1_7")
	s.tap(kXOT, kPOW, k2, kRIGHT)
	shot("03_typed_x2")
	s.tap(kF2)
	shot("04_calculus_menu")
	s.tap(kEXE)
	e.Step(1_000_000_000)
	shot("05_antiderivative")
	s.tap(kEXIT)
	shot("06_after_exit")
}

// FLOWCE_KEYS: a comma list of webui key names (resolveKey), "shot" saves a frame.
func TestFlowCEKeys(t *testing.T) {
	g3a, err := os.ReadFile("../../flowce_cg50/cg50/FlowCE.g3a")
	if err != nil {
		t.Skip("no FlowCE.g3a: ", err)
	}
	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("FlowCE.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launchAddin(-1, "FlowCE")
	n := 0
	for _, k := range strings.Split(os.Getenv("FLOWCE_KEYS"), ",") {
		if k == "shot" {
			e.Step(300_000_000)
			saveAddinFrame(t, e, fmt.Sprintf("flowcek_%02d", n))
			n++
			continue
		}
		var r, c uint32
		if n, _ := fmt.Sscanf(k, "%d-%d", &r, &c); n == 2 && strings.Contains(k, "-") && len(k) > 1 {
			s.tap([2]uint32{r, c})
			continue
		}
		for _, c := range resolveKey(k) {
			s.tap(c)
		}
	}
}

// FLOWCE_BP: comma list of hex addresses; types FLOWCE_KEYS (no shots), then single-steps after
// the last key until one is hit, logging pc, pr and the text-range words on the stack.
func TestFlowCEBreak(t *testing.T) {
	g3a, err := os.ReadFile("../../flowce_cg50/cg50/FlowCE.g3a")
	if err != nil {
		t.Skip("no FlowCE.g3a: ", err)
	}
	bp := map[uint32]bool{}
	for _, a := range strings.Split(os.Getenv("FLOWCE_BP"), ",") {
		var v uint32
		if _, err := fmt.Sscanf(a, "%x", &v); err == nil {
			bp[v] = true
		}
	}
	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("FlowCE.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launchAddin(-1, "FlowCE")
	keys := strings.Split(os.Getenv("FLOWCE_KEYS"), ",")
	for _, k := range keys[:len(keys)-1] {
		for _, c := range resolveKey(k) {
			s.tap(c)
		}
	}
	var last [2]uint32
	if n, _ := fmt.Sscanf(keys[len(keys)-1], "%d-%d", &last[0], &last[1]); n != 2 {
		last = resolveKey(keys[len(keys)-1])[0]
	}
	e.InjectKey(last[0], last[1])
	hits := 0
	for n := 0; n < 2_000_000_000 && e.Fault() == ""; n++ {
		e.Step(1)
		if !bp[e.cpu.pc] {
			continue
		}
		t.Logf("hit %08x pr %08x r4 %08x r5 %08x sp %08x", e.cpu.pc, e.cpu.pr, e.cpu.r[4], e.cpu.r[5], e.cpu.r[15])
		var chain []string
		for a := e.cpu.r[15]; a < e.cpu.r[15]+0x400; a += 4 {
			w := e.mem.Read(a, 4)
			if w >= 0x00300000 && w < 0x00520000 && w&1 == 0 {
				chain = append(chain, fmt.Sprintf("%08x", w))
			}
		}
		t.Logf("stack code words: %s", strings.Join(chain, " "))
		dump := func(what string, a uint32, n uint32) {
			if !(a>>24 == 0x08 || a>>24 == 0x8c || (a >= 0x00300000 && a < 0x00600000)) {
				t.Logf("  %s @%08x: (not memory)", what, a)
				return
			}
			var b []string
			for i := uint32(0); i < n; i += 4 {
				b = append(b, fmt.Sprintf("%08x", e.mem.Read(a+i, 4)))
			}
			t.Logf("  %s @%08x: %s", what, a, strings.Join(b, " "))
		}
		if os.Getenv("FLOWCE_ARGS") != "" {
			for _, a := range strings.Split(os.Getenv("FLOWCE_PEEK"), ",") {
				var v uint32
				if _, err := fmt.Sscanf(a, "%x", &v); err == nil {
					t.Logf("  peek %08x = %d (%08x)", v, int32(e.mem.Read(v, 4)), e.mem.Read(v, 4))
				}
			}
			t.Logf("  args r4..r7 %d %d %d %d, stack %d %d", int32(e.cpu.r[4]), int32(e.cpu.r[5]), int32(e.cpu.r[6]), int32(e.cpu.r[7]), int32(e.mem.Read(e.cpu.r[15], 4)), int32(e.mem.Read(e.cpu.r[15]+4, 4)))
		} else {
			t.Logf("  gen r4 = %s | r5 = %s", genString(e, e.cpu.r[4], 4), genString(e, e.cpu.r[5], 4))
			dump("r4", e.cpu.r[4], 16)
		}
		if os.Getenv("FLOWCE_RET") != "" {
			ret, r2 := e.cpu.pr, e.cpu.r[2]
			for m := 0; m < 50_000_000 && e.cpu.pc != ret; m++ {
				e.Step(1)
			}
			t.Logf("  returned to %08x: *r2(%08x) = %s", e.cpu.pc, r2, genString(e, r2, 4))
			dump("*r2", r2, 16)
		}
		if hits++; hits >= 40 {
			break
		}
	}
	if f := e.Fault(); f != "" {
		t.Log("fault: ", f)
	}
}

// genString decodes a giac gen of the CG50 build (TICE layout, 32-bit): value word, subtype at +4,
// type at +5; _SYMB -> ref_symbolic {ref_count, sommet, feuille}; _VECT -> ref_vecteur
// {ref_count, vtable, data, size bytes, capacity}.
func genString(e *Emulator, a uint32, depth int) string {
	ok := func(p uint32) bool { return p>>24 == 0x08 || p>>24 == 0x8c || (p >= 0x00300000 && p < 0x00600000) }
	if !ok(a) {
		return fmt.Sprintf("<bad %08x>", a)
	}
	v := e.mem.Read(a, 4)
	w := e.mem.Read(a+4, 4)
	sub, typ := int8(w>>24), (w>>16)&0xff
	switch typ {
	case 0:
		return fmt.Sprintf("%d", int32(v))
	case 6:
		return fmt.Sprintf("idnt@%08x", v)
	case 7:
		if depth <= 0 || !ok(v) {
			return fmt.Sprintf("vect@%08x", v)
		}
		data, size := e.mem.Read(v+8, 4), e.mem.Read(v+12, 4)
		s := fmt.Sprintf("[sub%d n=%d/8:", sub, size)
		for i := uint32(0); i < size/8 && i < 6; i++ {
			s += " " + genString(e, data+8*i, depth-1)
		}
		return s + "]"
	case 8:
		if depth <= 0 || !ok(v) {
			return fmt.Sprintf("symb@%08x", v)
		}
		return fmt.Sprintf("symb(f@%08x %s)", e.mem.Read(v+4, 4), genString(e, v+8, depth-1))
	}
	return fmt.Sprintf("<type %d sub %d val %08x>", typ, sub, v)
}
