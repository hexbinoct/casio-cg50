//go:build probe

package main

// Host-speed benchmarks (probe tag): go -C emu_go test -tags probe -run XXX -bench MenuHold
// Holding RIGHT on the MAIN MENU for 3 emulated s at 22M instr/s, with the old per-instruction
// device polling (Exact) vs event scheduling (Scheduled). Compare within one run: absolute
// numbers swing a lot on a long-uptime / hibernated Windows box.

import (
	"os"
	"testing"
)

func benchMenuHold(b *testing.B, exact bool) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		b.Skip(err)
	}
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		e := NewEmulator(flash)
		e.Resume(st)
		e.exactTick = exact
		e.SetInstrPerSecond(22_000_000)
		e.KeyDown(1, 7)
		b.StartTimer()
		for j := 0; j < 132; j++ { // 3 s emulated in 500k slices
			e.Step(500_000)
		}
	}
}

func BenchmarkMenuHoldExact(b *testing.B)     { benchMenuHold(b, true) }
func BenchmarkMenuHoldScheduled(b *testing.B) { benchMenuHold(b, false) }
