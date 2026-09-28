package main

// SH7305 key-scan unit (Casio "KIU", SH7724-style KEYSC) at 0xA44B0000 — the real path a
// keypress takes into the OS. Reverse-engineered (cont.18l) from the 3.60 keyboard ISR
// FUN_801e4c00 (INTEVT 0xBE0 via the dispatcher table at 0xFD8010C8), the driver init
// FUN_801e51c8, the synchronous scan FUN_801e5700 and the matrix decoder FUN_801e49bc.
// Mirrored 1:1 by emu/mmio.py KeyScan (the oracle) — keep both identical.
//
// Register map (16-bit accesses):
//   +0x00..+0x0B  six key-data words. Word w: low byte = matrix column 2w, high byte =
//                 column 2w+1; bit n of a byte = row n. (row,col) are the 0-based grid of
//                 re/KEYMAP.md (`inject` column), so AC/ON (0,0) = word0 bit0, DOWN (2,7) =
//                 word3 bit10, UP (1,8) = word4 bit1. The ISR maps (row,col) through the OS
//                 table 0x8068fa70[col*8+row] = (row+1)<<8|(col+1) = the 1-based grid code.
//   +0x0C  control: bit15 = unit enable (OS writes 0x8000 at init).
//   +0x0E, +0x16, +0x18(=0xC8), +0x1A(=0x0FFF), +0x1C(=0xFF)  scan timing/config (opaque).
//   +0x10  mode: 0x200 = normal (auto key-detect, then auto-scan while keys are held),
//                0x400 = detect only, 0x800 = scan now (sync scan), 0 = off.
//   +0x12  bit0 = scan busy (we are never busy).
//   +0x14  [15:8] interrupt enable per flag, [7:0] status flags, write-1-to-clear:
//                 bit3 = key detected (matrix went non-empty), bit1 = scan complete.
//          The ISR arms 0x48 (flags 3,6) while idle and 0x76 (1,2,4,5,6) while a key is
//          held/just released, and writes back the flags it read to acknowledge them.
//   IRQ: INTEVT 0xBE0 (INTC IPRF[15:12] = 13) whenever (flags & enable) != 0.
//
// Behavioural model: every scanPeriod cycles the unit "scans". If enabled and in a mode
// other than off, a press edge (matrix empty -> non-empty) sets the detect flag; while any
// key is held (and for 2 scans after the last release, so the OS sees the empty matrix and
// its state machine returns to idle) each scan sets the scan-complete flag. Mode 0x800 sets
// scan-complete every period unconditionally. Key repeat is the OS's own logic (initial
// delay 120 scans, then every 5 scans), so scanPeriod is also the repeat clock.

const (
	KeyscINTEVT = 0xBE0
	KeyscLevel  = 13

	keyscFlagScan   = 0x02
	keyscFlagDetect = 0x08

	// DefaultKeyScanPeriod: instructions between hardware scans. The OS's own repeat logic
	// (ISR FUN_801e4c00) starts repeating an arrow after 20 scans (*0x8c08ba38) and then posts
	// one repeat PER SCAN (*0x8c08ba3c = 0 at the menu), so the scan period is the repeat
	// clock. MEASURED on the real fx-CG50 (tools/keyprobe, 2026-09-27): 132 scan-complete
	// flags in 4 s with RIGHT held = 30.3 ms per scan (33 Hz), and NO scans while idle. So a
	// held arrow repeats after ~0.6 s and then 33/s. Time here is instruction-based, so the
	// value depends on host throughput (750k ≈ 30 ms at ~25M instr/s); hosts set it from
	// their actual rate via Emulator.SetKeyScanPeriod.
	DefaultKeyScanPeriod = 750_000
	KeyScanHz            = 33 // measured hardware scan rate while a key is held
)

type keyscUnit struct {
	base
	held       [6]uint16 // live matrix: word = col>>1, bit = row + 8*(col&1)
	bus        *MMIOBus  // interrupt gate (nil in unit tests that build the unit alone)
	ctrl, mode uint32
	ie, flags  uint32
	scanPeriod uint64
	scanNext   uint64
	scanLeft   int  // scans still to report after the matrix went empty
	wasHeld    bool // matrix state at the previous scan (press-edge detection)
}

func newKeysc() *keyscUnit {
	return &keyscUnit{base: newBase("KEYSC", 0xA44B0000, 0x1000), scanPeriod: DefaultKeyScanPeriod}
}

func (k *keyscUnit) anyHeld() bool {
	for _, w := range k.held {
		if w != 0 {
			return true
		}
	}
	return false
}

