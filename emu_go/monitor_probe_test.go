//go:build probe

package main

// Probe (2026-10-04, on-device debugger stage 2): can a UBC breakpoint armed while the OS sits
// in the MAIN MENU stop an add-in at its first instruction? The harness powers the UBC on,
// points DBR at a stub in the free DRAM 0x8C4E0000 (stub: CBR0 = 0, rte), arms channel 0
// BEFORE 0x00300000, launches DASM from its icon and logs every write the OS makes to
// MSTPCR0, the UBC and DBR on the way.
//   go -C emu_go test -tags probe -run TestMonitorLaunchBreak -count=1 -v .

import (
	"os"
	"testing"
)

func TestMonitorLaunchBreak(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(20_000_000)

	const stub = 0x8C4E0000
	t.Logf("at the menu: MSTPCR0=%#x DBR=%#x CBCR=%#x CBR0=%#x", e.mem.Read(0xA4150030, 4),
		e.cpu.dbr, e.mem.Read(ubcBase+ubcCBCR, 4), e.mem.Read(ubcBase, 4))
	for i, op := range []uint32{0xD002, 0xE100, 0x2012, 0x002B, 0x0009, 0x0009} {
		e.mem.W16(stub+uint32(2*i), op) // mov.l @(0xC),r0; mov #0,r1; mov.l r1,@r0; rte; nop
	}
	e.mem.W32(stub+0xC, ubcBase)
	e.mem.Write(0xA4150030, 4, e.mem.Read(0xA4150030, 4)&^mstpcr0UDB)
	e.cpu.dbr = stub
	e.mem.Write(ubcBase+ubcCBCR, 4, 1)
	fetchBreak(e.mem, 0, 0x00300000, false)

	e.mem.mmioHook = func(va, size, val uint32) {
		if va == 0xA4150030 || va&0xFFFFF000 == ubcBase {
			t.Logf("  OS write %#x <- %#x (pc %#x)", va, val, e.cpu.pc)
		}
	}
	dbr := e.cpu.dbr
	var spc, ssr, r15 uint32
	hit := false
	for _, k := range [][2]uint32{{2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {1, 7}, {1, 7}} {
		e.InjectKey(k[0], k[1])
		e.Step(30_000_000)
	}
	e.InjectKey(2, 1) // EXE: launch DASM (icon Z)
	for i := 0; i < 300_000_000 && !hit; i++ {
		e.Step(1)
		if e.cpu.dbr != dbr {
			t.Logf("  DBR changed %#x -> %#x (pc %#x)", dbr, e.cpu.dbr, e.cpu.pc)
			dbr = e.cpu.dbr
		}
		if e.cpu.pc == stub {
			hit, spc, ssr, r15 = true, e.cpu.spc, e.cpu.ssr, e.cpu.sgr
		}
		if e.Fault() != "" {
			t.Fatal(e.Fault())
		}
	}
	e.mem.mmioHook = nil
	if !hit {
		t.Fatalf("no break: pc=%#x MSTPCR0=%#x CBR0=%#x DBR=%#x breaks=%d", e.cpu.pc,
			e.mem.Read(0xA4150030, 4), e.mem.Read(ubcBase, 4), e.cpu.dbr, e.cpu.ubc.breaks)
	}
	t.Logf("BREAK: spc=%#x ssr=%#x r15=%#x vbr=%#x MMUCR=%#x PTEH=%#x", spc, ssr, r15, e.cpu.vbr,
		e.mem.Read(0xFF000010, 4), e.mem.Read(0xFF000000, 4))
	e.Step(100_000_000) // the stub disarmed channel 0 and returned: DASM must run on
	if e.cpu.ubc.breaks != 1 || e.Fault() != "" {
		t.Fatalf("breaks=%d fault=%q", e.cpu.ubc.breaks, e.Fault())
	}
	savePNG(t, e, "monitor_after")
}
