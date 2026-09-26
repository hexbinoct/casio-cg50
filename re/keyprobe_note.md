# fx-CG50 — `keyprobe` add-in: measure the OS timer tick, the KEYSC scan rate, and Pϕ (2026-09-26)

Read this on the Mac (gint/fxsdk ready, same setup as `aluprobe`). Goal: three numbers the ROM
cannot give us, so the emulator's instruction-based clock can be anchored to real time:

1. **OS tick rate** — how often the OS timer ISR (INTEVT 0x560) fires. Measured two ways: the OS
   tick counter at `0xFD8017D0` (the keyboard ISR reads it as a timestamp) per RTC second, and TMU
   channel 1's TCOR/TCR (which need Pϕ, see 3).
2. **KEYSC scan rate** — how often the key-scan unit at `0xA44B0000` reports "scan complete"
   (status +0x14 bit1) while a key is held, and whether it keeps scanning when nothing is held.
   The OS repeats a held key after 20 scans and then once per scan, so this IS the key-repeat clock.
3. **Pϕ** — the peripheral clock, from TMU channel 2's free-running counter per RTC second.

Everything is read in the **OS world** (`gint_world_switch`), because gint reprograms the TMUs and
the keyboard unit for itself; inside the switch the OS's own configuration and ISRs are live.
Raw volatile pointers only inside the measurement; no OS/gint calls until we are back.

## How to run
- Build like aluprobe (`fxsdk build-cg`, add-in, `-lgint-cg`). `gint_world_switch` +
  `GINT_CALL` are in `<gint/gint.h>` (gint ≥ 2.8). If your gint lacks it, tell me — there is a
  fallback plan (a hook via `gint_setrestart`/`__osmenu_return`), but every recent gint has it.
- For the USB output, paste in the **named-transfer block from `aluprobe/src/main.c`** (the one
  that worked with `fxlink -i -v -w`; the interactive text path overran libusb). The `SEND()`
  macro below marks the spot.
- Launch from the MENU. The screen will tell you when to **hold RIGHT** (keep it held until the
  screen changes, ~5 s) and when to release. Then it sends the text and also pages it on screen.
- Send me the captured text via `noted` (`python ~/.claude/bin/noted.py push "keyprobe" -f <file>`).

## Source (`keyprobe/src/main.c`)

