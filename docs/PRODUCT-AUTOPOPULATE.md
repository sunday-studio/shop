# Product auto-population: build spec

Status: approved by the user, ready for design, then backend, then swift.
Written 2026-10-10 by the research/spike agent. Research only, no product code.

Pipeline: **research spec (this document) → design → backend → swift.** Each step works from this file. If a step finds a gap or changes a decision, edit this file and tell the coordinator.

## 1. The decision

Products fill themselves. Two things create them:

1. **Receipt confirm.** When you confirm a receipt, every item line without a product is linked to an existing product or gets a new one (`create_or_link`).
2. **Barcode scan (Unpack).** An unknown EAN creates a product, prefilled from Open Food Facts (OFF). If OFF has nothing, a minimal product holding the barcode is created and flagged for review. A known EAN reports "known", as today.

Supporting parts:
- **Backfill:** the same `create_or_link` function run over receipts already confirmed.
- **Review queue:** uncertain products wait for one tap. Nothing uncertain is merged silently.
- **Merge / undo-merge / split-line:** every merge is reversible.
- **Flag:** all of it sits behind `AUTO_PRODUCTS=on` (default off) until the acceptance checks in §8 pass.

## 2. Evidence (13 real receipts, 98 item lines)

Tools: `spike/spikeprod.go.txt` (simulation), `spike/autopop/` (category rules and fixtures). Output: `spike/out/products-sim.txt`. Receipts: AH ×7, Dirk ×4, butcher ×2. Jumbo, Lidl and markets are untested.

| Measure | Result |
|---|---|
| Item lines | 98 (plus 1 skipped as too short) |
| Auto-linked (exact alias or score ≥ 0.92) | 25, **0 wrong**; all repeats of the same text (COCA-COLA ×5 lines, TIJM, AH BIO MELK, …) |
| 0.75–0.92 band (needs a tap) | 10: about 4 true duplicates, 6 wrong |
| New products | 63 clean, plus the 10 above = 73 products (about 69 distinct) |
| Review queue size | up to about 23 of 73 (~30%): near matches (3 of the 4 true duplicates disappear with the noise filter in §3.1) plus products with no category |

The 10 in the band:
- True duplicates, from OCR junk before the name: `AH ZALMFILET` ≈ `HHTHN AH ZALMFILET` (0.91), `STARB COFFEE` ≈ `HMHMNTT STARB COFFEE` (0.87), `TTNN FIT PIGGY` ≈ `FIT PIGGY` (0.88). `AH GRANOLA` ≈ `GRANOLA` (0.84) is probably the same product, unverified.
- Wrong: `AH BIO MELK` ≈ `AH BIO SAP` (0.84), `ARLA BIO HALFV. MELK` ≈ `ARLA MELK` (0.87, different variant), `AH BROCCOLI` ≈ `AH BIO SAP` (0.76), `AH PEPERMIX` ≈ `AH HAVERMOUT` (0.76), `AH PLUOT` ≈ `-- AH SPRUITJES` (0.77), `KIP LOPPOOT` ≈ `KIP POTEN` (0.79).

`AH BIO MELK` vs `AH BIO SAP` scores 0.84, so it goes to review and does not merge. `2 COCA COLA` merges with `COCA-COLA` at 1.00.

