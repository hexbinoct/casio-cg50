#!/usr/bin/env python3
"""Run one emu_go probe test with environment variables set, without an inline `VAR=x go …`
(which defeats the command allowlist).

  python re/run_probe.py TestResetStorage RESET_AS=1 [MORE=val ...]
"""
import os
import subprocess
import sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "emu_go")


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    test, env = sys.argv[1], dict(os.environ)
    for kv in sys.argv[2:]:
        k, _, v = kv.partition("=")
        env[k] = v
    cmd = ["go", "-C", ROOT, "test", "-tags", "probe", "-run", f"^{test}$", "-count=1", "-v",
           "-timeout", "30m", "."]
    sys.exit(subprocess.call(cmd, env=env))


if __name__ == "__main__":
    main()
