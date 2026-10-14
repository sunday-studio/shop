# Draws a small, invented Dutch-style receipt as a PNG on stdout. Run inside the OCR image, which
# has Pillow:  docker run --rm -i shop-ocr:ppocrv5 python - < mkreceipt.py > receipt.png
import sys
from PIL import Image, ImageDraw, ImageFont

lines = [
    "TESTWINKEL AMSTERDAM", "", "Kassabon 000123", "",
    "MELK HALFVOL 1L        1,29", "BROOD VOLKOREN         2,49",
    "BANANEN                1,99", "KAAS JONG BELEGEN       4,59", "",
    "TOTAAL                10,36", "PINNEN                10,36", "", "BTW 9%                 0,86",
]
f = ImageFont.load_default(size=30)
img = Image.new("RGB", (760, 40 + 46 * len(lines)), "white")
d = ImageDraw.Draw(img)
for i, t in enumerate(lines):
    d.text((30, 20 + 46 * i), t, fill="black", font=f)
img.save(sys.stdout.buffer, "PNG")