**Categories** (rules in §3.4): 67 distinct products labelled by hand (my labels, not the user's): 50 correct, **1 wrong**, 16 no hit (left uncategorised). Optimistic, because I wrote the keywords after seeing this data. A hit that is wrong costs more than no hit, which is why no hit means no category.

**English names** with the glossary as it is today (98 lines): 25 `dictionary`, 33 `partial`, 40 `missing`. What still fails is mostly brands (Coca-Cola, Fanta, Maggi, Euroma, Lipton, Indomie, Kewpie) and a few common words (§3.5). The `KIP POTEN` → "Chicken jars" bug and the missing `tijm`, `dille`, `citroengras`, `soja`, `rozemarijn`, `bosui` are already fixed in the glossary (commit 2a799fc); the backend only needs to add regression tests.

## 3. Exact rules

### 3.1 Line text cleaning (applies to receipts)

Input: `receipt_lines.raw_text` for `kind='item'` lines.

1. Remove a leading quantity: `^\d{1,2}\s+`. Remove leading bullets/dashes: `^[-–•*.\s]+`. Remove the trailing price: `\s+\d+[.,]\d{2}$`, and a trailing single VAT letter.
2. **Noise filter.** Drop up to 2 leading tokens while the token is 3+ letters, letters only, **has no vowel** (`aeiouy`), and is not in the glossary, brand list or abbreviation table. Examples from the receipts: `HHTHN`, `TTNN`, `HMHMNTT`, `NMN`, `HNNN`. (`AH` has a vowel and stays.)
3. **Odd name flag.** After step 2, if the first token is 8+ letters and unknown to the glossary, brand list and abbreviation table, the product is created but flagged `odd_name` (example: `NUNNUNONNTT BIO ZON OLIE`).
4. **Skip the line** (no product, no flag) if the result has fewer than 3 letters in a row, or matches a weight-row pattern like `^\d+[.,]?\d* ?kg( x \d+)?$` or `^[A-Z]{1,2} [A-Z]{2,4} KILO$` residue. Example skips: `3,170 kg x 5`. Lines with `user_edited=1` are never touched.
5. `norm_text = match.Normalise(cleaned)`.

### 3.2 Link thresholds (`create_or_link` for one line)

Candidates come from the existing matcher (FTS5 trigram, top 20, `match.Score`), excluding products where `deleted_at` or `merged_into_id` is set.

| Case | Action | Automatic? |
|---|---|---|
| Alias `(shop, norm)` exists, then `(any shop, norm)` | link | yes |
| Best score ≥ **0.92** | link, write alias `(shop, norm)` | yes |
| 0.75 ≤ best score < 0.92 | create a new product, `needs_review=1`, `review_reason='near_match'`, `suggested_merge_id=best` | review |
| best score < 0.75 | create a new product | yes |

Pairs the user marked "keep separate" are stored in `product_distinct` and are never suggested again.

New product fields: `name` = cleaned text in the case it came in (title-cased, brand prefix `AH` kept), `created_via='receipt'` (`'backfill'` when run by the backfill job), category per §3.4, `name_en` per §3.5, alias written. If the category has no hit, set `needs_review=1`, `review_reason='no_category'` (unless a stronger reason already applies). If §3.1 step 3 fired, `review_reason='odd_name'`.

Only `kind='item'` lines are processed. A product keeps its first reason; reasons do not stack.

Idempotent: a line that already has `product_id` is skipped, so confirming twice or re-running the backfill creates nothing new.

### 3.3 When it runs

- **Confirm:** inside `POST /v1/receipts/{id}/confirm`, same transaction as setting `confirmed_at`, when `AUTO_PRODUCTS=on`. Receipts that are not confirmed never create products.
- **Backfill:** receipts with `status='ok'` and `confirmed_at` set, in `purchased_at` order (oldest first, so the earliest spelling becomes the product name).
- A failure inside `create_or_link` must not fail the confirm; log it, return `auto_products.error`, and leave the lines unlinked.

### 3.4 Category keyword rules

Seed file: `spike/autopop/category_keywords.tsv` (move to `api/internal/.../category_keywords.tsv`, embedded like the glossary). Format: `keyword<TAB>category`, categories are the English names from migration 00010 (`Dairy`, `Produce`, `Meat & fish`, `Bakery`, `Pantry`, `Frozen`, `Drinks`, `Household`, `Personal care`). User keywords live in the table `category_keywords` and override the seed.

Matching on the folded product name (lowercase, no accents), split into tokens of letters:
1. A keyword matches a token if the token **equals** it, or the token **ends with** it and the keyword has 4+ letters (Dutch compounds end in their head word: `appelsap` → `sap`, `zalmfilet` → `filet`).
2. Pick the best match by: whole-token match beats tail match; then the longer keyword; then the later token; then the earlier row in the table.
3. No match → no category (`category_id` NULL, `review_reason='no_category'`).

Result on the 67 labelled products: 50 correct, 1 wrong (`PRINCES MAKREELFILET`: canned fish, guessed Meat & fish), 16 no hit. The reference test is `spike/autopop/cat_eval.py` with `spike/autopop/truth.py`; the backend port must reproduce 50/1/16.

For barcode products, OFF `categories_tags` map first (§4.3), the keyword rules on the name second.

### 3.5 English names

1. If the product has `name_en_source='manual'`, never touch it.
2. Barcode products: OFF `product_name_en` if present → `name_en_source='openfoodfacts'`.
3. Otherwise run `translate.Translate(name)`. Store the result in `name_en` and keep the existing statuses (`dictionary` / `partial` / `missing`, §6a of PLAN). Nothing new is shown for `missing`.
4. The small NL→EN model is **not** part of this build. After this ships, re-run its eval (`spike/nlen/eval.py`) on the real product list; its decision is separate.

**Glossary work (backend, small):**
- Regression tests: `kip poten` → "Chicken legs" status `dictionary`; `tijm`, `dille`, `citroengras`, `sojasaus`, `rozemarijn`, `bosui` → `dictionary`.
- Add missing words (all confirmed absent from `glossary.tsv` today): `krul` curly, `schouder` shoulder, `gebraden` roasted, `kalfs` veal (so `kalfspoten gebraden` → "veal legs roasted"), `verpakt` packed, `los` loose, `kerrie` curry, `laurier` bay leaf, `peen` carrot, `boomgaard` orchard.
- Add a `# --- brands` section with identity entries (`coca-cola	coca-cola`) so brands count as known: `coca-cola`, `fanta`, `lipton`, `maggi`, `euroma`, `kikkoman`, `kewpie`, `indomie`, `arla`, `camp`, `princes`, `kokki`, `uchibori`, `starb`. (The brand list grows from the review queue; users can also teach "keep as is" via §6a.)
- Not added because the meaning is not certain: `loppoot`, `zk`, `pepp`, `chtaz`. They stay `partial`/`missing` and the user teaches them.

## 4. Flows

### 4.1 Receipt confirm

```
confirm receipt
  for each item line without product_id (§3.1 cleaning, §3.2 thresholds)
     alias / ≥0.92 → link
     0.75–0.92     → create + review (near_match)
     <0.75         → create (+ review if no category / odd name)
  response gains auto_products {linked, created, needs_review}
```

### 4.2 Backfill

`POST /v1/products/autopopulate/backfill` starts a job over confirmed receipts. `dry_run:true` counts without writing. Idempotent and resumable (job payload stores the last receipt id).

### 4.3 Barcode scan creates the product (Unpack)

`POST /v1/barcodes/{code}/product` (idempotent: same code, same product).

1. **Known EAN** (a product with this `barcode`, not deleted/merged) → `200 {status:"known", product}`. No changes. This is today's behaviour for `GET /v1/barcodes/{code}`, which stays as it is.
2. **Unknown EAN.** Look up OFF (existing client, timeout 5 s, 24 h negative cache already exists):
   - **Found:** create the product from the prefill. `created_via='barcode'`, `name` = `product_name_nl` or `product_name` (brand not repeated if the name already starts with it), `brand`, `net_quantity`/`net_unit`, `off_code`, `barcode`, nutrition if present, `image_url` downloaded (existing behaviour), `name_en` per §3.5, category from `categories_tags` (§4.3.1) or the keyword rules. `needs_review=0` if a category was found, otherwise `1` with `review_reason='no_category'`. → `201 {status:"created"}`.
   - **Not found (OFF 404 / empty name):** create a minimal product: `name` = `"Unknown product " + last 4 digits`, `barcode`, `created_via='barcode'`, `needs_review=1`, `review_reason='barcode_minimal'`. → `201 {status:"created_minimal"}`. The app asks for a name (PATCH).
   - **OFF unreachable (timeout / 5xx):** same minimal product, `review_reason='barcode_off_unavailable'`, and the server queues job `off_refresh` (retry after 10 min, up to 5 tries). The retry fills **only empty fields** and never overwrites a name the user typed. → `201 {status:"created_minimal", off_status:"unavailable"}`.
3. After creating, compute `merge_suggestion`: best fuzzy match (score ≥ 0.75) of the new product's name against products that have **no barcode** and `created_via IN ('receipt','backfill','manual')`. Return it, set `suggested_merge_id`, and if found set `review_reason='barcode_name_match'` (overrides `no_category`; the category question is asked in the same card).
4. Return up to 5 `unmatched_lines` (same ranking as `GET /v1/products/{id}/unmatched-lines?days=14`, C.3.3).

**4.3.1 OFF category mapping** (`categories_tags` prefix → our category; first match in this order): `en:frozen` → Frozen; `en:beverages`, `en:waters`, `en:sodas` → Drinks; `en:dairies`, `en:milks`, `en:cheeses`, `en:yogurts`, `en:eggs` → Dairy; `en:meats`, `en:fishes`, `en:seafood`, `en:poultry` → Meat & fish; `en:breads`, `en:pastries`, `en:biscuits-and-cakes` → Bakery; `en:fruits`, `en:vegetables`, `en:plant-based-foods` (only when also `en:fresh`) → Produce; `en:cleaning`, `en:household` → Household; `en:hygiene`, `en:cosmetics` → Personal care; `en:snacks`, `en:groceries`, `en:sauces`, `en:cereals-and-potatoes`, `en:canned-foods` → Pantry. No match → keyword rules → none. The backend adds `categories_tags` as `category_hint` (raw list) on `OpenFoodFactsPrefill`.

**How a barcode product later links to receipt lines**
- Receipt lines never carry barcodes. Linking is by name/alias: when a line is linked to the barcode product (via the C.3.3 sheet, the review card, or `PUT /v1/lines/{id}/product` with `remember` true) the alias `(shop, norm)` is written, and future receipts link to it automatically.
- The response carries the unmatched lines from the last 14 days (`unmatched_lines`, C.3.3). Per the design, the app does **not** pop a sheet after each scan: the scanner row shows an "N on receipts" link and the Done summary offers the same list, so scanning is never interrupted. Choosing a line links it and writes the alias.
- Limitation: OFF names (`Halfvolle melk`) often score below 0.75 against receipt text (`AH HALFV MELK`). That is why C.3.3 exists: the user links once, the alias handles the rest.

**How it avoids duplicating a product receipts already created**
- Nothing is forced. The response carries `merge_suggestion` (§4.3 step 3) and the review queue shows the same suggestion.
- On accept, `POST /v1/products/{id}/merge` runs with the **barcode product as survivor** (`into`): it keeps its barcode, OFF name/nutrition/image; it takes over the receipt-created product's lines and aliases. A user-typed `name_en` (`manual`) on either side wins.
- If the user declines ("keep separate"), the pair goes into `product_distinct`.
- Also, if a later `PATCH` or `POST /v1/products` supplies a barcode that already exists, the existing `409` stays.

### 4.4 Review queue and undo

- Reasons (in queue order): `near_match`, `barcode_name_match`, `odd_name`, `no_category`, `barcode_minimal`, `barcode_off_unavailable`. Within a reason, newest first.
- Actions per product (`POST /v1/products/{id}/review`): `accept` (clear the flag, optionally set a category/name), `merge` (into `suggested_merge_id` or another product), `keep_separate`, `discard` (only for `created_via != 'manual'`: soft-delete the product, unlink its lines, remove its aliases).
- **Undo.** `merge` and `discard` return an undo handle (`merge_id`, or `discard_id` for discard). The app shows an undo toast. Undo is exact for 30 days. Anything undone twice returns `409`.
- English is *not* part of this queue; unknown words stay in the existing "needs English" queue.

## 5. Data model

Migration: next free number (expected `00011_auto_products.sql`), goose format, with a Down.

```sql
ALTER TABLE products ADD COLUMN created_via TEXT NOT NULL DEFAULT 'manual'
  CHECK (created_via IN ('manual','receipt','backfill','barcode'));
ALTER TABLE products ADD COLUMN needs_review INTEGER NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN review_reason TEXT
  CHECK (review_reason IN ('near_match','barcode_name_match','odd_name','no_category',
                           'barcode_minimal','barcode_off_unavailable'));
ALTER TABLE products ADD COLUMN suggested_merge_id INTEGER REFERENCES products(id);
ALTER TABLE products ADD COLUMN merged_into_id INTEGER REFERENCES products(id);  -- set on merge; row also gets deleted_at
ALTER TABLE products ADD COLUMN category_source TEXT
  CHECK (category_source IN ('rule','off','manual'));
CREATE INDEX products_review ON products(needs_review) WHERE needs_review = 1;

CREATE TABLE product_merges (
  id INTEGER PRIMARY KEY,
  from_product_id INTEGER NOT NULL REFERENCES products(id),
  into_product_id INTEGER NOT NULL REFERENCES products(id),
  line_ids TEXT NOT NULL,            -- JSON array of receipt_lines moved
  alias_ids TEXT NOT NULL,           -- JSON array of product_aliases moved
  from_review_before TEXT NOT NULL,  -- JSON of the `from` product's needs_review, review_reason, suggested_merge_id, so undo restores its queue card
  into_fields_before TEXT NOT NULL,  -- JSON of the `into` fields that were empty and got filled (barcode, brand, image_path, net_quantity, net_unit, off_code, category_id, nutrition)
  kind TEXT NOT NULL DEFAULT 'merge' CHECK (kind IN ('merge','discard','split')),
  created_at TEXT NOT NULL,
  undone_at TEXT);

CREATE TABLE product_distinct (
  a_id INTEGER NOT NULL REFERENCES products(id),
  b_id INTEGER NOT NULL REFERENCES products(id),
  created_at TEXT NOT NULL,
  PRIMARY KEY (a_id, b_id), CHECK (a_id < b_id));

CREATE TABLE category_keywords (    -- user terms; the built-in seed is embedded
  keyword TEXT PRIMARY KEY,         -- folded
  category_id INTEGER NOT NULL REFERENCES categories(id),
  updated_at TEXT NOT NULL);
```

Backfill state lives in the existing `jobs` table (`kind='autopop_backfill'`, payload with `dry_run`, `last_receipt_id`, counters). The `off_refresh` job also uses `jobs`.

Existing rows: `created_via` defaults to `manual`, nothing is flagged.

## 6. API

All under `/v1`, Bearer auth, errors as `application/problem+json` as in `openapi.yaml`. Dates are RFC 3339. New schema pieces must be added to `openapi.yaml` (it is the source of truth) and the MCP tools.

`Product` gains: `created_via`, `needs_review` (bool), `review_reason` (nullable), `suggested_merge` (nullable object `{product_id, name, name_en, brand, score}`; `suggested_merge_id` stays in the database), `category_source` (nullable), and `image_url` (nullable; the path of `GET /v1/products/{id}/image` when `has_image`). `barcode`, `net_quantity`, `net_unit`, `category_id`, `created_at` already exist. The app resolves the category name from `/v1/categories`.

### 6.1 Receipt confirm (changed)

`POST /v1/receipts/{id}/confirm` returns the Receipt as today plus, when the flag is on:

```json
"auto_products": {"linked": 6, "created": 3, "needs_review": 2, "error": null}
```

### 6.2 Barcode create

`POST /v1/barcodes/{code}/product`, no body. `code` as in `GET /v1/barcodes/{code}` (8 or 12–14 digits).

```json
// 200 known | 201 created | 201 created_minimal
{
  "status": "known" | "created" | "created_minimal",
  "created_now": true | false,                            // false on every 200 (repeat or known); true only on the 201 that created it
  "off_status": "found" | "not_found" | "unavailable",   // omitted when status = known
  "product": { ...Product },
  "merge_suggestion": {"product_id": 41, "name": "AH BIO MELK", "name_en": "AH organic milk", "score": 0.84} | null,
  "unmatched_lines": [ ...UnmatchedLine ]                  // max 5, last 14 days, ranked
}
```
When `status` is `known`, `merge_suggestion` is explicitly `null` and `unmatched_lines` is `[]`.

The call is idempotent. The app may send it hours later and more than once (offline queue): a repeat returns `200 known` with `created_now:false`. A client that lost the original `201` can tell "I just created this" from "already known" by `product.created_at` (a product created via `barcode` in the last 24 h that the client has queued is its own), or by sending the optional header `Idempotency-Key: <uuid>`: with the header, the server stores the first response for 24 h and replays it (same status and body) on repeats. The header is nice to have; `created_now` is required.

`POST /v1/products/{id}/refresh` (optional): retry Open Food Facts now for a product with `review_reason='barcode_off_unavailable'` or `'barcode_minimal'`. Fills only empty fields. `200 {product, off_status}`; `422` if the product has no barcode.
Errors: `400` bad code, `503` only when the database write fails (OFF failures never fail the request).

### 6.3 Review queue

`GET /v1/products/review-queue?limit=50&offset=0&reason=`

```json
{
  "total": 12,
  "counts": {"near_match": 3, "barcode_name_match": 1, "odd_name": 2, "no_category": 5, "barcode_minimal": 1, "barcode_off_unavailable": 0},
  "items": [{
    "product": { ...Product },
    "reason": "near_match",
    "suggested_merge": {"product_id": 7, "name": "ARLA MELK", "name_en": "ARLA milk", "brand": null, "score": 0.87} | null,
    "source_line_count": 3,
    "source_lines": [{"line_id": 310, "receipt_id": 12, "raw_text": "ARLA BIO HALFV. MELK", "shop_name": "Albert Heijn", "purchased_at": "2026-06-27T12:00:00Z", "line_total_cents": 189}]
  }]
}
```
`source_lines` has at most 3 entries; `source_line_count` is the full count (the card shows "+2 more receipts"). `counts` are per-reason totals for the whole queue (not the page). `limit=0` returns `total` and `counts` only, from a count query (the app uses it for the catalog badge, so it must be cheap). Sorted per §4.4.

`POST /v1/products/{id}/review`

```json
{"action": "accept" | "merge" | "keep_separate" | "discard",
 "category_id": 3,                // optional, accept only
 "name": "Arla halfvolle melk",   // optional, accept only
 "into_product_id": 7}            // merge only (default: suggested_merge_id)
```
**Merge direction.** The card's product is the one the queue is about. For `near_match` the card's product disappears into the suggestion (`into` = the suggestion). For `barcode_name_match` the card's product is the barcode product and it **survives**: the suggested receipt-created product is moved into it (`from` = the suggestion, `into` = the card's product). In both cases the response `product` is the survivor.
`accept` takes `category_id` and `name` together (both optional, either or both); setting a category sets `category_source='manual'`.

