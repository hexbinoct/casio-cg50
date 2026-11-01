// Package sh4 disassembles and analyses code for the SH4AL-DSP CPU of the fx-CG50 (SH7305):
// the SH-4A integer and system instructions, the SH-4A additions and the DSP set, including the
// 32-bit parallel-processing forms. The SH7305 has no FPU: the 0xF--- space holds the DSP data
// transfers and the parallel instructions, and the system-register slots that the SH-4 uses for
// fpul/fpscr name the DSP registers.
//
// It is a Go port of DASM, our on-device disassembler (github.com/hexbinoct/cg50-dasm, MIT):
// the decoder prints exactly what DASM prints, checked for every 16-bit word, every 32-bit DSP
// form and every instruction address of the OS image (see README.md). The package does not
// depend on the emulator; everything reads memory through a callback or an Image.
package sh4

// ReadFunc reads the big-endian 16-bit word at addr; ok is false when addr is not readable.
type ReadFunc func(addr uint32) (w uint16, ok bool)

// Kind classifies an instruction by its effect on the flow of control (and the PC-relative
// loads, which matter to listings and analysis).
type Kind uint8

const (
	KindNormal     Kind = iota // any other valid instruction
	KindIllegal                // not an instruction: printed as .word (or .long for a 32-bit DSP word pair)
	KindBranch                 // bra: Target
	KindCondBranch             // bt bf bt/s bf/s: Target
	KindCall                   // bsr: Target
	KindJumpReg                // jmp @Rn, braf Rn: Reg
	KindCallReg                // jsr @Rn, bsrf Rn: Reg
	KindReturn                 // rts
	KindRTE                    // rte
	KindTrapa                  // trapa #imm
	KindLiteral                // mov.w / mov.l @(disp,pc),Rn: LitAddr LitSize LitValue LitOK, Reg
	KindMova                   // mova @(disp,pc),r0: LitAddr
)

var kindNames = [...]string{"normal", "illegal", "branch", "cond-branch", "call", "jump-reg",
	"call-reg", "return", "rte", "trapa", "literal", "mova"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "kind?"
}

// Instr is one decoded instruction.
type Instr struct {
	Addr    uint32
	Op      uint16 // the first (or only) word
	Op2     uint16 // the second word of a 32-bit DSP instruction (Size 4), when it was readable
	Size    int    // 2, or 4 for a DSP parallel instruction (first word 0xF800-0xFBFF)
	Text    string // "mnemonic operands" exactly as DASM prints it, e.g. "mov.l @(0x10,pc),r1"
	Kind    Kind
	Delayed bool // the next instruction is this one's delay slot

	HasTarget bool   // Target is valid (bra bt bf bt/s bf/s bsr)
	Target    uint32 // the static branch or call target

	Reg uint8 // the register of jmp/jsr/braf/bsrf, the destination of a literal load (r0 for mova)

	LitAddr  uint32 // literal (mov.w/mov.l) or mova address
	LitSize  int    // 2 or 4 for KindLiteral
	LitValue uint32 // the literal, zero-extended (when LitOK)
	LitOK    bool   // the literal was readable
}

// Mnemonic is the first word of Text ("mov.l", "bt/s", ".word"; for a DSP parallel
// instruction the first operation, e.g. "padd" or "dct").
func (in *Instr) Mnemonic() string {
	for i := 0; i < len(in.Text); i++ {
		if in.Text[i] == ' ' {
			return in.Text[:i]
		}
	}
	return in.Text
}

// Operands is Text after the mnemonic ("" when there are none).
func (in *Instr) Operands() string {
	for i := 0; i < len(in.Text); i++ {
		if in.Text[i] == ' ' {
			return in.Text[i+1:]
		}
	}
	return ""
}

// End is the address after the instruction.
func (in *Instr) End() uint32 { return in.Addr + uint32(in.Size) }

// IsBranch reports whether the instruction changes the flow of control (branches, calls,
// jumps, returns, trapa).
func (in *Instr) IsBranch() bool { return in.Kind >= KindBranch && in.Kind <= KindTrapa }

func (in *Instr) String() string { return in.Text }

// InsnLen is the length in bytes of the instruction whose first word is op: 4 for the DSP
// parallel-processing instructions (0xF800-0xFBFF), else 2.
func InsnLen(op uint16) int {
	if op&0xFC00 == 0xF800 {
		return 4
	}
	return 2
}

