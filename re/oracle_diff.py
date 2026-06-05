#!/usr/bin/env python3
"""
Oracle lockstep diff for the BCD->glyph formatter bug (RECON_NOTES cont.17c).

The Go emulator boots the real 3.60 flash, types 98765+EXE, and at the FIRST entry
to the result formatter FUN_800fc5a4 (with r4 -> the result BCD 10 49 87 65) it dumps
the FULL machine state to emu_go/fmt_snapshot.bin and then PURELY single-steps 400000
instructions (no MMIO tick, no IRQ) logging architectural state before each step to
emu_go/fmt_trace_go.txt.

This script loads that snapshot into the Python reference CPU (the oracle), steps the
SAME pure way, and compares state line-by-line. The FIRST divergence pins the
mis-emulated instruction: the divergence observed at trace index i was PRODUCED by the
instruction executed at index i-1 (whose PC the Go log records on line i-1).

Run:  python re/oracle_diff.py
"""
import os, sys, struct

HERE = os.path.dirname(__file__)
sys.path.insert(0, os.path.join(HERE, "..", "emu"))
sys.path.insert(0, HERE)

from memory import Memory, DRAM_SIZE, ILRAM_SIZE, OCRAM_SIZE
from mmio import MMIOBus
from cpu import CPU

try:
    import sh4dis
    def disasm(op, pc): return sh4dis.decode(op, pc)
except Exception:
    def disasm(op, pc): return f"0x{op:04x}"

FLASH = os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin")
SNAP = os.path.join(HERE, "..", "emu_go", "fmt_snapshot.bin")
TRACE = os.path.join(HERE, "..", "emu_go", "fmt_trace_go.txt")

# field layout of each trace line (after idx): matches captureFormatter() in main.go
#   pc r0..r15 sr mach macl pr gbr
FIELDS = ["pc"] + [f"r{i}" for i in range(16)] + ["sr", "mach", "macl", "pr", "gbr"]


def load_snapshot(cpu, mem):
    with open(SNAP, "rb") as f:
        blob = f.read()
    regs = struct.unpack_from("<36I", blob, 0)
    off = 36 * 4
    cpu.r = list(regs[0:16])
    cpu.rbank1 = list(regs[16:24])
    (cpu.pc, cpu.pr, cpu.gbr, cpu.vbr, cpu.ssr, cpu.spc, cpu.sgr,
     cpu.mach, cpu.macl, cpu.fpul, cpu.fpscr, sr) = regs[24:36]
    cpu._sr = sr  # active bank already reflects this RB; assign directly (no swap)
    mem.dram[:] = blob[off:off + DRAM_SIZE]; off += DRAM_SIZE
    mem.ilram[:] = blob[off:off + ILRAM_SIZE]; off += ILRAM_SIZE
    mem.ocram[:] = blob[off:off + OCRAM_SIZE]; off += OCRAM_SIZE
    assert off == len(blob), f"snapshot size mismatch: used {off} of {len(blob)}"


def py_state(cpu):
    return {
        "pc": cpu.pc & 0xFFFFFFFF,
        **{f"r{i}": cpu.r[i] & 0xFFFFFFFF for i in range(16)},
        "sr": cpu._sr & 0xFFFFFFFF, "mach": cpu.mach & 0xFFFFFFFF,
        "macl": cpu.macl & 0xFFFFFFFF, "pr": cpu.pr & 0xFFFFFFFF,
        "gbr": cpu.gbr & 0xFFFFFFFF,
    }


def main():
    image = open(FLASH, "rb").read()
    print(f"loaded flash_full.bin: {len(image)} bytes")
    mmio = MMIOBus(log=False)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    load_snapshot(cpu, mem)
    print(f"snapshot loaded: pc=0x{cpu.pc:08x} r4=0x{cpu.r[4]:08x} [r4]=0x{mem.r32(cpu.r[4]):08x} sr=0x{cpu._sr:08x}")

    prev = None       # (idx, raw go fields) of previous line
    history = []      # last few go lines for context
    n = 0
    with open(TRACE) as tf:
        for line in tf:
            parts = line.split()
            idx = int(parts[0])
            go = {FIELDS[i]: int(parts[1 + i], 16) for i in range(len(FIELDS))}
            history.append((idx, go))
            if len(history) > 6:
                history.pop(0)

            py = py_state(cpu)
            diff = [k for k in FIELDS if go[k] != py[k]]
            if diff:
                print(f"\n*** DIVERGENCE at trace index {idx} (after {n} matched steps) ***")
                if prev is not None:
                    pidx, pg = prev
                    cpc = pg["pc"]
                    op = mem.r16(cpc)
                    print(f"  culprit instruction @ index {pidx}: pc=0x{cpc:08x}  op=0x{op:04x}  {disasm(op, cpc)}")
                print(f"  diverging fields at index {idx} (go = expected, py = oracle):")
                for k in diff:
                    print(f"    {k:5s}  go=0x{go[k]:08x}  py=0x{py[k]:08x}")
                print("  context (go trace, last lines):")
                for hidx, hg in history:
                    mark = "<-- divergence here" if hidx == idx else ""
                    print(f"    [{hidx}] pc=0x{hg['pc']:08x} op=0x{mem.r16(hg['pc']):04x} {disasm(mem.r16(hg['pc']), hg['pc'])} {mark}")
                return

            # step the oracle exactly like captureFormatter (pure, no interrupts)
            op = mem.r16(cpu.pc)
            cpu.pc = (cpu.pc + 2) & 0xFFFFFFFF
            cpu.execute(op)
            cpu.cycles += 1
            prev = (idx, go)
            n += 1

    print(f"\nNO divergence across all {n} traced steps — Go and oracle agree. "
          f"The formatter bug is NOT a CPU instruction divergence (look at data/MMIO/config).")


if __name__ == "__main__":
    main()
