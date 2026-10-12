# Backend status (handoff)

_Last updated 2026-10-12._ Read this first if you're picking up the server side. The detailed spec and milestone checklists are in [PLAN.md](PLAN.md).

## What it is

A Go server (`api/`) with SQLite, an OCR sidecar (`ocr/`, Python + PaddleOCR) and Docker Compose (`deploy/`). It serves the REST API (`/v1`, contract in [`openapi.yaml`](../openapi.yaml) **0.16.0**; migrations `00001`-`00019`), MCP for an AI assistant (`/mcp`), and the iOS app. Everything runs CPU-only in Docker, with no macOS dependency (a deliberate decision).

## Run it

```bash
cp deploy/.env.example deploy/.env    # add LOGO_DEV_TOKEN (logo.dev pk_…), optional
make setup                            # build + start; prints the app pairing password once
make test                             # Go tests (all green at the time of writing)
make e2e                              # whole-loop test in an isolated docker stack (see "End-to-end test" below)
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
| MCP | `internal/mcpserver` | 21 tools (incl. `review_new_products`, `resolve_review`, the `shopping_group_*` tools), 2 resources (stateless streamable HTTP) |
| Ops | `internal/backup`, `cmd/shop` | Nightly NAS backup service, restore, pre-migration snapshots, health checks |
| Shop suggestions | `internal/shopsuggest` | Chain + branch read from the receipt header; suggestion / accept / reject endpoints; migrations 00011, 00012 (nullable `receipts.shop_id`, `photo_taken_at`) |
| Auto products | `internal/autoprod` | `create_or_link`, category rules, review queue, merge / undo / split / discard, barcode create, `off_refresh`, backfill; migration 00013; `AUTO_PRODUCTS` on by default |
| Shopping list A: receipts check the list | `internal/shopping/receipt.go` | Migration 00017, openapi 0.14.0. Confirming a receipt checks off open items (linked product, or free text scoring 0.90 or more) and keeps an item open with a smaller qty and the note "1 of 3 bought" when fewer were bought. 0.75 to 0.90, or a different product of the same category, becomes a "Looks bought?" suggestion (`GET /v1/list/suggestions`, `.../accept`, `.../dismiss`). Only receipts dated within 3 days before the confirmation count. The result is on the confirm response (`list`); `POST /v1/receipts/{id}/list-checks/undo` puts everything back; deleting a receipt or a forced re-parse reverts its checks; a failure never fails the confirm |
| Shopping list B: groups and checklists | `internal/shopping/groups.go`, `internal/httpapi/list_groups.go` | Migration 00018, openapi 0.15.0. Groups (checklist or template) inside the one list: CRUD, atomic idempotent create with items (`client_id`, or `external_ref` for the assistant), bulk add, move between groups, delete with `items=keep|delete` and a 10 minute restore, template instantiate and save-as-template. A global `rev` counter and `GET /v1/list/changes?since_rev=` (the time cursor still works), tombstones for items and groups. A purchase checks a product in every live group. 4 MCP tools |
| Shopping list C: product groups and the plan | `internal/listplan`, `internal/httpapi/list_plan.go` | Migration 00019, openapi 0.16.0. "Same kind of item" product groups (suggested, never applied silently; confirm, deny, merge, split, every change undoable for 30 days). `GET /v1/list/plan` splits the open list by shop net of the cost of an extra stop (settings `GET/PATCH /v1/list/plan/settings`, defaults 250 cents and 2 shops); prices are the median of the last 3 purchases per shop inside 90 days (30 for fresh produce), savings are counted only on items priced at both shops, a missing pack size compares per piece and says so. Status `split`, `one_shop_fine` or `not_enough_data` |
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

## End-to-end test

`make e2e` starts a separate Docker stack, drives it over HTTP the way the iOS app does, prints a pass/fail table and exits non-zero on any failure. It always tears the stack down, also after a failure or Ctrl-C.

- **Isolation:** compose project `shop-e2e` (`deploy/docker-compose.e2e.yml` on top of `deploy/docker-compose.yml`), API on `127.0.0.1:18480`, its own API image tag (`shop-e2e-api`), a throwaway data volume, a throwaway tmpfs for backups. The live stack (project `shop`, port 8080, volume `shop_shop-data`) is never touched, and the test refuses to run any compose command for another project. The OCR container uses the existing `shop-ocr:ppocrv5` image (built once by `make e2e` if missing).
- **Needs:** Docker running, a free port 18480, and Go. About 1 to 2 minutes with a warm image cache.
- **Covers:** pairing (password set with the `shop` CLI inside the container, wrong password, device token), uploading a rendered receipt photo (real OCR, nothing mocked; dedupe 409, wrong type 415), status, lines, date and totals (printed total equals parsed sum), re-parse, shop suggestion and accept, list items with idempotent `client_id`, confirm (auto-created products and list auto-check), undo of the list checks, barcode create, the review queue and accepting a card, product merge and undo, groups and checklists (create, repeat, items, move, template, instantiate, delete, restore, `since_rev` feed), `GET /v1/list/plan` on thin data (`not_enough_data`) and its settings, categories (CRUD, delete with `move_to`), a backup run and the backup health check.
- **Open Food Facts** is external, so the API is pointed at a fake server on the host (`OFF_BASE_URL`, default empty = the real service) that answers found, not found and down. The real service is never contacted.
- **Private receipt photos:** if `spike/data/` (or `spike/receipts/`, or the folder in `E2E_PHOTOS`) holds JPEGs, they are uploaded too and the known-good ones (IMG_1463 to IMG_1474) must reconcile with the printed total; IMG_1475 is the documented curled photo and is only reported. Without photos this step is skipped and says so in the table.
- **Not covered:** no export endpoint exists (the backup status is checked through `/healthz`); the cheapest-store plan with real savings (`split`) needs receipts from two shops with the same product, which one rendered receipt cannot give, and is covered by the unit tests; the nightly schedule firing; backups to a real NAS; the iOS app.

## Conventions that matter

- Money is integer cents. Time is stored as UTC `2006-01-02T15:04:05Z`; local time is Europe/Amsterdam.
- `openapi.yaml` is the contract. Edit it first, then run `make gen` (sqlc + oapi-codegen). Never hand-edit `*.gen.go` or `internal/store/db`.
- sqlc on SQLite can't bind args in `ORDER BY` or mix `sqlc.slice` with named args. Use separate queries, or hand-written SQL (see `internal/search`).
- Migrations are append-only (`internal/migrations/000NN_*.sql`). The server snapshots the DB before applying new ones.
- No LLMs or embeddings: models are for OCR only. Everything must run in Docker on any CPU.
- All docs live in `docs/`. Commit per milestone. Secrets go in `deploy/.env` (git-ignored), never in commits.

## M11: what is done

- **Parser** (12 of the 13 spike receipts reconcile, sum == printed total): SUBTOTAAL / "N Artikelen" / "Van x voor y" / lone-price / savings-summary rows are ignored; a lost comma ("20015") or trailing junk ("2,49 49") is repaired only when that makes the sum equal the printed total exactly. Receipts 1 and 2 on the real DB were force-reparsed (their doubled sums are fixed: 8468 and 9980).
- **Uploads:** `shop_id` is optional on `POST /v1/receipts` (receipts show as Unassigned; they count in spend under an "Unassigned" bucket) and `photo_taken_at` is an optional hint that is never the purchase date. HEIC/HEIF get an explicit 415 (the app converts to JPEG); `purchased_at` is the client override or the date parsed from the receipt, else NULL. Never the upload time or EXIF.
- **Shop suggestions** (openapi 0.10+): `GET /v1/receipts/{id}/shop-suggestion`, `POST .../accept` (existing `shop_id` or editable `name`/`branch`/`domain`; `apply_to_waiting` moves the other unconfirmed receipts of the same branch, unassigned ones included), `POST .../reject`; also embedded as `shop_suggestion` in `GET /v1/receipts/{id}`. 7 AH + 4 Dirk headers detected, the 2 butcher slips return none.
- **Auto products** (spec: [PRODUCT-AUTOPOPULATE.md](PRODUCT-AUTOPOPULATE.md)): live, `AUTO_PRODUCTS` defaults to `on` (set `off` in `deploy/.env` to disable). Confirming a receipt links or creates products. Tested on the 13 real receipts (99 item lines: 28 links, 68 products, 5 near matches queued, weight and totals rows never become products; the category rules reproduce 50 correct / 1 wrong / 16 no hit); merge+undo, discard+undo and split+undo restore every row exactly; barcode create is tested against a fake Open Food Facts server.
- **What the backfill did on the real DB (2026-10-10):** snapshot `shop-20261010-205648.db` first. Receipts 1 and 2 were force-reparsed (a forced reparse clears `confirmed_at`, so they were confirmed again) and the confirm created 25 products and 3 links (receipt 1: 13 created; receipt 2: 12 created, 3 linked); the backfill with `skip_receipt_ids [3]` then found nothing left to do. 11 products wait in the review queue (near_match 2, no_category 6, odd_name 3). Products carry `created_via=receipt`.
- **Differences from the spec:** (1) the confirm hook runs right after the confirm in its own transaction, not the same one; a failure only sets `auto_products.error`. (2) Backfill progress is a JSON value in `settings` (`autopop_backfill_state`), not the job payload; a dry run is one rolled-back transaction; the request takes an optional `skip_receipt_ids`. (3) No `Idempotency-Key` header. (4) Matching ignores the matcher's same-shop alias bonus (+0.05): with it a 0.874 near match became a 0.924 auto-link. (5) `product_merges.into_product_id` is nullable (discard has no survivor) and has extra columns `alias_rows`, `from_review_before`. (6) Undo answers 409 for both already-undone and older than 30 days. (7) New endpoints use 422 for "cannot do that", 400 for a malformed barcode. (8) `suggested_merge` is filled on single-product, queue, review, undo and barcode responses, not in `GET /v1/products` lists. (9) After merging a `barcode_name_match` card, the surviving barcode product is re-queued as `no_category` if it still has no category.
