package sh4

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// asm builds an image from (address, words...) pieces; 32-bit values are given as two words.
type asm struct {
	base uint32
	data []byte
}

func newAsm(base uint32, size int) *asm { return &asm{base, make([]byte, size)} }

func (s *asm) w(addr uint32, words ...uint16) {
	for i, w := range words {
		o := addr - s.base + uint32(2*i)
		s.data[o], s.data[o+1] = byte(w>>8), byte(w)
	}
}

func (s *asm) l(addr uint32, v uint32) { s.w(addr, uint16(v>>16), uint16(v)) }

func (s *asm) bytes(addr uint32, b ...byte) { copy(s.data[addr-s.base:], b) }

// testProgram is a small add-in exercising every rule of the function detection.
func testProgram() *Image {
	const B = AddinCode
	s := newAsm(B, 0x6e)
	// start: calls funcB through a literal, funcC with bsr
	s.w(B+0x00, 0xd104) // mov.l @(0x10,pc),r1 = 0x300018
	s.w(B+0x02, 0x4f22) // sts.l pr,@-r15
	s.w(B+0x04, 0x410b) // jsr @r1
	s.w(B+0x06, 0x0009) // nop
	s.w(B+0x08, 0xb00a) // bsr 0x300020
	s.w(B+0x0a, 0x0009)
	s.w(B+0x0c, 0x4f26) // lds.l @r15+,pr
	s.w(B+0x0e, 0x000b) // rts
	s.w(B+0x10, 0x0009)
	s.w(B+0x12, 0x0009) // padding
	s.l(B+0x14, B+0x18) // literal: funcB
	// funcB
	s.w(B+0x18, 0xe005, 0x000b, 0x0009, 0x0009) // mov #5,r0; rts; nop; (pad)
	// funcC: a gcc jump table with three cases and a default
	s.w(B+0x20, 0xe102) // mov #2,r1
	s.w(B+0x22, 0x3416) // cmp/hi r1,r4
	s.w(B+0x24, 0x890c) // bt 0x300040
	s.w(B+0x26, 0xc702) // mova @(0x8,pc),r0 = 0x300030
	s.w(B+0x28, 0x024c) // mov.b @(r0,r4),r2
	s.w(B+0x2a, 0x0223) // braf r2
	s.w(B+0x2c, 0x0009)
	s.w(B+0x2e, 0x0009)
	s.bytes(B+0x30, 0x06, 0x0a, 0x0e, 0x00) // cases at 0x30002e + 6, 10, 14
	s.w(B+0x34, 0x000b, 0xe001)             // rts; mov #1,r0
	s.w(B+0x38, 0x000b, 0xe002)
	s.w(B+0x3c, 0x000b, 0xe003)
	s.w(B+0x40, 0x000b, 0xe000) // default
	// funcD: right after funcC, with a prologue (the gap rule)
	s.w(B+0x44, 0x2fe6, 0x6ef6, 0x000b, 0x0009)
	// funcE: right after funcD, no prologue, but a data word points at it
	s.w(B+0x4c, 0xe007, 0x000b, 0x0009, 0x0009)
	s.l(B+0x54, B+0x4c)
	// funcF: anywhere, a data word points at it and it has a prologue
	s.l(B+0x58, B+0x60)
	s.l(B+0x5c, 0x12345678)
	s.w(B+0x60, 0x4f22, 0x4f26, 0x000b, 0x0009)
	// an orphan: nothing points at it
	s.w(B+0x68, 0xe001, 0x000b, 0x0009)
	return NewImage(B, s.data)
}

