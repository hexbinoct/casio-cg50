//go:build probe

package cg50emu

// Probe (2026-11-07): what does the emulator do with an OS other than 3.60? Cold-boots the
// image in $OSVER_FLASH (default: the stand-in 3.80 flash re/make_synthetic_380.py builds)
// through the library API the apps use, with the native blitter off and then forced on, and
// walks first-boot setup like TestFirstBootScreens. Saves $TMPDIR/cg50_osver_<tag>_NN.png.
//
//   go -C emu_go test -tags probe -run 'TestOSVersionBoot$' -count=1 -v .

import (
	"fmt"
	"os"
	"testing"
)

func TestOSVersionBoot(t *testing.T) {
	path := os.Getenv("OSVER_FLASH")
	if path == "" {
		path = "../os/flash_dump/synthetic_380.bin"
	}
	flash, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no flash image: ", err)
	}
	for _, tag := range []string{"nohle", "hle"} {
		t.Run(tag, func(t *testing.T) { osverBoot(t, flash, tag) })
	}
}

func osverBoot(t *testing.T, flash []byte, tag string) {
	e := NewEmulator(flash)
	if tag == "hle" {
		e.cpu.hlePC, e.cpu.hle = hleBlitEntry, e.cpu.hleBlit // forced, whatever the OS
	}
	t.Logf("OS version %q, blitter %s, EnableHLE would give %v", OSVersion(flash), tag, OSSupported(flash))
	shots := 0
	var sleptAt uint64
	for e.cpu.cycles < 3_000_000_000 && shots < 10 {
		if e.cpu.sleeping {
			e.Step(1_000_000)
		} else {
			e.Step(1000)
		}
		if f := e.Fault(); f != "" {
			savePNG(t, e, fmt.Sprintf("osver_%s_fault", tag))
			t.Fatalf("%d cycles: fault: %s", e.cpu.cycles, f)
		}
		if !e.cpu.sleeping {
			sleptAt = 0
			continue
		}
		if sleptAt == 0 {
			sleptAt = e.cpu.cycles
		}
		if e.cpu.cycles-sleptAt > 60_000_000 {
			savePNG(t, e, fmt.Sprintf("osver_%s_%02d", tag, shots))
			k := [2]uint32{2, 1} // EXE after the setup screens
			if shots < len(firstBootKeys) {
				k = firstBootKeys[shots]
			}
			t.Logf("%11d idle at pc=%08x -> shot %d, pressing %d-%d", e.cpu.cycles, e.cpu.pc, shots, k[0], k[1])
			shots++
			e.InjectKey(k[0], k[1])
			sleptAt = 0
		}
	}
	t.Logf("stopped at %d cycles, pc=%08x, pushes=%d", e.cpu.cycles, e.cpu.pc, e.Pushes())
}

