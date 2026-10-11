# Backend needs from the iOS app

What the iPhone app asked the API for (started at `openapi.yaml` v0.5.0). **All 11 are live as of v0.9.0**; later API additions are in `openapi.yaml`, and new needs get added below. The app stays local-only: it only talks to the Shop server, so anything external (logos, Open Food Facts) has to come through the backend.
Each item lists the screen that needs it and the milestone in [IOS-PLAN.md](IOS-PLAN.md) that is blocked without it. Until an item lands, the app uses the fallback shown.

| # | Need | Screen | Milestone | App fallback until then |
|---|---|---|---|---|
| 1 | ✅ live (0.7.0) · **Line counts on receipt list items:** `line_count` (item lines) and `unlinked_count` (item lines with no product) on `Receipt` in `GET /v1/receipts` | H rows: "12:08 · 9 items", "3 items to link" | iM3 | Sub-line shows only the time / status |
| 2 | ✅ live (0.7.0) · **Receipt counts by status**, e.g. `GET /v1/receipts/counts` → `{needs_review, failed, processing}` | H attention card, tab badge | iM3 | `GET /v1/receipts?status=needs_review&limit=200` and count |
| 3 | ✅ live (0.7.0), used by X.1 · **Unified search** `GET /v1/search?q=&limit=` → `{products[], aliases[] {raw, product_id, shop_id}, receipts[]}` (receipts matched on line text) | X.1 Search ("Receipt names", "Receipts with 'melk'") | iM6 | Products section only |
| 4 | ✅ live (0.8.0) · **Shop logos served by the backend:** a `logo_url` (or `domain`) on `Shop` plus `GET /v1/shops/{id}/logo`, which fetches from the logo provider once and caches it on disk. The app caches the image too | ShopMark everywhere | any; user will send the provider URL | Monogram letters |
| 5 | ✅ live (0.7.0), app sends `client_id` · **Idempotent shopping-list writes:** accept a client-generated `client_id` (UUID) on `POST /v1/list` (or an `Idempotency-Key` header) and return the existing item on a repeat. Today a retried offline add would bump qty twice, because re-adding an open product bumps its qty | L offline sync | iM9 | Offline adds risk double qty on retry |
| 6 | ✅ live as `GET /v1/list/changes` (0.7.0); app still refetches the full list after sync, which is fine at list size · **List sync support:** `updated_at` on `ListItem` and `GET /v1/list?since=<rfc3339>` (incl. checked and deleted ids) | L offline sync, last-write-wins | iM9 | Full list refetch after each sync |
| 7 | ✅ live, no app change · **Tall stitched receipts:** the app stitches multi-page scans into one tall JPEG (e.g. 1600 × 6000). OCR preprocessing caps the *long side* at 2400 px, which shrinks such a receipt to ~640 px wide and hurts OCR. Cap the *width* (e.g. ≤ 1600) for images taller than ~2:1, or OCR in vertical tiles | C.1 multi-page scans | iM4 | Long receipts may land in `needs_review` more often |
| 9 | ✅ live (0.9.0), used by P and P.1 · **Best price + last bought on product lists:** `cheapest` (`ShopPrice`), `last_bought_at` and `purchases` on `Product` in `GET /v1/products` | P list ("Best per unit" column, "Recently bought" order), P.1 "bought 24× since May" | iM6 | One `GET /v1/stats/products/{id}/compare` per visible row (cached); list sorted A–Z |
| 10 | ✅ live (0.9.0), used by A · **Two-level spend:** `GET /v1/stats/spend?group_by=month&split_by=category` (each time bucket carries its category buckets) | A monthly bars stacked by category | iM8 | One `/stats/spend?group_by=category` call per month bucket (≤ 12) |
| 11 | ✅ live (0.9.0), used by A.2 · **Spend per product:** `group_by=product` on `/v1/stats/spend` (optionally with `category_id`), returning qty and cents per product | A.2 "Product · Qty · Spent" table | iM8 | Price history per product in the category (≤ 12 products), summed in the app |
| 8 | ✅ live (0.7.0), used by H.1 · **Product name + category on receipt lines:** `product_name` and `category_id` on `ReceiptLine` (single-receipt GET) | H.1 line titles and category dots | iM5 | App syncs the whole product catalog (`GET /v1/products` paged) and resolves names locally |

Already fine (no change needed): receipt upload retries are safe (sha256 dedupe → 409 with id); OFF lookup is server-side (`/v1/barcodes/{code}`); product/receipt images have GET endpoints; `/healthz` has the backup check; `ListItem.cheapest` covers the list price hints; frequent items cover "usually bought by now".

New entries get added here as the milestones uncover them.
