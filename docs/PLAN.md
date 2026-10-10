# Pantry — End-to-End Build Plan

Self-hosted grocery receipt tracker. You photograph **paper receipts (NL/EN)**, they are OCR'd locally, and a deterministic parser turns them into line items stored in SQLite. You get spend and price-comparison reports, a product catalog with nutrition, and a shopping list. It is used from an **iOS app (Swift)** and by your personal assistant over **MCP (HTTP)**. The backend is **Go** in **Docker** on a home **Mac mini (16 GB)**, it must run CPU-only, and backups go to a **NAS**.

---

## 0. Locked decisions

| Area | Decision |
|---|---|
| Users | One user per install. Bearer tokens per client (phone, assistant). No accounts. |
| Input | Paper receipts only, captured with the iOS document scanner. |
| Models | **OCR only**, using small weights. No LLMs for structuring, categorising, or suggestions. |
| Parsing | Deterministic Go: row reconstruction from boxes, regex classification, and arithmetic validation. |
| Matching / search | **Fuzzy only**: normalisation + SQLite FTS5 trigram + Jaro-Winkler/token-set scoring in Go. No embeddings. |
| Shops | Plain CRUD. The user picks the shop for each receipt. A generic parser is used for everything; per-shop tweaks are added only if the data demands them. |
| Host | Mac mini 16 GB. Docker = CPU-only (the macOS VM has no Metal/ANE access). The same images run on any amd64/arm64 box. |
| Storage | SQLite (WAL) + image files on disk. |
| Backups | Nightly to the NAS: SQLite online backup + rsync of images, with 30-day rotation and a restore drill. |
| Remote | Tailscale (no port forwarding). |
| MCP | Streamable HTTP at `/mcp` in the Go binary, with bearer auth. |
| Mobile | SwiftUI, iOS 17+. |

---

## 1. System overview

```
┌──────────── iPhone (SwiftUI) ─────────────┐
│ Scan receipt → pick shop → upload queue   │
│ Inbox · Review/fix · Unpack (barcode)     │
│ Spend charts · Prices · Shopping list     │
└──────────────────┬────────────────────────┘
                   │ HTTPS over LAN / Tailscale, Bearer token
┌──────────────────▼──────────── Mac mini · docker compose ─────────┐
│  api  (Go, single binary)                                         │
│   ├─ REST  /v1/...            ├─ MCP  /mcp (streamable HTTP)      │
│   ├─ jobs worker (SQLite-backed queue)                            │
│   ├─ preprocess → OCR client → rows → parser → validator → match  │
│   ├─ Open Food Facts client (barcode → product/nutrition)         │
│   └─ /data/pantry.db (WAL) · /data/images/{receipts,products}     │
│                                                                   │
│  ocr  (Python FastAPI sidecar, CPU) — engine picked by env:       │
│        ppocrv5 (default) | doctr | tesseract | paddleocr-vl       │
│                                                                   │
│  backup (cron container) → NAS mount                              │
└───────────────────────────────────────────────────────────────────┘
        ▲ optional, outside Docker: ocr-mac (Swift, Apple Vision) on :8010
        ▲ assistant → http://pantry:8080/mcp
```

**OCR contract** (every engine returns the same thing):

```
POST /ocr   multipart: image
200 { "engine": "ppocrv5", "version": "...", "ms": 1840,
      "width": 1600, "height": 4200,
      "lines": [ { "text": "AH HALFV MELK", "conf": 0.97, "box": [x0,y0,x1,y1] } ] }
GET  /healthz → { "engine": "...", "ready": true }
```

---

## 2. Repository layout

```
pantry/
├─ api/                      Go module github.com/<you>/pantry
│  ├─ cmd/pantry/            main: `serve`, `migrate`, `import`, `reparse`, `token`, `backup`
│  ├─ internal/
│  │  ├─ config/             env parsing
│  │  ├─ store/              sqlc-generated queries + tx helpers
│  │  ├─ migrations/         goose SQL files (embedded)
│  │  ├─ httpapi/            handlers, middleware (auth, logging, recover)
│  │  ├─ mcpserver/          MCP tools on go-sdk
│  │  ├─ jobs/               queue + worker
│  │  ├─ imaging/            decode, orient, grayscale, resize
│  │  ├─ ocr/                OCR client (+ fallback)
│  │  ├─ receipt/            rows.go, classify.go, parse.go, validate.go, numbers.go, dates.go
│  │  ├─ match/              normalise.go, fuzzy.go, aliases.go
│  │  ├─ openfoodfacts/      Open Food Facts client
│  │  ├─ stats/              spend, price points, compare, changes
│  │  └─ money/              cents type + NL formatting
│  ├─ testdata/receipts/     regression corpus (OCR JSON + expected JSON)
│  └─ sqlc.yaml
├─ ocr/                      Python sidecar (FastAPI), Dockerfile per engine target
├─ ocr-mac/                  Swift package: Apple Vision HTTP helper (optional)
├─ ios/Pantry/               Xcode project
├─ spike/                    harness, truth.csv, lines/*.json (images git-ignored)
├─ docs/                     all documentation (PLAN.md, spike-report.md, mcp.md, restore.md)
├─ openapi.yaml              shared contract (Go server + Swift client)
├─ deploy/
│  ├─ docker-compose.yml
│  ├─ backup/                backup.sh, restore.sh, crontab
│  └─ launchd/               com.pantry.ocr-mac.plist (optional)
├─ Makefile
└─ .github/workflows/ci.yml  go test, sqlc diff, lint, docker build, xcodebuild test
```

