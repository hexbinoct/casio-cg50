//go:build probe

package main

// Probe (2026-09-28): run DASM.g3a as it is really installed in fls0, on the full 32 MB dump
// resumed from its warm-boot save-state (see warm_boot_test.go / CG50_WARM in main.go):
//   CG50_FLASH=../os/flash_dump/flash_full_32mb.bin CG50_STATE=../os/flash_dump/cg50_state_32mb.bin \
//     CG50_WARM=1 go -C emu_go run . 600000000 30000 provision none
// DASM_KEYS is a comma list of row-col keys to press, one screenshot each (cg50_real_NN.png).

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
	for i, k := range strings.Split(os.Getenv("DASM_KEYS"), ",") {
		var r, c uint32
		if n, _ := fmt.Sscanf(k, "%d-%d", &r, &c); n != 2 {
			continue
		}
		e.InjectKey(r, c)
		e.Step(30_000_000)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		savePNG(t, e, fmt.Sprintf("real_%02d", i+1))
	}
}
