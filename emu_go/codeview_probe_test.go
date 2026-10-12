//go:build probe

package cg50emu

// Probe (2026-10-11) for ~/main/casio/codeview_cg50 (a source viewer for the PRGM app):
// what the OS's own detokenizer returns and what main memory holds.
//   TestTokens   OpCodeToStr (syscall 0x128F) for every opcode: single bytes and the two-byte
//                prefixes 7F F7 F9 E5 E6 E7. Written to $TMPDIR/cg50_opcodes.txt as
//                "<opcode hex> <result bytes hex>".
//   TestMCSList  MCS_SearchDirectory (0x1514) on "system" (the programs) and the items there,
//                with the first bytes of each program (MCS_SearchDirectoryItem, 0x1519).
//   go -C emu_go test -tags probe -run 'TestTokens|TestMCSList' -count=1 -v .

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	sysOpCodeToStr         = 0x128F
	sysMCSSearchDirectory  = 0x1514
	sysMCSSearchDirItem    = 0x1519
	codeviewBudget         = 50_000_000
)

func cvScratch(t *testing.T, e *Emulator, size uint32) uint32 {
	p, err := e.syscall(sysMalloc, codeviewBudget, size)
	if err != nil || p == 0 {
		t.Fatalf("malloc: %v %#x", err, p)
	}
	for i := uint32(0); i < size; i++ {
		e.mem.W8(p+i, 0)
	}
	return p
}

func cvString(e *Emulator, a uint32, max int) []byte {
	var b []byte
	for i := 0; i < max; i++ {
		c := byte(e.mem.R8(a + uint32(i)))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return b
}

func TestTokens(t *testing.T) {
	e := installState(t)
	e.Step(20_000_000)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.osIdle() {
		t.Fatal("OS not idle")
	}
	buf := cvScratch(t, e, 256)
	var out strings.Builder
	call := func(op uint32) {
		for i := uint32(0); i < 64; i++ {
			e.mem.W8(buf+i, 0)
		}
		if _, err := e.syscall(sysOpCodeToStr, codeviewBudget, op, buf); err != nil {
			t.Fatalf("OpCodeToStr(%#x): %v", op, err)
		}
		fmt.Fprintf(&out, "%04x %x\n", op, cvString(e, buf, 64))
	}
	for op := uint32(0); op < 0x100; op++ {
		call(op)
	}
	for _, pre := range []uint32{0x7F, 0xF7, 0xF9, 0xE5, 0xE6, 0xE7} {
		for lo := uint32(0); lo < 0x100; lo++ {
			call(pre<<8 | lo)
		}
	}
	path := os.TempDir() + "/cg50_opcodes.txt"
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}

func TestMCSList(t *testing.T) {
	e := installState(t)
	e.Step(20_000_000)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.osIdle() {
		t.Fatal("OS not idle")
	}
	s := cvScratch(t, e, 64)
	for i, c := range []byte("system") {
		e.mem.W8(s+uint32(i), uint32(c))
	}
	pdir, dirno := s+16, s+20
	r, err := e.syscall(sysMCSSearchDirectory, codeviewBudget, s, pdir, dirno)
	if err != nil {
		t.Fatal(err)
	}
	dir := e.mem.R32(pdir)
	t.Logf("MCS_SearchDirectory(\"system\") = %#x, pdir %#x, dirno %d", r, dir, e.mem.R8(dirno))
	if r != 0 || dir == 0 {
		return
	}
	// TMainMemoryDirectoryEntry: name[8], addr, count (u16), flags[2]
	items, count := e.mem.R32(dir+8), e.mem.R16(dir+12)
	t.Logf("dir name %q, items at %#x, count %d", cvString(e, dir, 8), items, count)
	for i := uint32(0); i < count && i < 64; i++ {
		it := items + 20*i // TDirectoryItem: name[8], offset, length, flags[4]
		name := cvString(e, it, 8)
		length := e.mem.R32(it + 12)
		// data through MCS_SearchDirectoryItem(dir, item, &flags, &itemptr, &data, &len)
		nm := s + 24
		for k := uint32(0); k < 9; k++ {
			e.mem.W8(nm+k, 0)
		}
		for k, c := range name {
			e.mem.W8(nm+uint32(k), uint32(c))
		}
		out := cvScratch(t, e, 32)
		// arguments 5 and 6 go on the stack: callOS runs the call at (r15 - 0x100) & ~3
		sp := (e.cpu.r[15] - 0x100) &^ 3
		e.mem.W32(sp, out+8)
		e.mem.W32(sp+4, out+12)
		r, err := e.syscall(sysMCSSearchDirItem, codeviewBudget, s, nm, out, out+4)
		if err != nil {
			t.Fatal(err)
		}
		data, dlen := e.mem.R32(out+8), e.mem.R32(out+12)
		var head []string
		for k := uint32(0); k < 48 && data != 0; k++ {
			head = append(head, fmt.Sprintf("%02x", e.mem.R8(data+k)))
		}
		t.Logf("  %-8q len %5d  search r=%#x data %#x len %d  %s", name, length, r, data, dlen, strings.Join(head, ""))
		if data != 0 {
			raw := make([]byte, dlen)
			for k := range raw {
				raw[k] = byte(e.mem.R8(data + uint32(k)))
			}
			os.WriteFile(fmt.Sprintf("%s/cg50_prog_%s.bin", os.TempDir(), name), raw, 0o644)
		}
	}
}