Responses: `accept`/`keep_separate` → `200 {product}`. `merge` → `200 {merge_id, kind:"merge", product (the survivor), merged_away: {id, name}, lines_moved, aliases_moved}`. `discard` → `200 {discard_id, kind:"discard", lines_unlinked}`. `409` if the product is no longer in the queue; `422` if `discard` on a manual product.

### 6.4 Merge, undo, split

`POST /v1/products/{id}/merge` body `{"into_product_id": 7}` — `{id}` is the product that disappears. Moves lines and aliases; copies empty fields (barcode, brand, image, net quantity, off code, category, nutrition) to `into`; keeps `into`'s name and any `manual` name_en; soft-deletes `{id}` with `merged_into_id`. Search index refreshed. Response `200 {merge_id, product, lines_moved, aliases_moved}`. `409` when both products have different barcodes (title: "These two have different barcodes.", user-safe text; the app never offers such a pair), `422` when `id == into`.

`POST /v1/product-merges/{id}/undo` → `200 {kind, product (restored), into (survivor), lines_restored, lines_skipped}`. The restored `product` keeps its review state (`needs_review`, `review_reason`, `suggested_merge`), so the app can put the card back in the queue without another fetch. `kind` is `merge`, `discard` or `split`. Restores the lines and aliases recorded in the merge, clears the `into` fields it filled, un-deletes the product. `lines_skipped` counts lines the user has relinked since. `409` if already undone or older than 30 days. Works for `kind` merge and discard.

