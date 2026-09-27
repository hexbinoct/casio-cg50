package main

// SH-4A MMU (cont.18q): the UTLB, ldtlb, address translation and MMU exceptions — needed to
// run add-ins. Built-in apps live in the OS image in P1/P2 and never translate, but an add-in
// (.g3a) runs at virtual 0x00300000 (code) / 0x08100000 (RAM), mapped by the OS:
//
//   - launch (0x8002c84c / 0x8002c8a4): PTEH/PTEL + ldtlb with MMUCR.URC choosing the slot —
//     one 4 KB code page 0x00300000 -> the .g3a in flash, eight 64 KB RAM pages 0x0810xxxx ->
//     DRAM 0x0C16xxxx (slots 55-62), slot 63 = VPN 0; then MMUCR.AT=1 and a jump to 0x00300000;
//   - every further code page is demand-paged: a TLB miss enters VBR+0x400, the OS handler
//     0x8002c918 reads PTEH, looks the page up in its table 0x8c04cf0c[(page-0x300000)>>12] and
//     maps it with ldtlb into its own round-robin slot (counter 0x8c04d70c, slots 0-54).
//
// Model: 64 UTLB entries in the CCN block (ITLB not modelled — fetches use the UTLB); MMUCR
// AT (bit0), TI (bit2, invalidate all), SV (bit8), URC (15:10) — URC is only what software
// writes (no automatic increment: the OS always sets it before ldtlb). When AT=1, P0/U0
// (va < 0x80000000) and P3 (0xC0000000-0xDFFFFFFF) translate; page sizes 1K/4K/64K/1M; ASID
// compared unless SH, or SV with SR.MD=1; only V=1 entries take part in the match (an entry
// with V=0 never hits, so flushed/unused entries — VPN 0 — don't shadow real mappings; a miss
// is taken instead). Exceptions are precise (cpu.stepMMU rolls the instruction back): TLB
// miss -> VBR+0x400; protection violation, initial page write -> VBR+0x100; TEA = address, PTEH.VPN = its page, EXPEVT = 0x040 read/fetch, 0x060
// write, 0x0A0/0x0C0 protection read/write, 0x080 initial page write. Memory-mapped UTLB
// address/data arrays at 0xF6000000/0xF7000000 are supported. Mirrored in emu/mmio.py CCN.

const (
	ccnPTEH   = 0x00
	ccnPTEL   = 0x04
	ccnTEA    = 0x0C
	ccnMMUCR  = 0x10
	ccnEXPEVT = 0x24
	ccnPTEA   = 0x34

	utlbSize = 64
)

var pageMask = [4]uint32{0x3FF, 0xFFF, 0xFFFF, 0xFFFFF} // SZ 1K, 4K, 64K, 1M

type tlbEntry struct {
	vpn, asid uint32 // address-array fields (vpn = bits 31:10)
	data      uint32 // data-array word = PTEL layout: PPN 28:10, V 8, SZ1 7, PR 6:5, SZ0 4, C 3, D 2, SH 1, WT 0
	ptea      uint32
}

func (e *tlbEntry) valid() bool  { return e.data&0x100 != 0 }
func (e *tlbEntry) dirty() bool  { return e.data&0x4 != 0 }
func (e *tlbEntry) shared() bool { return e.data&0x2 != 0 }
func (e *tlbEntry) pr() uint32   { return (e.data >> 5) & 3 }
func (e *tlbEntry) mask() uint32 { return pageMask[(e.data>>6)&2|(e.data>>4)&1] }
func (e *tlbEntry) ppn() uint32  { return e.data & 0x1FFFFC00 }
func (e *tlbEntry) addrWord() uint32 {
	return e.vpn | (e.data&0x4)<<7 | (e.data & 0x100) | e.asid // VPN | D<<9 | V<<8 | ASID
}

// mmuFault is raised (panic) by a translating memory access and caught by cpu.stepMMU.
type mmuFault struct {
	va, expevt, vector uint32
}

// ccn: the MMU/cache control block at 0xFF000000 (PTEH, PTEL, TTB, TEA, MMUCR, EXPEVT,
// INTEVT, PTEA, ...) plus the UTLB it drives.
type ccn struct {
	base
	utlb [utlbSize]tlbEntry
	at   bool    // MMUCR.AT: translation enabled
	last int     // index of the last UTLB hit (lookup fast path)
	mem  *Memory // mirrors `at` into mem.mmuAt (one field test on the hot path)
}

func (c *ccn) setAT(on bool) {
	c.at = on
	if c.mem != nil {
		c.mem.mmuAt = on
	}
}

func (c *ccn) read(va, size uint32) uint32 {
	off := (va - c.bs) & 0xFFFF
	if off == ccnEXPEVT {
		// Reset value doubles as the model strap the boot reads (0x0A02 = fx-CG50, cont.12);
		// once an exception has been taken it holds that exception's code.
		if v, ok := c.regs[ccnEXPEVT]; ok {
			return v
		}
		return 0x0A02
	}
	return c.regs[va-c.bs]
}

func (c *ccn) write(va, size, val uint32) {
	off := va - c.bs
	if off == ccnMMUCR {
		if val&4 != 0 { // TI: invalidate every UTLB entry; the bit itself reads back 0
			for i := range c.utlb {
				c.utlb[i].data &^= 0x100
			}
			val &^= 4
		}
		c.setAT(val&1 != 0)
	}
	c.regs[off] = val
}

