//go:build probe

package main

// Probe (2026-10-04, on-device debugger stage 2): DASM's resident monitor debugging another
// add-in, end to end on the 32 MB dump resumed at the real MAIN MENU.
//
// DASM (MONDBG_DASM, default ../../cg50_addons/dasm/DASM.g3a; its monitor ELF next to it in
// build-cg/monitor.elf) is started from its icon Z with that build swapped in (swapAddinCode,
// dasm_real_test.go). In its file picker F2 installs the monitor at 0x8C4E0000; EXE arms UBC
// channel 0 BEFORE 0x00300000 from inside the OS world and opens the MAIN MENU.
//
//   TestMonitorDasm        target = Casio's Geometry (icon J, 4x UP from Z):
//     - the break lands in the monitor (pc in its image, DBR = its entry), SPC = 0x00300000;
//     - F1 single steps (SPC moves, the monitor's stop counter increments);
//     - the cursor + F3 put a breakpoint after a call (call + 4), EXE stops there;
//     - F2 steps over the next call (stop at call + 4);
//     - no channel is enabled while the monitor runs; EXIT detaches (both channels off) and
//       the target runs on without faulting.
//   TestMonitorDasmGint    target = KeyProbe (icon X, 2x LEFT from Z), a gint add-in. A first run
//     finds the add-in-code PC its key-wait loop passes most often; then the entry stop, a
//     breakpoint there (written into the monitor's state S: the cursor can't travel that far),
//     EXE: a stop inside the target's own gint world (VBR 0x8C160000), F1 steps, EXIT, and the
//     target draws its next frame (gint's DMA push after the monitor used the LCD).
//     MONDBG_BASELINE=1: hold RIGHT in KeyProbe without the debugger — the emulator faults in
//     gint's interrupt handler (TLB miss on add-in RAM with SR.BL=1) after ~7 s either way.
//   TestMonitorDasmCancel  from the MAIN MENU back into DASM: everything disarmed.
// Screenshots of the whole 396x224 panel: $TMPDIR/cg50_mondbg_*.png.
//   go -C emu_go test -tags probe -run TestMonitorDasm -count=1 -v .

