package cg50emu

// debug.go — the host debugger: what CG50 Studio's debugger windows drive (docs/DEBUG_API.md).
// It is the HOST's, separate from the emulated User Break Controller (ubc.go), which belongs
// to the calculator and its own debuggers (DASM, gint's GDB stub); the two work side by side.
//
// Execution model. Every call below takes e.mu. Nothing here costs anything until a feature
// is armed (a breakpoint, a watchpoint, the trace, a break-on-event or a step goal): Step
// decides once per call whether the debugger is on (stepLocked's `dbg`) and only then runs
// each instruction through dstep, which checks and records around the very same dispatch the
// plain loop uses (interrupt acceptance, HLE, UBC, MMU, execute), so device timing and
// results are those of an undebugged run.
//
// A stop latches: the machine stays stopped, Step/Run do nothing and Run reports the stop,
// until Continue or a step command. Breakpoints, the syscall break and run-to stop BEFORE the
// instruction at their address runs; watchpoints, exceptions and finished steps stop AFTER
// the instruction that caused them; an interrupt stop comes right after the CPU entered the
// handler (PC = VBR+0x600, nothing of it run). Resuming (Continue, a step) never stops again
// at the address it resumes from before the instruction there has run.
//
// One step is one instruction as a debugger shows it: a delayed branch and its delay slot are
// one step (the core executes them together), and so is a bt/s or bf/s not taken with its
// slot.

import (
	"fmt"
	"sort"
	"strings"
)

// StopReason says why the machine stopped.
type StopReason uint8

const (
	StopNone       StopReason = iota // not stopped: the instruction budget was used up
	StopPause                        // Pause
	StopBreakpoint                   // an execution breakpoint (Stop.Breakpoint)
	StopWatchpoint                   // a memory watchpoint (Stop.Watch)
	StopStep                         // a StepInto / StepOver / StepOut / RunTo finished
	StopSyscall                      // an add-in called the OS syscall Stop.Syscall
	StopException                    // an exception was entered (Stop.Code = EXPEVT)
	StopInterrupt                    // an interrupt was accepted (Stop.Code = INTEVT)
	StopFault                        // the core halted (Stop.Fault; see Emulator.Fault)
)

var stopNames = [...]string{"none", "pause", "breakpoint", "watchpoint", "step", "syscall", "exception", "interrupt", "fault"}

func (r StopReason) String() string {
	if int(r) < len(stopNames) {
		return stopNames[r]
	}
	return fmt.Sprintf("StopReason(%d)", r)
}

// Stop describes a stop (or, with Reason StopNone, a run that used its whole budget).
type Stop struct {
	Reason StopReason
	PC     uint32 // the next instruction to run
	Cycles uint64 // the machine's clock at the stop (Emulator.Cycles)

	Breakpoint int      // StopBreakpoint: the breakpoint's ID
	Watch      WatchHit // StopWatchpoint: the access
	Syscall    uint32   // StopSyscall: the number (r0)
	Handler    uint32   // StopSyscall: the OS function the table gives for it
	Code       uint32   // StopException: EXPEVT; StopInterrupt: INTEVT
	From       uint32   // StopException / StopInterrupt: SPC, where the handler's rte returns
	Fault      string   // StopFault
}

func (s Stop) String() string {
	b := fmt.Sprintf("%s at %08x", s.Reason, s.PC)
	switch s.Reason {
	case StopBreakpoint:
		b += fmt.Sprintf(" (breakpoint %d)", s.Breakpoint)
	case StopWatchpoint:
		w := s.Watch
		rw := "read"
		if w.Write {
			rw = "write"
		}
		b += fmt.Sprintf(" (watchpoint %d: %s of %d bytes at %08x = %#x by %08x)", w.ID, rw, w.Size, w.Addr, w.Value, w.PC)
	case StopSyscall:
		b += fmt.Sprintf(" (syscall %#x -> %08x)", s.Syscall, s.Handler)
	case StopException, StopInterrupt:
		b += fmt.Sprintf(" (code %#x from %08x)", s.Code, s.From)
	case StopFault:
		b += ": " + s.Fault
	}
	return b
}

// Breakpoint is an execution breakpoint on a virtual address (exactly that address: the P1
// and P2 aliases of one instruction, 0x80xxxxxx and 0xA0xxxxxx, are different breakpoints).
type Breakpoint struct {
	ID      int
	Addr    uint32
	Enabled bool
	Hits    uint64 // times it was reached with its condition true (counted while enabled)
	Ignore  uint64 // the first Ignore hits do not stop
	// Cond, if set, must hold for a hit; it sees the registers before the instruction runs.
	Cond func(r *Regs) bool
}

// WatchKind is which accesses a watchpoint sees.
type WatchKind uint8

const (
	WatchRead   WatchKind = 1
	WatchWrite  WatchKind = 2
	WatchAccess WatchKind = WatchRead | WatchWrite
)

// Watchpoint watches CPU data accesses (operands, PC-relative literal loads) that overlap
// [Addr, Addr+Len). Instruction fetches, DMA and other bus masters are not seen.
type Watchpoint struct {
	ID   int
	Addr uint32
	Len  uint32 // 0 counts as 1
	Kind WatchKind
	// Phys compares bus addresses instead of virtual ones: the 29-bit physical address
	// (through the TLB for translated accesses) for the external bus, the P4 address for the
	// on-chip memories (IL RAM folded onto its 16 KB). It catches every alias of the range.
	Phys bool
	// MatchValue: only accesses whose value v satisfies (v ^ Value) & Mask == 0 (Mask 0 =
	// every bit) count. v is the value read or written, of the access's size.
	MatchValue  bool
	Value, Mask uint32
	Enabled     bool
	Hits        uint64
}

