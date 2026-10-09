//go:build probe

package main

// Probe (2026-10-09, wiki os/boot.md): SHIFT+AC/ON at the MAIN MENU (the OS's power-off), then
// AC/ON to switch back on. Single-steps from the AC/ON press and logs, in order: entries into
// known routines (the warm-flag writers, the restart syscall 0x1187, the reset vector, the boot
// check, the setup wizard, the idle routine), the first callees after the press, every write to
// the warm flag 0xFD8017DC, every LCD controller register write (index + value; GRAM pixel data
// only counted), the first writes to each other MMIO address, and each `sleep`. Frames (the
// panel as the user sees it, i.e. GRAM) go to $TMPDIR/cg50_poweroff_*.png.
//
//   go -C emu_go test -tags probe -run 'TestPowerOffTrace$' -count=1 -v .
//   POWEROFF_CALLS=n   log the first n distinct callees after the press (default 120)

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"testing"
)

// TestAutoPowerOff leaves the MAIN MENU idle for 11 emulated minutes (the first-boot default
// "Auto Power Off: 10 Min.") with a breakpoint on PowerOff's body 0x802aea26 (sc 0x183a; sc
// 0x1839 at 0x802aea24 sets r5 = 1 and falls into it), through the CPU's HLE hook.
//
//   go -C emu_go test -tags probe -run 'TestAutoPowerOff$' -count=1 -v .
func TestAutoPowerOff(t *testing.T) {
	e := poweroffState(t)
	ips := uint64(70_000_000)
	start := e.cpu.cycles
	hits := 0
	e.cpu.hlePC = 0x802aea26
	e.cpu.hle = func() bool {
		hits++
		t.Logf("PowerOff at %.1f emulated s: r4 (displayLogo)=%d r5=%d pr=%08x",
			float64(e.cpu.cycles-start)/float64(ips), e.cpu.r[4], e.cpu.r[5], e.cpu.pr)
		return false
	}
	for e.cpu.cycles-start < 11*60*ips && hits == 0 {
		e.Step(100_000_000)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
	}
	e.cpu.hlePC, e.cpu.hle = 0, nil
	e.Step(int(3 * ips))
	l := e.mmio.lcd
	t.Logf("after: %.1f s, sleeping=%v R007=%04x R100=%04x port A405012E=%02x, warm flag=%d",
		float64(e.cpu.cycles-start)/float64(ips), e.cpu.sleeping, l.reg[0x007], l.reg[0x100],
		e.mem.R8(0xA405012E), e.mem.R32(warmFlagAddr))
	savePNG(t, e, "poweroff_auto")
	if hits == 0 {
		t.Error("no PowerOff within 11 minutes")
	}
}

// poweroffState resumes the 32 MB image's state if present (the Mac), else the 16 MB one's.
func poweroffState(t *testing.T) *Emulator {
	if _, err := os.Stat("../os/flash_dump/flash_full_32mb.bin"); err == nil {
		return installState(t)
	}
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(20_000_000)
	return e
}

