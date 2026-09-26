"""Generate the 92x64 unselected/selected icons a .g3a needs (fxsdk generate_g3a)."""
from PIL import Image, ImageDraw
import os
d = os.path.join(os.path.dirname(__file__), "assets-cg")
os.makedirs(d, exist_ok=True)
for name, bg, fg in (("icon-uns.png", (40, 40, 40), (230, 230, 230)), ("icon-sel.png", (0, 90, 200), (255, 255, 255))):
    im = Image.new("RGB", (92, 64), bg)
    dr = ImageDraw.Draw(im)
    dr.rectangle((2, 2, 89, 61), outline=fg)
    dr.text((8, 14), "KEY", fill=fg)
    dr.text((8, 30), "PROBE", fill=fg)
    im.save(os.path.join(d, name))
    print("wrote", name)
