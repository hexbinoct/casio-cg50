package sh4

// Frozen goldens of the decoder, made by this package after it matched DASM's C decoder
// (dasm_compare_test.go) and checked here without C: `go test ./sh4 -run Golden -update`
// rewrites them. None contains firmware: the word table is every 16-bit value over synthetic
// memory, the DSP file and the OS file hold only hashes of our own listing text.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	wordsGolden = "testdata/words.golden.gz"
	dspGolden   = "testdata/dsp32.golden"
	osGolden    = "testdata/os_listing.golden"
)

func readGz(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeGz(t *testing.T, path string, data []byte) {
	t.Helper()
	var buf bytes.Buffer
	z, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	z.Write(data)
	z.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWordTableGolden: every 16-bit word, its text and all fields.
func TestWordTableGolden(t *testing.T) {
	got := wordTable()
	if *update {
		writeGz(t, wordsGolden, got)
		return
	}
	if n := diffLines(t, "words golden", readGz(t, wordsGolden), got); n != 0 {
		t.Fatalf("%d words differ from %s", n, wordsGolden)
	}
	for op := 0; op < 0x10000; op++ {
		in := DecodeWord(uint16(op), wordTablePC, synRead)
		checkKindConsistency(t, &in)
	}
}

// TestDSPGolden: all 2^26 32-bit DSP forms, one hash per first word.
func TestDSPGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	got := dspHashes()
	if *update {
		if err := os.WriteFile(dspGolden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(dspGolden)
	if err != nil {
		t.Fatal(err)
	}
	if n := diffLines(t, "dsp golden", want, got); n != 0 {
		t.Fatalf("%d DSP first words differ from %s", n, dspGolden)
	}
}

// osListingHashes: the listing of every even address of the OS part, as refLine text, hashed
// per MB ("<image id> <block> <sha256>").
func osListingHashes(id string, flash []byte) []string {
	img := NewImage(ROMBase, flash[:OSCodeSize])
	var out []string
	for blk := uint32(0); blk < OSCodeSize; blk += 1 << 20 {
		h := sha256.New()
		w := bufio.NewWriterSize(h, 1<<16)
		for a := ROMBase + blk; a < ROMBase+blk+1<<20; a += 2 {
			in, _ := img.Decode(a)
			w.WriteString(refLine(&in))
			w.WriteByte('\n')
		}
		w.Flush()
		out = append(out, fmt.Sprintf("%s %08x %s", id, ROMBase+blk, hex.EncodeToString(h.Sum(nil))))
	}
	return out
}

// TestOSListingGolden: the listing of the OS part of each dump found, against the hashes made
// when it matched DASM. A dump the golden doesn't know (another calculator's) is skipped; run
// TestDASMImage for it.
func TestOSListingGolden(t *testing.T) {
	dumps := osDumps(t)
	old, _ := os.ReadFile(osGolden)
	known := map[string][]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(old)), "\n") {
		if f := strings.Fields(l); len(f) == 3 {
			known[f[0]] = append(known[f[0]], l)
		}
	}
	for _, d := range dumps {
		id := imageID(d.data)
		got := osListingHashes(id, d.data)
		if *update {
			known[id] = got
			continue
		}
		want, ok := known[id]
		if !ok {
			t.Logf("%s: image %s not in %s, skipped", d.name, id, osGolden)
			continue
		}
		if strings.Join(want, "\n") != strings.Join(got, "\n") {
			for i := range got {
				if i >= len(want) || want[i] != got[i] {
					t.Errorf("%s: listing block %d differs", d.name, i)
				}
			}
		}
	}
	if *update {
		var ids []string
		for id := range known {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var all []string
		for _, id := range ids {
			all = append(all, known[id]...)
		}
		if err := os.WriteFile(filepath.FromSlash(osGolden), []byte(strings.Join(all, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
