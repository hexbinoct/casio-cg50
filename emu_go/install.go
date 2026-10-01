package main

// Installing an add-in: the host hands over a .g3a file and the OS itself writes it to
// \\fls0\NAME.g3a with its own Bfile_* syscalls (oscall.go), exactly as when a file is copied
// to the real calculator over USB, then rebuilds its add-in table so the MAIN MENU lists it.

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	installChunk  = 32 << 10 // bytes per Bfile_WriteFile_OS call (the buffer lives in the OS heap)
	installBudget = 2_000_000_000
	maxAddinSize  = 2 << 20 // the OS's own limit for a .g3a (FUN_8002d1a4 skips larger files)
)

// InstallAddin writes g3a to \\fls0\<name> (replacing a file of that name) and refreshes the
// MAIN MENU's add-in list. name is a plain file name such as "DASM.g3a". It works at the MAIN
// MENU and in the built-in apps; while an add-in runs it returns errNotIdle.
func (e *Emulator) InstallAddin(name string, g3a []byte) error {
	if err := checkAddin(name, g3a); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// a key or a timer may have just woken it: give it a moment to go back to sleep
	for i := 0; i < 50 && !e.cpu.sleeping; i++ {
		e.stepLocked(1_000_000)
	}
	// At the MAIN MENU, step out of it into Run-Matrix first and come back with MENU at the
	// end. Coming back builds a fresh menu, the only way the new icon appears (the menu counts
	// its icons once, when it is entered: FUN_8036427a). Leaving also ends an add-in the menu
	// may be sitting on (MENU inside an add-in keeps it suspended, its code mapped from the very
	// flash the OS is about to rewrite). In any other app the next MENU press shows the icon.
	if e.atMainMenu() {
		e.tapAndSettle(6, 2, 0) // 1: Run-Matrix, waiting for a key
		defer e.tapAndSettle(3, 8, 1)
	}
	if !e.osIdle() {
		return errNotIdle
	}
	if err := e.writeFile(name, g3a); err != nil {
		return err
	}
	if r, err := e.syscall(sysAddinTableBuild, installBudget); err != nil || r != 0 {
		return osErr("add-in table rebuild", int32(r), err)
	}
	return nil
}

// writeFile creates \\fls0\<name> with data through the OS's Bfile syscalls. Caller holds
// e.mu and has checked osIdle.
func (e *Emulator) writeFile(name string, data []byte) error {
	call := func(what string, n uint32, args ...uint32) (int32, error) {
		r, err := e.syscall(n, installBudget, args...)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", what, err)
		}
		return int32(r), nil
	}
	p, err := call("malloc", sysMalloc, 0x100+installChunk)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if p == 0 {
		return fmt.Errorf("install: the OS heap is full")
	}
	defer call("free", sysFree, uint32(p))
	path, size, buf := uint32(p), uint32(p)+0xf0, uint32(p)+0x100
	for i, ch := range `\\fls0\` + name + "\x00" {
		e.mem.W16(path+uint32(2*i), uint32(ch))
	}
	e.mem.W32(size, uint32(len(data)))

	call("delete", sysBfileDelete, path) // a missing file is fine
	if r, err := call("create", sysBfileCreate, path, 1, size); err != nil || r < 0 {
		return osErr("Bfile_CreateEntry_OS", r, err)
	}
	h, err := call("open", sysBfileOpen, path, 2, 0) // 2 = WRITE
	if err != nil || h < 0 {
		return osErr("Bfile_OpenFile_OS", h, err)
	}
	for off := 0; off < len(data); off += installChunk {
		chunk := data[off:min(off+installChunk, len(data))]
		for i, b := range chunk {
			e.mem.W8(buf+uint32(i), uint32(b))
		}
		if r, err := call("write", sysBfileWrite, uint32(h), buf, uint32(len(chunk))); err != nil || r != int32(len(chunk)) {
			call("close", sysBfileClose, uint32(h))
			return osErr("Bfile_WriteFile_OS", r, err)
		}
	}
	if r, err := call("close", sysBfileClose, uint32(h)); err != nil || r < 0 {
		return osErr("Bfile_CloseFile_OS", r, err)
	}
	return nil
}

// OS 3.60 MAIN MENU state (FUN_8036427a and the MENU-key handler FUN_80363aa8).
const (
	osInMainMenu    = 0x8c0a3043 // byte: 1 while the MAIN MENU is the active screen
	osInMainMenuRef = 0x80364448 // literal pool word of the menu code that holds that address
)

// atMainMenu reports whether the MAIN MENU is waiting for a key (possibly on top of a suspended
// add-in, so the MMU may be on). False on an OS other than 3.60.
func (e *Emulator) atMainMenu() bool {
	return e.mem.R32(osInMainMenuRef) == osInMainMenu && e.mem.R8(osInMainMenu) == 1 &&
		e.fault == "" && e.cpu.sleeping && e.cpu.sr&srRB == 0
}

// tapAndSettle taps a key, then runs until the in-menu flag reads menu and the OS idles again
// (a key tapped while an app is still starting can be lost). Caller holds e.mu.
func (e *Emulator) tapAndSettle(row, col uint32, menu byte) {
	e.tap.queue = append(e.tap.queue, [2]uint32{row, col})
	for i := 0; i < 1000 && (len(e.tap.queue) != 0 || e.tap.active ||
		e.mem.R8(osInMainMenu) != uint32(menu) || !e.osIdle()); i++ {
		e.stepLocked(1_000_000)
	}
	e.stepLocked(10_000_000) // let it finish drawing
}

func osErr(what string, r int32, err error) error {
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	return fmt.Errorf("install: %s returned %d", what, r)
}

// checkAddin rejects what the OS would not list: not a .g3a, a bad name, too big.
func checkAddin(name string, g3a []byte) error {
	switch {
	case !strings.HasSuffix(strings.ToLower(name), ".g3a"):
		return fmt.Errorf("install: %q is not a .g3a file name", name)
	case len(name) > 40 || strings.ContainsAny(name, `\/:*?"<>|`):
		return fmt.Errorf("install: bad file name %q", name)
	case len(g3a) < 0x7000 || !bytes.Equal(g3a[:8], invertBytes([]byte("USBPower"))):
		return fmt.Errorf("install: %s is not a .g3a add-in", name)
	case len(g3a) > maxAddinSize:
		return fmt.Errorf("install: %s is %d bytes; the OS lists add-ins up to %d", name, len(g3a), maxAddinSize)
	}
	for _, ch := range name {
		if ch < 0x20 || ch > 0x7e {
			return fmt.Errorf("install: file name %q must be plain ASCII", name)
		}
	}
	return nil
}

// invertBytes returns b with every bit flipped (the g3a header stores its magic inverted).
func invertBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i, x := range b {
		out[i] = ^x
	}
	return out
}