`POST /v1/lines/{id}/split-product` body `{"name": "optional"}`. Creates a new product from the line (`created_via='manual'`, category via §3.4), moves the alias for this line's `(shop, norm)` to the new product and relinks every line with the same shop and text that points at the old product. Response `201 {product, lines_moved, merge_id}` (`kind='split'`, so it can be undone with the undo endpoint). `404` if the line has no product, `422` if it is not `kind='item'`.

### 6.5 Backfill

`POST /v1/products/autopopulate/backfill` body `{"dry_run": true|false}` → `202 {job_id}`. `409` if one is already running.
`GET /v1/products/autopopulate/backfill` → `{state: "idle"|"running"|"done"|"failed", dry_run, receipts_total, receipts_done, linked, created, needs_review, error}`.

### 6.6 Flag and config

`AUTO_PRODUCTS=on|off` (default `off`). When off: confirm does nothing new, the backfill endpoint returns `409`, `POST /barcodes/{code}/product` still works (barcode creation is an explicit user action) and the review queue still lists whatever is flagged. Document in `deploy/` env examples.

### 6.7 MCP

Add `review_new_products` (read: the queue) and `resolve_review(product_id, action, into_product_id?)` (write).

## 7. Who does what, in order

### Step 1: Design (design & experience agent)