import (
	"debug/elf"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

const monBase = 0x8C4E0000

// savePanel writes the whole 396x224 LCD panel (GRAM) to $TMPDIR/cg50_<name>.png.
func savePanel(t *testing.T, e *Emulator, name string) {
	buf := make([]byte, panelW*panelH*2)
	e.mmio.lcd.renderPanel(buf)
	img := image.NewRGBA(image.Rect(0, 0, panelW, panelH))
	for p := 0; p < panelW*panelH; p++ {
		c := uint16(buf[2*p])<<8 | uint16(buf[2*p+1])
		img.SetRGBA(p%panelW, p/panelW, color.RGBA{uint8(c>>11) << 3, uint8(c>>5&0x3F) << 2, uint8(c&0x1F) << 3, 255})
	}
	path := fmt.Sprintf("%s/cg50_%s.png", os.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
	t.Logf("saved %s", path)
}

// addinCode reads the 16-bit instruction at add-in virtual address va through the OS's add-in
// page table (as its TLB-miss handler does), so the test needs no MMU state.
func addinCode(e *Emulator, va uint32) (uint16, bool) {
	if va < 0x00300000 || va >= 0x00500000 {
		return 0, false
	}
	ent := e.mem.R32(0x8C04CF0C + (va-0x00300000)>>12*4)
	if ent == 0 {
		return 0, false
	}
	p := ent&0x1FFFF000 | va&0xFFF
	return uint16(e.mem.flash[p])<<8 | uint16(e.mem.flash[p+1]), true
}

func isCall(op uint16) bool {
	return op&0xF000 == 0xB000 || op&0xF0FF == 0x400B || op&0xF0FF == 0x0003 // bsr, jsr, bsrf
}

type monRig struct {
	t                     *testing.T
	e                     *Emulator
	dasm                  string
	entry, imgEnd, stopAt uint32
	n                     uint32 // stops seen
	shots                 string // screenshot name prefix
}

func newMonRig(t *testing.T, shots string) *monRig {
	flash, err := os.ReadFile("../os/flash_dump/flash_full_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB save-state")
	}
	r := &monRig{t: t, dasm: os.Getenv("MONDBG_DASM"), shots: shots}
	if r.dasm == "" {
		r.dasm = "../../cg50_addons/dasm/DASM.g3a"
	}
	r.e = NewEmulator(flash)
	if err := r.e.Resume(st); err != nil {
		t.Fatal(err)
	}
	r.e.SetInstrPerSecond(70_000_000)
	r.e.Step(20_000_000)
	return r
}

func (r *monRig) shot(name string) { savePanel(r.t, r.e, r.shots+"_"+name) }

func (r *monRig) key(row, col uint32, n int) {
	r.e.InjectKey(row, col)
	r.e.Step(n)
	if f := r.e.Fault(); f != "" {
		r.t.Fatal(f)
	}
}

func (r *monRig) launchFromMenu(keys [][2]uint32) {
	for _, k := range keys {
		r.key(k[0], k[1], 30_000_000)
	}
	r.e.InjectKey(2, 1)
	for i := 0; r.e.cpu.pc != 0x00300000; i++ {
		if i > 200_000_000 {
			r.t.Fatal("the add-in never started")
		}
		r.e.Step(1)
	}
}

// armFromDasm: DASM from icon Z, F2 (install + explanation), EXE (arm + MAIN MENU).
func (r *monRig) armFromDasm() {
	t, e := r.t, r.e
	r.launchFromMenu([][2]uint32{{2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {2, 7}, {1, 7}, {1, 7}})
	swapAddinCode(t, e, r.dasm)
	r.key(2, 7, 60_000_000) // throwaway DOWN (the first key after launch is swallowed)
	r.key(5, 9, 60_000_000) // F2: debug the next add-in
	r.shot("01_explain")
	if e.mem.R32(monBase) != 0x4D4F4E31 {
		t.Fatalf("no monitor at %#x: %#x", monBase, e.mem.R32(monBase))
	}
	r.entry, r.imgEnd, r.stopAt = e.mem.R32(monBase+8), e.mem.R32(monBase+12), e.mem.R32(monBase+24)
	t.Logf("monitor: entry %#x image end %#x stops @%#x", r.entry, r.imgEnd, r.stopAt)
	r.key(2, 1, 100_000_000) // EXE: arm + MAIN MENU
	r.shot("02_menu")
	t.Logf("armed: MSTPCR0=%#x DBR=%#x CBCR=%#x CBR0=%#x CAR0=%#x", e.mem.Read(0xA4150030, 4), e.cpu.dbr,
		e.mem.Read(ubcBase+ubcCBCR, 4), e.mem.Read(ubcBase, 4), e.mem.Read(ubcBase+8, 4))
	if e.cpu.dbr != r.entry || e.mem.Read(ubcBase, 4)&1 == 0 || e.mem.Read(ubcBase+8, 4) != 0x00300000 ||
		e.mem.Read(0xA4150030, 4)&mstpcr0UDB != 0 {
		t.Fatal("not armed")
	}
}

func (r *monRig) inMon() bool   { return r.e.cpu.pc >= monBase && r.e.cpu.pc < r.imgEnd }
func (r *monRig) stops() uint32 { return r.e.mem.R32(r.stopAt) }

// waitStop runs until the monitor shows its next stop (and waits for a key); returns SPC.
func (r *monRig) waitStop(what string) uint32 {
	t, e := r.t, r.e
	r.n++
	for i := 0; i < 800; i++ {
		e.Step(1_000_000)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
		if r.stops() >= r.n && r.inMon() {
			e.Step(8_000_000) // let it draw and reach its key loop
			if c0, c1 := e.mem.Read(ubcBase, 4), e.mem.Read(ubcBase+0x20, 4); (c0|c1)&1 != 0 {
				t.Fatalf("a channel is enabled while the monitor runs: CBR0=%#x CBR1=%#x", c0, c1)
			}
			t.Logf("stop %d (%s): spc=%#x ssr=%#x r15=%#x vbr=%#x", r.n, what, e.cpu.spc, e.cpu.ssr, e.cpu.sgr, e.cpu.vbr)
			return e.cpu.spc
		}
	}
	t.Fatalf("no stop %d (%s): pc=%#x spc=%#x breaks=%d CBR0=%#x CBR1=%#x CAR1=%#x", r.n, what, e.cpu.pc, e.cpu.spc,
		e.cpu.ubc.breaks, e.mem.Read(ubcBase, 4), e.mem.Read(ubcBase+0x20, 4), e.mem.Read(ubcBase+0x28, 4))
	return 0
}

// resume presses a key that leaves the monitor and waits for the next stop.
func (r *monRig) resume(row, col uint32, what string) uint32 {
	r.e.InjectKey(row, col)
	return r.waitStop(what)
}

// detach: EXIT, then the target must run on with both channels off.
func (r *monRig) detach() {
	t, e := r.t, r.e
	breaks := e.cpu.ubc.breaks
	e.InjectKey(3, 7)
	e.Step(30_000_000)
	if r.inMon() {
		t.Fatalf("still in the monitor after EXIT: pc=%#x", e.cpu.pc)
	}
	if e.mem.Read(ubcBase, 4)&1 != 0 || e.mem.Read(ubcBase+0x20, 4)&1 != 0 {
		t.Fatalf("channels still on after detach: CBR0=%#x CBR1=%#x", e.mem.Read(ubcBase, 4), e.mem.Read(ubcBase+0x20, 4))
	}
	e.Step(600_000_000)
	if f := e.Fault(); f != "" {
		t.Fatal(f)
	}
	if e.cpu.ubc.breaks != breaks {
		t.Fatalf("breaks after detach: %d -> %d", breaks, e.cpu.ubc.breaks)
	}
	t.Logf("after detach: pc=%#x vbr=%#x stops=%d", e.cpu.pc, e.cpu.vbr, r.stops())
}

func TestMonitorDasm(t *testing.T) {
	r := newMonRig(t, "mondbg")
	r.armFromDasm()
	for i := 0; i < 4; i++ { // Z -> J (Geometry)
		r.key(1, 8, 30_000_000)
	}
	r.shot("03_menu_geometry")
	r.e.InjectKey(2, 1) // EXE: open Geometry
	pc := r.waitStop("entry")
	r.shot("04_entry")
	if pc != 0x00300000 || r.e.cpu.dbr != r.entry {
		t.Fatalf("entry stop at %#x (DBR %#x), want 0x00300000", pc, r.e.cpu.dbr)
	}

	for i := 0; i < 3; i++ { // F1: three single steps
		npc := r.resume(6, 9, "F1 step")
		if npc == pc {
			t.Fatalf("step %d did not move: %#x", i+1, npc)
		}
		pc = npc
	}
	r.shot("05_step3")

	nextCall := func() uint32 { // step on until PC is on a call
		for i := 0; i < 400; i++ {
			if op, ok := addinCode(r.e, pc); ok && isCall(op) {
				return pc
			}
			pc = r.resume(6, 9, "F1 step")
		}
		t.Fatal("no call within 400 steps")
		return 0
	}
	call := nextCall()
	t.Logf("call at %#x: breakpoint at %#x", call, call+4)
	r.key(2, 7, 20_000_000) // DOWN, DOWN: the cursor on call+4
	r.key(2, 7, 20_000_000)
	r.key(4, 9, 20_000_000) // F3: breakpoint there
	r.shot("06_bp_set")
	pc = r.resume(2, 1, "EXE continue to the breakpoint")
	r.shot("07_at_bp")
	if pc != call+4 {
		t.Fatalf("continue stopped at %#x, want the breakpoint %#x", pc, call+4)
	}
	pc = r.resume(6, 9, "F1 step")
	call = nextCall()
	t.Logf("next call at %#x: F2", call)
	pc = r.resume(5, 9, "F2 step over")
	r.shot("08_over")
	if pc != call+4 {
		t.Fatalf("step over stopped at %#x, want %#x", pc, call+4)
	}
	r.detach()
	r.shot("09_detached")
}

// monSymbol finds a symbol of the monitor build next to the DASM.g3a under test.
func monSymbol(t *testing.T, dasm, name string) uint32 {
	f, err := elf.Open(filepath.Join(filepath.Dir(dasm), "build-cg", "monitor.elf"))
	if err != nil {
		t.Skip("no monitor.elf: ", err)
	}
	defer f.Close()
	syms, _ := f.Symbols()
	for _, s := range syms {
		if s.Name == name {
			return uint32(s.Value)
		}
	}
	t.Fatalf("no symbol %s in monitor.elf", name)
	return 0
}

func TestMonitorDasmGint(t *testing.T) {
	// the target: KeyProbe (icon X: from the first icon 8x DOWN; from Z 2x LEFT), a gint add-in
	// that draws its screen and polls the keyboard in a loop
	var path [][2]uint32
	for i := 0; i < 8; i++ {
		path = append(path, [2]uint32{2, 7})
	}
	// 1. where does it loop? the add-in-code PC seen most often while it waits
	r := newMonRig(t, "mondbg_gint")
	r.launchFromMenu(path)
	r.e.Step(300_000_000)
	seen := map[uint32]int{}
	for i := 0; i < 400; i++ {
		r.e.Step(100_003)
		if pc := r.e.cpu.pc; pc >= 0x00300000 && pc < 0x00500000 {
			seen[pc]++
		}
	}
	idle, best := uint32(0), 0
	for pc, n := range seen {
		if n > best || n == best && pc < idle {
			idle, best = pc, n
		}
	}
	r.shot("00_plain")
	if best < 5 || r.e.cpu.vbr != 0x8C160000 {
		t.Fatalf("no loop found in the add-in (best %#x x%d, vbr %#x)", idle, best, r.e.cpu.vbr)
	}
	t.Logf("the add-in loops through %#x (%d of 400 samples, vbr %#x)", idle, best, r.e.cpu.vbr)
	if os.Getenv("MONDBG_BASELINE") != "" { // the same RIGHT hold without the debugger
		p0 := r.e.Pushes()
		r.e.KeyDown(1, 7)
		r.e.Step(700_000_000)
		r.e.KeyUp(1, 7)
		r.e.Step(300_000_000)
		r.shot("00_plain_right")
		t.Fatalf("baseline: fault %q pushes %d -> %d", r.e.Fault(), p0, r.e.Pushes())
	}

	// 2. debug it
	r = newMonRig(t, "mondbg_gint")
	r.armFromDasm()
	for i := 0; i < 2; i++ { // Z -> X
		r.key(2, 8, 30_000_000)
	}
	r.e.InjectKey(2, 1)
	if pc := r.waitStop("entry"); pc != 0x00300000 {
		t.Fatalf("entry stop at %#x", pc)
	}
	r.shot("04_entry")
	sAddr := monSymbol(t, r.dasm, "_S") // static struct S: started, ch1, bp, bp_on
	r.e.mem.W32(sAddr+8, idle)
	r.e.mem.W32(sAddr+12, 1)
	pc := r.resume(2, 1, "EXE continue to the loop")
	r.shot("05_gint_bp")
	if pc != idle || r.e.cpu.vbr != 0x8C160000 {
		t.Fatalf("stop at %#x vbr %#x, want %#x in gint's world (vbr 0x8c160000)", pc, r.e.cpu.vbr, idle)
	}
	for i := 0; i < 3; i++ {
		pc = r.resume(6, 9, "F1 step")
	}
	r.shot("06_gint_step")
	r.detach()
	r.shot("07_detached")
	// the target still answers keys and draws: KeyProbe wants RIGHT held for ~7 s
	// (stop at its next frame: holding on makes the emulator fault in gint's interrupt handler,
	// with or without the debugger — see MONDBG_BASELINE)
	p0 := r.e.Pushes()
	r.e.KeyDown(1, 7)
	for i := 0; i < 100 && r.e.Pushes() == p0; i++ {
		r.e.Step(10_000_000)
		if f := r.e.Fault(); f != "" {
			t.Fatal(f)
		}
	}
	r.shot("08_after_key")
	if r.e.Pushes() == p0 {
		t.Fatal("the target did not draw again after the detach")
	}
	t.Logf("the target drew a new frame after the detach (pushes %d -> %d)", p0, r.e.Pushes())
}

func TestMonitorDasmCancel(t *testing.T) {
	r := newMonRig(t, "mondbg_cancel")
	r.armFromDasm()
	// EXE on DASM's own icon: the OS returns into DASM, which disarms everything
	r.key(2, 1, 200_000_000)
	r.shot("03_back")
	e := r.e
	t.Logf("back in DASM: pc=%#x MSTPCR0=%#x DBR=%#x CBR0=%#x CBR1=%#x stops=%d", e.cpu.pc, e.mem.Read(0xA4150030, 4),
		e.cpu.dbr, e.mem.Read(ubcBase, 4), e.mem.Read(ubcBase+0x20, 4), r.stops())
	if e.mem.Read(ubcBase, 4)&1 != 0 || e.mem.Read(ubcBase+0x20, 4)&1 != 0 || e.cpu.dbr == r.entry || r.stops() != 0 {
		t.Fatal("still armed after coming back to DASM")
	}
	if e.cpu.pc < 0x00300000 || e.cpu.pc >= 0x00500000 {
		if !(e.cpu.pc >= 0x8C000000 && e.cpu.pc < 0x8C200000) { // DASM's code or gint's VBR area
			t.Logf("note: pc %#x", e.cpu.pc)
		}
	}
}
