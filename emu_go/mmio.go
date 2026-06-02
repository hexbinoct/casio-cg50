package main

// SH7305 MMIO bus + peripheral stubs. Faithful port of emu/mmio.py.
// Stubs return values that satisfy the OS poll loops (PLL ready, DMA done,
// key released) so boot/run progresses.

type region interface {
	contains(va uint32) bool
	name() string
	read(va, size uint32) uint32
	write(va, size, val uint32)
}

// base: a logged register window backed by a map (offset -> last written value).
type base struct {
	nm   string
	bs   uint32
	sz   uint32
	regs map[uint32]uint32
}

func newBase(nm string, bs, sz uint32) base {
	return base{nm: nm, bs: bs, sz: sz, regs: map[uint32]uint32{}}
}
func (b *base) contains(va uint32) bool { return va >= b.bs && va < b.bs+b.sz }
func (b *base) name() string            { return b.nm }
func (b *base) read(va, size uint32) uint32 {
	return b.regs[va-b.bs]
}
func (b *base) write(va, size, val uint32) { b.regs[va-b.bs] = val }

// CPG: boot PLL routine spins on 'ready' bit0 at +0x60 -> return 0 so it exits.
type cpg struct{ base }

func (c *cpg) read(va, size uint32) uint32 {
	off := va - c.bs
	if off == 0x60 {
		return 0
	}
	return c.regs[off]
}

// DMAC: CHCR of each channel at base+0xC (spaced 0x10). OS waits on TE (bit1);
// we complete instantly -> always report TE set.
type dmac struct{ base }

func (d *dmac) read(va, size uint32) uint32 {
	off := va - d.bs
	if (off & 0xF) == 0xC {
		return d.regs[off] | 0x2
	}
	return d.regs[off]
}

// KEYSC / KIU: all keys released = 0 in every data register.
type keysc struct{ base }

func (k *keysc) read(va, size uint32) uint32 { return 0 }

// ETMU: one-shot delays poll elapsed/underflow (bit15) at +0x60 -> report elapsed.
type etmu struct{ base }

func (e *etmu) read(va, size uint32) uint32 {
	off := (va - e.bs) & 0xFFFF
	if off == 0x60 {
		return 0x8000
	}
	return e.regs[off]
}

// CCN: MMU/cache control. The reset stub reads the HW model strap at +0x24 (0xFF000024)
// and selects model code: low16==0x0000->0xCA00, ==0x0020->0xCA01, ==0x0A02->0xCA02 (fx-CG50).
// We report 0x0A02 so the OS identifies as fx-CG50 (else fls0_init's verify loop @0x80365418
// never exits: it needs *(0xfd8018d4)==0xca02).
type ccn struct{ base }

func (c *ccn) read(va, size uint32) uint32 {
	if (va-c.bs)&0xFFFF == 0x24 {
		return 0x0A02
	}
	return c.regs[va-c.bs]
}

// FreeCounter: monotonic counter; boot delay loops read twice and wait for advance.
type freeCounter struct {
	base
	count uint32
}

func (f *freeCounter) read(va, size uint32) uint32 {
	f.count++
	mask := (uint32(1) << (size * 8)) - 1
	if size == 4 {
		mask = 0xFFFFFFFF
	}
	return f.count & mask
}

// PeriphIRQ (0xA4610000): flag at +0x88 (bits14/15 = timer underflow). Timer sets it,
// ISR acks by clearing. Treat the 0x88..0x8B word as one.
type periphIRQ struct{ base }

// Battery-voltage ADC reading reported at PERIPH_IRQ +0x82/+0x84. The OS battery
// monitor (FUN_801de54a) averages two samples and buckets the result (>>6) against
// thresholds ~347-475 (FUN_801e6bbc). A 0 reading falls below all thresholds -> level
// 0x12 -> the shell raises a "battery event" every loop and SKIPS drawing the main
// menu, parking in the idle event-pump. Reporting a normal mid-range voltage
// (raw>>6 == 453 -> bucket 2 "normal") lets the menu draw. 0x7140>>6 = 453.
const adcBatteryRaw = 0x7140

func (p *periphIRQ) read(va, size uint32) uint32 {
	off := va - p.bs
	if off == 0x82 || off == 0x84 {
		return adcBatteryRaw
	}
	if off >= 0x88 && off < 0x8C {
		off = 0x88
	}
	return p.regs[off]
}
func (p *periphIRQ) write(va, size, val uint32) {
	off := va - p.bs
	if off >= 0x88 && off < 0x8C {
		off = 0x88
	}
	p.regs[off] = val
}
func (p *periphIRQ) setTimerFlag() { p.regs[0x88] |= 0xC000 }

// INTX (0xA4140000): byte +0x24 reports key-scan ready (bit6) so the timer ISR's
// scan poll completes.
type intx struct{ base }

func (x *intx) read(va, size uint32) uint32 {
	if (va-x.bs)&0xFFFF == 0x24 {
		return 0x40
	}
	return x.regs[va-x.bs]
}

// ETMUCounter (0xA44D0000): down-counter at +0xD8 used as a fine delay reference;
// returns a value that decreases with cpu.cycles so delay loops complete.
type etmuCounter struct {
	base
	bus *MMIOBus
}

func (e *etmuCounter) read(va, size uint32) uint32 {
	if (va-e.bs)&0xFFFF == 0xD8 {
		var cyc uint64
		if e.bus != nil && e.bus.cpu != nil {
			cyc = e.bus.cpu.cycles
		}
		return uint32(-(cyc>>2)) & 0xFFFFFF
	}
	return e.regs[va-e.bs]
}