Deliver as updates to the wireframes/mockups the project already has (same canvas and rules as `design/DESIGN-RULES.md`, motion per `design/MOTION.md`). Light and dark.

1. **Review queue screen** ("New products"): list of cards with the product, why it is here (the six reasons in plain words), and the actions: Accept, Same as X (merge), Keep separate, Discard, pick a category, edit the name. Show the receipt line it came from (shop, date, raw text). States: loading, empty ("All caught up"), one item, many, error/offline, and after an action (the card leaves, count updates). Barcode cards show the scanned code and the OFF photo.
2. **Undo toast** after merge and discard: text ("Merged into X"), Undo button, duration (proposed 8 s), where it sits above the tab bar, what happens if the user navigates away.
3. **Catalog badge**: count of products waiting, and where it appears (tab bar and/or catalog header). Empty = no badge.
4. **Scan creates a product (Unpack)**: in the continuous scanner, three outcomes: *known* (as today: success haptic, "✓ name"), *created with data* (proposed: success haptic and "Added X · Dairy"), *created minimal* (proposed: warning haptic and "New product, add a name"). Haptic types and timing are the design agent's call. **Decided in `docs/DESIGN-PRODUCT-AUTOPOPULATE.md`:** known plays `scanKnown`, created with data, or created minimal because OFF was unreachable (`status:"created_minimal"` with `off_status:"unavailable"`; the app chooses by `off_status`), plays `scanNew` (two quick ticks), created minimal with `off_status:"not_found"` plays `warning`; this replaces IOS-PLAN C.3 "new → warning haptic". The unmatched-lines sheet (C.3.3) does not pop after creation: an "N on receipts" link on the scanner row and the Done summary offer it. The inline merge prompt ("Same as AH BIO MELK?") and the session tally stay.
5. **Empty and failed OFF states**: OFF has nothing (ask for a name, take a photo), OFF unavailable (product created, "Details will fill in later", retry happens on the server), offline scanning (barcode queued on the phone, sent when online), duplicate scan of the same code (just "known").
6. Copy for the review reasons and statuses.

