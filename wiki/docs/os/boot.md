# Boot and power-off

What the OS does between reset and the MAIN MENU, and how it tells a power-on from "off"
apart from a cold start after the batteries were removed.

## From reset

The CPU starts at `0xA0000000`, the uncached view of flash address 0. The code at
`0x80000000`/`0xA0000000`:

1. **Registers.** Sets the stack to `0xFD804000` (top of the on-chip IL RAM) and SR to
   `0x700000F0` (privileged, register bank 1, exceptions blocked, all interrupts masked).
2. **Early pokes.** Writes `0x00010013` to the BSC (`0xFEC10000`), and three CPG registers
   (`0xA4150020` ← 1, `0xA4150030` ← `0x1000`, `0xA4150038` ← `0xFFFFFFFF`).
3. **Cache and TLB.** CCR (`0xFF00001C`) ← `0x800`, `icbi @0xA0000000`, then MMUCR
   (`0xFF000010`) ← 4, which flushes the TLB.
4. **Pins and watchdog** (`0xA0000670`). Four PFC port registers at `0xA4050184`, then the
   watchdog at `0xA4520000`: WTCNT ← `0x5A00`, WTCSR ← `0xA507` then `0xA504`.
5. **Clocks** (`0xA000069A`). Adjusts `0xA4150024`, writes `0x4384` to `0xA4150050`, and waits
   for bit 0 of `0xA4150060`. Then sets FRQCR (`0xA4150000`) to
   `(FRQCR & 0x000F00F0) | 0x8F001102` and waits for the same bit again.
6. **DRAM** (`0xA000063C`). Programs the bus/SDRAM controller: a block of timing registers
   at `0xFEC10000`–`0xFEC1003C`, then `0xFEC10040`. DRAM is usable from here.
7. **Model code.** Reads the low 16 bits of `0xFF000024` and stores a model code at
   `0x8C04CA24`: 0 → `0xCA00`, `0x20` → `0xCA01`, `0x0A02` → `0xCA02`. Any other value
   stores nothing.
8. **OS startup** (`0xA00006CC`), called in a loop that never returns. It runs a few
   uncached init routines, flushes the TLB again, sets a first VBR (`0x80001454`), lowers SR
   to `0x500000F0` (register bank 0), runs the cached OS init (`0x8000495A`, `0x80002600`,
   `0x80009E04`) and finally calls the **OS main loop at `0x80003550`** forever.

!!! info "Read from the 3.60 code; run by the emulator"
    The boot code of OS 3.60 is byte-for-byte the same as in the 3.80 updater, at the same
    addresses (`re/boot_compare.py`). The emulator runs this exact path on every cold boot of
    the 3.60 flash.

!!! warning "Unconfirmed: what `0xFF000024` holds at reset"
    On the SH-4A, `0xFF000024` is **EXPEVT**, the register that records which exception or
    reset happened; 0 and `0x20` are the codes for a power-on and a manual reset. Early notes
    called it a "hardware strap". The OS 3.60 needs model code `0xCA02` later in the boot
    (otherwise the storage-memory check never finishes), so the emulator returns `0x0A02`
    there after reset. What a real calculator reads there at reset has not been measured.

## Power-on vs. cold start

When the calculator is switched **off**, its RAM keeps power. The OS uses that: the
power-off routine writes **1** to the IL RAM word `0xFD8017DC`. At the next boot:

- If `0xFD8017DC` = 1 and the model code (`0xFD8018D4`) is `0xCA02`, the OS skips the
  first-boot setup and goes straight to the MAIN MENU.
- Otherwise (the batteries were out, so RAM lost the 1) it runs the first-boot setup:
  language, then the rest of the wizard.

The word reads 2 while the setup itself is running, and the boot clears it once it has
been checked.

!!! info "Verified in the emulator"
    A cold boot of a full 32 MB flash dump runs the setup; seeding `0xFD8017DC` = 1 instead
    starts at that calculator's own MAIN MENU with its add-ins and settings
    (`CG50_WARM=1`, `emu_go/warm_boot_test.go`).
