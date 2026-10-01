package main

// R61524 LCD controller on bus area 5 (0xB4000000; P0 alias 0x14000000) — cont.18o.
//
// The screen the user sees is the controller's GRAM, not VRAM. The OS reaches GRAM two ways:
//   - the frame push: set up a window + address, select R202 (GRAM write), then DMAC ch2
//     streams the 384x216 VRAM into the data port (dmac.write -> lcd.dma);
//   - direct CPU pixel writes: the text-cursor blink (Keyboard_CursorFlash sc 0x8CA ->
//     0x800c4006 draw / 0x800c4266 erase) writes the caret straight to GRAM and never
//     touches VRAM or the DMA. Presenting VRAM snapshots therefore could never show it.
//
// Protocol (all accesses are 16-bit at +0): the register-select line RS is PFC port
// 0xA405013C bit4 — the OS clears it, writes the register index, sets it, then reads or
// writes data (helper 0x8004e272). Register reads matter to the OS: setORG 0x8004e3e8 does
// read-modify-write of R003 (entry mode) and getORG 0x8004e440 reads R003 bit7 to choose
// between window addressing (ORG=1, the real path: R210-R213 window + R200/R201 = 0,0) and
// absolute addressing. Boot programs R003=0x00A0 (ORG=1, I/D1=1 V-increment, I/D0=0
// H-decrement, AM=0 horizontal) and the window to the full 396x224 panel.
//
// The OS's 384x216 area sits at panel H = 389 - x (H address = 0x18B - (x+6)), V = y.
//
// CPU-visible behaviour (index, register read-back) is mirrored in emu/mmio.py LCD; GRAM,
// the address counter and DMA streaming are presentation-only (GRAM reads return 0 in both).

const (
	panelW = 396
	panelH = 224
	lcdReg = 0x800 // register index space (R000..R7FF)

	// OS drawing area -> panel address: H = osOriginH - x, V = y.
	osOriginH = 389
)

type lcd struct {
	base
	pfc  *base // RS = PFC 0xA405013C bit4
	idx  uint32
	reg  [lcdReg]uint16
	h, v uint32 // GRAM address counter
	gram []uint16
	gen  uint64 // bumped on every GRAM write: a host skips redrawing an unchanged frame
}

func newLCD(pfc *base) *lcd {
	l := &lcd{base: newBase("LCD_R61524", 0xB4000000, 0x20000), pfc: pfc, gram: make([]uint16, panelW*panelH)}
	l.bootDefaults()
	return l
}

// bootDefaults are the register values the OS's LCD init (0x8004dd3a) leaves behind; used
// for a fresh machine and for snapshots that predate the LCD model.
func (l *lcd) bootDefaults() {
	l.reg[0x003] = 0x00A0
	l.reg[0x210], l.reg[0x211] = 0, panelW-1
	l.reg[0x212], l.reg[0x213] = 0, panelH-1
}

func (l *lcd) rs() bool { return l.pfc != nil && l.pfc.regs[0x13C]&0x10 != 0 }

func (l *lcd) read(va, size uint32) uint32 {
	if !l.rs() {
		return l.idx
	}
	if l.idx == 0x202 {
		return 0 // GRAM read-back not modelled (the OS never reads pixels back)
	}
	return uint32(l.reg[l.idx])
}

func (l *lcd) write(va, size, val uint32) {
	if !l.rs() {
		l.idx = val & (lcdReg - 1)
		return
	}
	l.data(uint16(val))
}

func (l *lcd) window() (hsa, hea, vsa, vea uint32) {
	return uint32(l.reg[0x210]) & 0x1FF, uint32(l.reg[0x211]) & 0x1FF,
		uint32(l.reg[0x212]) & 0x1FF, uint32(l.reg[0x213]) & 0x1FF
}

// data handles a data-phase write to the selected register.
func (l *lcd) data(v uint16) {
	switch l.idx {
	case 0x202:
		l.pixel(v)
		return
	case 0x200, 0x201:
		l.reg[l.idx] = v
		l.setAddress()
		return
	}
	l.reg[l.idx] = v
}

// setAddress loads the address counter from R200/R201. With ORG=1 the address is relative
// to the window origin, which I/D places at the window corner the scan starts from.
func (l *lcd) setAddress() {
	h, v := uint32(l.reg[0x200])&0x1FF, uint32(l.reg[0x201])&0x1FF
	em := l.reg[0x003]
	if em&0x80 == 0 {
		l.h, l.v = h, v
		return
	}
	hsa, hea, vsa, vea := l.window()
	if em&0x10 != 0 {
		l.h = hsa + h
	} else {
		l.h = hea - h
	}
	if em&0x20 != 0 {
		l.v = vsa + v
	} else {
		l.v = vea - v
	}
}

