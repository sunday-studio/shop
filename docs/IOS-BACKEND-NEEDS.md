# Backend needs from the iOS app

What the iPhone app needs that the API (`openapi.yaml` v0.5.0) doesn't provide yet. The app stays local-only: it only talks to the Shop server, so anything external (logos, Open Food Facts) has to come through the backend.
Each item lists the screen that needs it and the milestone in [IOS-PLAN.md](IOS-PLAN.md) that is blocked without it. Until an item lands, the app uses the fallback shown.

| # | Need | Screen | Milestone | App fallback until then |
|---|---|---|---|---|
| 1 | **Line counts on receipt list items:** `line_count` (item lines) and `unlinked_count` (item lines with no product) on `Receipt` in `GET /v1/receipts` | H rows: "12:08 · 9 items", "3 items to link" | iM3 | Sub-line shows only the time / status |
| 2 | **Receipt counts by status**, e.g. `GET /v1/receipts/counts` → `{needs_review, failed, processing}` | H attention card, tab badge | iM3 | `GET /v1/receipts?status=needs_review&limit=200` and count |
| 3 | **Unified search** `GET /v1/search?q=&limit=` → `{products[], aliases[] {raw, product_id, shop_id}, receipts[]}` (receipts matched on line text) | X.1 Search ("Receipt names", "Receipts with 'melk'") | iM6 | Products section only |
| 4 | **Shop logos served by the backend:** a `logo_url` (or `domain`) on `Shop` plus `GET /v1/shops/{id}/logo`, which fetches from the logo provider once and caches it on disk. The app caches the image too | ShopMark everywhere | any; user will send the provider URL | Monogram letters |
| 5 | **Idempotent shopping-list writes:** accept a client-generated `client_id` (UUID) on `POST /v1/list` (or an `Idempotency-Key` header) and return the existing item on a repeat. Today a retried offline add would bump qty twice, because re-adding an open product bumps its qty | L offline sync | iM9 | Offline adds risk double qty on retry |
| 6 | **List sync support:** `updated_at` on `ListItem` and `GET /v1/list?since=<rfc3339>` (incl. checked and deleted ids) | L offline sync, last-write-wins | iM9 | Full list refetch after each sync |

Already fine (no change needed): receipt upload retries are safe (sha256 dedupe → 409 with id); OFF lookup is server-side (`/v1/barcodes/{code}`); product/receipt images have GET endpoints; `/healthz` has the backup check; `ListItem.cheapest` covers the list price hints; frequent items cover "usually bought by now".

New entries get added here as the milestones uncover them.