// TestSystemVersionScreen: where a user reads the OS version on the calculator. From the 3.60
// save-state at the MAIN MENU: open System (icon G: row 4, column 4), screenshot its soft
// keys, then each of F1..F6 in turn (EXIT between).
//
//	go -C emu_go test -tags probe -run 'TestSystemVersionScreen$' -count=1 -v .
func TestSystemVersionScreen(t *testing.T) {
	flash, err1 := os.ReadFile("../os/flash_dump/flash_full.bin")
	state, err2 := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err1 != nil || err2 != nil {
		t.Skip("no flash image or state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(state); err != nil {
		t.Fatal(err)
	}
	run := func() { e.Step(300_000_000) }
	tap := func(k [2]uint32) { e.InjectKey(k[0], k[1]); run() }
	down, right, exe, exit := [2]uint32{2, 7}, [2]uint32{1, 7}, [2]uint32{2, 1}, [2]uint32{3, 7}
	run()
	for _, k := range [][2]uint32{down, down, down, right, right, right, exe} {
		tap(k)
	}
	savePNG(t, e, "sys_0")
	fkeys := [][2]uint32{{6, 9}, {5, 9}, {4, 9}, {3, 9}, {2, 9}, {1, 9}} // F1..F6
	for i, k := range fkeys {
		tap(k)
		savePNG(t, e, fmt.Sprintf("sys_f%d", i+1))
		tap(exit)
	}
}

// TestResetStorage: the OS's own System → F5 Reset → F4 Storage Memory, in the emulator (the
// "I don't want my files in it" answer). Screens after each step, then the MAIN MENU's last row.
//
//	go -C emu_go test -tags probe -run 'TestResetStorage$' -count=1 -v .
func TestResetStorage(t *testing.T) {
	flash, err1 := os.ReadFile("../os/flash_dump/flash_full.bin")
	state, err2 := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err1 != nil || err2 != nil {
		t.Skip("no flash image or state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(state); err != nil {
		t.Fatal(err)
	}
	run := func() { e.Step(300_000_000) }
	tap := func(k [2]uint32) {
		e.InjectKey(k[0], k[1])
		run()
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
	}
	down, right, exe, menu := [2]uint32{2, 7}, [2]uint32{1, 7}, [2]uint32{2, 1}, [2]uint32{3, 8}
	f1, f4, f5 := [2]uint32{6, 9}, [2]uint32{3, 9}, [2]uint32{2, 9}
	run()
	which := f4 // RESET F4 Storage Memory (keeps add-ins); RESET_AS=1: F5 Add-In&Storage
	if os.Getenv("RESET_AS") != "" {
		which = f5
	}
	for _, k := range [][2]uint32{down, down, down, right, right, right, exe, f5, which} {
		tap(k)
	}
	savePNG(t, e, "rst_0_confirm")
	tap(f1) // Yes
	for i := 0; i < 20; i++ {
		run()
	}
	savePNG(t, e, "rst_1_done")
	tap([2]uint32{3, 7}) // EXIT
	tap(menu)
	for _, k := range [][2]uint32{down, down, down, down, down, down} {
		tap(k)
	}
	savePNG(t, e, "rst_2_menu")
	img := e.FlashImage()
	lo, hi := 0, 0
	for i, b := range img {
		was := byte(0xFF) // past the 16 MB image the flash reads erased
		if i < len(flash) {
			was = flash[i]
		}
		if b != was {
			if i < 16<<20 {
				lo++
			} else {
				hi++
			}
		}
	}
	t.Logf("flash bytes changed: %d in the first 16 MB, %d above", lo, hi)

	// RESTART: a new machine from the flash as it is now (warm, so no setup wizard); the
	// cursor starts on Run-Matrix, four DOWNs reach the fifth row (Python, Distribution, add-ins)
	r := NewEmulator(img)
	r.SetWarmBoot()
	r.Step(2_000_000_000)
	for _, k := range [][2]uint32{down, down, down, down} {
		r.InjectKey(k[0], k[1])
		r.Step(300_000_000)
	}
	savePNG(t, r, "rst_5_restart_menu")
	for _, k := range [][2]uint32{right, right, exe} {
		r.InjectKey(k[0], k[1])
		r.Step(300_000_000)
	}
	savePNG(t, r, "rst_6_launch")
	t.Logf("restart fault: %q", r.Fault())
}

// TestOSVersionInstall: first-boot setup to the MAIN MENU, then InstallAddin (KeyProbe), then
// Run-Matrix and MENU (a fresh menu lists the new icon) and down to the 19th icon.
//
//	go -C emu_go test -tags probe -run 'TestOSVersionInstall$' -count=1 -v .
func TestOSVersionInstall(t *testing.T) {
	path := os.Getenv("OSVER_FLASH")
	if path == "" {
		path = "../os/flash_dump/synthetic_380.bin"
	}
	flash, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no flash image: ", err)
	}
	g3a, err := os.ReadFile("../tools/keyprobe/KEYPROBE.g3a")
	if err != nil {
		t.Skip("no add-in: ", err)
	}
	e := NewEmulator(flash)
	idle := func() {
		var sleptAt uint64
		for start := e.cpu.cycles; e.cpu.cycles-start < 600_000_000; {
			if e.cpu.sleeping {
				e.Step(1_000_000)
			} else {
				e.Step(1000)
			}
			if f := e.Fault(); f != "" {
				savePNG(t, e, "osver_inst_fault")
				t.Fatalf("fault: %s (r15=%08x pr=%08x)", f, e.cpu.r[15], e.cpu.pr)
			}
			if !e.cpu.sleeping {
				sleptAt = 0
				continue
			}
			if sleptAt == 0 {
				sleptAt = e.cpu.cycles
			}
			if e.cpu.cycles-sleptAt > 60_000_000 {
				return
			}
		}
		t.Logf("%d: busy for 600M cycles (pc=%08x): an add-in that polls instead of sleeping", e.cpu.cycles, e.cpu.pc)
	}
	press := func(keys ...[2]uint32) {
		for _, k := range keys {
			idle()
			e.InjectKey(k[0], k[1])
		}
		idle()
	}
	press(append(firstBootKeys, [2]uint32{2, 1})...)
	savePNG(t, e, "osver_inst_0_menu")
	t.Logf("at the menu? atMainMenu=%v (3.60 check)", e.atMainMenu())
	err = e.InstallAddin("KEYPROBE.g3a", g3a)
	t.Logf("InstallAddin: %v", err)
	idle()
	savePNG(t, e, "osver_inst_1_after")
	down, right := [2]uint32{2, 7}, [2]uint32{1, 7}
	press([2]uint32{6, 2}, [2]uint32{3, 8}, down, down, down, down, right, right)
	savePNG(t, e, "osver_inst_2_icon")
	press(right, [2]uint32{2, 1}) // helomelo2 (a libfxcg add-in from the 3.60 storage)
	savePNG(t, e, "osver_inst_3_helo")
	press([2]uint32{3, 8}, [2]uint32{2, 8}, [2]uint32{2, 1}) // MENU, LEFT, EXE: KeyProbe (gint)
	savePNG(t, e, "osver_inst_4_keyprobe")
}
