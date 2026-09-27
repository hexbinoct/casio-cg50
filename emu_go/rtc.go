package main

// SH7305 RTC at 0xA413FEC0 (SH7724-compatible layout) — the OS's coarse time base.
// RE'd cont.18m: the OS idle path (0x802ae742) sets RCR2.PES = 1/2 s right before `sleep`
// and clears it on wake; the periodic interrupt (INTEVT 0xAA0 -> 0x801dfc6c) clears
// RCR2[7:4] and posts main-loop event 0x80. That 2 Hz wake-up is the cursor blink and the
// idle heartbeat; without it the editor never blinks and nothing time-based in the shell runs.
//
//	+0x00 R64CNT   bits 6..0 = 1 Hz .. 64 Hz counter (bit0 toggles at 128 Hz)
//	+0x02 RSECCNT  +0x04 RMINCNT  +0x06 RHRCNT  +0x08 RWKCNT  +0x0A RDAYCNT  +0x0C RMONCNT
//	+0x0E RYRCNT (16-bit)   all BCD
//	+0x1C RCR1  (CF bit7, CIE bit4, AIE bit3, AF bit0)
//	+0x1E RCR2  (PEF bit7, PES bits6..4, RTCEN bit3, ADJ bit2, RESET bit1, START bit0)
//	  PES: 1=1/256 s, 2=1/64 s, 3=1/16 s, 4=1/4 s, 5=1/2 s, 6=1 s, 7=2 s.  Each period: PEF
//	  set + INTEVT 0xAA0 (level 9). PEF/PES are cleared by the OS writing RCR2 back.
//
// Time is instruction-based like everything else: instrPerSec (host-supplied, see
// MMIOBus.SetInstrPerSecond) converts cpu.cycles to seconds. The calendar starts at a fixed
// epoch (2010-01-01 00:00:00, a Friday — the calc's own clock reads 2010) for deterministic
// goldens; hosts may SetClock() to real time.

const (
	RTCBase     = 0xA413FEC0
	RTCINTEVT   = 0xAA0
	RTCLevel    = 9
	rtcEpochDOW = 5 // 2010-01-01 was a Friday (0 = Sunday)
)

type rtc struct {
	base
	bus          *MMIOBus
	rcr1, rcr2   uint32
	epochSec     uint64 // seconds of calendar time at cycle 0 (from 2010-01-01 00:00:00)
	nextPeriodic uint64 // cycle of the next periodic event (valid while PES != 0)
}

func newRTC(bus *MMIOBus) *rtc { return &rtc{base: newBase("RTC", RTCBase, 0x40), bus: bus} }

func (r *rtc) instrPerSec() uint64 {
	if r.bus == nil || r.bus.instrPerSec == 0 {
		return 70_000_000
	}
	return r.bus.instrPerSec
}

func (r *rtc) cycles() uint64 {
	if r.bus == nil || r.bus.cpu == nil {
		return 0
	}
	return r.bus.cpu.cycles
}

// SetClock sets the calendar to unix seconds (applies at the current cycle).
func (r *rtc) SetClock(unix int64) {
	const epoch2010 = 1262304000 // 2010-01-01T00:00:00Z
	s := unix - epoch2010
	if s < 0 {
		s = 0
	}
	r.epochSec = uint64(s) - r.cycles()/r.instrPerSec()
}

func bcd(v uint64) uint32 { return uint32((v/10)<<4 | v%10) }

// periodHz returns the periodic-interrupt period in instructions for the PES field, 0 = off.
func (r *rtc) periodInstr() uint64 {
	ips := r.instrPerSec()
	switch (r.rcr2 >> 4) & 7 {
	case 1:
		return ips / 256
	case 2:
		return ips / 64
	case 3:
		return ips / 16
	case 4:
		return ips / 4
	case 5:
		return ips / 2
	case 6:
		return ips
	case 7:
		return ips * 2
	}
	return 0
}

func (r *rtc) read(va, size uint32) uint32 {
	off := va - r.bs
	cyc := r.cycles()
	ips := r.instrPerSec()
	sec := r.epochSec + cyc/ips
	switch off {
	case 0x00: // R64CNT
		sub := (cyc % ips) * 128 / ips
		return uint32(sub) & 0x7F
	case 0x02:
		return bcd(sec % 60)
	case 0x04:
		return bcd(sec / 60 % 60)
	case 0x06:
		return bcd(sec / 3600 % 24)
	case 0x08:
		return uint32((sec/86400 + rtcEpochDOW) % 7)
	case 0x0A, 0x0C, 0x0E:
		// day/month/year from days since 2010-01-01 (Gregorian)
		days := sec / 86400
		y := uint64(2010)
		for {
			ylen := uint64(365)
			if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
				ylen = 366
			}
			if days < ylen {
				break
			}
			days -= ylen
			y++
		}
		mlen := [12]uint64{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			mlen[1] = 29
		}
		m := uint64(0)
		for m < 11 && days >= mlen[m] {
			days -= mlen[m]
			m++
		}
		switch off {
		case 0x0A:
			return bcd(days + 1)
		case 0x0C:
			return bcd(m + 1)
		default:
			return bcd(y/100)<<8 | bcd(y%100)
		}
	case 0x1C:
		return r.rcr1
	case 0x1E:
		return r.rcr2
	}
	return r.regs[off]
}

func (r *rtc) write(va, size, val uint32) {
	off := va - r.bs
	switch off {
	case 0x1C:
		r.rcr1 = val & 0xFF
	case 0x1E:
		prevPES := (r.rcr2 >> 4) & 7
		r.rcr2 = val & 0xFF
		if pes := (r.rcr2 >> 4) & 7; pes != 0 && (pes != prevPES || r.nextPeriodic == 0) {
			r.alignPeriodic()
		}
	default:
		r.regs[off] = val
	}
}

// alignPeriodic schedules the next event on the RTC clock's own phase (the hardware derives
// the periodic event from its free-running 64 Hz divider chain, so re-arming PES does not
// restart the period — the OS re-arms it on every idle iteration).
func (r *rtc) alignPeriodic() {
	p := r.periodInstr()
	if p == 0 {
		r.nextPeriodic = 0
		return
	}
	r.nextPeriodic = (r.cycles()/p + 1) * p
}

// tick: raise the periodic interrupt while PES is programmed. Called from MMIOBus.tick.
func (r *rtc) tick(cpu *CPU) {
	if r.rcr2&0x70 == 0 {
		return
	}
	if cpu.cycles >= r.nextPeriodic {
		r.nextPeriodic += r.periodInstr()
		r.rcr2 |= 0x80 // PEF
		cpu.raiseIRQ(RTCINTEVT, RTCLevel)
	}
}
