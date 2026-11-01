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

// findRegValue scans back from pc (exclusive) up to max instructions for the last write of
// register r by a mov.w/mov.l @(disp,pc) or mov #imm, stopping at a return or an unconditional
// jump (a basic-block boundary, roughly) and at writers it cannot follow. The jump at self
// (the one being resolved, whose delay slot the scan starts in) does not stop it: in DASM it
// does, so a jmp @Rn or braf Rn is never resolved there (only jsr and bsrf are).
func findRegValue(read ReadFunc, pc uint32, r, max int, self uint32) (uint32, bool) {
	a := pc
	for i := 0; i < max; i++ {
		a -= 2
		w, ok := read(a)
		if !ok {
			return 0, false
		}
		op := uint32(w)
		in := decodeInfo(w, a, read)
		n := int(op>>8) & 15
		if in.Kind == KindLiteral && int(in.Reg) == r {
			if !in.LitOK {
				return 0, false
			}
			if in.LitSize == 2 {
				return uint32(int32(int16(in.LitValue))), true
			}
			return in.LitValue, true
		}
		if op&0xF000 == 0xE000 && n == r { // mov #imm,rN
			return uint32(int32(int8(op))), true
		}
		// other writers of rN we cannot follow: mov rM,rN / add / loads
		if (op&0xF00F == 0x6003 || op&0xF000 == 0x7000 || op&0xF000 == 0x5000 || op&0xF00F == 0x6002) && n == r {
			return 0, false
		}
		if in.Kind == KindReturn || in.Kind == KindRTE {
			return 0, false
		}
		if i > 0 && a != self && (in.Kind == KindJumpReg || in.Kind == KindBranch) {
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
// back (from its delay slot) for the instruction that loaded Rn. A jump to the syscall
// trampoline is resolved to the syscall when r0's load is found too; sys (may be nil) gives
// the handler and its name.
func ResolveIndirect(read ReadFunc, sys *Syscalls, in *Instr) (Indirect, bool) {
	if in.Kind != KindJumpReg && in.Kind != KindCallReg {
		return Indirect{}, false
	}
	pc := in.Addr
	v, ok := findRegValue(read, pc+4, int(in.Reg), 12, pc) // Rn may be loaded in the delay slot
	if !ok {
		return Indirect{}, false
	}
	if in.Op&0xF0FF == 0x0003 || in.Op&0xF0FF == 0x0023 { // braf/bsrf: PC-relative
		v += pc + 4
	}
	r := Indirect{Target: v, Syscall: -1}
	if v == SyscallTrampoline {
		num, ok := findRegValue(read, pc+4, 0, 12, pc)
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
// whose handler it is), a mova address, where an indirect jump goes, a branch target's name.
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
		switch {
		case !ok:
			return ""
		case r.Name != "":
			return "-> " + r.Name
		case r.Syscall >= 0:
			return fmt.Sprintf("-> syscall 0x%04x", r.Syscall)
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

// FuncName names the function at addr: the syscall it implements, "syscall_XXXX" for an
// unnamed handler, "start" for an add-in's entry, the syscall an add-in stub forwards to
// (loads, then jmp @Rn), else "sub_XXXXXXXX".
func (an *Analysis) FuncName(addr uint32) string {
	sys := an.opt.Syscalls
	if nm := sys.Name(addr); nm != "" {
		return nm
	}
	if num, ok := sys.Number(addr); ok && an.opt.AllHandlers {
		return fmt.Sprintf("syscall_%04x", num)
	}
	if !an.opt.AllHandlers && len(an.opt.Entries) > 0 && addr == an.opt.Entries[0] {
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
