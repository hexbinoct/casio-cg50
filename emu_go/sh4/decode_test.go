package sh4

import "testing"

// memRead serves big-endian words from a small map; anything else is unreadable.
func memRead(words map[uint32]uint16) ReadFunc {
	return func(a uint32) (uint16, bool) {
		w, ok := words[a]
		return w, ok
	}
}

func TestDecodeExamples(t *testing.T) {
	const pc = 0x80001000
	mem := memRead(map[uint32]uint16{
		pc + 2:     0x0009,                     // after the instruction
		0x80001018: 0x1234, 0x8000101a: 0x5678, // a long literal for d105 at 0x80001000
		0x80001008: 0xfffe, // a word literal for 9102
	})
	tests := []struct {
		op   uint16
		text string
		kind Kind
		dly  bool
	}{
		{0x0009, "nop", KindNormal, false},
		{0x000b, "rts", KindReturn, true},
		{0x002b, "rte", KindRTE, true},
		{0x6103, "mov r0,r1", KindNormal, false},
		{0x2f86, "mov.l r8,@-r15", KindNormal, false},
		{0x4f22, "sts.l pr,@-r15", KindNormal, false},
		{0x4f26, "lds.l @r15+,pr", KindNormal, false},
		{0x412b, "jmp @r1", KindJumpReg, true},
		{0x430b, "jsr @r3", KindCallReg, true},
		{0x0423, "braf r4", KindJumpReg, true},
		{0x0503, "bsrf r5", KindCallReg, true},
		{0xa001, "bra 0x80001006", KindBranch, true},
		{0xafff, "bra 0x80001002", KindBranch, true},
		{0xb800, "bsr 0x80000004", KindCall, true},
		{0x89fe, "bt 0x80001000", KindCondBranch, false},
		{0x8d02, "bt/s 0x80001008", KindCondBranch, true},
		{0x8b00, "bf 0x80001004", KindCondBranch, false},
		{0xc305, "trapa #0x5", KindTrapa, false},
		{0xd105, "mov.l @(0x14,pc),r1", KindLiteral, false},
		{0x9102, "mov.w @(0x4,pc),r1", KindLiteral, false},
		{0xc701, "mova @(0x4,pc),r0", KindMova, false},
		{0xe1ff, "mov #-1,r1", KindNormal, false},
		{0x71fc, "add #-4,r1", KindNormal, false},
		{0xc8ff, "tst #0xff,r0", KindNormal, false},
		{0xcd10, "and.b #0x10,@(r0,gbr)", KindNormal, false},
		{0x0142, "stc spc,r1", KindNormal, false},
		{0x01c2, "stc r4_bank,r1", KindNormal, false},
		{0x416a, "lds r1,dsr", KindNormal, false},
		{0x4f6a, "lds r15,dsr", KindNormal, false},
		{0x00ab, "synco", KindNormal, false},
		{0x40a9, "movua.l @r0,r0", KindNormal, false},
		{0x8c10, "ldrs 0x80001024", KindNormal, false},
		{0xf000, "nopx nopy", KindNormal, false},
		{0xf488, "movs.w @r4+,x0", KindNormal, false},
		{0xf4c8, "movs.w @r4+,m0", KindNormal, false},
		{0xffff, ".word 0xffff", KindIllegal, false},
		{0x0000, ".word 0x0000", KindIllegal, false},
		{0xfc00, ".word 0xfc00", KindIllegal, false},
	}
	for _, tc := range tests {
		in := DecodeWord(tc.op, pc, mem)
		if in.Text != tc.text || in.Kind != tc.kind || in.Delayed != tc.dly {
			t.Errorf("%04x: got %q %v delay=%v, want %q %v delay=%v", tc.op, in.Text, in.Kind,
				in.Delayed, tc.text, tc.kind, tc.dly)
		}
		if in.Size != 2 {
			t.Errorf("%04x: size %d", tc.op, in.Size)
		}
	}

	in := DecodeWord(0xd105, pc, mem)
	if in.LitAddr != 0x80001018 || in.LitSize != 4 || !in.LitOK || in.LitValue != 0x12345678 || in.Reg != 1 {
		t.Errorf("mov.l literal: %+v", in)
	}
	in = DecodeWord(0x9102, pc, mem)
	if in.LitAddr != 0x80001008 || in.LitSize != 2 || !in.LitOK || in.LitValue != 0xfffe {
		t.Errorf("mov.w literal: %+v", in)
	}
	in = DecodeWord(0xd101, pc, mem) // literal at 0x80001008: only half readable
	if in.LitOK {
		t.Errorf("unreadable literal reported readable: %+v", in)
	}
	// mova and mov.l round the PC down to a multiple of 4
	if in := DecodeWord(0xc700, pc+2, nil); in.LitAddr != pc+4 {
		t.Errorf("mova at pc+2: %08x", in.LitAddr)
	}
	in = DecodeWord(0x8902, pc, nil)
	if !in.HasTarget || in.Target != pc+8 {
		t.Errorf("bt target %+v", in)
	}
	if in.Mnemonic() != "bt" || in.Operands() != "0x80001008" {
		t.Errorf("split %q / %q", in.Mnemonic(), in.Operands())
	}
	if in := DecodeWord(0x0009, pc, nil); in.Operands() != "" || in.Mnemonic() != "nop" {
		t.Errorf("nop split")
	}
}