// WatchHit is the access that stopped at a watchpoint.
type WatchHit struct {
	ID    int
	Addr  uint32 // the access's virtual address
	Bus   uint32 // its bus address (see Watchpoint.Phys)
	Size  uint32
	Value uint32
	Write bool
	PC    uint32 // the instruction that made it (a delayed branch for its slot)
}

type goalKind uint8

const (
	goalNone   goalKind = iota
	goalInto            // one step
	goalReturn          // run until an rts at call depth 0 has run (StepOver of a call, StepOut)
	goalTo              // run until PC == target
)

// debugSyncBudget bounds how long a step command runs inside the call itself; a goal not met
// by then stays armed and finishes in the host's next Run/Step calls.
const debugSyncBudget = 2_000_000

type debugger struct {
	on      bool // anything armed: stepLocked takes the dstep path
	watchOn bool // an enabled watchpoint: dstep arms Memory.ops

	// pages has a bit per 4 KB page holding a stop address (an enabled breakpoint, the
	// syscall entry, a run-to target): dstep looks further only on those pages.
	pages  [1 << 17]uint8
	bps    map[uint32][]*Breakpoint
	bpByID map[int]*Breakpoint
	watch  []*Watchpoint // all, in ID order
	nextID int

	syscallOn  bool
	syscallSet map[uint32]bool // nil = every number
	excOn      bool
	excSet     map[uint32]bool
	irqOn      bool
	irqSet     map[uint32]bool

	goal     goalKind
	depth    int    // goalReturn: calls entered and not yet returned from
	target   uint32 // goalTo
	intoSlot bool   // goalInto: a bt/s or bf/s not taken ran; its slot is part of the step

	skipArmed bool // don't stop before the instruction at skipPC until it has run
	skipPC    uint32

	trace []uint32 // ring of executed PCs (nil = off); len is a power of two
	tpos  uint64   // PCs recorded so far

	curPC uint32 // the instruction dstep is running (for WatchHit.PC)
	whit  bool   // a watchpoint matched during the current instruction
	wh    WatchHit

	latched bool
	stop    Stop
}

// debugger returns the debugger, creating it on first use. Caller holds e.mu.
func (e *Emulator) debugger() *debugger {
	if e.debug == nil {
		e.debug = &debugger{bps: map[uint32][]*Breakpoint{}, bpByID: map[int]*Breakpoint{}, nextID: 1}
		e.mem.dbgw = e.debug
	}
	return e.debug
}

func (d *debugger) setPage(a uint32) { d.pages[a>>15] |= 1 << (a >> 12 & 7) }

// refresh recomputes the page filter and whether the debugger is on, after any change.
func (d *debugger) refresh() {
	clear(d.pages[:])
	any := false
	for a, l := range d.bps {
		for _, b := range l {
			if b.Enabled {
				d.setPage(a)
				any = true
				break
			}
		}
	}
	if d.syscallOn {
		d.setPage(syscallTrampoline)
	}
	if d.goal == goalTo {
		d.setPage(d.target)
	}
	d.watchOn = false
	for _, w := range d.watch {
		d.watchOn = d.watchOn || w.Enabled
	}
	d.on = any || d.watchOn || d.trace != nil || d.syscallOn || d.excOn || d.irqOn || d.goal != goalNone
}

// reset forgets the run state (a stop, a goal, the trace contents) after Resume.
func (d *debugger) reset() {
	d.latched, d.stop = false, Stop{}
	d.goal, d.skipArmed, d.whit = goalNone, false, false
	if d.trace != nil {
		clear(d.trace)
		d.tpos = 0
	}
	d.refresh()
}

// latch stops the machine with s (PC and clock filled in). Caller holds e.mu.
func (e *Emulator) latch(s Stop) bool {
	d := e.debug
	s.PC, s.Cycles = e.cpu.pc, e.cpu.cycles
	d.stop, d.latched = s, true
	d.skipArmed = false
	if d.goal != goalNone {
		d.goal = goalNone
		d.refresh()
	}
	return true
}

// ---- the debug step -------------------------------------------------------------------

// isCallOp: bsr, bsrf, jsr (they set PR to the instruction after the delay slot).
func isCallOp(op uint32) bool {
	return op&0xF000 == 0xB000 || op&0xF0FF == 0x0003 || op&0xF0FF == 0x400B
}

