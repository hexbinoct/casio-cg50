//go:build probe

package main

// Probe (2026-09-28): run DASM.g3a as it is really installed in fls0, on the full 32 MB dump
// resumed from its warm-boot save-state (see warm_boot_test.go / CG50_WARM in main.go):
//   CG50_FLASH=../os/flash_dump/flash_full_32mb.bin CG50_STATE=../os/flash_dump/cg50_state_32mb.bin \
//     CG50_WARM=1 go -C emu_go run . 600000000 30000 provision none
// DASM_KEYS is a comma list of row-col keys to press, one screenshot each (cg50_real_NN.png).
//
// DASM_LAUNCH=1 first starts DASM from its menu icon (Z: 8x DOWN, 2x RIGHT, EXE). With
// DASM_SWAP=<path to a DASM.g3a build> the code the OS maps is replaced by that build the moment
// the add-in starts (its pages go to erased flash 0x007c0000.., the OS's add-in page table
// 0x8c04cf0c is pointed there, as in dasm_swap_test.go), so a fresh build runs against the real
// storage without rewriting the file system. Each key then also saves the add-in's whole
// 396x224 frame (cg50_real_NN_full.png).

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestDasmReal(t *testing.T) {
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
	savePNG(t, e, "real_00")
	launched := false
	if os.Getenv("DASM_LAUNCH") != "" {
		for _, k := range [][2]uint32{{2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {1, 7}, {1, 7}} {
			e.InjectKey(k[0], k[1])
			e.Step(30_000_000)
		}
		e.InjectKey(2, 1) // EXE
		for i := 0; e.cpu.pc != 0x00300000; i++ {
			if i > 200_000_000 {
				t.Fatal("DASM never started")
			}
			e.Step(1)
		}
		launched = true
		if path := os.Getenv("DASM_SWAP"); path != "" {
			swapAddinCode(t, e, path)
		}
	}
	for i, k := range strings.Split(os.Getenv("DASM_KEYS"), ",") {
		var r, c uint32
		if n, _ := fmt.Sscanf(k, "%d-%d", &r, &c); n != 2 {
			continue
		}
		e.InjectKey(r, c)
		c0 := e.cpu.cycles
		if os.Getenv("DASM_TRACE") == fmt.Sprint(i+1) {
			// debugging a crash: single-step, keep the last 256 (pc, pr, r15), dump them on a fault
			type tr struct{ pc, pr, sp uint32 }
			var ring [256]tr
			for n := 0; n < 3_000_000_000 && e.Fault() == ""; n++ {
				ring[n&255] = tr{e.cpu.pc, e.cpu.pr, e.cpu.r[15]}
				e.Step(1)
				if e.Fault() != "" {
					ev := e.mem.R32(0xFF000028)
					t.Logf("INTEVT %#x -> OS table 0xfd8010c8[%#x] = %#x", ev, (ev-0x40)>>3, e.mem.R32(0xfd8010c8+((ev-0x40)>>3)))
					for k := 1; k <= 256; k++ {
						x := ring[(n+k)&255]
						t.Logf("  pc %08x pr %08x sp %08x", x.pc, x.pr, x.sp)
					}
				}
			}
		}
		e.Step(30_000_000)
		// long operations (DASM's whole-file analysis): keep going until the add-in waits for
		// a key again (sleeping in getkey) and report how long it took
		for n := 0; n < 400 && !e.cpu.sleeping; n++ {
			e.Step(10_000_000)
		}
		if d := e.cpu.cycles - c0; d > 40_000_000 {
			t.Logf("key %d (%s): %.1f M instructions (%.1f s at 118 MHz)", i+1, k, float64(d)/1e6, float64(d)/118e6)
		}
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		savePNG(t, e, fmt.Sprintf("real_%02d", i+1))
		if launched {
			saveAddinFrame(t, e, fmt.Sprintf("real_%02d_full", i+1))
		}
	}
}

// swapAddinCode replaces the code of the add-in the OS has just started (pc at 0x00300000) with
// the .g3a at path: its code pages go to erased flash at 0x007c0000 and the OS's add-in page
// table (0x8c04cf0c[(page-0x300000)>>12], read by the TLB miss handler) is pointed at them.
func swapAddinCode(t *testing.T, e *Emulator, path string) {
	g3a, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const free, tbl = 0x007c0000, 0x8c04cf0c
	code := g3a[0x7000:]
	pages := (len(code) + 0xfff) >> 12
	for i := 0; i < pages<<12; i++ {
		if e.mem.flash[free+i] != 0xFF {
			t.Fatalf("free region not erased at %#x", free+i)
		}
	}
	copy(e.mem.flash[free:], code)
	hi := e.mem.R32(tbl) &^ 0x1fffffff
	for k := 0; k < pages; k++ {
		e.mem.W32(tbl+uint32(4*k), hi|uint32(free+(k<<12)))
	}
	e.mem.W32(tbl+uint32(4*pages), 0)
	// drop the UTLB entries that already map the add-in's code (page 0 was mapped at launch)
	// so the next access misses and the OS handler reloads them from the patched table
	for i := range e.mmio.ccn.utlb {
		if u := &e.mmio.ccn.utlb[i]; u.valid() && u.vpn >= 0x00300000 && u.vpn < 0x00800000 {
			u.data &^= 0x100
		}
	}
	t.Logf("swapped in %s: %d code pages at %#x", path, pages, free)
}
