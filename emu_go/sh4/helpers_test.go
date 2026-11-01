package sh4

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the goldens in testdata/")

// The synthetic memory of the word table (the same function as dasmref.c): a hash of the
// address, with some words unreadable so both literal outcomes are covered.
func syn8(a uint32) uint8 { return uint8((a * 0x9E3779B1) >> 24) }
func synOK(a uint32) bool { return (a>>1)%7 != 3 }
func synRead(a uint32) (uint16, bool) {
	if !synOK(a) {
		return 0, false
	}
	return uint16(syn8(a))<<8 | uint16(syn8(a+1)), true
}

// wordTablePC is where the word table decodes every word.
const wordTablePC = 0x8c001000

// cKind maps a Kind to DASM's enum sh4_kind (rte is a return there; bra and bt/bf one kind;
// an illegal word has kind NONE).
func cKind(k Kind) int {
	switch k {
	case KindBranch, KindCondBranch:
		return 1
	case KindCall:
		return 2
	case KindJumpReg:
		return 3
	case KindCallReg:
		return 4
	case KindReturn, KindRTE:
		return 5
	case KindLiteral:
		return 6
	case KindMova:
		return 7
	case KindTrapa:
		return 8
	}
	return 0
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// refLine formats in the way dasmref.c prints DASM's decode of the same instruction.
func refLine(in *Instr) string {
	return fmt.Sprintf("%08x %04x %d %d %d %d %08x %08x %d %d %08x|%s", in.Addr, in.Op, in.Size,
		cKind(in.Kind), b2i(in.Delayed), in.Reg, in.Target, in.LitAddr, in.LitSize, b2i(in.LitOK),
		in.LitValue, in.Text)
}

// checkKindConsistency: the fields the C struct does not have agree with the text.
func checkKindConsistency(t *testing.T, in *Instr) {
	t.Helper()
	illegal := len(in.Text) > 0 && in.Text[0] == '.'
	if illegal != (in.Kind == KindIllegal) {
		t.Errorf("%08x %04x %q: kind %v", in.Addr, in.Op, in.Text, in.Kind)
	}
	mn := in.Mnemonic()
	switch in.Kind {
	case KindBranch:
		if mn != "bra" || !in.HasTarget {
			t.Errorf("%q: kind %v", in.Text, in.Kind)
		}
	case KindCondBranch:
		if mn != "bt" && mn != "bf" && mn != "bt/s" && mn != "bf/s" || !in.HasTarget {
			t.Errorf("%q: kind %v", in.Text, in.Kind)
		}
	case KindRTE:
		if mn != "rte" {
			t.Errorf("%q: kind %v", in.Text, in.Kind)
		}
	case KindReturn:
		if mn != "rts" {
			t.Errorf("%q: kind %v", in.Text, in.Kind)
		}
	}
	if in.HasTarget != (in.Kind == KindBranch || in.Kind == KindCondBranch || in.Kind == KindCall) {
		t.Errorf("%q: HasTarget %v kind %v", in.Text, in.HasTarget, in.Kind)
	}
}

// wordTable is the Go decode of every 16-bit word, one refLine each.
func wordTable() []byte {
	var buf bytes.Buffer
	buf.Grow(65536 * 60)
	for op := 0; op < 0x10000; op++ {
		in := DecodeWord(uint16(op), wordTablePC, synRead)
		buf.WriteString(refLine(&in))
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// dspHashes is one FNV-1a 64 hash per first word 0xF800-0xFBFF over the texts of all 65536
// second words (dasmref.c dsp prints the same).
func dspHashes() []byte {
	var buf bytes.Buffer
	const pc = 0x8c001000
	var second uint16
	read := func(a uint32) (uint16, bool) {
		if a == pc+2 {
			return second, true
		}
		return synRead(a)
	}
	var b tbuf
	var in Instr
	for op := 0xF800; op < 0xFC00; op++ {
		h := uint64(0xcbf29ce484222325)
		for g := 0; g < 0x10000; g++ {
			second = uint16(g)
			b.n = 0
			in = Instr{}
			decode(&in, &b, uint16(op), pc, read)
			for _, c := range b.b[:b.n] {
				h ^= uint64(c)
				h *= 0x100000001b3
			}
			h ^= '\n'
			h *= 0x100000001b3
		}
		fmt.Fprintf(&buf, "%04x %016x\n", op, h)
	}
	return buf.Bytes()
}

// osImage is a dump's OS part (the first 12 MB of the flash, mapped at 0x80000000), looked for
// in the git-ignored os/flash_dump/ of the repository; CG50_FLASH overrides. Skips without one.
type dumpFile struct {
	name string
	data []byte
}

func osDumps(t testing.TB) []dumpFile {
	t.Helper()
	var paths []string
	if p := os.Getenv("CG50_FLASH"); p != "" {
		paths = append(paths, p)
	} else {
		dir := filepath.Join("..", "..", "os", "flash_dump")
		for _, n := range []string{"flash_full.bin", "flash_full_32mb.bin"} {
			paths = append(paths, filepath.Join(dir, n))
		}
	}
	var out []dumpFile
	for _, p := range paths {
		d, err := os.ReadFile(p)
		if err != nil || len(d) < OSCodeSize {
			continue
		}
		out = append(out, dumpFile{filepath.Base(p), d})
	}
	if len(out) == 0 {
		t.Skip("no flash dump (os/flash_dump/flash_full*.bin or CG50_FLASH)")
	}
	return out
}

// imageID names an OS image by the SHA-256 of its first 12 MB (a hash, not firmware bytes).
func imageID(flash []byte) string {
	h := sha256.Sum256(flash[:OSCodeSize])
	return hex.EncodeToString(h[:8])
}

// addinFiles are .g3a files named in SH4_ADDINS (separated by the OS's list separator).
func addinFiles(t testing.TB) []dumpFile {
	t.Helper()
	var out []dumpFile
	for _, p := range filepath.SplitList(os.Getenv("SH4_ADDINS")) {
		if p == "" {
			continue
		}
		d, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("SH4_ADDINS: %v", err)
		}
		out = append(out, dumpFile{filepath.Base(p), d})
	}
	if len(out) == 0 {
		t.Skip("no add-ins (set SH4_ADDINS to .g3a paths)")
	}
	return out
}
