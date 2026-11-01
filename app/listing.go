package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"cg50emu/sh4"
)

// The disassembly is a virtual list over a whole address space (17 million rows for the
// 32 MB flash), so the page never holds it: it asks for batches of rows by row number, and
// for the row of an address. A rowMap is what makes that arithmetic: by default every
// halfword is one row, plus a label row before each function; the exceptions are units of
// several halfwords shown as one row (a 32-bit DSP instruction, a literal-pool word as .long,
// and a long run of erased or zero halfwords outside the code as one .fill row). With the
// labels and the exceptions sorted, the row of an address and the address of a row are
// binary searches, and a batch is a walk from there.

const (
	itemDSP  = 1  // a 32-bit DSP instruction: 2 halfwords
	itemLong = 2  // a literal-pool word: 2 halfwords
	itemFill = 3  // a run of one value (0xFFFF or 0x0000) outside the code
	minFill  = 16 // halfwords: shorter runs stay rows of their own
)

type item struct {
	off    uint32 // from the space's base, even
	halves uint32
	kind   uint8
	fill   uint16 // itemFill: the value
}

// rowMap maps rows to offsets in a space of size bytes.
type rowMap struct {
	size   uint32
	labels []uint32 // offsets of the labels (function starts), sorted
	items  []item   // the multi-halfword units, sorted, not overlapping
	extra  []uint32 // extra[i]: the rows items[:i] save (halves-1 each); len(items)+1
	count  int
}

func newRowMap(size uint32, labels []uint32, items []item) *rowMap {
	m := &rowMap{size: size &^ 1, labels: labels, items: items, extra: make([]uint32, len(items)+1)}
	for i, it := range items {
		m.extra[i+1] = m.extra[i] + it.halves - 1
	}
	m.count = int(m.size/2) - int(m.extra[len(items)]) + len(labels)
	return m
}

// linearMap is a space without an analysis: every halfword a row.
func linearMap(size uint32) *rowMap { return newRowMap(size, nil, nil) }

func (m *rowMap) labelsBefore(off uint32) int {
	return sort.Search(len(m.labels), func(i int) bool { return m.labels[i] >= off })
}

func (m *rowMap) hasLabel(off uint32) bool {
	i := m.labelsBefore(off)
	return i < len(m.labels) && m.labels[i] == off
}

// unit returns the unit holding off: its start, its size in halfwords, and its item (or -1).
func (m *rowMap) unit(off uint32) (start, halves uint32, idx int) {
	off &^= 1
	i := sort.Search(len(m.items), func(i int) bool { return m.items[i].off > off }) - 1
	if i >= 0 && m.items[i].off+2*m.items[i].halves > off {
		return m.items[i].off, m.items[i].halves, i
	}
	return off, 1, -1
}

// first is the first row of the unit at off (its label, if it has one). It does not go down
// within an item, so it is non-decreasing over the offsets.
func (m *rowMap) first(off uint32) int {
	off &^= 1
	i := sort.Search(len(m.items), func(i int) bool { return m.items[i].off >= off })
	ex := m.extra[i]
	if i > 0 {
		if it := m.items[i-1]; it.off+2*it.halves > off {
			ex = m.extra[i-1] + (off-it.off)/2
		}
	}
	return int(off/2) - int(ex) + m.labelsBefore(off)
}

// rowOf is the row showing off (the unit holding it, not its label); -1 outside the space.
func (m *rowMap) rowOf(off uint32) int {
	if off >= m.size {
		return -1
	}
	u, _, _ := m.unit(off)
	r := m.first(u)
	if m.hasLabel(u) {
		r++
	}
	return r
}

// at returns the unit shown on row r, and whether r is its label row.
func (m *rowMap) at(r int) (off uint32, label bool, ok bool) {
	if r < 0 || r >= m.count {
		return 0, false, false
	}
	n := int(m.size / 2)
	h := sort.Search(n, func(h int) bool { return m.first(uint32(h)*2) > r }) - 1
	u, _, _ := m.unit(uint32(h) * 2)
	return u, m.hasLabel(u) && m.first(u) == r, true
}

