package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"cg50emu"
	"cg50emu/sh4"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The debugger windows' side in Go, on the core's debug API (docs/DEBUG_API.md).
//
// Run control. The machine's goroutine (machine.run) keeps calling Step whatever happens: a
// stopped machine runs nothing, and a step that didn't finish at once (stepping over GetKey)
// is finished by those calls. After each tick it looks at Stopped(); a stop it hasn't seen
// is handed to noteStop, which numbers it, counts it for its breakpoint, and sends the page
// a "debugstop" event (StopInfo). The page then asks for DebugSnapshot (registers, call
// stack, hit counts) in one call. Pause / Continue / the steps are bindings; a step that ends
// at once returns its snapshot directly. Status.Paused means "stopped", and Status.Stop
// says why.
//
// Breakpoints. The page owns the list (debugger.js) and sends all of it after every change
// (SetBreakpoints). Here it becomes the core's breakpoints and watchpoints, the syscall,
// exception and interrupt breaks, and it is saved per dump (sessions/<hash>.breakpoints) and
// applied again to every new machine of that dump.

// BPSpec is a breakpoint as the page has it.
type BPSpec struct {
	ID        int    `json:"id"`
	Kind      string `json:"kind"` // exec, read, write, access, syscall, exception, interrupt
	Addr      uint32 `json:"addr"` // the address; for syscall the number, for exception/interrupt the code
	Any       bool   `json:"any"`  // syscall/exception/interrupt: every one
	Len       uint32 `json:"len"`  // watchpoints: bytes (0 = 1)
	Enabled   bool   `json:"enabled"`
	Condition string `json:"condition"` // exec: an expression over the registers (cond.go)
	Ignore    uint64 `json:"ignore"`    // exec: the first hits that don't stop
}

// BPInfo is a breakpoint with what the core knows about it.
type BPInfo struct {
	BPSpec
	Hits  uint64 `json:"hits"`
	Where string `json:"where"`           // the function (exec), the syscall's name, …
	Note  string `json:"note,omitempty"`  // e.g. how a watchpoint compares addresses
	Error string `json:"error,omitempty"` // the condition doesn't compile, …
}

type appliedBP struct {
	spec  BPSpec
	emuID int
	watch bool
	note  string
}

type debugState struct {
	mu      sync.Mutex
	hash    string // the dump the list belongs to
	specs   []BPSpec
	applied map[int]*appliedBP // by page id
	hits    map[int]uint64     // syscall/exception/interrupt entries: their stops, counted here
	errs    map[int]string
	trace   int
	seq     int
	last    *StopInfo // the current stop; nil while running
	seen    cg50emu.Stop
	seenOK  bool
}

// exceptions "any" stands for: every exception but the TLB misses (an add-in takes one per
// page it touches first). A TLB miss (0x040, 0x060) can be asked for by its code.
var anyException = []uint32{0x080, 0x0A0, 0x0C0, 0x0E0, 0x100, 0x160, 0x180, 0x1A0, 0x1E0}

var exceptionNames = map[uint32]string{
	0x040: "TLB miss (read)", 0x060: "TLB miss (write)", 0x080: "initial page write",
	0x0A0: "TLB protection (read)", 0x0C0: "TLB protection (write)", 0x0E0: "address error (read)",
	0x100: "address error (write)", 0x160: "trapa", 0x180: "illegal instruction",
	0x1A0: "illegal slot instruction", 0x1E0: "user break",
}

var interruptNames = map[uint32]string{
	0x400: "TMU0", 0x420: "TMU1", 0x440: "TMU2", 0x9E0: "ETMU0", 0xC20: "ETMU1", 0xC40: "ETMU2",
	0x900: "ETMU3", 0xD00: "ETMU4", 0xFA0: "ETMU5", 0x800: "DMAC0", 0x820: "DMAC1", 0x840: "DMAC2",
	0x860: "DMAC3", 0xB80: "DMAC4", 0xBA0: "DMAC5", 0xAA0: "RTC", 0xBE0: "key scan", 0x560: "OS tick",
}

