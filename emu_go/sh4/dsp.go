package sh4

// The DSP instructions of the 0xF--- space: movs (16 bits), the double/single data transfers
// movx/movy (16 bits, 0xF000-0xF3FF) and the parallel-processing instructions (32 bits, first
// word 0xF800-0xFBFF: a transfer field in the first word, the operation in the second).

// dspReg: the 4-bit DSP register field (movs, the Dz of the parallel instructions); "" = reserved.
var dspRegs = [16]string{"", "", "", "", "", "a1", "", "a0",
	"x0", "x1", "y0", "y1", "m0", "a1g", "m1", "a0g"}

func dspReg(r uint32) string { return dspRegs[r&15] }

// dspEA prints "@rN", "@rN+", "@rN+r8" (index register ix: 8 for X memory, 9 for Y) for mode 1..3.
func (b *tbuf) dspEA(r, mode, ix int) {
	b.c('@')
	b.reg(r)
	if mode >= 2 {
		b.c('+')
	}
	if mode == 3 {
		b.c('r')
		b.dec(int32(ix))
	}
}

// xy prints one X or Y memory transfer: "<op> @ea,<reg>" (load) or "<op> <reg>,@ea" (store).
func (b *tbuf) xy(op string, store bool, r0, r1 byte, r, mode, ix int) {
	b.s(op)
	b.c(' ')
	if store {
		b.c(r0)
		b.c(r1)
		b.c(',')
	}
	b.dspEA(r, mode, ix)
	if !store {
		b.c(',')
		b.c(r0)
		b.c(r1)
	}
}

func pick(c bool, a, b byte) byte {
	if c {
		return a
	}
	return b
}

// dspDDT prints the data transfer field: bits 9..0 of a 0xF000-0xF3FF word, or of the first
// word of a parallel instruction (is32). Bits: 9 Ax (r4/r5), 8 Ay (r6/r7), 7 Dx, 6 Dy, 5 X store,
// 4 Y store, 3..2 X mode, 1..0 Y mode (0 = nopx / nopy, 1 @, 2 @+, 3 @+index). SH4AL-DSP adds
// single transfers that reuse the idle side's bits: movx.w/.l with Axy in r4 r5 r0 r1 and Dxy in
// x0 x1 y0 y1 (store: a0 a1 x0 x1), movy.w/.l with Ayx in r6 r7 r2 r3 and Dyx in y0 y1 x0 x1
// (store: a0 a1 y0 y1). Returns false for an invalid field.
func (b *tbuf) dspDDT(f uint32, is32 bool) bool {
	xm, ym, sel := int(f>>2)&3, int(f)&3, (f>>6)&3
	if xm == 0 && ym == 0 {
		if f != 0 {
			return false
		}
		if !is32 {
			b.s("nopx nopy")
		}
		return true
	}
	if xm != 0 && ym == 0 && f&0x150 != 0 { // SH4AL movx, no movy
		st := f&0x20 != 0
		r := int(f>>9) & 1
		if f&0x100 == 0 {
			r += 4
		}
		op := "movx.w"
		if f&0x10 != 0 {
			op = "movx.l"
		}
		r0 := pick(st, pick(sel&1 != 0, 'x', 'a'), pick(sel&1 != 0, 'y', 'x'))
		b.xy(op, st, r0, pick(sel&2 != 0, '1', '0'), r, xm, 8)
		return true
	}
	if ym != 0 && xm == 0 && f&0x2a0 != 0 { // SH4AL movy, no movx
		st := f&0x10 != 0
		r := int(f>>8) & 1
		if f&0x200 != 0 {
			r += 2
		} else {
			r += 6
		}
		op := "movy.w"
		if f&0x20 != 0 {
			op = "movy.l"
		}
		r0 := pick(st, pick(sel&2 != 0, 'y', 'a'), pick(sel&2 != 0, 'x', 'y'))
		b.xy(op, st, r0, pick(sel&1 != 0, '1', '0'), r, ym, 9)
		return true
	}
	if xm != 0 {
		st := f&0x20 != 0
		b.xy("movx.w", st, pick(st, 'a', 'x'), pick(f&0x80 != 0, '1', '0'), 4+int(f>>9)&1, xm, 8)
	}
	if ym != 0 {
		st := f&0x10 != 0
		if xm != 0 {
			b.s("  ")
		}
		b.xy("movy.w", st, pick(st, 'a', 'y'), pick(f&0x40 != 0, '1', '0'), 6+int(f>>8)&1, ym, 9)
	}
	return true
}

