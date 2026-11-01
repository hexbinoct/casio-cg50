#!/usr/bin/env python3
"""Generates the app's launcher icons: three adaptive icons the user picks from in the app's
settings (MainActivity.showAppIcons; one <activity-alias> per icon in AndroidManifest.xml).

    python3 android/tools/make_launcher_icons.py [--preview DIR]

writes, under android/app/src/main/res/:
    drawable/ic_launcher_<name>_background.xml   the background layer (VectorDrawable)
    drawable/ic_launcher_<name>_foreground.xml   the foreground layer
    drawable/ic_launcher_<name>_monochrome.xml   a one-colour foreground for themed icons
    mipmap-anydpi/ic_launcher_<name>.xml         the <adaptive-icon> (also used as roundIcon)
for <name> = porcelain (the default), pixelgraph, core.

All layers are drawn on the 108x108 adaptive-icon grid: launchers show the middle 72 (18..90)
through their mask, and only the circle of radius 33 around the centre is always visible.
Palette = CG50 Studio's tokens (app/frontend/css/app.css). The designs were made as SVG first
(Porcelain, Pixel graph, Core); the drawing code below is that code, kept shape for shape and
colour for colour, with each shape turned into VectorDrawable path data (rounded rects and
circles as arcs, gradients as aapt:attr <gradient> in viewport coordinates).

--preview DIR also writes DIR/<name>.svg (both layers) and DIR/<name>_mono.svg from the same
path data, to compare against the original designs in a browser.
"""
import argparse
import math
import os

HERE = os.path.dirname(os.path.abspath(__file__))
RES = os.path.join(HERE, "..", "app", "src", "main", "res")

ROOM, ROOM_DEEP, ROOM_GLOW = "#162C31", "#0D1C20", "#21434A"
BODY, BODY_LOW, EDGE = "#EDE9E1", "#D9D2C6", "#BDB5A7"
GLASS, GLASS_HI = "#161B22", "#1F2731"
GRID = "#2C3F46"
ACCENT, ACCENT_DEEP = "#F2A868", "#D9824A"
AQUA, AMBER = "#74D3C2", "#ECC46B"
KEY, KEY_LIGHT, INK = "#3F4450", "#FAF8F3", "#2F3440"

MONO = "#FFFFFF"     # the monochrome layer: only its alpha counts (the launcher tints it)
MONO_DIM = 0.35      # alpha of the axes in the monochrome layer (they are dim in colour too)
MONO_SCALE = 0.7     # the monochrome glyph, scaled about the centre to the size of the system's
                     # themed icons (the colour designs fill most of the visible 72)


# ---------------- geometry → path data ----------------

def n(v):
    """a number for path data: at most 3 decimals, no trailing zeros"""
    s = f"{v:.3f}".rstrip("0").rstrip(".")
    return "0" if s in ("-0", "") else s


def rrect(x, y, w, h, r=0.0):
    """an SVG <rect x y width height rx> as path data (rx clamped as SVG does)"""
    r = min(r, w / 2, h / 2)
    if r <= 0:
        return f"M{n(x)},{n(y)}H{n(x + w)}V{n(y + h)}H{n(x)}Z"
    a = f"A{n(r)},{n(r)} 0 0 1 "
    return (f"M{n(x + r)},{n(y)}H{n(x + w - r)}{a}{n(x + w)},{n(y + r)}"
            f"V{n(y + h - r)}{a}{n(x + w - r)},{n(y + h)}"
            f"H{n(x + r)}{a}{n(x)},{n(y + h - r)}"
            f"V{n(y + r)}{a}{n(x + r)},{n(y)}Z")


def circle(cx, cy, r):
    a = f"A{n(r)},{n(r)} 0 1 0 "
    return f"M{n(cx - r)},{n(cy)}{a}{n(cx + r)},{n(cy)}{a}{n(cx - r)},{n(cy)}Z"


def curve(fn, x0, x1, count=80):
    pts = []
    for i in range(count + 1):
        x = x0 + (x1 - x0) * i / count
        pts.append(f"{x:.2f},{fn(x):.2f}")
    return "M" + " L".join(pts)