// Decode decodes the instruction at addr. The first word, a 32-bit instruction's second word
// and PC-relative literals are read through read. An unreadable first word gives ok = false.
func Decode(addr uint32, read ReadFunc) (in Instr, ok bool) {
	op, ok := read(addr)
	if !ok {
		return Instr{Addr: addr}, false
	}
	return DecodeWord(op, addr, read), true
}

// DecodeWord decodes op as the instruction at pc; read (may be nil) supplies a 32-bit
// instruction's second word at pc+2 (without it the text is ".word 0xf8xx") and literals.
func DecodeWord(op uint16, pc uint32, read ReadFunc) Instr {
	var b tbuf
	var in Instr
	decode(&in, &b, op, pc, read)
	in.Text = string(b.b[:b.n])
	return in
}

// kindOf decodes without building the text string: the analysis only needs the kind.
func kindOf(op uint16, pc uint32, read ReadFunc) Kind {
	var b tbuf
	var in Instr
	decode(&in, &b, op, pc, read)
	return in.Kind
}

// ---- tiny formatter (the same layout as DASM's) ----

type tbuf struct {
	b [128]byte
	n int
}

func (b *tbuf) c(ch byte) {
	if b.n < len(b.b) {
		b.b[b.n] = ch
		b.n++
	}
}
func (b *tbuf) s(s string) {
	for i := 0; i < len(s); i++ {
		b.c(s[i])
	}
}

// hex prints v in lower-case hex, at least digits wide (0 = minimal width).
func (b *tbuf) hex(v uint32, digits int) {
	var t [8]byte
	n := 0
	for {
		t[n] = "0123456789abcdef"[v&15]
		n++
		v >>= 4
		if v == 0 && n >= digits {
			break
		}
	}
	for n > 0 {
		n--
		b.c(t[n])
	}
}
func (b *tbuf) dec(v int32) {
	var t [11]byte
	n := 0
	u := uint32(v)
	if v < 0 {
		b.c('-')
		u = uint32(-v)
	}
	for {
		t[n] = byte('0' + u%10)
		n++
		u /= 10
		if u == 0 {
			break
		}
	}
	for n > 0 {
		n--
		b.c(t[n])
	}
}
func (b *tbuf) reg(r int) { b.c('r'); b.dec(int32(r)) }

// "@(0xNN,rM)"
func (b *tbuf) dispReg(disp uint32, r int) {
	b.s("@(0x")
	b.hex(disp, 0)
	b.c(',')
	b.reg(r)
	b.c(')')
}
func (b *tbuf) dispName(disp uint32, name string) {
	b.s("@(0x")
	b.hex(disp, 0)
	b.c(',')
	b.s(name)
	b.c(')')
}

// "op rM,rN"
func (b *tbuf) mn(op string, m, n int) { b.s(op); b.c(' '); b.reg(m); b.c(','); b.reg(n) }

// "op rN"
func (b *tbuf) n1(op string, n int) { b.s(op); b.c(' '); b.reg(n) }

// "op @rN"
func (b *tbuf) atN(op string, n int) { b.s(op); b.s(" @"); b.reg(n) }

// "op rM,@rN" / "op rM,@-rN"
func (b *tbuf) store(op string, m, n int, predec bool) {
	b.s(op)
	b.c(' ')
	b.reg(m)
	if predec {
		b.s(",@-")
	} else {
		b.s(",@")
	}
	b.reg(n)
}

// "op @rM,rN" / "op @rM+,rN"
func (b *tbuf) load(op string, m, n int, postinc bool) {
	b.s(op)
	b.s(" @")
	b.reg(m)
	if postinc {
		b.c('+')
	}
	b.c(',')
	b.reg(n)
}
func (b *tbuf) sysToReg(op, sys string, n int) { b.s(op); b.c(' '); b.s(sys); b.c(','); b.reg(n) }
func (b *tbuf) regToSys(op string, m int, sys string) {
	b.s(op)
	b.c(' ')
	b.reg(m)
	b.c(',')
	b.s(sys)
}
func (b *tbuf) sysStore(op, sys string, n int) { b.s(op); b.c(' '); b.s(sys); b.s(",@-"); b.reg(n) }
func (b *tbuf) sysLoad(op string, m int, sys string) {
	b.s(op)
	b.s(" @")
	b.reg(m)
	b.s("+,")
	b.s(sys)
}
func (b *tbuf) word(x uint16) { b.s(".word 0x"); b.hex(uint32(x), 4) }