```c
#include <gint/display.h>
#include <gint/keyboard.h>
#include <gint/gint.h>        // gint_world_switch, GINT_CALL
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <stdarg.h>

#define R8(a)   (*(volatile uint8_t  *)(a))
#define R16(a)  (*(volatile uint16_t *)(a))
#define R32(a)  (*(volatile uint32_t *)(a))

// --- SH7305 blocks (OS view) ---
#define CPG_FRQCR   0xA4150000
#define TMU_TSTR    0xA4490004
#define TMU_TCOR(n) (0xA4490008 + 12*(n))
#define TMU_TCNT(n) (0xA449000C + 12*(n))
#define TMU_TCR(n)  (0xA4490010 + 12*(n))
#define KEYSC       0xA44B0000            // +0x00..0x0B data, +0x0C ctrl, +0x10 mode, +0x14 IE<<8|flags (W1C)
#define INTC_IPRF   0xA4080014
#define INTC_IMR5   0xA4080094            // write 0x80 = mask KEYSC IRQ (as the OS ISR does)
#define INTC_IMCR5  0xA40800D4            // write 0x80 = unmask
#define RTC_R64CNT  0xA413FEC0
#define RTC_RSECCNT 0xA413FEC2            // BCD seconds
#define OS_TICKS    0xFD8017D0            // OS tick counter (ILRAM), read by the keyboard ISR

// results filled inside the OS world, printed afterwards
typedef struct {
    uint32_t frqcr, tstr, tcor[3], tcnt[3], tcr[3];
    uint16_t keysc[16];                   // +0x00..+0x1E before we touch anything
    uint16_t iprf; uint8_t imr5;
    uint32_t ticks0, ticks1;              // OS tick counter at window start/end (held-key window)
    uint32_t tcnt2_0, tcnt2_1;            // TMU2 free counter at window start/end
    uint32_t secs;                        // RTC seconds elapsed in the window (target 4)
    uint32_t scans, detects, other;       // KEYSC flag events seen (bit1 / bit3 / any other bit)
    uint32_t first_scan_r64;              // R64CNT when the first scan-complete was seen
    uint16_t data_first[6];               // key words at the first scan-complete
    uint16_t st_before, st_after;         // +0x14 before/after masking
    uint32_t idle_scans, idle_detects, idle_secs, idle_ticks;   // no-key window (2 s)
    uint32_t nticks_probe;                // OS ticks during a 1 s idle wait BEFORE masking (sanity)
} res_t;
static res_t R;

static inline uint32_t wait_sec_edge(void){       // spin until RSECCNT changes
    uint8_t s = R8(RTC_RSECCNT);
    while(R8(RTC_RSECCNT) == s) ;
    return R8(RTC_RSECCNT);
}

// Count KEYSC flag events for `secs` RTC seconds with the KEYSC IRQ masked so the OS ISR
// cannot clear the flags first. We clear them ourselves (write back the flags we read, W1C,
// keeping the IE byte).
static void count_window(uint32_t secs, uint32_t *scans, uint32_t *detects, uint32_t *other,
                         uint32_t *ticks_delta, uint32_t *tcnt2_delta, int record_first){
    uint32_t t0 = R32(OS_TICKS), c0 = R32(TMU_TCNT(2));
    uint32_t n_scan = 0, n_det = 0, n_oth = 0;
    int first = 1;
    for(uint32_t s = 0; s < secs; s++){
        uint8_t sec = R8(RTC_RSECCNT);
        while(R8(RTC_RSECCNT) == sec){
            uint16_t st = R16(KEYSC + 0x14);
            uint16_t fl = st & 0xFF;
            if(fl){
                if(fl & 0x02){ n_scan++;
                    if(record_first && first){ first = 0; R.first_scan_r64 = R8(RTC_R64CNT);
                        for(int i = 0; i < 6; i++) R.data_first[i] = R16(KEYSC + 2*i); } }
                if(fl & 0x08) n_det++;
                if(fl & ~0x0A) n_oth++;
                R16(KEYSC + 0x14) = st;            // W1C the flags we saw, IE unchanged
            }
        }
    }
    *scans = n_scan; *detects = n_det; *other = n_oth;
    *ticks_delta = R32(OS_TICKS) - t0;
    *tcnt2_delta = c0 - R32(TMU_TCNT(2));           // TMU counts down
}

static void probe_held(void){                        // runs in the OS world, RIGHT is held
    R.frqcr = R32(CPG_FRQCR); R.tstr = R8(TMU_TSTR);
    for(int n = 0; n < 3; n++){ R.tcor[n] = R32(TMU_TCOR(n)); R.tcnt[n] = R32(TMU_TCNT(n)); R.tcr[n] = R16(TMU_TCR(n)); }
    for(int i = 0; i < 16; i++) R.keysc[i] = R16(KEYSC + 2*i);
    R.iprf = R16(INTC_IPRF); R.imr5 = R8(INTC_IMR5);

    // sanity: OS ticks over one RTC second with everything untouched
    wait_sec_edge(); uint32_t t = R32(OS_TICKS); wait_sec_edge(); R.nticks_probe = R32(OS_TICKS) - t;

    R.st_before = R16(KEYSC + 0x14);
    R8(INTC_IMR5) = 0x80;                            // mask KEYSC IRQ (OS ISR off)
    wait_sec_edge();
    R.ticks0 = R32(OS_TICKS); R.tcnt2_0 = R32(TMU_TCNT(2));
    R.secs = 4;
    count_window(R.secs, &R.scans, &R.detects, &R.other, &R.ticks1, &R.tcnt2_1, 1);
    R.ticks1 += R.ticks0; R.tcnt2_1 = R.tcnt2_0 - R.tcnt2_1;
    R.st_after = R16(KEYSC + 0x14);
    R8(INTC_IMCR5) = 0x80;                           // unmask
}

static void probe_idle(void){                        // runs in the OS world, nothing held
    R8(INTC_IMR5) = 0x80;
    wait_sec_edge();
    uint32_t dummy;
    R.idle_secs = 2;
    count_window(R.idle_secs, &R.idle_scans, &R.idle_detects, &dummy, &R.idle_ticks, &dummy, 0);
    R8(INTC_IMCR5) = 0x80;
}

static char out[6144]; static int olen;
static void emit(const char *s){ int n = strlen(s); if(olen + n < (int)sizeof out){ memcpy(out + olen, s, n); olen += n; } }
static void emitf(const char *fmt, ...){ char b[160]; va_list ap; va_start(ap, fmt); vsnprintf(b, sizeof b, fmt, ap); va_end(ap); emit(b); }

static void say(const char *l1, const char *l2){ dclear(C_WHITE); dtext(4, 20, C_BLACK, l1); dtext(4, 40, C_BLACK, l2); dupdate(); }

int main(void){
    say("keyprobe: HOLD RIGHT now", "keep it held until this changes (~6 s)");
    while(1){ clearevents(); if(keydown(KEY_RIGHT)) break; }
    gint_world_switch(GINT_CALL(probe_held));

    say("RELEASE all keys", "measuring idle (~3 s)");
    while(1){ clearevents(); if(!keydown(KEY_RIGHT)) break; }
    gint_world_switch(GINT_CALL(probe_idle));

    olen = 0;
    emit("== KEYPROBE ==\n");
    emitf("FRQCR=%08lx TSTR=%02lx\n", (unsigned long)R.frqcr, (unsigned long)R.tstr);
    for(int n = 0; n < 3; n++) emitf("TMU%d TCOR=%08lx TCNT=%08lx TCR=%04lx\n", n, (unsigned long)R.tcor[n], (unsigned long)R.tcnt[n], (unsigned long)R.tcr[n]);
    emit("KEYSC +00..+1E:"); for(int i = 0; i < 16; i++) emitf(" %04x", R.keysc[i]); emit("\n");
    emitf("IPRF=%04x IMR5=%02x st_before=%04x st_after=%04x\n", R.iprf, R.imr5, R.st_before, R.st_after);
    emitf("OS ticks in 1 s (unmasked): %lu\n", (unsigned long)R.nticks_probe);
    emitf("HELD window %lu s: scans=%lu detects=%lu other=%lu ticks=%lu tcnt2=%lu\n",
          (unsigned long)R.secs, (unsigned long)R.scans, (unsigned long)R.detects, (unsigned long)R.other,
          (unsigned long)(R.ticks1 - R.ticks0), (unsigned long)R.tcnt2_1);
    emitf("first scan R64=%02lx data:", (unsigned long)R.first_scan_r64); for(int i = 0; i < 6; i++) emitf(" %04x", R.data_first[i]); emit("\n");
    emitf("IDLE window %lu s: scans=%lu detects=%lu ticks=%lu\n", (unsigned long)R.idle_secs,
          (unsigned long)R.idle_scans, (unsigned long)R.idle_detects, (unsigned long)R.idle_ticks);

    // SEND(): paste the named fxlink transfer block from aluprobe/src/main.c here, sending `out`
    // (name it "keyprobe" so fxlink -i -v -w saves fxlink-keyprobe-*.bin).

    // on-screen fallback, EXE pages
    int line = 0, y = 1; dclear(C_WHITE);
    for(int i = 0; i < olen;){
        int j = i; while(j < olen && out[j] != '\n') j++;
        char tmp[160]; int n = j - i; if(n > 159) n = 159; memcpy(tmp, out + i, n); tmp[n] = 0;
        dtext(1, y, C_BLACK, tmp); y += 14; line++; i = j + 1;
        if(line >= 14 || i >= olen){ dupdate(); getkey(); dclear(C_WHITE); y = 1; line = 0; }
    }
    return 1;
}
```

