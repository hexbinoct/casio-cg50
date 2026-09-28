package main

// gtimer.go — the SH7305 timer units as an add-in kernel (gint) drives them (2026-09-28,
// cg50_addons/dasm). The OS never needed them modelled: it keeps its own tick on the
// PERIPH_IRQ unit and reads ETMU5's counter (0xA44D00D8) as a free-running 32.768 kHz
// reference. gint's driver init writes TSTR/TCOR/TCNT/TCR of every timer and spins until
// each write reads back, then runs the keyboard scanner (128 Hz) and sleeps from ETMU
// underflow interrupts; its display driver waits for the DMAC transfer-end interrupt.
//
//   ETMU (0xA44D0000): 6 units at +0x30 + 0x20*i: TSTR +0 (8-bit, bit0), TCOR +4, TCNT +8,
//        TCR +0xC (8-bit: UNF bit1, UNIE bit0). 32.768 kHz. INTEVT 0x9e0 0xc20 0xc40 0x900
//        0xd00 0xfa0. +0xD8 (and its alias +0xC8) keep the legacy free-counter view of
//        ETMU5 until software programs that unit (the boot golden was frozen with it).
//   TMU  (0xA4490000): TSTR +0x04 (bits 0..2); channel i: TCOR +0x08+0xC*i, TCNT +0x0C+0xC*i,
//        TCR +0x10+0xC*i (16-bit: TPSC bits0-2 = Pphi/4,16,64,256; UNIE bit5; UNF bit8).
//        INTEVT 0x400 0x420 0x440. Pphi = 29.4912 MHz (117.96 MHz CPU / 4).
//   DMAC: a channel started with CHCR.IE (bit2) raises INTEVT 0x800+0x20*ch when the (instant)
//        transfer completes. The OS starts its VRAM push with CHCR 0x00101400 (IE clear).
//
// Time base: instructions map to seconds through MMIOBus.instrPerSec, like the RTC/KEYSC.
// Counters are computed lazily from the cycle they were started; underflows are scheduled
// (Emulator.nextDue) so the step loop's slow path runs exactly when one is due.

import "math"

const (
	GTimerLevel = 8
	TMUPphi     = 29_491_200
	tcrUNF      = 0x2  // ETMU TCR bit1 (gint mpu/tmu.h: UNF bit1, UNIE bit0)
	tcrUNIE     = 0x1  // ETMU TCR bit0
	tmuUNF      = 0x100
	tmuUNIE     = 0x20
)

var (
	etmuINTEVT = [6]uint32{0x9e0, 0xc20, 0xc40, 0x900, 0xd00, 0xfa0}
	tmuINTEVT  = [3]uint32{0x400, 0x420, 0x440}
	dmacINTEVT = [6]uint32{0x800, 0x820, 0x840, 0x860, 0xb80, 0xba0}
)

// timerChan is one down-counter: TCNT counts from startCnt at startCyc, reloading TCOR.
type timerChan struct {
	tcor, tcnt, tcr uint32
	running         bool
	startCyc        uint64
	startCnt        uint32
	nextUnf         uint64
	owned           bool // software has programmed this channel
}

func (c *timerChan) cur(cyc, div uint64) uint32 {
	if !c.running || cyc < c.startCyc {
		return c.tcnt
	}
	elapsed := (cyc - c.startCyc) / div
	if elapsed <= uint64(c.startCnt) {
		return c.startCnt - uint32(elapsed)
	}
	period := uint64(c.tcor) + 1
	rem := (elapsed - uint64(c.startCnt) - 1) % period
	return c.tcor - uint32(rem)
}

// restart (re)bases the countdown on the current value at cycle cyc.
func (c *timerChan) restart(cyc, div uint64) {
	c.tcnt = c.cur(cyc, div)
	c.startCyc, c.startCnt = cyc, c.tcnt
	c.nextUnf = cyc + (uint64(c.tcnt)+1)*div
}

func (c *timerChan) setRunning(on bool, cyc, div uint64) {
	if on && !c.running {
		c.running = true
		c.startCyc, c.startCnt = cyc, c.tcnt
		c.nextUnf = cyc + (uint64(c.tcnt)+1)*div
	} else if !on && c.running {
		c.tcnt = c.cur(cyc, div)
		c.running = false
	}
}

// service sets UNF when an underflow is due and reschedules; true if one happened.
func (c *timerChan) service(cyc, div uint64, unf uint32) bool {
	if !c.running || cyc < c.nextUnf {
		return false
	}
	period := (uint64(c.tcor) + 1) * div
	if period == 0 {
		period = div
	}
	for c.nextUnf <= cyc {
		c.nextUnf += period
	}
	c.tcr |= unf
	return true
}

func (c *timerChan) next() uint64 {
	if !c.running {
		return math.MaxUint64
	}
	return c.nextUnf
}

// writeTCR applies "write 0 to clear" to the underflow flag and stores the rest.
func (c *timerChan) writeTCR(val, unf uint32) {
	c.tcr = (val &^ unf) | (c.tcr & val & unf)
}

// ---- ETMU (keeps the type name the bus, save-states and SetInstrPerSecond use) ----

type etmuCounter struct {
	base
	bus *MMIOBus
	// countDiv: instructions per 32.768 kHz tick (instrPerSec/32768). 0 keeps the
	// pre-2026-09-27 legacy view of +0xD8 (every 4 instr, 24-bit) the boot golden was
	// frozen with, and clocks modelled channels every 4 instructions.
	countDiv uint64
	ch       [6]timerChan
}

func (e *etmuCounter) div() uint64 {
	if e.countDiv == 0 {
		return 4
	}
	return e.countDiv
}

