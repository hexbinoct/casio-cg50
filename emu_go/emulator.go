package main

// Emulator is a self-contained facade over the SH7305 core (CPU + memory + MMIO) with a
// thread-safe key queue, framebuffer access, save-state, and a real-time paced run loop.
// It is the single API a host UI drives — the desktop web server here, and (via the cgo
// bridge in android_bridge.go) an Android app. Typical lifecycle:
//
//	e := NewEmulator(flash)
//	e.Resume(savedSnapshot)            // instant: lands at the MAIN MENU
//	go e.RunRealtime(20e6, 60, blit, stop)
//	e.InjectKey(row, col)             // from the UI thread, anytime
//
// All public methods are safe to call concurrently with the run loop.

import (
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

	mu   sync.Mutex  // serialises Step vs Snapshot/Resume/InjectKey
	keys [][2]uint32 // pending matrix presses (row,col), drained at safe points

	// decode-confirmed key-injection state machine (mirrors the proven web-UI logic):
	// inject, then wait until the OS key decoder FUN_801952cc actually runs for the key
	// (re-injecting if a redraw flushed it), so presses are never silently dropped.
	curKey    [2]uint32
	haveKey   bool
	injected  bool
	sawDecode bool
	injStart  uint64
	retries   int
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
	return &Emulator{cpu: cpu, mem: mem, mmio: mmio}
}

// InjectKey enqueues a matrix key press at grid (col=C,row=R) using 0-based (row,col); see
// re/KEYMAP.md. SHIFT/ALPHA are themselves keys — enqueue the modifier before the target.
func (e *Emulator) InjectKey(row, col uint32) {
	e.mu.Lock()
	e.keys = append(e.keys, [2]uint32{row, col})
	e.mu.Unlock()
}

// Step advances the machine by n instructions, draining queued key presses at safe points.
func (e *Emulator) Step(n int) {
	e.mu.Lock()
	for i := 0; i < n; i++ {
		e.mmio.tick(e.cpu)
		e.cpu.step()
		e.driveKeys()
	}
	e.mu.Unlock()
}

// driveKeys runs one tick of the decode-confirmed injection state machine. Caller holds mu.
func (e *Emulator) driveKeys() {
	cpu := e.cpu
	if !e.haveKey {
		if len(e.keys) == 0 {
			return
		}
		e.curKey, e.keys = e.keys[0], e.keys[1:]
		e.haveKey, e.injected = true, false
	}
	if !e.injected {
		if keySafe(cpu) {
			injectKey(cpu, e.mem, e.curKey[0], e.curKey[1])
			e.injected, e.sawDecode, e.injStart, e.retries = true, false, cpu.cycles, 0
		}
		return
	}
	if cpu.pc >= 0x801952cc && cpu.pc < 0x801952e0 {
		e.sawDecode = true
	}
	if e.sawDecode {
		e.haveKey = false // landed
	} else if cpu.cycles-e.injStart > 40_000_000 && keySafe(cpu) {
		e.retries++
		if e.retries > 8 {
			e.haveKey = false // give up (key is a no-op in this context)
		} else {
			injectKey(cpu, e.mem, e.curKey[0], e.curKey[1])
			e.injStart = cpu.cycles
		}
	}
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
	e.haveKey, e.injected, e.keys = false, false, nil
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