**Tooling:** Go 1.23+, `modernc.org/sqlite` (pure Go, so no CGO), `sqlc`, `goose`, `oapi-codegen` (server types), `golangci-lint`, `github.com/modelcontextprotocol/go-sdk`. On iOS, `swift-openapi-generator` + `swift-openapi-urlsession`.

---

## 3. Conventions

- **Money:** `int64` cents everywhere. Format as `€ 1,29` only at the edges. Floats are never used for money.
- **Quantities:** `qty REAL` plus `unit ∈ {pc, kg, l}`. Product size is `net_quantity REAL` plus `net_unit ∈ {g, ml, pc}`.
- **Time:** stored as UTC RFC3339. A receipt's `purchased_at` is parsed as Europe/Amsterdam local time and then converted.
- **IDs:** `INTEGER PRIMARY KEY`. Images are named by `sha256` (`images/receipts/ab/abcd….jpg`).
- **Soft delete** (`deleted_at`) for shops, products, and categories, so history is never orphaned. Receipts are hard-deleted, image included.
- **Errors:** RFC 7807 `application/problem+json`.
- **Logging:** `log/slog` JSON. One line per request and one per job.
- **Config (env):** `PANTRY_DATA_DIR`, `PANTRY_ADDR`, `OCR_URL`, `OCR_FALLBACK_URL`, `OFF_USER_AGENT`, `PANTRY_WORKERS` (default 1), `PANTRY_TZ=Europe/Amsterdam`.
- **Tests:** table-driven Go tests. The parser regression corpus has to pass in CI. Handler tests use a temp SQLite DB.

---

## 4. Data model (migration `0001_init.sql`)

```sql
PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;

CREATE TABLE shops (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, chain TEXT, branch TEXT,
  address TEXT, city TEXT, parser_profile TEXT NOT NULL DEFAULT 'generic',
  notes TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT);

CREATE TABLE categories (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, sort INTEGER NOT NULL DEFAULT 0,
  deleted_at TEXT);

CREATE TABLE products (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, brand TEXT, category_id INTEGER REFERENCES categories(id),
  barcode TEXT UNIQUE, net_quantity REAL, net_unit TEXT CHECK (net_unit IN ('g','ml','pc')),
  image_path TEXT, off_code TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT);

CREATE TABLE nutrition (
  product_id INTEGER PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  per TEXT NOT NULL CHECK (per IN ('100g','100ml')),
  energy_kcal REAL, fat REAL, saturated_fat REAL, carbs REAL, sugars REAL,
  fibre REAL, protein REAL, salt REAL, source TEXT NOT NULL CHECK (source IN ('off','manual')));

CREATE TABLE receipts (
  id INTEGER PRIMARY KEY, shop_id INTEGER NOT NULL REFERENCES shops(id),
  purchased_at TEXT, total_cents INTEGER, parsed_sum_cents INTEGER,
  currency TEXT NOT NULL DEFAULT 'EUR', payment_method TEXT,
  image_path TEXT NOT NULL, image_sha256 TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL CHECK (status IN ('pending','processing','needs_review','ok','failed')),
  review_reasons TEXT,             -- JSON array
  ocr_engine TEXT, ocr_ms INTEGER, raw_ocr_json TEXT,
  parser_version INTEGER, confirmed_at TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX receipts_shop_date ON receipts(shop_id, purchased_at);
CREATE INDEX receipts_status ON receipts(status);

CREATE TABLE receipt_lines (
  id INTEGER PRIMARY KEY, receipt_id INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
  line_no INTEGER NOT NULL, raw_text TEXT NOT NULL, norm_text TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('item','discount','deposit','bag','other')),
  qty REAL NOT NULL DEFAULT 1, unit TEXT NOT NULL DEFAULT 'pc' CHECK (unit IN ('pc','kg','l')),
  unit_price_cents INTEGER, line_total_cents INTEGER NOT NULL, vat_code TEXT,
  parent_line_id INTEGER REFERENCES receipt_lines(id) ON DELETE SET NULL,
  product_id INTEGER REFERENCES products(id), user_edited INTEGER NOT NULL DEFAULT 0);
CREATE INDEX lines_receipt ON receipt_lines(receipt_id, line_no);
CREATE INDEX lines_product ON receipt_lines(product_id);

CREATE TABLE product_aliases (
  id INTEGER PRIMARY KEY, shop_id INTEGER REFERENCES shops(id),      -- NULL = any shop
  norm_text TEXT NOT NULL, product_id INTEGER NOT NULL REFERENCES products(id),
  created_at TEXT NOT NULL, UNIQUE (shop_id, norm_text));

CREATE VIRTUAL TABLE search_fts USING fts5(
  kind UNINDEXED, ref_id UNINDEXED, text, tokenize = 'trigram');   -- products + aliases

CREATE TABLE shopping_list_items (
  id INTEGER PRIMARY KEY, product_id INTEGER REFERENCES products(id),
  text TEXT NOT NULL, qty REAL, note TEXT, checked_at TEXT, created_at TEXT NOT NULL);

CREATE TABLE jobs (
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, payload TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('queued','running','done','failed')),
  attempts INTEGER NOT NULL DEFAULT 0, run_after TEXT NOT NULL, last_error TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX jobs_ready ON jobs(status, run_after);

CREATE TABLE api_tokens (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, token_hash TEXT NOT NULL,
  created_at TEXT NOT NULL, last_used_at TEXT);

-- price per item after its linked discounts, normalised to base unit
CREATE VIEW price_points AS
SELECT l.id AS line_id, l.product_id, r.shop_id, r.purchased_at, l.qty, l.unit,
       l.line_total_cents + COALESCE((SELECT SUM(d.line_total_cents) FROM receipt_lines d
                                      WHERE d.parent_line_id = l.id), 0) AS paid_cents
FROM receipt_lines l JOIN receipts r ON r.id = l.receipt_id
WHERE l.kind = 'item' AND l.product_id IS NOT NULL AND r.status = 'ok';
```

