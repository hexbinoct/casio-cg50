//go:build probe

package cg50emu

// Probe (2026-11-08): checks for Layer 2 of the syscall reference (re/syscall_docs.json).
// Each syscall documented as "tested" is called here through the OS, the way an add-in calls
// it, and its result checked. Runs on the 32 MB dump resumed at the MAIN MENU; file tests go
// into Run-Matrix first (like InstallFile).
//
//   go -C emu_go test -tags probe -run 'TestSyscallDocs$' -count=1 -v .

import (
	"fmt"
	"testing"
)

type scProbe struct {
	t   *testing.T
	e   *Emulator
	buf uint32 // scratch area from sys_malloc
}

func (p *scProbe) call(n uint32, args ...uint32) int32 {
	p.t.Helper()
	r, err := p.e.syscall(n, 400_000_000, args...)
	if err != nil {
		p.t.Fatalf("syscall %#x: %v", n, err)
	}
	return int32(r)
}

func (p *scProbe) bytes(a uint32, b ...byte) uint32 {
	for i, v := range b {
		p.e.mem.W8(a+uint32(i), uint32(v))
	}
	return a
}
func (p *scProbe) str(a uint32, s string) uint32 { return p.bytes(a, append([]byte(s), 0)...) }
func (p *scProbe) wstr(a uint32, s string) uint32 {
	for i, ch := range s + "\x00" {
		p.e.mem.W16(a+uint32(2*i), uint32(ch))
	}
	return a
}
func (p *scProbe) rwstr(a uint32, n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = uint16(p.e.mem.R16(a + uint32(2*i)))
	}
	return out
}
func (p *scProbe) rbytes(a uint32, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(p.e.mem.R8(a + uint32(i)))
	}
	return out
}

func (p *scProbe) want(what string, got, want int32) {
	p.t.Helper()
	if got != want {
		p.t.Errorf("%s = %d (%#x), want %d", what, got, uint32(got), want)
	} else {
		p.t.Logf("ok  %s = %d", what, got)
	}
}

