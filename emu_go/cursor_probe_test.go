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
	"image"
	"image/color"
	"image/png"
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

// Launches the user's custom add-in (menu icon J, selected by the ')' key) and watches for a
// panic (unmapped access), unmapped MMIO, LCD pushes and the PC, saving frames as PNG.
func TestAddinProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	for k := range e.mmio.unknown {
		delete(e.mmio.unknown, k)
	}
	save := func(tag string) { savePNG(t, e, "addin_"+tag) }
	e.InjectKey(3, 5) // ')' = icon J
	defer func() {
		if r := recover(); r != nil {
			t.Logf("PANIC at pc=%08x pr=%08x cycles=%d: %v", e.cpu.pc, e.cpu.pr, e.cpu.cycles, r)
			save("panic")
		}
		t.Logf("unmapped MMIO: %v", e.mmio.unknown)
	}()
	p0 := e.Pushes()
	for i := 1; i <= 30; i++ {
		e.Step(10_000_000)
		t.Logf("+%3dM pc=%08x pr=%08x sleeping=%v pushes=%d unknownMMIO=%d", i*10, e.cpu.pc, e.cpu.pr,
			e.cpu.sleeping, e.Pushes()-p0, len(e.mmio.unknown))
		if i == 1 || i == 5 || i == 30 {
			save(fmt.Sprint(i))
		}
	}
}

// How does the OS map an add-in? Logs MMU register writes (PTEH/PTEL/TTB/TEA/MMUCR/PTEA),
// UTLB/ITLB array accesses (0xF2-0xF7......), and every ldtlb with PTEH/PTEL at that moment.
func TestAddinMMUProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	n := 0
	e.mem.mmioHook = func(va, size, val uint32) {
		if ((va >= 0xFF000000 && va < 0xFF000040) && va != 0xFF000028) || (va >= 0xF0000000 && va < 0xF8000000) {
			if n < 120 {
				t.Logf("  w%d %08x = %08x (pc %08x)", size*8, va, val, e.cpu.pc)
			}
			n++
		}
	}
	e.InjectKey(3, 5) // icon J
	ld := 0
	defer func() { recover() }()
	for i := 0; i < 40_000_000; i++ {
		pc := e.cpu.pc
		if op := e.mem.R16(pc); op == 0x0038 && ld < 40 {
			t.Logf("  ldtlb @%08x PTEH=%08x PTEL=%08x PTEA=%08x MMUCR=%08x", pc,
				e.mem.R32(0xFF000000), e.mem.R32(0xFF000004), e.mem.R32(0xFF000034), e.mem.R32(0xFF000010))
			ld++
		}
		e.Step(1)
		if pc>>28 == 0 && pc < 0x00400000 && ld >= 0 {
			t.Logf("  first execution in U0 add-in space at pc=%08x after %d instr (from pr=%08x)", pc, i, e.cpu.pr)
			ld = -1000
		}
	}
	t.Logf("MMU-range writes: %d, ldtlb seen: %d", n, ld)
}

func TestExcTableProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []uint32{0x040, 0x060, 0x080, 0x0A0, 0x0C0, 0x0E0, 0x100, 0x160, 0x180, 0x1A0} {
		t.Logf("EXPEVT %03x -> handler %08x  imask %02x", ev, e.mem.R32(0xFD8010C8+((ev-0x40)>>5)*4), e.mem.R8(0xFD8012C8+((ev-0x40)>>5)))
	}
}

// savePNG writes the displayed frame to <os.TempDir()>/cg50_<name>.png (probes only).
func savePNG(t *testing.T, e *Emulator, name string) {
	buf := make([]byte, fbBytes)
	e.FramebufferRGB565(buf)
	img := image.NewRGBA(image.Rect(0, 0, FbWidth, FbHeight))
	for p := 0; p < FbWidth*FbHeight; p++ {
		c := uint16(buf[2*p])<<8 | uint16(buf[2*p+1])
		img.SetRGBA(p%FbWidth, p/FbWidth, color.RGBA{uint8(c>>11) << 3, uint8(c>>5&0x3F) << 2, uint8(c&0x1F) << 3, 255})
	}
	path := fmt.Sprintf("%s/cg50_%s.png", os.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Log(err)
		return
	}
	png.Encode(f, img)
	f.Close()
	t.Logf("saved %s", path)
}

