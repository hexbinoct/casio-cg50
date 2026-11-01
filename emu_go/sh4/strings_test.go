package sh4

import (
	"fmt"
	"testing"
	"time"
)

func TestFindStrings(t *testing.T) {
	var d []byte
	at := map[string]uint32{}
	put := func(name string, b []byte) {
		for len(d)%2 != 0 {
			d = append(d, 0xff)
		}
		at[name] = 0x1000 + uint32(len(d))
		d = append(d, b...)
		d = append(d, 0xff, 0xff)
	}
	put("hello", []byte("Hello, world\x00"))
	put("junk", []byte("@@#$%^&*\x00"))
	put("fill", []byte("UUUUUUUU\x00"))
	put("casio", []byte("\xe5\x41Abc def\x00"))
	put("short", []byte("ab\x00"))
	put("open", []byte("no terminator"))
	put("camel", []byte("GetVRAMAddress\x00"))
	put("noword", []byte("xY 1a zB 2c\x00"))
	put("utf16", []byte("\x00\\\x00f\x00l\x00s\x000\x00\\\x00A\x00.\x00g\x003\x00a\x00\x00"))
	img := NewImage(0x1000, d)

	got := FindStrings(img, 0, 0, StringOptions{})
	want := []String{
		{at["hello"], 12, EncASCII, "Hello, world", 12, true},
		{at["casio"], 9, EncCasio, `\xe5\x41Abc def`, 8, true},
		{at["open"], 13, EncASCII, "no terminator", 13, false},
		{at["camel"], 14, EncASCII, "GetVRAMAddress", 14, true},
		{at["utf16"], 22, EncUTF16, `\\fls0\\A.g3a`, 11, true},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("default\n got %v\nwant %v", got, want)
	}
	got = FindStrings(img, 0, 0, StringOptions{NeedNUL: true, Enc: EncASCII})
	// without EncCasio a lead byte ends a run: the rest of the Casio string is ASCII
	if len(got) != 3 || got[0].Text != "Hello, world" || got[1].Text != "AAbc def" || got[2].Text != "GetVRAMAddress" {
		t.Errorf("NUL, ASCII: %v", got)
	}
	got = FindStrings(img, 0, 0, StringOptions{Loose: true, MinLen: 2, Enc: EncASCII | EncCasio})
	if len(got) != 8 { // every byte run, the junk and the short one too
		t.Errorf("loose: %d %v", len(got), got)
	}
	got = FindStrings(img, at["casio"], at["casio"]+4, StringOptions{MinLen: 2, Loose: true})
	if len(got) != 1 || got[0].Text != `\xe5\x41Ab` {
		t.Errorf("range: %v", got)
	}
	if got = FindStrings(img, 0, 0, StringOptions{Max: 2}); len(got) != 2 {
		t.Errorf("Max: %d", len(got))
	}
}

// TestFindStringsOS: the OS's text, and the speed over the whole dump.
func TestFindStringsOS(t *testing.T) {
	for _, d := range osDumps(t) {
		img := NewImage(ROMBase, d.data)
		t0 := time.Now()
		all := FindStrings(img, 0, 0, StringOptions{MinLen: 6})
		dt := time.Since(t0)
		n := map[Encoding]int{}
		for _, s := range all {
			n[s.Enc]++
		}
		t.Logf("%s: %d MB in %v: %d strings %v", d.name, len(d.data)>>20, dt.Round(time.Millisecond), len(all), n)
		if n[EncASCII] < 1000 || n[EncUTF16] == 0 {
			t.Errorf("%s: too few strings %v", d.name, n)
		}
	}
}

func BenchmarkFindStrings(b *testing.B) {
	ds := osDumps(b)
	d := ds[len(ds)-1]
	img := NewImage(ROMBase, d.data)
	b.SetBytes(int64(len(d.data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FindStrings(img, 0, 0, StringOptions{MinLen: 6})
	}
}