// ctrlName: bits 7..4 of the 0x0n?2 / 0x4n?e family: sr gbr vbr ssr spc, then the DSP repeat
// registers mod rs re.
var ctrlNames = [8]string{"sr", "gbr", "vbr", "ssr", "spc", "mod", "rs", "re"}

func ctrlName(k int) string {
	if k >= 0 && k <= 7 {
		return ctrlNames[k]
	}
	return ""
}

// sysName: bits 7..4 of the sts/lds family (0x0n?a 0x4n?2 0x4n?6 0x4n?a): the system
// registers (sgr and dbr are control registers: stc/ldc) and, from 6, the DSP registers.
var sysNames = [16]string{"mach", "macl", "pr", "sgr", "", "", "dsr", "a0",
	"x0", "x1", "y0", "y1", "", "", "", "dbr"}

func sysName(k int) (name string, ctrl bool) {
	return sysNames[k&15], k == 3 || k == 15
}

func read32(read ReadFunc, a uint32) (uint32, bool) {
	hi, ok := read(a)
	if !ok {
		return 0, false
	}
	lo, ok := read(a + 2)
	if !ok {
		return 0, false
	}
	return uint32(hi)<<16 | uint32(lo), true
}

// decode is DASM's sh4_decode: fills *in and writes the text into b.
func decode(in *Instr, b *tbuf, x uint16, pc uint32, read ReadFunc) {
	in.Addr = pc
	in.Op = x
	in.Size = 2
	if !decodeBody(in, b, x, pc, read) {
		b.n = 0
		b.word(x)
		in.Kind = KindIllegal
	}
}

