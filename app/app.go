package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"cg50emu"
	"cg50emu/sh4"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is what the page calls (window.go.main.App.*) and what serves /frame.
type App struct {
	ctx   context.Context
	store *store

	mu             sync.Mutex
	m              *machine // nil until a dump is loaded
	speed          string
	installing     bool
	installed      []string
	installedFiles []installedFile // this session's installs, to name the add-in that runs
	loading        bool            // the remembered dump is being opened at start
	notice         string          // why no dump is open (the remembered one is gone or failed), for the welcome card

	// measured emulated speed (cycles per wall second), for the status bar and the time base
	lastCycles uint64
	lastAt     time.Time
	rate       float64
	timebase   uint64

	frameMu sync.Mutex
	frame   []byte // scratch for /frame

	dbg debugState  // breakpoints, stops (debug.go)
	ana analysisSet // the OS and add-in analyses, listings (analysis.go)
}

type installedFile struct {
	name string
	data []byte
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
	// First start: nothing remembered, and the page shows the welcome card asking for a dump.
	// Later starts reopen the dump used last time; if it moved or broke, the card says so
	// (an event sent now could arrive before the page listens, so it's part of the status).
	if st.LastDump != "" {
		if _, err := os.Stat(st.LastDump); err != nil {
			a.mu.Lock()
			a.notice = "The flash dump you used last time is no longer at " + st.LastDump + ". Open it again from where it is now."
			a.mu.Unlock()
			return
		}
		a.mu.Lock()
		a.loading = true
		a.mu.Unlock()
		go func() {
			err := a.loadDump(st.LastDump)
			a.mu.Lock()
			a.loading = false
			if err != nil {
				a.notice = "Couldn't open " + st.LastDump + ": " + err.Error()
			}
			a.mu.Unlock()
			if err != nil {
				a.toast("error", err.Error())
			}
		}()
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
	Loading    bool    `json:"loading"` // opening the remembered dump at start
	Notice     string  `json:"notice"`  // why no dump is open, if something went wrong

	OS        string `json:"os"`        // the dump's OS version, "3.60" ("" = not recognised)
	OSWarning string `json:"osWarning"` // set when that isn't the supported version

	DumpHash string         `json:"dumpHash"`
	Stop     *StopInfo      `json:"stop"`     // why it is stopped (Paused); nil while running
	Analysis AnalysisStatus `json:"analysis"` // the code analysis behind the disassembly
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
			a.watchAddin(m)
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
			if st.Paused {
				st.Percent, st.MIPS = 0, 0
			}
			// keep the OS's clock (RTC, key scan) in step with what the machine really does
			var setTB uint64
			if !st.Paused && a.rate > 1e6 {
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

// Version is the app's version, for the page.
func (a *App) Version() string { return version }

// GetStatus returns the current state (the page also polls it once at start).
func (a *App) GetStatus() Status {
	a.mu.Lock()
	st := Status{Speed: a.speed, Installing: a.installing, Loading: a.loading, Notice: a.notice}
	m := a.m
	a.mu.Unlock()
	// the emulator calls below wait while an install holds the machine: never under a.mu
	if m != nil {
		st.Loaded = true
		st.Dump = m.dump.Name
		st.DumpMB = m.dump.Size >> 20
		st.DumpHash = m.dump.Hash
		st.OS, st.OSWarning = m.dump.OS, m.dump.osWarning()
		st.Booting = m.booting()
		st.PC = fmt.Sprintf("%08X", m.emu.PC())
		st.Fault = m.emu.Fault()
		st.Stop, st.Paused = a.stopped(m)
	}
	a.ana.mu.RLock()
	st.Analysis = a.ana.status
	a.ana.mu.RUnlock()
	return st
}

func (a *App) toast(kind, text string) {
	runtime.EventsEmit(a.ctx, "toast", map[string]string{"kind": kind, "text": text})
}

// ---- loading ------------------------------------------------------------------------

// OpenDump asks for a flash dump file and loads it.
func (a *App) OpenDump() error {
	p, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Open your calculator's flash dump (not included with CG50 Studio)",
		Filters: dumpFilters(),
	})
	if err != nil || p == "" {
		return err
	}
	return a.loadDump(p)
}

// dumpFilters are the dump dialog's file types. Dumps are usually .bin, but any name works
// (readDump checks the contents), so every platform must be able to pick any file:
// Windows matches *.* , GTK needs * (*.* only matches names with a dot), and macOS takes a
// list of extensions, where "*" means nothing, so it gets no filter at all.
func dumpFilters() []runtime.FileFilter {
	switch goruntime.GOOS {
	case "darwin":
		return nil
	case "windows":
		return []runtime.FileFilter{{DisplayName: "Flash dump (*.bin)", Pattern: "*.bin"}, {DisplayName: "All files (*.*)", Pattern: "*.*"}}
	default:
		return []runtime.FileFilter{{DisplayName: "Flash dump (*.bin)", Pattern: "*.bin"}, {DisplayName: "All files", Pattern: "*"}}
	}
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
	a.analyseOS(data, d)
	a.mu.Lock()
	a.notice = ""
	a.mu.Unlock()
	st := a.store.settings()
	st.LastDump = path
	a.store.saveSettings(st)
	if resumed {
		a.toast("info", "Resumed "+d.Name+" where you left it")
	} else {
		a.toast("info", "Starting the calculator from "+d.Name)
	}
	if w := d.osWarning(); w != "" {
		a.toast("warn", w)
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
	a.prepareMachine(e, d) // breakpoints of this dump, the trace
	m := newMachine(e, d, boost)
	m.onStop = func(s cg50emu.Stop) { a.noteStop(s) }
	a.mu.Lock()
	a.m, a.timebase, a.lastAt, a.rate = m, realIPS, time.Time{}, 0
	a.installed, a.installedFiles = nil, nil
	a.mu.Unlock()
	go m.run(func() string {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.speed
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
	if err := a.resume(m, blob); err != nil {
		return err
	}
	a.toast("info", fmt.Sprintf("Loaded slot %d", n))
	return nil
}

// resume loads a state into the running machine. A stop doesn't survive Resume, so a machine
// that was stopped is paused again at the state's PC (the debugger windows then show it).
func (a *App) resume(m *machine, blob []byte) error {
	_, was := m.emu.Stopped()
	if err := m.emu.Resume(blob); err != nil {
		return err
	}
	m.endBoost()
	a.running()
	if was {
		m.emu.Pause()
		a.stopped(m)
	}
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
	if err := a.resume(m, blob); err != nil {
		return err
	}
	a.toast("info", "Loaded "+filepath.Base(p))
	return nil
}

// ---- add-ins ------------------------------------------------------------------------

// InstallAddin asks for .g3a files, or any files an add-in reads (KhiCAS's .ac2, ROMs), and
// installs them into the storage memory.
func (a *App) InstallAddin() error {
	if a.current() == nil {
		return errNoDump
	}
	ps, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Install add-ins and files",
		Filters: []runtime.FileFilter{
			{DisplayName: "Add-ins (*.g3a)", Pattern: "*.g3a"},
			{DisplayName: "All files (add-in data, ROMs)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return err
	}
	for _, p := range ps {
		a.installFile(p)
	}
	return nil
}

// installFile installs a .g3a as an add-in (the MAIN MENU is rebuilt) and copies any other
// file to the storage memory root under its own name, as a USB copy would.
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
		addin := strings.EqualFold(filepath.Ext(name), ".g3a")
		var err error
		if addin {
			err = m.emu.InstallAddin(name, b)
		} else {
			err = m.emu.InstallFile(name, b)
		}
		a.mu.Lock()
		a.installing = false
		if err == nil && addin {
			a.installed = append(a.installed, name)
			a.installedFiles = append(a.installedFiles, installedFile{name, b})
		}
		a.mu.Unlock()
		switch {
		case err != nil:
			a.toast("error", name+": "+err.Error())
		case !addin:
			a.toast("info", name+" copied to the storage memory")
		default:
			a.toast("info", name+" installed: it's in the MAIN MENU")
			if len(b) > sh4.G3AHeaderLen {
				a.setAddin(name, b[sh4.G3AHeaderLen:], false, 0, b) // listed until one is launched
			}
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
		default: // an add-in's data file (KhiCAS's .ac2), a ROM: copied to the storage memory
			a.installFile(p)
		}
	}
}