// dstep is the slow path's cpu.step with the debugger's checks around it; true = stopped.
func (e *Emulator) dstep() bool {
	c, d := e.cpu, e.debug
	if c.sleeping {
		c.step() // time passes, or a request wakes it: no instruction runs
		return false
	}
	if len(c.pending) != 0 && c.acceptInterrupt() {
		if d.goal == goalInto {
			return e.latch(Stop{Reason: StopStep})
		}
		if d.irqOn && (d.irqSet == nil || d.irqSet[c.lastIRQ]) {
			return e.latch(Stop{Reason: StopInterrupt, Code: c.lastIRQ, From: c.spc})
		}
	}
	pc := c.pc
	if d.pages[pc>>15]&(1<<(pc>>12&7)) != 0 && e.before(pc) {
		return true
	}
	var op uint32
	if d.goal != goalNone {
		op = c.peekOp(pc)
	}
	exc0 := c.excCnt
	if d.watchOn {
		d.whit, d.curPC = false, pc
		c.mem.ops |= opsDbg
	}
	ret := false // the instruction returned like an rts (HLE)
	if c.pc == c.hlePC && c.hle != nil && c.hle() {
		ret = true // ran natively, at its return address now (charges its own cycles)
	} else {
		if c.ubcOn {
			c.stepUBC()
		} else if c.mem.mmuAt {
			c.stepMMU()
		} else {
			o := c.mem.fetch16(c.pc)
			c.pc += 2
			c.execute(o)
		}
		c.cycles++
	}
	c.mem.ops &^= opsDbg
	if d.trace != nil {
		d.trace[d.tpos&uint64(len(d.trace)-1)] = pc
		d.tpos++
	}
	if d.skipArmed && pc == d.skipPC {
		d.skipArmed = false
	}
	// an exception whose SPC is the instruction itself means it did not run (an MMU fault
	// rolled it back, an illegal instruction, a UBC break before it); one after it (trapa, a
	// UBC break after it) leaves it done
	exc := c.excCnt != exc0
	ran := !exc || c.spc != pc
	switch {
	case exc && d.excOn && (d.excSet == nil || d.excSet[c.lastExc]):
		return e.latch(Stop{Reason: StopException, Code: c.lastExc, From: c.spc})
	case ran && d.whit:
		d.whit = false
		return e.latch(Stop{Reason: StopWatchpoint, Watch: d.wh})
	}
	switch d.goal {
	case goalInto:
		if !d.intoSlot && !exc && op&0xFD00 == 0x8D00 && c.pc == pc+2 {
			d.intoSlot = true // bt/s or bf/s not taken: its slot still belongs to this step
			return false
		}
		return e.latch(Stop{Reason: StopStep})
	case goalReturn:
		if !ran {
			break // it runs again after the handler
		}
		if ret || op == 0x000B {
			if d.depth <= 0 {
				return e.latch(Stop{Reason: StopStep})
			}
			d.depth--
		} else if isCallOp(op) {
			d.depth++
		}
	}
	return false
}

// debugRun is cpu.run through dstep: until stop, an MMIO write, sleep, or a debugger stop.
func (e *Emulator) debugRun(stop uint64) bool {
	c, d := e.cpu, e.debug
	if d.goal == goalNone && !d.watchOn && d.trace == nil {
		// only stop addresses and event breaks: an instruction needs dstep only on a page
		// with a stop address, with a request pending, or at the HLE entry; the rest run as
		// in cpu.run, with the exception check after them (what dstep would do for them)
		for c.cycles < stop && !e.mmio.dirty && !c.sleeping {
			pc := c.pc
			if len(c.pending) != 0 || d.pages[pc>>15]&(1<<(pc>>12&7)) != 0 || pc == c.hlePC {
				if e.dstep() {
					return true
				}
				continue
			}
			exc0 := c.excCnt
			if c.ubcOn {
				c.stepUBC()
			} else if c.mem.mmuAt {
				c.stepMMU()
			} else {
				op := c.mem.fetch16(pc)
				c.pc += 2
				c.execute(op)
			}
			c.cycles++
			if pc == d.skipPC {
				d.skipArmed = false
			}
			if c.excCnt != exc0 && d.excOn && (d.excSet == nil || d.excSet[c.lastExc]) {
				return e.latch(Stop{Reason: StopException, Code: c.lastExc, From: c.spc})
			}
		}
		return false
	}
	for c.cycles < stop && !e.mmio.dirty && !c.sleeping {
		if e.dstep() {
			return true
		}
	}
	return false
}

// before checks the stop addresses on pc's page, before the instruction there runs.
func (e *Emulator) before(pc uint32) bool {
	d, c := e.debug, e.cpu
	if d.skipArmed && pc == d.skipPC {
		return false
	}
	if d.goal == goalTo && pc == d.target {
		return e.latch(Stop{Reason: StopStep})
	}
	if d.syscallOn && pc == syscallTrampoline && (d.syscallSet == nil || d.syscallSet[c.r[0]]) {
		h, _ := e.syscallHandler(c.r[0])
		return e.latch(Stop{Reason: StopSyscall, Syscall: c.r[0], Handler: h})
	}
	var regs *Regs
	for _, b := range d.bps[pc] {
		if !b.Enabled {
			continue
		}
		if b.Cond != nil {
			if regs == nil {
				r := e.regsLocked()
				regs = &r
			}
			if !b.Cond(regs) {
				continue
			}
		}
		b.Hits++
		if b.Hits > b.Ignore {
			return e.latch(Stop{Reason: StopBreakpoint, Breakpoint: b.ID})
		}
	}
	return false
}

// access is called for each CPU operand access while watchpoints are armed (Memory.opRead /
// opWrite); the first match of an instruction is the one reported.
func (d *debugger) access(m *Memory, va, size, val uint32, write bool) {
	if d.whit {
		return
	}
	var bus uint32
	busOK := false
	for _, w := range d.watch {
		if !w.Enabled {
			continue
		}
		if (write && w.Kind&WatchWrite == 0) || (!write && w.Kind&WatchRead == 0) {
			continue
		}
		a := va
		if w.Phys {
			if !busOK {
				bus, busOK = m.busAddr(va), true
			}
			a = bus
		}
		n := uint64(max(w.Len, 1))
		if uint64(a)+uint64(size) <= uint64(w.Addr) || uint64(w.Addr)+n <= uint64(a) {
			continue
		}
		if w.MatchValue {
			mask := w.Mask
			if mask == 0 {
				mask = 0xFFFFFFFF
			}
			if (val^w.Value)&mask != 0 {
				continue
			}
		}
		w.Hits++
		if !busOK {
			bus, busOK = m.busAddr(va), true
		}
		d.whit = true
		d.wh = WatchHit{ID: w.ID, Addr: va, Bus: bus, Size: size, Value: val, Write: write, PC: d.curPC}
		return
	}
}

