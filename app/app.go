package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cg50emu"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is what the page calls (window.go.main.App.*) and what serves /frame.
type App struct {
	ctx   context.Context
	store *store

	mu         sync.Mutex
	m          *machine // nil until a dump is loaded
	paused     bool
	speed      string
	installing bool
	installed  []string

	// measured emulated speed (cycles per wall second), for the status bar and the time base
	lastCycles uint64
	lastAt     time.Time
	rate       float64
	timebase   uint64

	frameMu sync.Mutex
	frame   []byte // scratch for /frame
}

func NewApp() *App {
	return &App{store: newStore(), frame: make([]byte, cg50emu.PanelWidth*cg50emu.PanelHeight*4)}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	st := a.store.settings()
	a.speed = st.Speed
	runtime.OnFileDrop(ctx, a.onDrop)
	go a.statusLoop()
	if st.LastDump != "" {
		if _, err := os.Stat(st.LastDump); err == nil {
			go func() {
				if err := a.loadDump(st.LastDump); err != nil {
					a.toast("error", err.Error())
				}
			}()
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	m := a.m
	a.m = nil
	a.mu.Unlock()
	if m != nil {
		m.halt()
		a.saveSession(m)
	}
}

// ---- status -------------------------------------------------------------------------

// Status is sent to the page as the "status" event, statusHz times a second.
type Status struct {
	Loaded     bool    `json:"loaded"`
	Dump       string  `json:"dump"`
	DumpMB     int     `json:"dumpMB"`
	Paused     bool    `json:"paused"`
	Speed      string  `json:"speed"`
	Booting    bool    `json:"booting"`
	Percent    float64 `json:"percent"` // emulated speed vs the real calculator
	MIPS       float64 `json:"mips"`    // instructions actually executed per second (sleep excluded)
	PC         string  `json:"pc"`
	Fault      string  `json:"fault"`
	Installing bool    `json:"installing"`
}

func (a *App) statusLoop() {
	t := time.NewTicker(time.Second / statusHz)
	defer t.Stop()
	var lastExec uint64
	lastSave := time.Now()
	for range t.C {
		// autosave: a crash or a forced quit loses at most this much (a normal quit saves too)
		if time.Since(lastSave) > autosaveEvery {
			lastSave = time.Now()
			a.mu.Lock()
			m, busy := a.m, a.installing
			a.mu.Unlock()
			if m != nil && !busy {
				a.saveSession(m)
			}
		}
		st := a.GetStatus()
		a.mu.Lock()
		m := a.m
		a.mu.Unlock()
		if m != nil {
			now := time.Now()
			cyc, exe := m.emu.Cycles(), m.emu.Executed()
			a.mu.Lock()
			dt := now.Sub(a.lastAt).Seconds()
			if !a.lastAt.IsZero() && dt > 0.05 && cyc >= a.lastCycles && exe >= lastExec {
				r := float64(cyc-a.lastCycles) / dt
				a.rate = a.rate*(1-rateAlpha) + r*rateAlpha
				st.MIPS = float64(exe-lastExec) / dt / 1e6
			}
			if math.IsNaN(a.rate) || math.IsInf(a.rate, 0) {
				a.rate = 0 // JSON can't carry NaN: one bad sample must not kill the status events
			}
			a.lastCycles, a.lastAt, lastExec = cyc, now, exe
			st.Percent = 100 * a.rate / realIPS
			if a.paused {
				st.Percent, st.MIPS = 0, 0
			}
			// keep the OS's clock (RTC, key scan) in step with what the machine really does
			var setTB uint64
			if !a.paused && a.rate > 1e6 {
				tb := uint64(a.rate)
				if a.timebase == 0 || absDiff(tb, a.timebase)*10 > a.timebase {
					a.timebase, setTB = tb, tb
				}
			}
			a.mu.Unlock()
			if setTB != 0 {
				m.emu.SetInstrPerSecond(setTB)
			}
		}
		runtime.EventsEmit(a.ctx, "status", st)
	}
}

func absDiff(x, y uint64) uint64 {
	if x > y {
		return x - y
	}
	return y - x
}

// GetStatus returns the current state (the page also polls it once at start).
func (a *App) GetStatus() Status {
	a.mu.Lock()
	st := Status{Paused: a.paused, Speed: a.speed, Installing: a.installing}
	m := a.m
	a.mu.Unlock()
	// the emulator calls below wait while an install holds the machine: never under a.mu
	if m != nil {
		st.Loaded = true
		st.Dump = m.dump.Name
		st.DumpMB = m.dump.Size >> 20
		st.Booting = m.booting()
		st.PC = fmt.Sprintf("%08X", m.emu.PC())
		st.Fault = m.emu.Fault()
	}
	return st
}

func (a *App) toast(kind, text string) {
	runtime.EventsEmit(a.ctx, "toast", map[string]string{"kind": kind, "text": text})
}

// ---- loading ------------------------------------------------------------------------

// OpenDump asks for a flash dump file and loads it.
func (a *App) OpenDump() error {
	p, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Open your calculator's flash dump",
		Filters: []runtime.FileFilter{{DisplayName: "Flash dump (*.bin)", Pattern: "*.bin"}, {DisplayName: "All files", Pattern: "*.*"}},
	})
	if err != nil || p == "" {
		return err
	}
	return a.loadDump(p)
}

