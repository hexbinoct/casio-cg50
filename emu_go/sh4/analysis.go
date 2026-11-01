package sh4

// Function detection, ported from DASM's src/analysis.c.
//
// Functions are found by following code from the entry points (an add-in's entry, or the OS
// reset vector, every handler of its syscall table and the exception/interrupt vectors):
// fall-through, bt/bf/bra and gcc jump tables (mova + mov.b/mov.w @(r0,Rn) + braf) until
// rts / jmp, looking only at real code; bsr targets and jsr/jmp @Rn targets whose Rn was loaded
// from a literal pool become functions. Then two rules for code nothing calls directly: right
// after a function's literal pool and padding, a prologue (a register push or sts.l pr within
// the first 3 instructions) starts a function; and an address held in a literal pool or an
// aligned data word starts one if it has that prologue, or if it sits right after a function
// and explores cleanly (every path returns, no erased or zero words).

import (
	"context"
	"sort"
	"sync"
)

// Func is a detected function.
type Func struct {
	Addr uint32 // first instruction
	Size uint32 // code bytes: to the end of the last reachable instruction (at most 0xffff, as DASM)
	Pool uint32 // bytes to the end of its literal pools / jump tables (>= Size; at most 0xffff)
}

// End is the address after the function's code.
func (f Func) End() uint32 { return f.Addr + f.Size }

// PoolEnd is the address after its literal pools.
func (f Func) PoolEnd() uint32 { return f.Addr + f.Pool }

// Options say what to analyse.
type Options struct {
	Start, End uint32   // the code region (End exclusive); 0, 0 = the whole image
	Entries    []uint32 // where code starts for sure
	Syscalls   *Syscalls
	// AllHandlers: every syscall handler in the region is an entry point (for an OS image).
	AllHandlers bool
	// VBR: find the ldc Rn,vbr instructions and add the exception and interrupt vectors at
	// VBR+0x100, +0x400, +0x600 as entries (not in DASM).
	VBR bool
	// P2Mirror: an address in 0xA0000000-0xBFFFFFFF (P2, uncached) is the same memory as the
	// one 0x20000000 lower (P1): the OS's boot code runs from the P2 mirror of the flash. Call
	// targets, pointers and references are folded onto the region (not in DASM).
	P2Mirror bool
	// Progress, if set, is called now and then with the phase ("functions", "pointers",
	// "references") and its progress (total 0 = unknown).
	Progress func(phase string, done, total int)
}

// OSOptions analyse an OS image mapped at 0x80000000 as DASM does (its first 12 MB, from the
// reset vector and every syscall handler) plus the exception/interrupt vectors.
func OSOptions(img *Image, sys *Syscalls) Options {
	end := img.End()
	if end-img.Base > OSCodeSize {
		end = img.Base + OSCodeSize
	}
	return Options{Start: img.Base, End: end, Entries: []uint32{img.Base}, Syscalls: sys,
		AllHandlers: true, VBR: true, P2Mirror: true}
}

// AddinOptions analyse an add-in image (from G3AImage): the code from AddinCode, entry AddinCode.
// sys, the running OS's table (may be nil), names the syscalls the add-in calls.
func AddinOptions(img *Image, sys *Syscalls) Options {
	return Options{Start: AddinCode, End: img.End(), Entries: []uint32{AddinCode}, Syscalls: sys}
}

// Analysis holds the functions of an image and answers queries about them. Queries may run
// concurrently with each other; AddFunction excludes them while it runs.
type Analysis struct {
	img     *Image
	opt     Options
	lo, hi  uint32 // the code region
	mu      sync.RWMutex
	funcs   []Func // sorted by address after each round; [:nSorted] is searchable
	nSorted int
	partial bool
	vbr     []uint32 // VBR values found

	// exploring scratch
	vis   [0x10000 / 16]uint8 // one bit per instruction over 64 KB from the start
	paths [maxPaths]path
	found []uint32 // call targets for the next round
	rdFn  ReadFunc

	xmu  sync.Mutex
	xidx *xrefIndex
}

type path struct {
	a    uint32
	mask uint16     // bit r: v[r] holds the literal last loaded into r
	v    [16]uint32 // literals in registers
}

const maxPaths = 64