// busAddr is the bus address of a CPU access at va that has just succeeded (see
// Watchpoint.Phys).
func (m *Memory) busAddr(va uint32) uint32 {
	if m.translated(va) {
		if pa, _, ok := m.mmu.peek(va, m.priv()); ok {
			return pa
		}
		return va
	}
	if va >= 0xE0000000 {
		if va-IlramBase < IlramSize {
			return IlramBase + (va-IlramBase)&IlramMask
		}
		return va
	}
	return va & 0x1FFFFFFF
}

// ---- execution control ----------------------------------------------------------------

// Run advances the machine by up to n instructions (as Step) and says how it ended: Reason
// StopNone when the budget ran out, else the stop. While the machine is stopped it runs
// nothing and returns that stop again.
func (e *Emulator) Run(n int) Stop {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.stoppedLocked(); ok {
		return s
	}
	e.stepLocked(n)
	return e.statusLocked()
}

// stoppedLocked reports a latched stop or a fault.
func (e *Emulator) stoppedLocked() (Stop, bool) {
	if e.fault != "" {
		return Stop{Reason: StopFault, PC: e.cpu.pc, Cycles: e.cpu.cycles, Fault: e.fault}, true
	}
	if d := e.debug; d != nil && d.latched {
		return d.stop, true
	}
	return Stop{}, false
}

func (e *Emulator) statusLocked() Stop {
	if s, ok := e.stoppedLocked(); ok {
		return s
	}
	return Stop{Reason: StopNone, PC: e.cpu.pc, Cycles: e.cpu.cycles}
}

// Stopped returns the current stop, if the machine is stopped (a fault counts).
func (e *Emulator) Stopped() (Stop, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stoppedLocked()
}

// Pause stops the machine between two instructions. A Step running on another goroutine
// holds the lock for its chunk, so Pause takes effect when that chunk ends; later Steps do
// nothing until Continue. A pending step goal is dropped. Pausing a stopped machine changes
// nothing.
func (e *Emulator) Pause() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.stoppedLocked(); ok {
		return
	}
	e.debugger()
	e.latch(Stop{Reason: StopPause})
}

// Continue lets a stopped machine run again (the host's next Run/Step calls). A breakpoint at
// the current PC does not stop it again before that instruction has run.
func (e *Emulator) Continue() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resumeLocked()
}

func (e *Emulator) resumeLocked() *debugger {
	d := e.debugger()
	d.latched = false
	d.skipArmed, d.skipPC = true, e.cpu.pc
	d.goal, d.intoSlot, d.depth = goalNone, false, 0
	return d
}

// startGoal arms a step goal from the current PC and runs up to debugSyncBudget instructions
// at once. The result is the stop, or Reason StopNone if the goal is still running (the
// host's next Run/Step calls finish it).
func (e *Emulator) startGoal(g goalKind, depth int, target uint32) Stop {
	if e.fault != "" {
		return e.statusLocked()
	}
	d := e.resumeLocked()
	d.goal, d.depth, d.target = g, depth, target
	d.refresh()
	e.stepLocked(debugSyncBudget)
	return e.statusLocked()
}

// StepInto runs one instruction (a delayed branch with its slot is one). If an interrupt is
// accepted first, the step ends at the handler's entry instead. A sleeping CPU steps when it
// wakes.
func (e *Emulator) StepInto() Stop {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startGoal(goalInto, 0, 0)
}

// StepOver is StepInto, except that a call (bsr, bsrf, jsr) runs until the called function
// has returned: the stop is at the instruction after the call's delay slot, at the same call
// depth (recursion is counted, not mistaken for the return).
func (e *Emulator) StepOver() Stop {
	e.mu.Lock()
	defer e.mu.Unlock()
	if isCallOp(e.cpu.peekOp(e.cpu.pc)) {
		return e.startGoal(goalReturn, -1, 0)
	}
	return e.startGoal(goalInto, 0, 0)
}

// StepOut runs until the current function returns: until an rts runs that is not paired with
// a call made since the step-out began. The stop is at the return address.
func (e *Emulator) StepOut() Stop {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startGoal(goalReturn, 0, 0)
}

// RunTo runs until the next instruction to run is at addr (a one-shot breakpoint; other stops
// still apply and cancel it).
func (e *Emulator) RunTo(addr uint32) Stop {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startGoal(goalTo, 0, addr)
}

// ---- breakpoints ----------------------------------------------------------------------

// AddBreakpoint sets an enabled execution breakpoint at addr and returns its ID.
func (e *Emulator) AddBreakpoint(addr uint32) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	b := &Breakpoint{ID: d.nextID, Addr: addr, Enabled: true}
	d.nextID++
	d.bps[addr] = append(d.bps[addr], b)
	d.bpByID[b.ID] = b
	d.refresh()
	return b.ID
}

// RemoveBreakpoint deletes a breakpoint; false if there is no such ID.
func (e *Emulator) RemoveBreakpoint(id int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	b, ok := d.bpByID[id]
	if !ok {
		return false
	}
	delete(d.bpByID, id)
	l := d.bps[b.Addr]
	for i := range l {
		if l[i] == b {
			l = append(l[:i], l[i+1:]...)
			break
		}
	}
	if len(l) == 0 {
		delete(d.bps, b.Addr)
	} else {
		d.bps[b.Addr] = l
	}
	d.refresh()
	return true
}

