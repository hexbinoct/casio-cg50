#!/usr/bin/env python3
"""Public syscall names for the fx-CG50, gathered once into re/syscall_names.json.

Sources (each downloaded once, cached in the git-ignored re/_cache/syscall_names/):
- libfxcg (github Jonimoose/libfxcg): its stubs `SYSCALL(_Name, 0xNNNN)` in libfxcg/**/*.S;
- gint (git.planet-casio.com Lephenixnoir/gint): the fx-CG block of src/kernel/syscalls.S
  (labels followed by `syscall(0xNNNN)`), plus the two numbers its kernel uses elsewhere
  (tlbh.S: the TLB-miss handler, osmenu.c: 0x1E58);
- WikiPrizm (prizm.cemetech.net): the syscall index page and the RSS feeds of its sections.
  Only page titles, links and the "Syscall index" numbers are kept, never page text;
- names from this repository's own reverse engineering (OURS below, and the wiki pages that
  mention a syscall number, found by scanning wiki/docs).

Writes re/syscall_names.json: {"0x1839": {"name":..., "src": [...], "wp": "Syscalls/...",
"gint": "label", "libfxcg": "name", "ours": "text", "pages": ["os/boot.md"]}}.
Only names, numbers and links: no third-party text.  Run: python3 re/syscall_names.py
(--refresh downloads again)."""
import io
import json
import os
import re
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
CACHE = os.path.join(HERE, "_cache", "syscall_names")
OUT = os.path.join(HERE, "syscall_names.json")
WIKI = os.path.join(ROOT, "wiki", "docs")

LIBFXCG_TGZ = "https://codeload.github.com/Jonimoose/libfxcg/tar.gz/refs/heads/master"
GINT_RAW = "https://git.planet-casio.com/Lephenixnoir/gint/raw/branch/master/"
WP = "https://prizm.cemetech.net/"

# Our own names: syscalls this project's RE work described, where no public name exists or
# where a short description helps.  Checked in the 3.60 image by the wiki pages cited.
OURS = {
    0x0006: "MMU off, flush the UTLB",
    0x0007: "Turn the MMU on",
    0x0008: "Flush the TLB (AT stays on)",
    0x0009: "Map one add-in page into the next TLB slot",
    0x0011: "Invalidate the instruction cache",
    0x0017: "Rebuild the list of add-ins",
    0x0025: "MMU off, clear the add-in page table",
    0x0026: "Map a starting add-in (the mapper)",
    0x0027: "Map the add-in RAM (slots 55-63)",
    0x0028: "Rebuild a running add-in's mapping",
    0x0192: "LCD controller power-down",
    0x0197: "Display off",
    0x025D: "Write 8 pixels straight to the panel",
    0x02B0: "Generic bitmap routine",
    0x08D8: "Start the TMU free-running counter",
    0x08CC: "Cursor blink routine (2 Hz)",
    0x0DDA: "Erase the 156 storage sectors",
    0x0DDB: "Erase 160 sectors from 0xC00000",
    0x1187: "Restart the OS through the reset vector",
    0x1197: "Is flash offset 0x300 blank? (skip setup)",
    0x11BD: "Arm the IRQ1/IRQ2 wake-up pins",
    0x11BE: "Disarm the IRQ1/IRQ2 wake-up pins",
    0x11BF: "Read the USB and 3PIN pins",
    0x11C0: "Read the USB pin (0xA4050162 bit 1)",
    0x11C6: "KEYSC set-up while the unit is off",
    0x11C7: "KEYSC normal set-up",
    0x11C9: "Copy the flash routines to IL RAM",
    0x11D6: "Delay on the one-shot unit 0xA44A0000",
    0x11D7: "Delay x 100 (jumps to 0x11D6)",
    0x11ED: "A low-power routine",
    0x127E: "Is one given key held? (port scan)",
    0x128F: "OpCodeToStr (a BASIC token's text)",
    0x12C0: "The body of GetKeyWait_OS",
    0x16F5: "Idle wait (0x16F6 with r6 = 1)",
    0x16F6: "Idle wait (calls the idle routine)",
    0x182E: "Reprogram the PFC (power-on)",
    0x1834: "No key yet: wait",
    0x183A: "PowerOff's body (r5 = flag)",
    0x1A2E: "Write a setup item",
    0x1A2F: "Read a setup item",
    0x1BB4: "Wait n milliseconds",
    0x1ED5: "Delay of about 1 ms",
}