// Analyze finds the functions of img. When ctx is cancelled it stops and returns what was found
// so far (Partial reports it) with ctx's error.
func Analyze(ctx context.Context, img *Image, opt Options) (*Analysis, error) {
	an := &Analysis{img: img, opt: opt}
	an.lo, an.hi = opt.Start, opt.End
	if an.lo == 0 && an.hi == 0 {
		an.lo, an.hi = img.Base, img.End()
	}
	if an.lo < img.Base {
		an.lo = img.Base
	}
	if an.hi > img.End() || an.hi < an.lo {
		an.hi = img.End()
	}
	an.rdFn = an.readWord
	err := an.build(ctx)
	return an, err
}

// ---- reading, limited to the code region (as DASM) ----

func (an *Analysis) rd8(a uint32) (uint32, bool) {
	if a < an.lo || a >= an.hi {
		return 0, false
	}
	return uint32(an.img.Data[a-an.img.Base]), true
}

func (an *Analysis) rd16(a uint32) (uint32, bool) {
	if a < an.lo || a >= an.hi || an.hi-a < 2 || a&1 != 0 {
		return 0, false
	}
	d := an.img.Data[a-an.img.Base:]
	return uint32(d[0])<<8 | uint32(d[1]), true
}

func (an *Analysis) rd32(a uint32) (uint32, bool) {
	if a < an.lo || a >= an.hi || an.hi-a < 4 || a&3 != 0 {
		return 0, false
	}
	d := an.img.Data[a-an.img.Base:]
	return uint32(d[0])<<24 | uint32(d[1])<<16 | uint32(d[2])<<8 | uint32(d[3]), true
}

func (an *Analysis) readWord(a uint32) (uint16, bool) {
	v, ok := an.rd16(a)
	return uint16(v), ok
}

func (an *Analysis) inCode(a uint32) bool { return a >= an.lo && a < an.hi && a&1 == 0 }

// canon folds a P2 address onto its P1 mirror when that is in the region (Options.P2Mirror).
func (an *Analysis) canon(a uint32) uint32 {
	if an.opt.P2Mirror && a>>29 == 5 {
		if m := a - 0x20000000; m >= an.lo && m < an.hi {
			return m
		}
	}
	return a
}

func s8(x uint32) int32  { return int32(int8(x)) }
func s12(x uint32) int32 { return int32(x<<20) >> 20 }

// ---- the function table ----

// idxLE is the largest i < nSorted with funcs[i].Addr <= a, or -1.
func (an *Analysis) idxLE(a uint32) int {
	f := an.funcs[:an.nSorted]
	return sort.Search(len(f), func(i int) bool { return f[i].Addr > a }) - 1
}

func (an *Analysis) funcAt(a uint32) int {
	if i := an.idxLE(a); i >= 0 && an.funcs[i].Addr == a {
		return i
	}
	return -1
}

func (an *Analysis) funcContaining(a uint32) int {
	if i := an.idxLE(a); i >= 0 && a < an.funcs[i].Addr+an.funcs[i].Size {
		return i
	}
	return -1
}

func (an *Analysis) addFunc(a, codeEnd, poolEnd uint32) {
	sz, pl := codeEnd-a, poolEnd-a
	if sz > 0xffff {
		sz = 0xffff
	}
	if pl > 0xffff {
		pl = 0xffff
	}
	an.funcs = append(an.funcs, Func{a, sz, pl})
}

func (an *Analysis) sortFuncs() {
	sort.Slice(an.funcs, func(i, j int) bool { return an.funcs[i].Addr < an.funcs[j].Addr })
	an.nSorted = len(an.funcs)
}

func sortUniq(x []uint32) []uint32 {
	if len(x) < 2 {
		return x
	}
	sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
	k := 1
	for i := 1; i < len(x); i++ {
		if x[i] != x[k-1] {
			x[k] = x[i]
			k++
		}
	}
	return x[:k]
}

// ---- exploring one function ----

func (an *Analysis) pushPath(np *int, a uint32, from *path) {
	if *np >= maxPaths {
		return
	}
	q := &an.paths[*np]
	*np++
	q.a = a
	if from != nil {
		q.mask, q.v = from.mask, from.v
	} else {
		q.mask = 0
	}
}

