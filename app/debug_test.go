package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"cg50emu"
)

// TestSyncBreakpoints: the page's list becomes the core's breakpoints, watchpoints and event
// breaks; an unchanged entry keeps its core breakpoint (and its hits) across changes.
func TestSyncBreakpoints(t *testing.T) {
	e := cg50emu.NewEmulator(make([]byte, 0x1000))
	var d debugState
	specs := []BPSpec{
		{ID: 1, Kind: "exec", Addr: 0x80001000, Enabled: true, Condition: "r4 == 1", Ignore: 2},
		{ID: 2, Kind: "write", Addr: 0x8C001000, Len: 4, Enabled: true},
		{ID: 3, Kind: "syscall", Addr: 0x0EAB, Enabled: true},
		{ID: 4, Kind: "exec", Addr: 0x80002000, Enabled: false, Condition: "r4 =="},
	}
	d.syncBreakpoints(e, specs)
	bps := e.Breakpoints()
	if len(bps) != 2 || bps[0].Addr != 0x80001000 || !bps[0].Enabled || bps[0].Ignore != 2 || bps[0].Cond == nil ||
		bps[1].Enabled || bps[1].Cond != nil {
		t.Fatalf("breakpoints %+v", bps)
	}
	if d.errs[4] == "" {
		t.Error("a bad condition gives no error")
	}
	ws := e.Watchpoints()
	if len(ws) != 1 || !ws[0].Phys || ws[0].Addr != 0x0C001000 || ws[0].Len != 4 || ws[0].Kind != cg50emu.WatchWrite {
		t.Fatalf("watchpoints %+v (want the bus address, every alias)", ws)
	}
	first := d.applied[1].emuID

	// turn one off, drop the watchpoint, move the second breakpoint
	specs[0].Enabled = false
	specs = append(specs[:1], specs[2:]...)
	specs[2].Addr, specs[2].Condition = 0x80003000, ""
	d.syncBreakpoints(e, specs)
	bps = e.Breakpoints()
	if len(bps) != 2 || len(e.Watchpoints()) != 0 {
		t.Fatalf("after the change: %d breakpoints, %d watchpoints", len(bps), len(e.Watchpoints()))
	}
	if d.applied[1].emuID != first || bps[0].Enabled {
		t.Errorf("the unchanged entry got a new core breakpoint, or stayed on")
	}
	if bps[1].Addr != 0x80003000 {
		t.Errorf("moved breakpoint at %08x", bps[1].Addr)
	}
}

func TestBreakpointsPersist(t *testing.T) {
	s := &store{dir: t.TempDir()}
	if err := mkdirSessions(s); err != nil {
		t.Fatal(err)
	}
	specs := []BPSpec{{ID: 7, Kind: "exec", Addr: 0x801951A6, Enabled: true, Condition: "r4 != 0"},
		{ID: 9, Kind: "exception", Any: true, Enabled: false}}
	s.saveBreakpoints("abc", specs)
	got := s.loadBreakpoints("abc")
	if fmt.Sprint(got) != fmt.Sprint(specs) {
		t.Fatalf("loaded %+v", got)
	}
	if len(s.loadBreakpoints("other")) != 0 {
		t.Error("another dump's breakpoints")
	}
	s.saveBreakpoints("abc", nil)
	if len(s.loadBreakpoints("abc")) != 0 {
		t.Error("an empty list is not saved")
	}
}

func mkdirSessions(s *store) error { return os.MkdirAll(filepath.Join(s.dir, "sessions"), 0o755) }
