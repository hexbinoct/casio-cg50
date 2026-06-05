# fx-CG50 — on-device probe of the 0xA4CB0000 BCD-ALU peripheral (build + operate)

## ⚠ STATUS UPDATE (read this FIRST — supersedes the framing below; ~cont.18d)
**The bug is already FIXED in the emulator. The calculator now computes: Run-Matrix renders
`98765` (typed) and `4.695555556` (the leftover `56÷36+3.14`) — both were `0` before.** We
reverse-engineered the peripheral as a multi-word packed-BCD ALU and IMPLEMENTED it in both
`emu/mmio.py` (class `BCDALU`) and `emu_go/mmio.go` (`bcdALU`), and it's validated end-to-end.

So this probe is now a **refinement/verification** step, NOT a blocker. Its job, in priority order:
1. **cmd4** — the ONE thing we couldn't derive. It's used once by the OS (@0x800737f2) and we
   currently implement it as a provisional **passthrough (result = A)**. The probe must reveal its
   real operation (run cmd 4 across the test vectors below; compare to passthrough).
2. **Carry/borrow + overflow EDGES** — confirm the exact latch behaviour for the multi-word
   add/sub (does cmd 1/0 reset carry/borrow to 0? what happens on top-nibble overflow / borrow
   past the MSW? is there a status/flag reg?). These matter for real arithmetic (×, ÷, rounding
   that actually rounds, big subtractions) which we have NOT broadly tested yet.
3. **Regression-confirm our model**: cmd1/3 = BCD add first/continue, cmd0/2 = BCD sub
   first/continue, carry/borrow latched between words, LSW-first. The probe results for cmd 0-3
   should match this; if any differ, that's a bug to fix in both mmio files.
After the probe: update cmd4 (+ any edge fixes) in BOTH `emu/mmio.py` BCDALU and
`emu_go/mmio.go` bcdALU (keep them identical), regen goldens, `go -C emu_go test .`, re-verify.

(The original instructions below are still accurate for BUILDING and RUNNING the probe; just
ignore the "we must learn ... to implement it" urgency — we already implemented the add/sub core.)

---

**Read this on the Mac (gint/fxsdk ready). Capture the truth table of the ALU at 0xA4CB0000;
paste the captured text into the next session.**

## Why this matters (root cause recap)
The emulator boots real OS 3.60 and reaches Run-Matrix, but every numeric result renders as
"0". I traced it (cont.18c) all the way down: the BCD number-formatting/rounding path
(FUN_800fc5a4 -> FUN_800f790e -> FUN_8004c21a -> FUN_8004c69c -> FUN_8004b2b0 ->
FUN_8005dc06 -> FUN_80079dbc -> FUN_80073f38 -> FUN_80072fc8) drives a HARDWARE peripheral
at 0xA4CB0000. FUN_80072fc8 writes operands + a command and reads a result. The emulator
does NOT implement 0xA4CB00xx, so the result register reads 0 -> 98765 becomes 0. The value
itself decodes fine in software; only this peripheral's output is missing. We must learn what
the peripheral computes and emulate it.

## STEP 0 (do FIRST, may save the whole probe)
Grep the installed gint / fxsdk / libfxcg sources and headers for this peripheral — Lephenixnoir's
gint documents many SH7305 blocks and someone may have already named it:
```
grep -rinE "a4cb|0xa4cb" ~/.local/share/fxsdk ~/path/to/gint /usr/*/include 2>/dev/null
```
Also search Planète Casio / fxcg-50 wiki / gint mpu headers (<gint/mpu/...>) for "A4CB" or a
"BCD"/"decimal"/"DSP"/"math" coprocessor at 0xA4CB____. If it's documented, grab the op spec
and skip to "Report back".

## Confirmed register map (from RE of flash_full.bin, 137 literal-pool refs)
- 0xA4CB0010  command   (16-bit write)   — OS uses command values {0,1,2,3,4}
- 0xA4CB0014  operand A (32-bit write)
- 0xA4CB0018  operand B (32-bit write)
- 0xA4CB001C  result    (32-bit read)
- (one stray ref to 0xA4CB0009 — read it too, may be a status/ID byte)
Access order the OS uses (FUN_80072fc8): write A(0x14), write B(0x18), write cmd(0x10),
then read result(0x1C), immediately, no delay. Values are BCD-packed (2 decimal digits/byte),
e.g. 98765 -> word 0x09876500.