class Shape:
    """one VectorDrawable <path>: fill (a colour or a gradient dict) and/or stroke"""

    def __init__(self, d, fill=None, fill_alpha=1.0, stroke=None, stroke_width=0.0,
                 stroke_alpha=1.0, cap=None, join=None, even_odd=False):
        self.d, self.fill, self.fill_alpha = d, fill, fill_alpha
        self.stroke, self.stroke_width, self.stroke_alpha = stroke, stroke_width, stroke_alpha
        self.cap, self.join, self.even_odd = cap, join, even_odd

    def style(self):
        return (repr(self.fill), self.fill_alpha, self.stroke, self.stroke_width, self.stroke_alpha,
                self.cap, self.join, self.even_odd)


def merged(shapes):
    """join consecutive shapes of the same plain style into one path (fewer paths, same picture:
    the shapes merged this way never overlap)"""
    out = []
    for s in shapes:
        p = out[-1] if out else None
        if p and not isinstance(s.fill, dict) and p.style() == s.style():
            p.d += s.d
        else:
            out.append(Shape(**vars(s)))
    return out


def radial(cx, cy, r, c0, c1):
    return {"type": "radial", "centerX": cx, "centerY": cy, "gradientRadius": r, "startColor": c0, "endColor": c1}


def linear(x0, y0, x1, y1, c0, c1):
    return {"type": "linear", "startX": x0, "startY": y0, "endX": x1, "endY": y1, "startColor": c0, "endColor": c1}


def bbox_linear(bx, by, bw, bh, u1, v1, c0, c1):
    """SVG linearGradient x1=0 y1=0 x2=u1 y2=v1 in objectBoundingBox units on the box (bx,by,bw,bh)
    as the equivalent gradient in viewport coordinates. In box units the colour depends on
    t = (u*u1 + v*v1) / (u1² + v1²), which is linear in x and y: t = a(x-bx) + b(y-by); a
    viewport gradient from (bx,by) along (a,b) to where t = 1 gives exactly the same colours."""
    k = u1 * u1 + v1 * v1
    a, b = u1 / bw / k, v1 / bh / k
    m = a * a + b * b
    return linear(bx, by, bx + a / m, by + b / m, c0, c1)


def bg_room(cx=0.5, cy=0.42):
    """the room behind the calculator: SVG radialGradient cx cy r=0.75 over the 108 square"""
    return [Shape(rrect(0, 0, 108, 108), fill=radial(108 * cx, 108 * cy, 0.75 * 108, ROOM_GLOW, ROOM_DEEP))]


# ---------------- the three designs ----------------
# Each returns (background shapes, foreground shapes, monochrome shapes or None).

def porcelain():
    """Porcelain: the desktop icon's calculator, its screen showing a graph; EXE in apricot."""
    bg = bg_room()
    fg = []
    # shadow, body, bottom edge
    fg.append(Shape(rrect(36, 25.5, 38, 61, 7.5), fill="#000000", fill_alpha=0.28))
    fg.append(Shape(rrect(35, 23, 38, 61, 7.5), fill=EDGE))
    fg.append(Shape(rrect(35, 23, 38, 59.6, 7.5), fill=bbox_linear(35, 23, 38, 59.6, 0.35, 1, BODY, BODY_LOW)))
    # screen
    fg.append(Shape(rrect(39, 27.5, 30, 21, 2.6), fill=GLASS))
    axes = "M41,38 H67 M54,29.5 V46.5"
    sine = curve(lambda x: 38 - 6.2 * math.sin((x - 41) / 26 * 2 * math.pi), 41, 67)
    parab = curve(lambda x: 45.5 - 0.06 * (x - 54) ** 2, 42.5, 65.5)
    fg.append(Shape(axes, stroke=GRID, stroke_width=0.7))
    fg.append(Shape(sine, stroke=ACCENT, stroke_width=1.9, cap="round", join="round"))
    fg.append(Shape(parab, stroke=AQUA, stroke_width=1.1, cap="round", stroke_alpha=0.9))
    # F-key row, then 4x3 keys, EXE apricot
    keys = []
    for i in range(4):
        keys.append(rrect(39.5 + i * 7.6, 52.5, 5.6, 2.6, 1.1))
        fg.append(Shape(keys[-1], fill=KEY))
    for r in range(3):
        for c in range(4):
            fill = ACCENT if (r, c) == (2, 3) else (KEY if r == 0 else KEY_LIGHT)
            keys.append(rrect(39.5 + c * 7.6, 58.5 + r * 6.6, 5.6, 4.3, 1.3))
            if fill == KEY_LIGHT:
                fg.append(Shape(keys[-1], fill=fill, stroke=EDGE, stroke_width=0.5))
            else:
                fg.append(Shape(keys[-1], fill=fill))
    # monochrome: the body with the screen and the keys cut out, the graph on the screen
    mono = [Shape(rrect(35, 23, 38, 61, 7.5) + rrect(39, 27.5, 30, 21, 2.6) + "".join(keys),
                  fill=MONO, even_odd=True),
            Shape(axes, stroke=MONO, stroke_width=0.7, stroke_alpha=MONO_DIM),
            Shape(sine, stroke=MONO, stroke_width=1.9, cap="round", join="round"),
            Shape(parab, stroke=MONO, stroke_width=1.1, cap="round", stroke_alpha=0.9)]
    return bg, fg, mono


