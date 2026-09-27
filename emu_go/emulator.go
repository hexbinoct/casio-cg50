package main

// Emulator is a self-contained facade over the SH7305 core (CPU + memory + MMIO) with a
// thread-safe key interface, framebuffer access, save-state, and a real-time paced run loop.
// It is the single API a host UI drives — the desktop web server here, and (via the cgo
// bridge in android_bridge.go) an Android app. Typical lifecycle:
//
//	e := NewEmulator(flash)
//	e.Resume(savedSnapshot)            // instant: lands at the MAIN MENU
//	go e.RunRealtime(20e6, 60, blit, stop)
//	e.InjectKey(row, col)             // a tap, from the UI thread, anytime
//	e.KeyDown(row, col); e.KeyUp(...) // or hold a key (OS key-repeat works)
//
// All public methods are safe to call concurrently with the run loop.
//
// Keys reach the OS exactly as on the real calculator: they set bits in the KEYSC key-scan
// unit's matrix (keysc.go), the unit raises INTEVT 0xBE0, and the OS's own keyboard ISR
// scans, debounces, repeats and enqueues. There is no shortcut into the OS key queue any
// more (the old enqueue-call injector was flushed by redraws and needed a 20M-cycle retry
// timeout — the menu "reversal hang"; cont.18l).

import (
	"fmt"
	"sync"
	"time"
)

// Framebuffer geometry (RGB565 big-endian, at phys 0x0C000000 = start of DRAM).
const (
	FbWidth  = 384
	FbHeight = 216
	fbBytes  = FbWidth * FbHeight * 2
)

type Emulator struct {
	cpu  *CPU
	mem  *Memory
	mmio *MMIOBus

	mu  sync.Mutex // serialises Step vs Snapshot/Resume/keys
	tap keyTapper  // queued taps -> matrix press/release edges (drained by Step)

	downAt map[[2]uint32]uint64 // KeyDown cycle per held key (minimum-hold enforcement)

	// due is the first cycle at which a device tick or the key tapper has work (see nextDue);
	// Step runs the CPU alone until then. exactTick forces the old per-instruction polling
	// (tests compare the two).
	due       uint64
	exactTick bool

	// fault is set when the core hit something it cannot execute (an unmapped access, an
	// unimplemented instruction): the machine halts there instead of the panic killing the
	// host process (on Android that closed the app). Resume clears it.
	fault     string
	pendingUp []pendingRelease // KeyUp releases deferred until the minimum hold elapses

	// What the user sees is the LCD controller's GRAM (mmio.lcd, lcd.go): the OS updates it
	// by DMA-pushing VRAM (whole frames, so a redraw in progress — the "no graphics driver"
	// look — is never visible) and by direct pixel writes (the blinking text cursor, which
	// never touches VRAM). pushes counts the DMA frame pushes (diagnostics/tests).
	pushes uint64
	rgb565 []byte // scratch for FramebufferRGBA

	// dbg, if set (by the Android bridge), receives one-line key-path diagnostics.
	// nil on desktop/tests.
	dbg func(string)
}

// NewEmulator builds a fresh machine ready to boot from reset (pc = 0x80000000). Call
// Resume to instead jump to a saved state (e.g. the MAIN MENU) without re-running boot.
func NewEmulator(flash []byte) *Emulator {
	mmio := NewMMIOBus()
	mem := NewMemory(flash, mmio)
	cpu := NewCPU(mem)
	mmio.cpu = cpu
	mem.cpu = cpu
	cpu.pc = 0x80000000
	if mmio.timerPeriod == 0 {
		mmio.timerPeriod = 30000 // proven boot timer cadence
	}
	mmio.timerNext = 0
	e := &Emulator{cpu: cpu, mem: mem, mmio: mmio, downAt: map[[2]uint32]uint64{}, rgb565: make([]byte, fbBytes)}
	mmio.dmac.onLCDPush = e.presentFrame
	return e
}

// presentFrame counts a VRAM->LCD DMA push (the DMAC has already streamed it into GRAM).
// Called from the bus while Step holds mu.
func (e *Emulator) presentFrame(sar uint32) { e.pushes++ }

