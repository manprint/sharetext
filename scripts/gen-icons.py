#!/usr/bin/env python3
"""Regenerate PWA PNG icons from the maskable SVG design.

Source-of-truth design lives in cmd/server/static/icon-maskable.svg; this script
draws the equivalent raster icons (192/512 with rounded background for Android
"any" purpose, 180 with a flat background for apple-touch-icon since iOS
masks corners itself).

Requires: python3, Pillow.
"""
from PIL import Image, ImageDraw

TEAL = (15, 118, 110, 255)
WHITE = (255, 255, 255, 255)
OUTDIR = "cmd/server/static"


def make_icon(size: int, rounded: bool) -> Image.Image:
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    if rounded:
        d.rounded_rectangle((0, 0, size - 1, size - 1), radius=int(size * 0.21), fill=TEAL)
    else:
        d.rectangle((0, 0, size - 1, size - 1), fill=TEAL)
    inset = int(size * 0.20)
    d.rounded_rectangle(
        (inset, inset, size - 1 - inset, size - 1 - inset),
        radius=max(2, int(size * 0.063)), fill=WHITE,
    )
    lx = int(size * 0.27)
    lh = max(2, int(size * 0.047))
    ly = int(size * 0.35)
    lg = int(size * 0.11)
    for k, w in enumerate([0.46, 0.36, 0.40]):
        y = ly + k * lg
        d.rounded_rectangle((lx, y, lx + int(size * w), y + lh),
                            radius=max(1, lh // 2), fill=TEAL)
    return img


for size, rounded, name in [(192, True, "icon-192.png"),
                            (512, True, "icon-512.png"),
                            (180, False, "icon-180.png")]:
    path = f"{OUTDIR}/{name}"
    make_icon(size, rounded).save(path, optimize=True)
    print(f"wrote {path}")
