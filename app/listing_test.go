package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cg50emu"
	"cg50emu/sh4"
)

// model is the row map done the slow way: the list of rows, one entry each.
type modelRow struct {
	off   uint32
	label bool
}

func model(m *rowMap) []modelRow {
	var out []modelRow
	li, ii := 0, 0
	for off := uint32(0); off < m.size; {
		if li < len(m.labels) && m.labels[li] == off {
			out = append(out, modelRow{off, true})
			li++
		}
		out = append(out, modelRow{off, false})
		h := uint32(1)
		if ii < len(m.items) && m.items[ii].off == off {
			h = m.items[ii].halves
			ii++
		}
		off += 2 * h
	}
	return out
}

func randomMap(r *rand.Rand, size uint32) *rowMap {
	var labels []uint32
	var items []item
	for off := uint32(0); off < size; {
		if r.Intn(9) == 0 {
			labels = append(labels, off)
		}
		h := uint32(1)
		switch r.Intn(7) {
		case 0:
			h = 2
		case 1:
			h = uint32(minFill + r.Intn(40))
		}
		if off+2*h > size {
			h = 1
		}
		if h > 1 {
			items = append(items, item{off: off, halves: h, kind: itemLong})
		}
		off += 2 * h
	}
	return newRowMap(size, labels, items)
}

// TestRowMap checks the row arithmetic (count, the row of an offset, the unit of a row, the
// batches) against the model on random layouts.
func TestRowMap(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for round := 0; round < 40; round++ {
		size := uint32(2 + 2*r.Intn(3000))
		m := randomMap(r, size)
		rows := model(m)
		if m.count != len(rows) {
			t.Fatalf("round %d: count %d, model %d", round, m.count, len(rows))
		}
		for i, mr := range rows {
			off, label, ok := m.at(i)
			if !ok || off != mr.off || label != mr.label {
				t.Fatalf("round %d: row %d = %x %v, model %x %v", round, i, off, label, mr.off, mr.label)
			}
			if !mr.label {
				// every byte of the unit shows on this row
				_, h, _ := m.unit(mr.off)
				for b := mr.off; b < mr.off+2*h; b++ {
					if got := m.rowOf(b); got != i {
						t.Fatalf("round %d: rowOf(%x) = %d, want %d", round, b, got, i)
					}
				}
			}
		}
		if _, _, ok := m.at(len(rows)); ok {
			t.Fatal("a row past the end")
		}
		if m.rowOf(size) != -1 {
			t.Fatal("rowOf past the end")
		}
		// batches from every start agree with the model
		l := &listing{base: 0x1000, size: size, rows: m}
		for first := 0; first < len(rows); first += 1 + r.Intn(50) {
			n := 1 + r.Intn(300)
			us := l.units(first, n)
			want := min(n, len(rows)-first)
			if len(us) != want {
				t.Fatalf("round %d: batch %d+%d has %d units, want %d", round, first, n, len(us), want)
			}
			for k, u := range us {
				if mr := rows[first+k]; u.off != mr.off || u.label != mr.label {
					t.Fatalf("round %d: batch %d row %d = %x %v, model %x %v", round, first, k, u.off, u.label, mr.off, mr.label)
				}
			}
		}
	}
}

// a small program: start: mov.l @(8,pc),r1 ; jsr @r1 ; nop ; rts ; nop ; (pad) ; .long f ;
// then 60 bytes of 0xFF ; f: rts ; mov #1,r0
func testImage() *sh4.Image {
	code := []byte{0xd1, 0x02, 0x41, 0x0b, 0x00, 0x09, 0x00, 0x0b, 0x00, 0x09, 0x00, 0x09, 0x00, 0x30, 0x00, 0x4c}
	for i := 0; i < 60; i++ {
		code = append(code, 0xff)
	}
	code = append(code, 0x00, 0x0b, 0xe0, 0x01)
	return sh4.NewImage(sh4.AddinCode, code)
}

type testNames struct{ an *sh4.Analysis }

func (n testNames) funcName(a uint32) string {
	if _, ok := n.an.FuncAt(a); ok {
		return n.an.FuncName(a)
	}
	return ""
}
func (n testNames) symbol(a uint32) string { return "" }

