//go:build probe

package main

// Probe (2026-10-09, wiki os/boot.md): a cold boot of the 16 MB image (IL RAM zeroed, so the
// warm flag 0xFD8017DC is 0) through the first-boot setup. Logs entries into the boot check
// FUN_80365238, the setup wizard 0x8035e1be and its four screens (sc 0x1e0d, 0x1e0a, 0x1e05,
// 0x1e07), the routines after it, and every change of the warm flag. Each time the OS has
// idled for 60M cycles (a screen waiting for a key), it saves $TMPDIR/cg50_firstboot_NN.png
// and presses the next key of firstBootKeys (then EXE).
//
//   go -C emu_go test -tags probe -run 'TestFirstBootScreens$' -count=1 -v .
//   FIRSTBOOT_WARM=1   seed the warm flag = 1 first (a power-on from "off")

import (
	"fmt"
	"os"
	"testing"
)

// firstBootKeys walks the four setup screens: Message Language, Display Settings and Power
// Properties take F6 (Next); Battery Settings needs F1 (SELECT) and F1 (Yes, to its warning
// that capacity detection depends on the battery type) before F6 (Finish) is enabled.
var firstBootKeys = [][2]uint32{{1, 9}, {1, 9}, {1, 9}, {6, 9}, {6, 9}, {1, 9}}

func TestFirstBootScreens(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	e := NewEmulator(flash)
	if os.Getenv("FIRSTBOOT_WARM") != "" {
		e.mem.W32(warmFlagAddr, 1)
	}
	watch := map[uint32]string{
		0x80365238: "boot check FUN_80365238",
		0x801de9ca: "sc 0x1197: flash word 0x300 blank?",
		0x8035e1be: "SETUP WIZARD 0x8035e1be",
		0x8035d234: "  screen 1 (sc 0x1e0d)",
		0x8035cf58: "  screen 2 (sc 0x1e0a)",
		0x8035c7c0: "  screen 3 (sc 0x1e05)",
		0x8035cbfc: "  screen 4 (sc 0x1e07)",
		0x8035fce8: "after wizard 0x8035fce8",
		0x801504da: "0x801504da",
		0x801f2630: "0x801f2630",
		0x80365fc6: "flag:=1 + restart (0x80365fc6)",
		0x801de698: "restart sc 0x1187",
		0xa0000000: "RESET VECTOR",
	}
	seen := map[uint32]int{}
	flag := e.mem.R32(warmFlagAddr)
	t.Logf("boot from reset, warm flag = %d", flag)
	shots := 0
	var sleptAt uint64
	for e.cpu.cycles < 4_000_000_000 && shots < 12 {
		if e.cpu.sleeping {
			e.Step(1_000_000)
		} else {
			e.Step(1)
		}
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		if name, ok := watch[e.cpu.pc]; ok && !e.cpu.sleeping {
			seen[e.cpu.pc]++
			if seen[e.cpu.pc] <= 2 {
				t.Logf("%11d enter %s (pr=%08x) flag=%d", e.cpu.cycles, name, e.cpu.pr, e.mem.R32(warmFlagAddr))
			}
		}
		if v := e.mem.R32(warmFlagAddr); v != flag {
			t.Logf("%11d pc=%08x warm flag %d -> %d", e.cpu.cycles, e.cpu.pc, flag, v)
			flag = v
		}
		if !e.cpu.sleeping {
			sleptAt = 0
			continue
		}
		if sleptAt == 0 {
			sleptAt = e.cpu.cycles
		}
		if e.atMainMenu() {
			savePNG(t, e, fmt.Sprintf("firstboot_%02d_menu", shots))
			t.Logf("%11d at the MAIN MENU, warm flag = %d", e.cpu.cycles, flag)
			return
		}
		if e.cpu.cycles-sleptAt > 60_000_000 { // idle for a while: a screen waiting for a key
			savePNG(t, e, fmt.Sprintf("firstboot_%02d", shots))
			k := [2]uint32{2, 1} // EXE once the four screens are done
			if shots < len(firstBootKeys) {
				k = firstBootKeys[shots]
			}
			t.Logf("%11d idle -> shot %d, pressing %d-%d", e.cpu.cycles, shots, k[0], k[1])
			shots++
			e.InjectKey(k[0], k[1])
			sleptAt = 0
		}
	}
	t.Logf("stopped at %d cycles, pc=%08x, flag=%d", e.cpu.cycles, e.cpu.pc, flag)
}
