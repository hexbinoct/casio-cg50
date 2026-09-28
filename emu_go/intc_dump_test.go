//go:build probe

package main

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// Dump the INTC (0xA4080000) registers the OS had written by the time the menu snapshot
// was taken (values as stored by the stub: RMWs read 0, so fields written later win).
func TestDumpINTC(t *testing.T) {
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
	for _, r := range e.mmio.regions {
		x, ok := r.(*intcUnit)
		if !ok {
			continue
		}
		offs := make([]int, 0, len(x.regs))
		for k := range x.regs {
			offs = append(offs, int(k))
		}
		sort.Ints(offs)
		for _, k := range offs {
			t.Logf("INTC +%03x = %08x", k, x.regs[uint32(k)])
		}
	}
	// watch the OS's INTC writes for a while (menu idle + a key)
	n := 0
	e.mem.mmioHook = func(va, size, val uint32) {
		if va == 0xFF000028 && n < 40 {
			t.Logf("  INTEVT %03x accepted (pc %08x)", val, e.cpu.pc)
			n++
		}
		if va&^0xFFF == 0xA4080000 && n < 40 {
			t.Logf("  w%d INTC+%03x = %08x (pc %08x)", size*8, va&0xFFF, val, e.cpu.pc)
			n++
		}
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(5_000_000)
	e.InjectKey(1, 8)
	e.Step(20_000_000)

	// fresh boot: the OS's initial INTC programming
	t.Log("---- fresh boot ----")
	f := NewEmulator(flash)
	seen := map[string]int{}
	first := map[string]uint64{}
	f.mem.mmioHook = func(va, size, val uint32) {
		if va&^0xFFF == 0xA4080000 {
			k := fmt.Sprintf("w%d INTC+%03x = %08x pc %08x", size*8, va&0xFFF, val, f.cpu.pc)
			if seen[k] == 0 {
				first[k] = f.cpu.cycles
			}
			seen[k]++
		}
	}
	f.SetInstrPerSecond(70_000_000)
	f.Step(220_000_000)
	t.Logf("boot: pc=%08x", f.cpu.pc)
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return first[keys[i]] < first[keys[j]] })
	for _, k := range keys {
		t.Logf("  %-44s x%-5d first at %d", k, seen[k], first[k])
	}
}
