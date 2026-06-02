#!/usr/bin/env python3
"""
fx-CG50 (SH7305 / SH-4A) emulator — address space & memory.

SH-4 virtual address regions (we collapse cache behaviour; functionally P0/P1/P2
all alias the same physical memory):
    P0  0x00000000-0x7FFFFFFF  cacheable, TLB-mapped (boot uses it 1:1 though)
    P1  0x80000000-0x9FFFFFFF  cacheable,  phys = vaddr & 0x1FFFFFFF
    P2  0xA0000000-0xBFFFFFFF  uncacheable phys = vaddr & 0x1FFFFFFF
    P3  0xC0000000-0xDFFFFFFF  cacheable, TLB-mapped
    P4  0xE0000000-0xFFFFFFFF  control space (on-chip regs, store queues, on-chip RAM)

Physical map we reverse-engineered (see RECON_NOTES.md):
    0x00000000  OS image (4.65MB->11.9MB unpacked)         <- ROM/flash image, base of P1/P2
    0x0C000000  main DRAM (cached 0x8C.., uncached 0xAC..) <- 8 MB
    0x14000000  area 5 / R61524 LCD (cmd@+0, data@+2)      <- handled as MMIO
    0xA4000000.. on-chip peripherals (CPG/PFC/TMU/KEYSC..) <- MMIO (P4-ish, but at 0xA4..)
    0xFC000000.. BSC / DMAC / etc.                         <- MMIO
    0xFD800000  on-chip RAM (boot stack, kernel structs)   <- ~ 0x... RAM
    0xFF000000  CCN/MMU/cache control                      <- MMIO

This module owns plain RAM/ROM; MMIO ranges are delegated to an MMIO bus object.
"""

import struct

# physical region sizes
OS_IMAGE_BASE = 0x00000000
FLASH_SIZE = 0x08000000         # NOR flash address window (image is a prefix of it;
                                # rest reads as 0xFF erased). Writes = flash commands.
FLASH_MUT_TOP = 0x02000000      # mutable/writable NOR extent (32MB): OS image + fls0 storage tail
DRAM_BASE = 0x0C000000
DRAM_SIZE = 0x00800000          # 8 MB (fx-CG50 has 8MB? adjust when confirmed)
ILRAM_BASE = 0xFD800000         # on-chip RAM (boot stack @0xFD804000 implies >=16KB here)
ILRAM_SIZE = 0x00010000         # 64 KB (provisional)
OCRAM_BASE = 0xFE200000         # on-chip RAM for kernel lists/structs (swept 0xFE380000.. etc)
OCRAM_SIZE = 0x00200000         # 2 MB

# NOR command-state-machine states (AMD/Spansion JEDEC). The fls0 FTL programs/erases
# flash via these; boot reads code from the same array. Only program/erase mutate data.
_F_IDLE, _F_UNLOCK1, _F_UNLOCK2, _F_PROGRAM, _F_ERASE1, _F_ERASE2, _F_ERASE3, \
    _F_BUFCOUNT, _F_BUFDATA = range(9)


