//go:build probe

package cg50emu

// Probe (2026-10-11, for the wiki's Number format page): type numbers into Run-Matrix and
// record the BCD values the OS hands to the result formatter (0x800FC5A4) and to the BCD
// decoder (0x8004B580), plus the order in which the formatter chain's functions are entered
// and from where (PR at entry).
//
//   go -C emu_go test -tags probe -run TestNumFormat -count=1 -v .

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

var (
	nfDot, nfEXP, nfNEG = [2]uint32{5, 1}, [2]uint32{4, 1}, [2]uint32{3, 1}
	nfPLUS, nfMINUS     = [2]uint32{3, 2}, [2]uint32{2, 2}
	nfMUL, nfSHIFT      = [2]uint32{3, 3}, [2]uint32{6, 8}
	nfAC                = [2]uint32{0, 0}
	nfDigit             = [10][2]uint32{{6, 1}, {6, 2}, {5, 2}, {4, 2}, {6, 3}, {5, 3}, {4, 3}, {6, 4}, {5, 4}, {4, 4}}
)

// the formatter chain named on the page
var nfChain = map[uint32]string{
	0x800FC5A4: "fmt", 0x800F790E: "norm", 0x8004C21A: "c21a", 0x8004C69C: "c69c",
	0x8004B270: "b270", 0x8004B2B0: "round", 0x8005DC06: "dc06", 0x8004B580: "decode",
	0x80072FC8: "aluAdd", 0x8007306E: "aluSub",
}

type nfRun struct {
	t      *testing.T
	e      *Emulator
	trace  bool
	calls  []string
	fmtIn  []string
	decIn  map[string]bool
	decOrd []string
	sawFmt bool
	alu    map[string]int // "pre|post pc cmd" -> count of BCD ALU command writes
}

func (r *nfRun) bytesAt(a uint32, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%02x ", r.e.mem.R8(a+uint32(i)))
	}
	return strings.TrimSpace(b.String())
}

func (r *nfRun) run(n uint64) {
	e, c := r.e, r.e.cpu
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
		if r.trace && r.alu != nil && !c.mem.mmuAt && c.pc >= 0x80000000 {
			op := c.mem.R16(c.pc)
			var tgt, val uint32
			switch {
			case op&0xF00F == 0x2001: // mov.w Rm,@Rn
				tgt, val = c.r[op>>8&0xF], c.r[op>>4&0xF]
			case op&0xFF00 == 0x8100: // mov.w R0,@(disp,Rn)
				tgt, val = c.r[op>>4&0xF]+(op&0xF)*2, c.r[0]
			}
			if tgt == 0xA4CB0010 {
				ph := "pre"
				if r.sawFmt {
					ph = "post"
				}
				r.alu[fmt.Sprintf("%s %08x cmd%d", ph, c.pc, val&0xFFFF)]++
			}
		}
		if r.trace {
			if c.pc == 0x800FC5A4 {
				r.sawFmt = true
			}
			if name, ok := nfChain[c.pc]; ok {
				if len(r.calls) < 20000 {
					r.calls = append(r.calls, fmt.Sprintf("%s(pr=%08x)", name, c.pr))
				}
				switch name {
				case "fmt":
					r.fmtIn = append(r.fmtIn, fmt.Sprintf("r4=%08x [%s] r5=%08x r6=%08x", c.r[4], r.bytesAt(c.r[4], 24), c.r[5], c.r[6]))
				case "decode":
					s := r.bytesAt(c.r[5], 9)
					if !r.decIn[s] {
						r.decIn[s] = true
						r.decOrd = append(r.decOrd, s)
					}
				}
			}
		}
		e.Step(1)
	}
}

func (r *nfRun) tap(ks ...[2]uint32) {
	for _, k := range ks {
		r.e.InjectKey(k[0], k[1])
		r.run(30_000_000)
	}
}

func (r *nfRun) number(s string) []([2]uint32) {
	var ks [][2]uint32
	for _, ch := range s {
		switch {
		case ch >= '0' && ch <= '9':
			ks = append(ks, nfDigit[ch-'0'])
		case ch == '.':
			ks = append(ks, nfDot)
		case ch == 'E':
			ks = append(ks, nfEXP)
		case ch == '~': // (-)
			ks = append(ks, nfNEG)
		case ch == '+':
			ks = append(ks, nfPLUS)
		case ch == '-':
			ks = append(ks, nfMINUS)
		case ch == '*':
			ks = append(ks, nfMUL)
		case ch == '/':
			ks = append(ks, rcDIV)
		case ch == 'f': // a b/c (fraction template)
			ks = append(ks, [2]uint32{6, 5})
		case ch == 'R': // RIGHT (leave a template box)
			ks = append(ks, rcRIGHT)
		case ch == 'i': // SHIFT 0
			ks = append(ks, nfSHIFT, nfDigit[0])
		}
	}
	return ks
}

