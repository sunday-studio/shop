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

- Done: parser treats "22 SUBTOTAAL" / "2 TOTAAL" (leading item count) as subtotal/total, with a test (`TestSubtotalWithLeadingCount`). Migration 00010 (English categories) is guarded against UNIQUE collisions with user-made categories; verified applied on the running stack (v9 -> v10, categories read in English).
- Blocked: `shop reparse` skips receipts 1 and 2 because both are confirmed (`confirmed_at` set), by design. Needs a decision before re-parsing them.
- Next: HEIC / multi-page upload check (use the spike agent's HEIC-through-OCR findings), purchase date from receipt, then add the real receipts as fixtures.
