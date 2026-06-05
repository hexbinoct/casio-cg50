#!/usr/bin/env python3
"""Analyze emu_go/eval_calls.txt (the BCD eval call-trace) to map structure:
  - most-called targets (the BCD primitives / arithmetic)
  - the shallow call tree (rebaselined so the trace's min depth = 0)
  - calls whose args point into the BCD work region 0x?c186000-0x?c188000
"""
import os, re, collections

HERE = os.path.dirname(__file__)
TR = os.path.join(HERE, "..", "emu_go", "eval_calls.txt")
lines = open(TR).read().splitlines()

call_re = re.compile(r"\[(\d+)\] d(-?\d+)\s+CALL 0x([0-9a-f]+) -> 0x([0-9a-f]+)\s+r4=0x([0-9a-f]+) r5=0x([0-9a-f]+) r6=0x([0-9a-f]+) r7=0x([0-9a-f]+)")
ret_re  = re.compile(r"\[(\d+)\] d(-?\d+)\s+RET\s+0x([0-9a-f]+)\s+r0=0x([0-9a-f]+) \((-?\d+)\)")

calls = []
targets = collections.Counter()
for ln in lines:
    m = call_re.match(ln)
    if m:
        cyc, d, pc, tgt, r4, r5, r6, r7 = m.groups()
        calls.append(("CALL", int(cyc), int(d), pc, tgt, [r4,r5,r6,r7]))
        targets[tgt] += 1
        continue
    m = ret_re.match(ln)
    if m:
        cyc, d, pc, r0, r0s = m.groups()
        calls.append(("RET", int(cyc), int(d), pc, r0, r0s))

print(f"total events: {len(calls)}  (calls: {sum(targets.values())})")
print("\n=== top 25 call targets (the eval/BCD primitives) ===")
for tgt, n in targets.most_common(25):
    print(f"  0x{tgt}  x{n}")

def inbcd(h):
    v = int(h, 16) & 0x0fffffff
    return 0x0186000 <= v < 0x0188000

print("\n=== distinct targets called with an arg in BCD work region 0x?c186000-0x?c188000 ===")
seen = collections.Counter()
for ev in calls:
    if ev[0] == "CALL" and any(inbcd(x) for x in ev[5]):
        seen[ev[4]] += 1
for tgt, n in seen.most_common(30):
    print(f"  0x{tgt}  x{n}")

# Shallow call tree (rebaseline depths)
mind = min(ev[2] for ev in calls)
print(f"\n=== shallow call tree (depth<= mind+3; mind={mind}) — first 120 entries ===")
shown = 0
for ev in calls:
    d = ev[2] - mind
    if d <= 3:
        if ev[0] == "CALL":
            print(f"  [{ev[1]}] {'  '*d}CALL 0x{ev[3]} -> 0x{ev[4]} r4=0x{ev[5][0]} r5=0x{ev[5][1]}")
        else:
            print(f"  [{ev[1]}] {'  '*d}RET  0x{ev[3]} r0=0x{ev[4]} ({ev[5]})")
        shown += 1
        if shown >= 120:
            break
