/* dasmref: prints what DASM's C decoder and analysis (github.com/hexbinoct/cg50-dasm, src/)
 * make of a word table or an image, in a line format that the Go tests compare with the Go
 * port (dasm_compare_test.go builds it when DASM_SRC points at DASM's src/ directory):
 *
 *   cc -O2 -I$DASM_SRC dasmref.c $DASM_SRC/sh4dec.c $DASM_SRC/analysis.c names_host.c -o dasmref
 *
 * names_host.c is DASM's names.c with its two ROM reads (rom16, rom32: the running OS's memory)
 * turned into calls of host_rom16/host_rom32 below, which read an OS image file.
 *
 *   dasmref words <pc>                    every 16-bit word at pc, over synthetic memory
 *   dasmref dsp                           every 32-bit DSP form: one hash per first word
 *   dasmref image <file> <base> <start> <end>    every even address of an image
 *   dasmref funcs <file> <base> <entry> <rom>    the function table (rom: 1 = OS image)
 *   dasmref xrefs <file> <base> <entry> <rom> <target>...   cross-references to targets
 *   dasmref names <os> <file> <base> <entry> <rom> <start> <end>   names.c over an image, the
 *       syscall table read from the OS image <os>: for every address in [start,end) the listing
 *       comment of main.c ("com <addr> <comment>", when there is one) and, for every
 *       jmp/jsr/braf/bsrf, names_resolve_indirect ("ind <addr> <ok> <target> <name>|-"); then
 *       every function with main.c's func_name ("func <addr> <name>")
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

/* ---- the analysis needs a source; names.c the OS (its syscall table) ---- */
static src_t src;
const uint8_t *src_page(src_t *s, uint32_t addr) {
    if (!src_contains(s, addr)) return 0;
    return s->rom + ((addr - s->base) & ~(SRC_PAGE_SIZE - 1));
}
int src_read(src_t *s, uint32_t addr, int size, uint32_t *out) {
    if (!src_contains(s, addr) || addr - s->base + (uint32_t)size > s->size) return 0;
    uint32_t v = 0;
    for (int i = 0; i < size; i++) v = (v << 8) | s->rom[addr - s->base + (uint32_t)i];
    *out = v;
    return 1;
}
static const uint8_t *os_img; static size_t os_len;
uint32_t host_rom32(uint32_t a) {
    uint32_t o = a & 0x1FFFFFFF;
    if (o + 4 > os_len) return 0xFFFFFFFF;
    return (uint32_t)os_img[o] << 24 | (uint32_t)os_img[o + 1] << 16 | (uint32_t)os_img[o + 2] << 8 | os_img[o + 3];
}
uint16_t host_rom16(uint32_t a) {
    uint32_t o = a & 0x1FFFFFFF;
    if (o + 2 > os_len) return 0xFFFF;
    return (uint16_t)(os_img[o] << 8 | os_img[o + 1]);
}
static int src_rd(void *ctx, uint32_t a, int size, uint32_t *out) { return src_read((src_t *)ctx, a, size, out); }

/* main.c's comment_for, without the colours */
static void comment_for(uint32_t pc, const sh4_insn_t *in, char *out, int cap) {
    out[0] = 0;
    const char *nm;
    uint32_t tgt;
    switch (in->kind) {
    case SH4_K_LITERAL:
        if (!in->lit_ok) { snprintf(out, cap, "=?"); return; }
        if (in->lit_size == 4) {
            nm = names_for_handler(in->lit_val);
            if (nm) { snprintf(out, cap, "=0x%08lx %s", (unsigned long)in->lit_val, nm); return; }
            snprintf(out, cap, "=0x%08lx", (unsigned long)in->lit_val);
            return;
        }
        snprintf(out, cap, "=0x%04lx (%ld)", (unsigned long)in->lit_val, (long)(int16_t)in->lit_val);
        return;
    case SH4_K_MOVA:
        snprintf(out, cap, "&0x%08lx", (unsigned long)in->lit_addr); return;
    case SH4_K_JUMP_REG: case SH4_K_CALL_REG:
        if (names_resolve_indirect(&src, pc, in, &tgt, &nm)) {
            if (nm) snprintf(out, cap, "-> %s", nm);
            else snprintf(out, cap, "-> 0x%08lx", (unsigned long)tgt);
        }
        return;
    case SH4_K_BRANCH: case SH4_K_CALL:
        nm = names_for_handler(in->target);
        if (nm) snprintf(out, cap, "%s", nm);
        return;
    default: return;
    }
}

/* main.c's func_name */
static const char *func_name(uint32_t a, char *buf, int cap) {
    const char *nm;
    if (src.is_rom) { if ((nm = names_for_handler(a))) return nm; }
    else if (a == src.entry) return "start";
    else {
        for (uint32_t p = a; p < a + 8; p += 2) {       /* a stub: loads, then jmp @rN */
            uint32_t op, t; sh4_insn_t in; char txt[8];
            if (!src_read(&src, p, 2, &op)) break;
            sh4_decode((uint16_t)op, p, src_rd, &src, &in, txt, sizeof txt);
            if (in.kind == SH4_K_JUMP_REG) {
                if (names_resolve_indirect(&src, p, &in, &t, &nm) && nm) return nm;
                break;
            }
            if (in.kind != SH4_K_LITERAL && in.kind != SH4_K_NONE) break;
        }
    }
    snprintf(buf, cap, "sub_%08lx", (unsigned long)a);
    return buf;
}

static void open_src(char **argv) {
    load(argv[0]);
    img_base = (uint32_t)strtoul(argv[1], 0, 0);
    memset(&src, 0, sizeof src);
    src.rom = img; src.base = img_base; src.size = (uint32_t)img_len;
    src.entry = (uint32_t)strtoul(argv[2], 0, 0);
    src.is_rom = atoi(argv[3]);
    if (src.is_rom) { os_img = img; os_len = img_len; }   /* the OS image is its own syscall table */
    if (os_img) names_init();   /* else names_table_base() stays 0 (not used for an add-in) */
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
    if (!strcmp(mode, "names") && argc == 9) {
        load(argv[2]);
        os_img = img; os_len = img_len;
        open_src(argv + 3);
        uint32_t start = (uint32_t)strtoul(argv[7], 0, 0), end = (uint32_t)strtoul(argv[8], 0, 0);
        static char obuf[1 << 20];
        setvbuf(stdout, obuf, _IOFBF, sizeof obuf);
        for (uint32_t pc = start; pc < end; pc += 2) {
            uint32_t op, t = 0;
            sh4_insn_t in;
            char txt[8], com[64];
            const char *nm = 0;
            if (!src_read(&src, pc, 2, &op)) break;
            sh4_decode((uint16_t)op, pc, src_rd, &src, &in, txt, sizeof txt);
            comment_for(pc, &in, com, sizeof com);
            if (com[0]) printf("com %08x %s\n", pc, com);
            if (in.kind != SH4_K_JUMP_REG && in.kind != SH4_K_CALL_REG) continue;
            int ok = names_resolve_indirect(&src, pc, &in, &t, &nm);
            printf("ind %08x %d %08x %s\n", pc, ok, ok ? t : 0, nm ? nm : "-");
        }
        for (int i = 0; i < an_funcs_count(); i++) {
            char nb[24];
            printf("func %08x %s\n", an_func(i)->addr, func_name(an_func(i)->addr, nb, sizeof nb));
        }
        return 0;
    }
    fprintf(stderr, "bad arguments\n");
    return 2;
}
