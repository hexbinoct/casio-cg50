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

	downAt    map[[2]uint32]uint64 // KeyDown cycle per held key (minimum-hold enforcement)
	pendingUp []pendingRelease     // KeyUp releases deferred until the minimum hold elapses

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
	return &Emulator{cpu: cpu, mem: mem, mmio: mmio, downAt: map[[2]uint32]uint64{}}
}

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

// Step advances the machine by n instructions, driving queued taps onto the matrix.
func (e *Emulator) Step(n int) {
	e.mu.Lock()
	for i := 0; i < n; i++ {
		e.mmio.tick(e.cpu)
		e.cpu.step()
		e.tap.drive(e.mmio.keysc, e.cpu.cycles)
		if len(e.pendingUp) != 0 {
			e.flushReleases()
		}
	}
	e.mu.Unlock()
}

// FramebufferRGB565 copies the raw 384x216 RGB565 (big-endian) frame into dst (>= fbBytes).
func (e *Emulator) FramebufferRGB565(dst []byte) {
	e.mu.Lock()
	copy(dst, e.mem.dram[:fbBytes])
	e.mu.Unlock()
}

// FramebufferRGBA decodes the frame into dst as 8-bit RGBA (FbWidth*FbHeight*4 bytes), the
// layout Android's Bitmap.copyPixelsFromBuffer / a host canvas expects.
func (e *Emulator) FramebufferRGBA(dst []byte) {
	e.mu.Lock()
	d := e.mem.dram
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
