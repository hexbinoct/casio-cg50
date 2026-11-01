/* dasmref: prints what DASM's C decoder and analysis (github.com/hexbinoct/cg50-dasm, src/)
 * make of a word table or an image, in a line format that the Go tests compare with the Go
 * port (dasm_compare_test.go builds it when DASM_SRC points at DASM's src/ directory):
 *
 *   cc -O2 -I$DASM_SRC dasmref.c $DASM_SRC/sh4dec.c $DASM_SRC/analysis.c -o dasmref
 *
 *   dasmref words <pc>                    every 16-bit word at pc, over synthetic memory
 *   dasmref dsp                           every 32-bit DSP form: one hash per first word
 *   dasmref image <file> <base> <start> <end>    every even address of an image
 *   dasmref funcs <file> <base> <entry> <rom>    the function table (rom: 1 = OS image)
 *   dasmref xrefs <file> <base> <entry> <rom> <target>...   cross-references to targets
 *
 * Instruction line: "addr word len kind delay reg target lit_addr lit_size lit_ok lit_val|text"
 * (hex fields; kind is DASM's enum sh4_kind). */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "sh4dec.h"
#include "analysis.h"
#include "names.h"

/* ---- synthetic memory for the word table: a hash of the address; some words unreadable ---- */
static uint8_t syn8(uint32_t a) { return (uint8_t)((a * 0x9E3779B1u) >> 24); }
static int syn_ok(uint32_t a) { return (a >> 1) % 7 != 3; }
static uint16_t g_second;          /* dsp mode: the second word at pc + 2 */
static uint32_t g_pc;
static int syn_rd(void *ctx, uint32_t a, int size, uint32_t *out) {
    (void)ctx;
    uint32_t v = 0;
    for (int i = 0; i < size; i += 2) if (!syn_ok(a + (uint32_t)i)) return 0;
    for (int i = 0; i < size; i++) v = (v << 8) | syn8(a + (uint32_t)i);
    *out = v;
    return 1;
}
static int dsp_rd(void *ctx, uint32_t a, int size, uint32_t *out) {
    if (a == g_pc + 2 && size == 2) { *out = g_second; return 1; }
    return syn_rd(ctx, a, size, out);
}

/* ---- an image file ---- */
static uint8_t *img; static size_t img_len; static uint32_t img_base;
static int img_rd(void *ctx, uint32_t a, int size, uint32_t *out) {
    (void)ctx;
    uint32_t off = a - img_base;
    if (a < img_base || off + (uint32_t)size > img_len) return 0;
    uint32_t v = 0;
    for (int i = 0; i < size; i++) v = (v << 8) | img[off + i];
    *out = v;
    return 1;
}
static void load(const char *path) {
    FILE *f = fopen(path, "rb");
    if (!f) { perror(path); exit(1); }
    fseek(f, 0, SEEK_END); img_len = (size_t)ftell(f); fseek(f, 0, SEEK_SET);
    img = malloc(img_len);
    if (!img || fread(img, 1, img_len, f) != img_len) { fprintf(stderr, "read error\n"); exit(1); }
    fclose(f);
}

static void line(uint32_t pc, uint16_t op, sh4_read_fn rd) {
    char text[96];
    sh4_insn_t in;
    sh4_decode(op, pc, rd, 0, &in, text, sizeof text);
    printf("%08x %04x %d %d %d %d %08x %08x %d %d %08x|%s\n", pc, op, in.len, in.kind, in.delay,
           in.reg, in.target, in.lit_addr, in.lit_size, in.lit_ok, in.lit_val, text);
}

/* ---- the analysis needs a source and the syscall table base ---- */
static src_t src;
static uint32_t table_base;
const uint8_t *src_page(src_t *s, uint32_t addr) {
    if (!src_contains(s, addr)) return 0;
    return s->rom + ((addr - s->base) & ~(SRC_PAGE_SIZE - 1));
}
uint32_t names_table_base(void) { return table_base; }