// ClearBreakpoints deletes every breakpoint.
func (e *Emulator) ClearBreakpoints() {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	clear(d.bps)
	clear(d.bpByID)
	d.refresh()
}

// EnableBreakpoint turns a breakpoint on or off (it keeps its hit count).
func (e *Emulator) EnableBreakpoint(id int, on bool) bool {
	return e.editBreakpoint(id, func(b *Breakpoint) { b.Enabled = on })
}

// SetBreakpointIgnore lets the first n hits pass: the breakpoint stops on a hit once its Hits
// exceed n (Hits counts from its creation or the last ResetBreakpointHits).
func (e *Emulator) SetBreakpointIgnore(id int, n uint64) bool {
	return e.editBreakpoint(id, func(b *Breakpoint) { b.Ignore = n })
}

// SetBreakpointCondition sets (nil: removes) the breakpoint's condition.
func (e *Emulator) SetBreakpointCondition(id int, cond func(r *Regs) bool) bool {
	return e.editBreakpoint(id, func(b *Breakpoint) { b.Cond = cond })
}

// ResetBreakpointHits zeroes a breakpoint's hit count.
func (e *Emulator) ResetBreakpointHits(id int) bool {
	return e.editBreakpoint(id, func(b *Breakpoint) { b.Hits = 0 })
}

func (e *Emulator) editBreakpoint(id int, f func(*Breakpoint)) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	b, ok := d.bpByID[id]
	if ok {
		f(b)
		d.refresh()
	}
	return ok
}

// Breakpoints returns copies of every breakpoint, by ID.
func (e *Emulator) Breakpoints() []Breakpoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	out := make([]Breakpoint, 0, len(d.bpByID))
	for _, b := range d.bpByID {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ---- watchpoints ----------------------------------------------------------------------

// AddWatchpoint adds w (its ID and Hits are assigned; Kind 0 means WatchAccess) and returns
// the ID.
func (e *Emulator) AddWatchpoint(w Watchpoint) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	w.ID, w.Hits = d.nextID, 0
	d.nextID++
	if w.Kind == 0 {
		w.Kind = WatchAccess
	}
	d.watch = append(d.watch, &w)
	d.refresh()
	return w.ID
}

// RemoveWatchpoint deletes a watchpoint; false if there is no such ID.
func (e *Emulator) RemoveWatchpoint(id int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	for i, w := range d.watch {
		if w.ID == id {
			d.watch = append(d.watch[:i], d.watch[i+1:]...)
			d.refresh()
			return true
		}
	}
	return false
}

// EnableWatchpoint turns a watchpoint on or off.
func (e *Emulator) EnableWatchpoint(id int, on bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	for _, w := range d.watch {
		if w.ID == id {
			w.Enabled = on
			d.refresh()
			return true
		}
	}
	return false
}

// ClearWatchpoints deletes every watchpoint.
func (e *Emulator) ClearWatchpoints() {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	d.watch = nil
	d.refresh()
}

// Watchpoints returns copies of every watchpoint, by ID.
func (e *Emulator) Watchpoints() []Watchpoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	out := make([]Watchpoint, len(d.watch))
	for i, w := range d.watch {
		out[i] = *w
	}
	return out
}

// ---- break on events ------------------------------------------------------------------