func (an *Analysis) foundCall(t uint32) {
	t = an.canon(t)
	if an.inCode(t) && an.funcAt(t) < 0 {
		an.found = append(an.found, t)
	}
}

// writesRn: the common writers of Rn, after which a literal in Rn is forgotten.
func writesRn(op uint32) bool {
	h, lo := op>>12, op&0xFF
	return h == 5 || h == 6 || h == 7 || h == 9 || h == 0xE ||
		(h == 4 && lo != 0x0B && lo != 0x2B && lo != 0x22 && lo != 0x12 && lo != 0x02 && lo != 0x13)
}

// explore follows the function at start. clean: no path ran into erased/zero words, off the
// code, or (when only checking a candidate, !collect) into a word that is no instruction.
// codeEnd = end of the last reachable instruction, poolEnd = end of its literal pools / jump
// tables. collect: record call targets in an.found.
func (an *Analysis) explore(start uint32, collect bool) (codeEnd, poolEnd uint32, clean bool) {
	hi, pool, maxoff := start+2, start, uint32(0)
	clean = true
	np := 0
	an.pushPath(&np, start, nil)
	mark := func(o uint32) {
		if o < 0x8000 {
			an.vis[o>>3] |= 1 << (o & 7)
			if o > maxoff {
				maxoff = o
			}
		}
	}
	for np > 0 {
		np--
		P := an.paths[np]
		a := P.a
		var imm [16]uint8
		var immmask uint16
		var mova uint32
		tlReg, tlSize, tlScale, bound := -1, 0, int32(1), -1 // gcc jump table state
		for {
			if a < start { // shared code before the entry: not ours
				break
			}
			if !an.inCode(a) || a-start >= 0x10000 {
				clean = false
				break
			}
			off := (a - start) >> 1
			if an.vis[off>>3]&(1<<(off&7)) != 0 {
				break
			}
			an.vis[off>>3] |= 1 << (off & 7)
			if off > maxoff {
				maxoff = off
			}
			op, ok := an.rd16(a)
			if !ok || op == 0xFFFF || op == 0x0000 {
				clean = false
				break
			}
			if !collect && kindOf(uint16(op), a, an.rdFn) == KindIllegal { // real opcodes only
				clean = false
				break
			}
			if InsnLen(uint16(op)) == 4 { // 32-bit DSP instruction
				mark(off + 1)
				if a+4 > hi {
					hi = a + 4
				}
				P.mask &= 0xFF00 // its transfers move r0-r7
				a += 4
				continue
			}
			if a+2 > hi {
				hi = a + 2
			}
			n, m, h := int(op>>8)&15, int(op>>4)&15, op>>12
			stop, call := false, false
			if op&0xF000 == 0xD000 { // mov.l @(disp,pc),Rn
				la := a&^3 + 4 + (op&0xFF)*4
				if la+4 > pool {
					pool = la + 4
				}
				if v, ok := an.rd32(la); ok {
					P.v[n] = v
					P.mask |= 1 << n
				} else {
					P.mask &^= 1 << n
				}
				a += 2
				continue
			}
			switch {
			case h == 0x9: // mov.w @(disp,pc),Rn
				la := a + 4 + (op&0xFF)*2
				if la+2 > pool {
					pool = la + 2
				}
			case h == 0xB: // bsr
				if collect {
					an.foundCall(a + 4 + uint32(s12(op&0xFFF)*2))
				}
				call = true
			case h == 0xA: // bra
				if t := a + 4 + uint32(s12(op&0xFFF)*2); an.inCode(t) {
					an.pushPath(&np, t, &P)
				}
				stop = true
			case op&0xF900 == 0x8900: // bt bf bt/s bf/s
				if t := a + 4 + uint32(s8(op&0xFF)*2); an.inCode(t) {
					an.pushPath(&np, t, &P)
				}
			case op&0xF0FF == 0x400B || op&0xF0FF == 0x402B: // jsr / jmp @Rn
				if collect && (P.mask>>n)&1 != 0 {
					an.foundCall(P.v[n])
				}
				if op&0xF0FF == 0x402B {
					stop = true
				} else {
					call = true
				}
			case op&0xF0FF == 0x0003: // bsrf Rn
				call = true
			case op&0xF0FF == 0x0023: // braf Rn: a gcc jump table?
				if mova != 0 && tlReg == n && bound >= 0 && bound < 256 {
					for k := 0; k <= bound; k++ {
						ea := mova + uint32(k*tlSize)
						var d int32
						if tlSize == 2 {
							e, ok := an.rd16(ea)
							if !ok {
								break
							}
							d = int32(int16(e))
						} else {
							e, ok := an.rd8(ea)
							if !ok {
								break
							}
							d = int32(int8(e))
						}
						if t := a + 4 + uint32(d*tlScale); an.inCode(t) {
							an.pushPath(&np, t, &P)
						}
					}
					if te := mova + uint32((bound+1)*tlSize); te > pool {
						pool = te
					}
				}
				stop = true
			case op == 0x000B || op == 0x002B: // rts rte
				stop = true
			case op&0xFF00 == 0xC700: // mova @(disp,pc),r0
				mova = a&^3 + 4 + (op&0xFF)*4
			case h == 0xE: // mov #imm,Rn
				imm[n] = uint8(op)
				immmask |= 1 << n
			case op&0xF00F == 0x3006: // cmp/hi Rm,Rn: index > max?
				bound = -1
				if (immmask>>m)&1 != 0 {
					bound = int(imm[m])
				}
			case op&0xF00E == 0x000C: // mov.b/.w @(r0,Rm),Rn
				tlReg, tlSize, tlScale = n, 1, 1
				if op&1 != 0 {
					tlSize = 2
				}
			case op&0xF00F == 0x300C && n == m && n == tlReg: // add Rn,Rn
				tlScale = 2
			}
			if writesRn(op) {
				P.mask &^= 1 << n
			}
			if h == 0xF { // DSP transfers move r0-r7
				P.mask &= 0xFF00
			}
			if call { // calls clobber r0-r7
				P.mask &= 0xFF00
			}
			if stop || call { // the delay slot is ours too
				mark(off + 1)
				if a+4 > hi {
					hi = a + 4
				}
				if stop {
					break
				}
				a += 4
				continue
			}
			a += 2
		}
	}
	clear(an.vis[:maxoff>>3+1])
	poolEnd = hi
	if pool > hi {
		poolEnd = pool
	}
	return hi, poolEnd, clean
}

