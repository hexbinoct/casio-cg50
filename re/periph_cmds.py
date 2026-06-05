#!/usr/bin/env python3
"""
Enumerate the 0xA4CB0000 peripheral's COMMAND SET and the I/O shape of each accessor.

Each accessor follows the pattern (see FUN_80072fc8):
    r2 = &0xA4CB0014 ; *r2 = rA            (operand A, mov.l)
    r2 = &0xA4CB0018 ; *r2 = rB            (operand B, mov.l)
    r1 = #cmd ; r2 = &0xA4CB0010 ; *r2 = r1 (command, mov.w)
    r2 = &0xA4CB001C ; rX = *r2            (result, mov.l)

We disassemble the math module (0x80072c00..0x80074100) just enough to recover, for each
write to the command register, the immediate command value (the `mov #imm,rN` that feeds it).
That gives the full command vocabulary the OS uses, which is what we must implement.

Run:  python re/periph_cmds.py
"""
import os, collections

HERE = os.path.dirname(__file__)
flash = open(os.path.join(HERE, "..", "os", "flash_dump", "flash_full.bin"), "rb").read()

CMD_REG = 0xA4CB0010
MOD_LO, MOD_HI = 0x00072c00, 0x00074200   # phys range of the BCD math module


def be16(o): return (flash[o] << 8) | flash[o + 1]
def be32(o): return (flash[o] << 24) | (flash[o+1] << 16) | (flash[o+2] << 8) | flash[o+3]


def main():
    # 1) find pool slots in the module that hold 0xA4CB0010 (the command-reg address)
    cmd_pool = set()
    for o in range(MOD_LO, MOD_HI, 2):
        if be32(o) == CMD_REG:
            cmd_pool.add(o)
    print(f"command-reg (0xA4CB0010) pool slots in module: {len(cmd_pool)}")

    # 2) walk the module; track last `mov #imm,rN` per register, and the pc that loads a
    #    pool slot holding 0xA4CB0010 into a reg (mov.l @(disp,pc),r2). When we see a
    #    `mov.w rS,@r2` where r2 was last loaded with the cmd-reg address, rS's last imm = cmd.
    last_imm = {}      # reg -> (imm, pc)
    reg_is_cmdaddr = {}  # reg -> True if it currently holds &0xA4CB0010
    cmds = collections.Counter()
    sites = []
    o = MOD_LO
    while o < MOD_HI:
        op = be16(o)
        pc = 0x80000000 + o
        n = (op >> 8) & 0xF
        m = (op >> 4) & 0xF
        # mov #imm,Rn  : 1110nnnn iiiiiiii
        if (op & 0xF000) == 0xE000:
            imm = op & 0xFF
            if imm & 0x80: imm -= 0x100
            last_imm[n] = (imm, pc)
            reg_is_cmdaddr[n] = False
        # mov.l @(disp,pc),Rn : 1101nnnn dddddddd
        elif (op & 0xF000) == 0xD000:
            disp = op & 0xFF
            base = (pc + 4) & ~3
            tgt = base + disp * 4
            val = be32(tgt - 0x80000000) if 0 <= tgt - 0x80000000 < len(flash) else 0
            reg_is_cmdaddr[n] = (val == CMD_REG)
            last_imm[n] = None
        # mov.w Rm,@Rn : 0010nnnn mmmm0001  (store to cmd reg)
        elif (op & 0xF00F) == 0x2001:
            if reg_is_cmdaddr.get(n):
                src = last_imm.get(m)
                if src is not None:
                    cmds[src[0]] += 1
                    sites.append((pc, src[0], src[1]))
                else:
                    cmds["<reg>"] += 1
                    sites.append((pc, None, None))
        o += 2

    print(f"\ncommand values written to 0xA4CB0010 ({sum(cmds.values())} writes):")
    for k in sorted(cmds, key=lambda x: (isinstance(x, str), x)):
        print(f"   cmd = {k!r:>6}  x{cmds[k]}")
    print("\nsites (store pc -> cmd, imm-load pc):")
    for pc, cmd, ipc in sites:
        ip = f"0x{ipc:08x}" if ipc else "-"
        print(f"   store@0x{pc:08x}  cmd={cmd}  (imm loaded @{ip})")


if __name__ == "__main__":
    main()
