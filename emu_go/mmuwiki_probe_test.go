//go:build probe

package cg50emu

// Facts for the wiki page os/mmu-and-addins.md. Run with
//   go -C emu_go test -tags probe -run TestMMUWikiLaunch -count=1 -v .
// From the menu save-state it launches the add-in on icon J (the ')' key) and logs, until the
// first instruction at 0x00300000: every MMU register write and ldtlb, the call chain (shadow
// stack) at the entry, the CPU state there, the UTLB, the page table 0x8C04CF0C, and which code
// wrote which RAM during the launch. Then it counts TLB misses while the add-in runs, presses
// MENU, and opens Run-Matrix, logging the MMU writes each time.

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

type wrange struct {
	lo, hi uint32
	bytes  int
}

func TestMMUWikiLaunch(t *testing.T) {
	e := mmuWikiEmu(t)
	ccn := e.mmio.ccn
	mmuLog := func(tag string, max int) (stop func() int) {
		n := 0
		e.mem.mmioHook = func(va, size, val uint32) {
			if va >= 0xFF000000 && va < 0xFF000040 && va != 0xFF000028 && va != 0xFF000024 {
				if n < max {
					t.Logf("  [%s] w%d %08x = %08x (pc %08x)", tag, size*8, va, val, e.cpu.pc)
				}
				n++
			}
		}
		return func() int { e.mem.mmioHook = nil; return n }
	}

	// --- A: the launch ---
	writes := map[uint32]map[string]*wrange{} // writer pc -> region -> range
	region := func(a uint32) string {
		switch {
		case a >= 0x0C160000 && a < 0x0C1E0000:
			return "addinRAM"
		case a >= 0x0C1E0000 && a < 0x0C4E0000:
			return "upper3MB"
		case a >= 0x0C000000 && a < 0x0C800000:
			return "DRAM"
		case a >= 0xFD800000 && a < 0xFD810000:
			return "ILRAM"
		case a >= 0xFE200000 && a < 0xFE400000:
			return "FE2"
		}
		return ""
	}
	e.mem.accHook = func(a, size uint32, write bool) {
		if !write {
			return
		}
		r := region(a)
		if r == "" {
			return
		}
		m := writes[e.cpu.pc]
		if m == nil {
			m = map[string]*wrange{}
			writes[e.cpu.pc] = m
		}
		w := m[r]
		if w == nil {
			w = &wrange{lo: a, hi: a + size}
			m[r] = w
		}
		w.lo, w.hi, w.bytes = min(w.lo, a), max(w.hi, a+size), w.bytes+int(size)
	}
	stopLog := mmuLog("launch", 60)
	var sh shadow
	ld := 0
	e.InjectKey(3, 5) // ')' = icon J
	entered := false
	var prev uint32
	for i := 0; i < 80_000_000; i++ {
		pc := e.cpu.pc
		if pc == 0x00300000 {
			t.Logf("ENTRY after %d instructions, from pc %08x", i, prev)
			entered = true
			break
		}
		if pc >= 0x80000000 {
			if e.mem.R16(pc) == 0x0038 {
				if ld < 30 {
					t.Logf("  ldtlb @%08x slot=%d PTEH=%08x PTEL=%08x", pc, (ccn.regs[ccnMMUCR]>>10)&0x3F,
						ccn.regs[ccnPTEH], ccn.regs[ccnPTEL])
				}
				ld++
			}
			prev = pc
			sh.step(e)
		} else {
			prev = pc
			e.Step(1)
		}
		if e.Fault() != "" {
			t.Fatal(e.Fault())
		}
	}
	t.Logf("MMU register writes during launch: %d, ldtlb: %d", stopLog(), ld)
	e.mem.accHook = nil
	if !entered {
		t.Fatal("never reached 0x00300000")
	}
	t.Logf("call chain at entry (innermost first): %s", sh.sig(20))
	t.Logf("SR=%08x VBR=%08x r15=%08x PR=%08x MMUCR=%08x", e.cpu.sr, e.cpu.vbr, e.cpu.r[15], e.cpu.pr, ccn.regs[ccnMMUCR])
	dumpUTLB := func(tag string) {
		for i := range ccn.utlb {
			u := &ccn.utlb[i]
			if u.valid() {
				t.Logf("  [%s] slot %2d VPN %08x ASID %02x -> PPN %08x size %#x PR %d C %d D %d SH %d", tag, i, u.vpn,
					u.asid, u.ppn(), u.mask()+1, u.pr(), (u.data>>3)&1, (u.data>>2)&1, (u.data>>1)&1)
			}
		}
	}
	dumpUTLB("entry")
	tbl := func(tag string) {
		nz, first := 0, ""
		for k := uint32(0); k < 512; k++ {
			if v := e.mem.R32(0x8C04CF0C + 4*k); v != 0 {
				nz++
				if nz <= 4 {
					first += fmt.Sprintf(" [%d]=%08x", k, v)
				}
			}
		}
		t.Logf("[%s] page table: %d non-zero of 512 words;%s; counter 0x8C04D70C=%d; bytes CEE0=%d CEE1=%d",
			tag, nz, first, e.mem.R32(0x8C04D70C), e.mem.R8(0x8C04CEE0), e.mem.R8(0x8C04CEE1))
	}
	tbl("entry")
	pcs := make([]uint32, 0, len(writes))
	for pc := range writes {
		pcs = append(pcs, pc)
	}
	sort.Slice(pcs, func(i, j int) bool { return pcs[i] < pcs[j] })
	for _, pc := range pcs {
		for r, w := range writes[pc] {
			if w.bytes >= 4096 || r == "addinRAM" || r == "ILRAM" {
				t.Logf("  writer %08x %-8s %08x-%08x %d bytes", pc, r, w.lo, w.hi-1, w.bytes)
			}
		}
	}

	// --- B: the add-in runs; count TLB misses ---
	misses, teas := 0, ""
	for i := 0; i < 40_000_000; i++ {
		if e.cpu.pc == 0x8002C918 {
			misses++
			if misses <= 6 {
				teas += fmt.Sprintf(" %08x", ccn.regs[ccnTEA])
			}
		}
		e.Step(1)
		if e.Fault() != "" {
			t.Fatal(e.Fault())
		}
	}
	t.Logf("TLB-miss handler entries in 40M instructions: %d; first TEAs:%s", misses, teas)
	dumpUTLB("running")
	tbl("running")

	// --- C: MENU, then Run-Matrix ---
	stop := mmuLog("menu", 40)
	e.InjectKey(3, 8) // MENU
	e.Step(40_000_000)
	t.Logf("MENU: MMU writes %d, MMUCR=%08x AT=%v pc=%08x", stop(), ccn.regs[ccnMMUCR], ccn.at, e.cpu.pc)
	stop = mmuLog("runmat", 40)
	e.InjectKey(6, 2) // '1' = Run-Matrix
	e.Step(60_000_000)
	t.Logf("Run-Matrix: MMU writes %d, MMUCR=%08x AT=%v pc=%08x", stop(), ccn.regs[ccnMMUCR], ccn.at, e.cpu.pc)
	dumpUTLB("runmat")
	tbl("runmat")
	savePNG(t, e, "mmuwiki_runmat")
}

