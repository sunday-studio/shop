# Running the spike

The spike answers two questions with numbers from **your** receipts before any app work:

1. Which OCR engine reads Dutch/English thermal receipts best within CPU budget?
2. Does the deterministic parser get the totals right, and does validation catch it when it doesn't?

Everything runs in Docker on CPU, the same as production.

## 1. Collect and scan (you)

- Gather **100 paper receipts** from every shop you use, including long, crumpled and faded ones, ones with discounts, and ones with weighed items.
- Scan them with the iPhone document scanner (Notes → Scan Documents, or Files → Scan Documents). It crops and straightens, which matches what the app will do.
- Export the JPEGs into `spike/receipts/`. Subfolders per shop are optional but help: `spike/receipts/ah/001.jpg`. The folder name is used as the shop prefill when labelling.

`spike/receipts/` and the OCR cache are git-ignored.

## 2. Run the engines

```bash
make spike-up            # start the engine containers (first build takes a while)
make spike-ocr           # OCR every image with every engine; results are cached
```

| Engine | Port | Notes |
|---|---|---|
| `ppocrv5` | 8101 | PP-OCRv5 mobile det + latin rec. Current default |
| `ppocrv5-longside` | 8106 | Same engine with the old resize (no tiling). Shows whether tiling tall stitched receipts helps |
| `ppocrv5-server` | 8102 | Server-size det + rec |
| `tesseract` | 8103 | Tesseract 5, `nld+eng`, baseline |
| `doctr` | 8104 | docTR (PyTorch CPU) |
| `paddleocr-vl` | 8105 | 0.9B vision-language OCR. About 1 min per receipt on CPU and about 5 GB RAM. Started on its own because of its memory needs |

`make spike-ocr` skips engines that aren't running. To run PaddleOCR-VL:

```bash
docker compose -f deploy/docker-compose.spike.yml stop ppocrv5 ppocrv5-server tesseract doctr
docker compose -f deploy/docker-compose.spike.yml up -d paddleocr-vl
cd api && go run ./cmd/shop-spike ocr -engines paddleocr-vl     # ~100 min for 100 receipts
```

Engines are defined in `spike/engines.json`. Each one is any HTTP service implementing the OCR contract (`POST /ocr`, `GET /healthz`).

## 3. Label (you, ~1 hour)

```bash
make spike-label         # → http://127.0.0.1:8090
```

The page shows the receipt next to a form **prefilled from the parser**, so most receipts only need checking:

- **For all 100:** shop, date, total and number of item lines. ⌘/Ctrl+Enter saves and jumps to the next unlabelled receipt.
- **For ~25, spread across shops:** tick "Label every line too" and correct the lines. The page shows Σ lines vs the total and won't save until they agree.

Labels are written to `spike/truth.csv` and `spike/lines/<id>.json`. The line-labelled receipts become the parser's regression tests later.

## 4. Score

```bash
make spike-score         # → docs/spike-report.md + spike/results.csv
```

The report contains:
- a per-engine table against the targets (total correct ≥ 85 %, Σ lines = total ≥ 80 %, date ≥ 95 %, line F1 ≥ 0.85, time and RAM budgets);
- the recommended default and fallback engine;
- breakdowns per shop and per review reason;
- **silent errors**: receipts that passed every check but have the wrong total. These matter most; the target is 0;
- the list of wrong totals to work through.

## 5. Improve the parser, re-score

```bash
cd api && go run ./cmd/shop-spike show ah/017 -engine ppocrv5
```

`show` prints the rows rebuilt from OCR boxes and what the parser made of them. Fix the rule in `api/internal/receipt`, add a test, bump `ParserVersion`, then run `make spike-score` again. OCR is cached, so re-scoring takes seconds.

Iterate on parser rules first; switch engines second. If the targets can't be reached that way, decide then. The plan rules out an LLM.

## Dry run on generated receipts (2026-10-10)

Before real receipts existed, the harness was run on **16 generated receipts** (`ocr/tools/make_samples.py`) with increasing blur, tilt, fading and speckle. They are clean digital fonts, so this is **not** evidence about real thermal paper. It only proves the pipeline works and surfaced parser bugs that were then fixed:

- a stray `.` around prices and VAT codes (`17,98 B.`, `2,79.B`, `.4,81`);
- `;` read instead of the decimal comma (`1;338 kg`);
- a date glued to its time (`07-10-202618:44`).

After those fixes (parser v2):

| Engine | Total correct | Σ lines = total | Date¹ | Auto-ok | Silent errors | Line F1 | Median time | Peak RAM |
|---|---|---|---|---|---|---|---|---|
| `ppocrv5` | 100 % | 94 % | 81 % | 75 % | 0 | 0.97 | 5.8 s | see report |
| `ppocrv5-server` | 100 % | 94 % | 88 % | 81 % | 0 | 0.78 | 23 s | see report |
| `tesseract` | 100 % | 69 % | 81 % | 44 % | 0 | 0.96 | 0.3 s | see report |
| `doctr` | 94 % | 62 % | 88 % | 56 % | 0 | 0.89 | 3.2 s | see report |
| `paddleocr-vl` | 69 % | 69 % | 88 % | 56 % | 0 | 0.74 | 78 s | 6.4 GB |

¹ Two of the 16 samples had future dates (a generator bug, since fixed), which the parser correctly rejects. Excluding them, date accuracy is 93–100 %.

What this suggests, before real data:

- **Recommendation on this set:** `ppocrv5` as default, `ppocrv5-server` as fallback. The server variant clears more receipts without review (81 % vs 75 %), but at about 23 s per receipt it's too slow to be the default.
- **Tesseract** gets totals right and is about 20× faster, but it misreads more lines, so about half its receipts would need review. It's a reasonable cheap baseline; on clean digital fonts it's not obviously worse. Real thermal paper is where it's expected to fall behind.
- **PaddleOCR-VL** reads text well but returns layout *blocks*, not lines. The adapter spreads block lines evenly over the block's height, which loses the price column, so its 69 % reflects the adapter more than the model. It's also about 78 s and 6.4 GB per receipt on CPU. Only worth more work if the line-based engines fail on real receipts.
- No engine produced a silent error (a wrong total that passed every check).
