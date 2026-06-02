#!/usr/bin/env python3
"""Python-side CPU regression test: replays the frozen conformance cases
(emu/conformance.json) through the reference Python CPU and asserts the outputs
still match the committed expectations. If cpu.py behaviour drifts, this fails.

The SAME conformance.json is consumed by emu_go/conformance_test.go, so the Go
port is held to the identical contract.

Run:  python emu/test_cpu.py     (or: pytest emu/test_cpu.py)
"""
import os, sys, json
sys.path.insert(0, os.path.dirname(__file__))
from conformance_gen import replay

JSON = os.path.join(os.path.dirname(__file__), "conformance.json")


def _load():
    with open(JSON) as f:
        return json.load(f)["cases"]


def run_all():
    cases = _load()
    fails = 0
    for c in cases:
        got = replay(c)
        exp = c["expect"]
        if got != exp:
            fails += 1
            print(f"FAIL {c['name']}")
            for k in exp:
                if got.get(k) != exp.get(k):
                    print(f"   {k}: got {got.get(k)} want {exp.get(k)}")
    print(f"\n{len(cases)-fails}/{len(cases)} conformance cases pass")
    return fails


def test_conformance():
    """pytest entry point."""
    cases = _load()
    for c in cases:
        assert replay(c) == c["expect"], c["name"]


if __name__ == "__main__":
    sys.exit(1 if run_all() else 0)
