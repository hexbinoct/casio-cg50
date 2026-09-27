"""Derive the emulator timing constants from a KEYPROBE.TXT captured on the real fx-CG50
(tools/keyprobe). Usage: python re/keyprobe_analyze.py <KEYPROBE.TXT>

Prints: Pphi (peripheral clock), the OS timer tick rate (measured + from TMU1 config), the
KEYSC scan period while a key is held / idle behaviour, and the instruction-based constants to
use in the emulator at a given host throughput."""
import re, sys

PRESCALE = {0: 4, 1: 16, 2: 64, 3: 256, 4: 1024}   # SH TMU TCR.TPSC -> Pphi divider


def grab(pat, text, flags=0):
    m = re.search(pat, text, flags)
    if not m:
        raise SystemExit(f"missing field: {pat}")
    return m.groups()


def main():
    text = open(sys.argv[1], encoding="utf-8", errors="replace").read()
    print(text.strip())
    print("\n== derived ==")
    tmu = {}
    for n, tcor, tcnt, tcr in re.findall(r"TMU(\d) TCOR=([0-9a-f]+) TCNT=([0-9a-f]+) TCR=([0-9a-f]+)", text):
        tmu[int(n)] = (int(tcor, 16), int(tcnt, 16), int(tcr, 16))
    (ticks1s, tcnt2_1s, r64a, r64b) = grab(r"1s unmasked: ticks=(\d+) tcnt2=(\d+) r64 ([0-9a-f]+)->([0-9a-f]+)", text)
    (secs, scans, detects, other, ticks, tcnt2) = grab(r"HELD (\d+)s: scans=(\d+) detects=(\d+) other=(\d+) ticks=(\d+) tcnt2=(\d+)", text)
    (isecs, iscans, idetects, iticks) = grab(r"IDLE (\d+)s: scans=(\d+) detects=(\d+) ticks=(\d+)", text)
    data = grab(r"first scan R64=([0-9a-f]+) data:((?: [0-9a-f]{4}){6})", text)
    secs, scans, ticks, tcnt2 = int(secs), int(scans), int(ticks), int(tcnt2)

    div2 = PRESCALE.get(tmu[2][2] & 7, None)
    tcnt2_hz = tcnt2 / secs
    if tcnt2 == 0:
        pphi = None
        print("TMU2 did not count: the OS's TMU channels are stopped (TSTR=0) — the OS tick is not TMU-based")
    elif div2:
        pphi = tcnt2_hz * div2
        print(f"TMU2 counts {tcnt2_hz:,.0f}/s at Pphi/{div2}  ->  Pphi = {pphi/1e6:.3f} MHz")
    else:
        pphi = None
        print(f"TMU2 counts {tcnt2_hz:,.0f}/s (TCR2 prescaler {tmu[2][2] & 7} unknown)")

    tick_hz = ticks / secs
    if ticks == 0:
        print("OS tick counter at 0xFD8017D0 did not move inside the world switch: timer source/counter TBD (ETMU probe)")
    else:
        print(f"OS tick counter: {tick_hz:.1f} Hz measured over {secs} s (1-s sanity: {ticks1s} Hz)")
    if pphi and 1 in tmu:
        tcor1, _, tcr1 = tmu[1]
        div1 = PRESCALE.get(tcr1 & 7)
        if div1:
            print(f"TMU1 config: Pphi/{div1} / (TCOR1+1={tcor1+1})  ->  {pphi/div1/(tcor1+1):.2f} Hz  (IRQ enabled={bool(tcr1 & 0x20)})")

    if scans:
        scan_ms = 1000.0 * secs / scans
        print(f"KEYSC while held: {scans} scans in {secs} s  ->  scan period {scan_ms:.2f} ms  "
              f"(detect edges={detects}, other flags={other})")
        print(f"   -> OS key repeat: first after 20 scans = {20*scan_ms:.0f} ms, then every {scan_ms:.1f} ms ({1000/scan_ms:.1f}/s)")
    else:
        print("KEYSC while held: NO scan-complete flags seen (model assumption wrong, or IRQ mask ineffective)")
    print(f"KEYSC idle: {iscans} scans, {idetects} detects in {isecs} s  "
          f"-> {'scans continuously' if int(iscans) > 1 else 'quiet when idle'}")
    words = [int(w, 16) for w in data[1].split()]
    print(f"first-scan key words: {' '.join(f'{w:04x}' for w in words)}  "
          f"(RIGHT expected word3 bit9=0x0200: {'OK' if words[3] & 0x0200 else 'MISMATCH'})")

    print("\n== emulator constants (instruction-based time) ==")
    for name, ips in (("phone ~22M ips", 22e6), ("phone ~15M ips", 15e6), ("desktop ~70M ips", 70e6)):
        if scans:
            extra = f";  timer period for {tick_hz:.0f} Hz = {ips/tick_hz:,.0f} instr (now 30,000)" if tick_hz else ""
            print(f"  {name:18s}: KeyScanPeriod = {ips*secs/scans:,.0f} instr{extra}")


if __name__ == "__main__":
    main()