func TestAnalyzeProgram(t *testing.T) {
	img := testProgram()
	an, err := Analyze(context.Background(), img, AddinOptions(img, nil))
	if err != nil {
		t.Fatal(err)
	}
	const B = AddinCode
	want := []Func{
		{B + 0x00, 0x12, 0x18}, // code to the delay slot, pool to the literal
		{B + 0x18, 0x06, 0x06},
		{B + 0x20, 0x24, 0x24}, // all cases reached through the table
		{B + 0x44, 0x08, 0x08},
		{B + 0x4c, 0x06, 0x06},
		{B + 0x60, 0x08, 0x08},
	}
	if got := an.Funcs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("functions\n got %x\nwant %x", got, want)
	}
	if an.Partial() {
		t.Error("partial")
	}

	xr := func(target uint32) string { return fmt.Sprint(an.XrefsTo(target)) }
	for _, tc := range []struct {
		target uint32
		want   []Xref
	}{
		{B + 0x18, []Xref{{B + 0x04, XrefCall}}}, // the jsr, not the load that fed it
		{B + 0x20, []Xref{{B + 0x08, XrefCall}}},
		{B + 0x30, []Xref{{B + 0x26, XrefMova}}},
		{B + 0x40, []Xref{{B + 0x24, XrefBranch}}},
		{B + 0x4c, []Xref{{B + 0x54, XrefData}}},
		{B + 0x60, []Xref{{B + 0x58, XrefData}}},
		{0x12345678, []Xref{{B + 0x5c, XrefData}}},
	} {
		if got := xr(tc.target); got != fmt.Sprint(tc.want) {
			t.Errorf("xrefs to %08x: %s, want %v", tc.target, got, tc.want)
		}
		if got := fmt.Sprint(an.xrefsScan(tc.target, -1)); got != xr(tc.target) {
			t.Errorf("xrefs to %08x: scan %s, index %s", tc.target, got, xr(tc.target))
		}
	}
	if got := an.Callees(B); fmt.Sprint(got) != fmt.Sprint([]uint32{B + 0x18, B + 0x20}) {
		t.Errorf("callees %x", got)
	}
	if got := an.Callers(B + 0x18); fmt.Sprint(got) != fmt.Sprint([]uint32{B}) {
		t.Errorf("callers %x", got)
	}
	from, targets := an.XrefsFrom(B, B+0x12)
	if len(from) != 2 || targets[0] != B+0x18 || targets[1] != B+0x20 {
		t.Errorf("XrefsFrom %v %x", from, targets)
	}

	for _, tc := range []struct {
		a uint32
		c Class
	}{
		{B + 0x0e, ClassCode}, {B + 0x12, ClassPadding}, {B + 0x14, ClassPool},
		{B + 0x16, ClassPoolCont}, {B + 0x5c, ClassUnknown}, {B + 0x68, ClassUnknown},
	} {
		if c := an.Classify(tc.a); c != tc.c {
			t.Errorf("Classify(%08x) = %d, want %d", tc.a, c, tc.c)
		}
	}
	if f, ok := an.FuncContaining(B + 0x3c); !ok || f.Addr != B+0x20 {
		t.Errorf("FuncContaining %v %v", f, ok)
	}
	if _, ok := an.FuncContaining(B + 0x14); ok {
		t.Error("a pool word is not code")
	}
	if f, ok := an.FuncBefore(B + 0x5c); !ok || f.Addr != B+0x4c {
		t.Errorf("FuncBefore %v", f)
	}
	if an.FuncName(B) != "start" || an.FuncName(B+0x18) != "sub_00300018" {
		t.Errorf("names %q %q", an.FuncName(B), an.FuncName(B+0x18))
	}

	// the orphan, added by hand
	f, err := an.AddFunction(context.Background(), B+0x68)
	if err != nil || f != (Func{B + 0x68, 6, 6}) || an.NumFuncs() != 7 {
		t.Errorf("AddFunction: %v %v, %d functions", f, err, an.NumFuncs())
	}
	if _, err := an.AddFunction(context.Background(), 0x100); err == nil {
		t.Error("AddFunction outside the region")
	}
}

func TestAnalyzeCancel(t *testing.T) {
	img := testProgram()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// a cancelled context stops at the first check; the result is usable either way
	an, err := Analyze(ctx, img, AddinOptions(img, nil))
	if an == nil {
		t.Fatal("no analysis")
	}
	if err != nil && !an.Partial() {
		t.Error("error without Partial")
	}
}

