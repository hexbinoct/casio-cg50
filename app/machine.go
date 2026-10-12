package main

import (
	"sync/atomic"
	"time"

	"cg50emu"
)

const (
	// realIPS is the calculator's own pace on OS code, about 45 million instructions per
	// second (wiki: Timers → How fast the calculator runs). Speed 1× runs at this rate.
	realIPS = 45_000_000

	// bootBoost: a cold boot runs at Max until this many instructions (cycles) have passed,
	// about the MAIN MENU of a warm boot, then drops to the chosen speed.
	bootBoost = 700_000_000

	frameHz   = 60
	maxSlice  = 14 * time.Millisecond // flat-out work per 1/60 s tick, leaving the UI room
	maxChunk  = 250_000               // instructions per Step while running flat out
	statusHz  = 4
	rateAlpha = 0.35 // smoothing of the measured rate
)

// speeds maps the UI's speed choices to multiples of realIPS; 0 = as fast as possible.
var speeds = map[string]float64{"1x": 1, "2x": 2, "4x": 4, "max": 0}

// machine is one running calculator: the emulator plus the goroutine that drives it.
type machine struct {
	emu     *cg50emu.Emulator
	dump    dumpInfo
	stop    chan struct{}
	done    chan struct{}
	boostTo atomic.Uint64 // run flat out until Cycles() reaches this (0 = no boost)
}

func newMachine(e *cg50emu.Emulator, d dumpInfo, boost bool) *machine {
	m := &machine{emu: e, dump: d, stop: make(chan struct{}), done: make(chan struct{})}
	if boost {
		m.boostTo.Store(bootBoost)
	}
	return m
}

// run drives the machine until stop is closed. paused and speed are read each tick.
//
// Pacing follows the wall clock, not the ticks: each tick runs the machine up to where it
// should be by now (anchor + elapsed × rate), so a late or missed tick (Windows timers are
// coarse) is caught up rather than lost. A big gap (the app was busy or asleep) re-anchors
// instead of racing to catch up.
func (m *machine) run(get func() (paused bool, speed string)) {
	defer close(m.done)
	t := time.NewTicker(time.Second / frameHz)
	defer t.Stop()
	var (
		anchorAt  time.Time
		anchorCyc uint64
		lastMode  string
	)
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		paused, speed := get()
		e := m.emu
		if paused || e.Fault() != "" {
			lastMode = "" // re-anchor on resume
			continue
		}
		mult := speeds[speed]
		if mult == 0 || m.booting() {
			// As fast as possible while the CPU works, but never faster than 1× while it
			// sleeps: an idle OS would otherwise race its clock ahead for nothing.
			lastMode = ""
			ex := e.Executed()
			e.Step(realIPS / frameHz)
			if e.Executed()-ex < realIPS/frameHz/8 {
				continue // mostly asleep: this tick's 1× is enough
			}
			end := time.Now().Add(maxSlice)
			for time.Now().Before(end) {
				ex := e.Executed()
				e.Step(maxChunk)
				if e.Executed()-ex < maxChunk/8 {
					break // mostly asleep
				}
			}
			continue
		}
		now, cyc := time.Now(), e.Cycles()
		if speed != lastMode {
			anchorAt, anchorCyc, lastMode = now, cyc, speed
		}
		ips := mult * realIPS
		target := anchorCyc + uint64(now.Sub(anchorAt).Seconds()*ips)
		if target <= cyc {
			continue
		}
		behind := target - cyc
		if behind > uint64(ips/4) { // more than 1/4 s behind: don't race, start over from here
			anchorAt, anchorCyc = now, cyc
			behind = uint64(ips / frameHz)
		}
		end := now.Add(maxSlice)
		for behind > 0 && time.Now().Before(end) {
			n := min(behind, maxChunk)
			e.Step(int(n))
			behind -= n
		}
	}
}

// booting reports the cold-boot boost; it ends for good once reached (Resume resets Cycles).
func (m *machine) booting() bool {
	b := m.boostTo.Load()
	if b == 0 {
		return false
	}
	if m.emu.Cycles() >= b {
		m.boostTo.Store(0)
		return false
	}
	return true
}

// endBoost stops the boot boost (a state was loaded: the machine is past booting).
func (m *machine) endBoost() { m.boostTo.Store(0) }

// halt stops the run goroutine and waits for it.
func (m *machine) halt() {
	close(m.stop)
	<-m.done
}
