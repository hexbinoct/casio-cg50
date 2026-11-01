package sh4

import "fmt"

// Indirect jumps and calls, names and listing comments, ported from DASM's names.c and main.c.

// decodeInfo is DecodeWord without the text string.
func decodeInfo(op uint16, pc uint32, read ReadFunc) Instr {
	var b tbuf
	var in Instr
	decode(&in, &b, op, pc, read)
	return in
}

// writesReg reports whether op writes register r: every form that does (group 4 counts whole,
// though a few of its forms only read Rn). The pc-relative loads and mov #imm are matched
// before it. DASM's writes_reg.
func writesReg(op uint32, r int) bool {
	h, n, lo, hb := op>>12, int(op>>8)&15, op&15, op>>8
	if h == 0xF {
		return r < 8 // DSP transfers move r0-r7
	}
	if r == 0 && (hb == 0x84 || hb == 0x85 || // mov.b/w @(disp,Rm),r0
		hb >= 0xC4 && hb <= 0xC7 || // gbr loads, mova
		hb >= 0xC9 && hb <= 0xCB || // and/xor/or #imm,r0
		op&0xF0FF == 0x0063) { // movli.l @Rm,r0
		return true
	}
	if n != r {
		return false
	}
	switch h {
	case 0x0:
		return lo == 0x2 || lo == 0xA || lo >= 0xC && lo <= 0xE || op&0xFF == 0x29
	case 0x2:
		return 0x2E70>>lo&1 != 0 // @-Rn, and or xor xtrct
	case 0x3:
		return 0xDD10>>lo&1 != 0 // div1 sub(c/v) add(c/v)
	case 0x4:
		return op&0xFF != 0x0B && op&0xFF != 0x2B // not jsr/jmp
	case 0x5, 0x6, 0x7, 0x9, 0xD, 0xE:
		return true
	}
	return false
}

// findRegValue scans back from the jump or call at jpc over the max instructions before it
// for the last write of register r by a mov.w/mov.l @(disp,pc) or mov #imm. It stops at any
// other writer of r, at a return and at an unconditional jump (a basic-block boundary,
// roughly). slot: look at the delay slot first. The CPU reads the jump's own register before
// the delay slot runs, so a load there does not count for it; a syscall's number in r0 is
// read by the trampoline, after the slot. DASM's find_reg_value.
func findRegValue(read ReadFunc, jpc uint32, r int, slot bool, max int) (uint32, bool) {
	a := jpc
	if slot {
		a, max = jpc+4, max+1
	}
	for i := 0; i < max; i++ {
		a -= 2
		if a == jpc { // the jump itself
			a -= 2
		}
		w, ok := read(a)
		if !ok {
			return 0, false
		}
		op := uint32(w)
		in := decodeInfo(w, a, read)
		if in.Kind == KindLiteral && int(in.Reg) == r {
			if !in.LitOK {
				return 0, false
			}
			if in.LitSize == 2 {
				return uint32(int32(int16(in.LitValue))), true
			}
			return in.LitValue, true
		}
		if op&0xF000 == 0xE000 && int(op>>8)&15 == r { // mov #imm,rN
			return uint32(int32(int8(op))), true
		}
		if writesReg(op, r) { // a writer we cannot follow
			return 0, false
		}
		switch in.Kind {
		case KindReturn, KindRTE, KindJumpReg, KindBranch:
			return 0, false
		}
	}
	return 0, false
}

// regConst evaluates the straight-line code of the n instructions before pc and returns
// register r's value at pc when it is a known constant: literals, mov #imm, mov, add, sub,
// add #imm, the shifts by 1/2/8/16, and/or/xor of known registers. Anything else that writes a
// register makes it unknown; a branch or call in the window forgets everything before it.
func regConst(read ReadFunc, pc uint32, r, n int) (uint32, bool) {
	var val [16]uint32
	var known uint16
	for a := pc - uint32(2*n); a != pc; a += 2 {
		w, ok := read(a)
		if !ok {
			known = 0
			continue
		}
		op := uint32(w)
		rn, rm := int(op>>8)&15, int(op>>4)&15
		kn, km := known>>rn&1 != 0, known>>rm&1 != 0
		set := func(v uint32, ok bool) {
			if ok {
				val[rn] = v
				known |= 1 << rn
			} else {
				known &^= 1 << rn
			}
		}
		in := decodeInfo(w, a, read)
		switch {
		case in.Kind == KindLiteral:
			v := in.LitValue
			if in.LitSize == 2 {
				v = uint32(int32(int16(v)))
			}
			set(v, in.LitOK)
		case op&0xF000 == 0xE000: // mov #imm,Rn
			set(uint32(int32(int8(op))), true)
		case op&0xF000 == 0x7000: // add #imm,Rn
			set(val[rn]+uint32(int32(int8(op))), kn)
		case op&0xF00F == 0x6003: // mov Rm,Rn
			set(val[rm], km)
		case op&0xF00F == 0x300C: // add Rm,Rn
			set(val[rn]+val[rm], kn && km)
		case op&0xF00F == 0x3008: // sub Rm,Rn
			set(val[rn]-val[rm], kn && km)
		case op&0xF00F == 0x2009: // and
			set(val[rn]&val[rm], kn && km)
		case op&0xF00F == 0x200B: // or
			set(val[rn]|val[rm], kn && km)
		case op&0xF00F == 0x200A: // xor
			set(val[rn]^val[rm], kn && km)
		case op&0xF0FF == 0x4000: // shll
			set(val[rn]<<1, kn)
		case op&0xF0FF == 0x4008:
			set(val[rn]<<2, kn)
		case op&0xF0FF == 0x4018:
			set(val[rn]<<8, kn)
		case op&0xF0FF == 0x4028:
			set(val[rn]<<16, kn)
		case op&0xF0FF == 0x4001: // shlr
			set(val[rn]>>1, kn)
		case op&0xF0FF == 0x4009:
			set(val[rn]>>2, kn)
		case op&0xF0FF == 0x4019:
			set(val[rn]>>8, kn)
		case op&0xF0FF == 0x4029:
			set(val[rn]>>16, kn)
		case in.IsBranch():
			known = 0
		case writesRn(op) || op&0xF000 == 0x3000 || op&0xF000 == 0x2000 && op&0xF >= 7 ||
			op&0xF00F == 0x000A || op&0xF00F == 0x0002 || op&0xF00E == 0x000C || op&0xF00F == 0x000E ||
			op&0xF0FF == 0x0029:
			known &^= 1 << rn // another writer of Rn
		}
		if in.Kind == KindIllegal || op>>12 == 0xF {
			known = 0
		}
	}
	return val[r], known>>r&1 != 0
}