func (e *etmuCounter) cycles() uint64 {
	if e.bus != nil && e.bus.cpu != nil {
		return e.bus.cpu.cycles
	}
	return 0
}

func (e *etmuCounter) legacy() uint32 {
	cyc := e.cycles()
	if e.countDiv == 0 {
		return uint32(-(cyc >> 2)) & 0xFFFFFF
	}
	return uint32(-(cyc / e.countDiv))
}

func (e *etmuCounter) read(va, size uint32) uint32 {
	off := (va - e.bs) & 0xFFFF
	if (off == 0xD8 || off == 0xC8) && !e.ch[5].owned {
		return e.legacy()
	}
	if off == 0xC8 {
		return e.ch[5].cur(e.cycles(), e.div())
	}
	if off >= 0x30 && off < 0xF0 {
		i := (off - 0x30) / 0x20
		c := &e.ch[i]
		switch (off - 0x30) % 0x20 {
		case 0:
			if c.running {
				return 1
			}
			return 0
		case 4:
			return c.tcor
		case 8:
			return c.cur(e.cycles(), e.div())
		case 0xC:
			return c.tcr
		}
	}
	return e.regs[off]
}

func (e *etmuCounter) write(va, size, val uint32) {
	off := (va - e.bs) & 0xFFFF
	e.regs[off] = val
	if off < 0x30 || off >= 0xF0 {
		return
	}
	i := (off - 0x30) / 0x20
	c := &e.ch[i]
	cyc, div := e.cycles(), e.div()
	switch (off - 0x30) % 0x20 {
	case 0:
		c.owned = true
		c.setRunning(val&1 != 0, cyc, div)
	case 4:
		c.owned = true
		c.tcor = val
		if c.running {
			c.restart(cyc, div)
		}
	case 8:
		c.owned = true
		c.tcnt = val
		if c.running {
			c.startCyc, c.startCnt = cyc, val
			c.nextUnf = cyc + (uint64(val)+1)*div
		}
	case 0xC:
		c.owned = true
		c.writeTCR(val&0xFF, tcrUNF)
	}
}

func (e *etmuCounter) tick(cpu *CPU) {
	div := e.div()
	for i := range e.ch {
		if e.ch[i].service(cpu.cycles, div, tcrUNF) && e.ch[i].tcr&tcrUNIE != 0 {
			e.bus.raise(cpu, etmuINTEVT[i], 0)
		}
	}
}

func (e *etmuCounter) next() uint64 {
	d := uint64(math.MaxUint64)
	for i := range e.ch {
		d = min(d, e.ch[i].next())
	}
	return d
}

// ---- TMU ----

type tmu struct {
	base
	bus *MMIOBus
	ch  [3]timerChan
}

func newTMU(bus *MMIOBus) *tmu {
	return &tmu{base: newBase("TMU", 0xA4490000, 0x1000), bus: bus}
}

func (t *tmu) cycles() uint64 {
	if t.bus != nil && t.bus.cpu != nil {
		return t.bus.cpu.cycles
	}
	return 0
}

// div: instructions per count for channel i at its prescaler.
func (t *tmu) div(i int) uint64 {
	rate := uint64(TMUPphi/4) >> (2 * (t.ch[i].tcr & 7))
	if rate == 0 {
		rate = 1
	}
	ips := uint64(70_000_000)
	if t.bus != nil && t.bus.instrPerSec != 0 {
		ips = t.bus.instrPerSec
	}
	d := ips / rate
	if d == 0 {
		d = 1
	}
	return d
}

func (t *tmu) read(va, size uint32) uint32 {
	off := va - t.bs
	if off == 0x04 {
		var v uint32
		for i := range t.ch {
			if t.ch[i].running {
				v |= 1 << i
			}
		}
		return v
	}
	if off >= 0x08 && off < 0x08+3*0xC {
		i := int((off - 0x08) / 0xC)
		c := &t.ch[i]
		switch (off - 0x08) % 0xC {
		case 0:
			return c.tcor
		case 4:
			return c.cur(t.cycles(), t.div(i))
		case 8:
			return c.tcr
		}
	}
	return t.regs[off]
}

func (t *tmu) write(va, size, val uint32) {
	off := va - t.bs
	t.regs[off] = val
	cyc := t.cycles()
	if off == 0x04 {
		for i := range t.ch {
			t.ch[i].setRunning(val&(1<<i) != 0, cyc, t.div(i))
		}
		return
	}
	if off < 0x08 || off >= 0x08+3*0xC {
		return
	}
	i := int((off - 0x08) / 0xC)
	c := &t.ch[i]
	c.owned = true
	switch (off - 0x08) % 0xC {
	case 0:
		c.tcor = val
		if c.running {
			c.restart(cyc, t.div(i))
		}
	case 4:
		c.tcnt = val
		if c.running {
			c.startCyc, c.startCnt = cyc, val
			c.nextUnf = cyc + (uint64(val)+1)*t.div(i)
		}
	case 8:
		was := c.tcr & 7
		c.writeTCR(val&0xFFFF, tmuUNF)
		if c.running && c.tcr&7 != was {
			c.restart(cyc, t.div(i))
		}
	}
}

func (t *tmu) tick(cpu *CPU) {
	for i := range t.ch {
		if t.ch[i].service(cpu.cycles, t.div(i), tmuUNF) && t.ch[i].tcr&tmuUNIE != 0 {
			t.bus.raise(cpu, tmuINTEVT[i], 0)
		}
	}
}

func (t *tmu) next() uint64 {
	d := uint64(math.MaxUint64)
	for i := range t.ch {
		d = min(d, t.ch[i].next())
	}
	return d
}
