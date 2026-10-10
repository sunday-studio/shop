# Backend status (handoff)

_Last updated 2026-10-10 by the backend thread._ Read this first if you're picking up the server side. The detailed spec and milestone checklists are in [PLAN.md](PLAN.md).

## What it is

A Go server (`api/`) with SQLite, an OCR sidecar (`ocr/`, Python + PaddleOCR) and Docker Compose (`deploy/`). It serves the REST API (`/v1`, contract in [`openapi.yaml`](../openapi.yaml) **0.9.0**), MCP for an AI assistant (`/mcp`), and the iOS app. Everything runs CPU-only in Docker, with no macOS dependency (a deliberate decision).

## Run it

```bash
cp deploy/.env.example deploy/.env    # add LOGO_DEV_TOKEN (logo.dev pk_…), optional
make setup                            # build + start; prints the app pairing password once
make test                             # Go tests (all green at the time of writing)
make token NAME=assistant             # bearer token for MCP/scripts
```

- The API is at `http://localhost:8080`; `GET /healthz` shows each dependency, with no auth.
- The app pairs with URL + password (`POST /v1/auth/pair`).
- Operations (NAS backups, Tailscale, upgrades): [operations.md](operations.md). Restore: [restore.md](restore.md). MCP: [mcp.md](mcp.md).

## Done (all tested; verified on the Docker stack)

| Area | Where | Notes |
|---|---|---|
| Receipt pipeline | `internal/receipts`, `internal/receipt` (parser v2), `internal/jobs`, `ocr/` | Upload → OCR → deterministic parse → validate → `ok`/`needs_review`. Reparse/re-OCR. Tall scans are tiled (`ocr/tiling.py`) |
| Products, matching | `internal/products`, `internal/match`, `internal/openfoodfacts` | Fuzzy (FTS5 trigram + Jaro-Winkler), per-shop aliases learned on linking, barcode → Open Food Facts |
| English names | `internal/translate`, `internal/english` | Offline dictionary + compound rules, user glossary, unknown-word spans, review queue (spec §6a) |
| Stats, list | `internal/stats`, `internal/shopping` | Spend (time/shop/category/product, split_by), per-unit prices, price changes, frequent items. List: idempotent adds (client_id), changes feed |
| Search, logos | `internal/search`, `internal/logos` | Unified search; shop logos via logo.dev, cached on disk |
| Auth | `internal/auth`, `internal/httpapi/pairing.go` | Pairing password (bcrypt) → per-device tokens; rate-limited |
| MCP | `internal/mcpserver` | 15 tools, 2 resources (stateless streamable HTTP) |
| Ops | `internal/backup`, `cmd/shop` | Nightly NAS backup service, restore, pre-migration snapshots, health checks |
| Spike tooling | `cmd/shop-spike`, `internal/spike`, `docs/spike.md` | Multi-engine OCR, labelling web page, scoring report |

