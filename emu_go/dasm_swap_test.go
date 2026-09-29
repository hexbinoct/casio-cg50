//go:build probe

package main

// Probe (2026-09-28): run the sibling project's DASM.g3a (september/cg50_addons/dasm) on
// the emulator WITHOUT a Fugue filesystem writer, by hot-swapping the add-in that is
// already in the flash dump (helomelo2: header at flash 0x00db4000, 9 pages, code pages
// 0x00dbb000/0x00dbc000). Its 7 header pages are overwritten with DASM's header; DASM's
// code pages go to the erased region 0x007c0000.. and, once the OS has built the add-in's
// physical page table (0x8c04cf0c[(page-0x300000)>>12], filled at launch, read by the TLB
// miss handler 0x8002c918 which only rejects zero entries), the entries are pointed there.
// The launch maps code page 0 from the FS fragment itself, so page 0/1 are also written over
// helomelo2's two code pages (identical bytes either way).

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDasmSwap(t *testing.T) {
	flash, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		t.Skip("no flash image")
	}
	st, err := os.ReadFile("../os/flash_dump/cg50_state.bin")
	if err != nil {
		t.Skip("no menu save-state")
	}
	// DASM_G3A overrides; otherwise the office layout (may/cg50 + september/cg50_addons)
	// and the home-Mac layout (casio/casio-cg50 + casio/cg50_addons).
	var g3a []byte
	for _, p := range []string{os.Getenv("DASM_G3A"), "../../../september/cg50_addons/dasm/DASM.g3a", "../../cg50_addons/dasm/DASM.g3a"} {
		if p == "" {
			continue
		}
		if g3a, err = os.ReadFile(p); err == nil {
			break
		}
	}
	if g3a == nil {
		t.Skip("no DASM.g3a: ", err)
	}
	const hdr = 0x00db4000
	const free = 0x007c0000
	if !bytes.Equal(flash[hdr:hdr+8], []byte{0xaa, 0xac, 0xbd, 0xaf, 0x90, 0x88, 0x9a, 0x8d}) {
		t.Fatal("helomelo2 header not at 0xdb4000")
	}
	pages := (len(g3a) + 0xfff) >> 12
	if pages < 8 || pages > 7+64 {
		t.Fatalf("unexpected g3a size %d", len(g3a))
	}
	for i := 0; i < (pages-7)<<12; i++ {
		if flash[free+i] != 0xFF {
			t.Fatalf("free region not erased at %#x", free+i)
		}
	}
	padded := make([]byte, pages<<12)
	for i := range padded {
		padded[i] = 0xFF
	}
	copy(padded, g3a)
	// helomelo2's header stays (the OS re-validates headers against the FS entry when it
	// rebuilds the menu and drops a mismatching one); only the code is swapped.
	mode := os.Getenv("DASM_MODE") // control = untouched flash, code = swap code only, full = header too
	if mode == "" {
		mode = "late"
	}
	if mode == "code" || mode == "full" {
		copy(flash[hdr+0x7000:], padded[0x7000:0x9000])  // code pages 0,1 over its code pages
		copy(flash[free:], padded[0x7000:])              // all code pages in the erased area
	}
	if mode == "full" {
		copy(flash[hdr:], padded[:0x7000])
	}
	if mode == "code" || mode == "full" {
		// The OS re-validates the file when it rebuilds the menu: fix the header checksums
		// (fxgxa util.c) for the file as the FS sees it (helomelo2's size, our code inside).
		fsSize := int(^uint32(uint32(flash[hdr+0x10])<<24|uint32(flash[hdr+0x11])<<16|uint32(flash[hdr+0x12])<<8|uint32(flash[hdr+0x13])))
		v := make([]byte, fsSize)
		copy(v, flash[hdr:hdr+0x7000])
		copy(v[0x7000:], padded[0x7000:])
		var cs16 uint16
		for i := 0; i < 8; i++ {
			cs16 += uint16(v[0x7100+2*i])<<8 | uint16(v[0x7101+2*i])
		}
		v[0x16], v[0x17] = ^byte(cs16>>8), ^byte(cs16)
		var cs32 uint32
		for i := 0; i < 0x20; i++ {
			cs32 += uint32(v[i])
		}
		for i := 0x24; i < fsSize-4; i++ {
			cs32 += uint32(v[i])
		}
		v[0x20], v[0x21], v[0x22], v[0x23] = byte(cs32>>24), byte(cs32>>16), byte(cs32>>8), byte(cs32)
		copy(flash[hdr:], v[:0x7000])
		t.Logf("FS size %d: checksum16 %04x, checksum32 %08x (were %02x%02x / %x)", fsSize, cs16, cs32,
			^flash[hdr+0x16], ^flash[hdr+0x17], 0)
	}
	t.Logf("mode %s", mode)
	t.Logf("DASM.g3a: %d bytes, %d pages (7 header + %d code)", len(g3a), pages, pages-7)

	e := NewEmulator(flash)
	if err := e.Resume(st); err != nil {
		t.Fatal(err)
	}
	e.SetInstrPerSecond(70_000_000)
	e.Step(1_000_000)
	menu := make([]byte, fbBytes)
	e.FramebufferRGB565(menu)

	e.InjectKey(3, 5) // ')' = icon J (helomelo2's slot, now DASM)
	reached := false
	for i := 0; i < 60_000_000; i++ {
		if e.cpu.pc == 0x00300000 {
			reached = true
			t.Logf("first add-in instruction after %d steps", i)
			break
		}
		e.Step(1)
		if f := e.Fault(); f != "" {
			t.Fatal(f)
		}
	}
	if !reached {
		savePNG(t, e, "dasm_nolaunch")
		t.Logf("no launch: pc=%08x sleeping=%v at=%v", e.cpu.pc, e.cpu.sleeping, e.mmio.ccn.at)
		hist := map[uint32]int{}
		for i := 0; i < 200; i++ {
			e.Step(10_000)
			hist[e.cpu.pc&^0xff]++
		}
		for pc, n := range hist {
			if n >= 5 {
				t.Logf("    pc %08x..  %d/200", pc, n)
			}
		}
		t.Fatal("the OS never jumped to 0x00300000")
	}
	const tbl = 0x8c04cf0c
	e0 := e.mem.R32(tbl)
	t.Logf("page table: [0]=%08x [1]=%08x [2]=%08x [3]=%08x", e0, e.mem.R32(tbl+4), e.mem.R32(tbl+8), e.mem.R32(tbl+12))
	hi := e0 &^ 0x1fffffff
	if mode == "late" {
		// The FS validated and launched helomelo2; now swap the code underneath it (the OS
		// reads code pages straight from flash through the MMU; no icache in the emulator).
		copy(e.mem.flash[hdr+0x7000:], padded[0x7000:0x9000])
		copy(e.mem.flash[free:], padded[0x7000:])
	}
	if mode != "control" {
		for k := 0; k < pages-7; k++ {
			e.mem.W32(tbl+uint32(4*k), hi|uint32(free+(k<<12)))
		}
		e.mem.W32(tbl+uint32(4*(pages-7)), 0)
		t.Logf("patched %d table entries -> 0x%08x..", pages-7, free)
	}

	// ring of the last interrupt accepts / INTC writes, dumped on a fault
	type ev struct{ what string; va, val, pc, vbr uint32; cyc uint64 }
	ring := make([]ev, 0, 64)
	push := func(x ev) {
		if len(ring) == 64 {
			copy(ring, ring[1:])
			ring = ring[:63]
		}
		ring = append(ring, x)
	}
	irqs := map[uint32]int{}
	type tw struct{ va, val, pc uint32; size uint32; cyc uint64 }
	var timerWrites []tw
	var dmaWrites []tw
	e.mmio.watchBase, e.mmio.watchPC = 0xA44B0000, map[uint32]int{}
	e.mem.mmioHook = func(va, size, val uint32) {
		if va == 0xFF000028 {
			irqs[val]++
			push(ev{"INTEVT", va, val, e.cpu.spc, e.cpu.vbr, e.cpu.cycles})
		} else if va&^0xFFF == 0xA4080000 || va&^0xFFF == 0xA44D0000 || va&^0xFFF == 0xA4490000 || va&^0xFFF == 0xFE008000 {
			push(ev{"W", va, val, e.cpu.pc, e.cpu.vbr, e.cpu.cycles})
			if (va&^0xFFF == 0xA44D0000 || va&^0xFFF == 0xA4490000) && len(timerWrites) < 400 {
				timerWrites = append(timerWrites, tw{va, val, e.cpu.pc, size, e.cpu.cycles})
			}
			if va&^0xFFF == 0xFE008000 && (va&0xF) == 0xC && len(dmaWrites) < 300 {
				dmaWrites = append(dmaWrites, tw{va, val, e.cpu.pc, size, e.cpu.cycles})
			}
		}
	}
	dumpRing := func() {
		for _, x := range ring {
			t.Logf("    %-6s %08x = %08x  pc %08x vbr %08x  @%d", x.what, x.va, x.val, x.pc, x.vbr, x.cyc)
		}
	}
	shot := func(name string, n int) {
		for done := 0; done < n; done += 1_000_000 {
			t0 := time.Now()
			e.Step(1_000_000)
			if d := time.Since(t0); d > 3*time.Second {
				t.Logf("  SLOW chunk %s: %v  pc=%08x cyc=%d due=%d sleeping=%v pending=%v dirty=%v etmuNext=%d tmuNext=%d keyscNext=%d timerNext=%d rtcNext=%d",
					name, d, e.cpu.pc, e.cpu.cycles, e.due, e.cpu.sleeping, e.cpu.pending, e.mmio.dirty,
					e.mmio.etmu2.next(), e.mmio.tmu.next(), e.mmio.keysc.scanNext, e.mmio.timerNext, e.mmio.rtc.nextPeriodic)
				for i := range e.mmio.etmu2.ch {
					c := &e.mmio.etmu2.ch[i]
					if c.running {
						t.Logf("    ETMU%d running tcor=%x tcnt=%x tcr=%x nextUnf=%d", i, c.tcor, c.tcnt, c.tcr, c.nextUnf)
					}
				}
				if d > 30*time.Second {
					t.FailNow()
				}
			}
		}
		savePNG(t, e, name)
		t.Logf("  %-22s pc=%08x sleeping=%v at=%v fault=%q pushes=%d", name, e.cpu.pc, e.cpu.sleeping, e.mmio.ccn.at, e.Fault(), e.Pushes())
	}
	shot("dasm_00_start", 40_000_000)
	if e.Fault() != "" {
		t.Logf("fault: %s  spc=%08x ssr=%08x vbr=%08x pr=%08x", e.Fault(), e.cpu.spc, e.cpu.ssr, e.cpu.vbr, e.cpu.pr)
		dumpRing()
		t.FailNow()
	}
	cur := make([]byte, fbBytes)
	e.FramebufferRGB565(cur)
	if bytes.Equal(cur, menu) {
		t.Log("screen unchanged since the menu: sampling PCs")
		hist := map[uint32]int{}
		for i := 0; i < 200; i++ {
			e.Step(10_000)
			hist[e.cpu.pc&^0xff]++
		}
		for pc, n := range hist {
			if n >= 5 {
				t.Logf("    pc %08x..  %d/200", pc, n)
			}
		}
	}
	press := func(name string, r, c uint32) {
		e.InjectKey(r, c)
		shot(name, 30_000_000)
	}
	// DASM_KEYS=row-col,... replaces the scripted session below: one screenshot per key
	// (cg50_dasm_k01.png, ...), e.g. to try a new DASM build's features, plus the add-in's whole
	// 396x224 frame (cg50_dasm_k01_full.png; the panel view above is the OS's 384x216 window).
	if keys := os.Getenv("DASM_KEYS"); keys != "" {
		for i, k := range strings.Split(keys, ",") {
			var r, c uint32
			if n, _ := fmt.Sscanf(k, "%d-%d", &r, &c); n == 2 {
				press(fmt.Sprintf("dasm_k%02d", i+1), r, c)
				saveAddinFrame(t, e, fmt.Sprintf("dasm_k%02d_full", i+1))
			}
		}
		return
	}
	press("dasm_01_F6_rom", 1, 9)
	t.Logf("INTEVT accepts so far: %v", irqs)
	t.Logf("KEYSC readers (pc: reads): %v", e.mmio.watchPC)
	t.Logf("ETMU0 TSTR=%x TCOR=%x TCNT=%x TCR=%x  IPRJ=%04x IMR6=%02x  SR=%08x", e.mem.R8(0xA44D0030), e.mem.R32(0xA44D0034), e.mem.R32(0xA44D0038), e.mem.R8(0xA44D003C), e.mem.Read(0xA4080024, 2), e.mem.R8(0xA4080098), e.cpu.sr)
	t.Logf("sleep_block_counter=%d  ETMU next=%d cycles=%d", e.mem.R32(0x08105b24), e.mmio.etmu2.next(), e.cpu.cycles)
	for i := range e.mmio.etmu2.ch {
		c := &e.mmio.etmu2.ch[i]
		t.Logf("  ETMU%d running=%v owned=%v tcor=%x tcnt=%x tcr=%x nextUnf=%d", i, c.running, c.owned, c.tcor, c.tcnt, c.tcr, c.nextUnf)
	}
	for i := range e.mmio.tmu.ch {
		c := &e.mmio.tmu.ch[i]
		t.Logf("  TMU%d running=%v owned=%v tcor=%x tcnt=%x tcr=%x nextUnf=%d div=%d", i, c.running, c.owned, c.tcor, c.tcnt, c.tcr, c.nextUnf, e.mmio.tmu.div(i))
	}
	t.Logf("  TSTR=%x IPRA=%04x IMR4=%02x FRQCR=%08x", e.mem.R8(0xA4490004), e.mem.Read(0xA4080000, 2), e.mem.R8(0xA4080090), e.mem.R32(0xA4150000))
	t.Logf("DMAC CHCR writes: %d (IRQ accepts %v)", len(dmaWrites), irqs)
	for _, w := range dmaWrites {
		t.Logf("    w%-2d %08x = %08x  pc %08x  @%d", w.size*8, w.va, w.val, w.pc, w.cyc)
	}
	for _, off := range []uint32{0x0C, 0x1C, 0x2C, 0x3C, 0x60} {
		t.Logf("    DMAC reg +%02x = %08x", off, e.mem.R32(0xFE008000+off))
	}
	dumpRing()
	press("dasm_02_down", 2, 7)
	press("dasm_03_down", 2, 7)
	press("dasm_04_right", 1, 7)
	press("dasm_05_exe", 2, 1)
	press("dasm_06_exit", 3, 7)
	press("dasm_07_F3_hex", 4, 9)
	press("dasm_08_F4_str", 3, 9)
	press("dasm_09_F5_hdr", 2, 9)
	press("dasm_10_F1_open", 6, 9)
	// DASM_PICK=n opens the n-th file of the picker (0 = the first) instead of the first one.
	if n, _ := strconv.Atoi(os.Getenv("DASM_PICK")); n > 0 {
		for i := 0; i < n; i++ {
			e.InjectKey(2, 7) // DOWN
			e.Step(30_000_000)
		}
		savePNG(t, e, "dasm_10b_picked")
	}
	rdPages := map[uint32]int{}
	e.mem.flashRdHook = func(phys, size uint32) {
		if phys >= 0x00c00000 {
			rdPages[phys>>12]++
		}
	}
	press("dasm_11_exe_file", 2, 1)
	// DASM prints the title bar before the rows fetch their pages, so the rd<rc> shown on the
	// first frame is the open call's (always 0); one more frame shows the page reads' rc.
	press("dasm_11b_down", 2, 7)
	e.mem.flashRdHook = nil
	t.Logf("storage pages read while opening the file: %d distinct", len(rdPages))
	type pg struct{ p uint32; n int }
	var lst []pg
	for p, n := range rdPages {
		lst = append(lst, pg{p, n})
	}
	sort.Slice(lst, func(i, j int) bool { return lst[i].n > lst[j].n })
	for i, x := range lst {
		if i >= 12 {
			break
		}
		t.Logf("    page %08x x%-6d %x", x.p<<12, x.n, e.mem.flash[x.p<<12:(x.p<<12)+16])
	}
	press("dasm_12_F5_hdr", 2, 9)
	press("dasm_13_menu", 3, 8)
	if f := e.Fault(); f != "" {
		t.Fatal(f)
	}
	fmt.Println("done")
}

// saveAddinFrame writes the frame a gint add-in last pushed to the LCD, whole: gint draws
// 396x224 (the OS only shows the 384x216 window inside it, which is what savePNG renders).
func saveAddinFrame(t *testing.T, e *Emulator, name string) {
	const w, h = 396, 224
	sar, ok := e.mmio.FrameSAR()
	if !ok {
		t.Logf("%s: no LCD DMA source", name)
		return
	}
	buf := e.mem.span(sar, w*h*2)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for p := 0; p < w*h; p++ {
		c := uint16(buf[2*p])<<8 | uint16(buf[2*p+1])
		img.SetRGBA(p%w, p/w, color.RGBA{uint8(c>>11) << 3, uint8(c>>5&0x3F) << 2, uint8(c&0x1F) << 3, 255})
	}
	path := fmt.Sprintf("%s/cg50_%s.png", os.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Log(err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Log(err)
	}
}