func emit(ctx context.Context, name string, data any) {
	if ctx != nil {
		runtime.EventsEmit(ctx, name, data)
	}
}

// ---- breakpoints ------------------------------------------------------------------------

func (s *store) breakpointsPath(hash string) string { return s.sessionPath(hash, "breakpoints") }

func (s *store) loadBreakpoints(hash string) []BPSpec {
	var out []BPSpec
	if b, err := os.ReadFile(s.breakpointsPath(hash)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (s *store) saveBreakpoints(hash string, specs []BPSpec) {
	if len(specs) == 0 {
		s.remove(hash, "breakpoints")
		return
	}
	b, _ := json.MarshalIndent(specs, "", " ")
	s.write(hash, "breakpoints", b)
}

func validKind(k string) bool {
	switch k {
	case "exec", "read", "write", "access", "syscall", "exception", "interrupt":
		return true
	}
	return false
}

func sameTarget(x, y BPSpec) bool {
	return x.Kind == y.Kind && x.Addr == y.Addr && x.Len == y.Len && x.Any == y.Any
}

// syncBreakpoints makes the core's breakpoints those of specs (d.mu held). Unchanged
// entries keep their core breakpoint, so their hit counts.
func (d *debugState) syncBreakpoints(e *cg50emu.Emulator, specs []BPSpec) {
	if d.applied == nil {
		d.applied = map[int]*appliedBP{}
	}
	if d.hits == nil {
		d.hits = map[int]uint64{}
	}
	d.errs = map[int]string{}
	keep := map[int]BPSpec{}
	for _, s := range specs {
		keep[s.ID] = s
	}
	for id, ap := range d.applied {
		if s, ok := keep[id]; !ok || !sameTarget(s, ap.spec) {
			if ap.emuID >= 0 {
				if ap.watch {
					e.RemoveWatchpoint(ap.emuID)
				} else {
					e.RemoveBreakpoint(ap.emuID)
				}
			}
			delete(d.applied, id)
			delete(d.hits, id)
		}
	}
	var sysOn, sysAll, excOn, excAll, intOn, intAll bool
	var sysNums, excCodes, intCodes []uint32
	for _, s := range specs {
		ap := d.applied[s.ID]
		if ap == nil {
			ap = &appliedBP{spec: s, emuID: -1}
			switch s.Kind {
			case "exec":
				ap.emuID = e.AddBreakpoint(s.Addr)
			case "read", "write", "access":
				w := cg50emu.Watchpoint{Addr: s.Addr, Len: s.Len, Enabled: s.Enabled,
					Kind: map[string]cg50emu.WatchKind{"read": cg50emu.WatchRead, "write": cg50emu.WatchWrite, "access": cg50emu.WatchAccess}[s.Kind]}
				// compare bus addresses when the address leads somewhere now: every alias
				// (0x8C… and 0xAC…, an add-in's virtual RAM) is then caught
				if info, err := e.Translate(s.Addr); err == nil && info.Region != "" && info.Region != "mmio" {
					w.Addr, w.Phys = info.Bus, true
					ap.note = fmt.Sprintf("bus %08X: every alias", info.Bus)
				} else {
					ap.note = "virtual address (not mapped now)"
				}
				ap.emuID, ap.watch = e.AddWatchpoint(w), true
			}
			d.applied[s.ID] = ap
		}
		ap.spec = s
		switch s.Kind {
		case "exec":
			e.EnableBreakpoint(ap.emuID, s.Enabled)
			e.SetBreakpointIgnore(ap.emuID, s.Ignore)
			cond, err := compileCond(s.Condition)
			if err != nil {
				d.errs[s.ID] = err.Error()
			}
			e.SetBreakpointCondition(ap.emuID, cond)
		case "read", "write", "access":
			e.EnableWatchpoint(ap.emuID, s.Enabled)
		case "syscall":
			if s.Enabled {
				sysOn = true
				sysAll = sysAll || s.Any
				sysNums = append(sysNums, s.Addr)
			}
		case "exception":
			if s.Enabled {
				excOn = true
				if s.Any {
					excAll = true
				} else {
					excCodes = append(excCodes, s.Addr)
				}
			}
		case "interrupt":
			if s.Enabled {
				intOn = true
				intAll = intAll || s.Any
				intCodes = append(intCodes, s.Addr)
			}
		}
	}
	if sysAll {
		sysNums = nil
	}
	e.BreakOnSyscall(sysOn, sysNums...)
	if excAll {
		excCodes = append(excCodes, anyException...)
	}
	e.BreakOnException(excOn, excCodes...)
	if intAll {
		intCodes = nil
	}
	e.BreakOnInterrupt(intOn, intCodes...)
	d.specs = append([]BPSpec(nil), specs...)
}

// infos reports the list with hit counts (d.mu held).
func (a *App) infos(e *cg50emu.Emulator) []BPInfo {
	d := &a.dbg
	bh, wh := map[int]uint64{}, map[int]uint64{}
	if e != nil {
		for _, b := range e.Breakpoints() {
			bh[b.ID] = b.Hits
		}
		for _, w := range e.Watchpoints() {
			wh[w.ID] = w.Hits
		}
	}
	out := make([]BPInfo, 0, len(d.specs))
	for _, s := range d.specs {
		bi := BPInfo{BPSpec: s, Error: d.errs[s.ID]}
		ap := d.applied[s.ID]
		switch s.Kind {
		case "exec":
			bi.Where = a.symbol(s.Addr)
			if ap != nil {
				bi.Hits = bh[ap.emuID]
			}
		case "read", "write", "access":
			bi.Where = a.symbol(s.Addr)
			if ap != nil {
				bi.Hits, bi.Note = wh[ap.emuID], ap.note
			}
		case "syscall":
			bi.Hits = d.hits[s.ID]
			if s.Any {
				bi.Where = "every syscall an add-in makes"
			} else if bi.Where = sh4.SyscallNameOf(s.Addr); bi.Where == "" {
				bi.Where = "(no name known)"
			}
		case "exception":
			bi.Hits = d.hits[s.ID]
			bi.Where = exceptionNames[s.Addr]
			if s.Any {
				bi.Where = "every exception but TLB misses"
			}
		case "interrupt":
			bi.Hits = d.hits[s.ID]
			bi.Where = interruptNames[s.Addr]
			if s.Any {
				bi.Where = "every interrupt"
			}
		}
		out = append(out, bi)
	}
	return out
}

// SetBreakpoints replaces the breakpoint list (the page sends all of it after each change)
// and returns it with hit counts and names. It is saved for this dump.
func (a *App) SetBreakpoints(specs []BPSpec) ([]BPInfo, error) {
	clean := make([]BPSpec, 0, len(specs))
	for _, s := range specs {
		if !validKind(s.Kind) {
			return nil, fmt.Errorf("unknown breakpoint kind %q", s.Kind)
		}
		clean = append(clean, s)
	}
	m := a.current()
	d := &a.dbg
	d.mu.Lock()
	defer d.mu.Unlock()
	if m == nil {
		d.specs = clean
		return a.infos(nil), nil
	}
	d.syncBreakpoints(m.emu, clean)
	a.store.saveBreakpoints(m.dump.Hash, clean)
	return a.infos(m.emu), nil
}

// GetBreakpoints returns the list (with the page's ids, kept across restarts).
func (a *App) GetBreakpoints() []BPInfo {
	m := a.current()
	d := &a.dbg
	d.mu.Lock()
	defer d.mu.Unlock()
	if m == nil {
		return a.infos(nil)
	}
	return a.infos(m.emu)
}

// prepareMachine gives a new machine the debugger's settings: this dump's breakpoints (read
// from disk when the dump changes) and the trace.
func (a *App) prepareMachine(e *cg50emu.Emulator, d dumpInfo) {
	g := &a.dbg
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hash != d.Hash {
		g.hash = d.Hash
		g.specs = a.store.loadBreakpoints(d.Hash)
	}
	g.applied, g.hits = nil, nil
	g.syncBreakpoints(e, g.specs)
	if g.trace > 0 {
		e.SetTrace(g.trace)
	}
	g.last, g.seenOK = nil, false
}

// ---- stops ------------------------------------------------------------------------------

// StopInfo says why the machine stopped.
type StopInfo struct {
	Seq        int       `json:"seq"`
	Reason     string    `json:"reason"` // pause, breakpoint, watchpoint, step, syscall, exception, interrupt, fault
	PC         uint32    `json:"pc"`
	Where      string    `json:"where"`                // the PC as function+offset
	Text       string    `json:"text"`                 // one line for the status bar and the console
	Breakpoint int       `json:"breakpoint,omitempty"` // the page's id of the breakpoint that stopped it
	Syscall    uint32    `json:"syscall,omitempty"`
	Handler    uint32    `json:"handler,omitempty"`
	Code       uint32    `json:"code,omitempty"`
	From       uint32    `json:"from,omitempty"`
	Watch      *WatchHit `json:"watch,omitempty"`
	Fault      string    `json:"fault,omitempty"`
	Cycles     uint64    `json:"cycles"`
}

// WatchHit is the access that stopped at a watchpoint.
type WatchHit struct {
	Addr  uint32 `json:"addr"`
	Bus   uint32 `json:"bus"`
	Size  uint32 `json:"size"`
	Value uint32 `json:"value"`
	Write bool   `json:"write"`
	PC    uint32 `json:"pc"`
}

func (a *App) stopSeq() int {
	a.dbg.mu.Lock()
	defer a.dbg.mu.Unlock()
	return a.dbg.seq
}

// noteStop records a stop of the core (once) and tells the page.
func (a *App) noteStop(s cg50emu.Stop) *StopInfo {
	if m := a.current(); m != nil {
		a.watchAddin(m) // an add-in launched since the last look: analyse it before naming
	}
	d := &a.dbg
	d.mu.Lock()
	if d.seenOK && d.seen == s && d.last != nil {
		info := d.last
		d.mu.Unlock()
		return info
	}
	d.seq++
	d.seen, d.seenOK = s, true
	info := &StopInfo{Seq: d.seq, Reason: s.Reason.String(), PC: s.PC, Cycles: s.Cycles, Where: a.symbol(s.PC)}
	at := fmt.Sprintf("%08X", s.PC)
	if info.Where != "" {
		at += " (" + info.Where + ")"
	}
	// which entry of the list it was
	findSpec := func(kind string, val uint32) int {
		best := 0
		for _, sp := range d.specs {
			if sp.Kind == kind && sp.Enabled {
				if !sp.Any && sp.Addr == val {
					return sp.ID
				}
				if sp.Any && best == 0 {
					best = sp.ID
				}
			}
		}
		return best
	}
	switch s.Reason {
	case cg50emu.StopPause:
		info.Text = "Paused at " + at
	case cg50emu.StopStep:
		info.Text = "Step: " + at
	case cg50emu.StopBreakpoint:
		for id, ap := range d.applied {
			if !ap.watch && ap.emuID == s.Breakpoint && ap.spec.Kind == "exec" {
				info.Breakpoint = id
			}
		}
		info.Text = fmt.Sprintf("Breakpoint %d at %s", info.Breakpoint, at)
	case cg50emu.StopWatchpoint:
		w := s.Watch
		for id, ap := range d.applied {
			if ap.watch && ap.emuID == w.ID {
				info.Breakpoint = id
			}
		}
		info.Watch = &WatchHit{Addr: w.Addr, Bus: w.Bus, Size: w.Size, Value: w.Value, Write: w.Write, PC: w.PC}
		rw := "read"
		if w.Write {
			rw = "write"
		}
		by := fmt.Sprintf("%08X", w.PC)
		if sym := a.symbol(w.PC); sym != "" {
			by += " (" + sym + ")"
		}
		info.Text = fmt.Sprintf("Watchpoint %d: %s of %d bytes at %08X = 0x%X by %s", info.Breakpoint, rw, w.Size, w.Addr, w.Value, by)
	case cg50emu.StopSyscall:
		info.Syscall, info.Handler = s.Syscall, s.Handler
		info.Breakpoint = findSpec("syscall", s.Syscall)
		name := sh4.SyscallNameOf(s.Syscall)
		if name == "" {
			name = "(no name known)"
		}
		r := a.registers()
		info.Text = fmt.Sprintf("Syscall 0x%04X %s → %08X, called from %08X", s.Syscall, name, s.Handler, r.PR)
	case cg50emu.StopException:
		info.Code, info.From = s.Code, s.From
		info.Breakpoint = findSpec("exception", s.Code)
		info.Text = fmt.Sprintf("Exception 0x%03X %s at %08X: handler %s", s.Code, exceptionNames[s.Code], s.From, at)
	case cg50emu.StopInterrupt:
		info.Code, info.From = s.Code, s.From
		info.Breakpoint = findSpec("interrupt", s.Code)
		info.Text = fmt.Sprintf("Interrupt 0x%03X %s, interrupted %08X", s.Code, interruptNames[s.Code], s.From)
	case cg50emu.StopFault:
		info.Fault = s.Fault
		info.Text = "The calculator stopped: " + s.Fault
	default:
		info.Text = s.String()
	}
	if info.Breakpoint != 0 && (s.Reason == cg50emu.StopSyscall || s.Reason == cg50emu.StopException || s.Reason == cg50emu.StopInterrupt) {
		d.hits[info.Breakpoint]++
	}
	d.last = info
	d.mu.Unlock()
	emit(a.ctx, "debugstop", info)
	return info
}

// running: the machine is about to run again.
func (a *App) running() {
	a.dbg.mu.Lock()
	a.dbg.last = nil
	a.dbg.mu.Unlock()
}

func (a *App) registers() cg50emu.Regs {
	if m := a.current(); m != nil {
		return m.emu.Registers()
	}
	return cg50emu.Regs{}
}

// stopped reports the machine's stop, noting it if new.
func (a *App) stopped(m *machine) (*StopInfo, bool) {
	s, ok := m.emu.Stopped()
	if !ok {
		return nil, false
	}
	return a.noteStop(s), true
}

// ---- run control --------------------------------------------------------------------------

// SetPaused pauses (the machine stops between two instructions, a "pause" stop) or lets it
// run on (from a pause or any other stop).
func (a *App) SetPaused(p bool) {
	m := a.current()
	if m == nil {
		return
	}
	if p {
		m.emu.Pause()
		a.stopped(m)
		return
	}
	a.running()
	m.emu.Continue()
}

// Snapshot is the machine's state at a stop, for the debugger windows.
type Snapshot struct {
	Stop        *StopInfo         `json:"stop"`
	Regs        map[string]uint32 `json:"regs"`
	Bank        int               `json:"bank"` // the bank r0-r7 name now
	Sleeping    bool              `json:"sleeping"`
	Cycles      uint64            `json:"cycles"`
	Executed    uint64            `json:"executed"`
	Frames      []FrameInfo       `json:"frames"`
	Breakpoints []BPInfo          `json:"breakpoints"`
}

// FrameInfo is one frame of the call stack, innermost first.
type FrameInfo struct {
	Addr      uint32 `json:"addr"` // frame 0: the PC; others: where the caller resumes
	Call      uint32 `json:"call"` // the call instruction
	SP        uint32 `json:"sp"`   // the stack slot the return address was found in
	Name      string `json:"name"`
	Source    string `json:"source"` // "pc", "pr", "stack"
	Confirmed bool   `json:"confirmed"`
}

func (a *App) snapshot(m *machine) *Snapshot {
	r := m.emu.Registers()
	regs := map[string]uint32{}
	for i, v := range r.R {
		regs[fmt.Sprintf("r%d", i)] = v
	}
	for i := 0; i < 8; i++ {
		regs[fmt.Sprintf("r%d_bank0", i)] = r.Bank0[i]
		regs[fmt.Sprintf("r%d_bank1", i)] = r.Bank1[i]
	}
	for k, v := range map[string]uint32{"pc": r.PC, "pr": r.PR, "sr": r.SR, "gbr": r.GBR, "vbr": r.VBR,
		"ssr": r.SSR, "spc": r.SPC, "sgr": r.SGR, "dbr": r.DBR, "mach": r.MACH, "macl": r.MACL,
		"fpul": r.FPUL, "fpscr": r.FPSCR} {
		regs[k] = v
	}
	sn := &Snapshot{Regs: regs, Bank: r.Bank, Sleeping: r.Sleeping, Cycles: r.Cycles, Executed: r.Executed}
	src := map[cg50emu.FrameSource]string{cg50emu.FramePC: "pc", cg50emu.FramePR: "pr", cg50emu.FrameStack: "stack"}
	for _, f := range m.emu.CallStack(32) {
		sn.Frames = append(sn.Frames, FrameInfo{Addr: f.PC, Call: f.Call, SP: f.SP, Name: a.symbol(f.PC),
			Source: src[f.Source], Confirmed: f.Confirmed})
	}
	a.dbg.mu.Lock()
	sn.Stop = a.dbg.last
	sn.Breakpoints = a.infos(m.emu)
	a.dbg.mu.Unlock()
	return sn
}

// DebugSnapshot returns the registers, call stack and breakpoint hits now (normally asked
// for at a stop).
func (a *App) DebugSnapshot() (*Snapshot, error) {
	m := a.current()
	if m == nil {
		return nil, errNoDump
	}
	a.stopped(m)
	return a.snapshot(m), nil
}

// StepResult: a step that stopped comes with the state; one still running (stepping over a
// function that waits for a key) ends with a "debugstop" event later.
type StepResult struct {
	Stopped bool      `json:"stopped"`
	Snap    *Snapshot `json:"snap,omitempty"`
}

func (a *App) step(how string, addr uint32) (*StepResult, error) {
	m := a.current()
	if m == nil {
		return nil, errNoDump
	}
	if f := m.emu.Fault(); f != "" {
		return nil, errors.New("the calculator stopped: " + f)
	}
	a.running()
	var s cg50emu.Stop
	switch how {
	case "into":
		s = m.emu.StepInto()
	case "over":
		s = m.emu.StepOver()
	case "out":
		s = m.emu.StepOut()
	case "to":
		s = m.emu.RunTo(addr)
	}
	if s.Reason == cg50emu.StopNone {
		return &StepResult{}, nil
	}
	a.noteStop(s)
	return &StepResult{Stopped: true, Snap: a.snapshot(m)}, nil
}

// StepInto runs one instruction (a delayed branch with its slot is one).
func (a *App) StepInto() (*StepResult, error) { return a.step("into", 0) }

// StepOver runs one instruction; a call runs until it returns.
func (a *App) StepOver() (*StepResult, error) { return a.step("over", 0) }

// StepOut runs until the current function returns.
func (a *App) StepOut() (*StepResult, error) { return a.step("out", 0) }

// RunTo runs until the next instruction is at addr (other stops still apply).
func (a *App) RunTo(addr uint32) (*StepResult, error) { return a.step("to", addr) }

// SetRegister changes a register (names as the core's RegisterNames: r0…r15, r0_bank1, sr,
// pc, …) and returns the new state.
func (a *App) SetRegister(name string, value uint32) (*Snapshot, error) {
	m := a.current()
	if m == nil {
		return nil, errNoDump
	}
	if err := m.emu.SetRegister(name, value); err != nil {
		return nil, err
	}
	return a.snapshot(m), nil
}

// ---- memory -------------------------------------------------------------------------------

// read copies live memory at a virtual address for the debugger windows. An add-in page the
// OS hasn't mapped into the TLB yet is read where the OS's page table says it is, and the
// add-in's RAM where the OS always puts it, so those regions read whole.
func (m *machine) read(addr uint32, dst []byte) (int, error) {
	done := 0
	for done < len(dst) {
		n, err := m.emu.ReadMemory(addr+uint32(done), dst[done:], false)
		done += n
		if err == nil {
			return done, nil
		}
		var me *cg50emu.MemError
		if !errors.As(err, &me) || me.Kind != cg50emu.MemTLBMiss {
			return done, err
		}
		at := addr + uint32(done)
		phys, ok := m.addinPhys(at)
		if !ok {
			return done, err
		}
		chunk := min(len(dst)-done, int(4096-at&0xFFF))
		k, err := m.emu.ReadMemory(phys, dst[done:done+chunk], true)
		done += k
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// addinPhys is where an add-in address is when the TLB has no entry for it.
func (m *machine) addinPhys(a uint32) (uint32, bool) {
	switch {
	case a-addinBase < addinMax:
		ent, err := m.emu.Peek32(pageTable+(a-addinBase)>>12*4, false)
		if ent &= 0x1FFFFFFF; err != nil || ent == 0 || ent&0xFFF != 0 || ent >= 32<<20 {
			return 0, false
		}
		return ent | a&0xFFF, true
	case a-addinRAM < addinRAMLen:
		return addinRAMBus + (a - addinRAM), true
	}
	return 0, false
}

// MemBlock is memory read for a window.
type MemBlock struct {
	Addr uint32 `json:"addr"`
	Data []byte `json:"data"`           // the readable prefix (base64 in JSON)
	Err  string `json:"err,omitempty"`  // why the rest isn't
	Kind string `json:"kind,omitempty"` // "mmio", "tlb", "unmapped", …
}

func memKind(err error) string {
	var me *cg50emu.MemError
	if errors.As(err, &me) {
		return map[cg50emu.MemErrKind]string{cg50emu.MemUnmapped: "unmapped", cg50emu.MemTLBMiss: "tlb",
			cg50emu.MemMMIO: "mmio", cg50emu.MemReadOnly: "readonly"}[me.Kind]
	}
	return "error"
}

// ReadMemory reads up to n bytes (at most 64 KB) at a virtual address without side effects:
// device registers are refused, and the result says why it stopped early.
func (a *App) ReadMemory(addr uint32, n int) (MemBlock, error) {
	m := a.current()
	if m == nil {
		return MemBlock{}, errNoDump
	}
	n = max(0, min(n, 64<<10))
	buf := make([]byte, n)
	k, err := m.read(addr, buf)
	b := MemBlock{Addr: addr, Data: buf[:k]}
	if err != nil {
		b.Err, b.Kind = err.Error(), memKind(err)
	}
	return b, nil
}

// WriteMemory writes bytes at a virtual address straight into memory (flash included: the
// CPU runs patched code from its next fetch). It returns how many were written.
func (a *App) WriteMemory(addr uint32, data []int) (int, error) {
	m := a.current()
	if m == nil {
		return 0, errNoDump
	}
	b := make([]byte, len(data))
	for i, v := range data {
		if v < 0 || v > 255 {
			return 0, fmt.Errorf("byte %d is %d", i, v)
		}
		b[i] = byte(v)
	}
	n, err := m.emu.WriteMemory(addr, b, false)
	var me *cg50emu.MemError
	if errors.As(err, &me) && me.Kind == cg50emu.MemTLBMiss {
		if phys, ok := m.addinPhys(addr + uint32(n)); ok {
			k, err2 := m.emu.WriteMemory(phys, b[n:], true)
			n, err = n+k, err2
		}
	}
	return n, err
}

// MemoryRegions lists the regions the Memory and Disassembly windows offer.
func (a *App) MemoryRegions() []Region {
	s := &a.ana
	s.mu.RLock()
	defer s.mu.RUnlock()
	flash := s.flash
	if flash == 0 {
		flash = 32 << 20
	}
	var addin uint32
	if s.addin != nil {
		addin = s.addin.rows.size
	}
	return regions(flash, addin)
}

// ---- trace ----------------------------------------------------------------------------------

// SetTraceDepth keeps the last n executed instructions (0: off; on, it costs about 40 % of the
// emulator's top speed, never the real calculator's pace).
func (a *App) SetTraceDepth(n int) {
	n = max(0, min(n, 1<<20))
	a.dbg.mu.Lock()
	a.dbg.trace = n
	a.dbg.mu.Unlock()
	if m := a.current(); m != nil {
		m.emu.SetTrace(n)
	}
}

// TraceRow is one executed instruction.
type TraceRow struct {
	Addr uint32 `json:"addr"`
	Text string `json:"text"`
}

// TraceInfo is the end of the trace.
type TraceInfo struct {
	Total   uint64     `json:"total"` // instructions recorded in all
	Entries []TraceRow `json:"entries"`
}

// DebugTrace returns the last max instructions executed (oldest first), decoded.
func (a *App) DebugTrace(max int) TraceInfo {
	m := a.current()
	if m == nil {
		return TraceInfo{Entries: []TraceRow{}}
	}
	pcs, total := m.emu.Trace()
	if max > 0 && len(pcs) > max {
		pcs = pcs[len(pcs)-max:]
	}
	out := TraceInfo{Total: total, Entries: make([]TraceRow, 0, len(pcs))}
	text := map[uint32]string{}
	for _, pc := range pcs {
		t, ok := text[pc]
		if !ok {
			t = m.decodeAt(pc)
			text[pc] = t
		}
		out.Entries = append(out.Entries, TraceRow{Addr: pc, Text: t})
	}
	return out
}

// decodeAt decodes the live instruction at addr (its literal too).
func (m *machine) decodeAt(addr uint32) string {
	var b [4]byte
	n, _ := m.read(addr, b[:])
	if n < 2 {
		return "??"
	}
	rd := func(a uint32) (uint16, bool) {
		if a-addr+1 < uint32(n) {
			return uint16(b[a-addr])<<8 | uint16(b[a-addr+1]), true
		}
		var w [2]byte
		if k, _ := m.read(a, w[:]); k == 2 {
			return uint16(w[0])<<8 | uint16(w[1]), true
		}
		return 0, false
	}
	w, _ := rd(addr)
	in := sh4.DecodeWord(w, addr, rd)
	return in.Text
}

// ---- for the console ---------------------------------------------------------------------------

// RegisterNames lists the register names SetRegister takes.
func (a *App) RegisterNames() []string {
	n := cg50emu.RegisterNames()
	sort.Strings(n)
	return n
}

// Describe says what an address is: its region, where it leads (bus address, TLB), and the
// function holding it.
func (a *App) Describe(addr uint32) string {
	m := a.current()
	if m == nil {
		return ""
	}
	var parts []string
	info, err := m.emu.Translate(addr)
	if err != nil {
		parts = append(parts, err.Error())
	} else {
		t := fmt.Sprintf("%s, bus %08X", info.Region, info.Bus)
		if info.Translated {
			t += " (through the TLB)"
		}
		parts = append(parts, t)
	}
	if s := a.symbol(addr); s != "" {
		parts = append(parts, s)
	}
	return fmt.Sprintf("%08X: %s", addr, strings.Join(parts, "; "))
}
