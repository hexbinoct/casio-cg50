/*
 * fx-CG50 flash/RAM dumper — gint add-in.
 *
 * Streams memory-mapped regions over USB to the PC via fxlink. Read-only.
 *
 * Build:  scaffold a project, then drop this in as the main source:
 *           fxsdk new flashdump      # creates a CMake gint project
 *           # replace src/main.c with this file (keep the project's CMakeLists)
 *           fxsdk build-cg           # -> flashdump.g3a
 *
 * NOTE: the gint USB API symbol names below are correct for recent gint
 * (usb_ff_bulk / usb_open / usb_write_sync / usb_fxlink_*). If your gint version
 * differs, check <gint/usb.h>, <gint/usb-ff-bulk.h> and adjust. The dumping logic
 * (the regions + chunked streaming) stays the same.
 *
 * PC side:   fxlink -iw -o dump_     (wait for, and save, incoming transfers)
 * then run this add-in from the Menu with USB connected in fxlink mode.
 */

#include <gint/display.h>
#include <gint/keyboard.h>
#include <gint/usb.h>
#include <gint/usb-ff-bulk.h>
#include <stdint.h>

/* Regions to dump. START CONSERVATIVE for flash size, then increase if it
 * completes (reading past real flash end faults). fx-CG50 flash is 16-32 MB. */
struct region { const char *name; uint32_t addr; uint32_t size; };

static const struct region REGIONS[] = {
    /* THE key dump: whole flash = OS + fls0 storage + system area.
     * Start at 16 MB; if it finishes cleanly, bump to 0x02000000 (32 MB). */
    { "flash_full", 0x80000000, 0x01000000 },
    /* OS region only (cross-check vs our unpacked image; must match). */
    { "os",         0x80000000, 0x00C00000 },
    /* Live RAM (boot/runtime oracle). */
    { "dram",       0x8C000000, 0x00800000 },
    /* On-chip RAM (IRQ tables @0xFD80xxxx, kernel structs). */
    { "ilram",      0xFD800000, 0x00010000 },
};
#define NREGIONS (int)(sizeof(REGIONS)/sizeof(REGIONS[0]))

#define CHUNK 0x10000   /* 64 KB streaming chunks */

static void stream_region(const struct region *r)
{
    int pipe = usb_ff_bulk_output();

    /* fxlink message header: app id, type tag, total payload size */
    usb_fxlink_header_t header;
    usb_fxlink_fill_header(&header, "flashdump", r->name, r->size);
    usb_write_sync(pipe, &header, sizeof header, /*asynchronous=*/false);

    /* payload, in chunks, with a tiny on-screen progress bar */
    for(uint32_t off = 0; off < r->size; off += CHUNK) {
        uint32_t n = (r->size - off < CHUNK) ? (r->size - off) : CHUNK;
        usb_write_sync(pipe, (void const *)(r->addr + off), n, false);

        dclear(C_WHITE);
        dtext(2, 2, C_BLACK, "fx-CG50 flash dumper");
        dprint(2, 22, C_BLACK, "region: %s", r->name);
        dprint(2, 40, C_BLACK, "0x%08X +0x%06X / 0x%06X",
               (unsigned)r->addr, (unsigned)off, (unsigned)r->size);
        drect(2, 60, 2 + (int)(390u * (uint64_t)off / r->size), 70, C_BLACK);
        dupdate();
    }
    usb_commit_sync(pipe);
}

int main(void)
{
    dclear(C_WHITE);
    dtext(2, 2, C_BLACK, "Connect USB in fxlink mode,");
    dtext(2, 20, C_BLACK, "start: fxlink -iw -o dump_");
    dtext(2, 46, C_BLACK, "Then press [EXE] to begin.");
    dupdate();
    while(getkey().key != KEY_EXE) {}

    /* open the fxlink bulk interface and wait for the host */
    usb_interface_t const *intf[] = { &usb_ff_bulk, NULL };
    usb_open(intf, GINT_CALL_NULL);
    usb_open_wait();

    for(int i = 0; i < NREGIONS; i++)
        stream_region(&REGIONS[i]);

    usb_close();

    dclear(C_WHITE);
    dtext(2, 2, C_BLACK, "Dump complete.");
    dtext(2, 22, C_BLACK, "Check dump_*.bin on the PC.");
    dtext(2, 46, C_BLACK, "[EXIT] to quit.");
    dupdate();
    while(getkey().key != KEY_EXIT) {}
    return 1;
}