// buildRowMap lays out an analysed space: base is the space's first address in the
// analysis' coordinates, word reads the analysed image.
func buildRowMap(an *sh4.Analysis, word sh4.ReadFunc, base, size uint32) *rowMap {
	end := base + size
	var labels []uint32
	var items []item
	gap := func(from, to uint32) {
		for a := from; a < to; {
			w, ok := word(a)
			if !ok {
				a += 2
				continue
			}
			if w == 0xFFFF || w == 0x0000 {
				b := a + 2
				for b < to {
					if v, ok := word(b); !ok || v != w {
						break
					}
					b += 2
				}
				if n := (b - a) / 2; n >= minFill {
					items = append(items, item{off: a - base, halves: n, kind: itemFill, fill: w})
				}
				a = b
				continue
			}
			if sh4.InsnLen(w) == 4 && a+4 <= to {
				items = append(items, item{off: a - base, halves: 2, kind: itemDSP})
				a += 4
				continue
			}
			a += 2
		}
	}
	pos := base
	var funcs []sh4.Func
	if an != nil {
		funcs = an.Funcs()
	}
	for i, f := range funcs {
		if f.Addr < pos || f.Addr >= end || f.Addr&1 != 0 {
			continue
		}
		gap(pos, f.Addr)
		labels = append(labels, f.Addr-base)
		seg := min(f.PoolEnd(), end)
		if i+1 < len(funcs) && funcs[i+1].Addr > f.Addr {
			seg = min(seg, funcs[i+1].Addr)
		}
		seg = max(seg, f.Addr+2) &^ 1
		code := min(f.End(), seg)
		a := f.Addr
		for a < code {
			if w, ok := word(a); ok && sh4.InsnLen(w) == 4 && a+4 <= code {
				items = append(items, item{off: a - base, halves: 2, kind: itemDSP})
				a += 4
			} else {
				a += 2
			}
		}
		for a < seg {
			if a&3 == 0 && a+4 <= seg {
				items = append(items, item{off: a - base, halves: 2, kind: itemLong})
				a += 4
			} else {
				a += 2
			}
		}
		pos = seg
	}
	gap(pos, end)
	return newRowMap(size, labels, items)
}

// ---- rows ------------------------------------------------------------------------------

// Row is one line of the disassembly, as the page draws it (panels/disasm.js).
type Row struct {
	Kind    string  `json:"kind"` // "insn", "label", "data", "fill"
	Addr    uint32  `json:"addr"`
	Bytes   string  `json:"bytes,omitempty"`
	Mnem    string  `json:"mnem,omitempty"`
	Ops     string  `json:"ops,omitempty"`
	Comment string  `json:"comment,omitempty"`
	Target  *uint32 `json:"target,omitempty"` // an address the operand refers to: a link
	Text    string  `json:"text,omitempty"`   // label rows: the name
	Unk     bool    `json:"unk,omitempty"`    // outside the detected code (decoded as is)
	Size    int     `json:"size,omitempty"`   // bytes
}

// listing is one address space of the disassembly.
type listing struct {
	id, name   string
	base, size uint32
	rows       *rowMap
	an         *sh4.Analysis // nil: not analysed (RAM): every halfword decoded as is
	shift      uint32        // display address - analysis address (the P2 view of the flash)
	static     *sh4.Image    // the analysed image, for reads outside the live window
	sys        *sh4.Syscalls
}

// names answers "what is called here" across the analyses (analysis.go).
type names interface {
	funcName(addr uint32) string // the function starting at addr, "" if none
	symbol(addr uint32) string   // "name+0x1C", "" if no function holds addr
}

func (l *listing) contains(addr uint32) bool { return addr-l.base < l.size }