// LoadDumpFile loads the flash dump at path (recent files, scripts).
func (a *App) LoadDumpFile(path string) error { return a.loadDump(path) }

// loadDump replaces the running machine with one built from the dump at path, resuming
// where that dump was left last time if a session was saved.
func (a *App) loadDump(path string) error {
	data, d, err := readDump(path)
	if err != nil {
		return err
	}
	e := cg50emu.NewEmulator(data)
	resumed := false
	if blob, err := a.store.read(d.Hash, "state"); err == nil {
		if e.Resume(blob) == nil {
			resumed = true
		} else {
			e = cg50emu.NewEmulator(data) // a damaged session: start fresh
		}
	}
	if !resumed && d.Size > 16<<20 {
		e.SetWarmBoot() // the whole flash, storage included: skip first-boot setup
	}
	a.start(e, d, !resumed)
	st := a.store.settings()
	st.LastDump = path
	a.store.saveSettings(st)
	if resumed {
		a.toast("info", "Resumed "+d.Name+" where you left it")
	} else {
		a.toast("info", "Starting the calculator from "+d.Name)
	}
	return nil
}

// start makes e the running machine (saving the previous one's session).
func (a *App) start(e *cg50emu.Emulator, d dumpInfo, boost bool) {
	e.EnableHLE(true) // native OS blitter (emu_go/hle.go): less work per screen update
	e.SetInstrPerSecond(realIPS)
	now := time.Now()
	_, off := now.Zone()
	e.SetClock(now.Unix() + int64(off)) // the calculator shows local time

	a.mu.Lock()
	old := a.m
	a.m = nil
	a.mu.Unlock()
	if old != nil {
		old.halt()
		a.saveSession(old)
	}
	m := newMachine(e, d, boost)
	a.mu.Lock()
	a.m, a.timebase, a.lastAt, a.rate = m, realIPS, time.Time{}, 0
	a.installed = nil
	a.mu.Unlock()
	go m.run(func() (bool, string) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.paused, a.speed
	})
}

func (a *App) saveSession(m *machine) {
	if m.emu.Fault() != "" {
		return // never keep a crashed machine
	}
	if blob, err := m.emu.Snapshot(); err == nil {
		a.store.write(m.dump.Hash, "state", blob)
	}
}

func (a *App) current() *machine {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m
}

var errNoDump = errors.New("open a flash dump first")

const autosaveEvery = 2 * time.Minute

// Restart is the calculator's RESTART button: storage memory kept, RAM lost, cold boot.
func (a *App) Restart() error {
	m := a.current()
	if m == nil {
		return errNoDump
	}
	e := cg50emu.NewEmulator(m.emu.FlashImage())
	if m.dump.Size > 16<<20 {
		e.SetWarmBoot()
	}
	a.start(e, m.dump, true)
	a.toast("info", "Restarted (storage memory kept)")
	return nil
}

// ForgetChanges goes back to the dump file as it is on disk: everything the calculator
// stored since (installed add-ins, settings, the saved session) is dropped.
func (a *App) ForgetChanges() error {
	m := a.current()
	if m == nil {
		return errNoDump
	}
	a.mu.Lock()
	a.m = nil
	a.mu.Unlock()
	m.halt()
	a.store.remove(m.dump.Hash, "state")
	return a.loadDump(m.dump.Path)
}

// ---- running ------------------------------------------------------------------------

func (a *App) SetPaused(p bool) {
	a.mu.Lock()
	a.paused = p
	a.mu.Unlock()
}

func (a *App) SetSpeed(s string) error {
	if _, ok := speeds[s]; !ok {
		return fmt.Errorf("unknown speed %q", s)
	}
	a.mu.Lock()
	a.speed = s
	a.mu.Unlock()
	st := a.store.settings()
	st.Speed = s
	return a.store.saveSettings(st)
}

// ---- keys ---------------------------------------------------------------------------

// KeyDown / KeyUp hold a key of the matrix (row, col as in re/KEYMAP.md) for as long as the
// user holds it; Tap queues whole presses in order (e.g. ALPHA then a letter).
func (a *App) KeyDown(row, col int) {
	if m := a.current(); m != nil {
		m.emu.KeyDown(uint32(row), uint32(col))
	}
}