func TestListingRows(t *testing.T) {
	img := testImage()
	an, err := sh4.Analyze(context.Background(), img, sh4.AddinOptions(img, nil))
	if err != nil {
		t.Fatal(err)
	}
	m := buildRowMap(an, img.Word, img.Base, uint32(len(img.Data)))
	l := &listing{id: "t", base: img.Base, size: uint32(len(img.Data)), rows: m, an: an, static: img}
	read := func(a uint32, dst []byte) int { return copy(dst, img.Bytes(a, len(dst))) }
	rows := l.Rows(0, 100, read, testNames{an})
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%s %08x %s %s %s", r.Kind, r.Addr, r.Text, r.Mnem, r.Ops))
	}
	want := []string{
		"label 00300000 start  ",
		"insn 00300000  mov.l @(0x8,pc),r1",
		"insn 00300002  jsr @r1",
		"insn 00300004  nop ",
		"insn 00300006  rts ",
		"insn 00300008  nop ",
		"data 0030000a  .word 0x0009", // padding before the pool
		"data 0030000c  .long 0x0030004c",
		"fill 00300010  .fill 0x3c bytes",
		"label 0030004c sub_0030004c  ",
		"insn 0030004c  rts ",
		"insn 0030004e  mov #1,r0",
	}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("rows\n got %q\nwant %q", got, want)
	}
	if rows[7].Target == nil || *rows[7].Target != 0x0030004c || rows[7].Comment != "sub_0030004c" {
		t.Errorf("the pool word: target %v comment %q", rows[7].Target, rows[7].Comment)
	}
	if rows[2].Comment != "; -> sub_0030004c" || rows[2].Target == nil {
		t.Errorf("jsr comment %q", rows[2].Comment)
	}
	if rows[9].Comment != "; 1 caller" {
		t.Errorf("label comment %q", rows[9].Comment)
	}
	for i, r := range rows {
		if !(r.Kind == "label") {
			if got := l.rowOf(r.Addr); got != i {
				t.Errorf("rowOf(%08x) = %d, want %d", r.Addr, got, i)
			}
		}
	}
	// a batch from the middle is the same rows
	mid := l.Rows(5, 4, read, testNames{an})
	for k := range mid {
		if mid[k].Addr != rows[5+k].Addr || mid[k].Kind != rows[5+k].Kind {
			t.Fatalf("batch from row 5: %+v", mid)
		}
	}
	// unreadable memory shows as such
	none := l.Rows(1, 1, func(uint32, []byte) int { return 0 }, testNames{an})
	if len(none) != 1 || none[0].Mnem != "mov.l" { // read falls back to the analysed image
		t.Errorf("fallback: %+v", none)
	}
	lin := &listing{base: 0x8C000000, size: 64, rows: linearMap(64)}
	r0 := lin.Rows(0, 2, func(uint32, []byte) int { return 0 }, testNames{an})
	if len(r0) != 2 || r0[0].Mnem != "??" || r0[0].Comment != "unreadable" {
		t.Errorf("unreadable: %+v", r0)
	}
}

func TestFuncsCache(t *testing.T) {
	fs := []sh4.Func{{Addr: 0x80000000, Size: 0x40, Pool: 0x48}, {Addr: 0x80001000, Size: 0xffff, Pool: 0xffff}}
	b := encodeFuncs("abc", fs)
	got, err := decodeFuncs(b, "abc")
	if err != nil || fmt.Sprint(got) != fmt.Sprint(fs) {
		t.Fatalf("round trip: %v %v", got, err)
	}
	if _, err := decodeFuncs(b, "other"); err == nil {
		t.Error("another dump's cache was taken")
	}
	if _, err := decodeFuncs(b[:len(b)-1], "abc"); err == nil {
		t.Error("a short cache was taken")
	}
	bad := append([]byte(nil), b...)
	bad[len(funcsMagic)+1] = '9' // another version
	if _, err := decodeFuncs(bad, "abc"); err == nil {
		t.Error("another version was taken")
	}
}

