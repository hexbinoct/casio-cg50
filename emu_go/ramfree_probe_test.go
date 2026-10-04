//go:build probe

package main

// Probe (2026-10-04, on-device debugger stage 0): which physical RAM stays free (never written,
// ideally never read) while the OS and gint add-ins run — the place for a resident debugger
// monitor (cg50_addons/notes/debugger.md, approach A).
//
// One long session on the 32 MB dump resumed at the MAIN MENU, split in phases: MAIN MENU idle,
// Run-Matrix, Graph, Statistics, Python (shell), Casio's Geometry and 3D Graph add-ins,
// installing DASM through the OS, DASM used (files, function lists incl. the whole OS ROM,
// references), MAIN MENU idle with DASM suspended, Upsilon used, Upsilon idle. Memory.accHook
// marks every read and write (DRAM by physical address, the on-chip memories by P4 address,
// DRAM instruction fetches, DMA source reads) in ramGran-byte blocks with a bit per phase, plus
// the PC of the first writer/reader. The add-ins' MMU mappings and gint's placement (arenas,
// VRAM, stack; symbols from DASM's ELF) are recorded. RAMFREE_SESSION=warm instead tracks a
// warm power-on (boot from reset with the warm flag set, up to the MAIN MENU). Output in
// $RAMFREE_OUT (default os.TempDir()/ramfree, +"_warm"): <region>_{init,final}.bin (contents),
// <region>_{rd,wr}.bin (u32 BE phase mask per block), <region>_{frpc,fwpc}.bin, meta.json.
//
//   TMPDIR=<dir> go -C emu_go test -tags probe -run 'TestRamFree$' -count=1 -v .
//   (RAMFREE_DASM=<DASM.g3a>, default ../../cg50_addons/dasm/DASM.g3a; RAMFREE_ELF=<its ELF>,
//   default build-cg/dasm next to it — gint's symbols are only used if it is the same build)
//
// KhiCAS (icon P) can't be measured: it stops with "unable to load ram part khicas50.882"
// (that file is not in this calculator's storage).

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const ramGran = 64

type ramTrack struct {
	Name       string `json:"name"`
	Base, Size uint32
	buf        []byte
	init       []byte
	rd, wr     []uint32 // phase bitmask per block
	frpc, fwpc []uint32 // pc of the first reader / writer (0 = none)
}

func newRamTrack(name string, base uint32, buf []byte) *ramTrack {
	n := len(buf) / ramGran
	return &ramTrack{Name: name, Base: base, Size: uint32(len(buf)), buf: buf, init: append([]byte(nil), buf...),
		rd: make([]uint32, n), wr: make([]uint32, n), frpc: make([]uint32, n), fwpc: make([]uint32, n)}
}

type ramProbe struct {
	e      *Emulator
	tracks []*ramTrack
	phase  uint32 // bit of the current phase
	names  []string
	instrs []uint64
	t      *testing.T
	out    string
	shot   int
	maps   map[string]map[string]bool // phase -> "vpn -> ppn size" lines seen in the UTLB
}

func (p *ramProbe) hook(addr, size uint32, write bool) {
	var r *ramTrack
	switch {
	case addr-DramBase < DramSize:
		r = p.tracks[0]
	case addr-IlramBase < IlramSize:
		r = p.tracks[1]
	case addr-XyramBase < XyramSize:
		r = p.tracks[2]
	case addr-OcramBase < OcramSize:
		r = p.tracks[3]
	default:
		return // MMIO
	}
	off := addr - r.Base
	end := min(off+size-1, r.Size-1)
	pc := p.e.cpu.pc
	for b := off / ramGran; b <= end/ramGran; b++ {
		if write {
			if r.wr[b] == 0 {
				r.fwpc[b] = pc
			}
			r.wr[b] |= p.phase
		} else {
			if r.rd[b] == 0 {
				r.frpc[b] = pc
			}
			r.rd[b] |= p.phase
		}
	}
}