func codeSet(codes []uint32) map[uint32]bool {
	if len(codes) == 0 {
		return nil
	}
	m := make(map[uint32]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

// BreakOnSyscall stops when an add-in calls an OS syscall (execution reaches the OS's syscall
// routine at 0x80020070 with the number in r0), for the given numbers or, with none, for all.
// The stop is before the routine runs: StepOut from there stops when the syscall returns.
func (e *Emulator) BreakOnSyscall(on bool, numbers ...uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	d.syscallOn, d.syscallSet = on, codeSet(numbers)
	d.refresh()
}

// BreakOnException stops after the CPU entered an exception handler (general exceptions:
// illegal / slot illegal instruction, trapa, address errors the core models; MMU exceptions
// incl. the TLB misses of add-in demand paging; user breaks of the emulated UBC), for the
// given EXPEVT codes or, with none, for all. Interrupts are BreakOnInterrupt's.
func (e *Emulator) BreakOnException(on bool, expevt ...uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	d.excOn, d.excSet = on, codeSet(expevt)
	d.refresh()
}

// BreakOnInterrupt stops when the CPU accepts an interrupt (at the handler's entry), for the
// given INTEVT codes or, with none, for all.
func (e *Emulator) BreakOnInterrupt(on bool, intevt ...uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	d.irqOn, d.irqSet = on, codeSet(intevt)
	d.refresh()
}

// syscallHandler reads entry n of the running OS's syscall table, without side effects.
func (e *Emulator) syscallHandler(n uint32) (uint32, bool) {
	for a := uint32(syscallTrampoline); a < syscallTrampoline+0x10; a += 2 {
		op, err := e.peek(a, 2, false)
		if err != nil {
			return 0, false
		}
		if op>>8 == 0xD2 { // mov.l @(disp,pc),r2
			base, err := e.peek((a+4)&^3+(op&0xff)*4, 4, false)
			if err != nil {
				return 0, false
			}
			h, err := e.peek(base+n*4, 4, false)
			return h, err == nil
		}
	}
	return 0, false
}

// ---- trace ----------------------------------------------------------------------------

// SetTrace keeps the PCs of the last n executed instructions (rounded up to a power of two;
// 0 turns the trace off). A delayed branch and its slot are one entry, the branch's PC.
func (e *Emulator) SetTrace(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debugger()
	d.trace, d.tpos = nil, 0
	if n > 0 {
		size := 1
		for size < n {
			size <<= 1
		}
		d.trace = make([]uint32, size)
	}
	d.refresh()
}

// Trace returns the recorded PCs, oldest first, and how many were recorded since the trace
// was turned on (more than len when the ring has wrapped).
func (e *Emulator) Trace() ([]uint32, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.debug
	if d == nil || d.trace == nil {
		return nil, 0
	}
	size := uint64(len(d.trace))
	n := min(d.tpos, size)
	out := make([]uint32, n)
	for i := uint64(0); i < n; i++ {
		out[i] = d.trace[(d.tpos-n+i)&(size-1)]
	}
	return out, d.tpos
}

// ---- registers ------------------------------------------------------------------------

// SRFlags is SR decoded.
type SRFlags struct {
	T, S, Q, M bool
	IMASK      uint8
	DSP        bool // bit 12: the SH4AL-DSP's DSP enable (kept, not acted on by the core)
	FD         bool
	BL, RB, MD bool
}

// DecodeSR splits an SR value into its fields.
func DecodeSR(sr uint32) SRFlags {
	return SRFlags{T: sr&srT != 0, S: sr&srS != 0, Q: sr&srQ != 0, M: sr&srM != 0,
		IMASK: uint8(sr & srIMASK >> 4), DSP: sr&0x1000 != 0, FD: sr&srFD != 0,
		BL: sr&srBL != 0, RB: sr&srRB != 0, MD: sr&srMD != 0}
}

// Regs is a snapshot of the CPU's registers.
type Regs struct {
	R            [16]uint32 // R0-R15 as instructions name them now
	Bank0, Bank1 [8]uint32  // R0-R7 of each bank; one of them is R[0:8]
	Bank         int        // the bank R0-R7 name now: 1 when SR.MD and SR.RB are both set
	SR           uint32
	Flags        SRFlags
	GBR, VBR     uint32
	SSR, SPC     uint32
	SGR, DBR     uint32
	PR, PC       uint32
	MACH, MACL   uint32
	// FPUL and FPSCR are the two words lds/sts/sts.l with those encodings move (on the
	// SH4AL-DSP the 0x6A pair is DSR); the core has no FPU and no DSP data registers.
	FPUL, FPSCR uint32
	Sleeping    bool   // halted by `sleep` until an interrupt
	Cycles      uint64 // Emulator.Cycles
	Executed    uint64 // Emulator.Executed
}

// Registers returns a snapshot of every register.
func (e *Emulator) Registers() Regs {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.regsLocked()
}

func (e *Emulator) regsLocked() Regs {
	c := e.cpu
	r := Regs{R: c.r, SR: c.sr, Flags: DecodeSR(c.sr), GBR: c.gbr, VBR: c.vbr, SSR: c.ssr, SPC: c.spc,
		SGR: c.sgr, DBR: c.dbr, PR: c.pr, PC: c.pc, MACH: c.mach, MACL: c.macl, FPUL: c.fpul,
		FPSCR: c.fpscr, Sleeping: c.sleeping, Cycles: c.cycles, Executed: c.cycles - c.idle}
	var cur [8]uint32
	copy(cur[:], c.r[:8])
	if bank1(c.sr) {
		r.Bank, r.Bank1, r.Bank0 = 1, cur, c.rbank1
	} else {
		r.Bank0, r.Bank1 = cur, c.rbank1
	}
	return r
}

var regNames = func() []string {
	n := make([]string, 0, 45)
	for i := 0; i < 16; i++ {
		n = append(n, fmt.Sprintf("r%d", i))
	}
	for b := 0; b < 2; b++ {
		for i := 0; i < 8; i++ {
			n = append(n, fmt.Sprintf("r%d_bank%d", i, b))
		}
	}
	return append(n, "sr", "gbr", "vbr", "ssr", "spc", "sgr", "dbr", "pr", "pc", "mach", "macl", "fpul", "fpscr")
}()

// RegisterNames lists the names SetRegister accepts.
func RegisterNames() []string { return append([]string(nil), regNames...) }

// SetRegister sets one register by name (RegisterNames; case does not matter). Writing SR goes
// through the CPU's own SR write, so changing MD/RB switches the bank R0-R7 name, as `ldc`
// does. Writing PC wakes a sleeping CPU, and a breakpoint there stops before it runs.
func (e *Emulator) SetRegister(name string, v uint32) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cpu
	name = strings.ToLower(name)
	var i, b int
	if n, _ := fmt.Sscanf(name, "r%d_bank%d", &i, &b); n == 2 && i >= 0 && i < 8 && (b == 0 || b == 1) {
		if (b == 1) == bank1(c.sr) {
			c.r[i] = v
		} else {
			c.rbank1[i] = v
		}
		return nil
	}
	if n, _ := fmt.Sscanf(name, "r%d", &i); n == 1 && i >= 0 && i < 16 && name == fmt.Sprintf("r%d", i) {
		c.r[i] = v
		return nil
	}
	switch name {
	case "sr":
		c.setSR(v)
	case "gbr":
		c.gbr = v
	case "vbr":
		c.vbr = v
	case "ssr":
		c.ssr = v
	case "spc":
		c.spc = v
	case "sgr":
		c.sgr = v
	case "dbr":
		c.dbr = v
	case "pr":
		c.pr = v
	case "pc":
		c.pc, c.sleeping = v, false
		if e.debug != nil {
			e.debug.skipArmed = false
		}
	case "mach":
		c.mach = v
	case "macl":
		c.macl = v
	case "fpul":
		c.fpul = v
	case "fpscr":
		c.fpscr = v
	default:
		return fmt.Errorf("no register %q", name)
	}
	return nil
}

// ---- memory ---------------------------------------------------------------------------

// MemErrKind says why the debugger could not access an address.
type MemErrKind uint8

const (
	MemUnmapped MemErrKind = iota + 1 // nothing on the bus there (the CPU would halt)
	MemTLBMiss                        // translated and no valid TLB entry (the CPU would take a TLB miss)
	MemMMIO                           // a device register: refused, since reading one can change the device
	MemReadOnly                       // a write to flash beyond the 32 MB array (reads 0xFF)
)

// MemError is a debugger access that could not be made.
type MemError struct {
	Addr uint32
	Kind MemErrKind
}

func (m *MemError) Error() string {
	what := [...]string{"", "unmapped", "TLB miss", "device registers (MMIO), not read by the debugger", "read-only"}
	return fmt.Sprintf("%08x: %s", m.Addr, what[m.Kind])
}

// AddrInfo is where an address leads.
type AddrInfo struct {
	Addr       uint32 // the address asked about
	Bus        uint32 // the bus address (Watchpoint.Phys)
	Translated bool   // it went through the TLB
	Region     string // "flash", "dram", "ilram", "xyram", "ocram"; "mmio" or "" (MemError)
}

// region resolves addr (virtual, or with phys a bus address) to its backing store: buf[off:]
// with n bytes contiguous from there (bounded by a TLB page). buf == nil with no error is the
// erased flash beyond the array (reads 0xFF). No device is touched.
func (e *Emulator) region(addr uint32, phys bool) (info AddrInfo, buf []byte, off, n uint32, err error) {
	m := e.mem
	info.Addr = addr
	va := addr
	limit := ^uint32(0)
	if phys {
		if addr < 0x20000000 {
			va = 0xA0000000 | addr // the uncached P2 view of the bus
		} else if addr < 0xE0000000 {
			return info, nil, 0, 0, &MemError{addr, MemUnmapped}
		}
	} else if m.translated(va) {
		pa, left, ok := m.mmu.peek(va, m.priv())
		if !ok {
			return info, nil, 0, 0, &MemError{addr, MemTLBMiss}
		}
		info.Translated, va, limit = true, 0xA0000000|pa, left
	}
	reg := func(name string, b []byte, o uint32) (AddrInfo, []byte, uint32, uint32, error) {
		info.Region = name
		return info, b, o, min(uint32(len(b))-o, limit), nil
	}
	if va >= 0xE0000000 {
		info.Bus = va
		switch {
		case va-IlramBase < IlramSize:
			o := (va - IlramBase) & IlramMask
			info.Bus = IlramBase + o
			info, buf, off, n, err = reg("ilram", m.ilram, o)
			return info, buf, off, min(n, IlramMask+1-o), err
		case va-XyramBase < XyramSize:
			return reg("xyram", m.xyram, va-XyramBase)
		case va-OcramBase < OcramSize:
			return reg("ocram", m.ocram, va-OcramBase)
		}
		info.Region = "mmio"
		return info, nil, 0, 0, &MemError{addr, MemMMIO}
	}
	p := va & 0x1FFFFFFF
	info.Bus = p
	if p-DramBase < DramSize {
		return reg("dram", m.dram, p-DramBase)
	}
	if va >= 0xA4000000 && va < 0xC0000000 {
		info.Region = "mmio"
		return info, nil, 0, 0, &MemError{addr, MemMMIO}
	}
	if p < FlashMutTop {
		return reg("flash", m.flash, p)
	}
	if p < FlashSize {
		info.Region = "flash"
		return info, nil, 0, min(FlashSize-p, limit), nil
	}
	return info, nil, 0, 0, &MemError{addr, MemUnmapped}
}

// Translate says where a virtual address leads (through the TLB as the CPU would, now).
func (e *Emulator) Translate(va uint32) (AddrInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	info, _, _, _, err := e.region(va, false)
	return info, err
}

// ReadMemory copies memory at addr into dst for the debugger: a virtual address through the
// current MMU state (phys false), or a bus address (phys true: 0x00000000-0x1FFFFFFF, and
// the on-chip memories at their P4 addresses). It has no side effects: device registers are
// refused (MemMMIO), the flash reads as its array even during an erase, the TLB is only
// looked up. It stops at the first byte it cannot read and returns how many it copied, with
// a *MemError saying why.
func (e *Emulator) ReadMemory(addr uint32, dst []byte, phys bool) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.copyMem(addr, dst, phys, false)
}