// ---- the bus ----
const (
	TimerINTEVT = 0x560
	TimerLevel  = 8
)

type MMIOBus struct {
	regions   []region
	periphIRQ *periphIRQ
	etmu2     *etmuCounter
	cpu       *CPU
	unknown   map[uint32]int
	watchPC   map[uint32]int // PC histogram of readers of watchBase region
	watchBase uint32         // if nonzero, reads in [watchBase, watchBase+0x1000) are attributed to cpu.pc

	timerPeriod uint64
	timerNext   uint64
	timerTicks  uint64

	// KEYSC keypress injection: during [kbStart,kbEnd) cycles, reads of the KEYSC
	// region at offset kbReg return kbVal (kbReg<0 => all offsets return kbVal).
	kbStart, kbEnd uint64
	kbReg          int32
	kbVal          uint32
}

func NewMMIOBus() *MMIOBus {
	b := &MMIOBus{unknown: map[uint32]int{}}
	b.periphIRQ = &periphIRQ{base: newBase("PERIPH_IRQ", 0xA4610000, 0x1000)}
	b.etmu2 = &etmuCounter{base: newBase("ETMU2", 0xA44D0000, 0x1000)}
	b.etmu2.bus = b
	b.regions = []region{
		&cpg{base: newBase("CPG", 0xA4150000, 0x1000)},
		&base{nm: "PFC", bs: 0xA4050000, sz: 0x1000, regs: map[uint32]uint32{}},
		&base{nm: "WDT", bs: 0xA4520000, sz: 0x1000, regs: map[uint32]uint32{}},
		&keysc{base: newBase("KEYSC", 0xA4080000, 0x1000)},
		&base{nm: "TMU", bs: 0xA4490000, sz: 0x1000, regs: map[uint32]uint32{}},
		&etmu{base: newBase("ETMU", 0xA44A0000, 0x1000)},
		b.etmu2,
		b.periphIRQ,
		&freeCounter{base: newBase("FRC", 0xA4130000, 0x10000)},
		&intx{base: newBase("INTX", 0xA4140000, 0x1000)},
		&keysc{base: newBase("KIU_DATA", 0xA44B0000, 0x1000)},
		&base{nm: "BSC", bs: 0xFEC10000, sz: 0x1000, regs: map[uint32]uint32{}},
		&dmac{base: newBase("DMAC", 0xFE008000, 0x1000)},
		&ccn{base: newBase("CCN", 0xFF000000, 0x1000)},
		&base{nm: "LCD_R61524", bs: 0xB4000000, sz: 0x20000, regs: map[uint32]uint32{}},
	}
	return b
}

func (b *MMIOBus) find(va uint32) region {
	cands := [3]uint32{va, (va & 0x1FFFFFFF) | 0xA0000000, va & 0x1FFFFFFF}
	for _, c := range cands {
		for _, r := range b.regions {
			if r.contains(c) {
				return r
			}
		}
	}
	return nil
}

// findHit returns the region and the candidate address that matched.
func (b *MMIOBus) findHit(va uint32) (region, uint32) {
	cands := [3]uint32{va, (va & 0x1FFFFFFF) | 0xA0000000, va & 0x1FFFFFFF}
	for _, c := range cands {
		for _, r := range b.regions {
			if r.contains(c) {
				return r, c
			}
		}
	}
	return nil, va
}

func (b *MMIOBus) Read(va, size uint32) uint32 {
	if b.watchBase != 0 && (va&^0xFFF) == b.watchBase && b.cpu != nil {
		b.watchPC[b.cpu.pc]++
	}
	if b.kbEnd != 0 && (va&^0xFFF) == 0xA4080000 && b.cpu != nil &&
		b.cpu.cycles >= b.kbStart && b.cpu.cycles < b.kbEnd {
		if b.kbReg < 0 || uint32(b.kbReg) == (va-0xA4080000)&0xFFFF {
			return b.kbVal
		}
	}
	r, hit := b.findHit(va)
	if r == nil {
		b.unknown[va]++
		return 0
	}
	return r.read(hit, size)
}

func (b *MMIOBus) Write(va, size, val uint32) {
	r, hit := b.findHit(va)
	if r == nil {
		b.unknown[va]++
		return
	}
	r.write(hit, size, val)
}

// FrameSAR returns the source address of any DMAC channel currently programmed to
// push to the LCD area-5 (DAR==0x14000000), i.e. the live framebuffer, if any.
func (b *MMIOBus) FrameSAR() (uint32, bool) {
	for _, r := range b.regions {
		if d, ok := r.(*dmac); ok {
			for _, choff := range []uint32{0, 0x10, 0x20, 0x30} {
				if d.regs[choff+4] == 0x14000000 {
					return d.regs[choff], true
				}
			}
		}
	}
	return 0, false
}

// tick: cycle-driven timer. Every timerPeriod instructions set the PERIPH_IRQ flag
// and request INTEVT 0x560. Gated by cpu.BL/IMASK in accept, so safe to free-run.
func (b *MMIOBus) tick(cpu *CPU) {
	if b.timerPeriod == 0 {
		return
	}
	if cpu.cycles >= b.timerNext {
		b.timerNext = cpu.cycles + b.timerPeriod
		b.timerTicks++
		b.periphIRQ.setTimerFlag()
		cpu.raiseIRQ(TimerINTEVT, TimerLevel)
	}
}