// decodeBody returns false for a word that is no instruction (printed as .word).
func decodeBody(in *Instr, b *tbuf, x uint16, pc uint32, read ReadFunc) bool {
	n, m, d4, d8, d12 := int(x>>8)&15, int(x>>4)&15, int(x)&15, uint32(x)&0xFF, uint32(x)&0xFFF
	simm8 := int32(int8(d8))

	switch x >> 12 {
	case 0x0:
		switch d4 {
		case 0x2:
			if m&8 != 0 {
				b.s("stc r")
				b.dec(int32(m & 7))
				b.s("_bank,")
				b.reg(n)
				return true
			}
			if c := ctrlName(m); c != "" {
				b.sysToReg("stc", c, n)
				return true
			}
			return false
		case 0x3:
			switch m {
			case 0x0:
				b.n1("bsrf", n)
				in.Kind, in.Reg, in.Delayed = KindCallReg, uint8(n), true
			case 0x2:
				b.n1("braf", n)
				in.Kind, in.Reg, in.Delayed = KindJumpReg, uint8(n), true
			case 0x6:
				b.s("movli.l @")
				b.reg(n)
				b.s(",r0")
			case 0x7:
				b.s("movco.l r0,@")
				b.reg(n)
			case 0x8:
				b.atN("pref", n)
			case 0x9:
				b.atN("ocbi", n)
			case 0xa:
				b.atN("ocbp", n)
			case 0xb:
				b.atN("ocbwb", n)
			case 0xc:
				b.s("movca.l r0,@")
				b.reg(n)
			case 0xd:
				b.atN("prefi", n)
			case 0xe:
				b.atN("icbi", n)
			default:
				return false
			}
		case 0x4:
			b.s("mov.b ")
			b.reg(m)
			b.s(",@(r0,")
			b.reg(n)
			b.c(')')
		case 0x5:
			b.s("mov.w ")
			b.reg(m)
			b.s(",@(r0,")
			b.reg(n)
			b.c(')')
		case 0x6:
			b.s("mov.l ")
			b.reg(m)
			b.s(",@(r0,")
			b.reg(n)
			b.c(')')
		case 0x7:
			b.mn("mul.l", m, n)
		case 0x8:
			if n != 0 {
				return false
			}
			switch m {
			case 0:
				b.s("clrt")
			case 1:
				b.s("sett")
			case 2:
				b.s("clrmac")
			case 3:
				b.s("ldtlb")
			case 4:
				b.s("clrs")
			case 5:
				b.s("sets")
			case 8:
				b.s("clrdmxy")
			case 9:
				b.s("setdmx")
			case 0xc:
				b.s("setdmy")
			default:
				return false
			}
		case 0x9:
			switch {
			case x == 0x0009:
				b.s("nop")
			case x == 0x0019:
				b.s("div0u")
			case m == 2:
				b.n1("movt", n)
			default:
				return false
			}
		case 0xa: // sts / stc xxx,rN
			r, c := sysName(m)
			if r == "" {
				return false
			}
			if c {
				b.sysToReg("stc", r, n)
			} else {
				b.sysToReg("sts", r, n)
			}
		case 0xb:
			switch x {
			case 0x000b:
				b.s("rts")
				in.Kind, in.Delayed = KindReturn, true
			case 0x001b:
				b.s("sleep")
			case 0x002b:
				b.s("rte")
				in.Kind, in.Delayed = KindRTE, true
			case 0x00ab:
				b.s("synco")
			default:
				return false
			}
		case 0xc:
			b.s("mov.b @(r0,")
			b.reg(m)
			b.s("),")
			b.reg(n)
		case 0xd:
			b.s("mov.w @(r0,")
			b.reg(m)
			b.s("),")
			b.reg(n)
		case 0xe:
			b.s("mov.l @(r0,")
			b.reg(m)
			b.s("),")
			b.reg(n)
		case 0xf:
			b.s("mac.l @")
			b.reg(m)
			b.s("+,@")
			b.reg(n)
			b.c('+')
		default:
			return false
		}

	case 0x1:
		b.s("mov.l ")
		b.reg(m)
		b.c(',')
		b.dispReg(uint32(d4)*4, n)

	case 0x2:
		switch d4 {
		case 0x0:
			b.store("mov.b", m, n, false)
		case 0x1:
			b.store("mov.w", m, n, false)
		case 0x2:
			b.store("mov.l", m, n, false)
		case 0x4:
			b.store("mov.b", m, n, true)
		case 0x5:
			b.store("mov.w", m, n, true)
		case 0x6:
			b.store("mov.l", m, n, true)
		case 0x7:
			b.mn("div0s", m, n)
		case 0x8:
			b.mn("tst", m, n)
		case 0x9:
			b.mn("and", m, n)
		case 0xa:
			b.mn("xor", m, n)
		case 0xb:
			b.mn("or", m, n)
		case 0xc:
			b.mn("cmp/str", m, n)
		case 0xd:
			b.mn("xtrct", m, n)
		case 0xe:
			b.mn("mulu.w", m, n)
		case 0xf:
			b.mn("muls.w", m, n)
		default:
			return false
		}

	case 0x3:
		var s string
		switch d4 {
		case 0x0:
			s = "cmp/eq"
		case 0x2:
			s = "cmp/hs"
		case 0x3:
			s = "cmp/ge"
		case 0x4:
			s = "div1"
		case 0x5:
			s = "dmulu.l"
		case 0x6:
			s = "cmp/hi"
		case 0x7:
			s = "cmp/gt"
		case 0x8:
			s = "sub"
		case 0xa:
			s = "subc"
		case 0xb:
			s = "subv"
		case 0xc:
			s = "add"
		case 0xd:
			s = "dmuls.l"
		case 0xe:
			s = "addc"
		case 0xf:
			s = "addv"
		default:
			return false
		}
		b.mn(s, m, n)

	case 0x4:
		return decode4(in, b, x, n, m, d4)

	case 0x5:
		b.s("mov.l ")
		b.dispReg(uint32(d4)*4, m)
		b.c(',')
		b.reg(n)

	case 0x6:
		switch d4 {
		case 0x0:
			b.load("mov.b", m, n, false)
		case 0x1:
			b.load("mov.w", m, n, false)
		case 0x2:
			b.load("mov.l", m, n, false)
		case 0x3:
			b.mn("mov", m, n)
		case 0x4:
			b.load("mov.b", m, n, true)
		case 0x5:
			b.load("mov.w", m, n, true)
		case 0x6:
			b.load("mov.l", m, n, true)
		case 0x7:
			b.mn("not", m, n)
		case 0x8:
			b.mn("swap.b", m, n)
		case 0x9:
			b.mn("swap.w", m, n)
		case 0xa:
			b.mn("negc", m, n)
		case 0xb:
			b.mn("neg", m, n)
		case 0xc:
			b.mn("extu.b", m, n)
		case 0xd:
			b.mn("extu.w", m, n)
		case 0xe:
			b.mn("exts.b", m, n)
		case 0xf:
			b.mn("exts.w", m, n)
		}

	case 0x7:
		b.s("add #")
		b.dec(simm8)
		b.c(',')
		b.reg(n)

	case 0x8:
		switch n {
		case 0x0:
			b.s("mov.b r0,")
			b.dispReg(uint32(d4), m)
		case 0x1:
			b.s("mov.w r0,")
			b.dispReg(uint32(d4)*2, m)
		case 0x4:
			b.s("mov.b ")
			b.dispReg(uint32(d4), m)
			b.s(",r0")
		case 0x5:
			b.s("mov.w ")
			b.dispReg(uint32(d4)*2, m)
			b.s(",r0")
		case 0x8:
			b.s("cmp/eq #")
			b.dec(simm8)
			b.s(",r0")
		case 0x2:
			b.s("setrc #")
			b.dec(int32(d8))
		case 0xa:
			b.s("ldrc #")
			b.dec(int32(d8))
		case 0xc, 0xe: // ldrs / ldre @(disp,pc)
			if n == 0xc {
				b.s("ldrs 0x")
			} else {
				b.s("ldre 0x")
			}
			b.hex(pc+4+d8*2, 8)
		case 0x9, 0xb, 0xd, 0xf:
			names := [4]string{"bt", "bf", "bt/s", "bf/s"}
			t := pc + 4 + uint32(simm8*2)
			b.s(names[(n-9)/2])
			b.s(" 0x")
			b.hex(t, 8)
			in.Kind, in.HasTarget, in.Target, in.Delayed = KindCondBranch, true, t, n >= 0xd
		default:
			return false
		}

	case 0x9:
		ea := pc + 4 + d8*2
		b.s("mov.w ")
		b.dispName(d8*2, "pc")
		b.c(',')
		b.reg(n)
		in.Kind, in.Reg, in.LitAddr, in.LitSize = KindLiteral, uint8(n), ea, 2
		if read != nil {
			if v, ok := read(ea); ok {
				in.LitValue, in.LitOK = uint32(v), true
			}
		}

	case 0xa, 0xb:
		disp := int32(d12)
		if d12&0x800 != 0 {
			disp -= 0x1000
		}
		t := pc + 4 + uint32(disp*2)
		if x>>12 == 0xa {
			b.s("bra 0x")
			in.Kind = KindBranch
		} else {
			b.s("bsr 0x")
			in.Kind = KindCall
		}
		b.hex(t, 8)
		in.HasTarget, in.Target, in.Delayed = true, t, true

	case 0xc:
		switch n {
		case 0x0:
			b.s("mov.b r0,")
			b.dispName(d8, "gbr")
		case 0x1:
			b.s("mov.w r0,")
			b.dispName(d8*2, "gbr")
		case 0x2:
			b.s("mov.l r0,")
			b.dispName(d8*4, "gbr")
		case 0x3:
			b.s("trapa #0x")
			b.hex(d8, 0)
			in.Kind = KindTrapa
		case 0x4:
			b.s("mov.b ")
			b.dispName(d8, "gbr")
			b.s(",r0")
		case 0x5:
			b.s("mov.w ")
			b.dispName(d8*2, "gbr")
			b.s(",r0")
		case 0x6:
			b.s("mov.l ")
			b.dispName(d8*4, "gbr")
			b.s(",r0")
		case 0x7:
			ea := pc&^3 + 4 + d8*4
			b.s("mova ")
			b.dispName(d8*4, "pc")
			b.s(",r0")
			in.Kind, in.LitAddr, in.Reg = KindMova, ea, 0
		default:
			ops := [8]string{"tst #0x", "and #0x", "xor #0x", "or #0x",
				"tst.b #0x", "and.b #0x", "xor.b #0x", "or.b #0x"}
			b.s(ops[n-8])
			b.hex(d8, 0)
			if n < 0xc {
				b.s(",r0")
			} else {
				b.s(",@(r0,gbr)")
			}
		}

	case 0xd:
		ea := pc&^3 + 4 + d8*4
		b.s("mov.l ")
		b.dispName(d8*4, "pc")
		b.c(',')
		b.reg(n)
		in.Kind, in.Reg, in.LitAddr, in.LitSize = KindLiteral, uint8(n), ea, 4
		if read != nil {
			if v, ok := read32(read, ea); ok {
				in.LitValue, in.LitOK = v, true
			}
		}

	case 0xe:
		b.s("mov #")
		b.dec(simm8)
		b.c(',')
		b.reg(n)

	case 0xf:
		return decodeF(in, b, x, pc, read, m, d4)
	}
	return true
}

