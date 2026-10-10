"""Run inside an OCR image: python -m unittest test_tiling"""

import unittest

import tiling


class TilingTest(unittest.TestCase):
    def test_target_size(self):
        self.assertEqual(tiling.target_size(3000, 2000), (2400, 1600))  # normal: long side capped
        self.assertEqual(tiling.target_size(1600, 6000), (1600, 6000))  # tall: width kept
        self.assertEqual(tiling.target_size(3200, 12000), (1600, 6000))  # tall: width capped
        self.assertEqual(tiling.target_size(800, 1000), (800, 1000))  # never upscale

    def test_plan_covers_with_overlap(self):
        tiles = tiling.plan_tiles(1600, 6000)
        self.assertGreater(len(tiles), 1)
        self.assertEqual(tiles[0][0], 0)
        self.assertEqual(tiles[-1][1], 6000)
        for (a0, a1), (b0, b1) in zip(tiles, tiles[1:]):
            self.assertEqual(a1 - b0, tiling.OVERLAP)
        self.assertEqual(tiling.plan_tiles(1600, 2000), [(0, 2000)])

    def test_merge_keeps_each_line_once(self):
        tiles = tiling.plan_tiles(1000, 4000)  # tile 1500, step 1300
        # A line 40 px tall every 100 px; every tile reports the lines it fully contains.
        lines = [(f"line {y}", y) for y in range(20, 3960, 100)]
        per_tile = []
        for y0, y1 in tiles:
            per_tile.append([
                {"text": t, "conf": 1.0, "box": [10, y - y0, 300, y - y0 + 40]}
                for t, y in lines if y >= y0 and y + 40 <= y1
            ])
        merged = tiling.merge(tiles, per_tile)
        self.assertEqual([m["text"] for m in merged], [t for t, _ in lines])
        self.assertEqual(merged[5]["box"][1], 520)  # back in image coordinates


if __name__ == "__main__":
    unittest.main()