func (p *ramProbe) begin(name string) {
	p.names = append(p.names, name)
	p.instrs = append(p.instrs, p.e.Executed())
	p.phase = 1 << (len(p.names) - 1)
	p.t.Logf("== phase %d: %s", len(p.names)-1, name)
}

// tap presses a key and lets the machine settle: 30 M instructions, then on until the CPU
// sleeps (waiting for the next key) for long operations.
func (p *ramProbe) tap(r, c uint32) {
	e := p.e
	e.InjectKey(r, c)
	e.Step(30_000_000)
	for n := 0; n < 600 && !e.cpu.sleeping; n++ {
		e.Step(10_000_000)
	}
	if f := e.Fault(); f != "" {
		p.t.Fatal(f)
	}
	if e.mem.mmuAt {
		p.recordUTLB()
	}
}

func (p *ramProbe) keys(ks ...[2]uint32) {
	for _, k := range ks {
		p.tap(k[0], k[1])
	}
}

func (p *ramProbe) snap(tag string) {
	p.shot++
	savePNG(p.t, p.e, fmt.Sprintf("ramfree_%02d_%s", p.shot, tag))
}

func (p *ramProbe) recordUTLB() {
	name := p.names[len(p.names)-1]
	if p.maps[name] == nil {
		p.maps[name] = map[string]bool{}
	}
	for i := range p.e.mmio.ccn.utlb {
		u := &p.e.mmio.ccn.utlb[i]
		if !u.valid() {
			continue
		}
		p.maps[name][fmt.Sprintf("va %08x -> pa %08x size %#x asid %d sh %v", u.vpn&^u.mask(), u.ppn()&^u.mask(), u.mask()+1, u.asid, u.shared())] = true
	}
}

// vread reads a virtual (add-in) address: translated with the current UTLB, then read at the
// physical address; ok=false if it is not mapped.
func (p *ramProbe) vread(va uint32) (uint32, bool) {
	ph, ok := p.vphys(va)
	if !ok {
		return 0, false
	}
	return p.e.mem.R32(0xA0000000 | ph), true
}

// vphys translates an add-in virtual address with the current UTLB (no fault).
func (p *ramProbe) vphys(va uint32) (uint32, bool) {
	if va >= 0x80000000 && va < 0xC0000000 {
		return va & 0x1FFFFFFF, true
	}
	for i := range p.e.mmio.ccn.utlb {
		u := &p.e.mmio.ccn.utlb[i]
		if u.valid() && (va^u.vpn)&^u.mask()&0xFFFFFC00 == 0 {
			return u.ppn()&^u.mask() | va&u.mask(), true
		}
	}
	return 0, false
}

// elfSyms returns the symbols of the add-in's ELF (name -> value), or nil if the ELF is
// missing or is not the build in g3a (its code segment must equal the g3a's code).
func elfSyms(path string, g3a []byte) map[string]uint32 {
	f, err := elf.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	for _, pr := range f.Progs {
		if pr.Vaddr == 0x00300000 {
			b := make([]byte, pr.Filesz)
			pr.ReadAt(b, 0)
			if len(g3a) < 0x7000+len(b) || !bytes.Equal(b, g3a[0x7000:0x7000+len(b)]) {
				return nil
			}
		}
	}
	syms, err := f.Symbols()
	if err != nil {
		return nil
	}
	m := map[string]uint32{}
	for _, s := range syms {
		n := s.Name
		if i := strings.IndexByte(n, '.'); i > 0 { // local statics: _static_ram.1
			n = n[:i]
		}
		m[n] = uint32(s.Value)
	}
	return m
}