static void open_src(char **argv) {
    load(argv[0]);
    img_base = (uint32_t)strtoul(argv[1], 0, 0);
    memset(&src, 0, sizeof src);
    src.rom = img; src.base = img_base; src.size = (uint32_t)img_len;
    src.entry = (uint32_t)strtoul(argv[2], 0, 0);
    src.is_rom = atoi(argv[3]);
    table_base = 0;
    if (src.is_rom) {   /* as names_init: the trampoline's mov.l @(disp,pc),r2 = the table */
        for (uint32_t a = SYSCALL_TRAMPOLINE; a < SYSCALL_TRAMPOLINE + 0x10; a += 2) {
            uint32_t op, t;
            if (!img_rd(0, a, 2, &op)) break;
            if ((op & 0xFF00) == 0xD200) {
                if (img_rd(0, (a & ~3u) + 4 + (op & 0xFF) * 4, 4, &t) && ((t >> 24) == 0x80 || (t >> 24) == 0xA0))
                    table_base = t;
                break;
            }
        }
    }
    if (an_funcs_build(&src, 0) != 0) { fprintf(stderr, "an_funcs_build failed\n"); exit(1); }
}

int main(int argc, char **argv) {
    if (argc < 2) { fprintf(stderr, "usage: see the header of dasmref.c\n"); return 2; }
    const char *mode = argv[1];
    if (!strcmp(mode, "words") && argc == 3) {
        uint32_t pc = (uint32_t)strtoul(argv[2], 0, 0);
        for (uint32_t op = 0; op < 0x10000; op++) line(pc, (uint16_t)op, syn_rd);
        return 0;
    }
    if (!strcmp(mode, "dsp")) {
        char text[96];
        g_pc = 0x8c001000u;
        for (uint32_t op = 0xF800; op < 0xFC00; op++) {
            uint64_t h = 0xcbf29ce484222325ull;    /* FNV-1a 64 over "text\n" of every form */
            for (uint32_t g = 0; g < 0x10000; g++) {
                g_second = (uint16_t)g;
                sh4_decode((uint16_t)op, g_pc, dsp_rd, 0, 0, text, sizeof text);
                for (const char *p = text; *p; p++) { h ^= (uint8_t)*p; h *= 0x100000001b3ull; }
                h ^= '\n'; h *= 0x100000001b3ull;
            }
            printf("%04x %016llx\n", op, (unsigned long long)h);
        }
        return 0;
    }
    if (!strcmp(mode, "image") && argc == 6) {
        load(argv[2]);
        img_base = (uint32_t)strtoul(argv[3], 0, 0);
        uint32_t start = (uint32_t)strtoul(argv[4], 0, 0), end = (uint32_t)strtoul(argv[5], 0, 0);
        static char obuf[1 << 20];
        setvbuf(stdout, obuf, _IOFBF, sizeof obuf);
        for (uint32_t pc = start; pc < end; pc += 2) {
            uint32_t op;
            if (!img_rd(0, pc, 2, &op)) break;
            line(pc, (uint16_t)op, img_rd);
        }
        return 0;
    }
    if (!strcmp(mode, "funcs") && argc == 6) {
        open_src(argv + 2);
        for (int i = 0; i < an_funcs_count(); i++) {
            const an_func_t *f = an_func(i);
            printf("%08x %04x %04x\n", f->addr, f->size, f->pool);
        }
        return 0;
    }
    if (!strcmp(mode, "xrefs") && argc >= 7) {
        open_src(argv + 2);
        static an_xref_t xr[1 << 16];
        for (int k = 6; k < argc; k++) {
            uint32_t t = (uint32_t)strtoul(argv[k], 0, 0);
            int n = an_xrefs(&src, t, xr, 1 << 16, 0);
            printf("target %08x %d\n", t, n);
            for (int i = 0; i < n && i < (1 << 16); i++) printf("%08x %d\n", xr[i].from, xr[i].kind);
        }
        return 0;
    }
    fprintf(stderr, "bad arguments\n");
    return 2;
}