// pixel stores one GRAM word and advances the address counter inside the window per the
// entry mode (I/D0 = H direction, I/D1 = V direction, AM = vertical-first).
func (l *lcd) pixel(c uint16) {
	if l.h < panelW && l.v < panelH {
		l.gram[l.v*panelW+l.h] = c
		l.gen++
	}
	em := l.reg[0x003]
	hsa, hea, vsa, vea := l.window()
	stepH := func() bool { // true = wrapped
		if em&0x10 != 0 {
			if l.h >= hea {
				l.h = hsa
				return true
			}
			l.h++
		} else {
			if l.h <= hsa {
				l.h = hea
				return true
			}
			l.h--
		}
		return false
	}
	stepV := func() bool {
		if em&0x20 != 0 {
			if l.v >= vea {
				l.v = vsa
				return true
			}
			l.v++
		} else {
			if l.v <= vsa {
				l.v = vea
				return true
			}
			l.v--
		}
		return false
	}
	if em&0x08 == 0 {
		if stepH() {
			stepV()
		}
	} else if stepV() {
		stepH()
	}
}

// dma streams n bytes of big-endian RGB565 from src into the data port (a DMAC transfer
// whose destination is the LCD).
func (l *lcd) dma(src []byte) {
	for i := 0; i+1 < len(src); i += 2 {
		l.data(uint16(src[i])<<8 | uint16(src[i+1]))
	}
}

// renderOS writes the OS's 384x216 area of GRAM into dst as big-endian RGB565.
func (l *lcd) renderOS(dst []byte) {
	for y := 0; y < FbHeight; y++ {
		row := l.gram[y*panelW:]
		for x := 0; x < FbWidth; x++ {
			c := row[osOriginH-x]
			dst[(y*FbWidth+x)*2] = byte(c >> 8)
			dst[(y*FbWidth+x)*2+1] = byte(c)
		}
	}
}

// renderPanel writes the whole 396x224 panel into dst as big-endian RGB565: the OS's area
// (panel columns 6..389, rows 0..215) plus the frame around it (6 columns left and right, 8
// rows below), which the OS paints in its frame colour (DrawFrame) and gint add-ins use as
// part of their 396x224 screen.
func (l *lcd) renderPanel(dst []byte) {
	for y := 0; y < panelH; y++ {
		row := l.gram[y*panelW:]
		for x := 0; x < panelW; x++ {
			c := row[panelW-1-x]
			dst[(y*panelW+x)*2] = byte(c >> 8)
			dst[(y*panelW+x)*2+1] = byte(c)
		}
	}
}

// seedFromVRAM fills GRAM from a 384x216 big-endian RGB565 VRAM buffer and the frame around it
// with the OS's frame colour (Resume: GRAM is not in the save-state; at a snapshot the panel
// showed the last pushed VRAM inside the frame the OS last drew).
func (l *lcd) seedFromVRAM(src []byte, frame uint16) {
	for i := range l.gram {
		l.gram[i] = frame
	}
	for y := 0; y < FbHeight; y++ {
		for x := 0; x < FbWidth; x++ {
			i := (y*FbWidth + x) * 2
			l.gram[y*panelW+osOriginH-x] = uint16(src[i])<<8 | uint16(src[i+1])
		}
	}
	l.gen++
}

// save-state encoding (in the MMIO section's per-region map): marker, index, address
// counter, and every non-zero register at lcdKeyReg|index.
const (
	lcdKeyVer = 0xFFFFFFFF
	lcdKeyIdx = 0x80001000
	lcdKeyH   = 0x80001001
	lcdKeyV   = 0x80001002
	lcdKeyReg = 0x80000000
)

func (l *lcd) stateMap() map[uint32]uint32 {
	m := map[uint32]uint32{lcdKeyVer: 1, lcdKeyIdx: l.idx, lcdKeyH: l.h, lcdKeyV: l.v}
	for i, r := range l.reg {
		if r != 0 {
			m[lcdKeyReg|uint32(i)] = uint32(r)
		}
	}
	return m
}

// restoreState applies stateMap output; a map without the marker (a snapshot taken before
// the LCD model, when this region was a plain register log) gets the boot defaults.
func (l *lcd) restoreState(m map[uint32]uint32) {
	l.reg = [lcdReg]uint16{}
	if m[lcdKeyVer] != 1 {
		l.idx, l.h, l.v = 0, 0, 0
		l.bootDefaults()
		return
	}
	l.idx, l.h, l.v = m[lcdKeyIdx]&(lcdReg-1), m[lcdKeyH], m[lcdKeyV]
	for k, v := range m {
		if k&0xFFFFF000 == lcdKeyReg {
			l.reg[k&(lcdReg-1)] = uint16(v)
		}
	}
}
