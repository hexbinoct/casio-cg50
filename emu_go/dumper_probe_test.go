//go:build probe

package cg50emu

// Probe (2026-11-07): the FlashDump add-in (tools/flash_dump) on the emulator, up to the point
// where it waits for the computer (the emulator has no USB host). Installs FLASHDMP.g3a, opens
// it from the MAIN MENU, saves the whole panel ($TMPDIR/cg50_dumper_<tag>_N.png) at its menu and
// after F1. With $DUMPER_FLASH (e.g. ../os/flash_dump/synthetic_380.bin) it cold-boots that
// image through first-boot setup instead of resuming the 3.60 save-state.
//
//   go -C emu_go test -tags probe -run 'TestDumperScreens$' -count=1 -v .
//   python re/run_probe.py TestDumperScreens DUMPER_FLASH=../os/flash_dump/synthetic_380.bin

import (
	"os"
	"testing"
)

// savePanel (monitor_dasm_probe_test.go) saves the whole panel: gint draws on all of it.

func TestDumperScreens(t *testing.T) {
	g3a, err := os.ReadFile("../tools/flash_dump/FLASHDMP.g3a")
	if err != nil {
		t.Skip("build the add-in first: ", err)
	}
	tag, path := "360", os.Getenv("DUMPER_FLASH")
	var e *Emulator
	run := func(n int) {
		e.Step(n)
		if f := e.Fault(); f != "" {
			savePanel(t, e, "dumper_"+tag+"_fault")
			t.Fatal(f)
		}
	}
	tap := func(k [2]uint32) { e.InjectKey(k[0], k[1]); run(400_000_000) }
	if path == "" {
		flash, err1 := os.ReadFile("../os/flash_dump/flash_full.bin")
		state, err2 := os.ReadFile("../os/flash_dump/cg50_state.bin")
		if err1 != nil || err2 != nil {
			t.Skip("no flash image or state")
		}
		e = NewEmulator(flash)
		if err := e.Resume(state); err != nil {
			t.Fatal(err)
		}
		run(300_000_000)
	} else {
		flash, err := os.ReadFile(path)
		if err != nil {
			t.Skip(err)
		}
		tag = "other"
		e = NewEmulator(flash)
		run(200_000_000)
		for _, k := range append(firstBootKeys, [2]uint32{2, 1}) {
			tap(k)
		}
		tap([2]uint32{3, 8}) // MENU: from Run-Matrix (setup ends there) to the MAIN MENU
	}
	if err := e.InstallAddin("FLASHDMP.g3a", g3a); err != nil {
		t.Fatal(err)
	}
	run(300_000_000)
	// a fresh MAIN MENU (Run-Matrix and back) lists the new icon; the add-ins are on row 5
	tap([2]uint32{6, 2})
	tap([2]uint32{3, 8})
	for i := 0; i < 4; i++ {
		tap([2]uint32{2, 7})
	}
	tap([2]uint32{1, 7})
	tap([2]uint32{1, 7})
	savePanel(t, e, "dumper_"+tag+"_0_icon")
	tap([2]uint32{2, 1}) // EXE
	savePanel(t, e, "dumper_"+tag+"_1_menu")
	tap([2]uint32{6, 9}) // F1
	savePanel(t, e, "dumper_"+tag+"_2_connect")
}
