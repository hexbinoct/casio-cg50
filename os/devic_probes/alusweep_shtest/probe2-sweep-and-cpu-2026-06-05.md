# fx-CG50 follow-up probes (2026-06-05, Mac) — BCD-ALU full sweep + SH4 CPU conformance

Two add-ins built on this Mac, run on the physical calculator, captured via fxlink.
Raw blobs in `os/`:
- `fxlink-alusweep-sweep-2026.06.05-06h33-1.bin`  (BCD-ALU full characterization)
- `fxlink-shtest-cpu-2026.06.05-06h41-1.bin`      (SH4 CPU conformance ground truth)
Sources: `alusweep/src/main.c`, `shtest/src/main.c`. See also
`os/aluprobe-results-2026-06-05.md` (the first BCD-ALU probe) and the memory files.

================================================================================
## PART A — 0xA4CB0000 BCD-ALU: FULL command decode + register behavior
================================================================================

### Complete command decode (cmd 0..15; bit3 IGNORED so 8..15 mirror 0..7)
Decode of the 16-bit command written to 0xA4CB0010:
  operation = (cmd & 1) ? ADD : SUB           (packed-BCD, 8 digits, LSW-first)
  flag_in   = (cmd & 4) ? 1                    (forced one)
            : (cmd & 2) ? latch                (shared carry/borrow latch)
            :             0                    (forced zero)
  flag_out  = carry (add) or borrow (sub), written to the shared latch.
Resulting table (NEW: cmd5 = A+B+1 found; cmd6/7,12..15 = aliases):
  0 = A - B            8  = A - B
  1 = A + B            9  = A + B
  2 = A - B - flag     10 = A - B - flag
  3 = A + B + flag     11 = A + B + flag
  4 = A - B - 1        12 = A - B - 1
  5 = A + B + 1        13 = A + B + 1
  6 = A - B - 1        14 = A - B - 1
  7 = A + B + 1        15 = A + B + 1
(OS only uses 0..4, but implement the full decode above for robustness.)

### Registers (block aliases every 0x10; 4 words: +00 +04 +08 +0C)
  +00 = command / STATUS   (write cmd here; READ-back exposes the flag)
  +04 = operand A
  +08 = operand B
  +0C = result
Status read-back at +00 observed:
  flag SET (after cmd4(0,0), borrow=1): 0x10040000   (result reg = 0x99999999)
  flag CLR (after cmd1(0,0)):           0x00010000   (result reg = 0x00000000)
  -> the flag is observable in the +00 status word (bits differ: SET has 0x10040000,
     CLR 0x00010000). Low field also reflects last command. Emulator MAY expose this.

### Other confirmed behavior
- Operands are STICKY: re-issuing the command (write +00 only, no A/B rewrite)
  recomputes the same result. A/B persist as registers.
- Result is valid IMMEDIATELY (identical after 0/1/4/16 nops; no busy/ready bit).
- Multi-word ripple holds at depth 5: add 99999999+1 then +0x3 then carry word ->
  00000000 00000000 00000000 00000000 00000001 ; sub mirror -> 99999999 x4 then 00000004.
- flag_out latches correctly for every cmd incl. the new 5/6/7/13/14/15.

================================================================================
## PART B — SH7305 (SH-4A user ISA, big-endian, NO FPU) CPU conformance
================================================================================
All values below are REAL-HARDWARE ground truth. Office: run the identical vectors
through emu_go/emu and diff. Format reproduced from the raw blob.

NOTE/CAVEAT: in [div1 unsigned] the QUOTIENTS are correct (match [C div]); the
REMAINDER column is unreliable (the test's hand div1 routine omits the final
remainder fixup). Use [C div] for remainders. The div1 instruction itself is
validated by the quotients.

[addc] a b tin -> sum/Tout
  ffffffff 00000001 0 -> 00000000/1 ; ffffffff 00000000 1 -> 00000000/1
  7fffffff 00000001 0 -> 80000000/0 ; 00000000 00000000 1 -> 00000001/0
  80000000 80000000 0 -> 00000000/1 ; 12345678 9abcdef0 1 -> acf13569/0
  ffffffff ffffffff 1 -> ffffffff/1
[subc] a b tin -> diff/Tout
  ffffffff 00000001 0 -> fffffffe/0 ; ffffffff 00000000 1 -> fffffffe/0
  7fffffff 00000001 0 -> 7ffffffe/0 ; 00000000 00000000 1 -> ffffffff/1
  80000000 80000000 0 -> 00000000/0 ; 12345678 9abcdef0 1 -> 77777787/1
  ffffffff ffffffff 1 -> ffffffff/1
[negc] a tin -> neg/Tout
  ffffffff 0 -> 00000001/1 ; ffffffff 1 -> 00000000/1 ; 7fffffff 0 -> 80000001/1
  00000000 1 -> ffffffff/1 ; 80000000 0 -> 80000000/1 ; 12345678 1 -> edcba987/1
  ffffffff 1 -> 00000000/1
[addv] a b -> sum/Tovf
  7fffffff 00000001 -> 80000000/1 ; 80000000 ffffffff -> 7fffffff/1
  7fffffff 7fffffff -> fffffffe/1 ; 00000000 00000000 -> 00000000/0
  40000000 40000000 -> 80000000/1 ; 80000000 80000000 -> 00000000/1
