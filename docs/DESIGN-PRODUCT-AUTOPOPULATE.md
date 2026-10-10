# Design: product auto-population (review queue, undo, scan creates products)

Status: **spec for backend and swift** (design/experience agent, 2026-10-10). Input: [PRODUCT-AUTOPOPULATE.md](PRODUCT-AUTOPOPULATE.md) (§1, §4.3–4.4, §6, §7 Step 1). Follows [DESIGN-SYSTEM.md](DESIGN-SYSTEM.md), [design/DESIGN-RULES.md](../design/DESIGN-RULES.md) and [design/MOTION.md](../design/MOTION.md). Components are built and compile: `Design/Components/ProductReview.swift` (review card, skeleton, scanner rows, tally), the undo action on `ToastCenter` (`Design/Components/Banners.swift`), previews in the Catalog (**Review queue** and **Scanner rows** sections, light and dark through the Catalog's theme switch).

Frames (captured from the Catalog on an iPhone 17 simulator, both themes): `docs/design-frames/product-autopopulate/`
`review-cards-light.jpg`, `review-cards-dark.jpg`, `scanner-rows-light.jpg`, `scanner-rows-and-undo-toast-dark.jpg`. The scanner tray is always dark (camera surface), so its two frames differ only in the page behind it. The status bar overlapping the top edge in the frames is the simulator, not the design.

## 0. Principles

1. **Nothing uncertain is merged silently, and nothing is blocking.** Products fill themselves; the queue is a place to look when you feel like it, never a gate. Scanning and receipt confirm never stop for a question.
2. **One tap per decision.** Every card offers its most likely answer as the big button.
3. **Every merge and discard can be taken back** for 30 days, with an Undo that is easy to reach for the first 8 seconds.
4. **Amber means "look at this"**; blue means "information, nothing wrong" (for example "details will fill in later"); green means done.
5. **One haptic per action**, outcomes only once.

## 1. Where it lives

| Surface | What the user sees | Detail |
|---|---|---|
| **Products tab** | Amber `CountBadge` on the tab-bar icon (the existing `TabBarItem.badge`), and an `AttentionCard(count: n, title: "New products to check", hint: "about 2 min")` at the top of the Products list, above the search field | Both hidden at 0. Count = `GET /v1/products/review-queue?limit=0` → `total`. `99+` above 99. |
| **Receipt confirm** | Toast **Receipt confirmed · 3 new products, 2 to check** with a trailing **Review** button (blue) | Only when `auto_products.created > 0`. Without new products the toast stays **Receipt confirmed**. **Review** opens the queue. 4 s. |
| **Unpack, on Done** | Summary tray with the tally and **Review N new products** when any created product still needs a look | §5.5 |
| **Home** | Nothing. Home's amber card stays for *receipts*; products are not mixed into it (two numbers on one card would blur what "review" means) | |

Count refresh: on app foreground, after a receipt confirm (`auto_products.needs_review` is added immediately, then verified), after every scan session ends, and after every action in the queue (the queue itself carries `total`).

## 2. Review queue screen: "New products"

A **pushed full screen** (MOTION: long scrolling list with per-item actions), opened from the Products attention card, the confirm toast and the Unpack summary. Pull to refresh.

```
‹  New products                       12      ← NavHeader + count (ink3), count rolls
┌───────────────────────────────────────┐
│ ⚠ Might be the same product · 3       │  ← section header (DaySectionHeader style), reason headline + count
│ ┌───────────────────────────────────┐ │
│ │ [tile] Arla bio halfvolle melk  ⋯ │ │  ← ReviewCard
│ │        1 l · ● Zuivel             │ │
│ │ 🔗 Might be the same product       │ │
│ │ Looks like ARLA MELK. Same …?     │ │
│ │ ┌ On Albert Heijn · Sun 30 Aug ─┐ │ │  ← source receipt line (mono)
│ │ │ ARLA BIO HALFV. MELK     1,89 │ │ │
│ │ └───────────────────────────────┘ │ │
│ │ [ Same as ARLA MELK ] [Keep sep.] │ │  ← primary + secondary
│ └───────────────────────────────────┘ │
│  …cards, 12 pt apart…                 │
└───────────────────────────────────────┘
```

### Order and sections
Server order (spec §4.4): `near_match`, `barcode_name_match`, `odd_name`, `no_category`, `barcode_minimal`, `barcode_off_unavailable`, newest first within a reason. Sections appear only for reasons that have items, with the headline from the copy table and the count. Section headers are sticky. Page size 50 (`limit=50&offset`), "load more" fires when the last card is 3 cards from the bottom (a small `ProgressView` row meanwhile).

### The card (`ReviewCard`)
- **Header**: `ProductTile` 44 pt (the Open Food Facts photo when `image_url` exists, otherwise the three-letter tile in the category tint), name (`rowTitle`, up to 2 lines), size/detail (`rowSub`), category chip (dot + name, tap opens the category tray) or an amber **No category** chip, and a **⋯** icon button (32 pt) that opens the options tray.
- **Why it is here**: icon + headline (`captionMedium`, tone colour) and one explanatory sentence (`rowSub`, `ink2`).
- **Where it came from**: receipt-born products show the **source line** as a `RawRowTile` on the page background: caption `On <shop> · <date>`, raw mono text, line amount. Barcode-born products show **Scanned barcode** with the code in mono (and the OFF photo in the tile). If more than one source line exists, a caption **+2 more receipts** under the tile (needs `source_line_count`, see §10).
- **Buttons**: primary (`.shop` small, `medium` haptic on touch-down), secondary (tinted on the page background, `selection`). The two sit in one row; if the primary label does not fit (`Same as ARLA BIO HALFVOLLE MELK`) they stack (`ViewThatFits`).
- **Options tray (⋯)**: `OptionRow`s: **Edit name**, **Pick category**, **Discard** (red tint, hidden for `created_via = 'manual'`). The tray replaces itself for the name field or category picker (tray rule), never stacks.

### Buttons by reason

| Reason | Primary (API) | Secondary (API) | In the ⋯ tray |
|---|---|---|---|
| `near_match` | **Same as {suggested name}** → `merge` into `suggested_merge.product_id` | **Keep separate** → `keep_separate` | Edit name, Pick category, Discard |
| `barcode_name_match` | **Same as {suggested name}** → `merge` (the barcode product survives, see §10) | **Keep separate** → `keep_separate` | Edit name, Pick category, Discard |
| `odd_name` | **Looks right** → `accept` | **Edit name** → name tray, then `accept` with `name` | Pick category, Discard |
| `no_category` | **Accept** → `accept` (category stays empty) | **Pick category** → category tray, then `accept` with `category_id` | Edit name, Discard |
| `barcode_minimal` | **Name it** → name tray, then `accept` with `name` | **Keep as is** → `accept` | Pick category, Discard |
| `barcode_off_unavailable` | **Accept** → `accept` | **Edit name** → name tray, then `accept` with `name` | Pick category, Discard |

Choosing a category on any card sets it locally (the chip changes with a 160 ms cross-fade) and is sent with the next `accept`/`merge`-less action; if the user picks a category and then taps **Keep separate**, the app sends `PATCH /v1/products/{id}` first, then the review action. A merge ignores a locally picked category (the survivor keeps its own).

### Name tray (replaces the options tray)
Header **Name this product**; subtitle shows the barcode (mono) when there is one. `Field` (autofocus, capitalised words, return = Save), primary **Save** (`medium`), text **Cancel**. For `barcode_minimal` the field is empty with placeholder **What is it?** (not the "Unknown product 2345" placeholder name). A small **Take a photo** link under the field opens the camera and `PUT /v1/products/{id}/image` (already in the API), shown only for barcode-born products without an image.

### Category tray
The existing category list (`CategoryDot` + name rows, 8 visible, scroll), current one checked. Choosing a row dismisses and updates the chip (`selection`). For `no_category` cards the tray is the secondary action, so choosing a category does **not** auto-accept: the card now shows the category and the primary becomes **Accept** (it already is). One more tap, intentionally: a category pick is a decision about data, accepting is the confirmation.

### States

| State | What the user sees |
|---|---|
| **Loading** (first fetch) | Three `ReviewCardSkeleton`s (same footprint as the real cards, so they swap in place: no jump, no stagger). Header count placeholder hidden. |
| **Empty** ("All caught up") | `EmptyState` with a green check `GlyphIllustration`, title **All caught up**, message **New products show up here when Shop isn't sure about one. Scan or confirm a receipt and they'll appear.** No action. Header count gone; Products badge and attention card gone. If the user got here by finishing the last card, the last card collapses (240 ms), then the empty state fades in (160 ms) and a single `success` plays (§9). |
| **One item** | One card, no section header repeated beyond the one. The header count reads **1**. |
| **Many** | As above, sections sticky. |
| **After an action** | The card shows its spinner on the primary button and dims to 70 % (`isWorking`), buttons disabled. On success the card **collapses** over 240 ms (`Motion.collapse`), the section count and the header count roll (`numericText`), the tab-bar badge rolls. Merge and discard show the **undo toast**. Accept and keep separate show no toast (the card leaving is the confirmation). |
| **Action fails** (5xx, timeout) | Card returns to idle, `error` haptic, toast **Couldn't {merge / save / discard}. Try again.** |
| **409 (no longer in the queue)** | The card collapses quietly and a toast **Already handled** (no haptic), the list refetches. |
| **Error on load** | A red `Banner` (glyph alert) **Couldn't load new products.** with **Retry**; existing cards stay if there were any. |
| **Offline** | Cached cards stay readable; a neutral banner **Offline. You can review again when you're back online.**; every card shows **Needs a connection** and its buttons are disabled (`isOffline`). Review actions are not queued offline: they depend on the server's current state (a suggested merge may be gone). |
| **Undo restores a card** | The card re-expands at its old position (360 ms height spring), count rolls back up. |

## 3. Undo

### Toast (`ToastCenter.undoable(_:run:)`)
- **Text** (what survived, not just "merged"):
  - near_match merge: **Merged into {survivor name}**
  - barcode_name_match merge: **Merged {old name} into {survivor name}**
  - discard: **Discarded {name}**
  - split a line: **New product {name} · {n} lines moved**
- **Button**: **Undo** (blue, 36 pt tap height, trailing inside the capsule). Plays `soft` on touch-down (a restore, not an outcome), closes the toast, shows a spinner-less quick state (the card re-expands as feedback).
- **Duration**: **8 seconds** (agreed with research). No countdown ring; the toast simply fades with `ease`.
- **Placement**: the existing toast slot, 96 pt above the bottom edge (above the tab bar on tab screens; above the safe area on pushed screens such as the queue). One toast at a time. Tapping the toast body dismisses it (existing behaviour).
- **Navigating away**: the toast lives at the app root, so it **keeps counting** while the user changes screens or tabs and **Undo still works** from anywhere for its 8 seconds. After that, the 30-day undo exists on the server but has no entry in the UI yet (see §11, later).
- **A second action while a toast is open**: the new toast replaces the old one (the old action is no longer offered in the UI; the server still has it for 30 days).
- **Result of tapping Undo**:
  - success: toast **Restored {name}** (2.6 s, no action, no haptic); if `lines_skipped > 0`: **Restored {name} · {n} lines kept their new product**.
  - `409` (already undone, or older than 30 days): toast **Can't undo this one anymore**, `warning` haptic (nothing is broken, it is just late).
  - network error: toast **Couldn't undo. Try again.** with the Undo button kept for the remaining time, `error` haptic.
- A restored product returns to the review queue exactly as it was (needs `needs_review` kept, §10).

### Split a receipt line into its own product
Reached from the receipt's **Link product** tray and from a line's options tray: **This isn't the same product** (hint **Moves other lines with this text too**), shown only on a linked item line. One tap, no confirmation (undo exists): the line gets its own new product (`POST /v1/lines/{id}/split-product`), and the toast says **New product {name} · {n} lines moved** with **Undo** (same undo endpoint, `kind='split'`). The new product is `created_via='manual'` and not queued for review. If the name is poor the toast's Undo is not the way to fix it; the line's **Edit name** is.

## 4. Catalog badge

See §1. Specifics for the tab bar: `TabBarItem(id: .products, …, badge: reviewCount)` reuses the amber `CountBadge` ring (2 pt `tray` ring) already used on Home; it appears with the `pop` spring and rolls on change. The Products `AttentionCard` appears at the top of the list, under the large title and above the search field, and animates in/out with `Motion.collapse`. Tapping either goes to the queue. VoiceOver: tab reads **Products, 12 to review**.

## 5. Scan creates a product (Unpack)

This changes IOS-PLAN C.3 ("new → warning haptic", add-product tray after each new barcode). Since a scan now *creates* the product, the scanner never stops for a form.

### 5.1 Session tray
The dark session tray keeps its layout. New: a **tally line** under "This session · 14 scanned": `ScanTally` (green dot **9 known**, blue dot **4 added**, amber dot **1 to name**; the amber item only appears when something needs a name). Numbers roll as results land.

### 5.2 Outcomes (`ScanEntryRow`, one row per distinct code)

| Outcome | Row | Subtitle | Trailing |
|---|---|---|---|
| **Looking up** | spinner tile, **Looking up…** | the code | none |
| **Known** (`status: known`) | green check tile, product name | **Known · 1,5 l** | none (known products return no receipt lines) |
| **Created with data** (`status: created`, `off_status: found`) | green **+** tile, product name | **Added · Zuivel** (category) or **Added · Open Food Facts** when no category | **N on receipts** (blue) when `unmatched_lines` is non-empty |
| **Created, minimal** (`created_minimal`, `off_status: not_found`) | amber **+** tile, **Unknown product 2345** | **New · add a name** (amber) | **Name it** (primary pill) |
| **Created, OFF unavailable** (`created_minimal`, `off_status: unavailable`) | blue **clock** tile, **Unknown product 9876** | **Added · details fill in later** | **Add name** (quiet pill) |
| **Queued offline** | grey **wifi.slash** tile, the code in mono | **Waiting for a connection** | none |
| **Failed** (503) | red retry tile, **Couldn't look up** | the message (red) | **Retry** |

### 5.3 Merge prompt (inline, never a tray)
When the response has a `merge_suggestion`, the row grows by one inline line (height spring 360 ms): blue-tinted strip **Same as AH BIO MELK?** with **No** (`selection`) and **Merge** (`medium`). It does not block scanning; it stays until answered or the session ends. **Merge** calls `POST /v1/products/{id}/merge` with the **scanned (barcode) product as survivor** (spec §4.3), the strip collapses, the row becomes a normal "Added" row, and the undo toast appears (**Merged AH BIO MELK into {name}**). **No** calls `keep_separate` quietly. If the scan ends without an answer, the product stays in the review queue with reason `barcode_name_match`, which is why a missed prompt costs nothing.

### 5.4 Receipt lines (C.3.3), changed from "show after creating"
The unmatched-lines sheet no longer pops after every creation: during a session it would interrupt scanning again and again. Instead the row shows the blue **N on receipts** link; tapping it opens the existing `LinkLineTray` for that product. At the end of the session the **summary** (§5.5) offers it for the products that have lines.

### 5.5 Done → summary tray
Tapping **Done** replaces the session tray with a short summary (same dark surface, `heightSpring`): three `StatCard`-style cells **9 known · 4 added · 1 to name**, then, if any created product still needs a look, the primary **Review 5 new products** (opens the queue), otherwise **Done**. If created products have unmatched receipt lines, a secondary line **Link receipt lines (3)** walks through them one `LinkLineTray` at a time. With nothing created, **Done** closes straight away (no summary), as today.

### 5.6 Haptics and timing for scan outcomes

| Outcome | Haptic (from `Design/Haptics.swift`) | When |
|---|---|---|
| Known | `scanKnown` (one crisp rigid tick) | when the result lands (for a catalog hit, immediately) |
| Created with data | `scanNew` (light then firm tick) | when the `201` lands |
| Created, minimal | `warning` | when the `201` lands (a name is needed) |
| Created, OFF unavailable | `scanNew` | when the `201` lands (created fine; nothing to do now) |
| Same code again within 2.5 s | none | the row only moves to the top |
| Same code again after 2.5 s | `scanKnown` | the row bounces to the top (`pop`) |
| Queued offline | `uploadQueued` | when the code is stored on the phone |
| Queued code resolves later | none | (user is not touching the phone) |
| Failed | `error` | when it fails after retries; a `503` retry that succeeds plays its normal outcome |
| Merge (prompt) | `medium` on touch-down | one haptic, no extra success |
| No (prompt) | `selection` | |

The detection itself plays nothing: the row appears at once with its spinner (that is the visible feedback), and the outcome haptic lands with the result. This changes the old rule "new → warning": a product that was created with data is **not** a warning.

### 5.7 Empty and failed Open Food Facts states

| Situation | What happens in the UI | Copy |
|---|---|---|
| **OFF has nothing** (`created_minimal`, `not_found`) | Row amber, **Name it** opens the name tray in place (the scanner stays behind it, paused); a **Take a photo** link is offered. Product waits in the queue as `barcode_minimal` if left. | **New · add a name** · tray: **Name this product** |
| **OFF unavailable** (`created_minimal`, `unavailable`) | Row blue with a clock; **Add name** is optional because the server retries (`off_refresh`) and fills only empty fields, never a typed name. In the queue the card says **Details will fill in later**. | **Added · details fill in later** · queue: **Open Food Facts couldn't be reached. The server tries again soon.** |
| **Offline scan** | The code is stored on the phone and the row is grey; it is sent when the network returns (the `POST` is idempotent, same code = same product), then the row updates in place to its real outcome with no haptic and no motion beyond the 160 ms cross-fade. | **Waiting for a connection** |
| **Same code twice** | No new row; the existing row moves to the top. Known shows **Known**, a created product keeps **Added**. | (none) |
| **Barcode is not a valid EAN** (`400`) | Row red: **Not a valid barcode**, trailing **Remove** (no retry). | **Not a valid barcode** |
| **Server error** (503) | Red row with **Retry** (existing). | **Couldn't look up · Server not reachable** |

## 6. Review reasons (copy)

| Reason | Headline | Explanation (`{x}` = suggested product) |
|---|---|---|
| `near_match` | Might be the same product | Looks like {x}. Same product, or a different one? |
| `barcode_name_match` | Matches a product from your receipts | This barcode looks like {x} from your receipts. |
| `odd_name` | The name looks garbled | The receipt text was hard to read. Check the name. |
| `no_category` | No category yet | Pick a category so it counts in your analytics. |
| `barcode_minimal` | Not on Open Food Facts | Give it a name so you can find it later. |
| `barcode_off_unavailable` | Details will fill in later | Open Food Facts couldn't be reached. The server tries again soon. |

Source line caption: **On {shop} · {Sun 30 Aug}**. Barcode caption: **Scanned barcode**. Unknown product name stays **Unknown product 2345** (last four digits) until named.

## 7. Motion

| Element | Motion |
|---|---|
| Card leaves | 240 ms collapse; counts roll 180 ms |
| Card restored by undo | 360 ms height spring |
| Skeleton to cards | replace in place, same footprint, no stagger |
| Toast | spring in (offset 16 + fade), `ease` out; Undo press `pillScale` |
| Scanner row appears | spring, 8 pt slide + fade (existing); outcome change cross-fades 160 ms |
| Merge prompt | height spring 360 ms, content fade 160 ms |
| Summary tray | tray-to-tray height morph 360 ms spring |
| Tab badge | `pop` spring in, number roll |
| Reduce Motion | no slides or springs; 120 ms fades; haptics unchanged |

## 8. Components and tokens

Done (in `Design/`, compile, Catalog previews):

| Component | File | Notes |
|---|---|---|
| `ReviewReasonKind` | `Components/ProductReview.swift` | raw values = API `review_reason`; headline, explanation, icon, tone, button labels |
| `ReviewCard`, `ReviewCardSkeleton` | same | props per §2; `isWorking`, `isOffline` |
| `ScanOutcome`, `ScanEntryRow` | same | dark-tray row with merge prompt and receipt-lines link |
| `ScanTally` | same | rolling counts |
| `ToastCenter.undoable(_:run:)`, `ToastMessage.action` | `Components/Banners.swift` | 8 s, Undo plays `soft` |

Reused: `AttentionCard` (amber), `TabBarItem.badge`, `OptionRow`, `TrayHeader`, `Field`, `Banner`, `EmptyState`, `ProductTile`, `RawRowTile`, `CategoryDot`, `StatCard`, `LinkLineTray` (Features), `ProductFormTray` (Features, for **Edit** beyond name).

Tokens: amber (`amberText/amberTint/amber solid`) for needs-look, blue for information and merge prompt, green for done; card = `sunk` radius `Radius.card` (26); nothing new in Colors or Typography.

To build in Features (not mine): the queue screen, name and category trays, the summary tray, the offline barcode queue, Products badge/card wiring, confirm toast.

## 9. Haptics master table

| Moment | Haptic |
|---|---|
| Accept / Same as X / Name it (touch-down on the primary) | `medium` |
| Keep separate / Edit name / Pick category / Keep as is (secondary) | `selection` |
| Discard (options tray row, red) | `light` on touch-down (undo exists) |
| Undo | `soft` |
| Choose a category in the tray | `selection` |
| Card leaves, count updates | none |
| Queue becomes empty after the last action | `success` once |
| Action fails | `error` |
| Late undo (409) | `warning` |
| Scan outcomes | §5.6 |
| Receipt confirm toast with new products | none beyond the existing confirm haptic |

## 10. Asks for backend (and spec edits)

1. **Merge direction.** `POST /products/{id}/review {action:"merge"}` says `{id}` disappears into `into_product_id`. For `barcode_name_match` the card is the *barcode* product, which must **survive** (spec §4.3). Please state: *`review` with `merge` on a `barcode_name_match` item keeps the card's product and moves the suggested product into it*; for `near_match` the card's product disappears into the suggestion. Either way the response `product` is the survivor and should also return `merged_away: {id, name}` so the toast can say **Merged {old} into {survivor}**.
2. **Queue payload extras**: `counts: {near_match: 3, …}` (per-reason totals, so sticky headers and the empty state need no paging), and per item `source_line_count` (the card shows **+2 more receipts**). `source_lines` stays at most 3.
3. **Product fields the cards need on `Product`**: `image_url` (OFF photo), `barcode`, `created_via` (to hide **Discard** for `manual`), `category_id` (the app resolves the name from `/v1/categories`, agreed with research), `net_quantity`+`net_unit` for the detail line.
4. **Undo restores the queue item.** After `undo`, the restored product must keep `needs_review` and its `review_reason` (and `suggested_merge_id`), so the app can re-insert the card. The response already returns `product` and `into`; please include `review_reason` and `suggested_merge` on `product` so no extra fetch is needed.
5. **Undo response**: keep `lines_skipped` (the UI shows it). Also return the same `kind` (`merge`/`discard`/`split`) so the toast can word the result.
6. **Review `accept` with category**: confirm `accept` accepts `category_id` **and** `name` together (the name tray uses both), and that `category_source` becomes `manual`.
7. **Optional, nice to have**: `POST /v1/products/{id}/refresh` to retry Open Food Facts now (a **Try now** link on `barcode_off_unavailable` cards). Without it the card just says the server will try again.
8. **Scan response when known**: include `merge_suggestion: null` and `unmatched_lines: []` explicitly (the app treats absent as empty; stating it avoids a decode branch).
9. **Offline barcode queue**: the app sends `POST /v1/barcodes/{code}/product` when the network returns, possibly hours later, several times if it retried. Keep it idempotent (as specced) and return `200 status: "known"` on a repeat so the row resolves to the right outcome; the app would then show **Added** if it first saw the `201`. A repeat after a lost response will therefore say `known` for a product the app just created: please add `created_at` to the product (or `created_in_this_request: false`) so the app can show **Added** vs **Known** correctly on a retry.
10. **Badge count**: `GET /v1/products/review-queue?limit=0` → `total` is enough; please make sure `limit=0` is cheap (a count query).
11. **`merge` of a barcode product into a receipt product** is a 409 when both have different barcodes (specced). The app never offers that pair; mention it only so the 409 message is user-safe: **These two have different barcodes.**

## 11. Later, not in this build
A "Recently changed" list (30-day undo for merges, discards and splits) for when the 8-second toast was missed; showing the product photo in more places; a setting to turn automatic creation off per source (receipts vs scans).

## 12. Acceptance checklist (swift)
1. Confirm a receipt with 3 new products and 2 uncertain: toast **Receipt confirmed · 3 new products, 2 to check** with **Review**; Products tab badge shows 2.
2. Open the queue: sections by reason, cards match §2, **Same as X** merges, the card collapses, toast **Merged into X** with **Undo**; Undo restores the card in place.
3. **Discard** a receipt-born product: toast and Undo work; the discard row is absent on a manual product.
4. Empty queue shows **All caught up**; badge and attention card gone.
5. Offline: cards stay, buttons disabled with **Needs a connection**.
6. Unpack: scan a known code (`scanKnown`), an unknown with OFF data (`scanNew`, **Added · Zuivel**), one OFF doesn't have (`warning`, **Name it**), one with OFF unreachable (`scanNew`, **details fill in later**); the same code twice in 2 s buzzes once.
7. A scan whose name matches a receipt product shows the inline **Same as … ?** strip; **Merge** keeps the barcode product and offers Undo.
8. Done with created products shows the summary and **Review N new products**.
9. Airplane mode scan: grey row **Waiting for a connection**; reconnect resolves it in place without a buzz.
