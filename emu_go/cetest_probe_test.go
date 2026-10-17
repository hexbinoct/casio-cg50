//go:build probe

package cg50emu

// Probe (2026-10-16): ~/main/casio/ce_cg50/addin_test (CEtest.g3a: the TI-84 Plus CE fast eZ80
// path checked against CEmu's decoder, one instruction at a time, ce_cg50/core/difftest_core.c,
// the same test as the Mac's build/difftest). Installs the add-in on the 32 MB image, launches it,
// writes the parameters into its control block (physical 0x0C1E0000, ce_cg50/addin_test/src/
// main.c, cetest_ctl_t) and the go word, runs until the add-in sets done, and logs the report.
//   CETEST_G3A     the .g3a (default ../../ce_cg50/addin_test/CEtest.g3a)
//   CETEST_CASES   random states per opcode (default 50: ~3 min here)
//   CETEST_SEED    seed (default 1; 0 = the Mac program's default without a seed argument)
//   CETEST_NOHOT=1 fast_hot_enabled = 0 (fast_run's own handlers instead of the hot loop's)
//   CETEST_FULL=1  compare all of RAM per case (the reference method; very slow here)
//   CETEST_CHUNK   window mode: opcodes per all-of-RAM check (default 16)
//   CETEST_EXPECT  a file with the Mac's output (build/difftest <rom> <cases> <seed> > file):
//                  the report must equal it byte for byte
//   CETEST_MAX_G   give up after this many G SH-4 instructions (default 400)
//   go -C emu_go test -tags probe -run 'TestCEtest$' -count=1 -timeout 60m -v .

import (
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	ctBase  = 0x0C1E0000 // physical: CETEST_CTL_ADDR
	ctText  = 0x0C1E1000 // CETEST_TEXT_ADDR
	ctTextE = 0x0C200000
	ctMagic = 0x43455453 // "CETS"
	ctDoneW = 0x444F4E45 // "DONE"
	// word offsets in cetest_ctl_t
	ctPhase, ctGo, ctCases, ctSeedLo, ctSeedHi, ctFlags, ctChunk, ctTable, ctOp = 1, 2, 3, 4, 5, 6, 7, 8, 9
	ctTextLen, ctDone, ctFails, ctOks, ctHanded, ctSkipped, ctRunMs, ctLoadUs   = 10, 11, 12, 13, 14, 15, 16, 17
	ctChunks, ctReruns, ctTrunc                                                 = 18, 19, 20
	ctPhWait, ctPhRun, ctPhDone                                                 = 1, 3, 4
	ctFlNoHot, ctFlFull                                                         = 1, 2
)

var ctTables = []string{"base", "ED", "CB", "DD", "FD", "DDCB", "FDCB", ".SIS", ".SIL", ".LIS", ".LIL", ".SISED", ".SILED"}

func ctWord(e *Emulator, i int) uint32 {
	return binary.BigEndian.Uint32(e.mem.dram[ctBase-DramBase+4*i:])
}

func ctSetWord(e *Emulator, i int, v uint32) {
	binary.BigEndian.PutUint32(e.mem.dram[ctBase-DramBase+4*i:], v)
}

func ctEnvInt(name string, def uint64) uint64 {
	if v, err := strconv.ParseUint(os.Getenv(name), 0, 64); err == nil {
		return v
	}
	return def
}

