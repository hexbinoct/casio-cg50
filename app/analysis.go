package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cg50emu"
	"cg50emu/sh4"
)

// The code analysis behind the disassembly, the names, the cross-references, the Syscalls and
// Strings windows (package cg50emu/sh4):
//
//   - the OS: the flash dump as loaded, analysed once in the background when a dump is opened.
//     The function table is cached per dump in the settings folder (analysis/<hash>.funcs), so
//     the next start only rebuilds the reference index. The cache holds addresses and sizes,
//     nothing of the dump itself.
//   - the add-in: the one installed last (its .g3a, mapped as the OS maps it), replaced by the
//     one the calculator is running as soon as it launches one (its pages, read through the OS's
//     add-in page table; see addinFromPageTable).
//
// The listings (one per address space the disassembly offers) are rebuilt when either changes,
// and the page is told through the "listing" event and AnalysisStatus.Gen.

// AnalysisStatus is part of the status the page gets.
type AnalysisStatus struct {
	State  string `json:"state"` // "", "running", "ready", "error"
	Phase  string `json:"phase"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	Funcs  int    `json:"funcs"`
	Ms     int    `json:"ms"`
	Cached bool   `json:"cached"`
	Err    string `json:"err,omitempty"`
	Addin  string `json:"addin"` // the add-in listed at 0x00300000, "" if none
	Gen    int    `json:"gen"`   // bumps whenever the listings change
}

type osCode struct {
	img  *sh4.Image
	sys  *sh4.Syscalls
	an   *sh4.Analysis
	rows *rowMap
}

type addinCode struct {
	name    string
	running bool   // read from the running calculator (else: the installed file)
	key     uint32 // the page table's first entry it was read with (0: from a file)
	img     *sh4.Image
	an      *sh4.Analysis
	rows    *rowMap
	sum     [32]byte
}

type analysisSet struct {
	mu       sync.RWMutex
	hash     string // the dump analysed
	flash    uint32 // its size
	os       *osCode
	addin    *addinCode
	status   AnalysisStatus
	listings []*listing
	byName   map[string]uint32 // lower-case function name → address (built on first lookup)
	cancel   context.CancelFunc

	strMu   sync.Mutex
	strScan map[string]*strScan // region → its scan
	strLast *strSearch
}

// ---- regions ---------------------------------------------------------------------------

// Region is a named address range the Memory and Disassembly windows offer.
type Region struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Start uint32 `json:"start"`
	Size  uint32 `json:"size"`
	Code  bool   `json:"code"`           // offered in the disassembly
	Note  string `json:"note,omitempty"` // why it can't be read, or how it is read
}

const (
	flashP1     = 0x80000000
	flashP2     = 0xA0000000
	addinBase   = sh4.AddinCode // 0x00300000
	addinMax    = 4 << 20       // the page table's reach that we read
	addinRAM    = 0x08100000
	addinRAMLen = 0x80000
	addinRAMBus = 0x0C160000 // where the OS maps it (wiki: memory map → add-in mappings)
	pageTable   = 0x8C04CF0C // the OS 3.60 add-in page table, one word per 4 KB page
	osRAM       = 0x8C000000
	osRAMP2     = 0xAC000000
	ramLen      = cg50emu.DramSize
)

func regions(flash, addin uint32) []Region {
	if addin == 0 {
		addin = 2 << 20
	}
	return []Region{
		{ID: "flash", Name: "Flash", Start: flashP1, Size: flash, Code: true},
		{ID: "flash-p2", Name: "Flash (P2 mirror)", Start: flashP2, Size: flash, Code: true},
		{ID: "ram", Name: "RAM", Start: osRAM, Size: ramLen, Code: true},
		{ID: "ram-p2", Name: "RAM (P2 mirror)", Start: osRAMP2, Size: ramLen, Code: true},
		{ID: "addin", Name: "Add-in code", Start: addinBase, Size: addin, Code: true,
			Note: "virtual: through the TLB, else the OS's page table"},
		{ID: "addin-ram", Name: "Add-in RAM", Start: addinRAM, Size: addinRAMLen, Code: true,
			Note: "virtual: through the TLB, else its usual place 0C160000"},
		{ID: "ilram-os", Name: "IL RAM (OS)", Start: cg50emu.IlramBase, Size: cg50emu.IlramMask + 1, Code: true},
		{ID: "xyram", Name: "X/Y RAM", Start: 0xE500E000, Size: 0x4000, Code: true},
		{ID: "ilmem", Name: "IL memory", Start: 0xE5200000, Size: 0x1000, Code: true},
		{ID: "ocram", Name: "On-chip RAM", Start: cg50emu.OcramBase, Size: cg50emu.OcramSize},
		{ID: "mmio", Name: "Device registers", Start: 0xA4000000, Size: 0x1C000000,
			Note: "device registers (MMIO): not read by the debugger, reading one can change the device"},
		{ID: "p4", Name: "Control registers (P4)", Start: 0xFF000000, Size: 0x01000000,
			Note: "device registers (MMIO): not read by the debugger"},
	}
}

// ---- the OS ----------------------------------------------------------------------------

func (s *store) analysisPath(hash string) string {
	return filepath.Join(s.dir, "analysis", hash+".funcs")
}

// The cache file: a header line, then per function its address (4 bytes), code size and pool
// size (2 bytes each), big-endian. funcsVersion changes when the analysis would give other
// results (a new rule in sh4), so an old cache is simply not used.
const funcsMagic = "CG50STUDIO-FUNCS"
const funcsVersion = 1

func encodeFuncs(hash string, fs []sh4.Func) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %d %s %d\n", funcsMagic, funcsVersion, hash, len(fs))
	var e [8]byte
	for _, f := range fs {
		binary.BigEndian.PutUint32(e[0:], f.Addr)
		binary.BigEndian.PutUint16(e[4:], uint16(f.Size))
		binary.BigEndian.PutUint16(e[6:], uint16(f.Pool))
		b.Write(e[:])
	}
	return b.Bytes()
}

func decodeFuncs(b []byte, hash string) ([]sh4.Func, error) {
	nl := bytes.IndexByte(b, '\n')
	if nl < 0 {
		return nil, errors.New("no header")
	}
	var magic, h string
	var ver, n int
	if _, err := fmt.Sscanf(string(b[:nl]), "%s %d %s %d", &magic, &ver, &h, &n); err != nil {
		return nil, err
	}
	body := b[nl+1:]
	if magic != funcsMagic || ver != funcsVersion || h != hash || n < 0 || len(body) != 8*n {
		return nil, errors.New("not this dump's, or another version")
	}
	fs := make([]sh4.Func, n)
	for i := range fs {
		e := body[8*i:]
		fs[i] = sh4.Func{Addr: binary.BigEndian.Uint32(e), Size: uint32(binary.BigEndian.Uint16(e[4:])),
			Pool: uint32(binary.BigEndian.Uint16(e[6:]))}
		if i > 0 && fs[i].Addr <= fs[i-1].Addr {
			return nil, errors.New("unsorted")
		}
	}
	return fs, nil
}

// analyseOS analyses the OS of a dump (in the background), unless that dump is analysed or
// being analysed already.
func (a *App) analyseOS(data []byte, d dumpInfo) {
	s := &a.ana
	s.mu.Lock()
	if s.hash == d.Hash && s.status.State != "error" {
		s.mu.Unlock()
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.hash, s.flash, s.os, s.addin, s.byName = d.Hash, uint32(len(data)), nil, nil, nil
	s.status = AnalysisStatus{State: "running", Phase: "functions", Gen: s.status.Gen + 1}
	s.rebuildLocked()
	s.mu.Unlock()
	a.strReset()
	a.emitListing()

	go func() {
		t0 := time.Now()
		img := sh4.NewImage(sh4.ROMBase, data)
		sys := sh4.FindSyscalls(img)
		opt := sh4.OSOptions(img, sys)
		var progMu sync.Mutex
		opt.Progress = func(phase string, done, total int) {
			progMu.Lock()
			defer progMu.Unlock()
			s.mu.Lock()
			if s.hash == d.Hash {
				s.status.Phase, s.status.Done, s.status.Total = phase, done, total
			}
			s.mu.Unlock()
		}
		var an *sh4.Analysis
		cached := false
		if b, err := os.ReadFile(a.store.analysisPath(d.Hash)); err == nil {
			if fs, err := decodeFuncs(b, d.Hash); err == nil {
				an, cached = sh4.Restore(img, opt, fs), true
			}
		}
		if an == nil {
			var err error
			an, err = sh4.Analyze(ctx, img, opt)
			if err != nil {
				if ctx.Err() == nil {
					s.mu.Lock()
					s.status.State, s.status.Err = "error", err.Error()
					s.mu.Unlock()
				}
				return
			}
			p := a.store.analysisPath(d.Hash)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p+".tmp", encodeFuncs(d.Hash, an.Funcs()), 0o644); err == nil {
				os.Rename(p+".tmp", p)
			}
		}
		rows := buildRowMap(an, img.Word, flashP1, uint32(len(data)))
		an.BuildXrefs() // the label rows count callers: build the index now, not on first view
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if s.hash != d.Hash {
			s.mu.Unlock()
			return
		}
		s.os = &osCode{img: img, sys: sys, an: an, rows: rows}
		s.byName = nil
		s.status.State, s.status.Phase = "ready", ""
		s.status.Funcs, s.status.Cached = an.NumFuncs(), cached
		s.status.Ms = int(time.Since(t0).Milliseconds())
		s.status.Gen++
		if s.addin != nil && s.addin.an != nil { // name the add-in's syscalls now that the table is known
			s.addin = analyseAddin(s.addin.name, s.addin.img, sys, s.addin.running, s.addin.key, s.addin.sum)
		}
		s.rebuildLocked()
		s.mu.Unlock()
		a.emitListing()
	}()
}

func (a *App) emitListing() {
	if a.ctx != nil {
		a.ana.mu.RLock()
		st := a.ana.status
		a.ana.mu.RUnlock()
		emit(a.ctx, "listing", st)
	}
}

// ---- the add-in ------------------------------------------------------------------------

func analyseAddin(name string, img *sh4.Image, sys *sh4.Syscalls, running bool, key uint32, sum [32]byte) *addinCode {
	an, _ := sh4.Analyze(context.Background(), img, sh4.AddinOptions(img, sys))
	ac := &addinCode{name: name, running: running, key: key, img: img, an: an, sum: sum}
	// the image of a .g3a starts with its header (below 0x00300000): list from the code on
	code := img.Data[addinBase-img.Base:]
	ac.rows = buildRowMap(an, img.Word, addinBase, uint32(len(code)))
	return ac
}

// setAddin makes the add-in at 0x00300000 the one in code (the .g3a file, or the pages the
// calculator runs). The analysis is a few milliseconds.
func (a *App) setAddin(name string, code []byte, running bool, key uint32, g3a []byte) {
	sum := sha256.Sum256(code)
	s := &a.ana
	s.mu.RLock()
	same := s.addin != nil && s.addin.sum == sum && s.addin.running == running
	var sys *sh4.Syscalls
	if s.os != nil {
		sys = s.os.sys
	}
	s.mu.RUnlock()
	if same {
		return
	}
	var img *sh4.Image
	if g3a != nil {
		img, _ = sh4.G3AImage(g3a)
	}
	if img == nil {
		img = sh4.NewImage(addinBase, code)
	}
	ac := analyseAddin(name, img, sys, running, key, sum)
	s.mu.Lock()
	s.addin = ac
	s.byName = nil
	s.status.Addin = ac.label()
	s.status.Gen++
	s.rebuildLocked()
	s.mu.Unlock()
	a.strDrop("addin")
	a.emitListing()
}

func (ac *addinCode) label() string {
	if ac == nil {
		return ""
	}
	if ac.running {
		return ac.name + " (running)"
	}
	return ac.name + " (installed)"
}

// addinPages reads the OS's add-in page table at 0x8C04CF0C: one word per 4 KB page of the
// running add-in from 0x00300000, the page's address in flash. The table ends at the first
// word that is not a page of the flash. key identifies its contents (it changes when another
// add-in is launched, or while the OS is still filling it).
func addinPages(e *cg50emu.Emulator) (pages []uint32, key uint64) {
	var tbl [addinMax / 4096 * 4]byte
	n, _ := e.ReadMemory(pageTable, tbl[:], false)
	key = 14695981039346656037 // FNV-1a
	for i := 0; i+4 <= n; i += 4 {
		v := binary.BigEndian.Uint32(tbl[i:]) & 0x1FFFFFFF
		if v == 0 || v&0xFFF != 0 || v >= 32<<20 {
			break
		}
		for _, b := range tbl[i : i+4] {
			key = (key ^ uint64(b)) * 1099511628211
		}
		pages = append(pages, v)
	}
	return pages, key
}

// readAddinCode reads the running add-in's code through its pages (nil if a page can't be read).
func readAddinCode(e *cg50emu.Emulator, pages []uint32) []byte {
	code := make([]byte, 0, len(pages)*4096)
	for _, v := range pages {
		page := make([]byte, 4096)
		if k, err := e.ReadMemory(v, page, true); err != nil || k != len(page) {
			return nil
		}
		code = append(code, page...)
	}
	return code
}

// watchAddin is called a few times a second and at every stop: when the add-in page table
// changes, the calculator launched an add-in (or is still mapping it), and it becomes the
// listed one.
func (a *App) watchAddin(m *machine) {
	m.addinMu.Lock()
	defer m.addinMu.Unlock()
	if v, err := m.emu.Peek32(pageTable, false); err != nil || v&0x1FFFFFFF == 0 {
		return
	}
	pages, key := addinPages(m.emu)
	if len(pages) == 0 || key == m.addinKey {
		return
	}
	code := readAddinCode(m.emu, pages)
	if code == nil {
		return // tried again at the next look
	}
	m.addinKey = key
	name := "add-in"
	a.mu.Lock()
	for _, f := range a.installedFiles {
		if len(f.data) > sh4.G3AHeaderLen+256 && bytes.HasPrefix(code, f.data[sh4.G3AHeaderLen:min(len(f.data), sh4.G3AHeaderLen+4096)]) {
			name = f.name
		}
	}
	a.mu.Unlock()
	a.setAddin(name, code, true, 0, nil)
}

// ---- listings and names ------------------------------------------------------------------

// rebuildLocked makes the listings from what is analysed now (s.mu held).
func (s *analysisSet) rebuildLocked() {
	flash := s.flash
	if flash == 0 {
		flash = 32 << 20
	}
	var addinSize uint32
	if s.addin != nil {
		addinSize = s.addin.rows.size
	}
	var out []*listing
	for _, r := range regions(flash, addinSize) {
		if !r.Code {
			continue
		}
		l := &listing{id: r.ID, name: r.Name, base: r.Start, size: r.Size, rows: linearMap(r.Size)}
		switch r.ID {
		case "flash", "flash-p2":
			if s.os != nil {
				l.rows, l.an, l.static, l.sys = s.os.rows, s.os.an, s.os.img, s.os.sys
				l.shift = r.Start - flashP1
			}
		case "addin":
			if s.addin != nil {
				l.rows, l.an, l.static = s.addin.rows, s.addin.an, s.addin.img
			}
		}
		if l.sys == nil && s.os != nil {
			l.sys = s.os.sys
		}
		out = append(out, l)
	}
	s.listings = out
}

func (s *analysisSet) listing(id string) *listing {
	for _, l := range s.listings {
		if l.id == id {
			return l
		}
	}
	return nil
}

// osAddr folds a P2 address of the flash onto P1, where the OS analysis has it.
func (s *analysisSet) osAddr(a uint32) uint32 {
	if a>>29 == 5 && a-flashP2 < s.flash {
		return a - 0x20000000
	}
	return a
}

// analysisFor returns the analysis that covers addr and the address in its coordinates.
func (s *analysisSet) analysisFor(addr uint32) (*sh4.Analysis, uint32) {
	if s.os != nil {
		a := s.osAddr(addr)
		if lo, hi := s.os.an.Region(); a >= lo && a < hi {
			return s.os.an, a
		}
	}
	if s.addin != nil && s.addin.an != nil {
		if lo, hi := s.addin.an.Region(); addr >= lo && addr < hi {
			return s.addin.an, addr
		}
	}
	return nil, addr
}

// funcName names the function starting at addr ("" if none starts there).
func (s *analysisSet) funcName(addr uint32) string {
	an, a := s.analysisFor(addr)
	if an == nil {
		return ""
	}
	if _, ok := an.FuncAt(a); !ok {
		return ""
	}
	return an.FuncName(a)
}

// symbol names addr as function+offset ("" when no function holds it).
func (s *analysisSet) symbol(addr uint32) string {
	if o := s.osAddr(addr) - sh4.SyscallTrampoline; o < 10 && s.os != nil {
		// the five instructions every add-in syscall goes through (no function of its own)
		if o == 0 {
			return "syscall routine"
		}
		return fmt.Sprintf("syscall routine+0x%X", o)
	}
	an, a := s.analysisFor(addr)
	if an == nil {
		return ""
	}
	f, ok := an.FuncBefore(a)
	if !ok || a >= f.PoolEnd() && a >= f.End() {
		return ""
	}
	if a == f.Addr {
		return an.FuncName(f.Addr)
	}
	return fmt.Sprintf("%s+0x%X", an.FuncName(f.Addr), a-f.Addr)
}

// lockedNames lets a listing ask for names while s.mu is read-locked.
type lockedNames struct{ s *analysisSet }

func (n lockedNames) funcName(a uint32) string { return n.s.funcName(a) }
func (n lockedNames) symbol(a uint32) string   { return n.s.symbol(a) }

// Symbol is name+offset for an address ("" if unknown), for the other windows.
func (a *App) symbol(addr uint32) string {
	a.ana.mu.RLock()
	defer a.ana.mu.RUnlock()
	return a.ana.symbol(addr)
}

// ---- bindings: the disassembly ------------------------------------------------------------

// SpaceInfo is an address space the disassembly can list.
type SpaceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Base     uint32 `json:"base"`
	Size     uint32 `json:"size"`
	Count    int    `json:"count"`    // rows
	Analysed bool   `json:"analysed"` // functions and names known
}

// Listings is what the disassembly needs to know about the spaces.
type Listings struct {
	Gen    int         `json:"gen"`
	Spaces []SpaceInfo `json:"spaces"`
}

// DisasmSpaces lists the address spaces of the disassembly (the flash, its P2 mirror, the
// add-in, RAM, the on-chip memories) with their row counts.
func (a *App) DisasmSpaces() Listings {
	s := &a.ana
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Listings{Gen: s.status.Gen, Spaces: []SpaceInfo{}}
	for _, l := range s.listings {
		out.Spaces = append(out.Spaces, SpaceInfo{ID: l.id, Name: l.name, Base: l.base, Size: l.size,
			Count: l.rows.count, Analysed: l.an != nil})
	}
	return out
}

// DisasmRows returns rows [first, first+n) of a space (n at most 1024): a batch, the page
// asks for what it shows plus a margin.
func (a *App) DisasmRows(space string, first, n int) ([]Row, error) {
	n = max(0, min(n, 1024))
	m := a.current()
	s := &a.ana
	s.mu.RLock()
	defer s.mu.RUnlock()
	l := s.listing(space)
	if l == nil {
		return nil, fmt.Errorf("no address space %q", space)
	}
	read := func(addr uint32, dst []byte) int {
		if m == nil {
			if l.static != nil {
				return copy(dst, l.static.Bytes(addr-l.shift, len(dst)))
			}
			return 0
		}
		k, _ := m.read(addr, dst)
		return k
	}
	return l.Rows(first, n, read, lockedNames{s}), nil
}

// DisasmRowOf is the row of addr in a space (-1 outside it).
func (a *App) DisasmRowOf(space string, addr uint32) int {
	s := &a.ana
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l := s.listing(space); l != nil {
		return l.rowOf(addr)
	}
	return -1
}

// Lookup finds an address by name: a function's name (sub_80012345, a syscall's name or
// syscall_0EAB, start), or a hexadecimal address.
func (a *App) Lookup(name string) (uint32, error) {
	t := strings.TrimSpace(name)
	if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(t), "0x"), 16, 32); err == nil && t != "" {
		return uint32(v), nil
	}
	s := &a.ana
	s.mu.Lock()
	defer s.mu.Unlock()
	low := strings.ToLower(t)
	if s.os != nil {
		for _, sn := range sh4.SyscallNames() {
			if strings.ToLower(sn.Name) == low {
				if h, ok := s.os.sys.Handler(uint32(sn.Num)); ok {
					return h, nil
				}
			}
		}
		if n, ok := strings.CutPrefix(low, "syscall_"); ok {
			if v, err := strconv.ParseUint(n, 16, 32); err == nil {
				if h, ok := s.os.sys.Handler(uint32(v)); ok {
					return h, nil
				}
			}
		}
	}
	if s.byName == nil {
		s.byName = map[string]uint32{}
		for _, an := range []*sh4.Analysis{s.osAn(), s.addinAn()} {
			if an == nil {
				continue
			}
			for _, f := range an.Funcs() {
				k := strings.ToLower(an.FuncName(f.Addr))
				if _, dup := s.byName[k]; !dup {
					s.byName[k] = f.Addr
				}
			}
		}
	}
	if v, ok := s.byName[low]; ok {
		return v, nil
	}
	for _, p := range []string{"sub_", "loc_", "fn_"} {
		if n, ok := strings.CutPrefix(low, p); ok {
			if v, err := strconv.ParseUint(n, 16, 32); err == nil {
				return uint32(v), nil
			}
		}
	}
	return 0, fmt.Errorf("no function or address called %q", t)
}

func (s *analysisSet) osAn() *sh4.Analysis {
	if s.os == nil {
		return nil
	}
	return s.os.an
}

func (s *analysisSet) addinAn() *sh4.Analysis {
	if s.addin == nil {
		return nil
	}
	return s.addin.an
}

// XrefRow is one reference to an address.
type XrefRow struct {
	From uint32 `json:"from"`
	Kind string `json:"kind"`
	Fn   string `json:"fn"`   // the function it is in, name+offset
	Text string `json:"text"` // the instruction (or .long for a data word)
}

// Xrefs lists what refers to addr: calls, jumps, branches, literal loads, mova, data words
// (the index is built once per analysis, then each lookup is a binary search).
func (a *App) Xrefs(addr uint32) []XrefRow {
	s := &a.ana
	s.mu.RLock()
	defer s.mu.RUnlock()
	an, t := s.analysisFor(addr)
	out := []XrefRow{}
	if an == nil {
		return out
	}
	img := an.Image()
	seen := map[sh4.Xref]bool{}
	for _, x := range an.XrefsTo(t) {
		if seen[x] { // one per site (a register loaded on several paths is listed once)
			continue
		}
		seen[x] = true
		r := XrefRow{From: x.From, Kind: x.Kind.String(), Fn: s.symbol(x.From)}
		if x.Kind == sh4.XrefData {
			if v, ok := img.Long(x.From); ok {
				r.Text = fmt.Sprintf(".long 0x%08x", v)
			}
		} else if in, ok := img.Decode(x.From); ok {
			r.Text = in.Text
		}
		out = append(out, r)
	}
	return out
}

// SyscallRow is one entry of the OS's syscall table.
type SyscallRow struct {
	Num  uint32 `json:"num"`
	Name string `json:"name"`
	Addr uint32 `json:"addr"`
}

// Syscalls lists the OS's syscall table (every entry that leads into the flash), read from
// the OS itself, with the names known for the numbers.
func (a *App) Syscalls() []SyscallRow {
	s := &a.ana
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SyscallRow{}
	if s.os == nil {
		return out
	}
	for n, h := range s.os.sys.Handlers() {
		if h != 0 {
			out = append(out, SyscallRow{Num: uint32(n), Name: sh4.SyscallNameOf(uint32(n)), Addr: h})
		}
	}
	return out
}

// ---- strings -------------------------------------------------------------------------------

type strScan struct {
	list []sh4.String
	at   int // the stop sequence it was made at (live regions are scanned again after a stop)
}

type strSearch struct {
	token int
	list  []sh4.String
	idx   []int32
}

// StringRegion is a region the Strings window can scan.
type StringRegion struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// StringRegions lists what the Strings window can scan.
func (a *App) StringRegions() []StringRegion {
	return []StringRegion{
		{"os", "OS code (flash, first 12 MB)"}, {"storage", "Storage memory (rest of the flash)"},
		{"flash", "Whole flash"}, {"ram", "RAM (now)"}, {"addin", "Add-in"},
	}
}

func (a *App) strReset() {
	a.ana.strMu.Lock()
	a.ana.strScan, a.ana.strLast = nil, nil
	a.ana.strMu.Unlock()
}

func (a *App) strDrop(region string) {
	a.ana.strMu.Lock()
	delete(a.ana.strScan, region)
	a.ana.strMu.Unlock()
}

// scanRegion returns the strings of a region, scanning it if needed.
func (a *App) scanRegion(region string) ([]sh4.String, error) {
	seq := a.stopSeq()
	live := region == "storage" || region == "flash" || region == "ram"
	a.ana.strMu.Lock()
	if sc := a.ana.strScan[region]; sc != nil && (!live || sc.at == seq) {
		a.ana.strMu.Unlock()
		return sc.list, nil
	}
	a.ana.strMu.Unlock()

	var img *sh4.Image
	var lo, hi uint32
	m := a.current()
	s := &a.ana
	s.mu.RLock()
	osc, ac := s.os, s.addin
	s.mu.RUnlock()
	switch region {
	case "os":
		if osc == nil {
			return nil, errors.New("the OS is still being analysed")
		}
		img, lo, hi = osc.img, sh4.ROMBase, sh4.ROMBase+min(sh4.OSCodeSize, uint32(len(osc.img.Data)))
	case "storage", "flash":
		if m == nil {
			return nil, errNoDump
		}
		img = sh4.NewImage(sh4.ROMBase, m.emu.FlashImage())
		lo, hi = img.Base, img.End()
		if region == "storage" {
			lo = sh4.ROMBase + sh4.OSCodeSize
		}
	case "ram":
		if m == nil {
			return nil, errNoDump
		}
		buf := make([]byte, ramLen)
		n, _ := m.emu.ReadMemory(osRAM, buf, false)
		img = sh4.NewImage(osRAM, buf[:n])
		lo, hi = img.Base, img.End()
	case "addin":
		if ac == nil {
			return nil, errors.New("no add-in: install or launch one")
		}
		img, lo, hi = ac.img, addinBase, ac.img.End()
	default:
		return nil, fmt.Errorf("no region %q", region)
	}
	list := sh4.FindStrings(img, lo, hi, sh4.StringOptions{MinLen: 4})
	a.ana.strMu.Lock()
	if a.ana.strScan == nil {
		a.ana.strScan = map[string]*strScan{}
	}
	a.ana.strScan[region] = &strScan{list: list, at: seq}
	a.ana.strMu.Unlock()
	return list, nil
}

// StringsResult is a search: its token for StringRows, matches, and strings in the region.
type StringsResult struct {
	Token int `json:"token"`
	Count int `json:"count"`
	Total int `json:"total"`
	Ms    int `json:"ms"`
}

// SearchStrings scans a region (once; RAM and the storage memory again after each stop) and
// keeps the strings that contain q (any case), of an encoding ("any", "ascii", "casio",
// "utf16") and at least minLen characters. The rows come with StringRows.
func (a *App) SearchStrings(region, q, enc string, minLen int) (StringsResult, error) {
	t0 := time.Now()
	list, err := a.scanRegion(region)
	if err != nil {
		return StringsResult{}, err
	}
	want := map[string]sh4.Encoding{"ascii": sh4.EncASCII, "casio": sh4.EncCasio, "utf16": sh4.EncUTF16}[enc]
	q = strings.ToLower(q)
	idx := make([]int32, 0, 1024)
	for i := range list {
		st := &list[i]
		if want != 0 && st.Enc != want || st.Chars < minLen {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(st.Text), q) {
			continue
		}
		idx = append(idx, int32(i))
	}
	a.ana.strMu.Lock()
	tok := 1
	if a.ana.strLast != nil {
		tok = a.ana.strLast.token + 1
	}
	a.ana.strLast = &strSearch{token: tok, list: list, idx: idx}
	a.ana.strMu.Unlock()
	return StringsResult{Token: tok, Count: len(idx), Total: len(list), Ms: int(time.Since(t0).Milliseconds())}, nil
}

// StrRow is one string found.
type StrRow struct {
	Addr     uint32 `json:"addr"`
	Text     string `json:"text"`
	Encoding string `json:"encoding"`
	Size     int    `json:"size"`
}

// StringRows returns matches [first, first+n) of the search with this token (an older
// token's rows are gone: nil).
func (a *App) StringRows(token, first, n int) []StrRow {
	a.ana.strMu.Lock()
	defer a.ana.strMu.Unlock()
	sr := a.ana.strLast
	if sr == nil || sr.token != token || first < 0 {
		return nil
	}
	out := []StrRow{}
	for i := first; i < len(sr.idx) && i < first+min(n, 2000); i++ {
		st := sr.list[sr.idx[i]]
		t := st.Text
		if len(t) > 240 {
			t = t[:240] + "…"
		}
		out = append(out, StrRow{Addr: st.Addr, Text: t, Encoding: st.Enc.String(), Size: st.Size})
	}
	return out
}