// WriteMemory writes src at addr (as ReadMemory) straight into RAM or the flash array, as a
// debugger patch: no device sees it (MMIO is refused), the flash takes it without its
// command sequence, and the CPU executes the new bytes from its next fetch (the core decodes
// every instruction from memory: there is no cache to flush). Patched flash is part of the
// machine from then on (save-states, FlashImage).
func (e *Emulator) WriteMemory(addr uint32, src []byte, phys bool) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.copyMem(addr, src, phys, true)
}

func (e *Emulator) copyMem(addr uint32, b []byte, phys, write bool) (int, error) {
	done := 0
	for done < len(b) {
		a := addr + uint32(done)
		if done > 0 && a == 0 {
			return done, &MemError{a, MemUnmapped} // wrapped
		}
		_, buf, off, n, err := e.region(a, phys)
		if err != nil {
			return done, err
		}
		k := int(min(uint64(n), uint64(len(b)-done)))
		switch {
		case buf == nil && write:
			return done, &MemError{a, MemReadOnly}
		case buf == nil:
			for i := 0; i < k; i++ {
				b[done+i] = 0xFF
			}
		case write:
			copy(buf[off:off+uint32(k)], b[done:done+k])
		default:
			copy(b[done:done+k], buf[off:off+uint32(k)])
		}
		done += k
	}
	return done, nil
}

