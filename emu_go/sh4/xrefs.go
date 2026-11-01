package sh4

import "sort"

// Cross-references, as DASM's an_xrefs finds them for one target, but for every target at
// once: one pass over the code and data builds an index, then each lookup is a binary search.

// XrefKind is how an instruction or a data word refers to an address.
type XrefKind uint8

const (
	XrefCall   XrefKind = iota // bsr, or jsr @Rn with Rn loaded with the target
	XrefJump                   // bra, or jmp @Rn (a tail call)
	XrefBranch                 // bt bf bt/s bf/s
	XrefLoad                   // mov.l @(disp,pc),Rn loading the target (a pointer used otherwise)
	XrefMova                   // mova pointing at it
	XrefData                   // an aligned data word outside code holding it
)

var xrefKindNames = [...]string{"call", "jump", "branch", "load", "mova", "data"}

func (k XrefKind) String() string {
	if int(k) < len(xrefKindNames) {
		return xrefKindNames[k]
	}
	return "xref?"
}

// Xref is one reference: the instruction or data word at From refers to the target.
type Xref struct {
	From uint32
	Kind XrefKind
}

type xent struct {
	target, from uint32
	kind         XrefKind
}

type xrefIndex struct {
	e      []xent  // sorted by target, then from
	byFrom []int32 // indexes into e, sorted by from
}

// buildXrefs scans the function code and the data words outside it.
func (an *Analysis) buildXrefs() *xrefIndex {
	e := make([]xent, 0, len(an.funcs)*8)
	pools := make([]uint64, (an.hi-an.lo)/4/64+2) // pool words reported through their load
	poolBit := func(la uint32) (int, uint64) {
		w := (la - an.lo&^3) / 4
		return int(w / 64), 1 << (w % 64)
	}
	for _, f := range an.funcs {
		var lv, lpc [16]uint32
		var lidx [16]int
		var lmask uint16
		a1 := f.Addr + f.Size
		for a := f.Addr; a < a1; a += 2 {
			op, ok := an.rd16(a)
			if !ok {
				break
			}
			n, h := int(op>>8)&15, op>>12
			call, end := false, false
			if h == 0xF { // DSP: moves r0-r7
				lmask &= 0xFF00
				a += uint32(InsnLen(uint16(op))) - 2 // skip a second word
				continue
			}
			if h == 0xD { // mov.l @(disp,pc),Rn
				la := a&^3 + 4 + (op&0xFF)*4
				if v, ok := an.rd32(la); ok {
					lv[n], lpc[n], lidx[n] = v, a, len(e)
					lmask |= 1 << n
					e = append(e, xent{v, a, XrefLoad})
					i, b := poolBit(la)
					pools[i] |= b
				} else {
					lmask &^= 1 << n
				}
				continue
			}
			switch {
			case h == 0xB || h == 0xA: // bsr / bra
				t := a + 4 + uint32(s12(op&0xFFF)*2)
				if h == 0xB {
					e = append(e, xent{t, a, XrefCall})
					call = true
				} else {
					e = append(e, xent{t, a, XrefJump})
					end = true
				}
			case op&0xF900 == 0x8900: // bt bf bt/s bf/s
				e = append(e, xent{a + 4 + uint32(s8(op&0xFF)*2), a, XrefBranch})
			case op&0xFF00 == 0xC700: // mova
				e = append(e, xent{a&^3 + 4 + (op&0xFF)*4, a, XrefMova})
			case op&0xF0FF == 0x400B || op&0xF0FF == 0x402B: // jsr / jmp @Rn
				k := XrefJump
				if op&0xF0FF == 0x400B {
					k = XrefCall
				}
				if (lmask>>n)&1 != 0 {
					// report the call instead of the load that fed it, when that load is still
					// the target's latest reference
					t, li := lv[n], lidx[n]
					latest := e[li].kind == XrefLoad && e[li].from == lpc[n]
					for j := li + 1; latest && j < len(e); j++ {
						latest = e[j].target != t
					}
					if latest {
						e[li] = xent{t, a, k}
					} else {
						e = append(e, xent{t, a, k})
					}
				}
				if k == XrefCall {
					call = true
				} else {
					end = true
				}
			case op == 0x000B || op == 0x002B || op&0xF0FF == 0x0023:
				end = true
			}
			if writesRn(op) {
				lmask &^= 1 << n
			}
			if call {
				lmask &= 0xFF00
			}
			if end {
				lmask = 0
			}
		}
	}
	// data: aligned words outside code (0 and 0xFFFFFFFF are not indexed)
	fi, nf := 0, len(an.funcs)
	for a := (an.lo + 3) &^ 3; a+4 <= an.hi && a >= an.lo; a += 4 {
		for fi < nf && an.funcs[fi].Addr+an.funcs[fi].Size <= a {
			fi++
		}
		if fi < nf && an.funcs[fi].Addr <= a {
			a = (an.funcs[fi].Addr+an.funcs[fi].Size+3)&^3 - 4
			continue
		}
		v, ok := an.rd32(a)
		if !ok || v == 0 || v == 0xFFFFFFFF {
			continue
		}
		if i, b := poolBit(a); pools[i]&b != 0 {
			continue
		}
		e = append(e, xent{v, a, XrefData})
	}
	if an.opt.P2Mirror {
		for i := range e {
			e[i].target = an.canon(e[i].target)
		}
	}
	sort.Slice(e, func(i, j int) bool {
		if e[i].target != e[j].target {
			return e[i].target < e[j].target
		}
		return e[i].from < e[j].from
	})
	byFrom := make([]int32, len(e))
	for i := range byFrom {
		byFrom[i] = int32(i)
	}
	sort.Slice(byFrom, func(i, j int) bool { return e[byFrom[i]].from < e[byFrom[j]].from })
	return &xrefIndex{e: e, byFrom: byFrom}
}