// Pushes returns how many VRAM->LCD pushes the OS has made (frames actually presented).
func (e *Emulator) Pushes() uint64 { e.mu.Lock(); defer e.mu.Unlock(); return e.pushes }

// InjectKey queues a tap of the matrix key at 0-based (row,col); see re/KEYMAP.md. The key
// is held for a few hardware scans then released, and queued taps are spaced so each one is
// seen by the OS. SHIFT/ALPHA are themselves keys — tap the modifier before the target.
func (e *Emulator) InjectKey(row, col uint32) {
	e.mu.Lock()
	e.tap.queue = append(e.tap.queue, [2]uint32{row, col})
	e.mu.Unlock()
	if e.dbg != nil {
		e.dbg(fmt.Sprintf("tap r=%d c=%d", row, col))
	}
}

// KeyDown / KeyUp press and release a matrix key for as long as the host holds it (e.g. a
// touch down/up on an on-screen button). Holding an arrow key auto-repeats, as on the calc.
// A press is guaranteed to stay visible to the scanner for at least tapHoldScans scans: if
// the host's up arrives sooner (a fast tap can deliver down+up while Step holds the mutex
// for a whole slice, i.e. before a single instruction runs in between), the release is
// deferred so the OS ISR still sees the key.
func (e *Emulator) KeyDown(row, col uint32) {
	e.mu.Lock()
	e.mmio.keysc.press(row, col)
	e.downAt[[2]uint32{row, col}] = e.cpu.cycles
	cyc := e.cpu.cycles
	e.mu.Unlock()
	if e.dbg != nil {
		e.dbg(fmt.Sprintf("down r=%d c=%d cyc=%d", row, col, cyc))
	}
}

func (e *Emulator) KeyUp(row, col uint32) {
	e.mu.Lock()
	key := [2]uint32{row, col}
	minHold := tapHoldScans * e.mmio.keysc.scanPeriod
	at, ok := e.downAt[key]
	deferred := false
	if ok && e.cpu.cycles-at < minHold {
		e.pendingUp = append(e.pendingUp, pendingRelease{key, at + minHold})
		deferred = true
	} else {
		e.mmio.keysc.release(row, col)
	}
	delete(e.downAt, key)
	cyc := e.cpu.cycles
	e.mu.Unlock()
	if e.dbg != nil {
		e.dbg(fmt.Sprintf("up   r=%d c=%d cyc=%d deferred=%v", row, col, cyc, deferred))
	}
}

type pendingRelease struct {
	key [2]uint32
	at  uint64
}

// flushReleases applies deferred KeyUp releases whose minimum hold has elapsed. Caller holds mu.
func (e *Emulator) flushReleases() {
	now := e.cpu.cycles
	kept := e.pendingUp[:0]
	for _, p := range e.pendingUp {
		if now >= p.at {
			e.mmio.keysc.release(p.key[0], p.key[1])
		} else {
			kept = append(kept, p)
		}
	}
	e.pendingUp = kept
}

// SetKeyScanPeriod sets the KEYSC hardware scan interval in instructions. It is also the OS
// key-repeat clock (one repeat per scan after a 20-scan delay), so hosts should derive it
// from their real throughput: ~20 ms worth of instructions (see keysc.go).
func (e *Emulator) SetKeyScanPeriod(instr uint64) {
	if instr < 1000 {
		instr = 1000
	}
	e.mu.Lock()
	e.mmio.keysc.scanPeriod = instr
	e.mu.Unlock()
}

// SetInstrPerSecond tells the core the host's real throughput (instructions per wall-clock
// second) so the OS's time base (RTC periodic interrupt = cursor blink and idle heartbeat,
// the 32.768 kHz counter, the 33 Hz key scan) runs at real speed. Hosts should call it at
// start with their budget and again whenever the measured rate changes materially.
func (e *Emulator) SetInstrPerSecond(ips uint64) {
	e.mu.Lock()
	e.mmio.SetInstrPerSecond(ips)
	e.mu.Unlock()
}

// SetClock sets the emulated RTC calendar to unix seconds.
func (e *Emulator) SetClock(unix int64) {
	e.mu.Lock()
	e.mmio.rtc.SetClock(unix)
	e.mu.Unlock()
}

