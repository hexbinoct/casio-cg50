# How facts are verified

Facts on this site come from three places. Each page marks which one backs each claim.

## Reading the OS

The OS 3.60 image (dumped from a real calculator) is loaded in Ghidra at `0x80000000`, its
address when running. Addresses on this site are those runtime addresses.

Reading code tells you what the OS *does* with the hardware, not how the hardware
*responds*. Facts that only come from here are marked **Unconfirmed**.

## The emulator

[casio-cg50](https://github.com/hexbinoct/casio-cg50) boots the real OS from reset and runs
its apps and add-ins. If the OS or an add-in only works when the emulator behaves a certain
way, that is good evidence for the behaviour, and a test in the emulator's suite keeps it.

The emulator has two implementations, held to each other:

- a **Python** emulator, the reference, slow and simple;
- a **Go** emulator, the fast one, which must match the Python one instruction for
  instruction on a frozen boot trace and a suite of CPU test cases.

Both can be wrong the same way, which is why hardware measurements come first.

## The real calculator

Small add-ins, built with the fxSDK and gint, run on a real fx-CG50 and write their
measurements to a file or send them over USB. These are the strongest evidence on the
site. The results are kept in the repository under `os/devic_probes/`.

### The probes

Each probe, where its source and raw results live, and what on this site it backs. Paths
are in the [casio-cg50 repository](https://github.com/hexbinoct/casio-cg50).

| Probe (date) | Source | Raw results | Backs |
|---|---|---|---|
| **flash_dump** (June 2026; the full 32 MB later) | [`tools/flash_dump/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/flash_dump) | Not published: the flash dump is Casio's firmware, and the RAM dumps hold copies of OS code and data | [Memory map](../memory-map.md): flash layout and where the storage memory begins, the untouched top of DRAM, the 16 KB IL RAM, the RAM variables. [Interrupts](../peripherals/interrupts.md): the handler and priority tables in IL RAM. [Battery ADC](../peripherals/battery-adc.md): the `0x560` handler slot |
| **aluprobe** (2026-06-05) | Not in the repository | [`os/devic_probes/aluprobe_2/`](https://github.com/hexbinoct/casio-cg50/tree/main/os/devic_probes/aluprobe_2) | [BCD ALU](../peripherals/bcd-alu.md): the command set, command 4 |
| **alusweep** (2026-06-05) | Not in the repository | [`os/devic_probes/alusweep_shtest/`](https://github.com/hexbinoct/casio-cg50/tree/main/os/devic_probes/alusweep_shtest) (part A of the notes) | [BCD ALU](../peripherals/bcd-alu.md): every command, the shared carry/borrow flag, the register behaviour |
| **shtest** (2026-06-05) | Not in the repository | Same folder (part B); [`re/validate_silicon.py`](https://github.com/hexbinoct/casio-cg50/blob/main/re/validate_silicon.py) compares it with the emulator's CPU | [CPU](../cpu.md): the 202 integer arithmetic cases |
| **keyprobe** (2026-09-27) | [`tools/keyprobe/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/keyprobe) | [`keyprobe_2026-09-27.txt`](https://github.com/hexbinoct/casio-cg50/blob/main/os/devic_probes/keyprobe_2026-09-27.txt), notes in [`keyprobe-results-2026-09-27.md`](https://github.com/hexbinoct/casio-cg50/blob/main/os/devic_probes/keyprobe-results-2026-09-27.md) | [Keyboard](../peripherals/keyboard.md): the KEYSC registers, the key-data layout, the 30.3 ms scan. [Timers](../peripherals/timers.md): TMU state, FRQCR. [Display](../peripherals/display.md): the scan rate behind menu scrolling |
| **timerprobe** (2026-09-27) | [`tools/timerprobe/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/timerprobe) | [`timerprobe_2026-09-27.txt`](https://github.com/hexbinoct/casio-cg50/blob/main/os/devic_probes/timerprobe_2026-09-27.txt) | [Timers](../peripherals/timers.md): the ETMU5 counter at 32,768 per RTC second, ETMU0's values, no running TMU channel |
| **tickprobe** v1–v3 (2026-09-27) | [`tools/tickprobe/`](https://github.com/hexbinoct/casio-cg50/tree/main/tools/tickprobe) | [`tickprobe_v1_2026-09-27.txt`](https://github.com/hexbinoct/casio-cg50/blob/main/os/devic_probes/tickprobe_v1_2026-09-27.txt), [`v2`](https://github.com/hexbinoct/casio-cg50/blob/main/os/devic_probes/tickprobe_v2_2026-09-27.txt), [`v3`](https://github.com/hexbinoct/casio-cg50/blob/main/os/devic_probes/tickprobe_v3_2026-09-27.txt) | [Battery ADC](../peripherals/battery-adc.md): the unit at `0xA4610000` and its interrupt `0x560`; conversions that never finish while the CPU runs |
| **DASM** | A disassembler and debugger add-in developed alongside the emulator, in a separate project | Read on screen | [CPU](../cpu.md): SR bit 12, the UBC's "break after" decided at fetch. [Keyboard](../peripherals/keyboard.md): the key-data words hold the last scan |

The tickprobe's v4 test, a replay of the OS's idle sleep, is the one that reset a
calculator (below). It is compiled out of the source by default.

!!! danger "Probes can reset your calculator"
    A probe that put the CPU to sleep after touching the clock generator reset a calculator
    to its first-boot setup. Files in storage memory survived; everything in RAM did not.
    Read-only register sampling has been safe.
