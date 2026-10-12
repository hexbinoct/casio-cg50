"""Draws CG50 Studio's icon (build/appicon.png, 1024x1024) in the app's palette: a porcelain
calculator with a dark screen and an apricot dot on the deep blue-green ground. Wails makes
the Windows .ico and the macOS icon set from it (delete build/windows/icon.ico to refresh).

    python app/build/make_icon.py
"""
import os

from PIL import Image, ImageDraw, ImageFilter

S = 1024
HERE = os.path.dirname(os.path.abspath(__file__))

# sRGB equivalents of the CSS tokens in frontend/css/app.css
ROOM = (22, 44, 49)          # --room-glow / --room
ROOM_DEEP = (14, 30, 34)
BODY = (237, 233, 225)       # --body
BODY_LOW = (219, 213, 202)   # --body-low
EDGE = (196, 189, 176)       # --body-edge
GLASS = (24, 28, 36)         # --glass
SCREEN = (60, 86, 92)        # a lit screen, blue-green
ACCENT = (242, 168, 104)     # --accent
KEY = (63, 68, 80)           # --key-dark
KEY_LIGHT = (250, 248, 243)  # --key-light


def main():
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # ground: a rounded square with a soft vertical gradient
    ground = Image.new("RGBA", (S, S))
    gd = ImageDraw.Draw(ground)
    for y in range(S):
        t = y / S
        c = tuple(int(ROOM[i] * (1 - t) + ROOM_DEEP[i] * t) for i in range(3))
        gd.line([(0, y), (S, y)], fill=c + (255,))
    mask = Image.new("L", (S, S), 0)
    ImageDraw.Draw(mask).rounded_rectangle([40, 40, S - 40, S - 40], radius=210, fill=255)
    img.paste(ground, (0, 0), mask)

    # the calculator's shadow
    sh = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    ImageDraw.Draw(sh).rounded_rectangle([270, 200, 754, 880], radius=80, fill=(0, 0, 0, 120))
    sh = sh.filter(ImageFilter.GaussianBlur(28))
    img.alpha_composite(sh, (0, 18))

    # body
    d.rounded_rectangle([262, 172, 762, 862], radius=78, fill=EDGE)
    d.rounded_rectangle([262, 166, 762, 846], radius=78, fill=BODY)
    d.rounded_rectangle([262, 520, 762, 846], radius=78, fill=BODY_LOW)
    d.rectangle([262, 520, 762, 600], fill=BODY_LOW)
    d.rectangle([262, 500, 762, 540], fill=BODY)

    # screen
    d.rounded_rectangle([312, 222, 712, 470], radius=34, fill=GLASS)
    d.rounded_rectangle([340, 250, 684, 442], radius=14, fill=SCREEN)
    # a few "pixels" of a menu on it
    for i, x in enumerate((372, 466, 560)):
        d.rounded_rectangle([x, 284, x + 70, 344], radius=10, fill=(120, 170, 170) if i != 1 else ACCENT)
    d.rounded_rectangle([372, 370, 630, 392], radius=8, fill=(110, 150, 150))

    # keys: two rows of dark keys, then light keys
    for row, y in enumerate((548, 612)):
        for col in range(4):
            x = 326 + col * 98
            d.rounded_rectangle([x, y, x + 78, y + 40], radius=12, fill=KEY)
    for row, y in enumerate((690, 764)):
        for col in range(4):
            x = 326 + col * 98
            d.rounded_rectangle([x, y, x + 78, y + 48], radius=13, fill=KEY_LIGHT, outline=EDGE, width=3)

    # the apricot dot, under the screen
    d.ellipse([666, 484, 702, 520], fill=ACCENT)

    img.save(os.path.join(HERE, "appicon.png"))
    print("wrote", os.path.join(HERE, "appicon.png"))


if __name__ == "__main__":
    main()
