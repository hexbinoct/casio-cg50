# keyprobe on the real fx-CG50 (OS 3.60) — 2026-09-27, built on Windows via Docker fxSDK

Raw capture: `keyprobe_2026-09-27.txt` (written by the add-in to `\\fls0\KEYPROBE.TXT`, copied over
USB mass storage). Add-in: `tools/keyprobe/src/main.c`. Measurements run inside
`gint_world_switch` (OS world), KEYSC IRQ masked at INTC IMR5 during the counting windows.

## KEYSC (0xA44B0000) — the emulator model is CONFIRMED
- Live register snapshot with RIGHT held: `data 0000 0000 0000 0200 0000 0000` → word3 bit9, exactly
  the modelled layout (word = col>>1, bit = row + 8*(col&1); RIGHT = row1,col7).
- `+0x0C=8000 +0x0E=8042 +0x10=0200 +0x12=0002 +0x14=7600 +0x16=0000 +0x18=00c8 +0x1A=0fff
  +0x1C=00ff +0x1E=0002` — the OS's normal-mode config as RE'd; +0x14 IE=0x76 with a key held
  (ISR in its "pressed" state), flags 0 between scans. `+0x12`/`+0x1E` read 2 (model returns 0 for
  +0x12; the OS only tests bit0, so no change needed).
- **Scan rate while held: 132 scan-complete flags in 4 s = 30.3 ms per scan (33 Hz).** No "other"
  flag bits ever set. `detects=0` because RIGHT was already held before the window (no new edge).
- **Idle: 0 scans, 0 detects in 2 s** — the unit does NOT scan continuously; it scans only after a
  key-detect while keys are held. Matches the model (held → scan flags; idle → quiet).
- OS key repeat therefore = first repeat after 20 scans ≈ **0.6 s**, then **33 repeats/s**.
- INTC: IPRF=0xD000 (KEYSC priority 13, as RE'd), IMR5=0x77 (bit7 KEYSC unmasked).

## Timer — NOT measured (needs a second probe)
- TMU: TSTR=0, TMU0/2 idle, TMU1 TCOR=TCNT=0x2D000 TCR=0x23 but not started → the OS tick is not
  TMU-driven (the emulator's "timer INTEVT 0x560" is fed by our own 30,000-instr cadence, and the
  boot-time TMU1 program never runs). The OS tick must come from an ETMU (0xA44A/0xA44C/0xA44D).
- The tick counter candidate 0xFD8017D0 did not move over 1 s (either wrong address, or the OS
  timer ISR does not run inside gint's world switch). R64CNT read 0 (RSECCNT edges worked).
- Next probe: dump ETMU blocks (0xA44A0000, 0xA44C0000, 0xA44D0000: TCOR/TCNT/TCR/TSTR per
  channel) and count each channel's underflow flag per RTC second; ETMU runs from RCLK 32.768 kHz,
  so TCOR alone gives the rate once the active channel is known.

## Applied to the emulator
- `DefaultKeyScanPeriod` 500k → **750k instr** (= 30 ms at ~25M ips); `KeyScanHz = 33`.
- Android: `setKeyScanPeriod(instrPerFrame*60/33)`.
- `emu/mmio.py` `DEFAULT_SCAN_PERIOD` mirrored.
