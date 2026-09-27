//go:build probe

package main

// Investigation probes used to find the cursor blink (cont.18o); not part of the normal
// suite. Run with
//   go -C emu_go test -tags probe -run TestCursorProbe -v   (or -run TestLCDInitProbe)
// TestCursorProbe enters Run-Matrix from the menu save-state, types digits, and: attributes
// every VRAM write to its call chain (shadow call stack) with a pixel bounding box; counts the
// cursor-module entry points (sc 0x8C7..0x8D2) with callers; traces one blink's MMIO writes;
// captures the LCD index/data protocol of a full push and a cursor draw; and logs the calls
// made in each idle wake-up. TestLCDInitProbe logs the boot's LCD entry-mode/window writes.
// The shadow stack, Memory.wrHook and Memory.mmioHook are reusable for similar hunts.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

type wstat struct {
	n              int
	x0, y0, x1, y1 int
}

// shadow call stack of call-site PCs, maintained per single-step
type shadow struct {
	st    []uint32
	irq   uint64
	calls []uint32 // targets entered (optional log)
	logOn bool
}

const irqMark = 0xFFFFFFFF

func (s *shadow) step(e *Emulator) {
	pc0 := e.cpu.pc
	op := e.mem.R16(pc0)
	irq0 := e.cpu.irqCnt
	e.Step(1)
	if e.cpu.irqCnt != irq0 {
		s.st = append(s.st, irqMark)
		if s.logOn {
			s.calls = append(s.calls, irqMark)
		}
		return
	}
	switch {
	case op&0xF0FF == 0x400B || op&0xF000 == 0xB000 || op&0xF0FF == 0x0003: // jsr/bsr/bsrf
		if e.cpu.pr == pc0+4 {
			s.st = append(s.st, pc0)
			if s.logOn {
				s.calls = append(s.calls, e.cpu.pc)
			}
		}
	case op == 0x000B: // rts
		if n := len(s.st); n > 0 && s.st[n-1] != irqMark {
			s.st = s.st[:n-1]
		}
	case op == 0x002B: // rte
		for n := len(s.st); n > 0; n = len(s.st) {
			top := s.st[n-1]
			s.st = s.st[:n-1]
			if top == irqMark {
				break
			}
		}
	}
}

func (s *shadow) sig(depth int) string {
	var b strings.Builder
	for i := len(s.st) - 1; i >= 0 && i >= len(s.st)-depth; i-- {
		fmt.Fprintf(&b, "%08x ", s.st[i])
	}
	return b.String()
}