The per-base-unit price (`cents/kg`, `cents/l`, `cents/pc`) is computed in Go from `paid_cents`, `qty`/`unit`, and the product's `net_quantity`/`net_unit`. Seed categories: Zuivel/Dairy, Groente & fruit, Vlees & vis, Brood, Voorraad, Diepvries, Dranken, Huishouden, Verzorging, Overig.

---

## 5. Receipt processing spec

### 5.1 Pipeline (job `process_receipt`)

| Step | What happens | Output / failure |
|---|---|---|
| 1. Load | Decode the JPEG/HEIC, apply EXIF orientation | `failed: undecodable` |
| 2. Preprocess | Grayscale, auto-contrast (percentile stretch), long side ≤ 2400 px | image bytes |
| 3. OCR | `POST OCR_URL/ocr` with a 120 s timeout and 3 retries with backoff | `raw_ocr_json`, `ocr_ms` |
| 4. Rows | Cluster lines into rows (§5.2) | `[]Row` |
| 5. Parse | Classify + merge (§5.3) | lines, total, date, payment |
| 6. Validate | §5.4 | `ok` / `needs_review` + reasons |
| 7. Fallback | If `needs_review` and `OCR_FALLBACK_URL` is set → redo 3–6 with the fallback engine and keep the result with fewer reasons | |
| 8. Match | Exact alias → fuzzy suggestions (§6). Only exact alias hits are auto-linked. | `product_id` on lines |
| 9. Persist | Write everything in one transaction and set `parser_version` | |

`reparse` reruns steps 4–9 from the stored `raw_ocr_json` (no OCR call). Lines with `user_edited = 1` are preserved. `reocr` reruns steps 1–9.

### 5.2 Row reconstruction

1. Normalise boxes to [0, 1]. For each line, compute `yc` (vertical centre) and `h` (height).
2. Sort by `yc`. Greedily merge a line into the current row when the vertical overlap is ≥ 50% of the smaller height.
3. Inside a row, sort by `x0`. `text` = tokens joined with a single space, and the right-most token matching `^-?\d{1,4}[.,]\d{2}-?$` becomes `amount`. A trailing `-` means negative, a common NL style.
4. A trailing single letter or digit after the amount (`A`, `B`, `1`, `2`) becomes `vat_code`.

### 5.3 Classification (ordered rules; the first match wins)