## What I compute from it
- `tcnt2` per 4 s with TCR2's prescaler → **Pϕ** (boot dump: TCR2=3 → Pϕ/256).
- **OS tick Hz** = ticks/4 (cross-check: Pϕ / prescaler(TCR1) / (TCOR1+1)). The emulator fires it
  every 30,000 instructions, so this sets how many instructions per tick a host must run, or
  motivates the wall-clock-anchored timer.
- **KEYSC scan period** = 4 s / scans while held → sets `DefaultKeyScanPeriod` / the Android
  divisor exactly; idle-window scans tell whether the unit scans continuously or only after a
  key-detect (my model: only while held, +2 scans after release; `detects` should be 1 or 0).
- `data_first` must show word3 bit9 (0x0200) for RIGHT — a direct check of the matrix layout.

## If something misbehaves
- Calculator hangs inside the switch: the IMR5/IMCR5 addresses would be wrong for this chip
  (they are SH7724-style; the OS's own ISR uses 0xA4080094 = 0x80 and IPRF, so they should be
  right). Remove the mask/unmask lines; the counts then only see flags the OS ISR missed, still
  useful as a lower bound.
- `gint_world_switch` missing: say so, I'll write the fallback.
- RSECCNT never changes: RTC stopped in the OS world (unlikely); swap the loops to use R64CNT
  bit 7 edges (64 Hz → ~1 s per 64 toggles).
