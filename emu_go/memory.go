package main

import "fmt"

// SH7305 address space. P0/P1/P2 alias phys = vaddr & 0x1FFFFFFF; ROM (OS image)
// at phys 0, DRAM at 0x0C000000, on-chip RAM at 0xFD800000; MMIO delegated to the bus.
// Faithful port of emu/memory.py.

const (
	FlashSize   = 0x08000000 // NOR flash window; image is a prefix, rest reads 0xFF
	FlashMutTop = 0x02000000 // mutable/writable flash extent (32MB): OS image + fls0 storage tail
	DramBase    = 0x0C000000
	DramSize    = 0x00800000 // 8 MB
	IlramBase   = 0xFD800000
	IlramSize   = 0x00010000 // 64 KB
	OcramBase   = 0xFE200000 // on-chip RAM the OS uses for kernel lists/structs (swept 0xFE380000.. etc)
	OcramSize   = 0x00200000 // 2 MB: covers 0xFE224000, 0xFE2FFC24, 0xFE380000-8BFFC, 0xFE3C0000, 0xFE3FFD00
)

// NOR-flash command-state-machine states (AMD/Spansion JEDEC command set). The fls0
// FTL programs/erases flash via these; the boot reads code from the same array.
const (
	fIdle = iota
	fUnlock1
	fUnlock2
	fProgram
	fErase1
	fErase2
	fErase3
	fBufCount
	fBufData
)

type fword struct {
	addr, size, val uint32
}

type Memory struct {
	rom     []byte
	romSize uint32
	flash   []byte // mutable NOR array [0, FlashMutTop): image copy then 0xFF; program/erase mutate it
	dram    []byte
	ilram   []byte
	ocram   []byte // on-chip RAM at OcramBase
	mmio    *MMIOBus
	trace   bool
	wpages  map[uint32]int // if non-nil, histogram DRAM write target by 32KB page
	fwrites map[uint32]int // if non-nil, histogram FLASH write target by 32KB page
	fwLog   int            // remaining flash-write detail lines to print

	// NOR command state machine
	fcmd    int
	bufRem  int     // remaining buffered-program data words to collect
	bufW    []fword // buffered-program collected words
}

func NewMemory(osImage []byte, mmio *MMIOBus) *Memory {
	flash := make([]byte, FlashMutTop)
	n := copy(flash, osImage)
	for i := n; i < len(flash); i++ {
		flash[i] = 0xFF // erased NOR
	}
	return &Memory{
		rom:     osImage,
		romSize: uint32(len(osImage)),
		flash:   flash,
		dram:    make([]byte, DramSize),
		ilram:   make([]byte, IlramSize),
		ocram:   make([]byte, OcramSize),
		mmio:    mmio,
	}
}

// Read returns a big-endian value of 1/2/4 bytes at virtual address va.
func (m *Memory) Read(va, size uint32) uint32 {
	va &= 0xFFFFFFFF
	if va >= 0xE0000000 {
		if va >= IlramBase && va < IlramBase+IlramSize {
			return beRead(m.ilram, va-IlramBase, size)
		}
		if va >= OcramBase && va < OcramBase+OcramSize {
			return beRead(m.ocram, va-OcramBase, size)
		}
		return m.mmio.Read(va, size) // control regs (0xFF.., 0xFE.., store queues)
	}
	if mphys := va & 0x1FFFFFFF; mphys >= DramBase && mphys < DramBase+DramSize {
		// DRAM via ANY mirror, incl. the uncached VRAM mirror 0xAC000000 (P2). Must come
		// before the 0xA4..0xC0 MMIO range below, else VRAM draws are dropped as MMIO.
		return beRead(m.dram, mphys-DramBase, size)
	}
	if va >= 0xA4000000 && va < 0xC0000000 {
		return m.mmio.Read(va, size) // on-chip periph + area-5 LCD as MMIO
	}
	phys := va & 0x1FFFFFFF
	if phys < FlashSize {
		if phys < FlashMutTop {
			return beRead(m.flash, phys, size) // array-read mode (mutable NOR)
		}
		return (uint32(1) << (size * 8)) - 1 // beyond mutable extent: erased 0xFF
	}
	if phys >= DramBase && phys < DramBase+DramSize {
		return beRead(m.dram, phys-DramBase, size)
	}
	panic(memFault("read", va, size))
}

func (m *Memory) Write(va, size, val uint32) {
	va &= 0xFFFFFFFF
	mask := (uint32(1) << (size * 8)) - 1
	if size == 4 {
		mask = 0xFFFFFFFF
	}
	val &= mask
	if va >= 0xE0000000 {
		if va >= IlramBase && va < IlramBase+IlramSize {
			beWrite(m.ilram, va-IlramBase, size, val)
			return
		}
		if va >= OcramBase && va < OcramBase+OcramSize {
			beWrite(m.ocram, va-OcramBase, size, val)
			return
		}
		m.mmio.Write(va, size, val)
		return
	}
	if mphys := va & 0x1FFFFFFF; mphys >= DramBase && mphys < DramBase+DramSize {
		// DRAM via ANY mirror, incl. uncached VRAM 0xAC000000 — must precede the MMIO range.
		if m.wpages != nil {
			m.wpages[(mphys-DramBase)&^0x7FFF]++
		}
		beWrite(m.dram, mphys-DramBase, size, val)
		return
	}
	if va >= 0xA4000000 && va < 0xC0000000 {
		m.mmio.Write(va, size, val)
		return
	}
	phys := va & 0x1FFFFFFF
	if phys < FlashSize {
		if m.fwrites != nil {
			m.fwrites[phys&^0x7FFF]++
			if m.fwLog > 0 {
				fmt.Printf("  [flashwr] va=0x%08x phys=0x%08x size=%d val=0x%x st=%d\n", va, phys, size, val, m.fcmd)
				m.fwLog--
			}
		}
		m.flashCmd(phys, size, val)
		return
	}
	if phys >= DramBase && phys < DramBase+DramSize {
		if m.wpages != nil {
			m.wpages[(phys-DramBase)&^0x7FFF]++
		}
		beWrite(m.dram, phys-DramBase, size, val)
		return
	}
	panic(memFault("write", va, size))
}