// prologue: a register push (mov.l Rm,@-r15) or sts.l pr,@-r15 within the first 3
// instructions, all of them real instructions.
func (an *Analysis) prologue(a uint32) bool {
	for k := uint32(0); k < 3; k++ {
		p := a + 2*k
		op, ok := an.rd16(p)
		if !ok {
			return false
		}
		if op&0xFF0F == 0x2F06 || op == 0x4F22 {
			return true
		}
		if op == 0x000B || op == 0xFFFF || op == 0x0000 {
			return false
		}
		if kindOf(uint16(op), p, an.rdFn) == KindIllegal {
			return false
		}
	}
	return false
}

// ---- building ----

// gapCand is a candidate right after a function's pool; pointed: a data word points at it.
type gapCand struct {
	a       uint32
	pointed bool
}

func (an *Analysis) progress(phase string, done, total int) {
	if an.opt.Progress != nil {
		an.opt.Progress(phase, done, total)
	}
}

// traverse explores the pending starts round by round, in address order (step 1).
func (an *Analysis) traverse(ctx context.Context, starts []uint32) error {
	todo := append([]uint32(nil), starts...) // todo and an.found never share an array
	an.found = nil
	for len(todo) > 0 {
		todo = sortUniq(todo)
		for _, t := range todo {
			if an.funcAt(t) >= 0 {
				continue
			}
			ce, pe, _ := an.explore(t, true)
			an.addFunc(t, ce, pe)
			if len(an.funcs)&63 == 0 {
				an.progress("functions", len(an.funcs), 0)
				if err := ctx.Err(); err != nil {
					an.sortFuncs()
					return err
				}
			}
		}
		an.sortFuncs()
		todo, an.found = an.found, todo[:0]
	}
	return nil
}