GINT_EXTRA = {0x000C: "TLB-miss handler (tlbh.S)", 0x1E58: "SwitchToMainMenu (osmenu.c)"}


def fetch(url, name, refresh):
    path = os.path.join(CACHE, name)
    if os.path.exists(path) and not refresh:
        return open(path, "rb").read()
    os.makedirs(os.path.dirname(path), exist_ok=True)
    req = urllib.request.Request(url, headers={"User-Agent": "casio-cg50 syscall_names.py"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            data = r.read()
    except urllib.error.HTTPError:
        raise
    except urllib.error.URLError as e:
        # python.org's macOS Python has no CA certificates until "Install Certificates" runs:
        # fall back to curl, which uses the system's.
        if "CERTIFICATE" not in str(e):
            raise
        r = subprocess.run(["curl", "-sSfL", "-A", "casio-cg50 syscall_names.py", url],
                           capture_output=True, timeout=120)
        if r.returncode:
            raise urllib.error.URLError(f"curl {url}: {r.stderr.decode().strip()}")
        data = r.stdout
    open(path, "wb").write(data)
    time.sleep(0.5)  # be polite to the hosts
    print(f"  fetched {url} ({len(data)} bytes)")
    return data


def libfxcg(refresh):
    data = fetch(LIBFXCG_TGZ, "libfxcg-master.tar.gz", refresh)
    out = {}
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as t:
        for m in t.getmembers():
            if not (m.isfile() and m.name.endswith(".S") and "/libfxcg/" in m.name):
                continue
            text = t.extractfile(m).read().decode("latin-1")
            for name, num in re.findall(r"SYSCALL\(\s*_?([A-Za-z0-9_]+)\s*,\s*(0x[0-9A-Fa-f]+)\s*\)", text):
                out.setdefault(int(num, 16), name)
    return out


def gint(refresh):
    text = fetch(GINT_RAW + "src/kernel/syscalls.S", "gint-syscalls.S", refresh).decode("latin-1")
    m = re.search(r"#if GINT_OS_CG(.*?)#endif /\* GINT_OS_CG \*/", text, re.S)
    if not m:
        sys.exit("gint syscalls.S: no GINT_OS_CG block")
    out, label = {}, None
    for line in m.group(1).splitlines():
        line = line.split("/*")[0].strip()
        lm = re.match(r"^([A-Za-z_][A-Za-z0-9_]*):", line)
        if lm:
            label = lm.group(1).lstrip("_")
        sm = re.search(r"syscall\((0x[0-9A-Fa-f]+)\)", line)
        if sm and label:
            out[int(sm.group(1), 16)] = label
    # the two numbers gint's kernel uses outside syscalls.S (checked to still be there)
    tl = fetch(GINT_RAW + "src/kernel/tlbh.S", "gint-tlbh.S", refresh).decode("latin-1")
    om = fetch(GINT_RAW + "src/kernel/osmenu.c", "gint-osmenu.c", refresh).decode("latin-1")
    if re.search(r"GINT_OS_CG\s*\n#\s*define SYSCALL_TABLE 0x80020070\s*\n#\s*define SYSCALL_TLBH 12", tl):
        out[0x000C] = GINT_EXTRA[0x000C]
    if "0x1e58" in om:
        out[0x1E58] = GINT_EXTRA[0x1E58]
    return out


def wikiprizm(refresh):
    """{number: path} for every WikiPrizm syscall page that states a syscall index."""
    html = fetch(WP + "Syscalls/", "wp-syscalls.html", refresh).decode("utf-8", "replace")
    pages = set(re.findall(r'href="\.\./(Syscalls/[^"#?]+/)"', html))
    # sections = Syscalls/X/ entries that have pages below them (Syscalls/X/Y/); plain pages
    # have no feed of their own
    sections = {"/".join(p.split("/")[:2]) + "/" for p in pages if p.count("/") == 3}
    feeds = ["Syscalls/"] + sorted(sections)
    found, by_num = {}, {}
    for i, feed in enumerate(feeds):
        name = "wp-" + (feed.strip("/").replace("/", "_") or "root") + ".xml"
        try:
            x = fetch(WP + feed + "index.xml", name, refresh).decode("utf-8", "replace")
        except Exception as e:  # a plain page has no feed of its own
            if "404" not in str(e):
                print(f"  {feed}: {e}")
            continue
        for item in re.findall(r"<item>(.*?)</item>", x, re.S):
            link = re.search(r"<link>https://prizm\.cemetech\.net/([^<]+)</link>", item)
            desc = re.search(r"<description>(.*?)</description>", item, re.S)
            if not link:
                continue
            found[link.group(1)] = True
            if desc:
                for num in re.findall(r"[Ss]yscall (?:index|number)[^0-9A-Za-z]{0,6}(0x[0-9A-Fa-f]{1,4})",
                                      desc.group(1))[:2]:
                    by_num.setdefault(int(num, 16), link.group(1))
    # keep only pages the index itself links (they exist)
    by_num = {n: p for n, p in by_num.items() if p in pages}
    return by_num, len(pages)


def wiki_mentions():
    """{number: [page, ...]} for the syscall numbers the site's own pages mention."""
    out = {}
    pat_one = re.compile(r"[Ss]yscalls?\]?(?:\([^)]*\))?\s+`(0x[0-9A-Fa-f]{4})`")
    pat_list = re.compile(r"[Ss]yscalls\s+((?:`0x[0-9A-Fa-f]{4}`(?:\s*(?:,|and|–|-)\s*)?)+)")
    pat_paren = re.compile(r"\(syscall `(0x[0-9A-Fa-f]{4})`")
    pat_named = re.compile(r"`[A-Za-z_]+`\s+\((?:syscall\s+)?`(0x[0-9A-Fa-f]{4})`, at")
    for dp, _, files in os.walk(WIKI):
        for f in files:
            if not f.endswith(".md") or f == "syscall-reference.md":
                continue
            rel = os.path.relpath(os.path.join(dp, f), WIKI).replace(os.sep, "/")
            text = open(os.path.join(dp, f), encoding="utf-8").read()
            nums = set()
            for p in (pat_one, pat_paren, pat_named):
                nums.update(int(v, 16) for v in p.findall(text))
            for grp in pat_list.findall(text):
                vals = [int(v, 16) for v in re.findall(r"0x[0-9A-Fa-f]{4}", grp)]
                nums.update(vals)
                if "–" in grp and len(vals) == 2 and 0 < vals[1] - vals[0] < 16:
                    nums.update(range(vals[0], vals[1] + 1))
            if rel == "os/syscalls.md":  # its table's first column
                nums.update(int(v, 16) for v in re.findall(r"^\| `(0x[0-9A-Fa-f]{4})` \|", text, re.M))
            for n in nums:
                if n < 0x1F68:
                    out.setdefault(n, []).append(rel)
    return {n: sorted(v) for n, v in out.items()}


def main():
    refresh = "--refresh" in sys.argv
    lib = libfxcg(refresh)
    gi = gint(refresh)
    wp, wp_pages = wikiprizm(refresh)
    pages = wiki_mentions()
    out = {}

    def e(n):
        return out.setdefault(n, {"src": []})

    for n, name in lib.items():
        e(n)["libfxcg"] = name
    for n, name in gi.items():
        e(n)["gint"] = name
    for n, path in wp.items():
        e(n)["wp"] = path
    for n, text in OURS.items():
        e(n)["ours"] = text
    for n, p in pages.items():
        e(n)["pages"] = p
    for n, d in out.items():
        wpname = d["wp"].rstrip("/").split("/")[-1] if "wp" in d else None
        # Preferred display name: libfxcg's, then WikiPrizm's page title, then ours, then gint's
        # label; placeholders such as libfxcg's "Test127E" (Test + the number) are skipped
        real = lambda s: s and not re.fullmatch(r"Test[0-9A-Fa-f]{4}", s)
        d["name"] = next((s for s in (d.get("libfxcg"), wpname, d.get("ours"), d.get("gint")) if real(s)), "")
        d["src"] = [s for s in ("libfxcg", "gint", "wp", "ours") if s in d]
    named = {n: d for n, d in out.items() if d["name"]}
    js = {f"0x{n:04X}": out[n] for n in sorted(out)}
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(js, f, indent=1, ensure_ascii=False, sort_keys=False)
        f.write("\n")
    print(f"libfxcg: {len(lib)} numbers; gint (fx-CG): {len(gi)}; WikiPrizm: {len(wp)} numbered pages "
          f"of {wp_pages} linked; ours: {len(OURS)}; site pages mention {len(pages)} numbers")
    print(f"named: {len(named)} numbers -> {os.path.relpath(OUT, ROOT)}")


if __name__ == "__main__":
    main()
