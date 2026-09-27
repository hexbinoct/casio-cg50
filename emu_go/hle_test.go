package main

import (
	"bytes"
	"os"
	"testing"
)

// TestHLEBlitMatchesInterpreter runs every OS blitter call in a session (menu moves, opening
// Run-Matrix, typing, MENU, EXIT) both ways from the same machine state — the real code in
// the interpreter and hleBlit — and requires identical DRAM (VRAM included) and identical
// callee-saved registers, sp, pr and return pc. The caller-saved registers and T are ABI
// scratch and are not compared. It also reports how the charged cycles compare with the
// instructions the real code executed (the timing the HLE preserves).
func TestHLEBlitMatchesInterpreter(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash dump")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(100_000_000)
	e.Step(2_000_000)
	c, m := e.cpu, e.mem

	dram := make([]byte, len(m.dram))
	handled, fallback := 0, 0
	var actual, charged uint64
	byFormat := map[uint32][2]uint64{}
	run := func(budget int) {
		for i := 0; i < budget; i++ {
			if c.pc != hleBlitEntry {
				e.Step(1)
				continue
			}
			format := m.Read(c.r[4]+0x18, 1)
			r0, sr0, pc0, pr0, macl0, cyc0 := c.r, c.sr, c.pc, c.pr, c.macl, c.cycles
			copy(dram, m.dram)
			// Reference: the real code, interrupts masked so nothing else touches memory.
			c.sr |= srBL
			for !(c.pc == pr0 && c.r[15] == r0[15]) {
				c.step()
			}
			refDram := append([]byte(nil), m.dram...)
			refR, refPC := c.r, c.pc
			ref := c.cycles - cyc0
			// Same state again, natively.
			copy(m.dram, dram)
			c.r, c.sr, c.pc, c.pr, c.macl, c.cycles = r0, sr0, pc0, pr0, macl0, cyc0
			if !c.hleBlit() {
				fallback++
				// leave the machine in the reference state and go on
				copy(m.dram, refDram)
				c.r, c.pc, c.cycles = refR, refPC, cyc0+ref
				continue
			}
			handled++
			got := c.cycles - cyc0
			actual += ref
			charged += got
			f := byFormat[format]
			byFormat[format] = [2]uint64{f[0] + ref, f[1] + got}
			// The real code's frame (spilled locals + saved registers) lives below the entry sp
			// and is dead after the return; blank it in both before comparing.
			if sp := r0[15] & 0x1FFFFFFF; sp >= DramBase+0x100 && sp < DramBase+DramSize {
				lo := sp - DramBase - 0x100
				clear(m.dram[lo : lo+0x100])
				clear(refDram[lo : lo+0x100])
			}
			if !bytes.Equal(m.dram, refDram) {
				n := 0
				for j := range refDram {
					if m.dram[j] != refDram[j] {
						if n < 5 {
							t.Errorf("call %d (format %d): DRAM differs at phys %08x: hle %02x ref %02x",
								handled, format, DramBase+uint32(j), m.dram[j], refDram[j])
						}
						n++
					}
				}
				t.Fatalf("call %d (format %d): %d differing bytes", handled, format, n)
			}
			for j := 8; j < 16; j++ {
				if c.r[j] != refR[j] {
					t.Fatalf("call %d: r%d hle %08x ref %08x", handled, j, c.r[j], refR[j])
				}
			}
			if c.pc != refPC || c.pr != pr0 || c.macl != macl0 {
				t.Fatalf("call %d: pc/pr/macl hle %08x/%08x/%08x ref %08x/%08x/%08x",
					handled, c.pc, c.pr, c.macl, refPC, pr0, macl0)
			}
			c.sr = sr0
			c.cycles = cyc0 + ref // keep the reference timing for the rest of the session
		}
	}
	e.InjectKey(1, 7) // RIGHT
	run(6_000_000)
	e.InjectKey(2, 7) // DOWN
	run(6_000_000)
	e.InjectKey(2, 1) // EXE: open the highlighted app
	run(30_000_000)
	e.InjectKey(6, 2) // "1"
	run(6_000_000)
	e.InjectKey(2, 1) // EXE
	run(20_000_000)
	e.InjectKey(3, 8) // MENU
	run(30_000_000)
	e.InjectKey(3, 7) // EXIT
	run(10_000_000)

	if handled < 60 {
		t.Fatalf("only %d blitter calls handled natively (%d fell back)", handled, fallback)
	}
	t.Logf("%d calls native, %d fell back to the interpreter; cycles charged/actual = %.3f",
		handled, fallback, float64(charged)/float64(actual))
	for f, v := range byFormat {
		t.Logf("  format %d: charged/actual = %.3f (%d instr)", f, float64(v[1])/float64(v[0]), v[0])
	}
	if r := float64(charged) / float64(actual); r < 0.9 || r > 1.1 {
		t.Errorf("cycle charge off by more than 10%%: %.3f", r)
	}
}
