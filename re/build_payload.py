#!/usr/bin/env python3
"""Build payload.json for the noted.prodaccess1790.site push from alu_probe_note.md.
Keeps the auth token out of any file (token goes only in the curl -H header)."""
import json, os
HERE = os.path.dirname(__file__)
content = open(os.path.join(HERE, "alu_probe_note.md"), encoding="utf-8").read()
payload = {
    "topic": "fx-CG50 ALU probe v2 (UPDATE: bug already FIXED in emu; probe now = confirm cmd4 + edges)",
    "content": content,
    "repo": "F:/ru/myprojects/may/cg50",
}
out = os.path.join(HERE, "payload.json")
with open(out, "w", encoding="utf-8") as f:
    json.dump(payload, f)
print(f"wrote {out}  ({len(content)} chars of note, {os.path.getsize(out)} bytes json)")