def pixelgraph():
    """Pixel graph: the icon is the colour screen itself; two curves drawn as lit pixels."""
    bg = [Shape(rrect(0, 0, 108, 108), fill=linear(0, 0, 0, 108, GLASS_HI, GLASS))]
    cell, x0, y0, cnt = 4.0, 20.0, 20.0, 17           # a 17x17 grid of 4-unit pixels over the visible area
    lit = {}
    for c in range(cnt):
        lit[(c, 8)] = GRID                            # x axis
        lit[(8, c)] = GRID                            # y axis
    for c in range(cnt):                              # parabola (aqua), then sine (apricot) on top
        t = (c - 8) / 8
        r = round(14 - 10 * (1 - t * t))
        if 0 <= r < cnt:
            lit[(c, r)] = AQUA
    prev = None
    for c in range(cnt):
        r = round(8 - 5.5 * math.sin(c / (cnt - 1) * 2 * math.pi))
        rows = [r] if prev is None else range(min(prev, r), max(prev, r) + 1)
        for rr in rows:                               # join steep steps so the line stays connected
            lit[(c, rr)] = ACCENT
        prev = r
    # the pixels never overlap, so one path per colour draws the same picture
    by_colour = {}
    for (c, r), col in lit.items():
        by_colour.setdefault(col, []).append(
            rrect(x0 + c * cell + 0.45, y0 + r * cell + 0.45, cell - 0.9, cell - 0.9, 0.7))
    fg = [Shape("".join(by_colour[col]), fill=col) for col in (GRID, AQUA, ACCENT) if col in by_colour]
    mono = [Shape("".join(by_colour[GRID]), fill=MONO, fill_alpha=MONO_DIM),
            Shape("".join(by_colour[AQUA]), fill=MONO, fill_alpha=0.6),   # tells the two curves apart
            Shape("".join(by_colour[ACCENT]), fill=MONO)]
    return bg, fg, mono