| Kind | Rule (case-insensitive, accents stripped) |
|---|---|
| `total` | `^(totaal|te betalen|total|subtotaal\s*$)` with an amount. The **last** `totaal`/`te betalen` row is the total. |
| `subtotal` | `^subtotaal` |
| `payment` | `^(pin|pinnen|contant|betaald|maestro|v pay|vpay|visa|mastercard|wisselgeld|terug)` |
| `vat` | `^(btw|b\.t\.w|%|9[.,]?0?\s*%|21[.,]?0?\s*%)` or a VAT table block after the total |
| `discount` | `(bonus|korting|actie|voordeel|kassakorting|prijsvoordeel|\-\d+%)` or a negative amount |
| `deposit` | `(statiegeld|emballage|retour)` |
| `bag` | `(tas|draagtas|plastic tas|zak)\b` with an amount ≤ €1 |
| `qty_prefix` | `^(\d+)\s*[xX×]\s*(\d+[.,]\d{2})$` → applies to the **next** item row (or the previous one, depending on the profile) |
| `weight` | `(\d+[.,]\d{1,3})\s*kg\s*[xX×@]\s*(?:€\s*)?(\d+[.,]\d{2})(?:\s*/\s*kg)?` |
| `item` | Has an amount, text length ≥ 2, and appears between the first item-like row and the total |
| `header` / `ignore` | Everything else: above the first item, below payment, or without an amount |

After classification:
- A `qty_prefix` or `weight` row merges into its item. This sets `qty`, `unit`, and `unit_price_cents`, and checks `qty × unit_price ≈ line_total` (±1 cent per unit).
- A `discount` row gets `parent_line_id` = the nearest preceding `item`. A discount block after the subtotal stays unparented, but it still counts toward the sum.
- **Date:** the first regex match anywhere of `\b(\d{2})[-/.](\d{2})[-/.](\d{2,4})\b` with an optional `(\d{2}):(\d{2})`. Dates in the future or more than 2 years old are rejected.
- **Profiles** are optional overrides (a Go map keyed by `parser_profile`): extra stop words, qty-prefix direction, and extra total/discount keywords. Only `generic` exists initially.

### 5.4 Validation → `review_reasons`

| Reason | Condition |
|---|---|
| `no_total` | No total row found |
| `total_mismatch` | `|Σ(item+discount+deposit+bag) − total| > 1 cent` |
| `line_math` | Any merged line where `qty × unit_price` is off by more than the tolerance |
| `no_date` | No date found |
| `low_conf` | Median OCR confidence < 0.80 |
| `unparsed_rows` | ≥ 2 rows with an amount that were classified `ignore` between the header and the total |

`status = ok` if there are no reasons; otherwise `needs_review`. When you confirm in the app, the status becomes `ok` and `confirmed_at` is set, even if the reasons remain. (Sometimes the receipt itself is odd.)

---

## 6. Fuzzy matching spec

1. **Normalise** (`match.Normalise`): lowercase, strip accents, `&`→`en`, remove the amount/VAT tokens, collapse whitespace, expand a small abbreviation table (`halfv`→`halfvol`, `vl`→`vol`, `gr`→`g`, `ltr`→`l`, …). The table lives in code and grows from review sessions.
2. **Exact alias:** `(shop_id, norm)` first, then `(NULL, norm)` → auto-link.
3. **Candidates:** an FTS5 trigram query over `search_fts`, returning the top 20.
4. **Score:** `0.6·JaroWinkler + 0.4·TokenSetRatio`, with a +0.05 bonus when the alias came from the same shop.
5. **Suggest** the top 3 with score ≥ 0.75 in the app. Nothing auto-links below an exact alias match.
6. **Confirming** a link writes the alias `(shop_id, norm)` and refreshes `search_fts`.

The same scorer powers `GET /v1/products?q=` and the MCP `search_products`.

---

## 7. REST API (`/v1`, Bearer auth, `openapi.yaml` is the source of truth)

```
Shops        GET /shops · POST /shops · GET/PATCH/DELETE /shops/{id}
Categories   GET /categories · POST · PATCH/DELETE /categories/{id}
Receipts     POST /receipts                      multipart{image, shop_id, purchased_at?} → 202 {id,status}
             GET  /receipts?from&to&shop_id&status&limit&cursor
             GET  /receipts/{id}                 lines + suggestions[] per unmatched line
             GET  /receipts/{id}/image
             PATCH /receipts/{id}                shop_id, purchased_at, total_cents
             POST /receipts/{id}/confirm
             POST /receipts/{id}/reparse · POST /receipts/{id}/reocr
             DELETE /receipts/{id}
Lines        POST /receipts/{id}/lines · PATCH/DELETE /lines/{id}
             PUT  /lines/{id}/product            {product_id, remember: true} → writes alias
Products     GET /products?q&category_id · POST /products · GET/PATCH/DELETE /products/{id}
             GET /barcodes/{ean}         local hit | OFF prefill | 404
             PUT /products/{id}/image            multipart
             PUT /products/{id}/nutrition
             GET /products/{id}/unmatched-lines?days=14   candidate lines to link
Stats        GET /stats/spend?from&to&group_by=day|week|month|shop|category
             GET /stats/products/{id}/prices     points per shop over time
             GET /stats/products/{id}/compare    latest cents/base-unit per shop
             GET /stats/price-changes?since&min_pct
List         GET /list · POST /list · PATCH/DELETE /list/{id} · POST /list/{id}/check
System       GET /healthz (db, ocr, ocr_fallback, queue depth) · GET /version
```

