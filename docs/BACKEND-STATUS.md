# Backend status (handoff)

_Last updated 2026-10-11 by the backend thread._ Read this first if you're picking up the server side. The detailed spec and milestone checklists are in [PLAN.md](PLAN.md).

## What it is

A Go server (`api/`) with SQLite, an OCR sidecar (`ocr/`, Python + PaddleOCR) and Docker Compose (`deploy/`). It serves the REST API (`/v1`, contract in [`openapi.yaml`](../openapi.yaml) **0.12.1**; migrations `00001`-`00013`), MCP for an AI assistant (`/mcp`), and the iOS app. Everything runs CPU-only in Docker, with no macOS dependency (a deliberate decision).

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
| MCP | `internal/mcpserver` | 17 tools (incl. `review_new_products`, `resolve_review`), 2 resources (stateless streamable HTTP) |
| Ops | `internal/backup`, `cmd/shop` | Nightly NAS backup service, restore, pre-migration snapshots, health checks |
| Shop suggestions | `internal/shopsuggest` | Chain + branch read from the receipt header; suggestion / accept / reject endpoints; migrations 00011, 00012 (nullable `receipts.shop_id`, `photo_taken_at`) |
| Auto products | `internal/autoprod` | `create_or_link`, category rules, review queue, merge / undo / split / discard, barcode create, `off_refresh`, backfill; migration 00013; `AUTO_PRODUCTS` on by default |
| Spike tooling | `cmd/shop-spike`, `internal/spike`, `docs/spike.md` | Multi-engine OCR, labelling web page, scoring report |

All iOS backend needs (#1–#11 in [IOS-BACKEND-NEEDS.md](IOS-BACKEND-NEEDS.md)) are live.

## Not done / open

1. **IMG_1475 (receipt 3 on the real DB, Dirk)** is a curled library photo: price column drifts against the descriptions, so it parses with a total mismatch (printed 45,04, parsed 43,57) and stays flagged `needs_review`/`total_mismatch`. It needs a straightened photo (the app's document scanner). Its lines were left out of the backfill (`skip_receipt_ids [3]`); 3 of them are junk products (Jw Sardines, Raworganic Chtaz Wit, Madame Jeanette), 6 are fine. It is also unknown whether the two "Van x voor y" amounts are real line prices.
2. **Dutch to English model: held, conditional go** (PLAN M11). Not built; the glossary plus the user's taught words do the English names for now.
3. **The fallback OCR engine** isn't wired into production compose yet. Choose it from the spike report, then set `OCR_FALLBACK_URL`.
4. **Taught category keywords:** table `category_keywords` exists, no endpoint to add them yet.
5. **`Idempotency-Key`** on the barcode create call was not built (`created_now` covers retries).
6. **Never observed:** the nightly backup firing on schedule; backups to a real NAS (only the local `backups/` folder so far).
7. **No CI:** the workflow was removed on purpose by the owner. Run `make test` locally.
8. **Owner decisions pending:** Tailscale setup; connecting the AI assistant to MCP; whether to drop PaddleOCR-VL from the spike (recommended: keep it parked); confirming the GT America font licences.

## Conventions that matter

- Money is integer cents. Time is stored as UTC `2006-01-02T15:04:05Z`; local time is Europe/Amsterdam.
- `openapi.yaml` is the contract. Edit it first, then run `make gen` (sqlc + oapi-codegen). Never hand-edit `*.gen.go` or `internal/store/db`.
- sqlc on SQLite can't bind args in `ORDER BY` or mix `sqlc.slice` with named args. Use separate queries, or hand-written SQL (see `internal/search`).
- Migrations are append-only (`internal/migrations/000NN_*.sql`). The server snapshots the DB before applying new ones.
- No LLMs or embeddings: models are for OCR only. Everything must run in Docker on any CPU.
- All docs live in `docs/`. Commit per milestone. Secrets go in `deploy/.env` (git-ignored), never in commits.

## M11 (backend thread): what is done

- **Parser** (12 of the 13 spike receipts reconcile, sum == printed total): SUBTOTAAL / "N Artikelen" / "Van x voor y" / lone-price / savings-summary rows are ignored; a lost comma ("20015") or trailing junk ("2,49 49") is repaired only when that makes the sum equal the printed total exactly. Receipts 1 and 2 on the real DB were force-reparsed (their doubled sums are fixed: 8468 and 9980).
- **Uploads:** `shop_id` is optional on `POST /v1/receipts` (receipts show as Unassigned; they count in spend under an "Unassigned" bucket) and `photo_taken_at` is an optional hint that is never the purchase date. HEIC/HEIF get an explicit 415 (the app converts to JPEG); `purchased_at` is the client override or the date parsed from the receipt, else NULL. Never the upload time or EXIF.
- **Shop suggestions** (openapi 0.10+): `GET /v1/receipts/{id}/shop-suggestion`, `POST .../accept` (existing `shop_id` or editable `name`/`branch`/`domain`; `apply_to_waiting` moves the other unconfirmed receipts of the same branch, unassigned ones included), `POST .../reject`; also embedded as `shop_suggestion` in `GET /v1/receipts/{id}`. 7 AH + 4 Dirk headers detected, the 2 butcher slips return none.
- **Auto products** (spec: [PRODUCT-AUTOPOPULATE.md](PRODUCT-AUTOPOPULATE.md)): live, `AUTO_PRODUCTS` defaults to `on` (set `off` in `deploy/.env` to disable). Confirming a receipt links or creates products. Tested on the 13 real receipts (99 item lines: 28 links, 68 products, 5 near matches queued, weight and totals rows never become products; the category rules reproduce 50 correct / 1 wrong / 16 no hit); merge+undo, discard+undo and split+undo restore every row exactly; barcode create is tested against a fake Open Food Facts server.
- **What the backfill did on the real DB (2026-10-10):** snapshot `shop-20261010-205648.db` first. Receipts 1 and 2 were force-reparsed (a forced reparse clears `confirmed_at`, so they were confirmed again) and the confirm created 25 products and 3 links (receipt 1: 13 created; receipt 2: 12 created, 3 linked); the backfill with `skip_receipt_ids [3]` then found nothing left to do. 11 products wait in the review queue (near_match 2, no_category 6, odd_name 3). Products carry `created_via=receipt`.
- **Differences from the spec:** (1) the confirm hook runs right after the confirm in its own transaction, not the same one; a failure only sets `auto_products.error`. (2) Backfill progress is a JSON value in `settings` (`autopop_backfill_state`), not the job payload; a dry run is one rolled-back transaction; the request takes an optional `skip_receipt_ids`. (3) No `Idempotency-Key` header. (4) Matching ignores the matcher's same-shop alias bonus (+0.05): with it a 0.874 near match became a 0.924 auto-link. (5) `product_merges.into_product_id` is nullable (discard has no survivor) and has extra columns `alias_rows`, `from_review_before`. (6) Undo answers 409 for both already-undone and older than 30 days. (7) New endpoints use 422 for "cannot do that", 400 for a malformed barcode. (8) `suggested_merge` is filled on single-product, queue, review, undo and barcode responses, not in `GET /v1/products` lists. (9) After merging a `barcode_name_match` card, the surviving barcode product is re-queued as `no_category` if it still has no category.
