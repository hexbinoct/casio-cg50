package main

// Host calls into the OS: the emulator runs an OS function (a syscall such as Bfile_WriteFile_OS
// or sys_malloc) the way an add-in would call it, and gets its r0 back. This is how files get
// into the storage memory (install.go): the OS's own file system code writes them, so fls0 stays
// exactly as consistent as on the real calculator.
//
// The call borrows the OS's main context while it idles (asleep in the GetKey loop at the MAIN
// MENU or in an app): every register is saved, the function runs on the same stack with
// interrupts and devices live (timers tick, the flash controller completes its program/erase
// cycles), and when it returns everything is restored and the OS goes back to sleep where it
// was. Unlike cpu.callInject (interrupts masked, nothing ticks, a 5 M-instruction budget for a
// short routine) this suits long, interrupt-driven work like a 2 MB file write.

import (
	"errors"
	"fmt"
)

const (
	syscallTrampoline = 0x80020070 // `mov.l @(disp,pc),r2` (table base) ... jmp; r0 = number
	osCallReturn      = 0xDEAD0000 // return address of a host call: caught, never fetched

	sysMalloc          = 0x1f44 // void *sys_malloc(size)
	sysFree            = 0x1f42 // void sys_free(p)
	sysBfileOpen       = 0x1da3 // int Bfile_OpenFile_OS(name, mode, 0)
	sysBfileClose      = 0x1da4 // int Bfile_CloseFile_OS(h)
	sysBfileCreate     = 0x1dae // int Bfile_CreateEntry_OS(name, mode, size *int)
	sysBfileWrite      = 0x1daf // int Bfile_WriteFile_OS(h, buf, size)
	sysBfileDelete     = 0x1db4 // int Bfile_DeleteEntry(name)
	sysAddinTableBuild = 0x0017 // FUN_8002d1a4: scan \\fls0\*.g3a, rebuild the add-in table
)

var errNotIdle = errors.New("the calculator is busy (an add-in or a calculation is running): press MENU and try again")

// syscallAddr returns the handler of syscall n from the running OS's table.
func (e *Emulator) syscallAddr(n uint32) uint32 {
	base := uint32(0)
	for a := uint32(syscallTrampoline); a < syscallTrampoline+0x10; a += 2 {
		if op := e.mem.R16(a); op>>8 == 0xD2 { // mov.l @(disp,pc),r2
			base = e.mem.R32((a+4)&^3 + (op&0xff)*4)
			break
		}
	}
	return e.mem.R32(base + n*4)
}

// osIdle reports whether the OS's main context can be borrowed: asleep in its idle loop
// (the OS sleeps with SR.BL=1 and register bank 0), no add-in mapped, nothing pending.
func (e *Emulator) osIdle() bool {
	c := e.cpu
	return e.fault == "" && c.sleeping && c.sr&srRB == 0 && !e.mem.mmuAt && len(c.pending) == 0
}

// callOS runs the OS function at fn with up to 4 args in r4..r7 and returns r0. Caller holds
// e.mu and has checked osIdle. budget bounds the instructions the call may run.
func (e *Emulator) callOS(fn uint32, budget uint64, args ...uint32) (uint32, error) {
	c := e.cpu
	r0, rb0 := c.r, c.rbank1
	pc0, pr0, sr0 := c.pc, c.pr, c.sr
	gbr0, ssr0, spc0, sgr0 := c.gbr, c.ssr, c.spc, c.sgr
	mach0, macl0, fpul0, fpscr0 := c.mach, c.macl, c.fpul, c.fpscr
	sleep0 := c.sleeping
	hlePC0, hle0 := c.hlePC, c.hle

	done := false
	c.hlePC, c.hle = osCallReturn, func() bool {
		done = true
		c.sleeping = true // leave cpu.run; the loop below sees done
		return true
	}
	c.r[15] = (r0[15] - 0x100) &^ 3
	for i, a := range args {
		c.r[4+i] = a
	}
	c.pr, c.pc = osCallReturn, fn
	c.setSR(sr0 &^ (srBL | srRB | srIMASK)) // the main context with interrupts on
	c.sleeping = false

	start := c.cycles
	for !done && e.fault == "" && c.cycles-start < budget {
		e.stepLocked(oscallChunk)
		if oscallTrace != nil {
			oscallTrace(c)
		}
	}
	ret := c.r[0]

	c.hlePC, c.hle = hlePC0, hle0
	c.r, c.rbank1 = r0, rb0
	c.pc, c.pr = pc0, pr0
	c.setSR(sr0)
	c.gbr, c.ssr, c.spc, c.sgr = gbr0, ssr0, spc0, sgr0
	c.mach, c.macl, c.fpul, c.fpscr = mach0, macl0, fpul0, fpscr0
	c.sleeping = sleep0
	switch {
	case e.fault != "":
		return 0, errors.New(e.fault)
	case !done:
		return 0, fmt.Errorf("OS function %#x did not return within %d instructions", fn, budget)
	}
	return ret, nil
}

// syscall runs OS syscall n (see callOS).
func (e *Emulator) syscall(n uint32, budget uint64, args ...uint32) (uint32, error) {
	return e.callOS(e.syscallAddr(n), budget, args...)
}

// probes: single-step a host call (oscallChunk = 1) and look at every instruction
var (
	oscallChunk = 200_000
	oscallTrace func(*CPU)
)