func TestCEtest(t *testing.T) {
	g3aPath := os.Getenv("CETEST_G3A")
	if g3aPath == "" {
		g3aPath = "../../ce_cg50/addin_test/CEtest.g3a"
	}
	g3a, err := os.ReadFile(g3aPath)
	if err != nil {
		t.Skip("no CEtest.g3a: ", err)
	}
	cases := ctEnvInt("CETEST_CASES", 50)
	seed := ctEnvInt("CETEST_SEED", 1)
	chunk := ctEnvInt("CETEST_CHUNK", 16)
	maxInsn := ctEnvInt("CETEST_MAX_G", 400) * 1_000_000_000
	var flags uint32
	if os.Getenv("CETEST_NOHOT") == "1" {
		flags |= ctFlNoHot
	}
	if os.Getenv("CETEST_FULL") == "1" {
		flags |= ctFlFull
	}

	e := installState(t)
	s := &rcSession{t, e}
	if err := e.InstallAddin("CEtest.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	s.launch(-1)
	for n := 0; e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || e.Fault() != "" {
			t.Fatalf("CEtest never started (%s)", e.Fault())
		}
		e.Step(1)
	}
	launched := e.Executed()
	for ctWord(e, 0) != ctMagic || ctWord(e, ctPhase) != ctPhWait {
		if e.Fault() != "" || e.Executed()-launched > 2_000_000_000 {
			t.Fatalf("no control block (magic %08x phase %x, %s)", ctWord(e, 0), ctWord(e, ctPhase), e.Fault())
		}
		e.Step(100_000)
	}
	ctSetWord(e, ctCases, uint32(cases))
	ctSetWord(e, ctSeedLo, uint32(seed))
	ctSetWord(e, ctSeedHi, uint32(seed>>32))
	ctSetWord(e, ctFlags, flags)
	ctSetWord(e, ctChunk, uint32(chunk))
	ctSetWord(e, ctGo, 1)
	t.Logf("CEtest: %d cases per opcode, seed %d, flags %d (1 = no hot_run, 2 = all-RAM compare), chunk %d",
		cases, seed, flags, chunk)

	start, wall := e.Executed(), time.Now()
	var runStart uint64
	lastTable := ^uint32(0)
	for ctWord(e, ctDone) != ctDoneW {
		e.Step(20_000_000)
		if f := e.Fault(); f != "" {
			t.Fatalf("fault at table %d op %02X: %s", ctWord(e, ctTable), ctWord(e, ctOp), f)
		}
		if e.Executed()-start > maxInsn {
			t.Fatalf("gave up after %.1f G SH-4 instructions (table %d op %02X)", float64(e.Executed()-start)/1e9,
				ctWord(e, ctTable), ctWord(e, ctOp))
		}
		if ctWord(e, ctPhase) == ctPhRun && runStart == 0 {
			runStart = e.Executed()
			t.Logf("flash rebuild + reset: %.1f M SH-4 instructions", float64(runStart-start)/1e6)
		}
		if tb := ctWord(e, ctTable); tb != lastTable && tb < uint32(len(ctTables)) {
			lastTable = tb
			t.Logf("  table %-6s at %6.2f G SH-4, %5.1f s", ctTables[tb], float64(e.Executed()-start)/1e9,
				time.Since(wall).Seconds())
		}
	}
	insn := e.Executed() - runStart
	n := ctWord(e, ctTextLen)
	if n > ctTextE-ctText {
		n = ctTextE - ctText
	}
	report := string(e.mem.dram[ctText-DramBase : ctText-DramBase+n])
	for _, line := range strings.Split(strings.TrimRight(report, "\n"), "\n") {
		t.Log(line)
	}
	compared := ctWord(e, ctOks) + ctWord(e, ctFails)
	t.Logf("difftest_run: %.2f G SH-4 instructions (%.0f per case, %.0f per compared case), %d ms by the TMU, "+
		"≈ %.0f s on a real CG50 at 61 M SH-4/s; emulator %.1f s; window chunks %d, rerun %d",
		float64(insn)/1e9, float64(insn)/float64(compared+ctWord(e, ctHanded)+ctWord(e, ctSkipped)),
		float64(insn)/float64(compared), ctWord(e, ctRunMs), float64(insn)/61e6, time.Since(wall).Seconds(),
		ctWord(e, ctChunks), ctWord(e, ctReruns))
	if ctWord(e, ctTrunc) != 0 {
		t.Error("the report was truncated")
	}
	if p := os.Getenv("CETEST_EXPECT"); p != "" {
		want, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != report {
			t.Errorf("the report differs from %s", p)
		} else {
			t.Logf("the report equals %s byte for byte", p)
		}
	}
	if f := ctWord(e, ctFails); f != 0 || !strings.Contains(report, "TOTAL ok") {
		t.Errorf("%d failures", f)
	}
}