// TestAnalysisCacheOS: with a real dump, the cached analysis answers as a fresh one, and the
// row map of the whole flash agrees with itself (rowOf and at) at spread places.
func TestAnalysisCacheOS(t *testing.T) {
	path := os.Getenv("CG50_FLASH")
	if path == "" {
		path = filepath.Join("..", "os", "flash_dump", "flash_full_32mb.bin")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no flash dump (CG50_FLASH)")
	}
	img := sh4.NewImage(sh4.ROMBase, data)
	sys := sh4.FindSyscalls(img)
	opt := sh4.OSOptions(img, sys)
	t0 := time.Now()
	an, err := sh4.Analyze(context.Background(), img, opt)
	if err != nil {
		t.Fatal(err)
	}
	tA := time.Since(t0)
	blob := encodeFuncs("h", an.Funcs())
	t0 = time.Now()
	fs, err := decodeFuncs(blob, "h")
	if err != nil {
		t.Fatal(err)
	}
	re := sh4.Restore(img, opt, fs)
	tR := time.Since(t0)
	t0 = time.Now()
	m := buildRowMap(re, img.Word, sh4.ROMBase, uint32(len(data)))
	tM := time.Since(t0)
	t.Logf("analysis %v, from the cache (%d KB) %v; row map %v: %d rows, %d labels, %d items",
		tA, len(blob)>>10, tR, tM, m.count, len(m.labels), len(m.items))
	if re.NumFuncs() != an.NumFuncs() {
		t.Fatalf("%d functions from the cache, %d analysed", re.NumFuncs(), an.NumFuncs())
	}
	for a := uint32(sh4.ROMBase); a < sh4.ROMBase+sh4.OSCodeSize; a += 0x1236 {
		if re.FuncName(a) != an.FuncName(a) || re.Classify(a) != an.Classify(a) {
			t.Fatalf("%08x differs", a)
		}
	}
	for r := 0; r < m.count; r += 9973 {
		off, label, ok := m.at(r)
		if !ok {
			t.Fatalf("row %d missing", r)
		}
		want := r
		if label {
			want++
		}
		if got := m.rowOf(off); got != want {
			t.Fatalf("row %d: offset %x, rowOf %d", r, off, got)
		}
	}
	l := &listing{base: sh4.ROMBase, size: uint32(len(data)), rows: m, an: re, static: img, sys: sys}
	read := func(a uint32, dst []byte) int { return copy(dst, img.Bytes(a, len(dst))) }
	t0 = time.Now()
	n := 0
	for r := 0; r < m.count; r += m.count / 50 {
		n += len(l.Rows(r, 256, read, testNames{re}))
	}
	t.Logf("%d rows in 50 batches of 256: %v", n, time.Since(t0))
	h, _ := sys.Handler(0x0EAB)
	rows := l.Rows(l.rowOf(h)-1, 2, read, testNames{re})
	if rows[0].Kind != "label" || rows[0].Text != "GetKey" {
		t.Errorf("GetKey's label: %+v", rows[0])
	}
}

func TestCond(t *testing.T) {
	var r = regsWith(map[string]uint32{"r4": 0x10, "r5": 'A', "pr": 0x80001234, "sr": 0x40000001})
	for _, tc := range []struct {
		src  string
		want bool
	}{
		{"r4 == 0x10", true}, {"r4 != 16", false}, {"r5 == 'A'", true},
		{"pr >= 0x80000000 && r4 < 0x20", true}, {"r4 == 1 || t", true}, {"!t", false},
		{"(r4 & 0xF0) >> 4 == 1", true}, {"r4 + 1 == 0x11 && -1 == 0xFFFFFFFF", true},
		{"~r4 == 0xFFFFFFEF", true}, {"r4 << 1 | 1 == 0x21", true}, {"m", false},
	} {
		f, err := compileCond(tc.src)
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if got := f(&r); got != tc.want {
			t.Errorf("%q = %v, want %v", tc.src, got, tc.want)
		}
	}
	for _, bad := range []string{"r4 ==", "r16 == 1", "(r4", "8C001000 == pc", "r4 = 1"} {
		if _, err := compileCond(bad); err == nil {
			t.Errorf("%q compiled", bad)
		}
	}
	if f, err := compileCond("  "); f != nil || err != nil {
		t.Error("an empty condition is none")
	}
}

func regsWith(v map[string]uint32) (r cg50emu.Regs) {
	for i := 0; i < 16; i++ {
		r.R[i] = v[fmt.Sprintf("r%d", i)]
	}
	r.PR, r.SR, r.PC = v["pr"], v["sr"], v["pc"]
	return r
}
