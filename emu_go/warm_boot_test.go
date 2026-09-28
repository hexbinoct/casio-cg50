//go:build probe

package main

// Probe (2026-09-28): boot a flash image the way a real power-on from "off" does. The OS's
// power-off paths (FUN_801dfe4a, FUN_801504e0) set the IL RAM word 0xFD8017DC to 1 (unless it
// is 2 = setup in progress); FUN_80365238 skips the first-boot wizard FUN_8035e1be only when
// that word is 1 and the model code *0xFD8018D4 is 0xCA02, then clears it to 0. A cold boot
// (zeroed IL RAM) therefore always runs setup. Seeding the word = a warm power-on.
//
//   WARM_IMG=../os/flash_dump/flash_full_32mb.bin go -C emu_go test -tags probe -run TestWarmBoot -count=1 -v .

import (
	"os"
	"testing"
)

func TestWarmBoot(t *testing.T) {
	path := os.Getenv("WARM_IMG")
	if path == "" {
		path = "../os/flash_dump/flash_full_32mb.bin"
	}
	img, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no image: ", err)
	}
	e := NewEmulator(img)
	if os.Getenv("WARM_OFF") == "" {
		e.mem.W32(warmFlagAddr, 1)
	}
	for i := 1; i <= 12; i++ {
		e.Step(50_000_000)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		if i%3 == 0 {
			savePNG(t, e, "warm_"+string(rune('0'+i/3)))
		}
	}
	t.Logf("pc=%08x flag=%x model=%x pushes=%d", e.cpu.pc, e.mem.R32(warmFlagAddr), e.mem.R32(0xFD8018D4), e.Pushes())
}
