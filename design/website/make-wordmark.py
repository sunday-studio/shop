#!/usr/bin/env python3
"""Outline the product name into SVG paths (Inter SemiBold, SIL OFL), so no font is needed to show it.

    PYTHONPATH=<fonttools> python3 make-wordmark.py "Shop"

Writes wordmark.svg (text in currentColor) and logo-lockup.svg (badge + wordmark). Change the name
here, rerun, and every asset follows. No kerning is applied (fine for a short name; check the result).
Needs fontTools and brotli (pip install fonttools brotli)."""
import sys, os
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen

name = sys.argv[1] if len(sys.argv) > 1 else "Shop"
here = os.path.dirname(os.path.abspath(__file__))
font = TTFont(os.path.join(here, "fonts", "Inter-SemiBold.woff2"))
upm = font["head"].unitsPerEm
cmap = font.getBestCmap(); gs = font.getGlyphSet(); hmtx = font["hmtx"]
size = 100.0                      # em in SVG units
track = -0.03 * size              # -0.03em, like the app's headlines
scale = size / upm
x = 0.0
paths = []
for ch in name:
    g = cmap[ord(ch)]
    pen = SVGPathPen(gs)
    # flip y: font units go up, SVG goes down; baseline at y = 0.8 em
    gs[g].draw(TransformPen(pen, (scale, 0, 0, -scale, x, 80)))
    d = pen.getCommands()
    if d: paths.append(d)
    x += hmtx[g][0] * scale + track
width = x - track
d = " ".join(paths)
open(os.path.join(here, "wordmark.svg"), "w").write(
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width:.1f} 100" height="100" role="img" aria-label="{name}"><path fill="currentColor" d="{d}"/></svg>\n')
badge = open(os.path.join(here, "logo-badge.svg")).read()
inner = badge[badge.index("<defs>"):badge.rindex("</svg>")]
bw = 100 * 0.9
open(os.path.join(here, "logo-lockup.svg"), "w").write(
    f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {bw + 24 + width:.1f} 100" height="100" role="img" aria-label="{name}">'
    f'<g transform="translate(0 5) scale({bw/512:.5f})">{inner}</g>'
    f'<g transform="translate({bw + 24:.1f} 10) scale(0.9)"><path fill="currentColor" transform="translate(0 0)" d="{d}"/></g></svg>\n')
print("wrote wordmark.svg and logo-lockup.svg for", repr(name), f"({width:.0f} wide)")
