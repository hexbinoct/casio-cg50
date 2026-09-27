// tickprobe — what is the OS's "0x560" interrupt, and how often does it really fire?
//
// The emulator models INTEVT 0x560 as a fixed tick every 30 000 instructions (a boot-era guess).
// RE (RECON_NOTES cont.18r): its source is the unit at 0xA4610000, armed by the OS idle routine
// 0x802ae87a (+0x8A &= ~0x2000; +0x8C = 0; +0x88 = 0x0042; +0x8C = 0x8000; +0x8A = 0x01CF) just
// before it sleeps; the ISR 0x801ded94 clears flag bits 14/15 of +0x88/+0x8A.
//
// v1: bad clock (the 32 kHz counter doesn't run in gint's world).
// v2 (gint world, 1 ms timer): never completes within 10 s; +0x84 held an A/D result (0x8D80).
// v3 (OS world): still never completes within 2 s of the start — with the CPU running.
// => looks like the battery ADC converting only while the CPU sleeps.
// v4 tests exactly that, in the OS world, replaying the OS idle routine 0x802ae742:
//     RCR2 = (RCR2 & 0x0F) | 0x50 (RTC periodic alarm 1/2 s); SR.BL = 1, IMASK = 0;
//     *(u32 *)0xA4150020 = 0; sleep; — then, still with BL = 1 (no ISR has run), snapshot
// the unit and the RTC flag, time the sleep with the 32.768 kHz counter, and restore SR/RCR2
// (the OS ISRs then run and clean up). 4 trials with a conversion started, 2 controls without.
// Results -> \\fls0\TICKPRB.TXT.
//
// !! v4 RESET THE CALCULATOR (2026-09-27): running the sleep test on the real fx-CG50 rebooted it
// into first-boot setup (storage intact, RAM state lost). The OS does not always sleep this way:
// its idle routine first calls 0x801df084 and may take a different path (0xa0020926) that also
// prepares RAM/standby; writing CPG 0xA4150020 = 0 and sleeping from the world-switch context is
// not safe. The sleep test is compiled out unless TICKPROBE_SLEEP_TEST is defined; by default
// this build only reports the OS-world register state.
#include <gint/display.h>
#include <gint/keyboard.h>
#include <gint/gint.h>
#include <gint/bfile.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <stdarg.h>

#define R8(a)    (*(volatile uint8_t  *)(a))
#define R16(a)   (*(volatile uint16_t *)(a))
#define R32(a)   (*(volatile uint32_t *)(a))

#define U        0xA4610000
#define CNT32K   0xA44D00D8   // 32.768 kHz down-counter (runs in the OS world)
#define RCR2     0xA413FEDE   // RTC control 2: PEF bit7, PES bits 6..4
#define CPG20    0xA4150020   // written 0 by the OS right before `sleep`
#define RSECCNT  0xA413FEC2
#define FLAGS    0xC000
#define N        6            // trials 0..3 with a conversion started, 4..5 controls

static inline uint32_t get_sr(void){ uint32_t s; __asm__ volatile("stc sr, %0" : "=r"(s)); return s; }
static inline void set_sr(uint32_t s){ __asm__ volatile("ldc %0, sr" :: "r"(s)); }
static inline void cpu_sleep(void){ __asm__ volatile("sleep"); }

static char out[4096]; static int olen;
static void emit(const char *s){ int n = strlen(s); if(olen + n < (int)sizeof out){ memcpy(out + olen, s, n); olen += n; } }
static void emitf(const char *fmt, ...){ char b[192]; va_list ap; va_start(ap, fmt); vsnprintf(b, sizeof b, fmt, ap); va_end(ap); emit(b); }

static uint32_t ref_counts;
static uint16_t os_regs[16], end_regs[16];
static uint32_t slept[N];
static uint16_t w84[N], w88[N], w8a[N], w8c[N];
static uint8_t  wrcr2[N];

static void snap(uint16_t *d){ for(int i = 0; i < 16; i++) d[i] = R16(U + 0x80 + 2*i); }

static void arm_like_os(void){
    R16(U + 0x8A) = R16(U + 0x8A) & 0xDFFF;
    R16(U + 0x8C) = 0;
    R16(U + 0x88) = 0x0042;
    R16(U + 0x8C) = 0x8000;
    R16(U + 0x8A) = 0x01CF;
}

