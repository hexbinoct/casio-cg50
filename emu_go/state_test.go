package main

import (
	"path/filepath"
	"testing"
)

// SaveState/LoadState must round-trip the full machine: CPU registers, DRAM, ILRAM, OCRAM,
// and the flash delta (storage tail the OS wrote). This guards the save-state persistence
// that lets the emulator resume at the MAIN MENU instead of re-running first-boot setup.
func TestSaveStateRoundtrip(t *testing.T) {
	img := make([]byte, 0x1000)
	for i := range img {
		img[i] = byte(i)
	}
	mem := NewMemory(img, NewMMIOBus())
	cpu := NewCPU(mem)

	// distinctive architectural + memory state
	cpu.pc, cpu.r[5], cpu.r[15] = 0x80123456, 0xdeadbeef, 0x8c0ffff0
	cpu.mach, cpu.macl, cpu.pr = 0x11112222, 0x33334444, 0x80055260
	cpu.rbank1[2] = 0xb2b2b2b2
	mem.dram[100] = 0xAB
	mem.W32(0x0C000200, 0xCAFEF00D) // DRAM via the bus
	mem.ilram[8] = 0x5A             // ILRAM
	mem.ocram[0x40] = 0xC3          // OCRAM
	mem.flash[0x01000000] = 0x42    // a storage-tail flash byte (differs from 0xFF baseline)
	mem.flash[0x01040abc] = 0x7E
	// peripheral registers (MMIO section): PFC display-enable byte, a DMAC channel register,
	// and the KEYSC live configuration
	mem.Write(0xA405013C, 1, 0x1D)
	mem.Write(0xFE008024, 4, 0x14000000)
	mem.Write(0xA44B0010, 2, 0x400)
	mem.Write(0xA44B0014, 2, 0x7600)
	// LCD controller: select R211 (RS low), write it (RS high); PFC ends at 0x1D again
	mem.Write(0xA405013C, 1, 0x0D)
	mem.Write(0xB4000000, 2, 0x211)
	mem.Write(0xA405013C, 1, 0x1D)
	mem.Write(0xB4000000, 2, 0x123)
	// MMU: one UTLB entry (slot 7) + translation enabled
	mem.Write(0xFF000000, 4, 0x00300000)
	mem.Write(0xFF000004, 4, 0x0C01017C)
	mem.Write(0xFF000010, 4, 7<<10|0x100)
	mem.mmu.ldtlb()
	mem.Write(0xFF000010, 4, 7<<10|0x101)

	path := filepath.Join(t.TempDir(), "st.bin")
	if err := SaveState(path, cpu, mem); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// restore into a pristine machine and compare
	mem2 := NewMemory(img, NewMMIOBus())
	cpu2 := NewCPU(mem2)
	if err := LoadState(path, cpu2, mem2); err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if cpu2.pc != 0x80123456 || cpu2.r[5] != 0xdeadbeef || cpu2.r[15] != 0x8c0ffff0 ||
		cpu2.mach != 0x11112222 || cpu2.macl != 0x33334444 || cpu2.pr != 0x80055260 ||
		cpu2.rbank1[2] != 0xb2b2b2b2 {
		t.Errorf("CPU regs not restored: pc=%08x r5=%08x mach=%08x rbank1[2]=%08x",
			cpu2.pc, cpu2.r[5], cpu2.mach, cpu2.rbank1[2])
	}
	if mem2.dram[100] != 0xAB || mem2.R32(0x8C000200) != 0xCAFEF00D {
		t.Errorf("DRAM not restored: [100]=%02x [0x200]=%08x", mem2.dram[100], mem2.R32(0x8C000200))
	}
	if mem2.ilram[8] != 0x5A || mem2.ocram[0x40] != 0xC3 {
		t.Errorf("ILRAM/OCRAM not restored: ilram=%02x ocram=%02x", mem2.ilram[8], mem2.ocram[0x40])
	}
	if mem2.flash[0x01000000] != 0x42 || mem2.flash[0x01040abc] != 0x7E {
		t.Errorf("flash delta not restored: %02x %02x", mem2.flash[0x01000000], mem2.flash[0x01040abc])
	}
	// a byte we never touched must equal the fresh baseline (image prefix / erased tail)
	if mem2.flash[0x01000001] != 0xFF || mem2.flash[0x10] != img[0x10] {
		t.Errorf("baseline corrupted: tail=%02x img=%02x", mem2.flash[0x01000001], mem2.flash[0x10])
	}
	if v := mem2.Read(0xA405013C, 1); v != 0x1D {
		t.Errorf("PFC register not restored: %#x", v)
	}
	if v := mem2.Read(0xFE008024, 4); v != 0x14000000 {
		t.Errorf("DMAC register not restored: %#x", v)
	}
	if k := mem2.mmio.keysc; k.mode != 0x400 || k.ie != 0x76 || k.flags != 0 {
		t.Errorf("KEYSC state not restored: mode=%#x ie=%#x flags=%#x", k.mode, k.ie, k.flags)
	}
	if v := mem2.Read(0xB4000000, 2); v != 0x123 || mem2.mmio.lcd.reg[0x003] != 0xA0 {
		t.Errorf("LCD state not restored: R211 read %#x, R003 %#x", v, mem2.mmio.lcd.reg[0x003])
	}
	if c := mem2.mmio.ccn; !c.at || c.utlb[7].vpn != 0x00300000 || c.utlb[7].data != 0x0C01017C {
		t.Errorf("MMU not restored: at=%v utlb[7]=%+v", c.at, c.utlb[7])
	}
	// GRAM isn't saved; resume seeds the panel from VRAM (dram[100] = pixel (50,0) high byte)
	if c := mem2.mmio.lcd.gram[osOriginH-50]; c>>8 != 0xAB {
		t.Errorf("GRAM not seeded from VRAM on resume: %#04x", c)
	}
}

// A snapshot without the MMIO section (every state saved before 2026-09-27, including the
// phone's) must resume with the display-enable bit set and KEYSC configured, else the OS
// never pushes VRAM to the LCD again and keys are dead.
func TestLegacyStateDefaults(t *testing.T) {
	img := make([]byte, 0x1000)
	mem := NewMemory(img, nil) // nil bus: SnapshotBytes then writes no MMIO section
	cpu := NewCPU(mem)
	blob, err := SnapshotBytes(cpu, mem)
	if err != nil {
		t.Fatal(err)
	}
	mem2 := NewMemory(img, NewMMIOBus())
	cpu2 := NewCPU(mem2)
	if err := ResumeBytes(blob, cpu2, mem2); err != nil {
		t.Fatal(err)
	}
	if v := mem2.Read(0xA405013C, 1); v&0x10 == 0 {
		t.Errorf("legacy resume left display disabled: PFC 0x13C=%#x", v)
	}
	if k := mem2.mmio.keysc; k.ctrl != 0x8000 || k.mode != 0x200 || k.ie != 0x48 {
		t.Errorf("legacy resume left KEYSC unconfigured: %+v", *k)
	}
	if l := mem2.mmio.lcd; l.reg[0x003] != 0xA0 || l.reg[0x211] != panelW-1 || l.reg[0x213] != panelH-1 {
		t.Errorf("legacy resume left the LCD without its boot registers: R003=%#x R211=%d R213=%d",
			l.reg[0x003], l.reg[0x211], l.reg[0x213])
	}
}
