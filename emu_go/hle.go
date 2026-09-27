package main

// High-level emulation of the OS's generic bitmap blitter (3.60: 0x80056900), which is ~75%
// of the instructions of a MAIN MENU cursor move (~85 interpreted instructions per pixel, see
// blit_probe_test.go). When enabled (Emulator.EnableHLE; the Android bridge turns it on, the
// desktop/test default is off so goldens are untouched), the CPU loop calls hleBlit at the
// function's entry; it draws natively and returns as the function's `rts` would, charging
// the emulated cycles the interpreter would have spent so the machine's timing is unchanged.
//
// The routine, from the disassembly (all fields big-endian at r4; r5 = pixel writer):
//
//	+0x00 x     +0x04 y (panel row; the wrappers add the 24-line status bar before calling)
//	+0x08 xo    +0x0c yo   (start offset inside the bitmap; both reset to 0 unless 0<=xo<w, 0<=yo<h)
//	+0x10 w     +0x14 h    +0x18 format: 1 = 4 bpp palette, 2 = RGB565 big-endian, 3 = 1 bpp
//	+0x1c data  +0x20/+0x21 fg/bg palette index for 1 bpp   +0x22 transparent index (0xff = none)
//	+0x24 rop: 2 = invert, 3 = dither (odd x+y pixels -> white / the transparent colour)
//	+0x25 mode2: 2/3/4 = or/and/xor the VRAM pixel   +0x28 blend level (>0 -> 0x8004eb54)
//
// Clipping: returns without drawing if x<0, x>=384, y<0, y>=216, w<=0, h<=0, x>x+w-xo or
// y>y+h-yo; the right/bottom edges are clamped to 384/216. Row stride in pixels is w rounded
// up to even (4 bpp) or to a multiple of 8 (1 bpp). Pixel (x+i, y+j) comes from bitmap index
// (xo+i) + stride*(yo+j); its palette is the 16 RGB565 words at 0x80399d30. A pixel whose
// colour equals the transparent colour is skipped. r5==1 writes VRAM 0xAC000000 + y*768 + x*2;
// r5==2 talks to the panel directly (not handled here).
//
// Handled here: r5==1, blend<=0, any rop, mode2 in {1,2,3,4} (1 = plain; other values also
// read VRAM but never use it), formats 1-3 — every combination the OS was seen to use.
// Anything else returns false and the interpreter runs the real code.

const (
	hleBlitEntry = 0x80056900
	blitPalette  = 0x80399d30
	vramPhys     = 0x0C000000 // OS VRAM: 384*216 RGB565 at the start of DRAM
)

// Cycle charge per call = blitBase + rows*blitRow + sum over pixels of blitPix[format]
// (drawn) or blitSkip[format] (transparent). Calibrated against the interpreter by
// TestHLEBlitMatchesInterpreter (it reports the charged/actual ratio).
const (
	blitBase = 90
	blitRow  = 30
)

var (
	blitPix  = [4]uint64{0, 85, 83, 94}
	blitSkip = [4]uint64{0, 55, 53, 64}
)

// hleBlit runs the blitter natively if the call is one it handles; see the file comment.
func (c *CPU) hleBlit() bool {
	m := c.mem
	d := c.r[4]
	if c.r[5] != 1 {
		return false
	}
	if m.mmuAt && d < 0x80000000 {
		return false // descriptor in translated memory: let the real code take any TLB miss
	}
	rd8 := func(a uint32) uint32 { return m.Read(a, 1) }
	rd32 := func(a uint32) int32 { return int32(m.Read(a, 4)) }
	format := rd8(d + 0x18)
	if format < 1 || format > 3 {
		return false
	}
	rop, mode2, blend := rd8(d+0x24), rd8(d+0x25), rd32(d+0x28)
	if blend > 0 || mode2 == 0 || mode2 > 4 {
		return false
	}
	data := uint32(rd32(d + 0x1c))
	if m.mmuAt && data < 0x80000000 {
		return false
	}

	x, y, xo, yo, w, h := rd32(d), rd32(d+4), rd32(d+8), rd32(d+0xc), rd32(d+0x10), rd32(d+0x14)
	if !(xo >= 0 && xo < w && yo >= 0 && yo < h) {
		xo, yo = 0, 0
	}
	xend, yend := x+w-xo, y+h-yo
	rows := uint64(0)
	if x < 0 || x >= FbWidth || y < 0 || y >= FbHeight || h <= 0 || w <= 0 || x > xend || y > yend {
		goto done
	}
	if xend > FbWidth {
		xend = FbWidth
	}
	if yend > FbHeight {
		yend = FbHeight
	}
	{
		stride := w
		switch format {
		case 1:
			stride += w & 1
		case 3:
			if w&7 != 0 {
				stride += 8 - w&7
			}
		}
		var pal [16]uint32
		for i := range pal {
			pal[i] = m.Read(blitPalette+uint32(i)*2, 2)
		}
		transp := uint32(0xFFFFFFFF)
		if ti := rd8(d + 0x22); ti != 0xFF {
			transp = pal[ti&15]
		}
		fg, bg := rd8(d+0x20)&15, rd8(d+0x21)&15
		vram := m.dram[vramPhys-DramBase:]
		for py, srow := y, yo; py < yend; py, srow = py+1, srow+1 {
			rows++
			rowBase := stride * srow
			for px, sx := x, xo; px < xend; px, sx = px+1, sx+1 {
				idx := uint32(rowBase + sx)
				var color uint32
				switch format {
				case 1:
					b := rd8(data + idx>>1)
					if idx&1 == 0 {
						b >>= 4
					}
					color = pal[b&15]
				case 2:
					color = rd8(data+idx*2)<<8 | rd8(data+idx*2+1)
				case 3:
					if rd8(data+idx>>3)<<(idx&7)&0x80 != 0 {
						color = pal[fg]
					} else {
						color = pal[bg]
					}
				}
				if color == transp {
					c.cycles += blitSkip[format]
					continue
				}
				c.cycles += blitPix[format]
				switch rop {
				case 2:
					color = ^color
				case 3:
					if (px+py)&1 != 0 {
						color = transp // 0xFFFFFFFF (white) when there is no transparent colour
					}
				}
				off := py*FbWidth*2 + px*2
				if mode2 != 1 {
					dst := uint32(vram[off])<<8 | uint32(vram[off+1])
					switch mode2 {
					case 2:
						color |= dst
					case 3:
						color &= dst
					case 4:
						color ^= dst
					}
				}
				vram[off], vram[off+1] = byte(color>>8), byte(color)
			}
		}
	}
done:
	c.cycles += blitBase + rows*blitRow
	c.pc = c.pr // rts; callee-saved registers, pr and macl are as at entry
	return true
}