func (an *Analysis) index() *xrefIndex {
	an.xmu.Lock()
	defer an.xmu.Unlock()
	if an.xidx == nil {
		an.xidx = an.buildXrefs()
	}
	return an.xidx
}

// BuildXrefs builds the cross-reference index now (XrefsTo builds it on first use); a UI can
// call it in the background after Analyze. AddFunction drops it.
func (an *Analysis) BuildXrefs() {
	an.mu.RLock()
	defer an.mu.RUnlock()
	an.progress("references", 0, 0)
	an.index()
}

// NumXrefs is the size of the index.
func (an *Analysis) NumXrefs() int {
	an.mu.RLock()
	defer an.mu.RUnlock()
	return len(an.index().e)
}

func (x *xrefIndex) find(target uint32) []xent {
	lo := sort.Search(len(x.e), func(i int) bool { return x.e[i].target >= target })
	hi := lo
	for hi < len(x.e) && x.e[hi].target == target {
		hi++
	}
	return x.e[lo:hi]
}

// from returns the references made from [lo, hi).
func (x *xrefIndex) from(lo, hi uint32) []xent {
	i := sort.Search(len(x.byFrom), func(i int) bool { return x.e[x.byFrom[i]].from >= lo })
	var out []xent
	for ; i < len(x.byFrom) && x.e[x.byFrom[i]].from < hi; i++ {
		out = append(out, x.e[x.byFrom[i]])
	}
	return out
}

// XrefsFrom returns the references made by the instructions and data words in [lo, hi),
// with their targets, sorted by address.
func (an *Analysis) XrefsFrom(lo, hi uint32) (from []Xref, targets []uint32) {
	an.mu.RLock()
	defer an.mu.RUnlock()
	for _, x := range an.index().from(lo, hi) {
		from = append(from, Xref{x.from, x.kind})
		targets = append(targets, x.target)
	}
	return from, targets
}

// XrefsTo returns every instruction (inside detected code) and aligned data word (outside it)
// that refers to target, sorted by address: what DASM's X,θ,T key lists. A literal-pool word
// read by a mov.l is reported as that load, not as data.
func (an *Analysis) XrefsTo(target uint32) []Xref {
	an.mu.RLock()
	defer an.mu.RUnlock()
	ents := an.index().find(target)
	out := make([]Xref, len(ents))
	for i, x := range ents {
		out[i] = Xref{x.from, x.kind}
	}
	return out
}

// Callers returns the functions that call or jump to the function at addr (sorted, unique).
func (an *Analysis) Callers(addr uint32) []uint32 {
	an.mu.RLock()
	defer an.mu.RUnlock()
	var out []uint32
	for _, x := range an.index().find(addr) {
		if x.kind == XrefCall || x.kind == XrefJump {
			if i := an.funcContaining(x.from); i >= 0 {
				out = append(out, an.funcs[i].Addr)
			}
		}
	}
	return sortUniq(out)
}

// Callees returns the functions that the function at addr calls or tail-calls (bsr, jsr/jmp
// through a literal, bra out of its code), sorted and unique.
func (an *Analysis) Callees(addr uint32) []uint32 {
	an.mu.RLock()
	defer an.mu.RUnlock()
	i := an.funcAt(addr)
	if i < 0 {
		return nil
	}
	f := an.funcs[i]
	var out []uint32
	for _, x := range an.index().from(f.Addr, f.End()) {
		if (x.kind == XrefCall || x.kind == XrefJump) && (x.target < f.Addr || x.target >= f.End()) &&
			an.funcAt(x.target) >= 0 {
			out = append(out, x.target)
		}
	}
	return sortUniq(out)
}