// testROM is a tiny OS image: the trampoline loads the table base, the table has three entries.
func testROM() *Image {
	s := newAsm(ROMBase, 0x40000)
	s.w(SyscallTrampoline, 0xd201) // mov.l @(0x4,pc),r2: the literal at 0x80020078
	s.l(SyscallTrampoline+8, 0x80030000)
	s.l(0x80030000+4*0x1f44, 0x80001000) // sys_malloc
	s.l(0x80030000+4*0x1f42, 0x80001000) // sys_free: the same handler
	s.l(0x80030000+4*0x0003, 0x80001010) // unnamed
	s.l(0x80030000+4*0x0004, 0x12345678) // not a ROM address
	s.w(0x80001000, 0x000b, 0x0009)
	s.w(0x80001010, 0x000b, 0x0009)
	s.w(0x80000000, 0x000b, 0x0009)
	return NewImage(ROMBase, s.data)
}

func TestSyscallNames(t *testing.T) {
	names := SyscallNames()
	if len(names) != 273 {
		t.Errorf("%d names", len(names))
	}
	for i := 1; i < len(names); i++ {
		if names[i].Num <= names[i-1].Num {
			t.Errorf("not sorted at %d", i)
		}
	}
	if SyscallNameOf(0x1f44) != "sys_malloc" || SyscallNameOf(0x0003) != "" {
		t.Error("SyscallNameOf")
	}
	if n, ok := SyscallByName("Bdisp_PutDisp_DD"); !ok || n != 0x025f {
		t.Error("SyscallByName")
	}
}

func TestSyscallTable(t *testing.T) {
	img := testROM()
	sys := FindSyscalls(img)
	if sys == nil || sys.Base != 0x80030000 {
		t.Fatalf("table %+v", sys)
	}
	if sys.Name(0x80001000) != "sys_free" { // the lowest number sharing the handler
		t.Errorf("Name %q", sys.Name(0x80001000))
	}
	if n, ok := sys.Number(0x80001010); !ok || n != 3 {
		t.Errorf("Number %x", n)
	}
	if h, ok := sys.Handler(0x1f44); !ok || h != 0x80001000 {
		t.Errorf("Handler %x", h)
	}
	if sys.Count() != 2 || sys.Len() != 3 {
		t.Errorf("Count %d Len %d", sys.Count(), sys.Len())
	}
	if h := sys.Handlers(); h[3] != 0x80001010 || h[4] != 0 {
		t.Error("Handlers")
	}
	var none *Syscalls
	if none.Name(0) != "" || none.Count() != 0 {
		t.Error("nil Syscalls")
	}
	if FindSyscalls(NewImage(ROMBase, make([]byte, 0x30000))) != nil {
		t.Error("table found in zeros")
	}

	an, err := Analyze(context.Background(), img, OSOptions(img, sys))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := an.FuncAt(0x80001010); !ok {
		t.Error("handler not a function")
	}
	if an.FuncName(0x80001000) != "sys_free" || an.FuncName(0x80001010) != "syscall_0003" {
		t.Errorf("names %q %q", an.FuncName(0x80001000), an.FuncName(0x80001010))
	}

	// an add-in stub calling sys_malloc through the trampoline
	s := newAsm(AddinCode, 0x10)
	s.w(AddinCode, 0xd201, 0xd002, 0x422b, 0x0009) // mov.l L1,r2; mov.l L2,r0; jmp @r2; nop
	s.l(AddinCode+8, SyscallTrampoline)
	s.l(AddinCode+12, 0x1f44)
	addin := NewImage(AddinCode, s.data)
	jmp, _ := addin.Decode(AddinCode + 4)
	r, ok := ResolveIndirect(addin.Word, sys, &jmp)
	if !ok || r.Name != "sys_malloc" || r.Syscall != 0x1f44 || r.Target != 0x80001000 {
		t.Errorf("ResolveIndirect %+v %v", r, ok)
	}
	if c := Comment(addin.Word, sys, &jmp); c != "-> sys_malloc" {
		t.Errorf("Comment %q", c)
	}
	ld, _ := addin.Decode(AddinCode)
	if c := Comment(addin.Word, sys, &ld); c != "=0x80020070" {
		t.Errorf("Comment %q", c)
	}
	aan, _ := Analyze(context.Background(), addin, AddinOptions(addin, sys))
	if aan.FuncName(AddinCode) != "start" {
		t.Errorf("entry name %q", aan.FuncName(AddinCode))
	}
	if n := (&Analysis{img: addin, opt: Options{Syscalls: sys}}).FuncName(AddinCode); n != "sys_malloc" {
		t.Errorf("stub name %q", n)
	}
}

// ---- the OS image (skipped without a dump) ----