func decode4(in *Instr, b *tbuf, x uint16, n, m, d4 int) bool {
	switch d4 {
	case 0xc:
		b.mn("shad", m, n)
	case 0xd:
		b.mn("shld", m, n)
	case 0xf:
		b.s("mac.w @")
		b.reg(m)
		b.s("+,@")
		b.reg(n)
		b.c('+')
	case 0x3: // stc.l xxx,@-rn
		if m&8 != 0 {
			b.s("stc.l r")
			b.dec(int32(m & 7))
			b.s("_bank,@-")
			b.reg(n)
			return true
		}
		if c := ctrlName(m); c != "" {
			b.sysStore("stc.l", c, n)
			return true
		}
		return false
	case 0x7: // ldc.l @rn+,xxx
		if m&8 != 0 {
			b.s("ldc.l @")
			b.reg(n)
			b.s("+,r")
			b.dec(int32(m & 7))
			b.s("_bank")
			return true
		}
		if c := ctrlName(m); c != "" {
			b.sysLoad("ldc.l", n, c)
			return true
		}
		return false
	case 0xe: // ldc rn,xxx
		if m&8 != 0 {
			b.s("ldc ")
			b.reg(n)
			b.s(",r")
			b.dec(int32(m & 7))
			b.s("_bank")
			return true
		}
		if c := ctrlName(m); c != "" {
			b.regToSys("ldc", n, c)
			return true
		}
		return false
	case 0x0:
		return pick3(b, m, n, "shll", "dt", "shal")
	case 0x1:
		return pick3(b, m, n, "shlr", "cmp/pz", "shar")
	case 0x2: // sts.l / stc.l xxx,@-rn
		r, c := sysName(m)
		if r == "" {
			return false
		}
		if c {
			b.sysStore("stc.l", r, n)
		} else {
			b.sysStore("sts.l", r, n)
		}
	case 0x4:
		switch m {
		case 0:
			b.n1("rotl", n)
		case 1:
			b.n1("setrc", n)
		case 2:
			b.n1("rotcl", n)
		case 3:
			b.n1("ldrc", n)
		default:
			return false
		}
	case 0x5:
		return pick3(b, m, n, "rotr", "cmp/pl", "rotcr")
	case 0x6: // lds.l / ldc.l @rn+,xxx
		r, c := sysName(m)
		if r == "" {
			return false
		}
		if c {
			b.sysLoad("ldc.l", n, r)
		} else {
			b.sysLoad("lds.l", n, r)
		}
	case 0x8:
		return pick3(b, m, n, "shll2", "shll8", "shll16")
	case 0x9:
		switch m {
		case 0x0:
			b.n1("shlr2", n)
		case 0x1:
			b.n1("shlr8", n)
		case 0x2:
			b.n1("shlr16", n)
		case 0xa:
			b.s("movua.l @")
			b.reg(n)
			b.s(",r0")
		case 0xe:
			b.s("movua.l @")
			b.reg(n)
			b.s("+,r0")
		default:
			return false
		}
	case 0xa: // lds / ldc rn,xxx
		r, c := sysName(m)
		if r == "" {
			return false
		}
		if c {
			b.regToSys("ldc", n, r)
		} else {
			b.regToSys("lds", n, r)
		}
	case 0xb:
		switch m {
		case 0:
			b.atN("jsr", n)
			in.Kind, in.Reg, in.Delayed = KindCallReg, uint8(n), true
		case 1:
			b.atN("tas.b", n)
		case 2:
			b.atN("jmp", n)
			in.Kind, in.Reg, in.Delayed = KindJumpReg, uint8(n), true
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// pick3 prints "op rN" with op chosen by m = 0, 1, 2; false for any other m.
func pick3(b *tbuf, m, n int, s0, s1, s2 string) bool {
	switch m {
	case 0:
		b.n1(s0, n)
	case 1:
		b.n1(s1, n)
	case 2:
		b.n1(s2, n)
	default:
		return false
	}
	return true
}