## What we need
1. Initial dump of the register block 0xA4CB0000..0xA4CB003C (read each 32-bit word BEFORE
   touching anything) — may expose an ID/version/status.
2. Truth table: for each cmd in {0,1,2,3,4}, for each (A,B) test vector below, the 32-bit result.
3. A "sequence/state" test: the OS sometimes does cmd=1 then cmd=3 then cmd=3 across the three
   32-bit words of one value (FUN_80072fc8). So the unit MAY be a multi-word op where cmd=1
   means "first word/start" and cmd=3 means "continue". We test isolated calls AND that exact
   1,3,3 sequence to detect hidden accumulator state.

## CRITICAL cautions
- The OS uses this SAME peripheral for its own math. Do ALL raw register writes/reads in a tight
  burst and store results into an array. Do NOT call any OS/gint function (printf, Bdisp, getkey,
  malloc) BETWEEN setting operands and reading the result — that could run OS math and clobber the
  unit. Collect everything first, format/print afterwards.
- Run from a normal add-in (OS alive) so the peripheral's clock is already enabled (the OS uses it).
- If results look stale/wrong, try inserting a few `__asm__("nop")` (or a tiny volatile spin)
  between the cmd write and the result read, and re-run.
- Use uncached access. 0xA4CB0000 is in the P4/peripheral area and is inherently uncached on
  SH4 (0xA4xxxxxx), so a plain volatile pointer is fine; do not add 0x20000000.

## The add-in (gint C). Verify the fxlink/usb API names against your installed gint headers;
## if unsure, use the on-screen paged fallback (always works — photograph each page).
```c
#include <gint/display.h>
#include <gint/keyboard.h>
#include <gint/usb.h>          // for fxlink text capture (verify symbols on your gint)
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define ALU ((volatile uint32_t *)0xA4CB0000)   // ALU[o/4]
#define CMD (*(volatile uint16_t *)0xA4CB0010)

// One peripheral op, matching the OS access order exactly.
static uint32_t alu(uint16_t cmd, uint32_t A, uint32_t B){
    ALU[0x14/4] = A;
    ALU[0x18/4] = B;
    CMD         = cmd;
    return ALU[0x1C/4];
}

// test vectors chosen to separate passthrough / binary-add / BCD-add / sub / mul / shift
static const uint32_t TA[] = {
    0x00000000, 0x12345678, 0x00000000, 0x00000001, 0x00000009, 0x00000010,
    0x99999999, 0x12345678, 0x12345678, 0x09876500, 0x10000000, 0x00000005 };
static const uint32_t TB[] = {
    0x00000000, 0x00000000, 0x12345678, 0x00000001, 0x00000009, 0x00000010,
    0x00000001, 0x11111111, 0x87654321, 0x05000000, 0x00000001, 0x00000003 };
#define NV ((int)(sizeof(TA)/sizeof(TA[0])))

static char out[8192];
static int  olen;
static void emit(const char *s){ int n=strlen(s); if(olen+n<(int)sizeof(out)){ memcpy(out+olen,s,n); olen+=n; } }
static void emitf(const char *fmt, ...){ char b[160]; va_list ap; va_start(ap,fmt); vsnprintf(b,sizeof b,fmt,ap); va_end(ap); emit(b); }

int main(void){
    // --- PHASE 1: raw capture (no OS calls in the hot loops) ---
    uint32_t initregs[16];
    for(int i=0;i<16;i++) initregs[i] = ALU[i];          // 0x00..0x3C

    uint32_t res[5][NV];
    for(int c=0;c<5;c++)
        for(int v=0; v<NV; v++)
            res[c][v] = alu((uint16_t)c, TA[v], TB[v]);

    // sequence/state test: OS pattern cmd=1 then cmd=3 then cmd=3 on a 3-word value
    uint32_t seq[3];
    seq[0] = alu(1, 0x09876500, 0x05000000);
    seq[1] = alu(3, 0x00000000, 0x00000000);
    seq[2] = alu(3, 0x00000000, 0x00000000);
    // and the reverse to see if order matters
    uint32_t seqB[3];
    seqB[0] = alu(3, 0x09876500, 0x05000000);
    seqB[1] = alu(3, 0x00000000, 0x00000000);
    seqB[2] = alu(1, 0x00000000, 0x00000000);

    // --- PHASE 2: format (OS calls now OK) ---
    olen=0;
    emit("== A4CB0000 ALU PROBE ==\n");
    emit("init regs 0x00..0x3C:\n");
    for(int i=0;i<16;i++) emitf("  +%02x = %08lx\n", i*4, (unsigned long)initregs[i]);
    emitf("byte 0xA4CB0009 = %02x\n", *(volatile uint8_t*)0xA4CB0009);
    for(int c=0;c<5;c++){
        emitf("cmd %d:\n", c);
        for(int v=0; v<NV; v++)
            emitf("  A=%08lx B=%08lx -> %08lx\n",
                  (unsigned long)TA[v],(unsigned long)TB[v],(unsigned long)res[c][v]);
    }
    emitf("seq 1,3,3: %08lx %08lx %08lx\n",(unsigned long)seq[0],(unsigned long)seq[1],(unsigned long)seq[2]);
    emitf("seq 3,3,1: %08lx %08lx %08lx\n",(unsigned long)seqB[0],(unsigned long)seqB[1],(unsigned long)seqB[2]);

    // --- OUTPUT A: fxlink text over USB (preferred; bulk capture) ---
    // On Mac:  fxlink -iqw      (interactive, wait, quiet) BEFORE/just after launching the add-in.
    // Verify these symbols against your gint; common form:
    //   usb_interface_t const *itf[] = { &usb_ff_bulk, NULL };
    //   usb_open(itf, GINT_CALL_NULL); while(!usb_is_open()) usb_poll();
    //   usb_fxlink_text(out, 0);
    // If the USB API differs in your gint, just rely on OUTPUT B and skip this.

    // --- OUTPUT B: on-screen paged dump (ALWAYS works — photograph each page) ---
    int line=0, y=1; dclear(C_WHITE);
    for(int i=0;i<olen;){
        int j=i; while(j<olen && out[j]!='\n') j++;
        char tmp[160]; int n=j-i; if(n>159)n=159; memcpy(tmp,out+i,n); tmp[n]=0;
        dtext(1, y, C_BLACK, tmp); y+=14; line++; i=j+1;
        if(line>=14 || i>=olen){ dupdate(); getkey(); dclear(C_WHITE); y=1; line=0; }
    }
    return 1;
}
```