// gintInfo logs where gint 2.11 (DASM's build, symbols from its ELF) put its VRAMs, stack,
// heap arenas and world buffers.
func (p *ramProbe) gintInfo(sym map[string]uint32) map[string]string {
	info := map[string]string{}
	add := func(k, f string, a ...any) {
		info[k] = fmt.Sprintf(f, a...)
		p.t.Logf("gint %-22s %s", k, info[k])
	}
	if sym == nil {
		add("error", "no ELF matching the installed DASM.g3a")
	}
	for _, s := range []string{"_gint_vram", "_vram_1", "_vram_2", "_gint_stack_top", "_gint_world_os", "_gint_world_addin"} {
		va, ok := sym[s]
		if !ok {
			continue
		}
		v, ok := p.vread(va)
		if !ok {
			add(s, "unmapped")
			continue
		}
		ph, _ := p.vphys(v)
		add(s, "%08x (phys %08x)", v, ph)
	}
	for _, a := range []struct {
		name string
		va   uint32
	}{{"arena _uram", sym["_static_ram"]}, {"arena _ostk", sym["_os_stack"]}} {
		if a.va == 0 {
			continue
		}
		st, _ := p.vread(a.va + 0x14)
		en, _ := p.vread(a.va + 0x18)
		live, _ := p.vread(a.va + 0x24)
		peak, _ := p.vread(a.va + 0x28)
		vol, _ := p.vread(a.va + 0x2C)
		ps, _ := p.vphys(st)
		add(a.name, "%08x..%08x (phys %08x, %d KB) live %d peak %d total volume %d", st, en, ps, (en-st)>>10, live, peak, vol)
	}
	for _, va := range []uint32{0x00300000, 0x08100000, 0x08108600, 0x0817C000, 0x0817FFFC} {
		ph, ok := p.vphys(va)
		add(fmt.Sprintf("map %08x", va), "phys %08x (mapped %v)", ph, ok)
	}
	add("r15", "%08x", p.e.cpu.r[15])
	add("vbr", "%08x", p.e.cpu.vbr)
	return info
}

func (p *ramProbe) dump(extra map[string]any) {
	os.MkdirAll(p.out, 0755)
	u32 := func(a []uint32) []byte {
		b := make([]byte, 4*len(a))
		for i, v := range a {
			binary.BigEndian.PutUint32(b[4*i:], v)
		}
		return b
	}
	for _, r := range p.tracks {
		for suf, b := range map[string][]byte{"init": r.init, "final": r.buf, "rd": u32(r.rd), "wr": u32(r.wr), "frpc": u32(r.frpc), "fwpc": u32(r.fwpc)} {
			if err := os.WriteFile(filepath.Join(p.out, r.Name+"_"+suf+".bin"), b, 0644); err != nil {
				p.t.Fatal(err)
			}
		}
	}
	maps := map[string][]string{}
	for ph, m := range p.maps {
		for k := range m {
			maps[ph] = append(maps[ph], k)
		}
		sort.Strings(maps[ph])
	}
	meta := map[string]any{"gran": ramGran, "phases": p.names, "phase_start_instr": p.instrs,
		"end_instr": p.e.Executed(), "tracks": p.tracks, "utlb": maps}
	for k, v := range extra {
		meta[k] = v
	}
	b, _ := json.MarshalIndent(meta, "", " ")
	if err := os.WriteFile(filepath.Join(p.out, "meta.json"), b, 0644); err != nil {
		p.t.Fatal(err)
	}
	p.t.Logf("wrote %s", p.out)
}

func newRamProbe(t *testing.T, e *Emulator, out string) *ramProbe {
	p := &ramProbe{e: e, t: t, out: out, maps: map[string]map[string]bool{}}
	p.tracks = []*ramTrack{newRamTrack("dram", DramBase, e.mem.dram), newRamTrack("ilram", IlramBase, e.mem.ilram),
		newRamTrack("xyram", XyramBase, e.mem.xyram), newRamTrack("ocram", OcramBase, e.mem.ocram)}
	e.mem.accHook = p.hook
	return p
}