func TestPowerOffTrace(t *testing.T) {
	e := poweroffState(t)
	s := &rcSession{t, e}
	if !e.atMainMenu() {
		t.Fatal("state not at the MAIN MENU")
	}
	savePNG(t, e, "poweroff_0_menu")
	maxCalls := 120
	if v, err := strconv.Atoi(os.Getenv("POWEROFF_CALLS")); err == nil {
		maxCalls = v
	}
	watch := map[uint32]string{
		0x801dfe4a: "flag:=1 path A (0x801dfe4a)",
		0x801504e0: "flag:=1 path B (0x801504e0)",
		0x80365fc6: "flag:=1 path C (0x80365fc6)",
		0x801de698: "restart sc 0x1187 (0x801de698)",
		0xa0000000: "RESET VECTOR 0xA0000000",
		0x80000000: "RESET VECTOR 0x80000000",
		0xa0020008: "OS entry 0xA0020008",
		0x80020008: "OS entry 0x80020008",
		0x801dfe90: "OS init 0x801dfe90",
		0x80365238: "boot check 0x80365238",
		0x8035e1be: "SETUP WIZARD 0x8035e1be",
		0x802ae742: "idle routine 0x802ae742",
		0x801deca4: "flag read 0x801deca4",
		0x802aea24: "PowerOff sc 0x1839 (0x802aea24)",
		0x802aea26: "PowerOff body sc 0x183a (0x802aea26)",
		0x802af4fe: "PowerOff logo step 0x802af4fe",
	}
	var log []string
	rec := func(f string, a ...any) {
		if len(log) < 1500 {
			log = append(log, fmt.Sprintf("%11d pc=%08x  ", e.cpu.cycles, e.cpu.pc)+fmt.Sprintf(f, a...))
		}
	}
	mmioN := map[uint32]int{}
	gram := 0
	hook := func(va, size, val uint32) {
		if va >= 0xB4000000 && va < 0xB4020000 {
			l := e.mmio.lcd
			switch {
			case !l.rs():
				// index phase: logged with the data that follows
			case l.idx == 0x202:
				gram++
			default:
				rec("LCD R%03X <- %04x", l.idx, val&0xFFFF)
			}
			return
		}
		if va >= 0xA44B0000 && va < 0xA44B0100 { // KEYSC: noisy, count only
			mmioN[va]++
			return
		}
		mmioN[va]++
		if mmioN[va] <= 4 {
			rec("W%d %08x <- %x", size*8, va, val)
		}
	}

	s.tap([2]uint32{6, 8}) // SHIFT
	e.mem.mmioHook = hook
	defer func() { e.mem.mmioHook = nil }()
	// A wake-up inside a 1M-instruction sleeping step runs unobserved, so PowerOff's entry is
	// caught by the HLE hook (a breakpoint that never replaces the routine).
	e.cpu.hlePC = 0x802aea26
	e.cpu.hle = func() bool {
		rec("PowerOff body 0x802aea26: r4 (displayLogo)=%d r5=%d pr=%08x", e.cpu.r[4], e.cpu.r[5], e.cpu.pr)
		return false
	}

	run := func(phase string, budget uint64) {
		rec("=== %s", phase)
		seen := map[uint32]int{}
		callees := map[uint32]bool{}
		flag := e.mem.R32(warmFlagAddr)
		rec("warm flag = %d", flag)
		callAt, callN := uint32(0), 0
		start := e.cpu.cycles
		wasSleeping := e.cpu.sleeping
		sleeps := 0
		for e.cpu.cycles-start < budget {
			if e.cpu.sleeping {
				e.Step(1_000_000)
			} else {
				pc := e.cpu.pc
				if callN == 0 && len(callees) < maxCalls {
					op := e.mem.R16(pc)
					if op&0xF0FF == 0x400B || op&0xF000 == 0xB000 || op&0xF0FF == 0x0003 {
						callAt, callN = pc, 2
					}
				}
				e.Step(1)
				if callN > 0 {
					callN--
					if callN == 0 && !callees[e.cpu.pc] {
						callees[e.cpu.pc] = true
						rec("call %08x -> %08x", callAt, e.cpu.pc)
					}
				}
			}
			if f := e.Fault(); f != "" {
				rec("FAULT %s", f)
				break
			}
			if name, ok := watch[e.cpu.pc]; ok && !e.cpu.sleeping {
				seen[e.cpu.pc]++
				if seen[e.cpu.pc] <= 3 {
					rec("enter %s (pr=%08x)", name, e.cpu.pr)
				}
			}
			if v := e.mem.R32(warmFlagAddr); v != flag {
				rec("warm flag %d -> %d", flag, v)
				flag = v
			}
			if e.cpu.sleeping != wasSleeping {
				wasSleeping = e.cpu.sleeping
				if wasSleeping {
					sleeps++
					if sleeps <= 6 {
						rec("SLEEP sr=%08x stbcr(0xA4150020)=%08x", e.cpu.sr, e.mem.R32(0xA4150020))
					}
				} else if sleeps <= 6 {
					rec("wake")
				}
			}
		}
		rec("end of %s: %d sleeps, %d GRAM pixel writes so far, menu=%v", phase, sleeps, gram, e.atMainMenu())
	}

	flash0 := e.mem.flashDeltaBytes()
	flashCopy := append([]byte(nil), e.mem.flash...)
	e.InjectKey(0, 0) // AC/ON while SHIFT is latched = OFF
	run("SHIFT+AC/ON (power off)", 300_000_000)
	rec("flash array changed during power-off: %v", !bytes.Equal(flash0, e.mem.flashDeltaBytes()))
	{
		n, lo, hi := 0, -1, -1
		blocks := map[int]int{}
		for i := range flashCopy {
			if flashCopy[i] != e.mem.flash[i] {
				n++
				if lo < 0 {
					lo = i
				}
				hi = i
				blocks[i>>16]++
			}
		}
		rec("flash bytes changed: %d, range %#x..%#x, 64 KB blocks %v", n, lo, hi, blocks)
	}
	savePNG(t, e, "poweroff_1_off")
	l := e.mmio.lcd
	rec("LCD after off: R007=%04x R100=%04x R101=%04x R102=%04x R103=%04x R010=%04x",
		l.reg[0x007], l.reg[0x100], l.reg[0x101], l.reg[0x102], l.reg[0x103], l.reg[0x010])
	rec("GRAM after off (RGB565): OS area %04x %04x, bottom band %04x, side band %04x",
		l.gram[100*panelW+200], l.gram[20*panelW+300], l.gram[220*panelW+200], l.gram[100*panelW+2])

	e.InjectKey(0, 0) // AC/ON = power on
	run("AC/ON (power on)", 800_000_000)
	savePNG(t, e, "poweroff_2_on")

	for _, line := range log {
		t.Log(line)
	}
	t.Logf("KEYSC writes: %d", func() (n int) {
		for a, c := range mmioN {
			if a >= 0xA44B0000 && a < 0xA44B0100 {
				n += c
			}
		}
		return
	}())
}