static void clear_like_isr(void){
    R16(U + 0x8A) = R16(U + 0x8A) & 0xBFFF;
    R16(U + 0x88) = R16(U + 0x88) & 0xBFFF;
    R16(U + 0x8A) = R16(U + 0x8A) & 0x7FFF;
    R16(U + 0x88) = R16(U + 0x88) & 0x7FFF;
}

static void measure(void){
#ifndef TICKPROBE_SLEEP_TEST
    snap(os_regs);
    snap(end_regs);
    return;
#endif
    uint8_t s = R8(RSECCNT); while(R8(RSECCNT) == s);
    uint32_t c0 = R32(CNT32K);
    s = R8(RSECCNT); while(R8(RSECCNT) == s);
    ref_counts = c0 - R32(CNT32K);
    snap(os_regs);

    for(int i = 0; i < N; i++){
        clear_like_isr();
        if(i < 4) arm_like_os();                         // trials 4, 5: control (no conversion)
        uint8_t rcr2 = R8(RCR2);
        R8(RCR2) = (rcr2 & 0x0F) | 0x50;                 // RTC periodic 1/2 s, as the OS idle routine
        uint32_t sr = get_sr();
        set_sr((sr & 0xFFFFFF0F) | 0x10000000);          // BL = 1, IMASK = 0 (wake, but no ISR yet)
        R32(CPG20) = 0;
        uint32_t t0 = R32(CNT32K);
        cpu_sleep();
        slept[i] = t0 - R32(CNT32K);
        w84[i] = R16(U + 0x84); w88[i] = R16(U + 0x88); w8a[i] = R16(U + 0x8A); w8c[i] = R16(U + 0x8C);
        wrcr2[i] = R8(RCR2);
        set_sr(sr);                                      // OS ISRs run now and clean up
        R8(RCR2) = R8(RCR2) & 0x0F;                      // as the OS does after waking
        uint32_t t1 = R32(CNT32K); while(t1 - R32(CNT32K) < 3277) ;   // ~100 ms between trials
    }
    clear_like_isr();
    snap(end_regs);
}

static int save_file(void){
    uint16_t const *path = u"\\\\fls0\\TICKPRB.TXT";
    BFile_Remove(path);
    if(olen & 1) out[olen++] = '\n';
    int size = olen;
    int rc = BFile_Create(path, BFile_File, &size);
    if(rc < 0) return rc;
    int fd = BFile_Open(path, BFile_WriteOnly);
    if(fd < 0) return fd;
    rc = BFile_Write(fd, out, olen);
    BFile_Close(fd);
    return rc;
}

static void dump(const char *tag, const uint16_t *r){
    emitf("%s:", tag);
    for(int i = 0; i < 16; i++) emitf(" %02x=%04x", 0x80 + 2*i, r[i]);
    emit("\n");
}

int main(void){
    dclear(C_WHITE);
    dtext(4, 20, C_BLACK, "tickprobe v4: sleep test in the OS world (~5 s),");
    dtext(4, 36, C_BLACK, "don't touch keys");
    dupdate();
    gint_world_switch(GINT_CALL(measure));

    olen = 0;
    emit("== TICKPROBE v4 (sleep test, OS world) ==\n");
    emitf("ref: 32k counts per RTC second = %lu (expect ~32768)\n", (unsigned long)ref_counts);
    dump("os-state", os_regs);
    emit("per trial: sleep length; unit + RCR2 at wake-up, before any ISR ran\n");
    emit("(RCR2 bit7 = RTC periodic flag; 88/8A bits 14-15 = unit flags; 8C bit15 = start)\n");
    for(int i = 0; i < N; i++){
        emitf("%s %d: slept %lu counts = %lu ms  84=%04x 88=%04x 8A=%04x 8C=%04x RCR2=%02x\n",
              i < 4 ? "conv" : "ctrl", i, (unsigned long)slept[i], (unsigned long)(slept[i] * 1000 / 32768),
              w84[i], w88[i], w8a[i], w8c[i], wrcr2[i]);
    }
    dump("end", end_regs);

    int rc = save_file();
    emitf("saved TICKPRB.TXT rc=%d\n", rc);

    int line = 0, y = 1; dclear(C_WHITE);
    for(int i = 0; i < olen;){
        int j = i; while(j < olen && out[j] != '\n') j++;
        char tmp[192]; int m = j - i; if(m > 191) m = 191; memcpy(tmp, out + i, m); tmp[m] = 0;
        dtext(1, y, C_BLACK, tmp); y += 14; line++; i = j + 1;
        if(line >= 14 || i >= olen){ dupdate(); getkey(); dclear(C_WHITE); y = 1; line = 0; }
    }
    return 1;
}