[subv] a b -> diff/Tovf
  7fffffff 00000001 -> 7ffffffe/0 ; 80000000 ffffffff -> 80000001/0
  7fffffff 7fffffff -> 00000000/0 ; 00000000 00000000 -> 00000000/0
  40000000 40000000 -> 00000000/0 ; 80000000 80000000 -> 00000000/0
[rotcl] a tin -> res/Tout
  80000000 0 -> 00000000/1 ; 00000001 1 -> 00000003/0
  12345678 1 -> 2468acf1/0 ; ffffffff 0 -> fffffffe/1
[rotcr] a tin -> res/Tout
  80000000 0 -> 40000000/0 ; 00000001 1 -> 80000000/1
  12345678 1 -> 891a2b3c/0 ; ffffffff 0 -> 7fffffff/1
[shad] v n -> res (arithmetic; n<0 = right shift, sign fill; n=-32 -> 0 or all-sign)
  12345678: n=4->23456780 -4->01234567 31->00000000 -31->00000000 1->2468acf0
            -1->091a2b3c 0->12345678 -32->00000000
  80000000: 4->00000000 -4->f8000000 31->00000000 -31->ffffffff 1->00000000
            -1->c0000000 0->80000000 -32->ffffffff
  00000001: 4->00000010 -4->00000000 31->80000000 -31->00000000 1->00000002
            -1->00000000 0->00000001 -32->00000000
  ffffffff: 4->fffffff0 -4->ffffffff 31->80000000 -31->ffffffff 1->fffffffe
            -1->ffffffff 0->ffffffff -32->ffffffff
[shld] v n -> res (logical; n=-32 -> 0)
  12345678: 4->23456780 -4->01234567 31->00000000 -31->00000000 1->2468acf0
            -1->091a2b3c 0->12345678 -32->00000000
  80000000: 4->00000000 -4->08000000 31->00000000 -31->00000001 1->00000000
            -1->40000000 0->80000000 -32->00000000
  00000001: 4->00000010 -4->00000000 31->80000000 -31->00000000 1->00000002
            -1->00000000 0->00000001 -32->00000000
  ffffffff: 4->fffffff0 -4->0fffffff 31->80000000 -31->00000001 1->fffffffe
            -1->7fffffff 0->ffffffff -32->00000000
[dmulu] a b -> MACH:MACL
  ffffffff ffffffff -> fffffffe:00000001 ; 12345678 00000010 -> 00000001:23456780
  7fffffff 00000002 -> 00000000:fffffffe ; 00000002 00000003 -> 00000000:00000006
  80000000 00000002 -> 00000001:00000000
[dmuls] a b -> MACH:MACL
  ffffffff ffffffff -> 00000000:00000001 ; 12345678 00000010 -> 00000001:23456780
  7fffffff 00000002 -> 00000000:fffffffe ; 00000002 00000003 -> 00000000:00000006
  80000000 00000002 -> ffffffff:00000000
[div1 unsigned] dividend divisor -> quot (rem UNRELIABLE, see [C div])
  00000064 00000007 -> q=0000000e ; ffffffff 00000003 -> q=55555555
  000f4240 000003e8 -> q=000003e8 ; 00000007 00000002 -> q=00000003
  80000000 00000003 -> q=2aaaaaaa ; 00003039 00000001 -> q=00003039
  00000000 00000005 -> q=00000000 ; 7fffffff ffffffff -> q=00000000
[C div] a b -> u:a/b u:a%b s:a/b s:a%b   (CORRECT quotient+remainder)
  00000064 00000007 -> 0000000e 00000002 0000000e 00000002
  ffffffff 00000003 -> 55555555 00000000 00000000 ffffffff
  000f4240 000003e8 -> 000003e8 00000000 000003e8 00000000
  00000007 00000002 -> 00000003 00000001 00000003 00000001
  80000000 00000003 -> 2aaaaaaa 00000002 d5555556 fffffffe
  00003039 00000001 -> 00003039 00000000 00003039 00000000
  00000000 00000005 -> 00000000 00000000 00000000 00000000
  7fffffff ffffffff -> 00000000 7fffffff 80000001 00000000
[mac.l] sum of 0x40000000^2 *2 + 0x10000^2 + 0x7FFFFFFF^2
  S=0 -> 60000000:00000001         (full 64-bit accumulate)
  S=1 -> 00007fff:ffffffff         (saturates to 48-bit signed max)
[cmp] a b -> eq hs ge hi gt str | (a:)pz pl | tst(a,b)
  00000005 00000005 -> 1 1 1 0 0 1 | 1 1 | 0
  00000005 00000007 -> 0 0 0 0 0 1 | 1 1 | 0
  ffffffff 00000001 -> 0 1 0 1 0 0 | 0 0 | 0
  80000000 7fffffff -> 0 1 0 1 0 0 | 0 0 | 1
  00000003 00000003 -> 1 1 1 0 0 1 | 1 1 | 0
  00000000 00000000 -> 1 1 1 0 0 1 | 1 0 | 1
  41424344 44434241 -> 0 0 0 0 0 0 | 1 1 | 0
  00ff00ff ff00ff00 -> 0 0 1 0 1 0 | 1 1 | 1
[munge] a=0x11223344 b=0xAABBCCDD
  swap.b=11224433 swap.w=33441122 xtrct(a,b)=3344aabb
  extu.b(a)=00000044 exts.b(0xF0)=fffffff0 extu.w(a)=00003344 exts.w(0xF000)=fffff000

Verified by hand vs SH4 semantics: all correct except the div1 remainder caveat above.