func TestCursorProbe(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no menu save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	const ips = 70_000_000
	e.SetInstrPerSecond(ips)
	e.Step(1_000_000)
	e.InjectKey(2, 1) // EXE -> Run-Matrix
	e.Step(20_000_000)
	e.InjectKey(6, 2) // '1'
	e.Step(20_000_000)

	var sh shadow
	stats := map[string]*wstat{}
	e.mem.wrLo, e.mem.wrHi, e.mem.wrHook = DramBase, DramBase+fbBytes, func(p, sz, v uint32) {
		off := int(p - DramBase)
		x, y := (off/2)%FbWidth, (off/2)/FbWidth
		k := sh.sig(7)
		s := stats[k]
		if s == nil {
			s = &wstat{x0: x, y0: y, x1: x, y1: y}
			stats[k] = s
		}
		s.n++
		s.x0, s.y0 = min(s.x0, x), min(s.y0, y)
		s.x1, s.y1 = max(s.x1, x), max(s.y1, y)
	}

	e.InjectKey(5, 2) // '2'
	for i := 0; i < 20_000_000; i++ {
		sh.step(e)
	}
	var keys []string
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("=== key '2' redraw: %d distinct VRAM-writing call chains (innermost call site first)", len(keys))
	for _, k := range keys {
		s := stats[k]
		t.Logf("  n=%6d x[%3d..%3d] y[%3d..%3d] (%3dx%3d)  %s", s.n, s.x0, s.x1, s.y0, s.y1,
			s.x1-s.x0+1, s.y1-s.y0+1, k)
	}

	// cursor module (sc 0x8C7..0x8D2): which entries run, from where, and the flag bytes
	e.mem.wrHook = nil
	cur := map[uint32]string{0x800c3eb8: "Cursor_SetFlashOn", 0x800c3f36: "Cursor_SetFlashOff",
		0x800c3f68: "Keyboard_CursorFlash", 0x800c3f8c: "sc8CB", 0x800c3fa8: "sc8CC",
		0x800c3fc8: "toggle+draw", 0x800c4006: "draw", 0x800c4266: "erase", 0x800c434a: "SetFlashToggle",
		0x800c4344: "getToggle", 0x800c4396: "fb44-path"}
	flags := func() string {
		return fmt.Sprintf("fb3a=%02x fb44=%02x fb38=%04x a3c=%08x", e.mem.R8(0x8c04fb3a), e.mem.R8(0x8c04fb44),
			e.mem.R16(0x8c04fb38), e.mem.R32(0xfd801a3c))
	}
	cursorRun := func(tag string, n int) {
		cnt := map[string]int{}
		for i := 0; i < n; i++ {
			sh.step(e)
			if name, ok := cur[e.cpu.pc]; ok {
				caller := uint32(0)
				if len(sh.st) > 0 {
					caller = sh.st[len(sh.st)-1]
				}
				cnt[fmt.Sprintf("%-22s from %08x", name, caller)]++
			}
		}
		t.Logf("=== %s: %s", tag, flags())
		var ks []string
		for k := range cnt {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			t.Logf("  %5d  %s", cnt[k], k)
		}
	}
	t.Logf("before: %s", flags())
	e.InjectKey(6, 2) // '1' again
	cursorRun("key '1' (20M)", 20_000_000)
	cursorRun("idle 1.5 s", 3*ips/2)

	// one blink: run to the next cursor draw/erase, then log every MMIO write + the
	// visited PCs inside the LCD-window sender 0x801f0c28..0x801f0f58 until it returns.
	for i := 0; i < 2*ips; i++ {
		sh.step(e)
		if e.cpu.pc == 0x800c4006 || e.cpu.pc == 0x800c4266 {
			break
		}
	}
	t.Logf("=== blink at pc=%08x  %s  guards: dd0=%08x 17cb=%02x 2d20=%04x 2d24=%04x", e.cpu.pc, flags(),
		e.mem.R32(0xfd801dd0), e.mem.R8(0xfd8017cb), e.mem.R16(0x8c092d20), e.mem.R16(0x8c092d24))
	depth := len(sh.st)
	mm := 0
	e.mem.mmioHook = func(va, size, val uint32) {
		if mm < 60 {
			t.Logf("   mmio w%d %08x = %08x  (pc %08x)", size*8, va, val, e.cpu.pc)
		}
		mm++
	}
	visited := map[uint32]bool{}
	var path []uint32
	for i := 0; i < 5_000_000; i++ {
		sh.step(e)
		pc := e.cpu.pc
		if pc >= 0x801f0c28 && pc < 0x801f0f58 && !visited[pc] {
			visited[pc] = true
			path = append(path, pc)
		}
		if len(sh.st) < depth {
			break
		}
	}
	e.mem.mmioHook = nil
	t.Logf("   total mmio writes: %d; 0x801f0c28 path (%d PCs):", mm, len(path))
	var pb strings.Builder
	for _, p := range path {
		fmt.Fprintf(&pb, "%x ", p&0xFFFF)
	}
	t.Logf("   %s", pb.String())

	// LCD protocol capture: index/data (RS = PFC 0xA405013C bit4) for a full push (key) and a
	// cursor draw blink; data runs are collapsed.
	lcdLog := func(tag string, stop func() bool, max int) {
		var lines []string
		run, lastIdx := 0, uint32(0)
		flush := func() {
			if run > 0 {
				lines = append(lines, fmt.Sprintf("    ... %d more data writes to idx %03x", run, lastIdx))
				run = 0
			}
		}
		idxSeen := 0
		e.mem.mmioHook = func(va, size, val uint32) {
			if va&0x1FFFFFFF < 0x14000000 || va&0x1FFFFFFF >= 0x14020000 {
				if va&0x1FFFFFFF >= 0x1E008000 && va&0x1FFFFFFF < 0x1E008070 {
					lines = append(lines, fmt.Sprintf("  DMAC %08x = %08x", va, val))
				}
				return
			}
			rs := e.mmio.Read(0xA405013C, 1) & 0x10
			if rs == 0 {
				flush()
				lastIdx = val
				idxSeen = 0
				lines = append(lines, fmt.Sprintf("  IDX %03x (pc %08x)", val, e.cpu.pc))
			} else if idxSeen < 2 {
				idxSeen++
				lines = append(lines, fmt.Sprintf("  DAT %04x", val))
			} else {
				run++
			}
		}
		for i := 0; i < 40_000_000 && !stop(); i++ {
			sh.step(e)
		}
		flush()
		e.mem.mmioHook = nil
		t.Logf("=== LCD protocol: %s (%d lines)", tag, len(lines))
		for i, l := range lines {
			if i >= max {
				break
			}
			t.Log(l)
		}
	}
	p0 := e.Pushes()
	e.InjectKey(5, 2)
	lcdLog("key '2' until first full push +100k", func() bool { return e.Pushes() > p0 }, 80)
	lcdLog("next cursor DRAW", func() bool { return e.cpu.pc == 0x800c3ff2 }, 80)

	// idle: calls made in each wake episode
	sh.logOn = true
	wakes := 0
	was := e.cpu.sleeping
	for i := 0; i < 3*ips && wakes < 8; i++ {
		sh.step(e)
		if was && !e.cpu.sleeping {
			sh.calls = sh.calls[:0]
		}
		if !was && e.cpu.sleeping {
			wakes++
			seen := map[uint32]bool{}
			var b strings.Builder
			for _, c := range sh.calls {
				if !seen[c] {
					seen[c] = true
					if c == irqMark {
						b.WriteString("IRQ ")
					} else {
						fmt.Fprintf(&b, "%08x ", c)
					}
				}
			}
			t.Logf("wake %d (calls=%d, unique=%d): %s", wakes, len(sh.calls), len(seen), b.String())
		}
		was = e.cpu.sleeping
	}
}

