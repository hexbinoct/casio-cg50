package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

const goldenPath = "../emu/golden_boot.bin"
const flashPath = "../os/flash_dump/flash_full.bin"

// TestGoldenBoot boots flash_full.bin from reset (timer OFF, pure boot bring-up) and
// asserts the Go core reproduces the Python oracle's full CPU state at every checkpoint.
func TestGoldenBoot(t *testing.T) {
	gold, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Skipf("no golden file (%v) — run: python emu/gen_golden.py", err)
	}
	img, err := os.ReadFile(flashPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", flashPath, err)
	}
	magic := binary.BigEndian.Uint32(gold[0:])
	if magic != 0x474F4C44 {
		t.Fatalf("bad golden magic 0x%08x", magic)
	}
	count := binary.BigEndian.Uint32(gold[8:])
	stride := binary.BigEndian.Uint32(gold[12:])
	rec := func(i int) []uint32 {
		off := 16 + i*23*4
		out := make([]uint32, 23)
		for k := 0; k < 23; k++ {
			out[k] = binary.BigEndian.Uint32(gold[off+k*4:])
		}
		return out
	}

	mmio := NewMMIOBus()
	mem := NewMemory(img, mmio)
	cpu := NewCPU(mem)
	cpu.cycles = 0
	cpu.pc = 0x80000000

	names := []string{"pc", "sr", "r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
		"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15", "pr", "gbr", "vbr", "mach", "macl"}

	cur := func() []uint32 {
		return []uint32{cpu.pc, cpu.sr,
			cpu.r[0], cpu.r[1], cpu.r[2], cpu.r[3], cpu.r[4], cpu.r[5], cpu.r[6], cpu.r[7],
			cpu.r[8], cpu.r[9], cpu.r[10], cpu.r[11], cpu.r[12], cpu.r[13], cpu.r[14], cpu.r[15],
			cpu.pr, cpu.gbr, cpu.vbr, cpu.mach, cpu.macl}
	}

	stepN := func(nsteps uint32) (panicPC uint32, ok bool) {
		defer func() {
			if r := recover(); r != nil {
				panicPC = cpu.pc
				ok = false
				t.Fatalf("panic at instr ~%d pc=0x%08x: %v", cpu.cycles, cpu.pc, r)
			}
		}()
		for i := uint32(0); i < nsteps; i++ {
			cpu.step()
		}
		return 0, true
	}

	for i := 0; i < int(count); i++ {
		want := rec(i)
		got := cur()
		for k := 0; k < 23; k++ {
			if got[k] != want[k] {
				t.Fatalf("MISMATCH at checkpoint %d (instr %d), field %s:\n  got  0x%08x\n  want 0x%08x\n  (full got: %v)",
					i, i*int(stride), names[k], got[k], want[k], hexslice(got))
			}
		}
		stepN(stride)
	}
	t.Logf("OK: %d checkpoints matched over %d instructions; final PC=0x%08x SR=0x%08x",
		count, int(count)*int(stride), cpu.pc, cpu.sr)
}

func hexslice(v []uint32) string {
	s := ""
	for _, x := range v {
		s += fmt.Sprintf("%08x ", x)
	}
	return s
}
