"""Render a synthetic receipt image for end-to-end tests.

Usage (inside the ocr container, which has Pillow):
    python tools/fake_receipt.py < spec.txt > receipt.jpg

spec.txt: one receipt row per line; "left|right" puts `right` in the price
column. Optional first line "#noise" adds slight rotation and blur, closer
to a phone scan.
"""

import io
import random
import sys

from PIL import Image, ImageDraw, ImageFilter, ImageFont

rows = sys.stdin.read().splitlines()
noisy = bool(rows) and rows[0].strip() == "#noise"
if noisy:
    rows = rows[1:]

font = ImageFont.load_default(size=30)
width, line_h = 820, 46
img = Image.new("RGB", (width, 80 + line_h * len(rows)), "white")
draw = ImageDraw.Draw(img)
for i, row in enumerate(rows):
    left, _, right = row.partition("|")
    y = 40 + line_h * i
    draw.text((40, y), left, fill="black", font=font)
    if right:
        w = draw.textlength(right, font=font)
        draw.text((width - 60 - w, y), right, fill="black", font=font)

if noisy:
    rnd = random.Random(len(rows))
    img = img.rotate(rnd.uniform(-1.2, 1.2), expand=True, fillcolor="white")
    img = img.filter(ImageFilter.GaussianBlur(0.6))

buf = io.BytesIO()
img.save(buf, "JPEG", quality=85)
sys.stdout.buffer.write(buf.getvalue())