// LCD init sequence from reset (index + first data words), to learn the GRAM window.
func TestLCDInitProbe(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	e := NewEmulator(flash)
	n, idx, dat := 0, uint32(0), 0
	e.mem.mmioHook = func(va, size, val uint32) {
		p := va & 0x1FFFFFFF
		if p < 0x14000000 || p >= 0x14020000 {
			return
		}
		if e.mmio.Read(0xA405013C, 1)&0x10 == 0 {
			idx, dat = val, 0
			return
		}
		dat++
		if (idx == 0x003 || (idx >= 0x210 && idx <= 0x213)) && n < 300 {
			t.Logf("  @%9d idx %03x = %04x (pc %08x)", e.cpu.cycles, idx, val, e.cpu.pc)
			n++
		}
	}
	for i := 0; i < 400 && n < 300; i++ {
		e.Step(1_000_000)
	}
	t.Logf("done at %d instr, %d non-GRAM writes", e.cpu.cycles, n)
}

// MENU from inside Run-Matrix (resume-notes task 2): does it return to the main menu?
// Compares the displayed frame after MENU with the menu frame seen right after Resume.
func TestMenuFromAppProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	menu := make([]byte, fbBytes)
	e.FramebufferRGB565(menu)
	e.InjectKey(2, 1) // EXE -> Run-Matrix
	e.Step(30_000_000)
	app := make([]byte, fbBytes)
	e.FramebufferRGB565(app)
	e.InjectKey(3, 8) // MENU (0x7533; (3,7) is EXIT — they were swapped before cont.18o)
	cur := make([]byte, fbBytes)
	for i := 1; i <= 12; i++ {
		e.Step(10_000_000)
		e.FramebufferRGB565(cur)
		diff := func(a []byte) int {
			n := 0
			for p := 0; p < len(a); p += 2 {
				if a[p] != cur[p] || a[p+1] != cur[p+1] {
					n++
				}
			}
			return n
		}
		t.Logf("+%3dM instr: pc=%08x pixels differing from menu=%d, from Run-Matrix=%d", i*10, e.cpu.pc, diff(menu), diff(app))
	}
}

