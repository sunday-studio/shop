"""Resizing and vertical tiling for tall receipts.

Multi-page scans arrive stitched into one tall image (e.g. 1600 × 6000).
Capping the long side would shrink such a receipt to ~640 px wide and make
the text unreadable, so tall images keep their width (capped at MAX_WIDTH)
and are recognised in overlapping vertical tiles. Each line is kept from
exactly one tile: the one where its centre falls outside the shared half
of the overlap. That works for any engine, because it only needs boxes.
"""

import os

MAX_SIDE = int(os.environ.get("OCR_MAX_SIDE", "2400"))     # normal images: cap the long side
MAX_WIDTH = int(os.environ.get("OCR_MAX_WIDTH", "1600"))   # tall images: cap the width only
TALL_RATIO = float(os.environ.get("OCR_TALL_RATIO", "2.0"))  # height / width that counts as tall
TILE_RATIO = float(os.environ.get("OCR_TILE_RATIO", "1.5"))  # tile height = width × this
OVERLAP = int(os.environ.get("OCR_TILE_OVERLAP", "200"))     # px shared by neighbouring tiles


def is_tall(width: int, height: int) -> bool:
    return width > 0 and height / width > TALL_RATIO


def target_size(width: int, height: int) -> tuple[int, int]:
    """Size to resize to before OCR (never upscales)."""
    if is_tall(width, height):
        scale = min(1.0, MAX_WIDTH / width)
    else:
        scale = min(1.0, MAX_SIDE / max(width, height))
    return max(1, round(width * scale)), max(1, round(height * scale))


def plan_tiles(width: int, height: int) -> list[tuple[int, int]]:
    """Vertical tiles (y0, y1) covering the image; one tile if not tall."""
    if not is_tall(width, height):
        return [(0, height)]
    tile_h = max(int(width * TILE_RATIO), OVERLAP * 3)
    step = tile_h - OVERLAP
    tiles, y0 = [], 0
    while True:
        y1 = min(y0 + tile_h, height)
        tiles.append((y0, y1))
        if y1 >= height:
            return tiles
        y0 += step


def merge(tiles: list[tuple[int, int]], per_tile: list[list[dict]]) -> list[dict]:
    """Shift each tile's boxes into image coordinates and drop duplicates
    from the overlaps: a line belongs to the tile whose keep-band holds its
    vertical centre."""
    out = []
    last = len(tiles) - 1
    for i, ((y0, y1), lines) in enumerate(zip(tiles, per_tile)):
        keep_from = y0 + OVERLAP / 2 if i > 0 else float("-inf")
        keep_to = y1 - OVERLAP / 2 if i < last else float("inf")
        for line in lines:
            x0, by0, x1, by1 = line["box"]
            box = [x0, by0 + y0, x1, by1 + y0]
            yc = (box[1] + box[3]) / 2
            if keep_from <= yc < keep_to:
                out.append({**line, "box": box})
    out.sort(key=lambda l: ((l["box"][1] + l["box"][3]) / 2, l["box"][0]))
    return out
