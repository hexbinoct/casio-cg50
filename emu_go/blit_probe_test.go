//go:build probe

package main

// Investigation probe for the HLE of the OS bitmap blitter 0x80056900 (cont.18u): logs every
// call's descriptor (0x2c bytes at r4) and writer mode (r5) over a few workloads, with the
// instruction count of each call, so the native version covers exactly the modes the OS uses
// and charges a calibrated number of emulated cycles. Run with
//   go -C emu_go test -tags probe -run TestBlitProbe -v

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

const blitEntry = 0x80056900

type blitKey struct {
	format, transp, rop, mode2 byte
	blend                      int32
	mode                       uint32
}

func TestBlitProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(100_000_000)
	e.Step(2_000_000)

	calls := map[blitKey]int{}
	instr := map[blitKey]uint64{}
	pixels := map[blitKey]uint64{}
	var samples []string // (pixels, instr) pairs for the cycle-cost fit
	shown := map[blitKey]bool{}

	run := func(name string, budget int) {
		n := 0
		for i := 0; i < budget; i++ {
			if e.cpu.pc == blitEntry {
				r4, mode := e.cpu.r[4], e.cpu.r[5]
				var d [0x2c]byte
				for j := range d {
					d[j] = byte(e.mem.R8(r4 + uint32(j)))
				}
				be32 := func(o int) int32 {
					return int32(uint32(d[o])<<24 | uint32(d[o+1])<<16 | uint32(d[o+2])<<8 | uint32(d[o+3]))
				}
				k := blitKey{d[0x18], d[0x22], d[0x24], d[0x25], be32(0x28), mode}
				x, y, xo, yo, w, h := be32(0), be32(4), be32(8), be32(0xc), be32(0x10), be32(0x14)
				sp0, ret, c0 := e.cpu.r[15], e.cpu.pr, e.cpu.cycles
				for !(e.cpu.pc == ret && e.cpu.r[15] == sp0) {
					e.Step(1)
				}
				ni := e.cpu.cycles - c0
				px := uint64(0)
				if w > 0 && h > 0 {
					px = uint64(w-xo) * uint64(h-yo)
				}
				calls[k]++
				instr[k] += ni
				pixels[k] += px
				n++
				if !shown[k] || len(samples) < 40 {
					if !shown[k] {
						t.Logf("[%s] NEW combo %+v: x=%d y=%d xo=%d yo=%d w=%d h=%d data=%08x raw=% x",
							name, k, x, y, xo, yo, w, h, uint32(be32(0x1c)), d[:])
					}
					shown[k] = true
					samples = append(samples, fmt.Sprintf("%dx%d(%d,%d)->%d", w, h, xo, yo, ni))
				}
				continue
			}
			e.Step(1)
		}
		t.Logf("[%s] %d blitter calls", name, n)
	}

	e.InjectKey(1, 7) // RIGHT
	run("RIGHT", 6_000_000)
	e.InjectKey(1, 7)
	run("RIGHT2", 6_000_000)
	e.InjectKey(2, 7) // DOWN
	run("DOWN", 6_000_000)
	e.InjectKey(2, 1) // EXE: open the highlighted app
	run("EXE", 30_000_000)
	e.InjectKey(6, 2) // "1"
	run("1", 6_000_000)
	e.InjectKey(5, 2) // "2"
	run("2", 6_000_000)
	e.InjectKey(2, 1) // EXE
	run("EXE2", 20_000_000)
	e.InjectKey(3, 8) // MENU
	run("MENU", 30_000_000)
	e.InjectKey(3, 7) // EXIT
	run("EXIT", 10_000_000)

	var ks []blitKey
	for k := range calls {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return instr[ks[i]] > instr[ks[j]] })
	for _, k := range ks {
		t.Logf("combo %+v: %d calls, %d instr, %d px -> %.1f instr/px",
			k, calls[k], instr[k], pixels[k], float64(instr[k])/float64(max(pixels[k], 1)))
	}
	t.Logf("samples (w x h (xo,yo) -> instr): %v", samples)
}

// The phone's flow at several time bases: Resume, SetInstrPerSecond, HLE on/off, then the
// app's 5 ms chunks with RIGHT held for 1.2 emulated seconds. Counts pushes and executed
// instructions (the phone showed a dead machine at 45M: keys ignored, ~0 executed).
func TestSpeedSettingProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	states := []string{"../os/flash_dump/cg50_state.bin", "../re/_phone_state.bin"} // phone's via re/phone.py pull
	for _, path := range states {
		st, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		t.Logf("state %s", path)
		for _, ips := range []uint64{100_000_000, 45_000_000} {
		for _, hle := range []bool{false, true} {
			e := NewEmulator(flash)
			if err := e.Resume(st); err != nil {
				t.Fatal(err)
			}
			e.SetInstrPerSecond(ips)
			e.EnableHLE(hle)
			chunk := int(ips / 200)
			for i := 0; i < 200; i++ { // 1 s idle
				e.Step(chunk)
			}
			p0, x0 := e.Pushes(), e.Executed()
			e.KeyDown(1, 7)
			for i := 0; i < 240; i++ { // 1.2 s held
				e.Step(chunk)
			}
			e.KeyUp(1, 7)
			t.Logf("ips=%dM hle=%v: %d pushes in 1.2 s, %.2fM instr executed, fault=%q",
				ips/1_000_000, hle, e.Pushes()-p0, float64(e.Executed()-x0)/1e6, e.Fault())
		}
		}
	}
}

// Host speed of a held RIGHT on the MAIN MENU with the native blitter off vs on: wall time
// and LCD pushes for the same 3 emulated seconds (the pushes must match).
func TestHLESpeed(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	for _, hle := range []bool{false, true} {
		e := NewEmulator(flash)
		if err := e.Resume(st); err != nil {
			t.Fatal(err)
		}
		e.SetInstrPerSecond(100_000_000)
		e.EnableHLE(hle)
		e.Step(1_000_000)
		p0, x0 := e.Pushes(), e.Executed()
		t0 := time.Now()
		e.KeyDown(1, 7)
		e.Step(300_000_000)
		e.KeyUp(1, 7)
		dt := time.Since(t0)
		t.Logf("hle=%v: %d pushes in 3 emulated s, %d instr executed, host %.0f ms",
			hle, e.Pushes()-p0, e.Executed()-x0, float64(dt.Microseconds())/1e3)
	}
}
