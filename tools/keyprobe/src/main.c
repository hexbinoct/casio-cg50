// keyprobe — measure the fx-CG50 OS timer tick, the KEYSC key-scan rate and Pϕ.
// See re/keyprobe_note.md for the rationale. Results go to \\fls0\KEYPROBE.TXT on the
// calculator's storage memory (read it over USB mass storage), and are paged on screen.
#include <gint/display.h>
#include <gint/keyboard.h>
#include <gint/gint.h>        // gint_world_switch, GINT_CALL
#include <gint/bfile.h>
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

typedef struct {
    uint32_t frqcr, tstr, tcor[3], tcnt[3], tcr[3];
    uint16_t keysc[16];
    uint16_t iprf; uint8_t imr5;
    uint32_t ticks0, ticks1, tcnt2_0, tcnt2_1, secs;
    uint32_t scans, detects, other;
    uint32_t first_scan_r64;
    uint16_t data_first[6];
    uint16_t st_before, st_after;
    uint32_t idle_scans, idle_detects, idle_secs, idle_ticks;
    uint32_t nticks_probe, tcnt2_probe;
    uint32_t r64_0, r64_1;
} res_t;
static res_t R;

static inline uint32_t wait_sec_edge(void){
    uint8_t s = R8(RTC_RSECCNT);
    while(R8(RTC_RSECCNT) == s) ;
    return R8(RTC_RSECCNT);
}

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

static void probe_held(void){                        // OS world, RIGHT is held
    R.frqcr = R32(CPG_FRQCR); R.tstr = R8(TMU_TSTR);
    for(int n = 0; n < 3; n++){ R.tcor[n] = R32(TMU_TCOR(n)); R.tcnt[n] = R32(TMU_TCNT(n)); R.tcr[n] = R16(TMU_TCR(n)); }
    for(int i = 0; i < 16; i++) R.keysc[i] = R16(KEYSC + 2*i);
    R.iprf = R16(INTC_IPRF); R.imr5 = R8(INTC_IMR5);

    // sanity: OS ticks + TMU2 over one RTC second with everything untouched
    wait_sec_edge(); uint32_t t = R32(OS_TICKS), c = R32(TMU_TCNT(2)); R.r64_0 = R8(RTC_R64CNT);
    wait_sec_edge(); R.nticks_probe = R32(OS_TICKS) - t; R.tcnt2_probe = c - R32(TMU_TCNT(2)); R.r64_1 = R8(RTC_R64CNT);

    R.st_before = R16(KEYSC + 0x14);
    R8(INTC_IMR5) = 0x80;                            // mask KEYSC IRQ (OS ISR off)
    wait_sec_edge();
    R.ticks0 = R32(OS_TICKS); R.tcnt2_0 = R32(TMU_TCNT(2));
    R.secs = 4;
    uint32_t td, cd;
    count_window(R.secs, &R.scans, &R.detects, &R.other, &td, &cd, 1);
    R.ticks1 = R.ticks0 + td; R.tcnt2_1 = cd;
    R.st_after = R16(KEYSC + 0x14);
    R8(INTC_IMCR5) = 0x80;                           // unmask
}

static void probe_idle(void){                        // OS world, nothing held
    R8(INTC_IMR5) = 0x80;
    wait_sec_edge();
    uint32_t dummy;
    R.idle_secs = 2;
    count_window(R.idle_secs, &R.idle_scans, &R.idle_detects, &dummy, &R.idle_ticks, &dummy, 0);
    R8(INTC_IMCR5) = 0x80;
}

static char out[4096]; static int olen;
static void emit(const char *s){ int n = strlen(s); if(olen + n < (int)sizeof out){ memcpy(out + olen, s, n); olen += n; } }
static void emitf(const char *fmt, ...){ char b[160]; va_list ap; va_start(ap, fmt); vsnprintf(b, sizeof b, fmt, ap); va_end(ap); emit(b); }

static void say(const char *l1, const char *l2){ dclear(C_WHITE); dtext(4, 20, C_BLACK, l1); dtext(4, 40, C_BLACK, l2); dupdate(); }

static int save_file(void){
    uint16_t const *path = u"\\\\fls0\\KEYPROBE.TXT";
    BFile_Remove(path);
    if(olen & 1) out[olen++] = '\n';           // BFile_Write needs an even size
    int size = olen;
    int rc = BFile_Create(path, BFile_File, &size);
    if(rc < 0) return rc;
    int fd = BFile_Open(path, BFile_WriteOnly);
    if(fd < 0) return fd;
    rc = BFile_Write(fd, out, olen);
    BFile_Close(fd);
    return rc;
}

int main(void){
    say("keyprobe: HOLD RIGHT now", "keep it held until this changes (~7 s)");
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
    emitf("1s unmasked: ticks=%lu tcnt2=%lu r64 %02lx->%02lx\n", (unsigned long)R.nticks_probe, (unsigned long)R.tcnt2_probe, (unsigned long)R.r64_0, (unsigned long)R.r64_1);
    emitf("HELD %lus: scans=%lu detects=%lu other=%lu ticks=%lu tcnt2=%lu\n",
          (unsigned long)R.secs, (unsigned long)R.scans, (unsigned long)R.detects, (unsigned long)R.other,
          (unsigned long)(R.ticks1 - R.ticks0), (unsigned long)R.tcnt2_1);
    emitf("first scan R64=%02lx data:", (unsigned long)R.first_scan_r64); for(int i = 0; i < 6; i++) emitf(" %04x", R.data_first[i]); emit("\n");
    emitf("IDLE %lus: scans=%lu detects=%lu ticks=%lu\n", (unsigned long)R.idle_secs,
          (unsigned long)R.idle_scans, (unsigned long)R.idle_detects, (unsigned long)R.idle_ticks);

    int rc = save_file();
    emitf("saved KEYPROBE.TXT rc=%d\n", rc);

    int line = 0, y = 1; dclear(C_WHITE);
    for(int i = 0; i < olen;){
        int j = i; while(j < olen && out[j] != '\n') j++;
        char tmp[160]; int n = j - i; if(n > 159) n = 159; memcpy(tmp, out + i, n); tmp[n] = 0;
        dtext(1, y, C_BLACK, tmp); y += 14; line++; i = j + 1;
        if(line >= 14 || i >= olen){ dupdate(); getkey(); dclear(C_WHITE); y = 1; line = 0; }
    }
    return 1;
}