func TestSyscallDocs(t *testing.T) {
	e := installState(t)
	e.mu.Lock()
	defer e.mu.Unlock()
	p := &scProbe{t: t, e: e}
	p.buf = uint32(p.call(sysMalloc, 0x2000))
	if p.buf == 0 {
		t.Fatal("malloc failed")
	}
	A, B, C := p.buf, p.buf+0x200, p.buf+0x400

	// ---- byte strings and memory
	p.str(A, "hello")
	p.want("strlen(hello) 0x1349", p.call(0x1349, A), 5)
	p.bytes(A, 1, 2, 3, 4)
	p.bytes(B, 1, 2, 9, 4)
	p.want("memcmp3(1234,1294,4) 0x115E", p.call(0x115E, A, B, 4), -1)
	p.want("memcmp3(1294,1234,4)", p.call(0x115E, B, A, 4), 1)
	p.want("memcmp3(..,2)", p.call(0x115E, A, B, 2), 0)
	p.want("sys_memcmp(1234,1294,4) 0x1DD1", p.call(0x1DD1, A, B, 4), 0)
	p.want("sys_memcmp(..,2)", p.call(0x1DD1, A, B, 2), 1)
	p.want("sys_memcmp(..,0)", p.call(0x1DD1, A, B, 0), 1)
	for _, c := range []uint32{0x7F, 0xE5, 0xE6, 0xE7, 0xF7, 0xF9, 0x41, 0xE8, 0xF8, 0x00} {
		want := int32(0)
		if c == 0x7F || c == 0xE5 || c == 0xE6 || c == 0xE7 || c == 0xF7 || c == 0xF9 {
			want = 1
		}
		p.want(fmt.Sprintf("MB_IsLead(%#x) 0x1161", c), p.call(0x1161, c), want)
	}
	cmp := func(a, b string) int32 { p.str(A, a); p.str(B, b); return p.call(0x1635, A, B) }
	p.want("strcmp_ignore_case(abc,ABC) 0x1635", cmp("abc", "ABC"), 0)
	p.want("strcmp_ignore_case(ABC,abc)", cmp("ABC", "abc"), 0)
	t.Logf("strcmp_ignore_case(a,B) = %d, (B,a) = %d, (b,A) = %d, (A,b) = %d, (a,b) = %d, (abc,abcd) = %d",
		cmp("a", "B"), cmp("B", "a"), cmp("b", "A"), cmp("A", "b"), cmp("a", "b"), cmp("abc", "abcd"))
	p.bytes(A, 9, 9, 9, 9, 9, 9)
	p.bytes(B, 1, 2, 3, 4, 5, 6)
	p.call(0x1DD0, A, B, 4)
	if got := p.rbytes(A, 6); string(got) != "\x01\x02\x03\x04\x09\x09" {
		t.Errorf("0x1DD0 copy: %v", got)
	} else {
		t.Logf("ok  0x1DD0 copies n bytes")
	}

	// ---- 16-bit (file name) strings
	p.want("0x1DCD tolower('Q')", p.call(0x1DCD, 'Q'), 'q')
	p.want("0x1DCD tolower('q')", p.call(0x1DCD, 'q'), 'q')
	p.want("0x1DD8 tolower('A')", p.call(0x1DD8, 'A'), 'a')
	p.want("0x1DD9 toupper('z')", p.call(0x1DD9, 'z'), 'Z')
	p.want("0x1DD9 toupper('5')", p.call(0x1DD9, '5'), '5')
	p.wstr(A, "abc.TXT")
	p.want("0x1DD2 wcslen", p.call(0x1DD2, A), 7)
	p.e.mem.W16(A+6, 0xFFFF)
	p.want("0x1DD2 wcslen stops at 0xFFFF", p.call(0x1DD2, A), 3)
	p.wstr(A, "abc.TXT")
	p.wstr(B, "abc.TXT")
	p.want("0x1DD3 equal", p.call(0x1DD3, A, B), 1)
	p.wstr(B, "abc.TXt")
	p.want("0x1DD3 differ", p.call(0x1DD3, A, B), 0)
	p.wstr(B, "abc")
	p.want("0x1DD3 prefix", p.call(0x1DD3, A, B), 0)
	for i := uint32(0); i < 16; i++ {
		p.e.mem.W16(C+2*i, 0x7777)
	}
	p.call(0x1DD4, C, A)
	t.Logf("0x1DD4 wcscpy -> %x", p.rwstr(C, 9))
	for i := uint32(0); i < 16; i++ {
		p.e.mem.W16(C+2*i, 0x7777)
	}
	p.call(0x1DD5, C, B, 6)
	t.Logf("0x1DD5 (dst, \"abc\", 6) -> %x", p.rwstr(C, 8))
	for i := uint32(0); i < 16; i++ {
		p.e.mem.W16(C+2*i, 0x7777)
	}
	p.call(0x1DD5, C, A, 3)
	t.Logf("0x1DD5 (dst, \"abc.TXT\", 3) -> %x", p.rwstr(C, 8))
	p.wstr(C, "ab")
	p.e.mem.W16(C+6, 0x7777)
	p.call(0x1DDD, C, ' ', 5)
	t.Logf("0x1DDD (\"ab\", ' ', 5) -> %x", p.rwstr(C, 7))
	p.wstr(C, "aB.c1")
	p.call(0x1DDE, C, 1)
	t.Logf("0x1DDE (\"aB.c1\", 1) -> %q", string(u16s(p.rwstr(C, 5))))
	p.call(0x1DDE, C, 2)
	t.Logf("0x1DDE (.., 2) -> %q", string(u16s(p.rwstr(C, 5))))
	p.wstr(A, `\\fls0\a\b.txt`)
	p.want("0x1DDF first '\\' (offset in chars)", (p.call(0x1DDF, A, '\\')-int32(A))/2, 0)
	p.want("0x1DDF first '.'", (p.call(0x1DDF, A, '.')-int32(A))/2, 10)
	t.Logf("0x1DDF missing 'z' -> offset %d (len 14)", (p.call(0x1DDF, A, 'z')-int32(A))/2)
	p.want("0x1DE0 last '\\'", (p.call(0x1DE0, A, '\\')-int32(A))/2, 8)
	p.want("0x1DE0 missing 'z'", p.call(0x1DE0, A, 'z'), 0)
	p.want("0x1DE0 only at [0]", (p.call(0x1DE0, p.wstr(B, "xab"), 'x')-int32(B))/2, 0)
	for _, c := range []struct {
		s    string
		want int32
	}{{`\\fls0\x.txt`, 1}, {`\\crd0\x.txt`, 2}, {`fls0\x`, 1}, {`\\FLS0\x`, -1}, {`\\dev0\x`, -1}, {`\\f`, -1}, {`\\fls0`, 1}} {
		p.want("0x1D9F drive of "+c.s, p.call(0x1D9F, p.wstr(A, c.s)), c.want)
	}

	// ---- RTC records (8 bytes BCD: year hi, year lo, month, day, hour, minute, second, 0)
	p.call(0x11E1, A)
	t.Logf("0x11E1 default record -> % x", p.rbytes(A, 8))
	p.bytes(A, 0x20, 0x26, 0x11, 0x08, 0x12, 0x30, 0x00, 0)
	p.bytes(B, 0x20, 0x26, 0x11, 0x08, 0x12, 0x30, 0x01, 0)
	p.want("0x11D9 earlier", p.call(0x11D9, A, B), 1)
	p.want("0x11D9 later", p.call(0x11D9, B, A), 4)
	p.want("0x11D9 equal", p.call(0x11D9, A, A), 2)
	dim := func(y, m byte) int32 {
		p.bytes(C, 0x20, y, m)
		p.call(0x11E3, C, C+1, C+2, C+4)
		return int32(p.e.mem.R8(C + 4))
	}
	p.want("0x11E3 days Feb 2024", dim(0x24, 0x02), 0x29)
	p.want("0x11E3 days Feb 2026", dim(0x26, 0x02), 0x28)
	p.want("0x11E3 days Feb 2000", dim(0x00, 0x02), 0x29)
	p.want("0x11E3 days Apr", dim(0x26, 0x04), 0x30)
	p.want("0x11E3 days Dec", dim(0x26, 0x12), 0x31)
	p.bytes(C, 0x21, 0x00, 0x02)
	p.call(0x11E3, C, C+1, C+2, C+4)
	p.want("0x11E3 days Feb 2100", int32(p.e.mem.R8(C+4)), 0x28)
	valid := func(b ...byte) int32 { p.bytes(C, b...); return p.call(0x11E5, C) }
	p.want("0x11E5 2026-11-08 12:30:00", valid(0x20, 0x26, 0x11, 0x08, 0x12, 0x30, 0x00, 0), 1)
	p.want("0x11E5 Feb 29 2024", valid(0x20, 0x24, 0x02, 0x29, 0, 0, 0, 0), 1)
	p.want("0x11E5 Feb 29 2026", valid(0x20, 0x26, 0x02, 0x29, 0, 0, 0, 0), 0)
	p.want("0x11E5 year 1900", valid(0x19, 0x00, 0x01, 0x01, 0, 0, 0, 0), 0)
	p.want("0x11E5 year 1901", valid(0x19, 0x01, 0x01, 0x01, 0, 0, 0, 0), 1)
	p.want("0x11E5 month 13", valid(0x20, 0x26, 0x13, 0x01, 0, 0, 0, 0), 0)
	p.want("0x11E5 day 0", valid(0x20, 0x26, 0x01, 0x00, 0, 0, 0, 0), 0)
	p.want("0x11E5 hour 24", valid(0x20, 0x26, 0x01, 0x01, 0x24, 0, 0, 0), 0)
	p.want("0x11E5 minute 60", valid(0x20, 0x26, 0x01, 0x01, 0x23, 0x60, 0, 0), 0)
	p.want("0x11E5 not BCD", valid(0x20, 0x2A, 0x01, 0x01, 0, 0, 0, 0), 0)
	p.call(0x11E2, A)
	rec := p.rbytes(A, 8)
	t.Logf("0x11E2 now -> % x (valid %d)", rec, p.call(0x11E5, A))
	if y := e.mem.Read(0xA413FECE, 2); uint32(rec[0])<<8|uint32(rec[1]) != y {
		t.Errorf("0x11E2 year %02x%02x, RYRCNT %#x", rec[0], rec[1], y)
	}
	p.call(0x11E8, B)
	t.Logf("0x11E8 -> %#02x (RSECCNT now %#02x)", p.e.mem.R8(B), e.mem.Read(0xA413FEC2, 1))

	// ---- keyboard
	p.call(0x12A5, A, A+4)
	f0, n0 := p.e.mem.R32(A), p.e.mem.R32(A+4)
	t.Logf("0x12A5 repeat counts now first %d next %d", f0, n0)
	p.call(0x12A4, 7, 3)
	p.call(0x12A5, A, A+4)
	p.want("0x12A4/0x12A5 first", int32(p.e.mem.R32(A)), 7)
	p.want("0x12A4/0x12A5 next", int32(p.e.mem.R32(A+4)), 3)
	p.call(0x0FEA)
	p.call(0x12A5, A, A+4)
	t.Logf("after 0x0FEA: %d %d", p.e.mem.R32(A), p.e.mem.R32(A+4))
	p.call(0x0FEB)
	p.call(0x12A5, A, A+4)
	t.Logf("after 0x0FEB: %d %d", p.e.mem.R32(A), p.e.mem.R32(A+4))
	p.call(0x12A4, f0, n0)
	p.bytes(B, 0, 0)
	p.want("0x12A7 (0,0) ok", p.call(0x12A7, A, B), 1)
	p.want("0x12A7 (0,0) code", int32(p.e.mem.R16(A)), 0x0101)
	p.bytes(B, 2, 1)
	p.want("0x12A7 (2,1) ok", p.call(0x12A7, A, B), 1)
	p.want("0x12A7 (2,1) code", int32(p.e.mem.R16(A)), 0x0302)
	p.bytes(B, 1, 0)
	p.want("0x12A7 (1,0) no key", p.call(0x12A7, A, B), 0)
	p.want("0x12A8 0x0302 found", p.call(0x12A8, B, 0x0302), 1)
	t.Logf("0x12A8 0x0302 -> % x", p.rbytes(B, 2))
	p.want("0x12A8 0x0909 not found", p.call(0x12A8, B, 0x0909), 0)
	p.call(0x12C7)
	p.want("0x12C8 after ClearKeyBuffer", p.call(0x12C8), 0)
	p.call(0x12C6, 0xFFFFFFFF, 0xFFFFFFFF, 0x7534)
	p.call(0x12C6, 3, 4, 0)
	p.want("0x12C8 after two PutKeycode", p.call(0x12C8), 2)
	p.call(0x12C7)
	p.want("0x12C8 cleared", p.call(0x12C8), 0)

	// ---- frame colour index (0x02A4) and FrameColor (0x02A3)
	p.want("0x02A4 initial", int32(int8(p.call(0x02A4, 2, 0))), -3)
	p.want("0x02A4 set 3", int32(int8(p.call(0x02A4, 1, 3))), 3)
	p.want("0x02A4 read", int32(int8(p.call(0x02A4, 2, 0))), 3)
	fc := p.call(0x02A3, 1, 0x1234) & 0xFFFF
	t.Logf("FrameColor(1, 0x1234) with index 3 -> %#04x", fc)
	if fc == 0x1234 {
		t.Errorf("index 3 did not override the colour")
	}
	p.want("0x02A4 reset", int32(int8(p.call(0x02A4, 0, 0))), -3)
	p.want("FrameColor(1, 0x1234) after reset", p.call(0x02A3, 1, 0x1234)&0xFFFF, 0x1234)

	// ---- VRAM: 0x01A8 3x3 points, 0x0926 filled rectangle
	vram := uint32(p.call(0x01E6))
	px := func(x, y uint32) uint32 { return p.e.mem.R16(vram + 2*(y*384+x)) }
	p.call(0x0272, 1) // Bdisp_AllClr_VRAM
	p.call(0x01A8, 2, 5, 1)
	t.Logf("0x01A8(2,5,1): (6,15)=%#x (8,17)=%#x (9,15)=%#x (6,39)=%#x (8,41)=%#x (9,39)=%#x (6,42)=%#x",
		px(6, 15), px(8, 17), px(9, 15), px(6, 39), px(8, 41), px(9, 39), px(6, 42))
	p.call(0x01A8, 2, 5, 0)
	t.Logf("0x01A8(2,5,0): (6,39)=%#x (8,41)=%#x", px(6, 39), px(8, 41))
	p.call(0x01A8, 127, 63, 1)
	t.Logf("0x01A8(127,63,1): (383,213)=%#x (381,213)=%#x", px(383, 213), px(381, 213))
	stack5 := func(v uint32) { p.e.mem.W32((p.e.cpu.r[15]-0x100)&^3, v) } // 5th argument (callOS's stack)
	stack5(0xF800)
	p.call(0x0926, 10, 20, 12, 22)
	t.Logf("0x0926(10,20,12,22,red): (10,20)=%#x (12,22)=%#x (13,22)=%#x (12,23)=%#x (10,44)=%#x (12,46)=%#x (12,47)=%#x (13,46)=%#x",
		px(10, 20), px(12, 22), px(13, 22), px(12, 23), px(10, 44), px(12, 46), px(12, 47), px(13, 46))
	stack5(2)
	p.call(0x0925, 30, 20, 31, 21)
	t.Logf("0x0925(30,20,31,21,2): (30,44)=%#x (31,45)=%#x (30,20)=%#x", px(30, 44), px(31, 45), px(30, 20))
	p.call(0x0272, 1)

	// ---- files: into Run-Matrix first (InstallFile does the same at the MAIN MENU)
	if e.atMainMenu() {
		e.tapAndSettle(6, 2, 0)
	}
	if !e.osIdle() {
		t.Fatal("not idle in Run-Matrix")
	}
	path, size := p.wstr(A, `\\fls0\L2PROBE.bin`), B
	p.call(sysBfileDelete, path)
	p.e.mem.W32(size, 64)
	p.want("create", p.call(sysBfileCreate, path, 1, size), 0)
	h := p.call(sysBfileOpen, path, 2, 0)
	for i := uint32(0); i < 64; i++ {
		p.e.mem.W8(C+i, i)
	}
	p.want("0x1DB0 write 16", p.call(0x1DB0, uint32(h), C, 16), 16)
	p.want("0x1DB0 write 16 more", p.call(0x1DB0, uint32(h), C+0x20, 16), 16)
	t.Logf("tell after two 0x1DB0 writes = %d", p.call(0x1DAB, uint32(h)))
	p.want("0x1DB2 on the handle", p.call(0x1DB2, uint32(h)), 0)
	r := p.call(0x1DB1, uint32(h), C+0x30, 8, 4)
	t.Logf("0x1DB1(h, buf, 8, 4) = %d, tell = %d", r, p.call(0x1DAB, uint32(h)))
	p.want("close", p.call(sysBfileClose, uint32(h)), 0)
	h = p.call(sysBfileOpen, path, 0, 0)
	p.want("size", p.call(0x1DA6, uint32(h)), 64)
	for i := uint32(0); i < 64; i++ {
		p.e.mem.W8(C+0x100+i, 0xEE)
	}
	t.Logf("read = %d", p.call(0x1DAC, uint32(h), C+0x100, 64, 0))
	t.Logf("contents % x", p.rbytes(C+0x100, 64))
	ba := C + 0x180
	p.e.mem.W32(ba, 0)
	r = p.call(0x1DAA, uint32(h), 0, ba)
	addr := p.e.mem.R32(ba)
	t.Logf("0x1DAA(h, 0, &a) = %d, a = %#08x, at a: % x", r, addr, p.rbytes(addr, 16))
	r = p.call(0x1DAA, uint32(h), 8, ba)
	t.Logf("0x1DAA(h, 8, &a) = %d, a = %#08x", r, p.e.mem.R32(ba))
	p.want("close", p.call(sysBfileClose, uint32(h)), 0)
	p.want("0x1DAA on a bad handle", p.call(0x1DAA, 0x07000000, 0, ba), -5)
	p.want("0x1DB2 on a crd0 handle", p.call(0x1DB2, 0x02000000), -5)
	p.want("0x1DA6 on a crd0 handle (no card)", p.call(0x1DA6, 0x02000000), -23)
	p.want("0x1D99", p.call(0x1D99), -23)
	p.call(sysBfileDelete, path)

	// eActivity work file
	t.Logf("0x1DBC exists before = %d", p.call(0x1DBC))
	p.want("0x1DBB create", p.call(0x1DBB), 0)
	p.want("0x1DBC exists after", p.call(0x1DBC), 1)
	p.want("0x1DBE write 8", p.call(0x1DBE, p.str(C, "12345678"), 8, 0), 8)
	p.want("0x1DBB create again", p.call(0x1DBB), 0)
	p.want("0x1DBD on the new file reads nothing", p.call(0x1DBD, C, 16, 0), 0)
	p.str(C, "eact-work-test!")
	p.want("0x1DBE write", p.call(0x1DBE, C, 16, 0), 16)
	p.str(C, "xxxxxxxxxxxxxxx")
	p.want("0x1DBD read", p.call(0x1DBD, C, 16, 0), 16)
	t.Logf("read back %q", p.rbytes(C, 15))
	p.want("0x1DBE append at 16", p.call(0x1DBE, C, 4, 16), 4)
	p.want("0x1DBD read at 16", p.call(0x1DBD, C+0x40, 4, 16), 4)
	t.Logf("0x1DBF(0) = %d, exists after = %d", p.call(0x1DBF, 0), p.call(0x1DBC))
	p.call(sysBfileDelete, p.wstr(A, `\\fls0\EACTWORK.tmp`))
	t.Logf("exists after delete = %d", p.call(0x1DBC))

	// optimize (0x1DC3) and format (0x1DA2) the storage memory
	free := func() uint32 { p.e.mem.W32(B, 0); p.e.mem.W32(B+4, 0); p.call(0x1DA5, p.wstr(A, `\\fls0`), B); return p.e.mem.R32(B)<<16 ^ p.e.mem.R32(B+4) }
	p.wstr(C, `\\fls0\*`)
	count := func() int {
		n := 0
		hh := C + 0x100
		info := C + 0x200
		r := p.call(0x1DB7, C, hh, C+0x180, info)
		for r == 0 {
			n++
			r = p.call(0x1DB9, p.e.mem.R32(hh), C+0x180, info)
		}
		p.call(0x1DBA, p.e.mem.R32(hh))
		return n
	}
	t.Logf("files %d, free words %#x %#x", count(), p.e.mem.R32(B), p.e.mem.R32(B+4))
	free()
	f1, f2 := p.e.mem.R32(B), p.e.mem.R32(B+4)
	p.want("0x1DC3 optimize fls0", p.call(0x1DC3, p.wstr(A, `\\fls0`)), 0)
	free()
	t.Logf("free before %#x %#x, after optimize %#x %#x; files %d", f1, f2, p.e.mem.R32(B), p.e.mem.R32(B+4), count())
	r = p.call(0x1DA2, p.wstr(A, `\\fls0`))
	free()
	t.Logf("0x1DA2 = %d; files after %d; free %#x %#x", r, count(), p.e.mem.R32(B), p.e.mem.R32(B+4))

	p.call(sysFree, p.buf)
}

func u16s(a []uint16) []rune {
	out := make([]rune, len(a))
	for i, v := range a {
		out[i] = rune(v)
	}
	return out
}