func (an *Analysis) build(ctx context.Context) error {
	var todo []uint32
	for _, e := range an.opt.Entries {
		todo = append(todo, an.canon(e))
	}
	if sys := an.opt.Syscalls; sys != nil && an.opt.AllHandlers { // every syscall handler
		for i := uint32(0); i < SyscallTableLen; i++ {
			if h, ok := an.rd32(sys.Base + 4*i); ok && an.inCode(an.canon(h)) {
				h = an.canon(h)
				todo = append(todo, h)
			}
		}
	}
	err := an.run(ctx, todo)
	an.sortFuncs()
	if err != nil {
		an.partial = true
	}
	return err
}

func (an *Analysis) run(ctx context.Context, todo []uint32) error {
	var gapc []gapCand
	sweeps := 0
	vbrDone := !an.opt.VBR
	for {
		// 1. traverse from the pending starts
		if err := an.traverse(ctx, todo); err != nil {
			return err
		}
		todo = todo[:0]
		if !vbrDone { // the exception and interrupt vectors, once the reset path is known
			vbrDone = true
			todo = an.vectorEntries(todo)
			if len(todo) > 0 {
				continue
			}
		}

		// 2. right after a function's pool and padding: a prologue starts another function;
		//    without one it is a candidate that a pointer can confirm (step 3)
		gapc = gapc[:0]
		for i := range an.funcs {
			b := an.funcs[i].Addr + an.funcs[i].Pool
			next := an.hi
			if i+1 < len(an.funcs) {
				next = an.funcs[i+1].Addr
			}
			for b < next {
				op, ok := an.rd16(b)
				if !ok || (op != 0x0009 && op != 0x0000) {
					break
				}
				b += 2
			}
			if b >= next || an.funcAt(b) >= 0 {
				continue
			}
			if an.prologue(b) {
				todo = append(todo, b)
			} else {
				gapc = append(gapc, gapCand{a: b})
			}
		}
		if len(todo) > 0 {
			continue
		}
		if sweeps >= 2 {
			break
		}
		sweeps++

		// 3. aligned words outside code (literal pools, data) that point into code
		fi, nf := 0, len(an.funcs)
		for a := (an.lo + 3) &^ 3; a+4 <= an.hi && a >= an.lo; a += 4 {
			for fi < nf && an.funcs[fi].Addr+an.funcs[fi].Size <= a {
				fi++
			}
			if fi < nf && an.funcs[fi].Addr <= a {
				a = (an.funcs[fi].Addr+an.funcs[fi].Size+3)&^3 - 4
				continue
			}
			if v, ok := an.rd32(a); ok && an.inCode(an.canon(v)) {
				v = an.canon(v)
				g := sort.Search(len(gapc), func(i int) bool { return gapc[i].a >= v })
				if g < len(gapc) && gapc[g].a == v {
					gapc[g].pointed = true
				} else if an.funcContaining(v) < 0 && an.prologue(v) {
					todo = append(todo, v)
				}
			}
			if a&0xFFFF == 0 {
				an.progress("pointers", int(a-an.lo), int(an.hi-an.lo))
				if err := ctx.Err(); err != nil {
					return err
				}
			}
		}
		for _, g := range gapc {
			if g.pointed {
				if _, _, clean := an.explore(g.a, false); clean {
					todo = append(todo, g.a)
				}
			}
		}
		if len(todo) == 0 {
			break
		}
	}
	return nil
}

// vectorEntries appends VBR+0x100, +0x400 and +0x600 for every ldc Rn,vbr in the code found
// so far whose Rn comes from a literal or a mov #imm.
func (an *Analysis) vectorEntries(todo []uint32) []uint32 {
	for _, f := range an.funcs {
		for a := f.Addr; a < f.End(); a += 2 {
			op, ok := an.rd16(a)
			if !ok {
				break
			}
			if op&0xF0FF != 0x402E { // ldc Rn,vbr
				continue
			}
			v, ok := regConst(an.img.Word, a, int(op>>8)&15, 16)
			if !ok {
				continue
			}
			v = an.canon(v)
			known := false
			for _, x := range an.vbr {
				known = known || x == v
			}
			if !known {
				an.vbr = append(an.vbr, v)
			}
			for _, o := range []uint32{0x100, 0x400, 0x600} {
				if an.inCode(v+o) && an.funcAt(v+o) < 0 {
					todo = append(todo, v+o)
				}
			}
		}
	}
	return todo
}

