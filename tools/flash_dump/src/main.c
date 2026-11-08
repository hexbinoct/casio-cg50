/*
 * FlashDump: copies an fx-CG50's flash memory to a computer over USB, for the emulator
 * (CG50 Studio, the Android app). A gint add-in; read-only, it never writes to the flash.
 *
 * Build (tools/fxsdk-docker):
 *   docker run --rm -v "<repo>/tools/flash_dump:/work" fxsdk:latest fxsdk build-cg
 *   -> FLASHDMP.g3a
 *
 * On the computer, `fxlink -i -w` (from the fxSDK) receives the dump and saves it as
 * fxlink-flashdump-32mb-<date>.bin. Guide: tools/flash_dump/README.md.
 *
 * The OS version is read from the OS's own header in flash ("CASIOWIN" at 0x80020000, the
 * version string at 0x80020020), the same place the emulator reads it (emu_go/osversion.go).
 */

#include <gint/display.h>
#include <gint/keyboard.h>
#include <gint/usb.h>
#include <gint/usb-ff-bulk.h>
#include <stdint.h>
#include <string.h>

#define SUPPORTED_OS "03.60" /* what the emulator is made and tested for */

struct region { const char *name, *label; uint32_t addr; uint32_t size; };

/* fxlink names the received file fxlink-<application>-<type>-<date>.bin but keeps only 16
   characters of "<application>-<type>": with "flashdump-" that leaves 6 for the type */
static const struct region FLASH32 = { "32mb",  "flash", 0x80000000, 0x02000000 };
static const struct region FLASH16 = { "16mb",  "flash", 0x80000000, 0x01000000 };
static const struct region DRAM    = { "dram",  "RAM",   0x8C000000, 0x00800000 };
static const struct region ILRAM   = { "ilram", "IL RAM", 0xFD800000, 0x00010000 };

#define CHUNK 0x10000 /* 64 KB per USB write */

/* colours: a teal for headings, terracotta for warnings, grey for notes */
#define C_TEAL  C_RGB(3, 12, 13)
#define C_TERRA C_RGB(25, 11, 6)
#define C_GREY  C_RGB(12, 12, 12)

/* The OS version from the flash header, e.g. "03.60.0000"; "" if there is no valid header. */
static void os_version(char out[11])
{
    const char *hdr = (const char *)0x80020000;
    const char *v = (const char *)0x80020020;
    out[0] = 0;
    if(memcmp(hdr, "CASIOWIN", 8)) return;
    for(int i = 0; i < 10; i++) {
        char c = v[i];
        int dot = (i == 2 || i == 5);
        if(dot ? c != '.' : (c < '0' || c > '9')) return;
    }
    memcpy(out, v, 10);
    out[10] = 0;
}

static int y; /* the next text line, so screens read top to bottom */

static void line(color_t c, const char *text)
{
    dtext(6, y, c, text);
    y += 14;
}

static void gap(void) { y += 6; }

static void title(const char *text)
{
    dclear(C_WHITE);
    y = 6;
    line(C_TEAL, text);
    drect(6, y - 2, 389, y - 1, C_TEAL);
    y += 6;
}

/* the menu; returns the key chosen (F1, F2, F6 or EXIT) */
static int menu(const char *ver)
{
    title("FlashDump: copy the flash to a computer");
    if(!ver[0]) {
        line(C_TERRA, "OS version: not found (no CASIOWIN header).");
    }
    else {
        /* "03.60.0000" -> "3.60", as the Version screen and people write it */
        dprint(6, y, C_BLACK, "This calculator has OS %.4s.", ver[0] == '0' ? ver + 1 : ver);
        y += 14;
        if(!strncmp(ver, SUPPORTED_OS, 5)) {
            line(C_TEAL, "The emulator is made and tested for this OS.");
        }
        else {
            line(C_TERRA, "The emulator is made and tested for OS 3.60.");
            line(C_TERRA, "Other versions run, but aren't tested yet.");
        }
    }
    gap();
    line(C_BLACK, "[F1]  Whole flash, 32 MB  (recommended)");
    line(C_GREY,  "      OS and storage memory: your files and");
    line(C_GREY,  "      installed add-ins come along.");
    line(C_BLACK, "[F2]  First half only, 16 MB");
    line(C_GREY,  "      Runs, but most of your files stay behind.");
    line(C_BLACK, "[F6]  Flash and RAM  (for emulator developers)");
    line(C_BLACK, "[EXIT] Quit");
    dupdate();

    while(1) {
        int k = getkey().key;
        if(k == KEY_F1 || k == KEY_F2 || k == KEY_F6 || k == KEY_EXIT) return k;
    }
}