// flashCmd drives the NOR command state machine. Only PROGRAM/ERASE mutate the array;
// unlock and ID/CFI/reset commands change state only (so code-fetch reads stay valid).
func (m *Memory) flashCmd(phys, size, val uint32) {
	a12 := phys & 0xFFF
	c := val & 0xFF
	switch m.fcmd {
	case fIdle:
		if a12 == 0xAAA && c == 0xAA {
			m.fcmd = fUnlock1
		}
	case fUnlock1:
		if a12 == 0x554 && c == 0x55 {
			m.fcmd = fUnlock2
		} else {
			m.fcmd = fIdle
		}
	case fUnlock2:
		switch {
		case a12 == 0xAAA && c == 0xA0: // program
			m.fcmd = fProgram
		case a12 == 0xAAA && c == 0x80: // erase setup
			m.fcmd = fErase1
		case c == 0x25: // write-to-buffer (addr = sector); count comes next
			m.bufW = m.bufW[:0]
			m.bufRem = -1
			m.fcmd = fBufCount
		default: // 0x90 autoselect, 0x98 CFI, 0xF0 reset, etc — no array change
			m.fcmd = fIdle
		}
	case fProgram:
		m.flashProgram(phys, size, val)
		m.fcmd = fIdle
	case fErase1:
		if a12 == 0xAAA && c == 0xAA {
			m.fcmd = fErase2
		} else {
			m.fcmd = fIdle
		}
	case fErase2:
		if a12 == 0x554 && c == 0x55 {
			m.fcmd = fErase3
		} else {
			m.fcmd = fIdle
		}
	case fErase3:
		if c == 0x10 { // chip erase
			for i := range m.flash {
				m.flash[i] = 0xFF
			}
		} else if c == 0x30 { // sector erase (64KB) containing phys
			s := phys &^ 0xFFFF
			for i := s; i < s+0x10000 && i < FlashMutTop; i++ {
				m.flash[i] = 0xFF
			}
		}
		m.fcmd = fIdle
	case fBufCount:
		m.bufRem = int(val&0xFFFF) + 1 // word count - 1 was written
		m.fcmd = fBufData
	case fBufData:
		if m.bufRem > 0 {
			m.bufW = append(m.bufW, fword{phys, size, val})
			m.bufRem--
		} else { // confirm (0x29) — commit all buffered words
			for _, w := range m.bufW {
				m.flashProgram(w.addr, w.size, w.val)
			}
			m.fcmd = fIdle
		}
	}
}

// flashProgram performs a NOR word/byte program: bits can only go 1->0 (AND).
func (m *Memory) flashProgram(phys, size, val uint32) {
	if phys+size > FlashMutTop {
		return
	}
	for i := uint32(0); i < size; i++ {
		b := byte(val >> (8 * (size - 1 - i)))
		m.flash[phys+i] &= b
	}
}

func (m *Memory) R8(va uint32) uint32  { return m.Read(va, 1) }
func (m *Memory) R16(va uint32) uint32 { return m.Read(va, 2) }
func (m *Memory) R32(va uint32) uint32 { return m.Read(va, 4) }
func (m *Memory) W8(va, v uint32)      { m.Write(va, 1, v) }
func (m *Memory) W16(va, v uint32)     { m.Write(va, 2, v) }
func (m *Memory) W32(va, v uint32)     { m.Write(va, 4, v) }

// big-endian helpers
func beRead(buf []byte, off, size uint32) uint32 {
	var v uint32
	for i := uint32(0); i < size; i++ {
		v = (v << 8) | uint32(buf[off+i])
	}
	return v
}

func beReadClamped(buf []byte, off, size, limit uint32) uint32 {
	var v uint32
	for i := uint32(0); i < size; i++ {
		var b byte = 0xFF
		if off+i < limit {
			b = buf[off+i]
		}
		v = (v << 8) | uint32(b)
	}
	return v
}

func beWrite(buf []byte, off, size, val uint32) {
	for i := uint32(0); i < size; i++ {
		buf[off+size-1-i] = byte(val >> (8 * i))
	}
}

type MemFaultError struct{ msg string }

func (e MemFaultError) Error() string { return e.msg }

func memFault(op string, va, size uint32) MemFaultError {
	return MemFaultError{msg: fmt.Sprintf("%s unmapped 0x%08x (size %d)", op, va, size)}
}