func (l *listing) rowOf(addr uint32) int {
	if !l.contains(addr) {
		return -1
	}
	return l.rows.rowOf(addr - l.base)
}

type unitRef struct {
	off    uint32
	halves uint32
	kind   uint8
	fill   uint16
	label  bool
}

// units lists the units shown on rows [first, first+n).
func (l *listing) units(first, n int) []unitRef {
	m := l.rows
	off, label, ok := m.at(first)
	if !ok {
		return nil
	}
	out := make([]unitRef, 0, n)
	li := m.labelsBefore(off)
	_, _, ii := m.unit(off)
	if ii < 0 {
		ii = sort.Search(len(m.items), func(i int) bool { return m.items[i].off >= off })
	}
	for len(out) < n && off < m.size {
		u := unitRef{off: off, halves: 1}
		if ii < len(m.items) && m.items[ii].off == off {
			it := m.items[ii]
			u.halves, u.kind, u.fill = it.halves, it.kind, it.fill
			ii++
		}
		hasLabel := li < len(m.labels) && m.labels[li] == off
		if hasLabel {
			li++
			if label {
				lu := u
				lu.label = true
				out = append(out, lu)
				if len(out) == n {
					break
				}
			}
		}
		label = true // every later unit starts at its label row
		out = append(out, u)
		off += 2 * u.halves
	}
	return out
}

// Rows renders rows [first, first+n). read copies live memory at a display address into dst
// and returns how many bytes it could (the rest is unreadable).
func (l *listing) Rows(first, n int, read func(addr uint32, dst []byte) int, nm names) []Row {
	us := l.units(first, n)
	if len(us) == 0 {
		return []Row{}
	}
	last := us[len(us)-1]
	lo := l.base + us[0].off
	hi := l.base + last.off + 2*last.halves
	// the window: some room before (indirect jumps are resolved by scanning back) and after
	// (literal pools are up to 1 KB ahead); a fill row needs only its first word
	wlo := lo - min(64, lo-l.base)
	whi := min(hi+0x400, l.base+l.size)
	if hi-lo > 0x10000 {
		whi = min(lo+0x10000, whi)
	}
	buf := make([]byte, whi-wlo)
	got := read(wlo, buf)
	rd := func(a uint32) (uint16, bool) {
		if o := a - wlo; o+1 < uint32(got) && a >= wlo {
			return uint16(buf[o])<<8 | uint16(buf[o+1]), true
		}
		if l.static != nil {
			return l.static.Word(a - l.shift)
		}
		return 0, false
	}
	rd32 := func(a uint32) (uint32, bool) {
		h, ok1 := rd(a)
		w, ok2 := rd(a + 2)
		return uint32(h)<<16 | uint32(w), ok1 && ok2
	}
	rows := make([]Row, 0, len(us))
	for _, u := range us {
		a := l.base + u.off
		if u.label {
			rows = append(rows, l.labelRow(a, nm))
			continue
		}
		switch u.kind {
		case itemFill:
			r := Row{Kind: "fill", Addr: a, Mnem: ".fill", Size: int(2 * u.halves)}
			r.Ops = fmt.Sprintf("0x%x bytes", 2*u.halves)
			if u.fill == 0xFFFF {
				r.Comment, r.Bytes = "erased (0xFF)", "FFFF…"
			} else {
				r.Comment, r.Bytes = "zeros", "0000…"
			}
			if w, ok := rd(a); ok && w != u.fill {
				r.Comment += ", changed since the analysis"
			}
			rows = append(rows, r)
		case itemLong:
			v, ok := rd32(a)
			r := Row{Kind: "data", Addr: a, Mnem: ".long", Size: 4}
			if !ok {
				r.Mnem, r.Comment = "??", "unreadable"
			} else {
				r.Bytes = fmt.Sprintf("%04X%04X", v>>16, v&0xFFFF)
				r.Ops = fmt.Sprintf("0x%08x", v)
				if s := nm.funcName(v); s != "" {
					r.Comment, r.Target = s, ptr(v)
				} else if s := l.sys.Name(v); s != "" {
					r.Comment = s
				}
			}
			rows = append(rows, r)
		default:
			rows = append(rows, l.insnRow(a, u, rd, nm))
		}
	}
	return rows
}