// MENU trace: shallow call sequence (relative to the idle stack depth) after MENU in
// Run-Matrix, plus unmapped-MMIO accesses and flash writes during it.
func TestMenuTrace(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	var sh shadow
	for i := 0; i < 1_000_000; i++ {
		sh.step(e)
	}
	e.InjectKey(2, 1) // EXE -> Run-Matrix
	for i := 0; i < 30_000_000; i++ {
		sh.step(e)
	}
	for !e.cpu.sleeping { // settle at idle
		sh.step(e)
	}
	base := len(sh.st)
	t.Logf("idle stack depth %d: %s", base, sh.sig(base))
	for k := range e.mmio.unknown {
		delete(e.mmio.unknown, k)
	}
	e.mem.fwrites = map[uint32]int{}
	e.InjectKey(3, 7) // EXIT (this probe was written while (3,7) was mislabelled MENU)
	lines, lastTop := 0, uint32(0)
	p0 := e.Pushes()
	for i := 0; i < 60_000_000 && lines < 400; i++ {
		pc0 := e.cpu.pc
		d0 := len(sh.st)
		sh.step(e)
		d := len(sh.st)
		if d > d0 && d <= base+2 && sh.st[d-1] != irqMark {
			// a call at shallow depth: log caller site -> target
			if e.cpu.pc != lastTop {
				t.Logf("  @%9d d%+d call %08x -> %08x  pushes=%d", i, d-base, pc0, e.cpu.pc, e.Pushes()-p0)
				lines++
				lastTop = e.cpu.pc
			}
		}
		if d < base && d < d0 {
			t.Logf("  @%9d RETURN below idle depth (d%+d) to %08x", i, d-base, e.cpu.pc)
			lines++
		}
	}
	t.Logf("unmapped MMIO: %v", e.mmio.unknown)
	t.Logf("flash writes by 32KB page: %v", e.mem.fwrites)
}

// menuDiffRun resumes, enters Run-Matrix, presses (row,col) and returns first-call order of
// every call target within n instructions (target -> first instr index), plus the order.
func menuDiffRun(t *testing.T, row, col uint32, n int) (map[uint32]int, []uint32, *Emulator) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	e.InjectKey(2, 1)
	e.Step(30_000_000)
	for !e.cpu.sleeping {
		e.Step(1)
	}
	var sh shadow
	sh.logOn = true
	e.InjectKey(row, col)
	first := map[uint32]int{}
	var order []uint32
	for i := 0; i < n; i++ {
		nc := len(sh.calls)
		sh.step(e)
		if len(sh.calls) > nc {
			c := sh.calls[len(sh.calls)-1]
			if _, ok := first[c]; !ok {
				first[c] = i
				order = append(order, c)
			}
			sh.calls = sh.calls[:0]
		}
	}
	return first, order, e
}

func TestMenuDiff(t *testing.T) {
	const n = 40_000_000
	one, _, _ := menuDiffRun(t, 6, 2, n)   // '1'
	_, order, e := menuDiffRun(t, 3, 7, n) // EXIT: the key that was mislabelled MENU
	shown := 0
	for _, c := range order {
		if _, ok := one[c]; ok || c == irqMark {
			continue
		}
		if shown < 250 {
			t.Logf("  MENU-only call -> %08x", c)
		}
		shown++
	}
	t.Logf("%d MENU-only call targets; unmapped MMIO: %v", shown, e.mmio.unknown)
}