func TestNumFormat(t *testing.T) {
	img, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no image: ", err)
	}
	e := NewEmulator(img)
	e.mem.W32(warmFlagAddr, 1)
	r := &nfRun{t: t, e: e, decIn: map[string]bool{}}
	r.run(600_000_000)
	t.Logf("after boot: menu=%v fault=%q", e.atMainMenu(), e.Fault())
	r.tap(rc1) // Run-Matrix
	exprs := []string{"98765", "~98765", "0.5", "0.000123", "~0.000123", "0", "1/3", "1.5E~12",
		"9E99", "1E~99", "123456789012345", "2+3i"}
	if x := os.Getenv("NF_EXPRS"); x != "" {
		exprs = strings.Split(x, ",")
	}
	for i, x := range exprs {
		r.tap(r.number(x)...)
		r.calls, r.fmtIn, r.decIn, r.decOrd = nil, nil, map[string]bool{}, nil
		r.trace = true
		r.tap(rcEXE)
		r.trace = false
		t.Logf("=== %q", x)
		for _, f := range r.fmtIn {
			t.Logf("  fmt in: %s", f)
		}
		for _, d := range r.decOrd {
			t.Logf("  decode in: %s", d)
		}
		if i == 0 {
			t.Logf("  calls: %s", strings.Join(r.calls, " "))
		}
		savePNG(t, e, fmt.Sprintf("numfmt_%d", i))
		if i%4 == 3 {
			r.tap(nfAC)
		}
	}
	t.Logf("fault=%q", e.Fault())
}

// TestNumArith: which BCD ALU routines an arithmetic operation runs, and from where, before
// the result reaches the formatter.
func TestNumArith(t *testing.T) {
	img, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no image: ", err)
	}
	e := NewEmulator(img)
	e.mem.W32(warmFlagAddr, 1)
	r := &nfRun{t: t, e: e, decIn: map[string]bool{}}
	r.run(600_000_000)
	r.tap(rc1)
	for i, x := range []string{"12+34", "50-8", "7*8", "1/7", "2+3i*1"} {
		r.tap(r.number(x)...)
		r.calls, r.fmtIn, r.alu, r.sawFmt = nil, nil, map[string]int{}, false
		r.trace = true
		r.tap(rcEXE)
		r.trace = false
		pre := map[string]int{}
		var order []string
		for _, c := range r.calls {
			if strings.HasPrefix(c, "fmt(") {
				break
			}
			if strings.HasPrefix(c, "alu") {
				if pre[c] == 0 {
					order = append(order, c)
				}
				pre[c]++
			}
		}
		t.Logf("=== %q  (%d chain calls)", x, len(r.calls))
		for _, c := range order {
			t.Logf("  before fmt: %s ×%d", c, pre[c])
		}
		var ks []string
		for k := range r.alu {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			t.Logf("  ALU write: %s ×%d", k, r.alu[k])
		}
		for _, f := range r.fmtIn {
			t.Logf("  fmt in: %s", f)
		}
		savePNG(t, e, fmt.Sprintf("numarith_%d", i))
	}
}

// TestNumFraction: what a fraction result (a b/c) hands the formatter.
func TestNumFraction(t *testing.T) {
	img, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no image: ", err)
	}
	e := NewEmulator(img)
	e.mem.W32(warmFlagAddr, 1)
	r := &nfRun{t: t, e: e, decIn: map[string]bool{}}
	r.run(600_000_000)
	r.tap(rc1)
	for i, x := range []string{"f1R3", "f2R4", "f~5R7", "f12R7", "f1R12", "f123R1000"} {
		r.tap(r.number(x)...)
		r.calls, r.fmtIn, r.decIn, r.decOrd = nil, nil, map[string]bool{}, nil
		r.trace = true
		r.tap(rcEXE)
		r.trace = false
		t.Logf("=== %q", x)
		for _, f := range r.fmtIn {
			t.Logf("  fmt in: %s", f)
		}
		for _, d := range r.decOrd {
			t.Logf("  decode in: %s", d)
		}
		savePNG(t, e, fmt.Sprintf("numfrac_%d", i))
	}
}
