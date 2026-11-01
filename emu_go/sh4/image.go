package sh4

import (
	"bytes"
	"errors"
)

// Image is a block of memory at a fixed address: an OS flash image, an add-in's code, a RAM
// snapshot. Reads are big-endian; the bytes are not copied.
type Image struct {
	Base uint32
	Data []byte
}

// NewImage returns an Image of data mapped at base.
func NewImage(base uint32, data []byte) *Image { return &Image{Base: base, Data: data} }

// End is the address after the last byte.
func (m *Image) End() uint32 { return m.Base + uint32(len(m.Data)) }

// Contains reports whether addr is inside the image.
func (m *Image) Contains(addr uint32) bool { return addr-m.Base < uint32(len(m.Data)) }

// Byte reads the byte at addr.
func (m *Image) Byte(addr uint32) (uint8, bool) {
	off := addr - m.Base
	if off >= uint32(len(m.Data)) {
		return 0, false
	}
	return m.Data[off], true
}

// Word reads the 16-bit word at addr (any alignment). Its method value m.Word is a ReadFunc.
func (m *Image) Word(addr uint32) (uint16, bool) {
	off := addr - m.Base
	if off >= uint32(len(m.Data)) || uint32(len(m.Data))-off < 2 {
		return 0, false
	}
	return uint16(m.Data[off])<<8 | uint16(m.Data[off+1]), true
}

// Long reads the 32-bit word at addr (any alignment).
func (m *Image) Long(addr uint32) (uint32, bool) {
	off := addr - m.Base
	if off >= uint32(len(m.Data)) || uint32(len(m.Data))-off < 4 {
		return 0, false
	}
	d := m.Data[off : off+4]
	return uint32(d[0])<<24 | uint32(d[1])<<16 | uint32(d[2])<<8 | uint32(d[3]), true
}

// Bytes returns up to n bytes at addr (fewer at the end of the image, nil outside it).
func (m *Image) Bytes(addr uint32, n int) []byte {
	off := addr - m.Base
	if off >= uint32(len(m.Data)) {
		return nil
	}
	end := uint64(off) + uint64(n)
	if end > uint64(len(m.Data)) {
		end = uint64(len(m.Data))
	}
	return m.Data[off:end]
}

// Decode decodes the instruction at addr from the image.
func (m *Image) Decode(addr uint32) (Instr, bool) { return Decode(addr, m.Word) }

// Memory addresses of the fx-CG50 that the analysis presets use.
const (
	ROMBase      = 0x80000000 // the flash, cached P1 mapping
	OSCodeSize   = 0x00C00000 // the OS image at the start of the flash; storage memory follows
	AddinCode    = 0x00300000 // where the OS maps an add-in's code (the .g3a after its header)
	G3AHeaderLen = 0x7000     // a .g3a's header (and icons) before the code
)

var g3aMagic = []byte{0xAA, 0xAC, 0xBD, 0xAF, 0x90, 0x88, 0x9A, 0x8D}

// G3AImage maps a .g3a file the way the OS does: the code (the file after its 0x7000-byte
// header) at AddinCode, so the header itself sits at AddinCode - G3AHeaderLen, as in DASM.
// The entry point is AddinCode.
func G3AImage(file []byte) (*Image, error) {
	if len(file) <= G3AHeaderLen {
		return nil, errors.New("sh4: file too short for a .g3a")
	}
	if !bytes.Equal(file[:len(g3aMagic)], g3aMagic) {
		return nil, errors.New("sh4: not a .g3a (bad magic)")
	}
	return NewImage(AddinCode-G3AHeaderLen, file), nil
}