// keys (row-col, re/KEYMAP.md)
var (
	kMENU, kEXIT, kEXE      = [2]uint32{3, 8}, [2]uint32{3, 7}, [2]uint32{2, 1}
	kUP, kDOWN, kRIGHT      = [2]uint32{1, 8}, [2]uint32{2, 7}, [2]uint32{1, 7}
	kF1, kF2, kF3, kF4, kF6 = [2]uint32{6, 9}, [2]uint32{5, 9}, [2]uint32{4, 9}, [2]uint32{3, 9}, [2]uint32{1, 9}
	kVARS, kXT, kOPTN       = [2]uint32{4, 8}, [2]uint32{6, 6}, [2]uint32{5, 8}
	kPOW, kSQ, kPLUS, kDIV  = [2]uint32{4, 7}, [2]uint32{5, 7}, [2]uint32{3, 2}, [2]uint32{2, 3}
	kMUL, kSIN, kLN         = [2]uint32{3, 3}, [2]uint32{3, 6}, [2]uint32{4, 6}
	kDigit                  = map[byte][2]uint32{'0': {6, 1}, '1': {6, 2}, '2': {5, 2}, '3': {4, 2}, '4': {6, 3}, '5': {5, 3}, '6': {4, 3}, '7': {6, 4}, '8': {5, 4}, '9': {4, 4}}
)

func (p *ramProbe) num(s string) {
	for i := 0; i < len(s); i++ {
		p.tap(kDigit[s[i]][0], kDigit[s[i]][1])
	}
}

func (p *ramProbe) atMenu(what string) {
	if !p.e.atMainMenu() {
		p.snap("notmenu")
		p.t.Fatalf("%s: not at the MAIN MENU", what)
	}
}

// launch starts the MAIN MENU icon at index i (0 = Run-Matrix, rows of 4; in the 32 MB dump:
// 16 H Python, 18 J Geometry, 19 K 3D Graph, 24 P KhiCAS, 27 S Upsilon, 34 Z DASM). The cursor
// is first put back on the first icon: '1' opens Run-Matrix, MENU comes back with the cursor on it.
func (p *ramProbe) launch(i int) {
	p.keys(kDigit['1'], kMENU)
	p.atMenu("launch")
	for n := 0; n < i/4; n++ {
		p.e.InjectKey(kDOWN[0], kDOWN[1])
		p.e.Step(30_000_000)
	}
	for n := 0; n < i%4; n++ {
		p.e.InjectKey(kRIGHT[0], kRIGHT[1])
		p.e.Step(30_000_000)
	}
	p.e.InjectKey(kEXE[0], kEXE[1])
	p.e.Step(30_000_000)
}

// launchAddin starts the add-in at icon index i in a phase "<name>-launch" (the OS's launch
// path) and switches to phase name the moment it starts (pc = 0x00300000).
func (p *ramProbe) launchAddin(i int, name string) {
	p.begin(name + "-launch")
	p.keys(kDigit['1'], kMENU)
	p.atMenu("launch")
	for n := 0; n < i/4; n++ {
		p.e.InjectKey(kDOWN[0], kDOWN[1])
		p.e.Step(30_000_000)
	}
	for n := 0; n < i%4; n++ {
		p.e.InjectKey(kRIGHT[0], kRIGHT[1])
		p.e.Step(30_000_000)
	}
	p.e.InjectKey(kEXE[0], kEXE[1])
	for n := 0; p.e.cpu.pc != 0x00300000; n++ {
		if n > 200_000_000 || p.e.Fault() != "" {
			p.t.Fatalf("%s never started (%s)", name, p.e.Fault())
		}
		p.e.Step(1)
	}
	p.begin(name)
	if code, ok := p.vphys(0x00300000); ok {
		p.t.Logf("%s code page 0 at phys %08x", name, code)
	}
}

// frame reports where the add-in's last LCD frame came from (its VRAM).
func (p *ramProbe) frame() string {
	sar, ok := p.e.mmio.FrameSAR()
	return fmt.Sprintf("LCD DMA source %08x (valid %v)", sar, ok)
}

// leave presses MENU (then EXIT, MENU if needed) and checks the MAIN MENU is up.
func (p *ramProbe) leave(what string) {
	p.keys(kMENU)
	for n := 0; n < 3 && !p.e.atMainMenu(); n++ {
		p.keys(kEXIT, kMENU)
	}
	p.atMenu(what)
}