// press / release change the physical matrix (0-based row 0..7, col 0..11). Key-detect is
// edge-triggered on the real unit (it wakes the scanner), so a press into an empty matrix
// makes the next tick scan immediately instead of waiting out the scan period.
func (k *keyscUnit) press(row, col uint32) {
	if row < 8 && col < 12 {
		if !k.anyHeld() {
			k.scanNext = 0
		}
		k.held[col>>1] |= 1 << (row + 8*(col&1))
	}
}

func (k *keyscUnit) release(row, col uint32) {
	if row < 8 && col < 12 {
		k.held[col>>1] &^= 1 << (row + 8*(col&1))
	}
}

func (k *keyscUnit) releaseAll() { k.held = [6]uint16{} }

// resumeDefaults restores the post-init configuration the OS programmed long before a
// save-state was taken (peripheral registers are not part of the snapshot): unit enabled,
// normal mode, key-detect interrupt armed. A cold boot writes the same values itself.
func (k *keyscUnit) resumeDefaults() {
	k.ctrl, k.mode, k.ie, k.flags = 0x8000, 0x200, 0x48, 0
	k.scanLeft, k.wasHeld = 0, false
}

func (k *keyscUnit) read(va, size uint32) uint32 {
	off := va - k.bs
	switch {
	case off < 0x0C:
		w := uint32(k.held[off>>1])
		switch size {
		case 2:
			return w
		case 1:
			if off&1 == 0 {
				return w >> 8
			}
			return w & 0xFF
		}
		var lo uint32
		if (off>>1)+1 < 6 {
			lo = uint32(k.held[(off>>1)+1])
		}
		return w<<16 | lo
	case off == 0x0C:
		return k.ctrl
	case off == 0x10:
		return k.mode
	case off == 0x12:
		return 0
	case off == 0x14:
		return k.ie<<8 | k.flags
	}
	return k.regs[off]
}

func (k *keyscUnit) write(va, size, val uint32) {
	off := va - k.bs
	switch off {
	case 0x0C:
		k.ctrl = val & 0xFFFF
	case 0x10:
		k.mode = val & 0xFFFF
	case 0x14:
		k.ie = (val >> 8) & 0xFF
		k.flags &^= val & 0xFF
	default:
		k.regs[off] = val
	}
}

// tick is called every instruction by the bus; it does real work once per scanPeriod.
func (k *keyscUnit) tick(cpu *CPU) {
	if cpu.cycles < k.scanNext {
		return
	}
	k.scanNext = cpu.cycles + k.scanPeriod
	held := k.anyHeld()
	if k.ctrl&0x8000 == 0 || k.mode == 0 {
		k.wasHeld = held
		return
	}
	if held && !k.wasHeld {
		k.flags |= keyscFlagDetect
	}
	if held {
		k.scanLeft = 2
	}
	if k.mode&0x800 != 0 || (k.mode&0x200 != 0 && (held || k.scanLeft > 0)) {
		k.flags |= keyscFlagScan
		if !held && k.scanLeft > 0 {
			k.scanLeft--
		}
	}
	k.wasHeld = held
	if k.flags&k.ie != 0 {
		if k.bus != nil {
			k.bus.raise(cpu, KeyscINTEVT, KeyscLevel)
		} else {
			cpu.raiseIRQ(KeyscINTEVT, KeyscLevel)
		}
	}
}

// keyTapper turns queued host "taps" into press/release edges on the KEYSC matrix, paced so
// the OS ISR observes each key: held for tapHoldScans scan periods, then a gap of
// tapGapScans before the next queued key (the release and the ISR's return to idle happen
// in that gap). Keys the host holds down itself (KeyDown/KeyUp) bypass this.
type keyTapper struct {
	queue     [][2]uint32
	active    bool
	key       [2]uint32
	releaseAt uint64
	nextAt    uint64
}

const (
	tapHoldScans = 3
	tapGapScans  = 3
)

func (t *keyTapper) drive(k *keyscUnit, now uint64) {
	if t.active {
		if now >= t.releaseAt {
			k.release(t.key[0], t.key[1])
			t.active = false
			t.nextAt = now + tapGapScans*k.scanPeriod
		}
		return
	}
	if len(t.queue) == 0 || now < t.nextAt {
		return
	}
	t.key, t.queue = t.queue[0], t.queue[1:]
	k.press(t.key[0], t.key[1])
	t.active = true
	t.releaseAt = now + tapHoldScans*k.scanPeriod
}
