package cg50emu

// Host-speed benchmarks of the run loop with and without the debugger (debug.go):
//
//	go -C emu_go test -run XXX -bench Debug -benchtime 3x .
//
// Each run boots the 16 MB dump from reset for 40 M instructions (CPU-bound OS code: the
// boot's own copy/clear/decompress loops) in the 250 000-instruction chunks the desktop app
// uses, and reports the executed instructions per host second (Minstr/s). Compare variants
// within one run: absolute numbers depend on the machine and its load.

import (
	"os"
	"testing"
	"time"
)

func benchBoot(b *testing.B, setup func(e *Emulator)) {
	flash, err := os.ReadFile(flashPath)
	if err != nil {
		b.Skip(err)
	}
	const total, chunk = 40_000_000, 250_000
	var exec uint64
	var elapsed time.Duration
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		e := NewEmulator(flash)
		if setup != nil {
			setup(e)
		}
		b.StartTimer()
		t0 := time.Now()
		for j := 0; j < total/chunk; j++ {
			e.Step(chunk)
		}
		elapsed += time.Since(t0)
		exec += e.Executed()
	}
	b.ReportMetric(float64(exec)/elapsed.Seconds()/1e6, "Minstr/s")
}

// BenchmarkDebugOff: no debugger feature in use (the shipping configuration).
func BenchmarkDebugOff(b *testing.B) { benchBoot(b, nil) }

// BenchmarkDebugBreakpoints: three breakpoints the boot never reaches (one on a page it runs).
func BenchmarkDebugBreakpoints(b *testing.B) {
	benchBoot(b, func(e *Emulator) {
		e.AddBreakpoint(0x80000003) // never an instruction address, on the reset code's page
		e.AddBreakpoint(0x8C7FF000)
		e.AddBreakpoint(0xA0123456)
	})
}

// BenchmarkDebugTrace: the trace ring alone (64 K entries).
func BenchmarkDebugTrace(b *testing.B) { benchBoot(b, func(e *Emulator) { e.SetTrace(1 << 16) }) }

// BenchmarkDebugWatch: one watchpoint on a RAM word the boot never touches.
func BenchmarkDebugWatch(b *testing.B) {
	benchBoot(b, func(e *Emulator) {
		e.AddWatchpoint(Watchpoint{Addr: 0x0C7FFFF0, Len: 4, Phys: true, Enabled: true})
	})
}