// startProbe resumes the 32 MB dump at the MAIN MENU with tracking on; the deferred dump writes
// the maps (also after a failure: the phases so far).
func startProbe(t *testing.T, out string) (*ramProbe, map[string]any) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB save-state")
	}
	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	p := newRamProbe(t, e, out)
	extra := map[string]any{}
	t.Cleanup(func() {
		e.mem.accHook = nil
		p.dump(extra)
	})
	return p, extra
}

func TestRamFree(t *testing.T) {
	out := os.Getenv("RAMFREE_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "ramfree")
	}
	switch os.Getenv("RAMFREE_SESSION") {
	case "warm":
		ramFreeWarm(t, out+"_warm")
		return
	}
	dasmPath := os.Getenv("RAMFREE_DASM")
	if dasmPath == "" {
		dasmPath = "../../cg50_addons/dasm/DASM.g3a"
	}
	dasm, err := os.ReadFile(dasmPath)
	if err != nil {
		t.Fatal(err)
	}
	elfPath := os.Getenv("RAMFREE_ELF")
	if elfPath == "" {
		elfPath = filepath.Join(filepath.Dir(dasmPath), "build-cg", "dasm")
	}
	dasmSyms := elfSyms(elfPath, dasm)
	p, extra := startProbe(t, out)
	e := p.e

	p.begin("menu-idle")
	e.Step(20_000_000)
	p.snap("menu")
	e.Step(2_000_000_000)
	p.atMenu("idle")

	p.begin("run-matrix")
	p.keys(kDigit['1'])
	p.num("2")
	p.keys(kPOW)
	p.num("100")
	p.keys(kEXE, kSIN)
	p.num("30")
	p.keys(kEXE)
	p.num("1")
	p.keys(kDIV)
	p.num("7")
	p.keys(kPLUS, kLN)
	p.num("2")
	p.keys(kMUL, kSQ, kEXE)
	p.num("123456789")
	p.keys(kMUL)
	p.num("987654321")
	p.keys(kEXE, kOPTN, kF1, kEXIT, kUP, kUP)
	p.snap("runmat")
	p.leave("after Run-Matrix")

	p.begin("graph")
	p.keys(kDigit['5'])
	p.keys(kXT, kSQ, kEXE, kF6)
	e.Step(300_000_000)
	p.snap("graph_draw")
	p.leave("after Graph")

	p.begin("statistics")
	p.keys(kDigit['2'])
	for _, v := range []string{"1", "4", "9", "16", "25"} {
		p.num(v)
		p.keys(kEXE)
	}
	p.keys(kF2, kF1)
	e.Step(100_000_000)
	p.snap("stats")
	p.leave("after Statistics")

	p.begin("python")
	p.launch(16)
	e.Step(200_000_000)
	p.snap("python_in")
	p.keys(kF4) // SHELL
	e.Step(200_000_000)
	p.num("2")
	p.keys(kPOW)
	p.num("1000")
	p.keys(kEXE)
	p.num("1")
	p.keys(kDIV)
	p.num("7")
	p.keys(kEXE)
	e.Step(100_000_000)
	p.snap("python_shell")
	p.leave("after Python")

	p.begin("geometry") // Casio's add-ins (MMU-mapped, not gint)
	p.launch(18)
	e.Step(300_000_000)
	p.keys(kEXE, kF1, kEXIT, kF2, kEXIT, kDOWN, kEXE)
	p.snap("geometry")
	p.leave("after Geometry")

	p.begin("3d-graph")
	p.launch(19)
	e.Step(300_000_000)
	p.keys(kEXE, kF6)
	e.Step(300_000_000)
	p.snap("3dgraph")
	p.leave("after 3D Graph")

	p.begin("install-dasm")
	if err := e.InstallAddin("DASM.g3a", dasm); err != nil {
		t.Fatal(err)
	}
	e.Step(20_000_000)
	p.atMenu("after install")

	p.launchAddin(34, "dasm")
	e.Step(150_000_000)
	p.recordUTLB()
	p.keys(kDOWN) // swallowed while DASM starts
	p.snap("dasm_picker")
	p.keys(kDOWN, kDOWN, kEXE) // Conv.g3a
	p.snap("dasm_file")
	p.keys(kDOWN, kDOWN, kDOWN, kDOWN, kRIGHT, kRIGHT, kRIGHT, kEXE)
	p.keys(kVARS)
	p.snap("dasm_vars")
	p.keys(kDOWN, kDOWN, kDOWN, kEXE, kXT)
	p.snap("dasm_refs")
	p.keys(kEXE, kF3)
	p.snap("dasm_hex")
	p.keys(kEXIT, kOPTN, kOPTN, kF6) // font swap twice, then the OS ROM
	p.keys(kVARS)                    // analyses the whole OS ROM (~1.8 G instructions)
	p.snap("dasm_rom_vars")
	p.keys(kDOWN, kDOWN, kEXE, kXT)
	p.snap("dasm_rom_refs")
	p.keys(kEXIT, kF1)  // back to the picker
	p.keys(kEXE, kVARS) // the picker is on the last file (khicas50, 2 MB): analysed via BFile
	p.snap("dasm_file2_vars")
	extra["gint_dasm"] = p.gintInfo(dasmSyms)
	extra["dasm_frame"] = p.frame()
	extra["os_addin_page_table"] = fmt.Sprintf("%08x", e.mem.R32(0x8c04cf0c))
	p.leave("after DASM")

	p.begin("menu-idle2") // DASM suspended under the MAIN MENU
	e.Step(1_000_000_000)
	p.atMenu("idle2")

	p.launchAddin(27, "upsilon")
	e.Step(400_000_000)
	if !e.mem.mmuAt {
		t.Fatal("Upsilon did not start")
	}
	p.recordUTLB()
	p.snap("upsilon_home")
	p.keys(kEXE) // Calculation
	p.num("1")
	p.keys(kPLUS)
	p.num("2")
	p.keys(kEXE)
	p.num("2")
	p.keys(kPOW)
	p.num("64")
	p.keys(kEXE, kSIN)
	p.num("1")
	p.keys(kEXE)
	p.snap("upsilon_calc")
	extra["upsilon_r15"] = fmt.Sprintf("%08x", e.cpu.r[15])
	extra["upsilon_frame"] = p.frame()
	// Upsilon has no way back to the MAIN MENU in the emulator (MENU = its own home screen;
	// EXIT, SHIFT+MENU, ALPHA+MENU, AC/ON do nothing; SHIFT+AC/ON enters the OS's power-off),
	// so the session ends idling on its home screen.
	p.keys(kMENU)
	p.begin("upsilon-idle")
	e.Step(1_000_000_000)
	if f := e.Fault(); f != "" || !e.mem.mmuAt {
		t.Fatalf("Upsilon died: %q", f)
	}
	p.snap("end")
	t.Logf("phases %v at instr %v, end %d", p.names, p.instrs, e.Executed())
}

// ramFreeWarm tracks a warm power-on (reset with the warm flag set) to the MAIN MENU: which RAM
// the boot itself writes (on a real calculator DRAM keeps its contents across power-off).
func ramFreeWarm(t *testing.T, out string) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full_32mb.bin")
	if err != nil {
		t.Skip("no 32 MB image")
	}
	e := NewEmulator(flash)
	e.mem.W32(warmFlagAddr, 1)
	p := newRamProbe(t, e, out)
	defer func() { e.mem.accHook = nil }()
	p.begin("warm-boot")
	for i := 0; i < 12; i++ {
		e.Step(50_000_000)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
	}
	p.snap("warm_end")
	t.Logf("pc=%08x at menu %v", e.cpu.pc, e.atMainMenu())
	p.dump(nil)
}
