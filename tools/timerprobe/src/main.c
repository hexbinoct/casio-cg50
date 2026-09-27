// timerprobe — find what drives the fx-CG50 OS timer tick, without assuming a block.
// Inside gint_world_switch (OS world) it samples every 32-bit word of the known timer
// register blocks + the RTC continuously for one RTC second and reports the words that
// changed (first/last value, change count), then diffs the whole ILRAM (0xFD800000, 64 KB)
// across one second to find RAM counters the OS ISRs bump. Results -> \\fls0\TIMERPRB.TXT.
#include <gint/display.h>
#include <gint/keyboard.h>
#include <gint/gint.h>
#include <gint/bfile.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <stdarg.h>

#define R8(a)   (*(volatile uint8_t  *)(a))
#define R32(a)  (*(volatile uint32_t *)(a))
#define RTC_RSECCNT 0xA413FEC2

typedef struct { uint32_t base, len; } blk_t;
static const blk_t BLK[] = {
    {0xA4490000, 0x40},   // TMU
    {0xA44A0000, 0x100},  // "ETMU" (elapsed flag +0x60 in the emulator model)
    {0xA44C0000, 0x40},   // timer strobed by 0x801e6d40 (+0 data, +0x20 ctrl)
    {0xA44D0000, 0x100},  // down-counter +0xD8; gint ETMU0 at +0x30
    {0xA44E0030, 0x10}, {0xA44F0030, 0x10}, {0xA4500030, 0x10}, {0xA4510030, 0x10}, {0xA4520030, 0x10}, // gint ETMU1-5
    {0xA413FEC0, 0x40},   // RTC
};
#define NBLK ((int)(sizeof BLK / sizeof BLK[0]))
#define NW   (0x40/4 + 0x100/4 + 0x40/4 + 0x100/4 + 5*4 + 0x40/4)

static uint32_t first[NW], last[NW], prev[NW];
static uint32_t changes[NW];
static uint32_t secs_done;

// ILRAM diff
#define ILRAM     0xFD800000
#define ILRAM_LEN 0x10000
static uint32_t il0[ILRAM_LEN/4];
static uint32_t il1[ILRAM_LEN/4];

static inline void wait_sec_edge(void){ uint8_t s = R8(RTC_RSECCNT); while(R8(RTC_RSECCNT) == s); }

static void probe(void){
    // ILRAM snapshot A
    for(int i = 0; i < ILRAM_LEN/4; i++) il0[i] = R32(ILRAM + 4*i);
    wait_sec_edge();
    int k = 0;
    for(int b = 0; b < NBLK; b++) for(uint32_t o = 0; o < BLK[b].len; o += 4){ uint32_t v = R32(BLK[b].base + o); first[k] = last[k] = prev[k] = v; changes[k] = 0; k++; }
    // sample continuously for 2 RTC seconds
    for(int s = 0; s < 2; s++){
        uint8_t sec = R8(RTC_RSECCNT);
        while(R8(RTC_RSECCNT) == sec){
            k = 0;
            for(int b = 0; b < NBLK; b++) for(uint32_t o = 0; o < BLK[b].len; o += 4){
                uint32_t v = R32(BLK[b].base + o);
                if(v != prev[k]){ changes[k]++; prev[k] = v; }
                last[k] = v; k++;
            }
        }
    }
    secs_done = 2;
    // ILRAM snapshot B (≈3 s after A)
    for(int i = 0; i < ILRAM_LEN/4; i++) il1[i] = R32(ILRAM + 4*i);
}

static char out[6144]; static int olen;
static void emit(const char *s){ int n = strlen(s); if(olen + n < (int)sizeof out){ memcpy(out + olen, s, n); olen += n; } }
static void emitf(const char *fmt, ...){ char b[160]; va_list ap; va_start(ap, fmt); vsnprintf(b, sizeof b, fmt, ap); va_end(ap); emit(b); }

static int save_file(void){
    uint16_t const *path = u"\\\\fls0\\TIMERPRB.TXT";
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

int main(void){
    dclear(C_WHITE); dtext(4, 20, C_BLACK, "timerprobe: sampling ~4 s, don't touch keys"); dupdate();
    gint_world_switch(GINT_CALL(probe));

    olen = 0;
    emit("== TIMERPROBE ==\n");
    emitf("window=%lus  (peripheral words that changed: addr first last changes)\n", (unsigned long)secs_done);
    int k = 0;
    for(int b = 0; b < NBLK; b++) for(uint32_t o = 0; o < BLK[b].len; o += 4){
        if(changes[k]) emitf("P %08lx %08lx %08lx %lu\n", (unsigned long)(BLK[b].base + o), (unsigned long)first[k], (unsigned long)last[k], (unsigned long)changes[k]);
        k++;
    }
    emit("static regs of interest:\n");
    k = 0;
    for(int b = 0; b < NBLK; b++){ emitf("B %08lx:", (unsigned long)BLK[b].base);
        for(uint32_t o = 0; o < BLK[b].len && o < 0x40; o += 4){ emitf(" %08lx", (unsigned long)first[k + o/4]); }
        emit("\n"); k += BLK[b].len/4; }
    emit("ILRAM words that changed over ~3 s (addr A B delta):\n");
    int n = 0;
    for(int i = 0; i < ILRAM_LEN/4 && n < 60; i++) if(il0[i] != il1[i]){
        emitf("M %08lx %08lx %08lx %ld\n", (unsigned long)(ILRAM + 4*i), (unsigned long)il0[i], (unsigned long)il1[i], (long)(il1[i] - il0[i])); n++; }
    emitf("(%d ILRAM words changed)\n", n);
    int rc = save_file();
    emitf("saved TIMERPRB.TXT rc=%d\n", rc);

    int line = 0, y = 1; dclear(C_WHITE);
    for(int i = 0; i < olen;){
        int j = i; while(j < olen && out[j] != '\n') j++;
        char tmp[160]; int m = j - i; if(m > 159) m = 159; memcpy(tmp, out + i, m); tmp[m] = 0;
        dtext(1, y, C_BLACK, tmp); y += 14; line++; i = j + 1;
        if(line >= 14 || i >= olen){ dupdate(); getkey(); dclear(C_WHITE); y = 1; line = 0; }
    }
    return 1;
}
