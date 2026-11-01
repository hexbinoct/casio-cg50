package sh4

import "sort"

// xrefsScan is DASM's an_xrefs for one target, line by line: the reference the index must
// agree with. poolCap is how many pool words are remembered as already reported through their
// load (DASM: 64; <0 = no limit, as the index).
func (an *Analysis) xrefsScan(target uint32, poolCap int) []Xref {
	var out []Xref
	var pools []uint32
	for _, f := range an.funcs {
		var lv, lpc [16]uint32
		var lmask uint16
		a1 := f.Addr + f.Size
		for a := f.Addr; a < a1; a += 2 {
			op, ok := an.rd16(a)
			if !ok {
				break
			}
			n, h := int(op>>8)&15, op>>12
			call, end := false, false
			if h == 0xF {
				lmask &= 0xFF00
				a += uint32(InsnLen(uint16(op))) - 2
				continue
			}
			if h == 0xD {
				la := a&^3 + 4 + (op&0xFF)*4
				if v, ok := an.rd32(la); ok {
					lv[n], lpc[n] = v, a
					lmask |= 1 << n
					if v == target {
						out = append(out, Xref{a, XrefLoad})
						if poolCap < 0 || len(pools) < poolCap {
							pools = append(pools, la)
						}
					}
				} else {
					lmask &^= 1 << n
				}
				continue
			}
			switch {
			case h == 0xB || h == 0xA:
				t := a + 4 + uint32(s12(op&0xFFF)*2)
				k := XrefJump
				if h == 0xB {
					k = XrefCall
				}
				if t == target {
					out = append(out, Xref{a, k})
				}
				if h == 0xB {
					call = true
				} else {
					end = true
				}
			case op&0xF900 == 0x8900:
				if a+4+uint32(s8(op&0xFF)*2) == target {
					out = append(out, Xref{a, XrefBranch})
				}
			case op&0xFF00 == 0xC700:
				if a&^3+4+(op&0xFF)*4 == target {
					out = append(out, Xref{a, XrefMova})
				}
			case op&0xF0FF == 0x400B || op&0xF0FF == 0x402B:
				k := XrefJump
				if op&0xF0FF == 0x400B {
					k = XrefCall
				}
				if (lmask>>n)&1 != 0 && lv[n] == target {
					if c := len(out); c > 0 && out[c-1].From == lpc[n] && out[c-1].Kind == XrefLoad {
						out[c-1] = Xref{a, k}
					} else {
						out = append(out, Xref{a, k})
					}
				}
				if k == XrefCall {
					call = true
				} else {
					end = true
				}
			case op == 0x000B || op == 0x002B || op&0xF0FF == 0x0023:
				end = true
			}
			if writesRn(op) {
				lmask &^= 1 << n
			}
			if call {
				lmask &= 0xFF00
			}
			if end {
				lmask = 0
			}
		}
	}
	fi, nf := 0, len(an.funcs)
	for a := (an.lo + 3) &^ 3; a+4 <= an.hi && a >= an.lo; a += 4 {
		for fi < nf && an.funcs[fi].Addr+an.funcs[fi].Size <= a {
			fi++
		}
		if fi < nf && an.funcs[fi].Addr <= a {
			a = (an.funcs[fi].Addr+an.funcs[fi].Size+3)&^3 - 4
			continue
		}
		if v, ok := an.rd32(a); ok && v == target {
			seen := false
			for _, p := range pools {
				seen = seen || p == a
			}
			if !seen {
				out = append(out, Xref{a, XrefData})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}