Done when: each state above exists as a frame in light and dark, the handoff names components/tokens, and the haptic mapping is a table the swift agent can implement.

### Step 2: Backend (backend agent)

1. Migration (§5), config flag, `openapi.yaml` and MCP additions (§6).
2. `create_or_link` (§3), noise filter, thresholds, category rules (embed the TSV), English rules, glossary additions and tests (§3.5).
3. Merge, undo-merge, split-line, review queue and review action, backfill job, `off_refresh` job, `POST /barcodes/{code}/product`, `category_hint` on the OFF prefill and its mapping (§4.3.1).
4. Tell the swift agent when the endpoints are live on the dev server.

**Acceptance criteria and test data:**
- Fixtures: the 13 OCR files `spike/out/IMG_1463.json` … `IMG_1475.json`, parsed with the current parser, confirmed in date order in a test database. Expected, from the simulation: 98 item lines; 25 linked automatically; no automatic link between any of the six wrong pairs in §2 (`AH BIO MELK`/`AH BIO SAP`, `ARLA BIO HALFV. MELK`/`ARLA MELK`, `AH BROCCOLI`/`AH BIO SAP`, `AH PEPERMIX`/`AH HAVERMOUT`, `AH PLUOT`/`-- AH SPRUITJES`, `KIP LOPPOOT`/`KIP POTEN`); `HHTHN AH ZALMFILET`, `HMHMNTT STARB COFFEE`, `TTNN FIT PIGGY` are cleaned and link to their clean twins; about 69–73 products exist at the end; no product is created from `3,170 kg x 5`; `NUNNUNONNTT BIO ZON OLIE` is flagged `odd_name`.
- Category test: `spike/autopop/cat_eval.py` logic ported to Go reproduces 50 correct / 1 wrong / 16 no hit on `spike/autopop/truth.py`.
- Idempotence: confirming twice, and running the backfill twice, create zero new products the second time.
- Merge/undo: merge `ARLA MELK` into `ARLA BIO HALFV. MELK`'s product and undo: lines, aliases, empty fields and `deleted_at` return to the exact earlier state (compare row dumps). Undo twice → `409`. A line relinked after the merge shows up in `lines_skipped`.
- Barcode, using recorded OFF responses served by a fake OFF server (no live calls in tests): `found-full` (name, brand, quantity, nutrition, category tags) → `201 created`, category set; `found-no-category` → `created` with `no_category`; `not-found` → `created_minimal`/`barcode_minimal`; `timeout` and `503` → `created_minimal`/`off_status:"unavailable"` and an `off_refresh` job; repeat the same code → `200 known`. Record the fixtures from one real scan of a Dutch grocery product and one non-food code (for example `4006381333931`, which I expect not to be in OFF: verify when recording). Use EAN-13 codes with valid check digits.
- Barcode vs receipts: with a receipt-created `AH BIO MELK` and no barcode, creating a barcode product with a similar OFF name returns `merge_suggestion`, `suggested_merge_id` set, and nothing merged. Accepting merges with the barcode product as survivor and keeps its barcode.
- Flag: with `AUTO_PRODUCTS=off`, confirm creates nothing; with `on`, `auto_products` is in the response.
- `go test ./...` passes; `make` targets for OpenAPI lint pass if they exist.

