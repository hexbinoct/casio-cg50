package main

// Machine save-state: a full snapshot of the volatile machine (CPU registers + DRAM,
// ILRAM, OCRAM + the flash delta) so a later launch RESUMES at the exact instant it was
// taken — e.g. the MAIN MENU after first-boot setup — instead of re-running the language/
// setup wizard every cold start. This is the persistence the Android app wants: provision
// once, then resume instantly. (Flash-only persistence is insufficient: fls0 mount state is
// coupled to battery-backed RAM the real calc keeps alive, so we snapshot RAM too.)

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const stateMagic = "CG50ST01"

// statePath is the default save-state file (git-ignored under os/, since it holds OS-derived
// RAM/flash bytes). Shared by the `provision`/auto-resume path in main and the web UI buttons.
const statePath = "../os/flash_dump/cg50_state.bin"

// snapshotRegs / restoreRegs marshal the architectural registers (raw banks: c.r is the
// active bank, c.rbank1 the inactive one, and c.sr holds the RB bit — so restoring all
// three verbatim is consistent without re-banking).
func (c *CPU) snapshotRegs() []uint32 {
	b := make([]uint32, 0, 36)
	b = append(b, c.r[:]...)
	b = append(b, c.rbank1[:]...)
	b = append(b, c.pc, c.pr, c.gbr, c.vbr, c.ssr, c.spc, c.sgr,
		c.mach, c.macl, c.fpul, c.fpscr, c.sr)
	return b // 16 + 8 + 12 = 36
}

func (c *CPU) restoreRegs(b []uint32) {
	copy(c.r[:], b[0:16])
	copy(c.rbank1[:], b[16:24])
	i := 24
	c.pc, c.pr, c.gbr, c.vbr = b[i], b[i+1], b[i+2], b[i+3]
	c.ssr, c.spc, c.sgr = b[i+4], b[i+5], b[i+6]
	c.mach, c.macl, c.fpul, c.fpscr, c.sr = b[i+7], b[i+8], b[i+9], b[i+10], b[i+11]
}

// SnapshotBytes returns a gzip-compressed save-state in memory (for hosts that persist a
// byte[] themselves — e.g. the Android bridge or the web UI). SaveState wraps it to a file.
func SnapshotBytes(cpu *CPU, mem *Memory) ([]byte, error) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)

	var u32 [4]byte
	wU32 := func(v uint32) error {
		binary.BigEndian.PutUint32(u32[:], v)
		_, e := gz.Write(u32[:])
		return e
	}
	wBlock := func(p []byte) error {
		if e := wU32(uint32(len(p))); e != nil {
			return e
		}
		_, e := gz.Write(p)
		return e
	}

	if _, err := gz.Write([]byte(stateMagic)); err != nil {
		return nil, err
	}
	regs := cpu.snapshotRegs()
	if err := wU32(uint32(len(regs))); err != nil {
		return nil, err
	}
	for _, v := range regs {
		if err := wU32(v); err != nil {
			return nil, err
		}
	}
	for _, blk := range [][]byte{mem.dram, mem.ilram, mem.ocram, mem.flashDeltaBytes()} {
		if err := wBlock(blk); err != nil {
			return nil, err
		}
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// SaveState writes a gzip-compressed snapshot to path.
func SaveState(path string, cpu *CPU, mem *Memory) error {
	b, err := SnapshotBytes(cpu, mem)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

// LoadState restores a snapshot from path. ResumeBytes does the same from an in-memory blob.
func LoadState(path string, cpu *CPU, mem *Memory) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return ResumeBytes(raw, cpu, mem)
}

// ResumeBytes restores a gzip snapshot and leaves the CPU ready to RESUME from the saved PC.
// The caller should reset the free-running cycle counter / timer schedule afterward (the OS
// only uses timer deltas, so an absolute counter reset is invisible).
func ResumeBytes(raw []byte, cpu *CPU, mem *Memory) error {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		return err
	}
	if len(data) < 8 || string(data[:8]) != stateMagic {
		return fmt.Errorf("save-state: bad magic")
	}
	p := 8
	rU32 := func() (uint32, error) {
		if p+4 > len(data) {
			return 0, fmt.Errorf("save-state: truncated header")
		}
		v := binary.BigEndian.Uint32(data[p : p+4])
		p += 4
		return v, nil
	}
	rBlock := func(dst []byte, name string) error {
		n, e := rU32()
		if e != nil {
			return e
		}
		if p+int(n) > len(data) {
			return fmt.Errorf("save-state: %s block overruns file", name)
		}
		if int(n) != len(dst) && dst != nil {
			return fmt.Errorf("save-state: %s size %d != expected %d", name, n, len(dst))
		}
		copy(dst, data[p:p+int(n)])
		p += int(n)
		return nil
	}

	nRegs, err := rU32()
	if err != nil {
		return err
	}
	if int(nRegs) > (len(data)-p)/4 {
		return fmt.Errorf("save-state: bad reg count")
	}
	regs := make([]uint32, nRegs)
	for i := range regs {
		if regs[i], err = rU32(); err != nil {
			return err
		}
	}
	cpu.restoreRegs(regs)
	if err := rBlock(mem.dram, "dram"); err != nil {
		return err
	}
	if err := rBlock(mem.ilram, "ilram"); err != nil {
		return err
	}
	if err := rBlock(mem.ocram, "ocram"); err != nil {
		return err
	}
	// flash delta is variable-length; read it raw then apply over the baseline flash.
	fn, err := rU32()
	if err != nil {
		return err
	}
	if p+int(fn) > len(data) {
		return fmt.Errorf("save-state: flash block overruns file")
	}
	if _, err := mem.applyFlashDelta(data[p : p+int(fn)]); err != nil {
		return err
	}
	p += int(fn)
	cpu.pending = nil // resume cleanly; a fresh timer IRQ will re-arm
	return nil
}