func ptr(v uint32) *uint32 { return &v }

func (l *listing) labelRow(a uint32, nm names) Row {
	r := Row{Kind: "label", Addr: a, Text: nm.funcName(a)}
	if r.Text == "" {
		r.Text = fmt.Sprintf("sub_%08x", a)
	}
	var notes []string
	if l.an != nil {
		aa := a - l.shift
		if num, ok := l.sys.Number(aa); ok {
			notes = append(notes, fmt.Sprintf("syscall 0x%04X", num))
		}
		if n := len(l.an.Callers(aa)); n == 1 {
			notes = append(notes, "1 caller")
		} else if n > 1 {
			notes = append(notes, strconv.Itoa(n)+" callers")
		}
	}
	if len(notes) > 0 {
		r.Comment = "; " + strings.Join(notes, " · ")
	}
	return r
}

func (l *listing) insnRow(a uint32, u unitRef, rd sh4.ReadFunc, nm names) Row {
	r := Row{Kind: "insn", Addr: a}
	if l.an != nil {
		cl := l.an.Classify(a - l.shift)
		if cl == sh4.ClassPool || cl == sh4.ClassPoolCont || cl == sh4.ClassPadding {
			w, ok := rd(a)
			r.Kind, r.Mnem, r.Size = "data", ".word", 2
			if ok {
				r.Bytes, r.Ops = fmt.Sprintf("%04X", w), fmt.Sprintf("0x%04x", w)
			} else {
				r.Mnem, r.Comment = "??", "unreadable"
			}
			return r
		}
		r.Unk = cl == sh4.ClassUnknown
	}
	w, ok := rd(a)
	if !ok {
		r.Kind, r.Mnem, r.Comment, r.Size = "data", "??", "unreadable", 2
		return r
	}
	in := sh4.DecodeWord(w, a, rd)
	if u.halves == 1 && in.Size == 4 {
		// a DSP first word the layout didn't pair (data, or its second word unreadable)
		in = sh4.Instr{Addr: a, Op: w, Size: 2, Text: fmt.Sprintf(".word 0x%04x", w), Kind: sh4.KindIllegal}
	}
	r.Size = in.Size
	if in.Size == 4 {
		r.Bytes = fmt.Sprintf("%04X %04X", in.Op, in.Op2)
	} else {
		r.Bytes = fmt.Sprintf("%04X", in.Op)
	}
	r.Mnem, r.Ops = in.Mnemonic(), in.Operands()
	if in.Kind == sh4.KindIllegal {
		r.Kind = "data"
	}
	c := sh4.Comment(rd, l.sys, &in)
	switch in.Kind {
	case sh4.KindBranch, sh4.KindCondBranch, sh4.KindCall:
		r.Target = ptr(in.Target)
		if s := nm.funcName(in.Target); s != "" {
			r.Ops, c = s, ""
		}
	case sh4.KindLiteral:
		if in.LitOK && in.LitSize == 4 {
			if s := nm.funcName(in.LitValue); s != "" {
				r.Target = ptr(in.LitValue)
				if l.sys.Name(in.LitValue) == "" {
					c += " " + s
				}
			}
		}
	case sh4.KindMova:
		r.Target = ptr(in.LitAddr)
	case sh4.KindJumpReg, sh4.KindCallReg:
		if x, ok := sh4.ResolveIndirect(rd, l.sys, &in); ok {
			r.Target = ptr(x.Target)
			if x.Name == "" && x.Syscall < 0 {
				if s := nm.funcName(x.Target); s != "" {
					c = "-> " + s
				}
			}
		}
	}
	if c != "" {
		r.Comment = "; " + c
	}
	return r
}