### Step 3: Swift (swift app agent)

1. API client types and calls for §6; queue count for the badge (`review-queue?limit=0`).
2. Review queue screen from the design, actions, undo toast wired to the undo endpoints.
3. Unpack: call `POST /v1/barcodes/{code}/product` for unknown scans (known scans keep the existing path), haptics and feedback per the design table, merge prompt, C.3.3 list from `unmatched_lines` via the "N on receipts" link and the Done summary, minimal-product name prompt, offline queue for scans.
4. Catalog badge.
5. Confirm flow: show a small "3 new products" note from `auto_products`, linking to the queue.

**Acceptance criteria:**
- Against a dev server loaded with the §7 step 2 fixtures: the badge count equals `total`; each action removes the card and updates the count; merge shows the undo toast and undo restores the card.
- Scanning an unknown code: created-with-data and created-minimal paths show the designed feedback and the right haptic; scanning it again reports "known"; with the server unreachable, the scan is queued and sent later without creating a duplicate.
- The merge prompt appears only when `merge_suggestion` is present and never merges without a tap.
- Runs in the iOS Simulator: screenshots of each state in light and dark attached to the handoff.

## 8. Rollout and checks before the flag goes on

1. Run the backfill with `dry_run:true` on the real database and read the counts (expected shape: few creations per receipt after the first week, queue about a third of the products).
2. Confirm 5 new receipts with the flag on; review the queue by hand.
3. Only then set `AUTO_PRODUCTS=on` in `deploy/` and run the real backfill.

## 9. Risks and open points

- Only 13 receipts and two chains were tested; Jumbo, Lidl and market formats are unknown. The thresholds (0.92 / 0.75) held with zero wrong auto-links here, but re-check on the first 50 real receipts.
- Wrong merges are the expensive failure (price history). The 0.92 line, the review queue, and the 30-day undo log exist for that.
- A receipt with a total mismatch should be corrected before confirming; confirm is what creates products.
- OFF names may rarely match receipt text. The expected result is a few extra products in the queue, not wrong links.
- Category labels in the evidence are mine, not the user's; the user should check the 50/1/16 numbers against their own view.
- Open question for the user, not blocking: should brand-only identity entries (`coca-cola`) live in the built-in glossary or in user terms? This spec puts them in the built-in file.