static void progress(const struct region *r, uint32_t done, int part, int parts)
{
    title("FlashDump: sending");
    if(r->size >= 0x100000)
        dprint(6, y, C_BLACK, "Part %d of %d: %s, %d MB", part, parts, r->label,
            (int)(r->size >> 20));
    else
        dprint(6, y, C_BLACK, "Part %d of %d: %s, %d KB", part, parts, r->label,
            (int)(r->size >> 10));
    y += 18;
    int w = (int)(380u * (uint64_t)done / r->size);
    drect_border(6, y, 386, y + 14, C_NONE, 1, C_TEAL);
    if(w > 0) drect(7, y + 1, 6 + w, y + 13, C_TEAL);
    y += 22;
    dprint(6, y, C_GREY, "%d.%d of %d MB", (int)(done >> 20),
        (int)(((done & 0xFFFFF) * 10) >> 20), (int)(r->size >> 20));
    y += 20;
    line(C_GREY, "A full 32 MB takes well under a minute.");
    line(C_GREY, "Keep the cable connected until it says Done.");
    dupdate();
}

static void send(const struct region *r, int part, int parts)
{
    int pipe = usb_ff_bulk_output();
    usb_fxlink_header_t header;
    usb_fxlink_fill_header(&header, "flashdump", r->name, r->size);
    usb_write_sync(pipe, &header, sizeof header, false);

    for(uint32_t off = 0; off < r->size; off += CHUNK) {
        uint32_t n = (r->size - off < CHUNK) ? (r->size - off) : CHUNK;
        usb_write_sync(pipe, (void const *)(r->addr + off), n, false);
        if((off & 0x7FFFF) == 0) progress(r, off, part, parts); /* every 512 KB */
    }
    usb_commit_sync(pipe);
}

int main(void)
{
    char ver[11];
    os_version(ver);

    int k = menu(ver);
    if(k == KEY_EXIT) return 1;

    const struct region *parts[3];
    int n = 0;
    if(k == KEY_F1) parts[n++] = &FLASH32;
    if(k == KEY_F2) parts[n++] = &FLASH16;
    if(k == KEY_F6) { parts[n++] = &FLASH32; parts[n++] = &DRAM; parts[n++] = &ILRAM; }

    title("FlashDump: connect to the computer");
    line(C_BLACK, "1. Connect the calculator to the computer by USB.");
    line(C_GREY,  "   No connection-mode choice is needed: this");
    line(C_GREY,  "   add-in handles the USB itself.");
    line(C_BLACK, "2. On the computer, run:   fxlink -i -w");
    line(C_GREY,  "   (fxlink comes with the fxSDK; see the guide)");
    gap();
    line(C_TEAL,  "Waiting for the computer...");
    dupdate();

    usb_interface_t const *intf[] = { &usb_ff_bulk, NULL };
    usb_open(intf, GINT_CALL_NULL);
    usb_open_wait();

    for(int i = 0; i < n; i++)
        send(parts[i], i + 1, n);
    usb_close();

    title("FlashDump: done");
    line(C_BLACK, "The computer has the dump. fxlink saved it as");
    dprint(6, y, C_TEAL, "  fxlink-flashdump-%s-<date>.bin", parts[0]->name);
    y += 14;
    if(n > 1) line(C_GREY, "  (and one file per RAM region)");
    gap();
    line(C_BLACK, "Check its size: exactly 32 MB (33554432 bytes)");
    line(C_BLACK, "or 16 MB (16777216 bytes) for the first half.");
    line(C_BLACK, "Then open it in CG50 Studio or the Android app.");
    gap();
    line(C_GREY, "[EXIT] Quit");
    dupdate();
    while(getkey().key != KEY_EXIT) {}
    return 1;
}