// ReleaseAllKeys clears the matrix and drops queued taps (e.g. when the host loses focus).
func (e *Emulator) ReleaseAllKeys() {
	e.mu.Lock()
	e.mmio.keysc.releaseAll()
	e.tap = keyTapper{}
	e.pendingUp = nil
	for k := range e.downAt {
		delete(e.downAt, k)
	}
	e.mu.Unlock()
}

// Step advances the machine by n instructions, driving queued taps onto the matrix. If the
// core faults, the machine halts (see Fault) rather than panicking into the host.
func (e *Emulator) Step(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fault != "" {
		return
	}
	defer func() {
		if x := recover(); x != nil {
			e.fault = fmt.Sprintf("core halted at pc=%08x: %v", e.cpu.pc, x)
			if e.dbg != nil {
				e.dbg(e.fault)
			}
		}
	}()
	// Devices only act at scheduled cycles (KEYSC scan, periodic timer, RTC periodic IRQ, key
	// tapper press/release), so polling them before every instruction is wasted work. The slow
	// path below is exactly the old per-instruction loop body; it runs whenever something may
	// be due: at/after `due`, after any MMIO write (event times may have moved), with deferred
	// key releases pending, or at the first iteration (host calls happen between Steps). In
	// between, the CPU runs alone — and while it sleeps with nothing pending, time jumps
	// straight to the next event (the old loop only incremented cycles there).
	end := e.cpu.cycles + uint64(n)
	e.mmio.dirty = true
	for e.cpu.cycles < end {
		if e.mmio.dirty || e.cpu.cycles >= e.due || e.exactTick || len(e.pendingUp) != 0 {
			e.mmio.dirty = false
			e.mmio.tick(e.cpu)
			e.cpu.step()
			e.tap.drive(e.mmio.keysc, e.cpu.cycles)
			if len(e.pendingUp) != 0 {
				e.flushReleases()
			}
			e.due = e.nextDue()
			continue
		}
		if e.cpu.sleeping {
			e.due = e.nextDue() // entering sleep can bring an ADC completion forward
		}
		stop := min(e.due, end)
		if e.cpu.sleeping {
			if len(e.cpu.pending) == 0 {
				e.cpu.idle += stop - e.cpu.cycles
				e.cpu.cycles = stop // nothing can wake it before the next event
			} else {
				e.cpu.step() // a pending request wakes it
			}
			continue
		}
		e.cpu.run(stop, &e.mmio.dirty)
	}
}

// nextDue is the earliest cycle at which the slow path in Step has anything to do: a device
// tick (acts when cycles >= its time) or the key tapper (acts after the step that reaches its
// time, i.e. from cycles == time-1).
func (e *Emulator) nextDue() uint64 {
	m := e.mmio
	d := m.keysc.scanNext
	if m.timerPeriod != 0 {
		if p := m.periphIRQ; !p.adcMode {
			d = min(d, m.timerNext)
		} else {
			if p.swArmed {
				d = min(d, p.swDoneAt)
			}
			if p.armed && e.cpu.sleeping { // sleeping cycles advance cycles and idle together
				d = min(d, e.cpu.cycles+(p.doneAt-min(p.doneAt, e.cpu.idle)))
			}
		}
		if m.rtc.rcr2&0x70 != 0 {
			d = min(d, m.rtc.nextPeriodic)
		}
	}
	tapAt := uint64(0)
	switch {
	case e.tap.active:
		tapAt = e.tap.releaseAt
	case len(e.tap.queue) != 0:
		tapAt = e.tap.nextAt
	default:
		return d
	}
	if tapAt > 0 {
		tapAt--
	}
	return min(d, tapAt)
}

// Executed returns how many instructions the CPU has actually executed (cycles spent asleep
// excluded). Hosts size their per-frame budget from executed-instructions per host second —
// the machine's real capacity — rather than from cheap idle frames.
func (e *Emulator) Executed() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cpu.cycles - e.cpu.idle
}

// Fault returns why the core halted ("" while running normally).
func (e *Emulator) Fault() string { e.mu.Lock(); defer e.mu.Unlock(); return e.fault }

