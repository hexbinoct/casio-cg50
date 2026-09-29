package main

// intc.go — SH7305 interrupt controller (0xA4080000): priority registers IPRA..IPRL at
// +0x00..+0x2C (16-bit, four 4-bit fields each), mask registers IMR0..IMR12 at +0x80..+0xB0
// (8-bit, 1 = masked, writes set bits) and mask-clear registers at +0xC0..+0xF0 (writes clear
// bits). Reads return the programmed values (they used to read 0, which made the OS's
// read-modify-writes store only the last field written).
//
// A source is requested only while its priority field is nonzero and its mask bit is clear.
// That is what lets an add-in kernel (gint) take the machine: it zeroes every priority and
// masks everything, then enables only its own timers/DMA, so the OS's tick, KEYSC and RTC
// interrupts stop reaching its vector table; on the world switch back it restores what it
// read. Field positions: gint src/intc/intc.c for the timers/DMA/RTC, and the OS's own
// reprogramming seen in the emulator (probe TestDumpINTC, 2026-09-28) for its tick (0x560:
// IPRB[15:12], IMR4 bit3, priority 12) and KEYSC (0xBE0: IPRF[15:12], IMR5 bit7, priority 13).
//
// Levels: the OS's sources keep the levels the emulator always used (TimerLevel, KeyscLevel,
// RTCLevel); sources the OS never uses (TMU, ETMU, DMAC) take the priority field as level.

type intcSrc struct {
	ipr, shift int
	imr        int
	bit        uint8
}

var intcSources = map[uint32]intcSrc{
	0x400: {0, 12, 4, 0x10}, 0x420: {0, 8, 4, 0x20}, 0x440: {0, 4, 4, 0x40}, // TMU0-2
	0x9e0: {9, 12, 6, 0x08}, 0xc20: {6, 8, 5, 0x02}, 0xc40: {6, 4, 5, 0x04}, // ETMU0-2
	0x900: {4, 4, 2, 0x01}, 0xd00: {8, 12, 6, 0x10}, 0xfa0: {11, 12, 8, 0x02}, // ETMU3-5
	0x800: {4, 12, 1, 0x01}, 0x820: {4, 12, 1, 0x02}, 0x840: {4, 12, 1, 0x04}, 0x860: {4, 12, 1, 0x08}, // DMAC DEI0-3
	0xb80: {5, 8, 5, 0x10}, 0xba0: {5, 8, 5, 0x20}, // DMAC DEI4-5
	0xaa0: {10, 12, 10, 0x02}, // RTC periodic
	0xbe0: {5, 12, 5, 0x80},   // KEYSC
	0x560: {1, 12, 4, 0x08},   // the OS's tick unit (0xA4610000)
}

type intcUnit struct {
	base
	ipr [12]uint16
	imr [13]uint8
}

func newINTC() *intcUnit {
	k := &intcUnit{base: newBase("INTC", 0xA4080000, 0x1000)}
	k.seedOSDefaults() // a cold boot zeroes and reprograms everything itself
	return k
}

func (k *intcUnit) read(va, size uint32) uint32 {
	off := va - k.bs
	switch {
	case off < 0x30 && off&3 == 0:
		return uint32(k.ipr[off/4])
	case off >= 0x80 && off < 0xB4 && off&3 == 0:
		return uint32(k.imr[(off-0x80)/4])
	case off >= 0xC0 && off < 0xF4:
		return 0
	}
	return k.regs[off]
}

func (k *intcUnit) write(va, size, val uint32) {
	off := va - k.bs
	switch {
	case off < 0x30 && off&3 == 0:
		k.ipr[off/4] = uint16(val)
	case off >= 0x80 && off < 0xB4 && off&3 == 0:
		k.imr[(off-0x80)/4] |= uint8(val)
	case off >= 0xC0 && off < 0xF4 && off&3 == 0:
		k.imr[(off-0xC0)/4] &^= uint8(val)
	default:
		k.regs[off] = val
	}
}

// enabled reports whether the INTC lets a source through, and its priority field.
func (k *intcUnit) enabled(intevt uint32) (bool, uint32) {
	s, ok := intcSources[intevt]
	if !ok {
		return true, 0
	}
	prio := uint32(k.ipr[s.ipr]>>s.shift) & 0xF
	if prio == 0 || k.imr[s.imr]&s.bit != 0 {
		return false, prio
	}
	return true, prio
}

// gate reports a source's current INTC state: its priority field and whether its mask bit is
// set. Sources the INTC model does not know are never gated (known = false).
func (k *intcUnit) gate(intevt uint32) (prio uint32, masked, known bool) {
	s, ok := intcSources[intevt]
	if !ok {
		return 0, false, false
	}
	return uint32(k.ipr[s.ipr]>>s.shift) & 0xF, k.imr[s.imr]&s.bit != 0, true
}

// seedOSDefaults gives a snapshot without INTC state the fields the OS had programmed for
// the sources it uses (it re-programs the tick's and KEYSC's on every service, but the first
// request after a resume needs them set): tick 12, KEYSC 13, RTC 8 (IPRK = 0x8000 and
// MSKCLR10 = 0x03 seen at 5.3M instructions of a cold boot), nothing masked.
func (k *intcUnit) seedOSDefaults() {
	k.ipr[1] |= 0xC000
	k.ipr[5] |= 0xD000
	k.ipr[10] |= 0x8000
	k.imr[4] &^= 0x08
	k.imr[5] &^= 0x80
	k.imr[10] &^= 0x02
}

func (k *intcUnit) stateMap() map[uint32]uint32 {
	m := make(map[uint32]uint32, 12+13+len(k.regs))
	for off, v := range k.regs {
		m[off] = v
	}
	for i, v := range k.ipr {
		m[uint32(i*4)] = uint32(v)
	}
	for i, v := range k.imr {
		m[0x80+uint32(i*4)] = uint32(v)
	}
	return m
}

func (k *intcUnit) restoreState(m map[uint32]uint32) {
	for off := range k.regs {
		delete(k.regs, off)
	}
	k.ipr, k.imr = [12]uint16{}, [13]uint8{}
	seen := false
	for off, v := range m {
		switch {
		case off < 0x30 && off&3 == 0:
			k.ipr[off/4] = uint16(v)
			seen = true
		case off >= 0x80 && off < 0xB4 && off&3 == 0:
			k.imr[(off-0x80)/4] = uint8(v)
		default:
			k.regs[off] = v
		}
	}
	if !seen {
		k.seedOSDefaults() // legacy snapshot: INTC was a stub when it was taken
	}
}

// raise requests an interrupt through the INTC gate. level 0 = use the priority field.
func (b *MMIOBus) raise(cpu *CPU, intevt, level uint32) {
	ok, prio := b.intc.enabled(intevt)
	if !ok {
		return
	}
	if level == 0 {
		level = prio
	}
	if level == 0 {
		level = 8
	}
	cpu.raiseIRQ(intevt, level)
}
