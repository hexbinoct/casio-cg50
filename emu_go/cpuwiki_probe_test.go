//go:build probe

package main

// Probe (2026-10-09, for the wiki's CPU page): single-step OS 3.60 through a warm boot of the
// 16 MB image and a short Run-Matrix session (1÷7, 2^10) and record what the CPU actually does:
// F-prefix opcodes (FPU on a plain SH-4A, DSP on the SH4AL-DSP), the FPSCR/FPUL-slot transfer
// encodings, what sits in delay slots (PC-relative loads, branches), sleep sites and the SR
// they run with, ldc/stc Rn_BANK sites, exceptions taken (VBR+0x100/0x400) and the SR bits seen.
//
//   go -C emu_go test -tags probe -run TestCPUWikiStats -count=1 -v .

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

type cpuStats struct {
	steps                            uint64
	fOps, fpscrOps, fpulOps          uint64
	fPCs, fpscrPCs, fpulPCs          map[uint32]uint64
	delayed, slotPCrel, slotBranch   uint64
	slotPCrelPCs, slotBranchPCs      map[uint32]uint64
	sleepPCs                         map[uint32]uint64
	sleepSR                          map[uint32]uint64
	bankPCs                          map[uint32]uint64
	exc                              map[string]uint64 // "vector expevt"
	irqs                             uint64
	srOr, srAnd                      uint32
	vbrs                             map[uint32]bool
	rtePCs                           map[uint32]uint64
}

func newCPUStats() *cpuStats {
	return &cpuStats{fPCs: map[uint32]uint64{}, fpscrPCs: map[uint32]uint64{}, fpulPCs: map[uint32]uint64{},
		slotPCrelPCs: map[uint32]uint64{}, slotBranchPCs: map[uint32]uint64{}, sleepPCs: map[uint32]uint64{},
		sleepSR: map[uint32]uint64{}, bankPCs: map[uint32]uint64{}, exc: map[string]uint64{},
		vbrs: map[uint32]bool{}, rtePCs: map[uint32]uint64{}, srAnd: 0xFFFFFFFF}
}

func isDelayed(op uint32) bool {
	switch {
	case op&0xF000 == 0xA000, op&0xF000 == 0xB000: // bra, bsr
		return true
	case op&0xFF00 == 0x8D00, op&0xFF00 == 0x8F00: // bt/s, bf/s
		return true
	case op&0xF0FF == 0x402B, op&0xF0FF == 0x400B: // jmp, jsr
		return true
	case op == 0x000B, op == 0x002B: // rts, rte
		return true
	case op&0xF0FF == 0x0023, op&0xF0FF == 0x0003: // braf, bsrf
		return true
	}
	return false
}

func isBranch(op uint32) bool {
	return isDelayed(op) || op&0xFF00 == 0x8900 || op&0xFF00 == 0x8B00 || op&0xFF00 == 0xC300
}

func isPCrel(op uint32) bool {
	return op&0xF000 == 0x9000 || op&0xF000 == 0xD000 || op&0xFF00 == 0xC700
}

