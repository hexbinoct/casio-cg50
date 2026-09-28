//go:build probe

package main

// Probe (2026-09-28): why does fls0_open (FUN_80358b1e) return -6 when the emulator boots a
// FULL 32 MB flash dump (whole fls0 storage present) from reset? Boots the image, waits for
// the first entry into fls0_open and prints the call tree under it (target, r4-r7 at entry,
// r0 at return) until it returns. Image: FLS0_IMG, default os/flash_dump/flash_full_32mb.bin.
//
//   go -C emu_go test -tags probe -run TestFls0Trace -count=1 -v .
//   FLS0_FN=0x80365238 FLS0_DEPTH=3 ...   (trace another function / limit the depth)

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestFls0Trace(t *testing.T) {
	path := os.Getenv("FLS0_IMG")
	if path == "" {
		path = "../os/flash_dump/flash_full_32mb.bin"
	}
	img, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no image: ", err)
	}
	fn := uint32(0x80358b1e)
	if v := os.Getenv("FLS0_FN"); v != "" {
		x, _ := strconv.ParseUint(v, 0, 32)
		fn = uint32(x)
	}
	maxDepth := 8
	if v := os.Getenv("FLS0_DEPTH"); v != "" {
		maxDepth, _ = strconv.Atoi(v)
	}
	e := NewEmulator(img)
	c := e.cpu
	const limit = 1_500_000_000
	for c.pc != fn {
		if c.cycles > limit {
			t.Fatalf("%08x never reached", fn)
		}
		e.Step(1)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
	}
	t.Logf("entered %08x at instr %d: r4=%08x r5=%08x r6=%08x r7=%08x pr=%08x", fn, c.cycles, c.r[4], c.r[5], c.r[6], c.r[7], c.pr)
	type frame struct {
		ret, sp, target uint32
		line            int
	}
	stack := []frame{{c.pr, c.r[15], fn, -1}}
	var lines []string
	calls := 0
	for len(stack) > 0 && c.cycles < limit {
		pc := c.pc
		op := e.mem.fetch16(pc)
		isCall := op&0xF0FF == 0x400B || op&0xF000 == 0xB000 || op&0xF0FF == 0x0003 // jsr / bsr / bsrf
		inIRQ := c.sr&(1<<28) != 0                                                  // SR.BL: exception handler
		e.Step(1)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		if isCall && !inIRQ {
			calls++
			d := len(stack)
			li := -1
			if d <= maxDepth && len(lines) < 4000 {
				li = len(lines)
				lines = append(lines, fmt.Sprintf("%s%08x(%x, %x, %x, %x)  from %08x", strings.Repeat("  ", d), c.pc, c.r[4], c.r[5], c.r[6], c.r[7], pc))
			}
			stack = append(stack, frame{pc + 4, c.r[15], c.pc, li})
		}
		for len(stack) > 0 {
			top := stack[len(stack)-1]
			if c.pc != top.ret || c.r[15] < top.sp {
				break
			}
			if top.line >= 0 {
				lines[top.line] += fmt.Sprintf("  -> %d (0x%x)", int32(c.r[0]), c.r[0])
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				t.Logf("%08x returned r0=%d (0x%x) after %d calls, at instr %d", fn, int32(c.r[0]), c.r[0], calls, c.cycles)
			}
		}
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}