## Build + run
- `fxsdk build-cg` (or your usual: an fxsdk project with `fxsdk build-cg` producing a .g3a).
  Ensure `-lgint-cg` and the project type is add-in. If `<gint/usb.h>` symbols don't resolve,
  delete OUTPUT A entirely and ship with OUTPUT B only.
- Copy the .g3a to the calculator (USB mass storage / fxlink), launch it from the MENU.
- For OUTPUT A: on the Mac run `fxlink -iqw` (or `fxlink -i`) to receive the text; paste it.
- For OUTPUT B: press EXE to page through; photograph every page.

## Report back (what to bring to the office)
1. The full text: init reg dump, the `byte 0xA4CB0009` value, all 5 cmd blocks (each with the 12
   A/B -> result lines), and the two seq lines.
2. Anything from STEP 0 (if gint/community already documents 0xA4CB0000).
Paste that into the next Claude Code session; I'll derive each command's operation (binary vs BCD
add/sub/mul/shift/round/passthrough, and any multi-word state) and implement 0xA4CB0000 in both
emu_go/mmio.go and emu/mmio.py, then regen goldens + add a conformance case and confirm 98765 renders.

## Quick interpretation guide (so you can sanity-check on the spot)
- cmd where result == A for B=0  => passthrough/load.
- A=9,B=9: result 0x12 => binary add; 0x18 => BCD add; 0x51 => BCD mul(81); 0x0/borrow => sub.
- A=0x99999999,B=1: 0x9999999A => binary add; 0x00000000 (+carry) => BCD add with wrap.
- A=0x09876500,B=0x05000000 on some cmd returning 0x09876500 => that's the round/identity path we hit.
- If isolated cmd=3 differs from cmd=3-inside-the-1,3,3-sequence => the unit has internal state.
