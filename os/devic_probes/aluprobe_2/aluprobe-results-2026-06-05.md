# A4CB0000 BCD-ALU probe — captured results + analysis (2026-06-05, on Mac)

Raw capture: `os/fxlink-aluprobe-probe-2026.06.05-05h10-1.bin` (2611 B text).
Add-in source: `aluprobe/src/main.c` (built `ALUProbe.g3a`). Capture path that
worked on this Mac: `fxlink -i -v -w` (auto-saves the named "probe" transfer to
`fxlink-aluprobe-probe-*.bin`). The `usb_fxlink_text()` + interactive-print path
overruns libusb on macOS ("data overrun") — use a NAMED-type transfer instead.

## Verdict: peripheral is a packed-BCD ALU (8 BCD digits / 32-bit word, LSW-first)
with a latched carry/borrow. cmd0-3 match the existing emu model exactly. cmd4 solved.

| cmd | operation             | latch-in        |
|-----|-----------------------|-----------------|
| 0   | BCD A - B             | borrow reset 0  |
| 1   | BCD A + B             | carry  reset 0  |
| 2   | BCD A - B - borrow    | borrow latched  |
| 3   | BCD A + B + carry     | carry  latched  |
| 4   | BCD A - B - 1         | borrow reset 1  |  <-- was provisional passthrough(=A)

## cmd4 = A - B - 1 (proof: cmd4[v] == cmd0[v] - 1 in BCD for all 12 vectors)
0-0 ->99999999 ; 12345678-0 ->12345677 ; 5-3 ->00000001 ; 99999999-1 ->99999997.
It is the "subtract-with-borrow START" twin of the add family (init borrow=1).
TODO @office: in emu/mmio.py BCDALU and emu_go/mmio.go bcdALU, replace cmd4
passthrough with: result = bcd_sub(A, B, borrow_in=1); latch borrow_out (like cmd0/2).
Keep both files identical. Regen goldens; `go -C emu_go test .`; re-verify 98765 renders.

## Edges confirmed
- Carry latches on MSW overflow: 99999999+1 -> 00000000(+carry); cmd3 v8 picks it up
  (12345678+11111111 = 23456790 under cmd3 vs 23456789 under cmd1).
- Borrow latches past MSW: 0-12345678 -> 87654322 (ten's complement) sets borrow;
  cmd2 propagates (1-1-borrow -> 99999999).
- START variants reset latch (cmd0=0, cmd1=0, cmd4=1); CONTINUE (2,3) use latched.
- seq 1,3,3 = 14876500 00000000 00000000 ; seq 3,3,1 identical -> latch reads 0
  after a non-overflowing add; shared add/sub latch behaves consistently.
- byte 0xA4CB0009 = 00 (no status/ID byte).
- Register block ALIASES every 0x10: init dump = 00030000 / 03810000 / 00000000 /
  03810000 repeating. Only 0x00-0x0F decode; 0x10/14/18/1C mirror 0x00/04/08/0C.
  Pre-op result reg reads 03810000 (leftover latch, not a version).

## cmd4 DEEP probe (v2 capture: fxlink-aluprobe-probe2-2026.06.05-06h01-1.bin)
Resolves cmd4 continue/latch + the shared-latch question. RESULTS:
- C1 latch-out : 4(0,0)=99999999  2(5,3)=00000001  -> cmd4 LATCHES borrow-out
  (the next cmd2 sees borrow=1: 5-3-1=1). Not latching would give 00000002.
- C2 3word-sub : 4(0,1)=99999998  2(0,0)=99999999  2(1,0)=00000000  -> a real
  multi-word subtract STARTED with cmd4 (forced borrow) + cmd2 continues works.
- C3 in-mode   : 4(0,0)=99999999  4(5,3)=00000001  -> cmd4 borrow-in is a FIXED
  reset to 1 (always A-B-1), NOT latch+1 (which would give 00000000).
- C5 shared    : 1(1,1)=00000002  4(0,0)=99999999  3(5,3)=00000009  -> CARRY AND
  BORROW SHARE ONE LATCH BIT. cmd4 left borrow=1; the next cmd3 (add-continue)
  consumed it as carry-in: 5+3+1=9. Separate latches would give 00000008.

## FINAL MODEL (implement exactly this in emu/mmio.py BCDALU + emu_go/mmio.go bcdALU)
One shared `flag` bit. All ops packed-BCD, LSW-first. flag_out latched for the
NEXT op; the OS sequences ops per 32-bit word of a multi-word value.
  cmd0 = A - B,        flag_in := 0      ; flag_out = borrow
  cmd1 = A + B,        flag_in := 0      ; flag_out = carry
  cmd2 = A - B - flag, flag_in := latch  ; flag_out = borrow
  cmd3 = A + B + flag, flag_in := latch  ; flag_out = carry
  cmd4 = A - B - 1,    flag_in := 1      ; flag_out = borrow   (was passthrough=A)
IMPORTANT: use a SINGLE shared flag field for carry & borrow (C5), not two. If
the current emu has separate carry/borrow latches, unify them.
Init reg block: not meaningful (varies run-to-run: v1=00030000/03810000.., v2=
00010000/0..). byte 0xA4CB0009=00. Block aliases every 0x10.

## Full raw table (self-contained — these 12 (A,B) vectors, in order, per cmd)
v :  A         B
0 :  00000000  00000000
1 :  12345678  00000000
2 :  00000000  12345678
3 :  00000001  00000001
4 :  00000009  00000009
5 :  00000010  00000010
6 :  99999999  00000001
7 :  12345678  11111111
8 :  12345678  87654321
9 :  09876500  05000000
10:  10000000  00000001
11:  00000005  00000003

Results (one column per vector v0..v11):
cmd0 (A-B):       0;12345678;87654322;0;0;0;99999998;01234567;24691357;04876500;09999999;2
cmd1 (A+B):       0;12345678;12345678;2;18;20;0;23456789;99999999;14876500;10000001;8
cmd2 (A-B-bor):   0;12345678;87654322;99999999;99999999;99999999;99999997;01234567;24691357;04876499;09999999;2
cmd3 (A+B+car):   0;12345678;12345678;2;18;20;0;23456790;99999999;14876500;10000001;8
cmd4 (A-B-1):     99999999;12345677;87654321;99999999;99999999;99999999;99999997;01234566;24691356;04876499;09999998;1
(Note: cmd2/cmd3 columns include carry/borrow LATCHED from the previous vector in
the same row, since the probe ran v0..v11 as one continuous sequence per cmd. The
isolated/per-op truth is in the FINAL MODEL section above. Ground-truth raw blobs:
os/fxlink-aluprobe-probe-*.bin (v1) and os/fxlink-aluprobe-probe2-*.bin (v2).)