def core():
    """Core: the SH-4 package with its pins, a tiny graph on the die: the calculator, emulated."""
    bg = bg_room()
    fg, pins = [], []
    for i in range(6):
        p = 34.5 + i * 7.8
        for x, y, w, h in ((p, 24.5, 3.4, 6), (p, 77.5, 3.4, 6), (24.5, p, 6, 3.4), (77.5, p, 6, 3.4)):
            pins.append(rrect(round(x, 1), round(y, 1), w, h, 1.2))
            fg.append(Shape(pins[-1], fill=EDGE))
    fg.append(Shape(rrect(29, 29, 50, 50, 7), fill=BODY))
    fg.append(Shape(rrect(29, 74.5, 50, 4.5, 2), fill=BODY_LOW))
    fg.append(Shape(circle(35.5, 35.5, 1.8), fill=BODY_LOW))
    fg.append(Shape(rrect(37, 39, 34, 27, 3), fill=GLASS))
    axis = "M39.5,52.5 H68.5"
    sine = curve(lambda x: 52.5 - 8 * math.sin((x - 39.5) / 29 * 2 * math.pi), 39.5, 68.5)
    fg.append(Shape(axis, stroke=GRID, stroke_width=0.7))
    fg.append(Shape(sine, stroke=ACCENT, stroke_width=2.1, cap="round", join="round"))
    # monochrome: pins, the package with the die and the pin-1 dot cut out, the graph on the die
    mono = [Shape("".join(pins), fill=MONO),
            Shape(rrect(29, 29, 50, 50, 7) + rrect(37, 39, 34, 27, 3) + circle(35.5, 35.5, 1.8),
                  fill=MONO, even_odd=True),
            Shape(axis, stroke=MONO, stroke_width=0.7, stroke_alpha=MONO_DIM),
            Shape(sine, stroke=MONO, stroke_width=2.1, cap="round", join="round")]
    return bg, fg, mono


ICONS = [("porcelain", "Porcelain", porcelain),
         ("pixelgraph", "Pixel graph", pixelgraph),
         ("core", "Core", core)]


# ---------------- output ----------------

def argb(col, alpha=1.0):
    return f"#{round(alpha * 255):02X}{col[1:].upper()}" if alpha < 1 else col.upper()


def vector_xml(shapes, comment, scale=1.0):
    shapes = merged(shapes)
    grad = any(isinstance(s.fill, dict) for s in shapes)
    out = ['<?xml version="1.0" encoding="utf-8"?>',
           f"<!-- {comment} Generated by android/tools/make_launcher_icons.py: edit that, not this. -->",
           '<vector xmlns:android="http://schemas.android.com/apk/res/android"']
    if grad:
        out.append('    xmlns:aapt="http://schemas.android.com/aapt"')
    out += ['    android:width="108dp"', '    android:height="108dp"',
            '    android:viewportWidth="108"', '    android:viewportHeight="108">']
    if scale != 1:
        out.append(f'    <group android:pivotX="54" android:pivotY="54" android:scaleX="{n(scale)}" android:scaleY="{n(scale)}">')
    for s in shapes:
        attrs = [f'android:pathData="{s.d}"']
        if s.fill is not None and not isinstance(s.fill, dict):
            attrs.append(f'android:fillColor="{s.fill.upper()}"')
        if s.fill is not None and s.fill_alpha < 1:
            attrs.append(f'android:fillAlpha="{n(s.fill_alpha)}"')
        if s.even_odd:
            attrs.append('android:fillType="evenOdd"')
        if s.stroke:
            attrs.append(f'android:strokeColor="{s.stroke.upper()}"')
            attrs.append(f'android:strokeWidth="{n(s.stroke_width)}"')
            if s.stroke_alpha < 1:
                attrs.append(f'android:strokeAlpha="{n(s.stroke_alpha)}"')
            if s.cap:
                attrs.append(f'android:strokeLineCap="{s.cap}"')
            if s.join:
                attrs.append(f'android:strokeLineJoin="{s.join}"')
        sep = "\n        "
        if isinstance(s.fill, dict):
            g = s.fill
            gattrs = (sep + "        ").join([f'android:{k}="{n(v) if isinstance(v, float) else (v.upper() if k.endswith("Color") else v)}"'
                               for k, v in g.items()])
            out.append(f"    <path{sep}{sep.join(attrs)}>")
            out.append('        <aapt:attr name="android:fillColor">')
            out.append(f"            <gradient{sep}        {gattrs} />")
            out.append("        </aapt:attr>")
            out.append("    </path>")
        else:
            out.append(f"    <path{sep}{sep.join(attrs)} />")
    if scale != 1:
        k = next(i for i, line in enumerate(out) if line.startswith("    <group"))
        out[k + 1:] = ["    " + line for line in "\n".join(out[k + 1:]).split("\n")]
        out.append("    </group>")
    out.append("</vector>")
    return "\n".join(out) + "\n"