const analysisGolden = "testdata/analysis.golden"

func osAnalysis(t testing.TB, flash []byte) (*Analysis, time.Duration) {
	img := NewImage(ROMBase, flash)
	t0 := time.Now()
	an, err := Analyze(context.Background(), img, OSOptions(img, FindSyscalls(img)))
	if err != nil {
		t.Fatal(err)
	}
	return an, time.Since(t0)
}

// TestAnalyzeOS: the OS's vectors, a few known functions, and a golden of the function table
// and reference counts (hashes and counts only) per known image.
func TestAnalyzeOS(t *testing.T) {
	old, _ := os.ReadFile(analysisGolden)
	known := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(old)), "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			known[f[0]] = l
		}
	}
	for _, d := range osDumps(t) {
		an, dt := osAnalysis(t, d.data)
		t0 := time.Now()
		an.BuildXrefs()
		dx := time.Since(t0)
		sys := an.Syscalls()
		if sys == nil || sys.Base != 0x80687CD0 || sys.Len() != 8040 {
			t.Errorf("%s: syscall table %+v", d.name, sys)
		}
		if v := an.VBR(); !containsU32(v, 0x80020F00) {
			t.Errorf("%s: VBR %x", d.name, v)
		}
		for _, a := range []uint32{0x80020F00 + 0x100, 0x80020F00 + 0x400, 0x80020F00 + 0x600} {
			if _, ok := an.FuncAt(a); !ok {
				t.Errorf("%s: vector %08x is no function", d.name, a)
			}
		}
		h, _ := sys.Handler(0x1f44)
		if an.FuncName(h) != "sys_malloc" {
			t.Errorf("%s: handler %08x named %q", d.name, h, an.FuncName(h))
		}
		hs := sha256.Sum256(funcTable(an))
		id := imageID(d.data)
		line := fmt.Sprintf("%s funcs=%d table=%s xrefs=%d", id, an.NumFuncs(), hex.EncodeToString(hs[:8]), an.NumXrefs())
		t.Logf("%s: %s; functions %v, xref index %v", d.name, line, dt.Round(time.Millisecond), dx.Round(time.Millisecond))
		if *update {
			known[id] = line
			continue
		}
		if w, ok := known[id]; ok && w != line {
			t.Errorf("%s:\n got %s\nwant %s", d.name, line, w)
		}
	}
	if *update {
		var ls []string
		for _, l := range known {
			ls = append(ls, l)
		}
		sort.Strings(ls)
		os.WriteFile(analysisGolden, []byte(strings.Join(ls, "\n")+"\n"), 0o644)
	}
}

func containsU32(x []uint32, v uint32) bool {
	for _, y := range x {
		if y == v {
			return true
		}
	}
	return false
}

// ---- benchmarks ----

func BenchmarkAnalyzeOS(b *testing.B) {
	d := osDumps(b)[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		osAnalysis(b, d.data)
	}
}

func BenchmarkXrefIndexOS(b *testing.B) {
	an, _ := osAnalysis(b, osDumps(b)[0].data)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		an.buildXrefs()
	}
}

// BenchmarkAnalyzeFlash32: the whole 32 MB flash as one region (OS and storage memory).
func BenchmarkAnalyzeFlash32(b *testing.B) {
	var flash []byte
	for _, d := range osDumps(b) {
		if len(d.data) == 32<<20 {
			flash = d.data
		}
	}
	if flash == nil {
		b.Skip("no 32 MB dump")
	}
	img := NewImage(ROMBase, flash)
	opt := OSOptions(img, FindSyscalls(img))
	opt.End = img.End()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		an, _ := Analyze(context.Background(), img, opt)
		an.BuildXrefs()
		if i == 0 {
			b.ReportMetric(float64(an.NumFuncs()), "funcs")
		}
	}
}

func BenchmarkAnalyzeAddin(b *testing.B) {
	d := addinFiles(b)[0]
	img, err := G3AImage(d.data)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		an, _ := Analyze(context.Background(), img, AddinOptions(img, nil))
		an.BuildXrefs()
		if i == 0 {
			b.ReportMetric(float64(an.NumFuncs()), "funcs")
			b.ReportMetric(float64(len(d.data))/1e6, "MB")
		}
	}
}