// ---- queries ----

// Image is the analysed image.
func (an *Analysis) Image() *Image { return an.img }

// Syscalls is the syscall table the analysis names calls with (may be nil).
func (an *Analysis) Syscalls() *Syscalls { return an.opt.Syscalls }

// Region is the analysed code region [start, end).
func (an *Analysis) Region() (start, end uint32) { return an.lo, an.hi }

// Partial reports whether the analysis was stopped before it finished.
func (an *Analysis) Partial() bool {
	an.mu.RLock()
	defer an.mu.RUnlock()
	return an.partial
}

// VBR returns the vector base addresses found (OS images, with Options.VBR).
func (an *Analysis) VBR() []uint32 {
	an.mu.RLock()
	defer an.mu.RUnlock()
	return append([]uint32(nil), an.vbr...)
}

// Funcs returns the functions sorted by address (a copy).
func (an *Analysis) Funcs() []Func {
	an.mu.RLock()
	defer an.mu.RUnlock()
	return append([]Func(nil), an.funcs...)
}

// NumFuncs is the number of functions.
func (an *Analysis) NumFuncs() int {
	an.mu.RLock()
	defer an.mu.RUnlock()
	return len(an.funcs)
}

// FuncAt returns the function starting at addr.
func (an *Analysis) FuncAt(addr uint32) (Func, bool) {
	an.mu.RLock()
	defer an.mu.RUnlock()
	if i := an.funcAt(addr); i >= 0 {
		return an.funcs[i], true
	}
	return Func{}, false
}

// FuncContaining returns the function whose code holds addr (the last one starting at or
// before it, if addr is inside its code).
func (an *Analysis) FuncContaining(addr uint32) (Func, bool) {
	an.mu.RLock()
	defer an.mu.RUnlock()
	if i := an.funcContaining(addr); i >= 0 {
		return an.funcs[i], true
	}
	return Func{}, false
}

// FuncBefore returns the last function starting at or before addr.
func (an *Analysis) FuncBefore(addr uint32) (Func, bool) {
	an.mu.RLock()
	defer an.mu.RUnlock()
	if i := an.idxLE(addr); i >= 0 {
		return an.funcs[i], true
	}
	return Func{}, false
}

// AddFunction makes addr a function (a user's "code starts here") and follows it and the
// functions it calls, as the first step of Analyze; the pointer rules are not rerun. It
// returns the function at addr.
func (an *Analysis) AddFunction(ctx context.Context, addr uint32) (Func, error) {
	an.mu.Lock()
	defer an.mu.Unlock()
	if !an.inCode(addr) {
		return Func{}, errOutside
	}
	err := an.traverse(ctx, []uint32{addr})
	an.sortFuncs()
	an.xmu.Lock()
	an.xidx = nil
	an.xmu.Unlock()
	if i := an.funcAt(addr); i >= 0 {
		return an.funcs[i], err
	}
	return Func{}, err
}

type analysisError string

func (e analysisError) Error() string { return string(e) }

const errOutside = analysisError("sh4: address outside the analysed code region")

// Class says what a halfword of the image is, in a listing.
type Class uint8

const (
	ClassUnknown  Class = iota // outside every function's code and pool (decode it if you like)
	ClassCode                  // inside a function's code
	ClassPool                  // the first half of a 4-byte literal-pool word: show it as .long
	ClassPoolCont              // the second half of one
	ClassPadding               // the halfword between the code and an aligned pool
)

// Classify says what the halfword at addr is, by DASM's listing rule: between a function's
// code and the end of its pool every aligned word is a literal, except the halfword of padding
// right after the code.
func (an *Analysis) Classify(addr uint32) Class {
	an.mu.RLock()
	defer an.mu.RUnlock()
	i := an.idxLE(addr)
	if i < 0 {
		return ClassUnknown
	}
	f := an.funcs[i]
	if addr < f.End() {
		return ClassCode
	}
	if addr < f.PoolEnd() {
		switch {
		case addr&3 == 0:
			return ClassPool
		case addr-2 >= f.End():
			return ClassPoolCont
		default:
			return ClassPadding
		}
	}
	return ClassUnknown
}