All iOS backend needs (#1–#11 in [IOS-BACKEND-NEEDS.md](IOS-BACKEND-NEEDS.md)) are live.

## Not done / open

1. **The parser has never seen a real receipt.** Every result so far is from generated receipts. The next real step is the spike: scan about 100 receipts into `spike/receipts/`, then run `make spike-up`, `make spike-ocr`, `make spike-label` and `make spike-score` ([spike.md](spike.md)). Then fix parser rules from the failure list and commit about 25 line-labelled receipts as regression tests (PLAN M1 "done when").
2. **The fallback OCR engine** isn't wired into production compose yet. Choose it from the spike report, then set `OCR_FALLBACK_URL`.
3. **The English glossary** (`internal/translate/glossary.tsv`) will need words from real product names. Users can also teach words (M10).
4. **Never observed:** the nightly backup firing on schedule; backups to a real NAS (only a local folder so far).
5. **No CI:** the workflow was removed on purpose by the owner. Run `make test` locally.
6. **Owner decisions pending:**
   - Tailscale setup.
   - Connecting the AI assistant to MCP.
   - Whether to drop PaddleOCR-VL from the spike (recommended: keep it parked).
   - Confirming the GT America font licences (committed under `design/` and `ios/`).

## Conventions that matter

- Money is integer cents. Time is stored as UTC `2006-01-02T15:04:05Z`; local time is Europe/Amsterdam.
- `openapi.yaml` is the contract. Edit it first, then run `make gen` (sqlc + oapi-codegen). Never hand-edit `*.gen.go` or `internal/store/db`.
- sqlc on SQLite can't bind args in `ORDER BY` or mix `sqlc.slice` with named args. Use separate queries, or hand-written SQL (see `internal/search`).
- Migrations are append-only (`internal/migrations/000NN_*.sql`). The server snapshots the DB before applying new ones.
- No LLMs or embeddings: models are for OCR only. Everything must run in Docker on any CPU.
- All docs live in `docs/`. Commit per milestone. Secrets go in `deploy/.env` (git-ignored), never in commits.

## M11 progress (backend thread)

- Parser: 12 of the 13 spike receipts reconcile (sum == printed total). SUBTOTAAL/"N Artikelen"/"Van x voor y"/lone-price rows are ignored; lost-comma ("20015") and trailing-junk ("2,49 49") prices are repaired only when that makes the sum equal the total exactly. IMG_1475 (perspective/curl photo) is deliberately left flagged `total_mismatch`; its fix is upstream (document-scanner crop in the app). Open question for whoever has the paper: its sum only reaches the total if the two "Van x voor y" amounts count as line amounts.
- Uploads: HEIC/HEIF get an explicit 415 (app converts to JPEG). `purchased_at` is the client override or the parsed receipt date, else NULL; never upload time or EXIF.
- Shop suggestions (openapi 0.10.0, migration 00011): `internal/shopsuggest` reads the header (chain from the first ~10 rows, keyboard-noise rows skipped; branch = street + number + town). `GET /v1/receipts/{id}/shop-suggestion`, `POST .../accept` (existing `shop_id` or editable `name`; `apply_to_waiting` moves other unconfirmed receipts of the same branch), `POST .../reject` (remembered by branch key). Also embedded as `shop_suggestion` in `GET /v1/receipts/{id}`. 7 AH + 4 Dirk headers detected, the 2 butcher slips return none.
- Not done: `shop_id` is still required on upload. Making it nullable (receipts "Unassigned") waits for the user's OK.

## Auto-populate products (backend step of docs/PRODUCT-AUTOPOPULATE.md) - built, flag ON by default

Live on the dev stack (openapi 0.12.0, migration 00013, package `internal/autoprod`). `AUTO_PRODUCTS` now defaults to `on` (the user's call; set `AUTO_PRODUCTS=off` in `deploy/.env` to disable). Confirming a receipt links or creates products; the backfill over already confirmed receipts still has to be started by hand.

- **Works even with the flag off:** the review queue (lists whatever is flagged), review/merge/undo/split, `POST /v1/barcodes/{code}/product` (an explicit scan), `POST /v1/products/{id}/refresh`, and the `off_refresh` job. **Needs the flag on:** creating products on receipt confirm, and the backfill (409 otherwise).
- **Tested** on the 13 real receipts (fixture `internal/autoprod/testdata/receipts.json`): 99 item lines give 28 links and 68 products, 5 near matches queued, no weight-row product, twins of OCR-junk names link to the clean product, the six wrong pairs are never auto-linked, `NUNNUNONNTT` is flagged odd_name, a second pass and a backfill create nothing. Category rules reproduce the spike's 50 correct / 1 wrong / 16 no hit. Merge+undo and discard+undo restore every row exactly. Barcode create is tested against a fake OFF server (found, no category, not found, timeout, 503, repeat, merge with a receipt product).
- **Differences from the spec:** (1) the confirm hook runs right after the confirm in its own transaction, not the same one; a failure only sets `auto_products.error`. (2) Backfill progress is a JSON value in `settings` (`autopop_backfill_state`), not the job payload; a dry run is one transaction that is rolled back. (3) No `Idempotency-Key` header; `created_now` is provided. (4) Matching ignores the matcher's same-shop alias bonus (+0.05): with it a 0.874 near match became a 0.924 auto-link. (5) `product_merges.into_product_id` is nullable (discard has no survivor) and has extra columns `alias_rows`, `from_review_before`. (6) Undo answers 409 for both already-undone and older than 30 days. (7) `ErrInvalid` on the new endpoints is 422; a malformed barcode is 400. (8) `suggested_merge` is filled on single-product, queue, review, undo and barcode responses, not on `GET /v1/products` lists. (9) After merging a `barcode_name_match` card, the surviving barcode product is re-queued as `no_category` if it still has no category. (10) User category keywords have a table (`category_keywords`) but no API to teach them yet.