// FramebufferRGB565 copies the displayed 384x216 RGB565 (big-endian) frame into dst
// (>= fbBytes): the LCD panel's contents (GRAM), not the live VRAM.
func (e *Emulator) FramebufferRGB565(dst []byte) {
	e.mu.Lock()
	e.mmio.lcd.renderOS(dst)
	e.mu.Unlock()
}

// VRAMRGB565 copies the LIVE VRAM (phys 0x0C000000), including drawing in progress; for
// diagnostics and tests that watch the OS draw.
func (e *Emulator) VRAMRGB565(dst []byte) {
	e.mu.Lock()
	copy(dst, e.mem.dram[:fbBytes])
	e.mu.Unlock()
}

// FramebufferRGBA decodes the presented frame into dst as 8-bit RGBA (FbWidth*FbHeight*4
// bytes), the layout Android's Bitmap.copyPixelsFromBuffer / a host canvas expects.
func (e *Emulator) FramebufferRGBA(dst []byte) {
	e.mu.Lock()
	d := e.rgb565
	e.mmio.lcd.renderOS(d)
	for i := 0; i < FbWidth*FbHeight; i++ {
		p := uint16(d[i*2])<<8 | uint16(d[i*2+1])
		dst[i*4+0] = uint8((p>>11)&0x1F) << 3
		dst[i*4+1] = uint8((p>>5)&0x3F) << 2
		dst[i*4+2] = uint8(p&0x1F) << 3
		dst[i*4+3] = 0xFF
	}
	e.mu.Unlock()
}

// Snapshot / Resume persist or restore the full machine (CPU + RAM + flash delta) as a
// gzip blob the host stores. Resume lands the machine exactly where Snapshot was taken.
func (e *Emulator) Snapshot() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return SnapshotBytes(e.cpu, e.mem)
}

func (e *Emulator) Resume(blob []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cpu.cycles, e.cpu.idle = 0, 0 // before restoring: the ADC deadline is relative to idle
	if err := ResumeBytes(blob, e.cpu, e.mem); err != nil {
		return err
	}
	e.cpu.cycles, e.mmio.timerNext, e.mmio.timerTicks = 0, e.mmio.timerPeriod, 0
	e.cpu.pending = nil
	e.tap = keyTapper{}
	e.pendingUp = nil
	for k := range e.downAt {
		delete(e.downAt, k)
	}
	e.mmio.keysc.releaseAll()
	e.mmio.keysc.resumeDefaults() // peripheral config isn't in the snapshot; restore post-init state
	e.fault = ""
	return nil
}

// PC returns the current program counter (handy for logging/diagnostics from a host).
func (e *Emulator) PC() uint32 { e.mu.Lock(); defer e.mu.Unlock(); return e.cpu.pc }

// Cycles returns the instructions executed since the last Resume (free-running counter).
func (e *Emulator) Cycles() uint64 { e.mu.Lock(); defer e.mu.Unlock(); return e.cpu.cycles }

// RunUnpaced runs flat-out for n instructions (e.g. to boot fresh / drive first-boot). It is
// the fast path; RunRealtime is the wall-clock-paced path for interactive use.
func (e *Emulator) RunUnpaced(n int) { e.Step(n) }

// RunRealtime drives the machine at ~targetIPS instructions/second, calling frame() after
// each ~1/frameHz slice so the host can blit. Returns when stop is closed. Pacing matters
// once interactive: the core runs near or above real-HW speed, so without a cap the OS clock,
// cursor blink and key-repeat would run too fast (and a phone would needlessly burn battery).
// If the host can't sustain targetIPS, the ticker coalesces and it degrades to best-effort.
func (e *Emulator) RunRealtime(targetIPS, frameHz int, frame func(), stop <-chan struct{}) {
	if frameHz <= 0 {
		frameHz = 60
	}
	slice := targetIPS / frameHz
	if slice < 1 {
		slice = 1
	}
	tick := time.NewTicker(time.Second / time.Duration(frameHz))
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			e.Step(slice)
			if frame != nil {
				frame()
			}
		}
	}
}