// ppi is one row of the parallel-processing operation table: key = bits 15..8 of the second word
// with the condition bits 9..8 at 00 (unconditional only) or 01 (dct / dcf allowed: 10 / 11).
// lo constrains bits 7..4: 1 = ..00, 2 = ..01, 3 = 00.., 4 = 01.. Operands: X = Sx (bits 7..6),
// Y = Sy (5..4), N = Dz (3..0), h / l = mach / macl. The first match wins (the SH4AL
// pabs/prnd/pswap forms before the older ones sharing a key).
type ppi struct {
	key, lo uint8
	name    string
	ops     string
}

var ppiTab = [...]ppi{
	{0xa0, 0, "psubc", "XYN"}, {0xb0, 0, "paddc", "XYN"}, {0x84, 0, "pcmp", "XY"},
	{0xa4, 0, "pwsb", "XYN"}, {0xb4, 0, "pwad", "XYN"}, {0x88, 0, "pabs", "XN"},
	{0xa8, 0, "pabs", "YN"}, {0x98, 0, "prnd", "XN"}, {0xb8, 0, "prnd", "YN"},
	{0x89, 2, "pabs", "XN"}, {0xa9, 4, "pabs", "YN"}, {0x99, 2, "prnd", "XN"},
	{0xb9, 4, "prnd", "YN"}, {0x81, 0, "pshl", "XYN"}, {0x91, 0, "psha", "XYN"},
	{0xa1, 0, "psub", "XYN"}, {0x85, 0, "psub", "YXN"}, {0xb1, 0, "padd", "XYN"},
	{0x95, 0, "pand", "XYN"}, {0xa5, 0, "pxor", "XYN"}, {0xb5, 0, "por", "XYN"},
	{0x89, 0, "pdec", "XN"}, {0xa9, 0, "pdec", "YN"}, {0x99, 1, "pinc", "XN"},
	{0xb9, 3, "pinc", "YN"}, {0x8d, 0, "pclr", "N"}, {0x9d, 1, "pdmsb", "XN"},
	{0xbd, 3, "pdmsb", "YN"}, {0xc9, 0, "pneg", "XN"}, {0xe9, 0, "pneg", "YN"},
	{0xd9, 0, "pcopy", "XN"}, {0xf9, 0, "pcopy", "YN"}, {0xcd, 0, "psts", "hN"},
	{0xdd, 0, "psts", "lN"}, {0xed, 0, "plds", "Nh"}, {0xfd, 0, "plds", "Nl"},
	{0x9d, 2, "pswap", "XN"}, {0xbd, 4, "pswap", "YN"},
}

var (
	dspSx = [4]string{"x0", "x1", "a0", "a1"}
	dspSy = [4]string{"y0", "y1", "m0", "m1"}
	dspDu = [4]string{"x0", "y0", "a0", "a1"}
	dspSe = [4]string{"x0", "x1", "y0", "a1"}
	dspSf = [4]string{"y0", "y1", "x0", "a1"}
	dspSg = [4]string{"m0", "m1", "a0", "a1"}
)

