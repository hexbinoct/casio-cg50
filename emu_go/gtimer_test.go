package main

import "testing"

func hasIRQ(cpu *CPU, intevt uint32) bool {
	for _, p := range cpu.pending {
		if p.intevt == intevt {
			return true
		}
	}
	return false
}

func newTimerRig(ips uint64) (*MMIOBus, *Memory, *CPU) {
	mmio := NewMMIOBus()
	mem := NewMemory(make([]byte, 0x1000), mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	mmio.timerPeriod = 30000 // not the pure-boot mode: interrupt sources active
	mmio.SetInstrPerSecond(ips)
	// enable the add-in sources in the INTC as gint's intc_priority() does
	mem.Write(0xA4080024, 2, 0x7000) // ETMU0 IPRJ[15:12]
	mem.Write(0xA40800D8, 1, 0x08)   // MSKCLR6 bit3
	mem.Write(0xA4080000, 2, 0xD000) // TMU0 IPRA[15:12]
	mem.Write(0xA40800D0, 1, 0x10)   // MSKCLR4 bit4
	mem.Write(0xA4080010, 2, 0xF000) // DMAC DEI0-3 IPRE[15:12]
	mem.Write(0xA40800C4, 1, 0x0F)   // MSKCLR1 bits 0-3
	return mmio, mem, cpu
}

// ETMU: gint's driver init writes each register and spins until it reads back; a running
// channel counts down at 32.768 kHz, reloads TCOR on underflow, sets UNF and (with UNIE)
// requests its INTEVT. The legacy free-counter view of +0xD8 stays until ETMU5 is programmed.
func TestETMUChannels(t *testing.T) {
	mmio, mem, cpu := newTimerRig(1_000_000) // countDiv = 30 instr per tick
	const u0 = 0xA44D0030
	cpu.cycles = 1000
	if v := mem.Read(0xA44D00D8, 4); v != ^uint32(1000/30)+1 {
		t.Fatalf("legacy +0xD8 = %#x", v)
	}
	// configure(): TSTR=0, TCOR=-1 (read back), TCNT=-1 (read back), TCR=0 (read back)
	mem.Write(u0, 1, 0)
	mem.Write(u0+4, 4, 0xFFFFFFFF)
	mem.Write(u0+8, 4, 0xFFFFFFFF)
	mem.Write(u0+0xC, 1, 0)
	if mem.Read(u0+4, 4) != 0xFFFFFFFF || mem.Read(u0+8, 4) != 0xFFFFFFFF || mem.Read(u0+0xC, 1) != 0 {
		t.Fatalf("read-back failed: TCOR %#x TCNT %#x TCR %#x", mem.Read(u0+4, 4), mem.Read(u0+8, 4), mem.Read(u0+0xC, 1))
	}
	// keyboard timer: 256 ticks, interrupt enabled
	mem.Write(u0+4, 4, 255)
	mem.Write(u0+8, 4, 255)
	mem.Write(u0+0xC, 1, tcrUNIE)
	mem.Write(u0, 1, 1)
	if mmio.etmu2.next() != 1000+256*30 {
		t.Fatalf("next underflow = %d, want %d", mmio.etmu2.next(), 1000+256*30)
	}
	cpu.cycles = 1000 + 100*30
	if v := mem.Read(u0+8, 4); v != 155 {
		t.Fatalf("TCNT after 100 ticks = %d, want 155", v)
	}
	cpu.cycles = 1000 + 256*30 - 1
	mmio.etmu2.tick(cpu)
	if hasIRQ(cpu, 0x9e0) {
		t.Fatal("early interrupt")
	}
	cpu.cycles = 1000 + 256*30
	mmio.etmu2.tick(cpu)
	if !hasIRQ(cpu, 0x9e0) {
		t.Fatalf("pending = %+v, want INTEVT 0x9e0", cpu.pending)
	}
	if v := mem.Read(u0+0xC, 1); v != tcrUNIE|tcrUNF {
		t.Fatalf("TCR = %#x, want UNIE|UNF", v)
	}
	if v := mem.Read(u0+8, 4); v != 255 {
		t.Fatalf("TCNT after underflow = %d, want 255 (reloaded)", v)
	}
	mem.Write(u0+0xC, 1, tcrUNIE) // handler clears UNF (writes 0 to it)
	if v := mem.Read(u0+0xC, 1); v != tcrUNIE {
		t.Fatalf("UNF not cleared: %#x", v)
	}
	// second period: another request exactly 256 ticks later
	cpu.pending = nil
	cpu.cycles = 1000 + 2*256*30
	mmio.etmu2.tick(cpu)
	if !hasIRQ(cpu, 0x9e0) {
		t.Fatalf("no second interrupt")
	}
	// stop: TCNT freezes
	mem.Write(u0, 1, 0)
	frozen := mem.Read(u0+8, 4)
	cpu.cycles += 5000
	if mem.Read(u0+8, 4) != frozen {
		t.Fatal("stopped channel kept counting")
	}
	// ETMU5 programmed by software -> +0xD8 is its TCNT, not the legacy counter
	const u5 = 0xA44D00D0
	mem.Write(u5+4, 4, 999)
	mem.Write(u5+8, 4, 999)
	if v := mem.Read(0xA44D00D8, 4); v != 999 {
		t.Fatalf("+0xD8 after programming ETMU5 = %d, want 999", v)
	}
}

// TMU0 at Pphi/4 with UNIE requests INTEVT 0x400 on underflow.
func TestTMUChannel(t *testing.T) {
	mmio, mem, cpu := newTimerRig(TMUPphi / 4) // 1 instruction per count at TPSC=0
	const tcor, tcnt, tcr = 0xA4490008, 0xA449000C, 0xA4490010
	mem.Write(tcr, 2, tmuUNIE)
	mem.Write(tcor, 4, 99)
	mem.Write(tcnt, 4, 99)
	cpu.cycles = 500
	mem.Write(0xA4490004, 1, 1)
	if v := mem.Read(0xA4490004, 1); v != 1 {
		t.Fatalf("TSTR = %d", v)
	}
	cpu.cycles = 550
	if v := mem.Read(tcnt, 4); v != 49 {
		t.Fatalf("TCNT = %d, want 49", v)
	}
	cpu.cycles = 599
	mmio.tmu.tick(cpu)
	if hasIRQ(cpu, 0x400) {
		t.Fatal("early interrupt")
	}
	cpu.cycles = 600
	mmio.tmu.tick(cpu)
	if !hasIRQ(cpu, 0x400) {
		t.Fatalf("pending = %+v, want 0x400", cpu.pending)
	}
	if v := mem.Read(tcr, 2); v != tmuUNIE|tmuUNF {
		t.Fatalf("TCR = %#x", v)
	}
	mem.Write(tcr, 2, tmuUNIE)
	if v := mem.Read(tcr, 2); v != tmuUNIE {
		t.Fatalf("UNF not cleared: %#x", v)
	}
}

// DMAC: a channel started with CHCR.IE raises the transfer-end interrupt; without IE (the
// OS's 0x00101400) nothing is requested.
func TestDMACInterrupt(t *testing.T) {
	mmio, mem, cpu := newTimerRig(1_000_000)
	_ = mmio
	mem.Write(0xFE008030, 4, 0x0C000000) // channel 1
	mem.Write(0xFE008034, 4, 0x0C100000)
	mem.Write(0xFE008038, 4, 4)
	mem.Write(0xFE00803C, 4, 0x00101400)
	if hasIRQ(cpu, 0x820) {
		t.Fatal("interrupt without IE")
	}
	mem.Write(0xFE00803C, 4, 0x00101405)
	if !hasIRQ(cpu, 0x820) {
		t.Fatalf("pending = %+v, want 0x820 (DEI1)", cpu.pending)
	}
	if mem.Read(0xFE00803C, 4)&2 == 0 {
		t.Fatal("TE not reported")
	}
	// DE=1 written with TE=1 (a restored register) must not start another transfer
	cpu.pending = nil
	mem.Write(0xFE00803C, 4, 0x00101407)
	if hasIRQ(cpu, 0x820) {
		t.Fatal("restored CHCR with TE set restarted the transfer")
	}
}

// DMAC memory-to-memory: gint's dma_memset fills VRAM from a fixed 32-byte pattern in IL
// memory (SM fixed, DM increment, 32-byte units); dma_memcpy increments both.
func TestDMACMemoryTransfer(t *testing.T) {
	_, mem, _ := newTimerRig(1_000_000)
	for i := uint32(0); i < 32; i += 4 {
		mem.Write(XyramBase+0x200000+i, 4, 0x11223344)
	}
	mem.Write(0xFE008030, 4, 0xE5200000) // channel 1: SAR = IL memory pattern
	mem.Write(0xFE008034, 4, 0x0C0F0000) // DAR: DRAM
	mem.Write(0xFE008038, 4, 8)          // 8 x 32 bytes
	mem.Write(0xFE00803C, 4, 0x00104401) // DM=inc SM=fixed TS=32B DE
	for i := uint32(0); i < 256; i += 4 {
		if v := mem.Read(0xAC0F0000+i, 4); v != 0x11223344 {
			t.Fatalf("memset: [%#x] = %#x", 0x0C0F0000+i, v)
		}
	}
	if v := mem.Read(0xAC0F0100, 4); v == 0x11223344 {
		t.Fatal("memset ran past the count")
	}
	mem.Write(0xAC0F0000, 4, 0xDEADBEEF)
	mem.Write(0xFE008040, 4, 0x0C0F0000) // channel 2
	mem.Write(0xFE008044, 4, 0x0C0F1000)
	mem.Write(0xFE008048, 4, 2)
	mem.Write(0xFE00804C, 4, 0x00105401) // DM=inc SM=inc
	if mem.Read(0xAC0F1000, 4) != 0xDEADBEEF || mem.Read(0xAC0F1004, 4) != 0x11223344 || mem.Read(0xAC0F1020, 4) != 0x11223344 {
		t.Fatal("memcpy did not copy 64 bytes")
	}
}