func (s *cpuStats) observe(e *Emulator, n uint64) {
	c := e.cpu
	end := c.cycles + n
	for c.cycles < end && e.Fault() == "" {
		if c.sleeping {
			d := e.nextDue()
			k := uint64(1)
			if d > c.cycles {
				k = d - c.cycles
			}
			if c.cycles+k > end {
				k = end - c.cycles
			}
			e.Step(int(k))
			continue
		}
		pc := c.pc
		s.srOr |= c.sr
		s.srAnd &= c.sr
		if !c.mem.mmuAt && pc >= 0x80000000 {
			op := c.mem.R16(pc)
			switch {
			case op&0xF000 == 0xF000:
				s.fOps++
				s.fPCs[pc]++
			case op&0xF0FF == 0x406A, op&0xF0FF == 0x006A, op&0xF0FF == 0x4066, op&0xF0FF == 0x4062:
				s.fpscrOps++
				s.fpscrPCs[pc]++
			case op&0xF0FF == 0x405A, op&0xF0FF == 0x005A, op&0xF0FF == 0x4056, op&0xF0FF == 0x4052:
				s.fpulOps++
				s.fpulPCs[pc]++
			case op == 0x001B:
				s.sleepPCs[pc]++
				s.sleepSR[c.sr]++
			case op&0xF08F == 0x4087, op&0xF08F == 0x408E, op&0xF08F == 0x0082, op&0xF08F == 0x4083:
				s.bankPCs[pc]++
			case op == 0x002B:
				s.rtePCs[pc]++
			}
			if isDelayed(op) {
				s.delayed++
				slot := c.mem.R16(pc + 2)
				if isPCrel(slot) {
					s.slotPCrel++
					s.slotPCrelPCs[pc+2]++
				}
				if isBranch(slot) {
					s.slotBranch++
					s.slotBranchPCs[pc+2]++
				}
			}
		}
		irq0 := c.irqCnt
		e.Step(1)
		s.steps++
		s.vbrs[c.vbr] = true
		if c.irqCnt != irq0 {
			s.irqs++
		} else if c.pc == c.vbr+0x100 || c.pc == c.vbr+0x400 {
			s.exc[fmt.Sprintf("VBR+%#x EXPEVT=%#x", c.pc-c.vbr, e.mem.R32(0xFF000024))]++
		}
	}
}

func topPCs(m map[uint32]uint64, n int) string {
	type kv struct {
		k uint32
		v uint64
	}
	var l []kv
	for k, v := range m {
		l = append(l, kv{k, v})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].v > l[j].v || l[i].v == l[j].v && l[i].k < l[j].k })
	out := fmt.Sprintf("%d sites:", len(l))
	for i, x := range l {
		if i == n {
			out += " …"
			break
		}
		out += fmt.Sprintf(" %08x×%d", x.k, x.v)
	}
	return out
}

func TestCPUWikiStats(t *testing.T) {
	img, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no image: ", err)
	}
	e := NewEmulator(img)
	e.mem.W32(warmFlagAddr, 1)
	s := newCPUStats()
	s.observe(e, 600_000_000)
	t.Logf("after boot: pc=%08x atMainMenu=%v fault=%q", e.cpu.pc, e.atMainMenu(), e.Fault())
	tap := func(ks ...[2]uint32) {
		for _, k := range ks {
			e.InjectKey(k[0], k[1])
			s.observe(e, 40_000_000)
		}
	}
	tap(rc1) // Run-Matrix
	tap(rc1, rcDIV, rc7, rcEXE)
	tap(rc2, rcPOW, rc1, rc0, rcEXE)
	savePNG(t, e, "cpuwiki_runmat")
	t.Logf("end: pc=%08x fault=%q steps=%d irqs=%d", e.cpu.pc, e.Fault(), s.steps, s.irqs)
	t.Logf("SR bits: OR=%08x AND=%08x (bit12=%v, FD bit15 ever=%v)", s.srOr, s.srAnd, s.srOr&0x1000 != 0, s.srOr&srFD != 0)
	var vb []string
	for v := range s.vbrs {
		vb = append(vb, fmt.Sprintf("%08x", v))
	}
	sort.Strings(vb)
	t.Logf("VBR values: %v", vb)
	t.Logf("F-prefix ops: %d  %s", s.fOps, topPCs(s.fPCs, 12))
	t.Logf("FPSCR-slot transfers: %d  %s", s.fpscrOps, topPCs(s.fpscrPCs, 12))
	t.Logf("FPUL-slot transfers: %d  %s", s.fpulOps, topPCs(s.fpulPCs, 12))
	t.Logf("delayed branches: %d, PC-relative in slot: %d (%s), branch in slot: %d (%s)",
		s.delayed, s.slotPCrel, topPCs(s.slotPCrelPCs, 12), s.slotBranch, topPCs(s.slotBranchPCs, 8))
	t.Logf("sleep: %s", topPCs(s.sleepPCs, 12))
	for sr, n := range s.sleepSR {
		t.Logf("  sleep with SR=%08x ×%d", sr, n)
	}
	t.Logf("Rn_BANK ldc/stc: %s", topPCs(s.bankPCs, 16))
	t.Logf("rte: %s", topPCs(s.rtePCs, 12))
	for k, n := range s.exc {
		t.Logf("exception %s ×%d", k, n)
	}
}