// dspPPI prints the parallel-processing word g. Returns false for an invalid one.
func (b *tbuf) dspPPI(g uint32) bool {
	if g&0xe800 == 0 { // pshl / psha #imm7,Dz
		imm := int32(g>>4) & 0x7f
		z := dspReg(g)
		if z == "" {
			return false
		}
		if g&0x1000 != 0 {
			b.s("psha #")
		} else {
			b.s("pshl #")
		}
		if imm&0x40 != 0 {
			imm -= 0x80
		}
		b.dec(imm)
		b.c(',')
		b.s(z)
		return true
	}
	if g&0xc000 == 0x4000 && g&0x3000 != 0x1000 { // pmuls, with padd/psub/pclr
		switch {
		case g&0x2000 != 0:
			if g&0x1000 != 0 {
				b.s("padd ")
			} else {
				b.s("psub ")
			}
			b.s(dspSx[(g>>6)&3])
			b.c(',')
			b.s(dspSy[(g>>4)&3])
			b.c(',')
			b.s(dspDu[g&3])
			b.s("  ")
		case g&0xf0 == 0x10:
			b.s("pclr ")
			b.s(dspDu[g&3])
			b.s("  ")
		case g&0xf3 != 0:
			return false
		}
		b.s("pmuls ")
		b.s(dspSe[(g>>10)&3])
		b.c(',')
		b.s(dspSf[(g>>8)&3])
		b.c(',')
		b.s(dspSg[(g>>2)&3])
		return true
	}
	key, lo := (g>>8)&0xff, (g>>4)&15
	cond := key & 3
	if cond >= 2 { // dct / dcf: as the 01 form
		key -= cond - 1
	}
	for i := range ppiTab {
		p := &ppiTab[i]
		if uint32(p.key) != key {
			continue
		}
		if (p.lo == 1 && lo&3 != 0) || (p.lo == 2 && lo&3 != 1) ||
			(p.lo == 3 && lo&12 != 0) || (p.lo == 4 && lo&12 != 4) {
			continue
		}
		z := dspReg(g)
		if z == "" {
			for j := 0; j < len(p.ops); j++ {
				if p.ops[j] == 'N' {
					return false
				}
			}
		}
		if cond >= 2 {
			if cond == 2 {
				b.s("dct ")
			} else {
				b.s("dcf ")
			}
		}
		b.s(p.name)
		b.c(' ')
		for j := 0; j < len(p.ops); j++ {
			if j > 0 {
				b.c(',')
			}
			switch p.ops[j] {
			case 'X':
				b.s(dspSx[(g>>6)&3])
			case 'Y':
				b.s(dspSy[(g>>4)&3])
			case 'N':
				b.s(z)
			case 'h':
				b.s("mach")
			case 'l':
				b.s("macl")
			}
		}
		return true
	}
	return false
}

var movsAr = [4]int{4, 5, 2, 3}

// decodeF decodes the 0xF--- space.
func decodeF(in *Instr, b *tbuf, x uint16, pc uint32, read ReadFunc, m, d4 int) bool {
	if x&0x0400 != 0 { // movs.w / movs.l: 0xF4xx-0xF7xx
		r := dspReg(uint32(m))
		mode, st, ar := (d4>>2)&3, d4&1 != 0, movsAr[(x>>8)&3]
		if x&0x0800 != 0 || r == "" { // 0xFC00-0xFFFF: none
			return false
		}
		if d4&2 != 0 {
			b.s("movs.l ")
		} else {
			b.s("movs.w ")
		}
		if st {
			b.s(r)
			b.c(',')
		}
		b.c('@')
		if mode == 0 {
			b.c('-')
		}
		b.reg(ar)
		if mode >= 2 {
			b.c('+')
		}
		if mode == 3 {
			b.s("r8")
		}
		if !st {
			b.c(',')
			b.s(r)
		}
		return true
	}
	if x&0x0800 != 0 { // 32-bit parallel instruction
		in.Size = 4
		if read == nil {
			return false
		}
		g16, ok := read(pc + 2)
		if !ok {
			return false
		}
		in.Op2 = g16
		g := uint32(g16)
		if b.dspPPI(g) {
			p := b.n
			if x&0x3ff != 0 {
				b.s("  ")
			}
			q := b.n
			if b.dspDDT(uint32(x)&0x3ff, true) {
				if b.n == q { // no transfer
					b.n = p
				}
				return true
			}
		}
		b.n = 0
		b.s(".long 0x")
		b.hex(uint32(x)<<16|g, 8)
		in.Kind = KindIllegal
		return true
	}
	return b.dspDDT(uint32(x)&0x3ff, false) // 0xF000-0xF3FF
}