---

## 8. MCP server

- Transport: streamable HTTP at `/mcp`, sharing the HTTP server and token auth with REST.
- Implementation: `github.com/modelcontextprotocol/go-sdk`. Each tool is a thin wrapper over the `stats` and `store` packages, so the assistant and the app see the same numbers.

| Tool | Input | Output (compact) |
|---|---|---|
| `spend_summary` | from, to, group_by?, shop?, category? | buckets `{key, cents, eur}` + total |
| `list_receipts` | from?, to?, shop?, status?, limit? | `{id, date, shop, total}` |
| `get_receipt` | id | header + lines |
| `search_products` | query, limit? | `{id, name, brand, score}` |
| `price_history` | product (id or name), shop? | points `{date, shop, eur_per_unit}` |
| `compare_prices` | product | per shop: latest `eur_per_base_unit`, date |
| `price_changes` | since, min_pct? | `{product, shop, old, new, pct}` |
| `list_shops` | — | `{id, name, city}` |
| `frequent_items` | days?, min_count? | items bought ≥ N times and not in the last X days |
| `shopping_list_get` | — | items |
| `shopping_list_add` | text, qty?, product? | item |
| `shopping_list_check` / `shopping_list_remove` | id | ok |

Product arguments accept an ID or text. Text is resolved through the fuzzy matcher, and the result says which product it picked.

---

## 9. iOS app

**Structure**

```
Pantry/
├─ App/            PantryApp, RootTabView, Settings (server URL, token in Keychain)
├─ API/            generated client (swift-openapi-generator) + APIClient wrapper
├─ Sync/           UploadQueue (files in Application Support + SwiftData records, retries w/ backoff)
├─ Cache/          SwiftData models: CachedProduct, CachedListItem, CachedShop
├─ Features/
│  ├─ Receipts/    InboxView, ScanFlow (VNDocumentCameraViewController → ShopPicker → enqueue)
│  ├─ Review/      ReviewView (zoomable image + editable line list + suggestions + confirm)
│  ├─ Unpack/      ScannerView (DataScannerViewController EAN), QuickAddSheet, NutritionForm
│  ├─ Spend/       SpendView (Swift Charts: bar by month, stacked by shop/category, range picker)
│  ├─ Products/    ProductList, ProductDetail (price history chart per shop, compare)
│  ├─ List/        ShoppingListView (works offline, syncs on reconnect)
│  └─ Manage/      Shops CRUD, Categories CRUD
└─ Shared/         Money formatting (nl_NL), date helpers, ImageCache
```

**Key flows**
1. **Scan:** **+** → document scanner (multi-page = one long receipt, stitched vertically on the phone) → shop picker (last used is preselected) → optional date override → queued → uploaded in the background → the inbox shows `processing` → polling turns it into `ok` / `needs_review`.
2. **Review:** a split view with the image on top and lines below. Tapping a line edits qty, price, and kind. The "Link product" sheet shows 3 suggestions plus search plus "new product". A sticky banner shows Σ vs total live, and **Confirm** is enabled at any time.
3. **Unpack:** continuous barcode scanning with haptic feedback.
   - A known product shows "✓ Melk halfvol".
   - An unknown product opens QuickAdd with OFF prefill, a photo, and nutrition.
   - After saving, the app offers "Link to receipt line?" with the unmatched lines from the last 14 days.

---

## 10. Deployment & operations

### 10.1 `deploy/docker-compose.yml`

```yaml
services:
  api:
    image: pantry-api:latest
    build: ../api
    restart: unless-stopped
    ports: ["8080:8080"]
    volumes: ["pantry-data:/data"]
    environment:
      PANTRY_DATA_DIR: /data
      OCR_URL: http://ocr:8000
      OCR_FALLBACK_URL: ${OCR_FALLBACK_URL:-}
      OFF_USER_AGENT: "Pantry/0.1 (self-hosted)"
    depends_on: [ocr]
  ocr:
    image: pantry-ocr:ppocrv5
    build: { context: ../ocr, args: { ENGINE: ppocrv5 } }
    restart: unless-stopped
    mem_limit: 3g
  # ocr-vl:      # optional accurate fallback, enabled after the spike if it earns its place
  #   image: pantry-ocr:paddleocr-vl
  #   mem_limit: 10g
  backup:
    image: pantry-api:latest
    entrypoint: ["/usr/local/bin/backup-cron"]
    volumes: ["pantry-data:/data:ro", "/Volumes/nas/pantry:/backup"]
    environment: { BACKUP_KEEP_DAYS: "30", BACKUP_AT: "03:30" }
volumes: { pantry-data: {} }
```

