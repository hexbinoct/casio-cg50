package main

import (
	"fmt"
	"strconv"
	"strings"

	"cg50emu"
)

// Breakpoint conditions: a small expression over the registers, compiled once into a Go
// function the core calls before the instruction runs (with its lock held, so it can only
// look at the registers it is given).
//
//	r4 == 0x10          pr >= 0x80000000 && r0 != 0      (r5 & 0xff) == 'A'      t
//
// Values are 32-bit unsigned; a condition holds when it is not zero. Operators, loosest
// first: || && | ^ & == != < <= > >= << >> + - and the unary ! ~ -. Names: r0-r15, pc, pr,
// sr, gbr, vbr, ssr, spc, sgr, dbr, mach, macl, and t, s, q, m (SR's bits). Numbers are
// decimal, 0x hex, or a character in single quotes.

type condFn func(r *cg50emu.Regs) uint32

// compileCond compiles a condition; "" is no condition (nil).
func compileCond(src string) (func(*cg50emu.Regs) bool, error) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	p := &condParser{src: src}
	p.next()
	f, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if p.tok != "" {
		return nil, fmt.Errorf("unexpected %q", p.tok)
	}
	return func(r *cg50emu.Regs) bool { return f(r) != 0 }, nil
}

type condParser struct {
	src string
	pos int
	tok string // "" at the end
}

var condOps = []string{"||", "&&", "==", "!=", "<=", ">=", "<<", ">>", "|", "^", "&", "<", ">", "+", "-", "!", "~", "(", ")"}

func (p *condParser) next() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if p.pos >= len(p.src) {
		p.tok = ""
		return
	}
	rest := p.src[p.pos:]
	if rest[0] == '\'' { // 'A'
		if len(rest) >= 3 && rest[2] == '\'' {
			p.tok, p.pos = rest[:3], p.pos+3
			return
		}
		p.tok, p.pos = rest[:1], p.pos+1
		return
	}
	for _, op := range condOps {
		if strings.HasPrefix(rest, op) {
			p.tok, p.pos = op, p.pos+len(op)
			return
		}
	}
	n := 0
	for n < len(rest) && (rest[n] == '_' || rest[n] >= '0' && rest[n] <= '9' || rest[n] >= 'a' && rest[n] <= 'z' || rest[n] >= 'A' && rest[n] <= 'Z') {
		n++
	}
	if n == 0 {
		n = 1
	}
	p.tok, p.pos = rest[:n], p.pos+n
}

// binary operators by precedence level (0 = loosest)
var condLevels = [][]string{{"||"}, {"&&"}, {"|"}, {"^"}, {"&"}, {"==", "!="}, {"<", "<=", ">", ">="}, {"<<", ">>"}, {"+", "-"}}

func (p *condParser) expr(level int) (condFn, error) {
	if level == len(condLevels) {
		return p.unary()
	}
	l, err := p.expr(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		for _, o := range condLevels[level] {
			if p.tok == o {
				op = o
			}
		}
		if op == "" {
			return l, nil
		}
		p.next()
		r, err := p.expr(level + 1)
		if err != nil {
			return nil, err
		}
		l = binop(op, l, r)
	}
}

func b2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func binop(op string, l, r condFn) condFn {
	switch op {
	case "||":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) != 0 || r(x) != 0) }
	case "&&":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) != 0 && r(x) != 0) }
	case "|":
		return func(x *cg50emu.Regs) uint32 { return l(x) | r(x) }
	case "^":
		return func(x *cg50emu.Regs) uint32 { return l(x) ^ r(x) }
	case "&":
		return func(x *cg50emu.Regs) uint32 { return l(x) & r(x) }
	case "==":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) == r(x)) }
	case "!=":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) != r(x)) }
	case "<":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) < r(x)) }
	case "<=":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) <= r(x)) }
	case ">":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) > r(x)) }
	case ">=":
		return func(x *cg50emu.Regs) uint32 { return b2u(l(x) >= r(x)) }
	case "<<":
		return func(x *cg50emu.Regs) uint32 { return l(x) << (r(x) & 31) }
	case ">>":
		return func(x *cg50emu.Regs) uint32 { return l(x) >> (r(x) & 31) }
	case "+":
		return func(x *cg50emu.Regs) uint32 { return l(x) + r(x) }
	default: // "-"
		return func(x *cg50emu.Regs) uint32 { return l(x) - r(x) }
	}
}

func (p *condParser) unary() (condFn, error) {
	switch p.tok {
	case "!", "~", "-":
		op := p.tok
		p.next()
		f, err := p.unary()
		if err != nil {
			return nil, err
		}
		switch op {
		case "!":
			return func(x *cg50emu.Regs) uint32 { return b2u(f(x) == 0) }, nil
		case "~":
			return func(x *cg50emu.Regs) uint32 { return ^f(x) }, nil
		}
		return func(x *cg50emu.Regs) uint32 { return -f(x) }, nil
	case "(":
		p.next()
		f, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		if p.tok != ")" {
			return nil, fmt.Errorf("missing )")
		}
		p.next()
		return f, nil
	case "":
		return nil, fmt.Errorf("the condition ends too early")
	}
	t := p.tok
	p.next()
	if len(t) == 3 && t[0] == '\'' {
		v := uint32(t[1])
		return func(*cg50emu.Regs) uint32 { return v }, nil
	}
	if t[0] >= '0' && t[0] <= '9' {
		v, err := strconv.ParseUint(t, 0, 32)
		if err != nil {
			return nil, fmt.Errorf("%q is not a 32-bit number (hex needs 0x)", t)
		}
		c := uint32(v)
		return func(*cg50emu.Regs) uint32 { return c }, nil
	}
	if f := condReg(strings.ToLower(t)); f != nil {
		return f, nil
	}
	return nil, fmt.Errorf("unknown name %q (registers r0-r15, pc, pr, sr, gbr, …; t s q m)", t)
}

func condReg(n string) condFn {
	if len(n) >= 2 && n[0] == 'r' {
		if i, err := strconv.Atoi(n[1:]); err == nil && i >= 0 && i < 16 && n == "r"+strconv.Itoa(i) {
			return func(x *cg50emu.Regs) uint32 { return x.R[i] }
		}
	}
	switch n {
	case "pc":
		return func(x *cg50emu.Regs) uint32 { return x.PC }
	case "pr":
		return func(x *cg50emu.Regs) uint32 { return x.PR }
	case "sr":
		return func(x *cg50emu.Regs) uint32 { return x.SR }
	case "gbr":
		return func(x *cg50emu.Regs) uint32 { return x.GBR }
	case "vbr":
		return func(x *cg50emu.Regs) uint32 { return x.VBR }
	case "ssr":
		return func(x *cg50emu.Regs) uint32 { return x.SSR }
	case "spc":
		return func(x *cg50emu.Regs) uint32 { return x.SPC }
	case "sgr":
		return func(x *cg50emu.Regs) uint32 { return x.SGR }
	case "dbr":
		return func(x *cg50emu.Regs) uint32 { return x.DBR }
	case "mach":
		return func(x *cg50emu.Regs) uint32 { return x.MACH }
	case "macl":
		return func(x *cg50emu.Regs) uint32 { return x.MACL }
	case "t":
		return func(x *cg50emu.Regs) uint32 { return x.SR & 1 }
	case "s":
		return func(x *cg50emu.Regs) uint32 { return x.SR >> 1 & 1 }
	case "q":
		return func(x *cg50emu.Regs) uint32 { return x.SR >> 8 & 1 }
	case "m":
		return func(x *cg50emu.Regs) uint32 { return x.SR >> 9 & 1 }
	}
	return nil
}