func (a *App) KeyUp(row, col int) {
	if m := a.current(); m != nil {
		m.emu.KeyUp(uint32(row), uint32(col))
	}
}

func (a *App) Tap(keys [][2]int) {
	if m := a.current(); m != nil {
		for _, k := range keys {
			m.emu.InjectKey(uint32(k[0]), uint32(k[1]))
		}
	}
}

func (a *App) ReleaseAllKeys() {
	if m := a.current(); m != nil {
		m.emu.ReleaseAllKeys()
	}
}

// ---- states -------------------------------------------------------------------------

// SaveSlot / LoadSlot are the quick save states (per dump).
func (a *App) SaveSlot(n int) error {
	m := a.current()
	if m == nil {
		return errNoDump
	}
	blob, err := m.emu.Snapshot()
	if err != nil {
		return err
	}
	if err := a.store.write(m.dump.Hash, fmt.Sprintf("slot%d", n), blob); err != nil {
		return err
	}
	a.toast("info", fmt.Sprintf("Saved to slot %d", n))
	return nil
}

func (a *App) LoadSlot(n int) error {
	m := a.current()
	if m == nil {
		return errNoDump
	}
	blob, err := a.store.read(m.dump.Hash, fmt.Sprintf("slot%d", n))
	if err != nil {
		return fmt.Errorf("slot %d is empty", n)
	}
	if err := m.emu.Resume(blob); err != nil {
		return err
	}
	m.endBoost()
	a.toast("info", fmt.Sprintf("Loaded slot %d", n))
	return nil
}

// SaveStateAs / OpenState use a file the user picks (it remembers which dump it belongs to).
func (a *App) SaveStateAs() error {
	m := a.current()
	if m == nil {
		return errNoDump
	}
	p, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save state",
		DefaultFilename: "cg50-" + time.Now().Format("20060102-150405") + ".cg50state",
		Filters:         []runtime.FileFilter{{DisplayName: "CG50 Studio state (*.cg50state)", Pattern: "*.cg50state"}},
	})
	if err != nil || p == "" {
		return err
	}
	blob, err := m.emu.Snapshot()
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, wrapState(m.dump.Hash, blob), 0o644); err != nil {
		return err
	}
	a.toast("info", "State saved to "+filepath.Base(p))
	return nil
}

func (a *App) OpenState() error {
	if a.current() == nil {
		return errNoDump
	}
	p, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Open state",
		Filters: []runtime.FileFilter{{DisplayName: "CG50 Studio state (*.cg50state)", Pattern: "*.cg50state"}},
	})
	if err != nil || p == "" {
		return err
	}
	return a.openStateFile(p)
}

func (a *App) openStateFile(p string) error {
	m := a.current()
	if m == nil {
		return errNoDump
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	blob, err := unwrapState(b, m.dump.Hash)
	if err != nil {
		return err
	}
	if err := m.emu.Resume(blob); err != nil {
		return err
	}
	m.endBoost()
	a.toast("info", "Loaded "+filepath.Base(p))
	return nil
}

// ---- add-ins ------------------------------------------------------------------------

// InstallAddin asks for .g3a files and installs them into the storage memory.
func (a *App) InstallAddin() error {
	if a.current() == nil {
		return errNoDump
	}
	ps, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Install add-ins",
		Filters: []runtime.FileFilter{{DisplayName: "Add-ins (*.g3a)", Pattern: "*.g3a"}},
	})
	if err != nil {
		return err
	}
	for _, p := range ps {
		a.installFile(p)
	}
	return nil
}

func (a *App) installFile(p string) {
	m := a.current()
	if m == nil {
		a.toast("error", errNoDump.Error())
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		a.toast("error", err.Error())
		return
	}
	a.mu.Lock()
	a.installing = true
	a.mu.Unlock()
	go func() {
		name := filepath.Base(p)
		err := m.emu.InstallAddin(name, b)
		a.mu.Lock()
		a.installing = false
		if err == nil {
			a.installed = append(a.installed, name)
		}
		a.mu.Unlock()
		if err != nil {
			a.toast("error", name+": "+err.Error())
		} else {
			a.toast("info", name+" installed: it's in the MAIN MENU")
		}
	}()
}

// Installed lists the add-ins installed in this session.
func (a *App) Installed() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.installed...)
}

// onDrop handles files dropped on the window: add-ins, a dump, or a state file.
func (a *App) onDrop(x, y int, paths []string) {
	for _, p := range paths {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".g3a":
			a.installFile(p)
		case ".cg50state":
			if err := a.openStateFile(p); err != nil {
				a.toast("error", err.Error())
			}
		case ".bin":
			if err := a.loadDump(p); err != nil {
				a.toast("error", err.Error())
			}
		default:
			a.toast("error", filepath.Base(p)+": drop a .g3a add-in, a flash dump (.bin) or a .cg50state file")
		}
	}
}