- **Docker Desktop memory:** raise the VM to **10–12 GB** if PaddleOCR-VL is used. PP-OCRv5 alone fits in about 2 GB.
- Images are built multi-arch (`linux/arm64,linux/amd64`).
- `ocr-mac` (optional): `swift build -c release` → launchd plist → `OCR_URL=http://host.docker.internal:8010`.

### 10.2 Backups to the NAS

1. Mount the NAS share on the Mac mini at `/Volumes/nas/pantry` (SMB, auto-mounted at login). Docker Desktop has to have that path shared.
2. Nightly `pantry backup` does:
   - an `sqlite3` online backup API → `pantry-YYYYMMDD.db` (consistent with WAL), then `PRAGMA integrity_check` on the copy;
   - `rsync -a --delete-after /data/images/ /backup/images/` (images are content-addressed, so only new files move);
   - deletion of DB snapshots older than `BACKUP_KEEP_DAYS`;
   - writing `last_backup.json`, which `/healthz` exposes.
3. If the mount is missing or the backup fails, `/healthz` reports `backup: stale` (> 36 h). The app shows a warning in Settings, and the MCP `health` resource reports it too.
4. **Restore drill** (run in M8, then quarterly): `deploy/backup/restore.sh <date>` → stop api → copy the DB + rsync the images back → start → spot-check the totals.

### 10.3 Remote access

Tailscale on the Mac mini and the iPhone. The app's server URL is `http://pantry.<tailnet>.ts.net:8080`, or use `tailscale serve` for HTTPS. The assistant uses the same URL.

---

## 11. Milestones

Estimates are in focused working days. Each milestone ends with a demo and a tagged release.

### M0 — Foundations (1.5 d)
- [x] Monorepo, Makefile (`make run`, `make test`, `make gen`, `make up`), `.editorconfig`
- [ ] Licence (pick one)
- [x] Go module skeleton, `cmd/pantry serve` with `/healthz`, slog, config
- [x] goose + `0001_init.sql`, sqlc wired up, `pantry migrate`
- [x] `openapi.yaml` v0 (shops + receipts + healthz), oapi-codegen types
- [x] Python `ocr/` FastAPI skeleton implementing the OCR contract with **PP-OCRv5**
- [x] `docker-compose.yml` (api + ocr)
- [ ] Multi-arch builds (`buildx --platform linux/arm64,linux/amd64`); only arm64 verified locally
- [x] CI workflow written: `go vet`, `golangci-lint`, `go test`, generated-code diff, docker build (not yet run; no remote)

**Status: done (2026-10-10).** Pins: paddleocr 3.7.0 / paddlex 3.7.2 / paddlepaddle 3.2.2, Go 1.26. Synthetic NL receipt OCR: 18/18 lines, ~4 s CPU.

**Done when** `make up` on the Mac mini returns a healthy `/healthz` that shows the OCR engine as ready.

### M1 — Spike: 100 receipts (5–6 d, including your labelling time)
- [ ] **Capture:** scan 100 receipts with the iOS document scanner and export the JPEGs to `spike/receipts/NNN.jpg`
- [ ] **Truth:** `spike/truth.csv` (`id, shop, date, total_cents, n_items`) for all 100; full line JSON for 25 (stratified)
- [ ] OCR sidecar targets: `ppocrv5` (mobile + server), `doctr`, `tesseract` (nld+eng), `paddleocr-vl` (0.9B, CPU)
- [ ] `ocr-mac` minimal Swift helper (Apple Vision, accurate, language correction off; logs whether `nl-NL` is supported)
- [ ] `spike/run`: Go CLI that OCRs each image × engine (cached), runs `internal/receipt` parser v0, and scores it
- [ ] Parser v0 per §5 (rows, classification, merge, validation)
- [ ] `docs/spike-report.md`: per-engine and per-shop metrics, failure taxonomy, recommendation

| Metric | Pass |
|---|---|
| Total correct | ≥ 85 % |
| Σ lines == total | ≥ 80 % |
| Date correct | ≥ 95 % |
| Line F1 (25 labelled) | ≥ 0.85 |
| Time / receipt (Docker CPU) | default engine ≤ 15 s; fallback ≤ 90 s |
| Peak RAM | default ≤ 3 GB; fallback ≤ 10 GB |

**Done when:** the default and fallback engines are chosen, and the 25 labelled receipts are committed as `api/testdata/receipts/` (OCR JSON + expected JSON). The images stay local.
**If the targets are missed:** iterate on the parser rules first (cheap). Change engines second. Do not add an LLM.