class Memory:
    def __init__(self, os_image: bytes, mmio):
        # ROM: the unpacked OS image, addressed from physical 0.
        self.rom = bytearray(os_image)
        self.rom_size = len(os_image)
        # mutable NOR array [0, FLASH_MUT_TOP): image copy then 0xFF; program/erase mutate it
        self.flash = bytearray(FLASH_MUT_TOP)
        n = min(len(os_image), FLASH_MUT_TOP)
        self.flash[:n] = os_image[:n]
        for i in range(n, FLASH_MUT_TOP):
            self.flash[i] = 0xFF
        self.dram = bytearray(DRAM_SIZE)
        self.ilram = bytearray(ILRAM_SIZE)
        self.ocram = bytearray(OCRAM_SIZE)
        self.mmio = mmio            # object with read(phys,size)/write(phys,size,val)
        self.trace = False
        # NOR command state
        self._fcmd = _F_IDLE
        self._buf_rem = 0
        self._buf_w = []

    # ---- virtual -> (kind, backing, offset) ----
    def _resolve(self, va):
        va &= 0xFFFFFFFF
        top = va >> 28
        # P4 control space (0xE0000000-0xFFFFFFFF) — partly on-chip RAM, partly regs
        if va >= 0xE0000000:
            if ILRAM_BASE <= va < ILRAM_BASE + ILRAM_SIZE:
                return ("ram", self.ilram, va - ILRAM_BASE)
            if OCRAM_BASE <= va < OCRAM_BASE + OCRAM_SIZE:
                return ("ram", self.ocram, va - OCRAM_BASE)
            return ("mmio", None, va)          # control regs (0xFF.., 0xFE.., store queues)
        # DRAM via ANY mirror, incl. the uncached VRAM mirror 0xAC000000 (P2). Must come
        # before the 0xA4..0xC0 MMIO range below, else VRAM draws are dropped as MMIO.
        mphys = va & 0x1FFFFFFF
        if DRAM_BASE <= mphys < DRAM_BASE + DRAM_SIZE:
            return ("dram", self.dram, mphys - DRAM_BASE)
        # 0xA4000000-0xBFFFFFFF: on-chip peripherals + area-5 LCD live here as MMIO.
        if 0xA4000000 <= va < 0xC0000000:
            phys = va & 0x1FFFFFFF
            # area-5 LCD (0x14000000) and on-chip periph (0x04xxxxxx) are MMIO
            return ("mmio", None, va)
        # P0/P1/P2 normal: phys = va & 0x1FFFFFFF
        phys = va & 0x1FFFFFFF
        if phys < FLASH_SIZE:
            return ("flash", None, phys)
        if DRAM_BASE <= phys < DRAM_BASE + DRAM_SIZE:
            return ("dram", self.dram, phys - DRAM_BASE)
        return ("unmapped", None, va)

    # ---- reads (big-endian) ----
    def read(self, va, size):
        kind, buf, off = self._resolve(va)
        if kind == "mmio":
            return self.mmio.read(va, size)
        if kind == "flash":
            if off < FLASH_MUT_TOP:
                return int.from_bytes(self.flash[off:off + size], "big")  # array-read (mutable NOR)
            return (1 << (size * 8)) - 1                 # beyond mutable extent: erased 0xFF
        if kind == "unmapped":
            raise MemFault(f"read{size*8} from unmapped 0x{va:08x}")
        b = bytes(buf[off:off + size])
        if len(b) < size:
            b = b + b"\x00" * (size - len(b))
        return int.from_bytes(b, "big")

    def write(self, va, size, val):
        kind, buf, off = self._resolve(va)
        val &= (1 << (size * 8)) - 1
        if kind == "mmio":
            self.mmio.write(va, size, val)
            return
        if kind == "flash":
            self._flash_cmd(off, size, val)   # NOR command state machine (program/erase persist)
            return
        if kind == "unmapped":
            raise MemFault(f"write{size*8} 0x{val:x} to unmapped 0x{va:08x}")
        buf[off:off + size] = val.to_bytes(size, "big")

    # ---- NOR flash command state machine (mirror of emu_go/memory.go flashCmd) ----
    def _flash_cmd(self, phys, size, val):
        a12 = phys & 0xFFF
        c = val & 0xFF
        st = self._fcmd
        if st == _F_IDLE:
            if a12 == 0xAAA and c == 0xAA:
                self._fcmd = _F_UNLOCK1
        elif st == _F_UNLOCK1:
            self._fcmd = _F_UNLOCK2 if (a12 == 0x554 and c == 0x55) else _F_IDLE
        elif st == _F_UNLOCK2:
            if a12 == 0xAAA and c == 0xA0:
                self._fcmd = _F_PROGRAM
            elif a12 == 0xAAA and c == 0x80:
                self._fcmd = _F_ERASE1
            elif c == 0x25:                       # write-to-buffer (addr = sector); count next
                self._buf_w = []
                self._buf_rem = -1
                self._fcmd = _F_BUFCOUNT
            else:                                  # 0x90/0x98/0xF0 etc — no array change
                self._fcmd = _F_IDLE
        elif st == _F_PROGRAM:
            self._flash_program(phys, size, val)
            self._fcmd = _F_IDLE
        elif st == _F_ERASE1:
            self._fcmd = _F_ERASE2 if (a12 == 0xAAA and c == 0xAA) else _F_IDLE
        elif st == _F_ERASE2:
            self._fcmd = _F_ERASE3 if (a12 == 0x554 and c == 0x55) else _F_IDLE
        elif st == _F_ERASE3:
            if c == 0x10:                          # chip erase
                for i in range(FLASH_MUT_TOP):
                    self.flash[i] = 0xFF
            elif c == 0x30:                        # sector erase (64KB) containing phys
                s = phys & ~0xFFFF
                for i in range(s, min(s + 0x10000, FLASH_MUT_TOP)):
                    self.flash[i] = 0xFF
            self._fcmd = _F_IDLE
        elif st == _F_BUFCOUNT:
            self._buf_rem = (val & 0xFFFF) + 1     # word count - 1 was written
            self._fcmd = _F_BUFDATA
        elif st == _F_BUFDATA:
            if self._buf_rem > 0:
                self._buf_w.append((phys, size, val))
                self._buf_rem -= 1
            else:                                  # confirm (0x29) — commit buffered words
                for (a, sz, v) in self._buf_w:
                    self._flash_program(a, sz, v)
                self._fcmd = _F_IDLE

    def _flash_program(self, phys, size, val):
        if phys + size > FLASH_MUT_TOP:
            return
        for i in range(size):
            b = (val >> (8 * (size - 1 - i))) & 0xFF
            self.flash[phys + i] &= b              # NOR program: bits only 1->0

    # convenience
    def r8(self, va):  return self.read(va, 1)
    def r16(self, va): return self.read(va, 2)
    def r32(self, va): return self.read(va, 4)
    def w8(self, va, v):  self.write(va, 1, v)
    def w16(self, va, v): self.write(va, 2, v)
    def w32(self, va, v): self.write(va, 4, v)


class MemFault(Exception):
    pass
