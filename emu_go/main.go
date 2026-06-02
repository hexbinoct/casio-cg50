package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"
	"strconv"
	"time"
)

// dumpFB decodes a 396x224 RGB565 (big-endian) framebuffer at the given cached
// DRAM address and writes it as a PNG, so we can SEE what the emulator displays.
func dumpFB(mem *Memory, cached uint32, path string) {
	const W, H = 384, 216 // fx-CG50 framebuffer: 384px stride (usable area in the 396x224 panel)
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			p := mem.R16(cached + uint32(y*W+x)*2)
			r := uint8((p>>11)&0x1F) << 3
			g := uint8((p>>5)&0x3F) << 2
			b := uint8(p&0x1F) << 3
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Println("dumpFB:", err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
	fmt.Printf("wrote %s (%dx%d from 0x%08x)\n", path, W, H, cached)
}

// Runner: boot the 3.60 full flash and run, optionally with the periodic timer.
// Usage: go run . [maxIns] [timerPeriod]   (timerPeriod 0 = no interrupts)
func main() {
	maxIns := uint64(2_000_000)
	var timerPeriod uint64 = 0
	if len(os.Args) > 1 {
		maxIns = parseUint(os.Args[1])
	}
	if len(os.Args) > 2 {
		timerPeriod = parseUint(os.Args[2])
	}

	img, err := os.ReadFile("../os/flash_dump/flash_full.bin")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	mmio := NewMMIOBus()
	mem := NewMemory(img, mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu // wire the cpu back-ref so cycle-based MMIO (ETMU down-counter
	// @0xA44D00D8) advances; without it FUN_803742f8's busy-delay never completes.
	mmio.watchPC = map[uint32]int{}
	mmio.watchBase = 0xA4080000 // attribute reads of this region to the reader PC (KEYSC scan)
	cpu.pc = 0x80000000
	mmio.timerPeriod = timerPeriod
	mmio.timerNext = 0

	mode := ""
	if len(os.Args) > 3 {
		mode = os.Args[3]
	}
	if mode == "ftl" {
		runFTL(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "stack" {
		runStack(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "gate" {
		runGate(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "draw" {
		runDraw(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "shelltrace" {
		runShellTrace(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "wmap" {
		runWmap(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "flashwr" {
		runFlashWr(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "drive" {
		// args: [4]=kbReg(hex, -1=all) [5]=kbVal(hex) [6]=pressAtCycle(dec)
		reg := int64(-1)
		val := uint64(0xFFFF)
		pressAt := uint64(110_000_000)
		if len(os.Args) > 4 {
			reg = int64(int32(parseUint(os.Args[4])))
		}
		if len(os.Args) > 5 {
			val = parseUint(os.Args[5])
		}
		if len(os.Args) > 6 {
			pressAt = parseUint(os.Args[6])
		}
		mmio.kbReg = int32(reg)
		mmio.kbVal = uint32(val)
		mmio.kbStart = pressAt
		mmio.kbEnd = pressAt + 3_000_000 // hold ~3M instr
		runDrive(cpu, mem, mmio, maxIns)
		return
	}
	if mode == "key" {
		// args: [4]=row(dec) [5]=col(dec) [6]=pressAtCycle(dec)
		row, col := uint32(5), uint32(3) // default: a mid-matrix key
		pressAt := uint64(160_000_000)
		if len(os.Args) > 4 {
			row = uint32(parseUint(os.Args[4]))
		}
		if len(os.Args) > 5 {
			col = uint32(parseUint(os.Args[5]))
		}
		if len(os.Args) > 6 {
			pressAt = parseUint(os.Args[6])
		}
		runKey(cpu, mem, mmio, maxIns, row, col, pressAt)
		return
	}
	if mode == "seq" {
		// args: [4]="row-col,row-col,..." [5]=pressAt(dec) [6]=interval(dec)
		seqStr := "1-9" // default: one 'advance' (C10R2 = code 0x29)
		pressAt := uint64(130_000_000)
		interval := uint64(8_000_000)
		if len(os.Args) > 4 {
			seqStr = os.Args[4]
		}
		if len(os.Args) > 5 {
			pressAt = parseUint(os.Args[5])
		}
		if len(os.Args) > 6 {
			interval = parseUint(os.Args[6])
		}
		runSeq(cpu, mem, mmio, maxIns, seqStr, pressAt, interval)
		return
	}
	if mode == "prof" {
		runProf(cpu, mem, mmio, maxIns)
		return
	}
	fmt.Printf("booting flash_full.bin (%d bytes); maxIns=%d timer=%d\n", len(img), maxIns, timerPeriod)
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x after %d instr: %v\n", cpu.pc, cpu.cycles, r)
			report(cpu, mmio, start)
		}
	}()

	checkpoint := maxIns / 20
	if checkpoint == 0 {
		checkpoint = 1
	}
	seen := map[uint32]bool{}
	bestNZ := 0
	vramNZ := func() (uint32, int) {
		sar, _ := mmio.FrameSAR()
		if sar == 0 {
			sar = 0x0C000000
		}
		vram := 0x8C000000 | (sar & 0x1FFFFFFF)
		nz := 0
		for k := uint32(0); k < 396*224; k += 8 {
			if mem.R16(vram+k*2) != 0 {
				nz++
			}
		}
		return vram, nz * 8
	}
	for cpu.cycles < maxIns {
		mmio.tick(cpu)
		cpu.step()
		if cpu.cycles&0x3FFFF == 0 { // ~every 256k: watch for new frame pushes
			if sar, ok := mmio.FrameSAR(); ok && !seen[sar] {
				seen[sar] = true
				fmt.Printf("  [%10d] VRAM->LCD push SAR=0x%08x\n", cpu.cycles, sar)
			}
		}
		if cpu.cycles%checkpoint == 0 {
			vram, nz := vramNZ()
			if nz > bestNZ {
				bestNZ = nz
			}
			fmt.Printf("  [%10d] PC=0x%08x SR=0x%08x ticks=%d irqs=%d vram_nz~%d (vram=0x%08x)\n",
				cpu.cycles, cpu.pc, cpu.sr, mmio.timerTicks, cpu.irqCnt, nz, vram)
		}
	}
	fmt.Printf("best vram_nz seen: %d\n", bestNZ)
	report(cpu, mmio, start)
}

func report(cpu *CPU, mmio *MMIOBus, start time.Time) {
	// hottest unmapped MMIO — a new unmodeled register (like the battery ADC was)
	// shows up here as a frequently-polled address returning 0.
	if len(mmio.unknown) > 0 {
		type uk struct {
			a uint32
			n int
		}
		var u []uk
		for a, n := range mmio.unknown {
			u = append(u, uk{a, n})
		}
		sort.Slice(u, func(i, j int) bool { return u[i].n > u[j].n })
		fmt.Printf("top unmapped MMIO (addr x count): ")
		for i := 0; i < len(u) && i < 12; i++ {
			fmt.Printf(" 0x%08x:x%d", u[i].a, u[i].n)
		}
		fmt.Println()
	}
	if len(mmio.watchPC) > 0 {
		type pk struct {
			pc uint32
			n  int
		}
		var p []pk
		for pc, n := range mmio.watchPC {
			p = append(p, pk{pc, n})
		}
		sort.Slice(p, func(i, j int) bool { return p[i].n > p[j].n })
		fmt.Printf("readers of 0x%08x region (pc x count): ", mmio.watchBase)
		for i := 0; i < len(p) && i < 10; i++ {
			fmt.Printf(" 0x%08x:x%d", p[i].pc, p[i].n)
		}
		fmt.Println()
	}
	fmt.Printf("model: *0xfd8018d4=0x%08x *0x8c04ca24=0x%08x strap[0xFF000024]=0x%08x (want 0xca02)\n",
		cpu.mem.R32(0xfd8018d4), cpu.mem.R32(0x8c04ca24), cpu.mem.R32(0xFF000024))
	fmt.Printf("is_erased cmp: flash@0x300=0x%08x  *0x806827a4=0x%08x\n",
		cpu.mem.R32(0xa0000300), cpu.mem.R32(0x806827a4))
	el := time.Since(start).Seconds()
	rate := float64(cpu.cycles) / el / 1e6
	fmt.Printf("\n=== ran %d instr in %.2fs (%.1f M instr/s) ===\n", cpu.cycles, el, rate)
	fmt.Printf("PC=0x%08x SR=0x%08x VBR=0x%08x PR=0x%08x irqs=%d ticks=%d fpu_ops=%d\n",
		cpu.pc, cpu.sr, cpu.vbr, cpu.pr, cpu.irqCnt, mmio.timerTicks, cpu.fpuOps)
	// stack dump: scan the current stack for code-pointer return addresses to
	// reconstruct the call chain that parked the OS here.
	// hunt for a rendered framebuffer ANYWHERE in DRAM (distinguishes "drew but
	// never pushed to LCD" from "never drew at all").
	const FB = 396 * 224 * 2
	dram := cpu.mem.dram
	best, bestOff := 0, uint32(0)
	for off := uint32(0); off+FB < DramSize; off += 0x8000 {
		nz := 0
		for k := uint32(0); k < FB; k += 64 {
			if dram[off+k] != 0 || dram[off+k+1] != 0 {
				nz++
			}
		}
		if nz > best {
			best, bestOff = nz, off
		}
	}
	fmt.Printf("densest DRAM framebuffer-window: phys=0x%08x nz=%d/%d (%.0f%%)\n",
		DramBase+bestOff, best, FB/64, 100*float64(best)/float64(FB/64))
	{ // raw framebuffer bytes for offline stride/orientation exploration
		raw := make([]byte, 0x40000)
		for i := range raw {
			raw[i] = byte(cpu.mem.R8(0x8C000000 + uint32(i)))
		}
		if f, err := os.Create("fb_raw.bin"); err == nil {
			f.Write(raw)
			f.Close()
			fmt.Println("wrote fb_raw.bin (256KB from 0x8c000000)")
		}
	}
	dumpFB(cpu.mem, 0x8C000000, "fb_0c000000.png")
	dumpFB(cpu.mem, 0x8C028800, "fb_0c028800.png")
	dumpFB(cpu.mem, 0x8C088000, "fb_088000.png")
	dumpFB(cpu.mem, 0x8C090000, "fb_090000.png")
	dumpFB(cpu.mem, 0x8C000000|(DramBase+bestOff)&0x1FFFFFFF, "fb_densest.png")

	sp := cpu.r[15]
	fmt.Printf("R15(sp)=0x%08x  call-chain candidates (return addrs on stack):\n", sp)
	for off := uint32(0); off < 0x300; off += 4 {
		w := cpu.mem.R32(sp + off)
		if w >= 0x80001000 && w < 0x80C00000 {
			fmt.Printf("  [sp+0x%03x] 0x%08x\n", off, w)
		}
	}
}

// runFTL hooks the flash translation-layer routines and records their return codes
// (r0 at return) to test whether flash records pass ECC during boot.
func runFTL(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	watch := map[uint32]string{
		0x80371718: "ECC_verify(FUN_80371718)",     // ret 1=clean 2=corrected 3=uncorrectable
		0x80370ff0: "block_read(FUN_80370ff0)",      // ret 0=ok -5=ECC-fail -2/-6=other
		0x803715dc: "rec_verify(FUN_803715dc)",
		0x801885f2: "load_setup(FUN_801885f2)",      // ret -6 on record-validate fail
		0x8018879c: "validate_rec(FUN_8018879c)",    // ret 1=found type0x1d ok, 0=not
	}
	order := []string{"block_read(FUN_80370ff0)", "rec_verify(FUN_803715dc)",
		"ECC_verify(FUN_80371718)", "load_setup(FUN_801885f2)", "validate_rec(FUN_8018879c)"}
	type frame struct {
		name string
		ret  uint32
	}
	var pending []frame
	hist := map[string]map[int32]int{}
	calls := map[string]int{}
	blkHist := map[uint64]int{}
	for _, n := range watch {
		hist[n] = map[int32]int{}
	}
	fmt.Printf("FTL probe: booting up to %d instr, hooking flash ECC/reader returns...\n", maxIns)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x after %d: %v\n", cpu.pc, cpu.cycles, r)
		}
		fmt.Printf("\n=== FTL return-code histograms (after %d instr) ===\n", cpu.cycles)
		for _, n := range order {
			fmt.Printf("%-28s calls=%d  returns:", n, calls[n])
			for code, c := range hist[n] {
				fmt.Printf("  %d:x%d", code, c)
			}
			fmt.Println()
		}
		// block_read(param_1=r4, param_2=r5): param_2 is the record/block number.
		// distinct count + most-read blocks tells progressing-scan vs stuck-reread.
		fmt.Printf("\nblock_read args: %d distinct (r4,r5) keys\n", len(blkHist))
		type bk struct {
			key uint64
			n   int
		}
		var top []bk
		for k, n := range blkHist {
			top = append(top, bk{k, n})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
		for i := 0; i < len(top) && i < 25; i++ {
			fmt.Printf("  r4=%d r5=0x%x  x%d\n", int32(top[i].key>>32), uint32(top[i].key), top[i].n)
		}
	}()
	for cpu.cycles < maxIns {
		pc := cpu.pc
		if pc == 0x80370ff0 {
			blkHist[uint64(cpu.r[4])<<32|uint64(cpu.r[5])]++
		}
		if name, ok := watch[pc]; ok {
			pending = append(pending, frame{name, cpu.pr})
			calls[name]++
		}
		if len(pending) > 0 && pc == pending[len(pending)-1].ret {
			f := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			hist[f.name][int32(cpu.r[0])]++
		}
		mmio.tick(cpu)
		cpu.step()
	}
}

// runShellTrace records the control flow through the shell FUN_802aea26 for ONE
// entry (after warmup): every in-function PC that is a branch/call site, and the
// r0 value each time execution returns INTO the shell from a callee. This shows
// which conditional branches the shell takes and which app-launch/draw call is
// skipped. lo..hi bound the shell body; the function returns via rts @0x802af3d8.
func runShellTrace(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	const lo, hi = 0x802aea26, 0x802af3e0
	warm := uint64(0) // capture the FIRST shell entry (the app-launch one, post-ADC-fix)
	armed, done := false, false
	prevIn := false
	var lines []string
	add := func(s string) {
		if len(lines) < 700 {
			lines = append(lines, s)
		}
	}
	fmt.Printf("SHELLTRACE: capturing one FUN_802aea26 entry after warmup=%d\n", warm)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x: %v\n", cpu.pc, r)
		}
		fmt.Printf("=== shell control-flow trace (%d events) ===\n", len(lines))
		for _, s := range lines {
			fmt.Println(s)
		}
	}()
	for cpu.cycles < maxIns && !done {
		pc := cpu.pc
		in := pc >= lo && pc < hi
		if !armed && in && cpu.cycles >= warm {
			armed = true
			add(fmt.Sprintf("ENTER @%d r4=%d r5=%d", cpu.cycles, cpu.r[4], cpu.r[5]))
		}
		if armed {
			if in && !prevIn {
				// returned into the shell from a callee: log r0
				add(fmt.Sprintf("  <- ret pc=0x%08x r0=0x%x (%d)", pc, cpu.r[0], int32(cpu.r[0])))
			}
			if in {
				op := mem.R16(pc)
				// log branch/call ops to keep the trace readable
				hb := op >> 12
				if hb == 0x8 /*bt/bf*/ || hb == 0xA /*bra*/ || hb == 0xB /*bsr*/ ||
					op == 0x000b /*rts*/ || (op&0xF0FF) == 0x402b /*jmp @Rn*/ ||
					(op&0xF0FF) == 0x400b /*jsr @Rn*/ {
					add(fmt.Sprintf("    0x%08x op=%04x", pc, op))
				}
				if pc == 0x802af3d8 { // rts
					add(fmt.Sprintf("RETURN @%d", cpu.cycles))
					done = true
				}
			}
			prevIn = in
		}
		mmio.tick(cpu)
		cpu.step()
	}
}

// runDraw watches the framebuffer (DRAM @ the pushed SAR) for ANY change and logs
// the PC + nonzero-count when it changes — finding whether/where the OS draws after
// the initial screen-clear. Checks every 2048 instr.
func runDraw(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	const fbCached = 0x8C000000 // the SAR pushed after the battery-ADC fix (full buffer base)
	const fbLen = 396 * 224 * 2
	snap := func() (uint64, int) {
		var h uint64 = 1469598103934665603
		nz := 0
		for k := uint32(0); k < fbLen; k += 2 {
			v := mem.R16(fbCached + k)
			if v != 0 {
				nz++
			}
			h = (h ^ uint64(v)) * 1099511628211
		}
		return h, nz
	}
	last, _ := snap()
	changes := 0
	fmt.Printf("DRAW: watching FB @0x%08x (%d B) for changes over %d instr\n", fbCached, fbLen, maxIns)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x after %d: %v\n", cpu.pc, cpu.cycles, r)
		}
		fmt.Printf("=== %d framebuffer changes detected ===\n", changes)
	}()
	for cpu.cycles < maxIns {
		mmio.tick(cpu)
		cpu.step()
		if cpu.cycles&0x7FF == 0 {
			h, nz := snap()
			if h != last {
				changes++
				if changes <= 60 {
					fmt.Printf("  [%11d] FB changed: nz=%d pc=0x%08x pr=0x%08x\n", cpu.cycles, nz, cpu.pc, cpu.pr)
				}
				last = h
			}
		}
	}
}

// runGate counts entries to specific functions in the 3.60 os_main_loop and
// histograms their return value (r0 at the matching return PC), to see which
// gate condition prevents the menu-drawing shell (FUN_802aea26) from running.
func runGate(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	names := map[uint32]string{
		0x8035e1be: "FUN_8035e1be",
		0x80056236: "s_80056236(7)",
		0x8035d234: "s_8035d234",
		0x8035cf58: "s_8035cf58",
		0x8035c7c0: "s_8035c7c0",
		0x8035cbfc: "s_8035cbfc",
		0x801de9ca: "is_erased",
	}
	order := []uint32{0x8035e1be, 0x80056236, 0x8035d234, 0x8035cf58, 0x8035c7c0, 0x8035cbfc, 0x801de9ca}
	calls := map[uint32]int{}
	rets := map[uint32]map[int32]int{}
	for a := range names {
		rets[a] = map[int32]int{}
	}
	type fr struct {
		a   uint32
		ret uint32
	}
	var stk []fr
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x after %d: %v\n", cpu.pc, cpu.cycles, r)
		}
		fmt.Printf("\n=== os_main_loop gate counts (after %d instr) ===\n", cpu.cycles)
		for _, a := range order {
			fmt.Printf("%-32s calls=%-8d returns:", names[a], calls[a])
			for code, c := range rets[a] {
				fmt.Printf("  %d:x%d", code, c)
			}
			fmt.Println()
		}
	}()
	for cpu.cycles < maxIns {
		pc := cpu.pc
		if nm, ok := names[pc]; ok && nm != "" {
			calls[pc]++
			stk = append(stk, fr{pc, cpu.pr})
		}
		if n := len(stk); n > 0 && pc == stk[n-1].ret {
			rets[stk[n-1].a][int32(cpu.r[0])]++
			stk = stk[:n-1]
		}
		mmio.tick(cpu)
		cpu.step()
	}
}

// runDrive boots to the UI, injects a KEYSC keypress (configured on mmio), and dumps
// a PNG every 15M instr so we can WATCH the screen respond. fb @0x8c000000, 384x216.
func runDrive(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	fmt.Printf("DRIVE: kbReg=%d kbVal=0x%x press@[%d,%d) maxIns=%d\n",
		mmio.kbReg, mmio.kbVal, mmio.kbStart, mmio.kbEnd, maxIns)
	next := uint64(15_000_000)
	frame := 0
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x: %v\n", cpu.pc, r)
		}
		dumpFB(mem, 0x8C000000, fmt.Sprintf("drive_final.png"))
	}()
	for cpu.cycles < maxIns {
		mmio.tick(cpu)
		cpu.step()
		if cpu.cycles >= next {
			dumpFB(mem, 0x8C000000, fmt.Sprintf("drive_%02d.png", frame))
			frame++
			next += 15_000_000
		}
	}
}

// Key-event queue layout (3.60 keyboard driver @0x801e0000). Each DAT below holds
// a runtime pointer/base set up at driver init; the enqueue FUN_801e684c and the
// peek FUN_801e6994 operate on these. We read them to inspect/inject the queue.
const (
	kqCountPtr = 0x801e6a1c // -> int* count (entries currently queued, 0..16)
	kqWidxPtr  = 0x801e6a20 // -> int* write index (0..15, wraps)
	kqRowBase  = 0x801e6a24 // -> byte[16] row buffer base (stores row+1)
	kqColBase  = 0x801e6a28 // -> byte[16] col buffer base (stores col+1)
	kqModBase  = 0x801e6a2c // -> byte[16] modifier buffer base
	kqRidxPtr  = 0x801e6a40 // -> int* read index (consumer side, FUN_801e6994)
)

func dumpKQ(mem *Memory, tag string) {
	cntP := mem.R32(kqCountPtr)
	wiP := mem.R32(kqWidxPtr)
	riP := mem.R32(kqRidxPtr)
	rowB := mem.R32(kqRowBase)
	colB := mem.R32(kqColBase)
	fmt.Printf("  KQ[%s]: cntPtr=0x%08x wiPtr=0x%08x riPtr=0x%08x rowBase=0x%08x colBase=0x%08x\n",
		tag, cntP, wiP, riP, rowB, colB)
	if cntP != 0 && wiP != 0 && riP != 0 && rowB != 0 && colB != 0 {
		fmt.Printf("         count=%d widx=%d ridx=%d  rows=[", mem.R32(cntP), mem.R32(wiP), mem.R32(riP))
		for i := uint32(0); i < 16; i++ {
			fmt.Printf("%02x ", mem.R8(rowB+i))
		}
		fmt.Printf("] cols=[")
		for i := uint32(0); i < 16; i++ {
			fmt.Printf("%02x ", mem.R8(colB+i))
		}
		fmt.Println("]")
	}
}

// runKey boots to the language screen, then injects a single keypress (matrix
// row,col) by CALLING the OS's own scan-enqueue routine FUN_801e684c as a
// subroutine — the faithful path: the key lands in exactly the queue the UI
// consumes, with the driver's own dedup/format. Dumps a PNG every 15M instr.
func runKey(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64, row, col uint32, pressAt uint64) {
	fmt.Printf("KEY: inject (row=%d,col=%d) at cycle>=%d, maxIns=%d\n", row, col, pressAt, maxIns)
	injected := false
	next := uint64(15_000_000)
	frame := 0
	safe := func() bool {
		// not in an exception (BL clear), not inside the keyboard driver, not in
		// the early vector/dispatch region -> the queue state is consistent.
		if cpu.sr&srBL != 0 {
			return false
		}
		if cpu.pc >= 0x801e0000 && cpu.pc < 0x801f0000 {
			return false
		}
		return cpu.pc >= 0x80002000 && cpu.pc < 0x80c00000
	}
	fbHash := func() uint64 {
		var h uint64 = 1469598103934665603
		for k := uint32(0); k < 384*216*2; k += 2 {
			h = (h ^ uint64(mem.R16(0x8C000000+k))) * 1099511628211
		}
		return h
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x: %v\n", cpu.pc, r)
		}
		dumpKQ(mem, "final")
		fmt.Printf("FBHASH row=%d col=%d -> %016x\n", row, col, fbHash())
		dumpFB(mem, 0x8C000000, "key_final.png")
	}()
	for cpu.cycles < maxIns {
		mmio.tick(cpu)
		cpu.step()
		if !injected && cpu.cycles >= pressAt && safe() {
			fmt.Printf("[%d] injecting at pc=0x%08x sp=0x%08x\n", cpu.cycles, cpu.pc, cpu.r[15])
			dumpKQ(mem, "pre")
			sp := cpu.r[15]
			keybuf := sp - 8
			mem.W8(keybuf, row)
			mem.W8(keybuf+1, col)
			ret := cpu.callInject(0x801e684c, keybuf, 0)
			fmt.Printf("  FUN_801e684c returned %d\n", ret)
			dumpKQ(mem, "post")
			injected = true
		}
		if cpu.cycles >= next {
			dumpKQ(mem, fmt.Sprintf("f%02d", frame))
			dumpFB(mem, 0x8C000000, fmt.Sprintf("key_%02d.png", frame))
			frame++
			next += 15_000_000
		}
	}
}

// injectKey enqueues one matrix keypress (0-based row,col) by calling the OS's
// own scan-enqueue routine FUN_801e684c as a subroutine. Returns its result.
func injectKey(cpu *CPU, mem *Memory, row, col uint32) uint32 {
	sp := cpu.r[15]
	keybuf := sp - 8
	mem.W8(keybuf, row)
	mem.W8(keybuf+1, col)
	return cpu.callInject(0x801e684c, keybuf, 0)
}

func keySafe(cpu *CPU) bool {
	if cpu.sr&srBL != 0 {
		return false
	}
	if cpu.pc >= 0x801e0000 && cpu.pc < 0x801f0000 {
		return false
	}
	return cpu.pc >= 0x80002000 && cpu.pc < 0x80c00000
}

// runSeq drives the first-boot setup UI by injecting a SEQUENCE of keypresses
// ("row-col,row-col,...", 0-based matrix coords) spaced `interval` instructions
// apart starting at `pressAt`, dumping a PNG after each so we can watch the setup
// flow advance toward the main menu. Known keys (grid C,R -> inject row=R-1,col=C-1):
//   DOWN = C8R3 (2-7) | advance/Next = C10R2 (1-9) | SHIFT = C9R7 (6-8)
func runSeq(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64, seqStr string, pressAt, interval uint64) {
	type key struct{ row, col uint32 }
	var seq []key
	for _, tok := range splitComma(seqStr) {
		var r, c uint32
		if n, _ := fmt.Sscanf(tok, "%d-%d", &r, &c); n == 2 {
			seq = append(seq, key{r, c})
		}
	}
	fmt.Printf("SEQ: %d keys %v, press@%d every %d, maxIns=%d\n", len(seq), seq, pressAt, interval, maxIns)
	idx := 0
	nextPress := pressAt
	frame := 0
	nextFrame := pressAt + interval/2
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x: %v\n", cpu.pc, r)
		}
		var h uint64 = 1469598103934665603
		for k := uint32(0); k < 384*216*2; k += 2 {
			h = (h ^ uint64(mem.R16(0x8C000000+k))) * 1099511628211
		}
		fmt.Printf("FBHASH seq -> %016x\n", h)
		dumpFB(mem, 0x8C000000, "seq_final.png")
	}()
	for cpu.cycles < maxIns {
		mmio.tick(cpu)
		cpu.step()
		if idx < len(seq) && cpu.cycles >= nextPress && keySafe(cpu) {
			k := seq[idx]
			ret := injectKey(cpu, mem, k.row, k.col)
			fmt.Printf("[%d] key#%d (row=%d,col=%d) -> %d (pc=0x%08x)\n", cpu.cycles, idx, k.row, k.col, ret, cpu.pc)
			idx++
			nextPress += interval
		}
		if cpu.cycles >= nextFrame {
			dumpFB(mem, 0x8C000000, fmt.Sprintf("seq_%02d.png", frame))
			frame++
			nextFrame += interval
		}
	}
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, ch := range s {
		if ch == ',' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(ch)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// runFlashWr histograms FLASH write targets (which we currently ignore). If the FS
// mount/format is trying to program/erase flash, this reveals it — confirming whether
// "ignore flash writes" is what breaks the fls0 mount. Logs first writes in detail.
func runFlashWr(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	warm := maxIns / 4
	mem.fwrites = map[uint32]int{}
	mem.fwLog = 40
	fmt.Printf("FLASHWR: %d instr, logging flash writes (warmup=%d for histogram)\n", maxIns, warm)
	armed := false
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x: %v\n", cpu.pc, r)
		}
		type kv struct {
			page uint32
			n    int
		}
		var top []kv
		var total int
		for p, n := range mem.fwrites {
			top = append(top, kv{p, n})
			total += n
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
		fmt.Printf("=== flash write pages (32KB), total=%d ===\n", total)
		for i := 0; i < len(top) && i < 20; i++ {
			fmt.Printf("  phys=0x%08x  x%d\n", top[i].page, top[i].n)
		}
	}()
	for cpu.cycles < maxIns {
		if !armed && cpu.cycles >= warm {
			mem.fwrites = map[uint32]int{} // reset to capture steady-state only
			armed = true
		}
		mmio.tick(cpu)
		cpu.step()
	}
}

// runWmap histograms DRAM write targets (by 32KB page) after a warmup, to find
// WHERE the menu actually draws (the visible buffers @0x8c000000/0x8c028800 stay
// black, so the menu paints elsewhere). Top pages reveal the real draw buffer.
func runWmap(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	warm := maxIns / 4
	fmt.Printf("WMAP: %d instr, histogram DRAM write pages after warmup=%d\n", maxIns, warm)
	armed := false
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x: %v\n", cpu.pc, r)
		}
		type kv struct {
			page uint32
			n    int
		}
		var top []kv
		for p, n := range mem.wpages {
			top = append(top, kv{p, n})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
		fmt.Printf("=== hottest DRAM write pages (32KB) ===\n")
		for i := 0; i < len(top) && i < 20; i++ {
			fmt.Printf("  cached=0x%08x  x%d\n", 0x8C000000+top[i].page, top[i].n)
		}
	}()
	for cpu.cycles < maxIns {
		if !armed && cpu.cycles >= warm {
			mem.wpages = map[uint32]int{}
			armed = true
		}
		mmio.tick(cpu)
		cpu.step()
	}
}

// runStack periodically scans the stack for code return-addresses and histograms
// them. Frequent high-level frames reveal the stable steady-state call chain (the
// loop anchor), which the PC histogram can't show. Skips the first 1/4 (boot).
func runStack(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	hist := map[uint32]uint64{}
	var samples uint64
	warm := maxIns / 4
	fmt.Printf("STACK: %d instr, sampling return-addrs after warmup=%d\n", maxIns, warm)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x after %d: %v\n", cpu.pc, cpu.cycles, r)
		}
		type kv struct {
			pc uint32
			n  uint64
		}
		var top []kv
		for pc, n := range hist {
			top = append(top, kv{pc, n})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
		fmt.Printf("\n=== return-addrs seen on stack (%d samples) — pct = in how many stacks ===\n", samples)
		for i := 0; i < len(top) && i < 45; i++ {
			fmt.Printf("  0x%08x  %5.1f%%  (x%d)\n", top[i].pc, 100*float64(top[i].n)/float64(samples), top[i].n)
		}
	}()
	for cpu.cycles < maxIns {
		mmio.tick(cpu)
		cpu.step()
		if cpu.cycles >= warm && cpu.cycles&0x3FFF == 0 { // sample every ~16k instr
			samples++
			sp := cpu.r[15]
			seen := map[uint32]bool{}
			for off := uint32(0); off < 0x400; off += 4 {
				w := mem.R32(sp + off)
				// code addr in OS (0x80001000..0x80c00000), dedup per-stack
				if w >= 0x80001000 && w < 0x80C00000 && !seen[w] {
					seen[w] = true
					hist[w]++
				}
			}
		}
	}
}

// runProf samples PC into 64-byte buckets to find the steady-state hot loop.
// Skips the first 1/4 (boot) so the post-delay churn dominates the histogram.
func runProf(cpu *CPU, mem *Memory, mmio *MMIOBus, maxIns uint64) {
	hist := map[uint32]uint64{}
	warm := maxIns / 4
	fmt.Printf("PROF: %d instr, sampling PC after warmup=%d (64B buckets)\n", maxIns, warm)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("FAULT @0x%08x after %d: %v\n", cpu.pc, cpu.cycles, r)
		}
		type kv struct {
			pc  uint32
			n   uint64
		}
		var top []kv
		var total uint64
		for pc, n := range hist {
			top = append(top, kv{pc, n})
			total += n
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
		fmt.Printf("\n=== hottest PC buckets (sampled %d) ===\n", total)
		for i := 0; i < len(top) && i < 40; i++ {
			fmt.Printf("  0x%08x  %8d  %5.1f%%\n", top[i].pc, top[i].n, 100*float64(top[i].n)/float64(total))
		}
	}()
	for cpu.cycles < maxIns {
		mmio.tick(cpu)
		cpu.step()
		if cpu.cycles >= warm {
			hist[cpu.pc&^0x3F]++
		}
	}
}

func parseUint(s string) uint64 {
	base := 10
	if len(s) > 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		s = s[2:]
		base = 16
	}
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0
	}
	return v
}
