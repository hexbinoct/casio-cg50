#!/usr/bin/env python3
"""
fx-CG50 emulator runner — boot the OS image from reset and trace.

Usage:
    python run.py [max_instructions] [trace_count]

Loads os/os_image/cg50_os_3.80.plain.bin, sets PC=0x80000000 (reset_entry),
and single-steps. Prints a disassembly+effect trace for the first `trace_count`
instructions, then runs quietly to `max_instructions` or until a fault.

Success criterion for the skeleton: reproduce the boot MMIO writes documented in
RECON_NOTES.md (SR, CCR@0xFF00001C, MMUCR@0xFF000010, CPG, WDT, PFC, BSC) and get
past the early init without hitting an unimplemented instruction.
"""
import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "re"))

from memory import Memory, MemFault
from mmio import MMIOBus
from cpu import CPU, IllegalInstruction

try:
    import sh4dis            # reuse our disassembler for the trace
    def disasm(op, pc): return sh4dis.decode(op, pc)
except Exception:
    def disasm(op, pc): return f"0x{op:04x}"

IMG = os.path.join(os.path.dirname(__file__), "..", "os", "os_image", "cg50_os_3.80.plain.bin")


def main():
    max_ins = int(sys.argv[1], 0) if len(sys.argv) > 1 else 300
    trace_n = int(sys.argv[2], 0) if len(sys.argv) > 2 else 80

    image = open(IMG, "rb").read()
    print(f"loaded OS image: {len(image)} bytes (0x{len(image):x})")

    mmio = MMIOBus(log=True)
    mem = Memory(image, mmio)
    cpu = CPU(mem)
    cpu.pc = 0x80000000
    print(f"reset: PC=0x{cpu.pc:08x} SR=0x{cpu.sr:08x}\n")

    # milestone addresses (from RECON_NOTES): reaching these = boot progressed
    milestones = {
        0x80003550: "os_main_loop",
        0x8000936e: "event_dispatch",
        0x80055260: "Bdisp_PutDisp_DD (frame push)",
        0x80002c8c: "timer ISR",
    }
    seen = set()

    last_pc = None
    reason = "reached max instructions"
    for i in range(max_ins):
        pc = cpu.pc
        if pc in milestones and pc not in seen:
            seen.add(pc)
            print(f"  *** reached {milestones[pc]} @0x{pc:08x} after {cpu.cycles} instructions")
        op = mem.r16(pc)
        if i < trace_n:
            mmio.log = True
            print(f"{i:5d} {pc:08x}: {op:04x}  {disasm(op, pc)}")
        else:
            mmio.log = False
        try:
            cpu.step()
        except IllegalInstruction as e:
            reason = f"ILLEGAL instruction at 0x{pc:08x}: {e}"
            break
        except MemFault as e:
            reason = f"MEM fault at 0x{pc:08x}: {e}"
            break
        except Exception as e:
            reason = f"{type(e).__name__} at 0x{pc:08x}: {e}"
            break
        if cpu.pc == pc and op not in (0x0009,):
            reason = f"PC stuck (tight loop / wait) at 0x{pc:08x}"
            break
        last_pc = pc
    else:
        i += 1

    print(f"\n=== stopped after {cpu.cycles} instructions: {reason} ===")
    print(f"PC=0x{cpu.pc:08x}  PR=0x{cpu.pr:08x}  SR=0x{cpu.sr:08x}  "
          f"VBR=0x{cpu.vbr:08x}  GBR=0x{cpu.gbr:08x}")
    print("regs:", " ".join(f"r{i}=0x{cpu.r[i]:08x}" for i in range(16)))
    if mmio.unknown:
        print("\nunmapped MMIO touched:")
        for va, c in sorted(mmio.unknown.items()):
            print(f"  0x{va:08x}  x{c}")


if __name__ == "__main__":
    main()