// Drives the add-in past its first prompt: a few keys, a frame after each.
func TestAddinInputProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	e.InjectKey(3, 5) // icon J
	e.Step(40_000_000)
	keys := []struct {
		name     string
		row, col uint32
	}{{"1", 6, 2}, {"2", 5, 2}, {"EXE", 2, 1}, {"MENU", 3, 8}}
	defer func() {
		if r := recover(); r != nil {
			t.Logf("PANIC at pc=%08x: %v", e.cpu.pc, r)
			savePNG(t, e, "addin_panic")
		}
	}()
	for i, k := range keys {
		e.InjectKey(k.row, k.col)
		e.Step(40_000_000)
		t.Logf("after %s: pc=%08x sleeping=%v pushes=%d", k.name, e.cpu.pc, e.cpu.sleeping, e.Pushes())
		savePNG(t, e, fmt.Sprintf("addin_key%d", i))
	}
}

// MENU inside the add-in -> "TLB ERROR PC=00000001": ring-buffer the instructions before the
// PC first drops below 0x1000, plus MMU state changes (MMUCR/ldtlb) after the MENU press.
func TestAddinMenuProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	e.InjectKey(3, 5)
	e.Step(40_000_000)
	e.InjectKey(6, 2)
	e.Step(40_000_000)
	nm := 0
	e.mem.mmioHook = func(va, size, val uint32) {
		if va == 0xFF000010 && nm < 40 {
			t.Logf("  MMUCR <- %08x (pc %08x)", val, e.cpu.pc)
			nm++
		}
	}
	type rec struct{ pc, op, r15, pr, sr uint32 }
	var ring [48]rec
	k := 0
	e.InjectKey(3, 8) // MENU
	for i := 0; i < 60_000_000; i++ {
		pc := e.cpu.pc
		op := uint32(0)
		if !e.cpu.sleeping {
			func() { defer func() { recover() }(); op = e.mem.R16(pc) }()
		}
		ring[k%len(ring)] = rec{pc, op, e.cpu.r[15], e.cpu.pr, e.cpu.sr}
		k++
		tea0 := e.mem.mmu.regs[ccnTEA]
		e.Step(1)
		if e.mem.mmu.regs[ccnTEA] != tea0 || e.cpu.pc < 0x1000 {
			t.Logf("MMU fault (or low PC %08x) after %d instr; EXPEVT=%x TEA=%08x MMUCR=%08x SPC=%08x", e.cpu.pc, i,
				e.mem.R32(0xFF000024), e.mem.R32(0xFF00000C), e.mem.R32(0xFF000010), e.cpu.spc)
			for j := k - len(ring); j < k; j++ {
				r := ring[(j+len(ring))%len(ring)]
				t.Logf("   pc=%08x op=%04x r15=%08x pr=%08x sr=%08x", r.pc, r.op, r.r15, r.pr, r.sr)
			}
			return
		}
	}
	t.Log("no MMU fault after MENU")
}

// Replays the phone's snapshot (pulled to the scratchpad) and saves frames: as resumed, after
// idle, after DOWN, after EXE. Reports MMU state and any fault.
func TestPhoneStateProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, err := os.ReadFile(os.TempDir() + "/claude/F--ru-myprojects-may-cg50/32594427-9e61-44ae-974d-8e739bcc3fb7/scratchpad/phone_state.bin")
	if err != nil {
		t.Skip(err)
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(20_000_000)
	t.Logf("resumed pc=%08x AT=%v MMUCR=%08x sleeping=%v", e.cpu.pc, e.mmio.ccn.at, e.mmio.ccn.regs[ccnMMUCR], e.cpu.sleeping)
	savePNG(t, e, "ph_resumed")
	e.Step(20_000_000)
	savePNG(t, e, "ph_idle")
	for _, k := range []struct {
		n    string
		r, c uint32
	}{{"up", 1, 8}, {"down", 2, 7}} {
		e.InjectKey(k.r, k.c)
		e.Step(20_000_000)
		t.Logf("after %s: pc=%08x pushes=%d fault=%q", k.n, e.cpu.pc, e.Pushes(), e.Fault())
		savePNG(t, e, "ph_"+k.n)
	}
}

// Menu auto-repeat throughput: hold RIGHT at the MAIN MENU for 3 emulated seconds at a given
// host speed (instructions per real second) and count completed cursor moves (LCD pushes).
func TestRepeatRateProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	for _, ips := range []uint64{22_000_000, 44_000_000, 66_000_000, 100_000_000, 150_000_000} {
		e := NewEmulator(flash)
		if err := e.Resume(st); err != nil {
			t.Fatal(err)
		}
		e.SetInstrPerSecond(ips)
		e.Step(1_000_000)
		p0 := e.Pushes()
		e.KeyDown(1, 7) // RIGHT
		// first push = the initial press; repeats start after 20 scans (~0.6 s)
		var firstRepeat uint64
		c0 := e.Cycles()
		for e.Cycles()-c0 < 3*ips {
			n := e.Pushes()
			e.Step(100_000)
			if e.Pushes() > n && n > p0 && firstRepeat == 0 {
				firstRepeat = e.Cycles() - c0
			}
		}
		e.KeyUp(1, 7)
		moves := e.Pushes() - p0
		t.Logf("ips=%3dM: %3d pushes in 3 s (first repeat at %.2f s) -> %.1f moves/s after the initial delay",
			ips/1_000_000, moves, float64(firstRepeat)/float64(ips),
			float64(moves-1)/(3-float64(firstRepeat)/float64(ips)))
	}
}

