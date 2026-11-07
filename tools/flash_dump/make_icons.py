"""Draw the dumper's two 92x64 MAIN MENU icons (unselected, selected) into assets-cg/.

A flash chip with an arrow leaving it. Colours: a warm paper ground with a deep teal chip
(unselected); the selected icon inverts to a teal ground with the paper colour, and a
terracotta arrow on both, its complement, so the arrow reads as "out of the chip".

  python tools/flash_dump/make_icons.py
"""
import os

from PIL import Image, ImageDraw

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "assets-cg")

PAPER = (244, 236, 220)
TEAL = (22, 96, 104)
TERRA = (200, 92, 52)


def icon(bg, fg, arrow):
    im = Image.new("RGB", (92, 64), bg)
    d = ImageDraw.Draw(im)
    # chip body with pins
    x0, y0, x1, y1 = 14, 14, 50, 50
    d.rounded_rectangle((x0, y0, x1, y1), radius=4, fill=fg)
    for i in range(4):
        p = y0 + 5 + i * 8
        d.rectangle((x0 - 6, p, x0 - 1, p + 3), fill=fg)
        d.rectangle((x1 + 1, p, x1 + 6, p + 3), fill=fg)
    d.ellipse((x0 + 4, y0 + 4, x0 + 9, y0 + 9), fill=bg)
    # arrow out of the chip, to the right
    d.rectangle((58, 29, 74, 35), fill=arrow)
    d.polygon([(72, 22), (86, 32), (72, 42)], fill=arrow)
    return im


if __name__ == "__main__":
    os.makedirs(OUT, exist_ok=True)
    icon(PAPER, TEAL, TERRA).save(os.path.join(OUT, "icon-uns.png"))
    icon(TEAL, PAPER, TERRA).save(os.path.join(OUT, "icon-sel.png"))
    print("wrote", OUT)