### M2 — Backend core (6 d)
- [x] `store`: sqlc queries for all tables, tx helper, `PRAGMA` setup, `busy_timeout`
- [x] Auth middleware + `pantry token create|list|revoke`
- [x] Shops & categories CRUD (+ seed categories)
- [x] `POST /receipts`: sha256 dedupe (409 with the existing id), store the image, enqueue a job
- [x] `jobs`: polling worker (claim with `UPDATE … RETURNING`), retries with backoff, crash-safe (`running` > 10 min → requeue)
- [x] `ocr` client with timeout/retry/fallback. Preprocessing (EXIF orientation, resize ≤2400 px) lives in the OCR sidecar, not Go
- [x] `receipt` parser promoted from the spike, `parser_version` constant, full validator
- [x] Receipts GET/PATCH/DELETE/confirm/reparse/reocr, lines CRUD
- [x] `pantry import -shop ID <dir>` (uploads over HTTP, so it runs from any machine) and `pantry reparse [-status S]`
- [x] Parser table tests on synthetic layouts + full-stack handler tests (temp DB, fake OCR, queue)
- [ ] Golden corpus from real receipts (comes from the spike)

**Status: done (2026-10-10)** except the real-receipt criterion, which moves to the spike. Two generated receipts (one rotated + blurred) went through real PP-OCRv5 → parser → `ok` with exact totals. Added migration 00003 (`date_locked`, `header_edited`) so user edits survive reparse.

**Done when** all 100 spike receipts are imported through the real API, and ≥ 80 % of them land in `ok` without edits.

### M3 — Products & fuzzy matching (4 d)
- [x] `match.Normalise` + abbreviation table + tests
- [x] FTS5 trigram index maintenance (triggers or explicit refresh)
- [x] Jaro-Winkler + token-set scorer, `Suggest(shopID, text) []Candidate`
- [x] Products CRUD, image upload (resize to 1024 px, JPEG), nutrition PUT
- [x] `openfoodfacts` client: `GET https://world.openfoodfacts.org/api/v2/product/{ean}` with a proper User-Agent, 24 h negative cache, mapping to product + nutrition
- [x] `GET /barcodes/{ean}`, `PUT /lines/{id}/product` (alias write), suggestions in `GET /receipts/{id}`
- [x] `GET /products/{id}/unmatched-lines`

**Status: done (2026-10-10).** Barcode lookup lives at `GET /v1/barcodes/{code}` (a `/v1/products/barcode/…` path clashes with `/v1/products/{id}/…`). Search index rebuilt with diacritics folding + triggers (migration 00004). Verified live against Open Food Facts.

**Done when** linking a line once makes the same text from the same shop auto-link on the next receipt, and an EAN scan of an AH/Jumbo/Lidl product returns an OFF prefill.

### M4 — Stats & shopping list (3 d)
- [x] `stats.Spend` (group by day/week/month/shop/category, Europe/Amsterdam buckets)
- [x] Price points + base-unit normalisation + `compare` + `price-changes` (median of the last 3 vs the previous 3 points)
- [x] `frequent_items`
- [x] Shopping list CRUD/check
- [x] Endpoints + tests with fixture data (hand-computed expected totals)

**Status: done (2026-10-10).** Spend counts `ok` + `needs_review` receipts; price analytics use `ok` receipts only. Shopping-list text auto-links to a product at fuzzy score ≥ 0.9 and re-adding an open product bumps its qty. Tests assert bucket sums equal totals for every grouping.

**Done when** the spend totals for each month equal the sum of the confirmed receipts (property test), and the compare output is correct on fixtures.

### M5 — MCP over HTTP (2 d)
- [x] go-sdk server mounted at `/mcp` behind token auth
- [x] All tools from §8, with JSON schemas and compact outputs
- [x] Integration test: an MCP client in Go calls each tool against a fixture DB
- [x] Write `docs/mcp.md` with setup + example prompts
- [ ] Connect your own assistant (needs you)

**Status: done (2026-10-10)** except connecting your assistant. Stateless streamable HTTP with JSON responses; 13 tools + 2 resources; product/shop args accept id or fuzzy name. Verified with the Go SDK client in tests and with raw JSON-RPC against the Docker stack.

**Done when** the assistant correctly answers: September spend, spend per shop this year, "where is milk cheapest", "add eggs to the list".

### M6 — iOS MVP: capture & review (7 d)
- [ ] Xcode project, generated client from `openapi.yaml`, Keychain token, Settings/onboarding (URL + token + test connection)
- [ ] ScanFlow (document scanner, multi-page stitch, shop picker), UploadQueue (persistent, background `URLSession`)
- [ ] Inbox (status filters, pull to refresh, polling while processing)
- [ ] ReviewView (image zoom, line edit, kind picker, add/delete line, live Σ vs total, link product with suggestions/search/create, confirm)
- [ ] Shops & categories management screens
- [ ] Unit tests (queue, money formatting), one UI test for scan → upload against a mock server