// peek reads a big-endian value of size 1/2/4 for the debugger (no side effects).
func (e *Emulator) peek(addr, size uint32, phys bool) (uint32, error) {
	var b [4]byte
	if _, err := e.copyMem(addr, b[:size], phys, false); err != nil {
		return 0, err
	}
	return beRead(b[:], 0, size), nil
}

// Peek32 reads a 32-bit big-endian word as ReadMemory does.
func (e *Emulator) Peek32(addr uint32, phys bool) (uint32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.peek(addr, 4, phys)
}

// ---- call stack -----------------------------------------------------------------------

// FrameSource says where a call-stack frame came from.
type FrameSource uint8

const (
	FramePC    FrameSource = iota // frame 0: the current PC
	FramePR                       // the PR register (a function that has not saved it yet)
	FrameStack                    // a word on the stack
)

// Frame is one entry of the call stack: frame 0 is where the CPU is, each later one a return
// address in the function that called the one before.
type Frame struct {
	PC        uint32      // frame 0: the current PC; others: the return address
	Call      uint32      // the call instruction (PC-4); 0 for frame 0
	SP        uint32      // FrameStack: the stack slot it was found in
	Source    FrameSource //
	Confirmed bool        // the call is a bsr whose target begins the inner frame's function
}

// callStackScan is how far above r15 CallStack looks for return addresses.
const callStackScan = 0x4000

// CallStack reconstructs up to max frames (0 = 32), best effort, from the code and the stack
// alone (nothing is tracked while running). Return addresses are words whose instruction 4
// bytes before is a call (bsr, bsrf, jsr): PR, then the stack from r15 up (16 KB). PR is
// used when the current function has not saved it yet (no `sts.l pr,@-r15` between the
// function's start and the PC). A bsr whose target is not at or shortly before the inner
// frame's code is taken for a stale word and dropped; jsr/bsrf targets are unknown, so those
// frames are kept unconfirmed. Reliable for ordinary compiled code that saves PR in its
// prologue; it can be wrong with stale return addresses left in a frame, a prologue that a
// branch skipped or that comes after an early return, tail calls (jmp/bra), and exception
// or interrupt handlers (the interrupted PC is in SPC, not on the stack).
func (e *Emulator) CallStack(max int) []Frame {
	e.mu.Lock()
	defer e.mu.Unlock()
	if max <= 0 {
		max = 32
	}
	c := e.cpu
	frames := []Frame{{PC: c.pc, Source: FramePC}}
	inner := c.pc
	callAt := func(ret uint32) (uint32, bool) {
		if ret&1 != 0 || ret < 4 {
			return 0, false
		}
		op, err := e.peek(ret-4, 2, false)
		if err != nil || !isCallOp(op) {
			return 0, false
		}
		return op, true
	}
	// bsrOK: a bsr at call (return address ret) calls code at or below inner (within 64 KB)
	bsrOK := func(op, ret, inner uint32) (ok, confirmed bool) {
		if op&0xF000 != 0xB000 {
			return true, false
		}
		disp := op & 0xFFF
		if disp&0x800 != 0 {
			disp -= 0x1000
		}
		t := ret + disp*2
		ok = t <= inner && inner-t < 0x10000
		return ok, ok
	}
	sp := c.r[15] &^ 3
	type cand struct {
		ret, at uint32
		op      uint32
	}
	var cands []cand
	for a := sp; a-sp < callStackScan && len(cands) < 4*max; a += 4 {
		w, err := e.peek(a, 4, false)
		if err != nil {
			break
		}
		if op, ok := callAt(w); ok {
			cands = append(cands, cand{w, a, op})
		}
	}
	// PR is the caller's return address while the current function has not saved it: look
	// for `sts.l pr,@-r15` between the function's start (a bsr's target, else just after the
	// nearest rts before the PC) and the PC
	if op, ok := callAt(c.pr); ok {
		ok, conf := bsrOK(op, c.pr, inner)
		start, known := uint32(0), false
		if conf {
			disp := op & 0xFFF
			if disp&0x800 != 0 {
				disp -= 0x1000
			}
			start, known = c.pr+disp*2, true
		} else if ok {
			for a := c.pc - 2; c.pc-a < 0x1000; a -= 2 {
				if w, err := e.peek(a, 2, false); err != nil {
					break
				} else if w == 0x000B {
					start, known = a+4, true
					break
				}
			}
		}
		saved := false
		for a := start; known && a < c.pc && c.pc-a <= 0x1000; a += 2 {
			if w, err := e.peek(a, 2, false); err != nil || w == 0x4F22 {
				saved = err == nil
				break
			}
		}
		if ok && !saved {
			frames = append(frames, Frame{PC: c.pr, Call: c.pr - 4, Source: FramePR, Confirmed: conf})
			inner = c.pr - 4
		}
	}
	for _, k := range cands {
		if len(frames) >= max {
			break
		}
		ok, conf := bsrOK(k.op, k.ret, inner)
		if !ok {
			continue
		}
		frames = append(frames, Frame{PC: k.ret, Call: k.ret - 4, SP: k.at, Source: FrameStack, Confirmed: conf})
		inner = k.ret - 4
	}
	return frames
}