// ldtlb loads PTEH/PTEL/PTEA into the UTLB entry MMUCR.URC selects.
func (c *ccn) ldtlb() {
	e := &c.utlb[(c.regs[ccnMMUCR]>>10)&0x3F]
	pteh := c.regs[ccnPTEH]
	e.vpn, e.asid = pteh&0xFFFFFC00, pteh&0xFF
	e.data = c.regs[ccnPTEL] & 0x1FFFFDFF
	e.ptea = c.regs[ccnPTEA]
}

func (c *ccn) matches(e *tlbEntry, va uint32, md bool) bool {
	if !e.valid() || (va^e.vpn)&^e.mask()&0xFFFFFC00 != 0 {
		return false
	}
	return e.shared() || (md && c.regs[ccnMMUCR]&0x100 != 0) || e.asid == c.regs[ccnPTEH]&0xFF
}

// translate maps a P0/U0/P3 virtual address; panics with mmuFault on a miss/violation.
func (c *ccn) translate(va uint32, write, md bool) uint32 {
	idx := -1
	if c.matches(&c.utlb[c.last], va, md) {
		idx = c.last
	} else {
		for i := range c.utlb {
			if c.matches(&c.utlb[i], va, md) {
				idx = i
				break
			}
		}
	}
	code := uint32(0x040)
	if write {
		code = 0x060
	}
	if idx < 0 {
		panic(mmuFault{va, code, 0x400})
	}
	c.last = idx
	e := &c.utlb[idx]
	pr := e.pr()
	if (md && write && pr == 0) || (!md && (pr < 2 || (write && pr == 2))) {
		pc := uint32(0x0A0)
		if write {
			pc = 0x0C0
		}
		panic(mmuFault{va, pc, 0x100})
	}
	if write && !e.dirty() {
		panic(mmuFault{va, 0x080, 0x100})
	}
	m := e.mask()
	return e.ppn()&^m | va&m
}

// raise performs MMU-exception entry for fault f at instruction address spc.
func (c *ccn) raise(cpu *CPU, f mmuFault, spc uint32) {
	c.regs[ccnTEA] = f.va
	c.regs[ccnPTEH] = f.va&0xFFFFFC00 | c.regs[ccnPTEH]&0xFF
	c.regs[ccnEXPEVT] = f.expevt
	cpu.ssr, cpu.spc, cpu.sgr = cpu.sr, spc, cpu.r[15]
	cpu.setSR(cpu.sr | srMD | srRB | srBL)
	cpu.pc = cpu.vbr + f.vector
}

// stateMap / restoreState serialise the MMU into the save-state's CCN register map
// (keys utlbKey|i<<4|{0 address word, 4 data word, 8 PTEA}).
const utlbKey = 0x00100000

func (c *ccn) stateMap() map[uint32]uint32 {
	m := make(map[uint32]uint32, len(c.regs)+3*utlbSize)
	for k, v := range c.regs {
		m[k] = v
	}
	for i := range c.utlb {
		e := &c.utlb[i]
		if e.vpn|e.asid|e.data|e.ptea != 0 {
			k := utlbKey | uint32(i)<<4
			m[k], m[k+4], m[k+8] = e.addrWord(), e.data, e.ptea
		}
	}
	return m
}

func (c *ccn) restoreState(m map[uint32]uint32) {
	for k := range c.regs {
		delete(c.regs, k)
	}
	c.utlb = [utlbSize]tlbEntry{}
	for k, v := range m {
		if k&0xFFF00000 == utlbKey {
			e := &c.utlb[(k>>4)&0x3F]
			switch k & 0xF {
			case 0:
				e.vpn, e.asid = v&0xFFFFFC00, v&0xFF
			case 4:
				e.data = v
			case 8:
				e.ptea = v
			}
			continue
		}
		c.regs[k] = v
	}
	c.setAT(c.regs[ccnMMUCR]&1 != 0)
	c.last = 0
}

// utlbArrays: the memory-mapped UTLB address array (0xF6000000) and data array 1
// (0xF7000000); entry = address bits 13:8, associative write = address bit 7 (address array).
type utlbArrays struct {
	base
	c *ccn
}

func (a *utlbArrays) read(va, size uint32) uint32 {
	e := &a.c.utlb[(va>>8)&0x3F]
	if va < 0xF7000000 {
		return e.addrWord()
	}
	return e.data
}

func (a *utlbArrays) write(va, size, val uint32) {
	if va >= 0xF7000000 {
		if va < 0xF7800000 { // data array 1 (0xF78..: data array 2 = PTEA, unused)
			a.c.utlb[(va>>8)&0x3F].data = val & 0x1FFFFDFF
		} else {
			a.c.utlb[(va>>8)&0x3F].ptea = val
		}
		return
	}
	setVD := func(e *tlbEntry) {
		e.data = e.data&^0x104 | val&0x100 | (val>>7)&0x4 // V = bit8, D = bit9 -> data bit2
	}
	if va&0x80 == 0 { // non-associative: write VPN, D, V, ASID of the addressed entry
		e := &a.c.utlb[(va>>8)&0x3F]
		e.vpn, e.asid = val&0xFFFFFC00, val&0xFF
		setVD(e)
		return
	}
	// associative write: update V/D of every entry matching the written VPN (and its ASID,
	// unless the entry is shared or single-virtual mode is on)
	sv := a.c.regs[ccnMMUCR]&0x100 != 0
	for i := range a.c.utlb {
		e := &a.c.utlb[i]
		if (val^e.vpn)&^e.mask()&0xFFFFFC00 == 0 && (e.shared() || sv || e.asid == val&0xFF) {
			setVD(e)
		}
	}
}