// Indirect is where a jmp/jsr/braf/bsrf goes.
type Indirect struct {
	Target  uint32 // the destination (a syscall's handler when the table is known)
	Name    string // the syscall or handler name, "" if none
	Syscall int32  // the syscall number for a call through the trampoline, else -1 (-2: number not found)
}

// ResolveIndirect resolves the target of a jmp @Rn / jsr @Rn / braf Rn / bsrf Rn by scanning
// back from it for the instruction that loaded Rn (not counting its delay slot, which runs after
// Rn is read). A jump to the syscall
// trampoline is resolved to the syscall when r0's load is found too; sys (may be nil) gives
// the handler and its name.
func ResolveIndirect(read ReadFunc, sys *Syscalls, in *Instr) (Indirect, bool) {
	if in.Kind != KindJumpReg && in.Kind != KindCallReg {
		return Indirect{}, false
	}
	pc := in.Addr
	v, ok := findRegValue(read, pc, int(in.Reg), false, 10)
	if !ok {
		return Indirect{}, false
	}
	if in.Op&0xF0FF == 0x0003 || in.Op&0xF0FF == 0x0023 { // braf/bsrf: PC-relative
		v += pc + 4
	}
	r := Indirect{Target: v, Syscall: -1}
	if v == SyscallTrampoline {
		num, ok := findRegValue(read, pc, 0, true, 10) // r0 may be loaded in the delay slot
		if !ok {
			r.Name, r.Syscall = "syscall", -2
			return r, true
		}
		num &= 0xFFFF
		r.Name, r.Syscall = SyscallNameOf(num), int32(num)
		if h, ok := sys.Handler(num); ok {
			r.Target = h
		}
		return r, true
	}
	r.Name = sys.Name(v)
	return r, true
}

// Comment is DASM's listing comment for an instruction: a literal's value (and the syscall
// whose handler it is), a mova address, where an indirect jump goes (a syscall without a name:
// its handler), a branch target's name.
func Comment(read ReadFunc, sys *Syscalls, in *Instr) string {
	switch in.Kind {
	case KindLiteral:
		if !in.LitOK {
			return "=?"
		}
		if in.LitSize == 4 {
			if nm := sys.Name(in.LitValue); nm != "" {
				return fmt.Sprintf("=0x%08x %s", in.LitValue, nm)
			}
			return fmt.Sprintf("=0x%08x", in.LitValue)
		}
		return fmt.Sprintf("=0x%04x (%d)", in.LitValue, int16(in.LitValue))
	case KindMova:
		return fmt.Sprintf("&0x%08x", in.LitAddr)
	case KindJumpReg, KindCallReg:
		r, ok := ResolveIndirect(read, sys, in)
		if !ok {
			return ""
		}
		if r.Name != "" {
			return "-> " + r.Name
		}
		return fmt.Sprintf("-> 0x%08x", r.Target)
	case KindBranch, KindCondBranch, KindCall:
		return sys.Name(in.Target)
	}
	return ""
}

// Comment is the listing comment for in (see the function Comment).
func (an *Analysis) Comment(in *Instr) string { return Comment(an.img.Word, an.opt.Syscalls, in) }

// Resolve resolves an indirect jump or call of the analysed image (see ResolveIndirect).
func (an *Analysis) Resolve(in *Instr) (Indirect, bool) {
	return ResolveIndirect(an.img.Word, an.opt.Syscalls, in)
}

// FuncName names the function at addr: in the OS, the syscall it implements, or
// "syscall_XXXX" for an unnamed handler; in an add-in, "start" for its entry, or the syscall
// that a stub (loads, then jmp @Rn) forwards to; else "sub_XXXXXXXX".
func (an *Analysis) FuncName(addr uint32) string {
	sys := an.opt.Syscalls
	if nm := sys.Name(addr); nm != "" {
		return nm
	}
	if num, ok := sys.Number(addr); ok && an.opt.AllHandlers {
		return fmt.Sprintf("syscall_%04x", num)
	}
	if an.opt.AllHandlers { // the OS: DASM looks for stubs only in an add-in
		return fmt.Sprintf("sub_%08x", addr)
	}
	if len(an.opt.Entries) > 0 && addr == an.opt.Entries[0] {
		return "start"
	}
	for p := addr; p < addr+8; p += 2 { // a stub: loads, then jmp @rN
		w, ok := an.img.Word(p)
		if !ok {
			break
		}
		in := decodeInfo(w, p, an.img.Word)
		if in.Kind == KindJumpReg {
			if r, ok := ResolveIndirect(an.img.Word, sys, &in); ok && r.Name != "" {
				return r.Name
			}
			break
		}
		if in.Kind != KindLiteral && in.Kind != KindNormal && in.Kind != KindIllegal {
			break
		}
	}
	return fmt.Sprintf("sub_%08x", addr)
}