// Where does one MAIN MENU cursor move spend its instructions? Exclusive instruction counts per
// function (function = the call target on top of a shadow call stack), top 30.
func TestMenuMoveProfile(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(100_000_000)
	e.Step(2_000_000)
	for !e.cpu.sleeping {
		e.Step(1)
	}
	var targets []uint32 // shadow stack of function entry addresses
	cur := uint32(0)
	excl := map[uint32]int{}
	total := 0
	e.InjectKey(1, 7) // RIGHT
	woke := false
	for i := 0; i < 20_000_000; i++ {
		if e.cpu.sleeping {
			if woke && i > 200_000 {
				break
			}
			e.Step(1)
			continue
		}
		woke = true
		pc0 := e.cpu.pc
		op := e.mem.R16(pc0)
		irq0 := e.cpu.irqCnt
		e.Step(1)
		excl[cur]++
		total++
		switch {
		case e.cpu.irqCnt != irq0:
			targets = append(targets, cur)
			cur = e.cpu.pc
		case op&0xF0FF == 0x400B || op&0xF000 == 0xB000 || op&0xF0FF == 0x0003:
			if e.cpu.pr == pc0+4 {
				targets = append(targets, cur)
				cur = e.cpu.pc
			}
		case op == 0x000B || op == 0x002B: // rts / rte
			if n := len(targets); n > 0 {
				cur = targets[n-1]
				targets = targets[:n-1]
			}
		}
	}
	type kv struct {
		f uint32
		n int
	}
	var l []kv
	for f, n := range excl {
		l = append(l, kv{f, n})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].n > l[j].n })
	t.Logf("one RIGHT move: %d instructions executed", total)
	for i, x := range l {
		if i >= 30 {
			break
		}
		t.Logf("  %08x %9d  %5.1f%%", x.f, x.n, 100*float64(x.n)/float64(total))
	}
}

// PC histogram (32-byte buckets) for one MAIN MENU cursor move.
func TestMenuMoveHotspots(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(100_000_000)
	e.Step(2_000_000)
	for !e.cpu.sleeping {
		e.Step(1)
	}
	h := map[uint32]int{}
	total := 0
	e.InjectKey(1, 7) // RIGHT
	for i := 0; i < 20_000_000; i++ {
		if e.cpu.sleeping {
			if total > 200_000 {
				break
			}
			e.Step(1)
			continue
		}
		h[e.cpu.pc&^31]++
		total++
		e.Step(1)
	}
	type kv struct {
		a uint32
		n int
	}
	var l []kv
	for a, n := range h {
		l = append(l, kv{a, n})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].n > l[j].n })
	t.Logf("one RIGHT move: %d instructions", total)
	cum := 0
	for i, x := range l {
		if i >= 20 {
			break
		}
		cum += x.n
		t.Logf("  %08x %9d %5.1f%% (cum %5.1f%%)", x.a, x.n, 100*float64(x.n)/float64(total), 100*float64(cum)/float64(total))
	}
}

// What does the OS write to the ADC block (0xA4610000) at idle, and where from? Tells whether
// the 0x560 interrupt is self-restarting (continuous conversions) or started by something else.
func TestADCProbe(t *testing.T) {
	flash, _ := os.ReadFile("../os/flash_dump/flash_full.bin")
	st, _ := os.ReadFile("../os/flash_dump/cg50_state.bin")
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(100_000_000)
	e.Step(5_000_000)
	n := 0
	e.mem.mmioHook = func(va, size, val uint32) {
		if va&^0xFF == 0xA4610000 && n < 24 {
			t.Logf("  @%9d w%d %08x = %04x (pc %08x) irqs=%d", e.cpu.cycles, size*8, va, val, e.cpu.pc, e.mmio.timerTicks)
			n++
		}
	}
	t0 := e.mmio.timerTicks
	e.Step(1_000_000)
	t.Logf("in 1M instr: %d synthetic 0x560 IRQs, %d ADC-block writes logged", e.mmio.timerTicks-t0, n)
}