func TestDecodeDSP32(t *testing.T) {
	const pc = 0x8c000000
	tests := []struct {
		w1, w2 uint16
		text   string
	}{
		{0xf800, 0xb107, "padd x0,y0,a0"},    // no transfer: the operation alone
		{0xf800, 0xb100, ".long 0xf800b100"}, // Dz 0 is reserved
		{0xf808, 0xb107, "padd x0,y0,a0  movx.w @r4+,x0"},
		{0xf800, 0x0000, ".long 0xf8000000"},
		{0xf800, 0x8d07, "pclr a0"},
		{0xf800, 0x8e07, "dct pclr a0"},
		{0xf800, 0x8f07, "dcf pclr a0"},
	}
	for _, tc := range tests {
		read := memRead(map[uint32]uint16{pc: tc.w1, pc + 2: tc.w2})
		in, ok := Decode(pc, read)
		if !ok || in.Text != tc.text || in.Size != 4 || in.Op2 != tc.w2 {
			t.Errorf("%04x %04x: %q size %d op2 %04x, want %q", tc.w1, tc.w2, in.Text, in.Size, in.Op2, tc.text)
		}
	}
	// without the second word: one .word, but still 4 bytes long (as DASM)
	in := DecodeWord(0xf800, pc, nil)
	if in.Text != ".word 0xf800" || in.Size != 4 || in.Kind != KindIllegal {
		t.Errorf("unreadable second word: %+v", in)
	}
	if InsnLen(0xf800) != 4 || InsnLen(0xfbff) != 4 || InsnLen(0xf7ff) != 2 || InsnLen(0xfc00) != 2 {
		t.Error("InsnLen")
	}
}

func TestDecodeUnreadable(t *testing.T) {
	if _, ok := Decode(0x1234, memRead(nil)); ok {
		t.Error("unreadable word decoded")
	}
}

func TestImage(t *testing.T) {
	img := NewImage(0x1000, []byte{0x12, 0x34, 0x56, 0x78, 0x9a})
	if w, ok := img.Word(0x1001); !ok || w != 0x3456 {
		t.Errorf("Word %x %v", w, ok)
	}
	if _, ok := img.Word(0x1004); ok {
		t.Error("Word past the end")
	}
	if v, ok := img.Long(0x1000); !ok || v != 0x12345678 {
		t.Errorf("Long %x", v)
	}
	if _, ok := img.Long(0x1002); ok {
		t.Error("Long past the end")
	}
	if _, ok := img.Byte(0xfff); ok {
		t.Error("Byte before the start")
	}
	if b := img.Bytes(0x1003, 10); len(b) != 2 {
		t.Errorf("Bytes %x", b)
	}
	if img.End() != 0x1005 || !img.Contains(0x1004) || img.Contains(0x1005) {
		t.Error("End/Contains")
	}
	if _, err := G3AImage(make([]byte, 100)); err == nil {
		t.Error("G3AImage accepted a short file")
	}
	g := make([]byte, G3AHeaderLen+4)
	copy(g, g3aMagic)
	copy(g[G3AHeaderLen:], []byte{0x00, 0x0b, 0x00, 0x09})
	gi, err := G3AImage(g)
	if err != nil {
		t.Fatal(err)
	}
	if in, _ := gi.Decode(AddinCode); in.Text != "rts" {
		t.Errorf("g3a entry: %q", in.Text)
	}
}

func BenchmarkDecodeAllWords(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		op := uint16(i)
		_ = DecodeWord(op, wordTablePC, synRead)
	}
}

func BenchmarkDecodeOS(b *testing.B) {
	d := osDumps(b)[0]
	img := NewImage(ROMBase, d.data[:OSCodeSize])
	b.ReportAllocs()
	b.ResetTimer()
	a := uint32(ROMBase)
	for i := 0; i < b.N; i++ {
		img.Decode(a)
		a += 2
		if a >= ROMBase+OSCodeSize {
			a = ROMBase
		}
	}
}

// BenchmarkDecodeKindOS: the decode the analysis uses (no text string).
func BenchmarkDecodeKindOS(b *testing.B) {
	d := osDumps(b)[0]
	img := NewImage(ROMBase, d.data[:OSCodeSize])
	b.ResetTimer()
	a := uint32(ROMBase)
	for i := 0; i < b.N; i++ {
		w, _ := img.Word(a)
		kindOf(w, a, img.Word)
		a += 2
		if a >= ROMBase+OSCodeSize {
			a = ROMBase
		}
	}
}