// mmuWikiEmu resumes the 16 MB dump's menu save-state (skips without it).
func mmuWikiEmu(t *testing.T) *Emulator {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no menu save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	return e
}

// TestMMUWikiClears: during the same launch, every memset (0x80385178) of 4 KB or more, the
// 3 MB clear at 0x80362FD6, flash data reads of the add-in's two code pages, and how much of the
// add-in RAM is non-zero when the add-in starts.
func TestMMUWikiClears(t *testing.T) {
	e := mmuWikiEmu(t)
	codeReads := 0
	e.mem.flashRdHook = func(phys, size uint32) {
		if phys >= 0x00DBB000 && phys < 0x00DBD000 {
			codeReads++
		}
	}
	t.Logf("r15 at the MAIN MENU before the key: %08x", e.cpu.r[15])
	e.InjectKey(3, 5)
	sp := e.cpu.r[15]
	for i := 0; i < 80_000_000 && e.cpu.pc != 0x00300000; i++ {
		if d := int64(e.cpu.r[15]) - int64(sp); d > 0x10000 || d < -0x10000 {
			t.Logf("  stack moves %08x -> %08x at pc %08x", sp, e.cpu.r[15], e.cpu.pc)
		}
		sp = e.cpu.r[15]
		switch e.cpu.pc {
		case 0x80385178:
			if e.cpu.r[6] >= 0x1000 {
				t.Logf("  memset(%08x, %#x, %#x) from %08x", e.cpu.r[4], e.cpu.r[5], e.cpu.r[6], e.cpu.pr-4)
			}
		case 0x80362FD6:
			t.Logf("  3 MB clear from 0x8C1E0000 (called from %08x)", e.cpu.pr-4)
		case 0x801DF4A4:
			t.Logf("  IL RAM flash-routine copy (called from %08x)", e.cpu.pr-4)
		case 0x803639A8:
			t.Logf("  app runner 0x803639A8(r4=%#x, r5=%#x, r6=%#x) from %08x", e.cpu.r[4], e.cpu.r[5], e.cpu.r[6], e.cpu.pr-4)
		case 0x8003083C:
			t.Logf("  stack reset to 0x8C1E0000 (from %08x)", e.cpu.pr-4)
		}
		e.Step(1)
	}
	e.mem.flashRdHook = nil
	t.Logf("at entry pc=%08x r4=%#x r5=%#x; data reads of the code pages during launch: %d", e.cpu.pc, e.cpu.r[4], e.cpu.r[5], codeReads)
	for pg := uint32(0); pg < 8; pg++ {
		nz := 0
		for a := uint32(0); a < 0x10000; a += 4 {
			if e.mem.R32(0xAC160000+pg*0x10000+a) != 0 {
				nz++
			}
		}
		t.Logf("  add-in RAM 64 KB page %d (phys %08x): %d non-zero words", pg, 0x0C160000+pg*0x10000, nz)
	}
}