**Done when** you use only the app for every receipt for 2 weeks, and it takes under 90 s to review a `needs_review` receipt.

### M7 — iOS catalog, charts, list (6 d)
- [ ] Unpack: DataScanner EAN, known/new feedback, QuickAdd with OFF prefill, camera photo, NutritionForm, link to an unmatched line
- [ ] Products list/detail with a price history chart per shop + compare card
- [ ] Spend tab (Swift Charts): monthly bars, by-shop/by-category breakdowns, range picker
- [ ] Shopping list with an offline SwiftData cache + sync, and "cheapest at" hints
- [ ] Polish: empty states, error toasts, Dutch number formatting, dark mode

**Done when** a full grocery haul can be unpacked with every new product added and linked, and the list works in airplane mode and syncs afterwards.

### M8 — Ops hardening (2 d)
- [ ] `pantry backup` + backup container, NAS mount docs, retention, `/healthz` staleness
- [ ] Restore drill executed and documented (`docs/restore.md`)
- [ ] Tailscale setup docs; optional `tailscale serve` HTTPS
- [ ] Image + DB size check in `/healthz`; log rotation
- [ ] Upgrade procedure: `docker compose pull && up -d`, with migrations auto-run on start (with a backup taken first)

**Done when** you can rebuild the Mac mini from scratch using only the NAS backup and the docs, in under 30 min.

### M9 — Later (unscheduled, opt-in)
- Per-shop parser profiles, if `needs_review` clusters on one chain
- Nutrition-label OCR + regex parsing
- Bank CSV import (ING/ABN/Rabo) to reconcile receipt totals
- Widgets / App Intents ("add milk to the list")
- Monthly nutrition totals from linked products

---

## 12. Timeline (part-time, ~10 h/week ≈ 1.25 d/week)

| Weeks | Milestones |
|---|---|
| 1 | M0 |
| 2–5 | M1 spike (including scanning and labelling) |
| 6–10 | M2 backend core |
| 11–13 | M3 products & matching |
| 14–15 | M4 stats & list |
| 16 | M5 MCP (the assistant is useful from here) |
| 17–22 | M6 iOS MVP |
| 23–27 | M7 iOS catalog/charts/list |
| 28 | M8 ops |

Total ≈ **37–38 working days** (~7 months part-time, ~8 weeks full-time). M5 deliberately comes before the app: once M2–M5 are done, the `pantry import` CLI plus the assistant are already useful while the iOS app is being built.

---

## 13. Testing strategy

| Layer | How |
|---|---|
| Parser | The golden corpus (25 → grows with every fixed `needs_review`): OCR JSON in → expected lines/total/date out. Run in CI. Bump `parser_version` when rules change. |
| Numbers/dates | Table tests for `1,29`, `1.29`, `-0,50`, `0,50-`, `1.234,56`, `2 x 1,29`, `0,512 kg x 3,49`, and the date formats |
| Matching | Fixtures of (raw text, shop) → expected product, plus precision measured at the 0.75 threshold |
| Store/API | `httptest` + temp SQLite, migrations applied fresh per test |
| Stats | Property tests: spend by any grouping sums to the same grand total |
| MCP | Go MCP client against a fixture DB |
| OCR sidecar | Smoke test on 3 sample images in CI (docker build + run) |
| iOS | XCTest for the queue, money, and parsing of API models. One XCUITest flow against a mock server. |
| End-to-end | `make e2e`: compose up → import 10 corpus images → assert statuses + totals → MCP spend_summary matches |

---

## 14. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Faded or crumpled thermal paper makes OCR unreliable | Document scanner capture, the fallback engine on `needs_review`, and a quick manual-fix UI. Scan soon after shopping, because thermal paper fades. |
| The generic parser misses chain-specific layouts | The golden corpus shows exactly which rules fail. Per-shop profiles stay as an escape hatch. |
| PaddleOCR-VL too slow or heavy on CPU | Use it only as a fallback, or drop it. The spike decides with numbers. |
| Docker on macOS can't use the Mac's acceleration | Accepted by design (CPU default). The optional `ocr-mac` is available if speed ever matters. |
| The alias table grows noisy | Aliases are editable/deletable from the product detail screen, and are only ever created by an explicit confirmation. |
| NAS unavailable | Backup staleness shows in `/healthz` + the app. Local snapshots are kept for 3 days as a buffer. |
| Open Food Facts gaps for store brands | Manual entry is a first-class path, and the result is cached locally forever. |

---

## 15. Immediate next steps

1. Start saving receipts in a box now; the spike needs 100.
2. M0 scaffold: repo, compose, PP-OCRv5 sidecar, `/healthz`.
3. Scan the first 20 receipts and run PP-OCRv5 on them, to check early that the OCR output looks sane.
