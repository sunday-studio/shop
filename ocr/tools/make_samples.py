"""Generate synthetic receipts with known answers, to exercise the spike harness.

    python tools/make_samples.py <out_dir> [count]

Writes <out_dir>/receipts/sample-NNN.jpg, <out_dir>/truth.csv and
<out_dir>/lines/sample-NNN.json. Damage increases with the index: rotation,
blur, faded contrast and speckle, imitating phone scans of thermal paper.
These are NOT real receipts; they only show whether the pipeline works and
roughly how engines degrade.
"""

import csv
import json
import os
import random
import sys

from PIL import Image, ImageDraw, ImageEnhance, ImageFilter, ImageFont

ITEMS = [
    ("AH HALFV MELK 1,5L", 129), ("VOLKOREN BROOD", 249), ("BANANEN", None), ("KIPFILET 500G", 549),
    ("HAVERDRANK BARISTA", 219), ("GOUDA JONG 48+", 425), ("APPELS ELSTAR", 279), ("PINDAKAAS", 239),
    ("EIEREN 10 ST", 329), ("TOMATEN", None), ("YOGHURT VOL 1L", 159), ("PASTA PENNE", 119),
    ("WC PAPIER 8 ROL", 449), ("KOFFIEBONEN", 899), ("SPINAZIE", 199), ("CREME FRAICHE", 139),
]
SHOPS = ["Albert Heijn", "Lidl", "Jumbo", "Aldi"]


def money(c):
    return ("-" if c < 0 else "") + f"{abs(c) // 100},{abs(c) % 100:02d}"


def receipt(rnd, i):
    shop = SHOPS[i % len(SHOPS)]
    rows, lines = [shop.upper(), f"Filiaal {rnd.randint(1000, 9999)}"], []
    for name, price in rnd.sample(ITEMS, rnd.randint(4, 9)):
        if price is None:  # weighed item
            kg = round(rnd.uniform(0.3, 1.6), 3)
            per = rnd.choice([199, 249, 349])
            total = round(kg * per)
            rows += [f"{name}|", f"{str(kg).replace('.', ',')} kg x {money(per)} EUR/kg|{money(total)} B"]
            lines.append({"text": name, "kind": "item", "qty": kg, "unit": "kg", "unit_price_cents": per, "total_cents": total})
            continue
        qty = rnd.choice([1, 1, 1, 2, 3])
        total = qty * price
        rows.append(f"{name}|{money(total)} B")
        if qty > 1:
            rows.append(f"{qty} x {money(price)}|")
        lines.append({"text": name, "kind": "item", "qty": qty, "unit": "pc", "unit_price_cents": price, "total_cents": total})
        if rnd.random() < 0.2:
            disc = -rnd.choice([25, 50, 100])
            rows.append(f"BONUS {name.split()[0]}|{money(disc)} B")
            lines.append({"text": f"BONUS {name.split()[0]}", "kind": "discount", "qty": 1, "unit": "pc", "total_cents": disc})
    if rnd.random() < 0.4:
        rows.append("STATIEGELD|0,15")
        lines.append({"text": "STATIEGELD", "kind": "deposit", "qty": 1, "unit": "pc", "unit_price_cents": 15, "total_cents": 15})
    total = sum(l["total_cents"] for l in lines)
    day, month = rnd.randint(1, 28), rnd.choice([7, 8, 9])  # past dates only
    rows += ["", f"TOTAAL|{money(total)}", f"PIN|{money(total)}", f"{day:02d}-{month:02d}-2026 {rnd.randint(8, 20):02d}:{rnd.randint(0, 59):02d}"]
    return shop, f"2026-{month:02d}-{day:02d}", total, lines, rows


def render(rows, damage, rnd):
    font = ImageFont.load_default(size=28)
    width, lh = 760, 42
    img = Image.new("L", (width, 60 + lh * len(rows)), 255)
    d = ImageDraw.Draw(img)
    for i, row in enumerate(rows):
        left, _, right = row.partition("|")
        y = 30 + lh * i
        d.text((30, y), left, fill=0, font=font)
        if right:
            d.text((width - 40 - d.textlength(right, font=font), y), right, fill=0, font=font)
    if damage > 0:
        img = ImageEnhance.Contrast(img).enhance(1 - 0.12 * damage)  # thermal fade
        img = img.rotate(rnd.uniform(-0.8, 0.8) * damage, expand=True, fillcolor=245)
        img = img.filter(ImageFilter.GaussianBlur(0.35 * damage))
        px = img.load()
        for _ in range(int(img.width * img.height * 0.002 * damage)):  # speckle
            x, y = rnd.randrange(img.width), rnd.randrange(img.height)
            px[x, y] = rnd.choice([0, 255])
    return img.convert("RGB")


def main():
    out = sys.argv[1]
    n = int(sys.argv[2]) if len(sys.argv) > 2 else 12
    rnd = random.Random(42)
    os.makedirs(f"{out}/receipts", exist_ok=True)
    os.makedirs(f"{out}/lines", exist_ok=True)
    with open(f"{out}/truth.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["id", "shop", "date", "total", "n_items", "notes"])
        for i in range(n):
            shop, date, total, lines, rows = receipt(rnd, i)
            damage = round(4 * i / max(n - 1, 1), 2)  # 0 (clean) … 4 (rough)
            sid = f"sample-{i + 1:03d}"
            render(rows, damage, rnd).save(f"{out}/receipts/{sid}.jpg", quality=80)
            items = sum(1 for l in lines if l["kind"] == "item")
            w.writerow([sid, shop, date, money(total), items, f"synthetic damage={damage}"])
            with open(f"{out}/lines/{sid}.json", "w") as lf:
                json.dump(lines, lf, indent=2)
    print(f"wrote {n} samples to {out}")


main()