def adaptive_xml(name, title, mono):
    lines = ['<?xml version="1.0" encoding="utf-8"?>',
             f"<!-- Launcher icon \"{title}\". Generated by android/tools/make_launcher_icons.py. -->",
             '<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">',
             f'    <background android:drawable="@drawable/ic_launcher_{name}_background" />',
             f'    <foreground android:drawable="@drawable/ic_launcher_{name}_foreground" />']
    if mono:
        lines.append(f'    <monochrome android:drawable="@drawable/ic_launcher_{name}_monochrome" />')
    lines.append("</adaptive-icon>")
    return "\n".join(lines) + "\n"


def svg_preview(layers, defs_id, scale=1.0):
    """the same path data as SVG (gradients in user space, as a VectorDrawable draws them)"""
    defs, body, k = [], [], 0
    for s in merged(layers):
        a = [f'd="{s.d}"']
        if isinstance(s.fill, dict):
            k += 1
            g, gid = s.fill, f"{defs_id}{k}"
            stops = f'<stop offset="0" stop-color="{g["startColor"]}"/><stop offset="1" stop-color="{g["endColor"]}"/>'
            if g["type"] == "radial":
                defs.append(f'<radialGradient id="{gid}" gradientUnits="userSpaceOnUse" cx="{g["centerX"]}" '
                            f'cy="{g["centerY"]}" r="{g["gradientRadius"]}">{stops}</radialGradient>')
            else:
                defs.append(f'<linearGradient id="{gid}" gradientUnits="userSpaceOnUse" x1="{g["startX"]}" '
                            f'y1="{g["startY"]}" x2="{g["endX"]}" y2="{g["endY"]}">{stops}</linearGradient>')
            a.append(f'fill="url(#{gid})"')
        else:
            a.append(f'fill="{s.fill or "none"}"')
        if s.fill is not None and s.fill_alpha < 1:
            a.append(f'fill-opacity="{s.fill_alpha}"')
        if s.even_odd:
            a.append('fill-rule="evenodd"')
        if s.stroke:
            a.append(f'stroke="{s.stroke}" stroke-width="{s.stroke_width}"')
            if s.stroke_alpha < 1:
                a.append(f'stroke-opacity="{s.stroke_alpha}"')
            if s.cap:
                a.append(f'stroke-linecap="{s.cap}"')
            if s.join:
                a.append(f'stroke-linejoin="{s.join}"')
        body.append(f'<path {" ".join(a)}/>')
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 108 108"><defs>{"".join(defs)}</defs>'
            f'<g transform="translate(54 54) scale({scale}) translate(-54 -54)">{"".join(body)}</g></svg>')


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--preview", metavar="DIR", help="also write SVG previews of the generated paths")
    args = ap.parse_args()
    for name, title, fn in ICONS:
        bg, fg, mono = fn()
        d = os.path.join(RES, "drawable")
        write(os.path.join(d, f"ic_launcher_{name}_background.xml"), vector_xml(bg, f'Launcher icon "{title}": background layer.'))
        write(os.path.join(d, f"ic_launcher_{name}_foreground.xml"), vector_xml(fg, f'Launcher icon "{title}": foreground layer.'))
        if mono:
            write(os.path.join(d, f"ic_launcher_{name}_monochrome.xml"),
                  vector_xml(mono, f'Launcher icon "{title}": monochrome layer (themed icons; only alpha counts).', MONO_SCALE))
        write(os.path.join(RES, "mipmap-anydpi", f"ic_launcher_{name}.xml"), adaptive_xml(name, title, mono))
        if args.preview:
            write(os.path.join(args.preview, f"{name}.svg"), svg_preview(bg + fg, "g"))
            if mono:
                write(os.path.join(args.preview, f"{name}_mono.svg"), svg_preview(mono, "m", MONO_SCALE))
        print(f"{name}: {len(merged(bg))} + {len(merged(fg))} paths" + (f", monochrome {len(merged(mono))}" if mono else ""))


if __name__ == "__main__":
    main()
