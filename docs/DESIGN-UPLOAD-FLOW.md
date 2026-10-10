# Design: importing receipts from Photos and Files

Status: **spec for build** (design/experience agent, 2026-10-10). Replaces the "Add photos" stopgap. Follows [DESIGN-SYSTEM.md](DESIGN-SYSTEM.md), [design/DESIGN-RULES.md](../design/DESIGN-RULES.md) and [design/MOTION.md](../design/MOTION.md). New components are already in `Design/Components/Import.swift` (with Catalog previews); this document says how screens use them.

## 0. Principles

1. **Importing is not capturing.** The scanner is about one receipt in front of you. Import is about receipts that already exist, often many, often old. It gets its own list screen, not the scanner's "shop tray over a camera backdrop".
2. **Ask as little as possible.** A photo of a receipt already contains the shop and the date. The server reads them. The default path is *pick photos, tap Import*. Shop and date are things the user can set, not things they must set.
3. **One decision per screen region.** Grouping (which photos are one receipt), shop, and date are three separate controls, each with a default, none blocking the others.
4. **Nothing is lost.** Everything picked is written to disk before the import screen closes. Offline, a crash, or a server error never costs the user a photo.
5. **Quiet success, loud failure.** One haptic and one card per batch, never per receipt.

## 1. What exists today (the stopgap)

Add tray → "Add photos" (orange card) → `PhotosPicker` (max 5, one receipt) → the scanner's "Which shop?" tray with a Date row → `Add receipt`. HEIC is converted to JPEG on the device. Multi-page = stitched tall JPEG (`Stitcher`). Upload goes through `UploadQueue` (disk-backed, background session, 5 s → 30 min backoff, 409 = duplicate). `POST /v1/receipts` currently **requires** `shop_id` and accepts an optional `purchased_at`.

What this spec changes: a source choice (Photos or Files), many receipts per import, explicit grouping, a shop that can be "let Shop find out", dates that default to "read from the receipt", a batch progress card on Home, and the "Unknown shop → suggested shop" card.

## 2. Entry points

| # | Where | What it looks like | Notes |
|---|---|---|---|
| E1 | **Add tray** (the + button) | Third card, replaces "Add photos": icon `image`, orange, **"Import receipts"**, caption **"From Photos or Files, old ones too"**. Same 96 pt height. | Opens S2 (source tray) which *replaces* the Add tray (heights differ, 360 ms morph). |
| E2 | **Home empty state** (no receipts yet) | Under the primary "Scan a receipt" button: a text link **"Import receipts from Photos"** (blue, `label`). | First-run users often have a camera roll full of receipts. Opens S2 directly. |
| E3 | **Upload queue screen** (Settings → Upload queue), empty state and nav-bar trailing icon button | Empty state action **"Import receipts"**; header trailing `IconButton(.plus)` when the list is non-empty. | The queue is where people look after "where did my photos go". Opens S2. |
| E4 | **Needs-review "No date found"** fix tray | Not an entry to import; it reuses the date options of S5. Listed so the date UI is consistent. | See §5. |
| E5 | *(later, not in this build)* iOS **Share sheet** extension: "Share to Shop" from Photos, Files, Mail | Needs an extension target and an app-group queue. | Land the flow first; the Import screen (S3) is the destination for shared items too. |

No entry in tab bars or Products/List: import is an Add action, nothing else.

## 3. Flow

```
 Add tray ──┐
 Home empty ─┼─▶ S2 Source tray ──▶ system picker (Photos / Files) ──▶ S3 Import screen (full screen)
 Queue ─────┘      (replaces tray)        (not ours)                     │  ├─ S4 Shop tray (per batch or per receipt)
                                                                          │  ├─ S5 Receipt details tray (date, shop, pages)
                                                                          │  └─ Import ▶ writes to disk, enqueues
                                                                          ▼
                                                  Home with UploadProgressCard (S6) ─▶ rows: Reading… → ok / needs review / Unknown shop (S7)
```

Tray or full screen (MOTION.md): S2, S4, S5 are **trays** (short decisions that return to where the user was). S3 is a **full screen** (`fullScreenCover`, like the scanner): it has a long scrolling list and its own sub-decisions. S3 hosts its own `trayHost`. A tray never opens a second tray on top: S4 and S5 *replace* each other inside S3.

## 4. Screens

### S1 · Add tray (updated)
Unchanged layout. Third card text: title **Import receipts**, caption **From Photos or Files, old ones too**. Last-scan caption stays. Tap: `Haptics` none on the card itself beyond its press style `.light` on touch-down (tray replacement does not play `trayOpen` again, see S2).

### S2 · Source tray
Replaces the Add tray in place (`TrayCoordinator.present` replacing, so `heightSpring`, 160 ms content cross-fade, **no** second `trayOpen`).

- Header: **Import receipts**, close.
- Two `OptionRow`s (52 pt each): `image` **Photo library** hint *Pick as many as you like*; `doc` **Files** hint *JPEG, PNG, HEIC or PDF*.
- Footnote (`rowSub`, `ink2`): **Receipts are read on your own server. Nothing leaves your home network.**
- Taps use `OptionRow` default `.light` on touch-down. The picker presenting is the system's own motion.
- Photo library: `PhotosPicker`, `selectionBehavior: .ordered`, `maxSelectionCount: 30`, `matching: .images`. No permission prompt (the picker is out of process).
- Files: `fileImporter`, `allowedContentTypes: [.jpeg, .png, .heic, .pdf]`, `allowsMultipleSelection: true`. PDFs are rendered page by page to JPEG on the device (PDFKit, 200 dpi, white background). A PDF's pages count as pages of **one** receipt by default (see grouping).
- Cancelling the system picker returns to the Add tray (same replace morph back).

### S3 · Import screen (full screen)

Layout, top to bottom, 20 pt gutters, `bg` background (this is a normal screen, not the dark scanner backdrop):

1. **Header** (`NavHeader`-style, not centered): close `IconButton(.close)` left; title **Import receipts**; right: count text `ink2` ("6 photos"). Close with unsaved picks asks nothing if untouched; if the user changed grouping/shop, a confirm tray "Discard 6 photos?" (destructive row **Discard**, secondary **Keep editing**).
2. **Grouping control**, only when ≥ 2 photos: `SegmentedControl` with **Separate receipts** | **One long receipt**. Default **Separate receipts**. "One long receipt" is disabled (40 % opacity) and shows the hint *Up to 5 pages* under the control when more than 5 photos are picked. A PDF import defaults to *One long receipt* for that file only.
3. **Batch shop row** (flat row, 52 pt, `trayField` rounded tile, like the Date row today): label **Shop**, value = `ShopMark` + name, or **Let Shop find out** (default if the backend supports it, see §9), or amber **Pick a shop**. Chevron. Tap → S4. Applies to every receipt without its own override.
4. **Receipt list**, one `ImportReceiptRow` per receipt, with a `JoinPill` sitting on the hairline between consecutive rows when grouping is *Separate*. First 8 rows stagger in (24 ms). Each row shows: page strip (up to 3 thumbs, 56 pt), title (**Receipt 3** or **Receipt 3 · 2 pages**), shop chip (amber *Pick a shop* if unset, otherwise mark + name), date chip (default **Date from receipt**), remove (✕). A **Straightened** chip on the strip (tap = use the original) shows it was auto-straightened; **Edges not found · Adjust** appears under the title when it wasn't (§5a).
   - Tapping the **page strip or title** opens S5 for that receipt (reorder/remove pages, shop, date).
   - **JoinPill** "Join pages" ⇄ "Same receipt": joins the photo above and below into one receipt. Disabled when the joined group would exceed 5 pages. Joining merges the two rows with a 360 ms height spring and the page strips slide together; the right-hand row's shop/date chips are dropped (the first row's win).
   - Switching the segmented control to *One long receipt* joins all (≤ 5); back to *Separate* splits all. Individual joins made by hand are kept until the control is used.
5. **Footer** (floating, same material as `ConfirmBar`, bottom safe area): one primary button **Import 6 receipts** (singular: **Import receipt**). The count rolls (`numericText`) as rows join or are removed. Under the button, one line of status text (`rowSub`):
   - normal: **Uploads in the background**
   - offline: grey dot **Offline · uploads when you're back online**
   - shop missing: amber **Choose a shop for 2 receipts** (button disabled, tapping the line scrolls to the first unset row and flashes its chip once)
   - unreadable files present: amber **2 files can't be read · remove them or import the rest** (button stays enabled; unreadable rows are skipped on import)

States:

| State | What the user sees |
|---|---|
| Loading thumbnails | Rows appear immediately with a `sunk` placeholder page strip and a small spinner; thumbnails fill in (no cross-fade stagger, replace in place). HEIC/PDF decode happens off the main thread. Footer button disabled with label **Reading photos…** until at least one row is ready. |
| Ready, one photo | No grouping control, no join pill; one row. Footer **Import receipt**. |
| Ready, several | As above. |
| Over the limit | Picker caps at 30. If a PDF brings the total over 30 pages, a `Banner` (amber, glyph alert) **Only the first 30 pages were added.** |
| Unreadable file | The row shows a red-tinted strip placeholder and **Can't read this file** in `redText`, with a **Remove** text button. Does not block the rest. |
| Duplicate inside the batch | Same image picked twice: the second row is dropped and a toast **Skipped a duplicate** (no haptic). (Server-side duplicates are handled by the queue, §6.) |
| All rows removed | Screen closes back to the Add tray. |
| Dark mode, Dynamic Type XL | Chips wrap under the title (two lines); page strip shrinks to 48 pt; footer button keeps 50 pt min height. |

### S4 · Shop tray (inside S3)
The existing "Which shop?" list, reused and extended:
- First row (when supported): **Let Shop find out** with `UnknownShopMark`, hint **Reads the shop name from the receipt**. Selected by default.
- Then shops (last used first, with "last used"), then **Other shop** (opens the existing `ShopFormTray`, replacing this tray).
- Header: **Which shop?** with subtitle when opened from a row: **Receipt 3 only**; from the batch row: **All receipts without their own shop**.
- Choosing a row plays `selection` and dismisses with `ease`; the chip/row updates with a 160 ms cross-fade.

### S5 · Receipt details tray (inside S3)
Opened from a row. Height ≈ 420 pt, then grows to ≈ 520 when the date picker is expanded (replace morph, not a second tray).
- Header: **Receipt 3** + subtitle **2 pages**.
- Pages: horizontal strip of thumbnails with a ✕ on each and **+ Add page** (opens the picker for one more page, up to 5). Drag to reorder (reorder steps play `selection`).
- Shop row (same as the batch row) → replaces this tray with S4 and returns here.
- **Date** segmented control, three segments: **From receipt** (default) | **Photo date** (disabled unless the photo carries a date; label shows e.g. **Photo · 3 Oct**) | **Pick…**. **Pick…** expands the graphical `DatePicker` below (`in: ...Date.now`, Amsterdam time zone, same as `ReceiptDateTray`) with time. A caption under the control explains the selection:
  - From receipt: **Shop reads the date printed on it. If there isn't one, you'll be asked later.**
  - Photo date: **The day you took this photo. It can differ from the day you shopped.**
  - Pick: no caption.
- **Straighten & crop** row (value **Automatic**, **Adjusted** or **Original**) → S8, see §5a. Hidden for PDFs and scanner pages.
- Primary **Done**; destructive text row **Remove this receipt**.

### S6 · Upload progress card (Home) and queue screen
See §6.

### S7 · After upload
See §7.

## 5. Dates (the hard part)

The truth about dates, in order of trust: **printed on the receipt** (OCR) > **date the user typed** > **photo capture date** (EXIF, when the picker keeps it; often missing or equal to a scan/export time) > upload time. The design follows that order:

1. **Default is "From receipt"** for single imports *and* batches. No date prompt appears before upload. This is what makes old batches cheap: dozens of photos, one tap.
2. **Per receipt override** exists (S5) for the cases the user knows better.
3. **Photo date is a hint, never applied silently.** If EXIF `DateTimeOriginal` is readable from the picked data (read it *before* HEIC conversion, because conversion drops it), the row shows a quiet third line **Photo from 3 Oct** and S5 enables the *Photo date* segment. Selecting it sends that value as `purchased_at`.
4. **"Set one date for all"**: below the batch shop row, a text link **Set a date for all** (blue, `label`), visible only with ≥ 2 receipts. It opens S5-style date tray titled **Date for all receipts** with *From receipt* | *Pick…* only (no photo date; it differs per photo). Rows then show the date chip as **12 Sep** instead of **Date from receipt**. Use case: a batch scanned in one go on the same day.
5. **When OCR finds no date** the receipt lands in *Needs review* with the existing reason **No date found** (Home row subtitle "No date found · 17:20"). The fix tray (existing review banner action) shows the same three choices as S5: *Photo date* (if it was stored with the upload as `photo_taken_at`, see §9), *Pick…*, and **Use upload day**. Receipts with no date sort by upload time until fixed, and are **not** counted in analytics until dated (copy in the banner: **Pick a date so it counts in your totals.**). Batch fixing is one by one via the Needs-review list; each fix tray pre-selects *Photo date* when present so it is one tap per receipt.
6. Dates are never guessed from the file name or file modification time.

## 5a. Straighten and crop (picked photos)

Why: a library photo skips the VisionKit scanner, so it is never straightened or cropped. A receipt photographed at an angle has perspective distortion: columns drift (IMG_1475: the price column moves about 24 px against the descriptions at the top and 13 px at the bottom) and OCR mis-pairs descriptions and prices. The scanner path already corrects this; imports must too.

**Decision: automatic for every picked image, with an optional manual adjust. Never a gate, never a required review.** A batch must stay "pick, tap Import".

| Rule | Spec |
|---|---|
| Applies to | Photos from the library and image files (JPEG, PNG, HEIC). **Not** scanner pages (already corrected) and **not** PDFs (rendered flat). |
| Detection | While the S3 thumbnails load, on background tasks (2–3 at a time), on a copy downscaled to 1024 px on the long side: `VNDetectDocumentSegmentationRequest` first; if its candidate is rejected (see Accept), `VNDetectRectanglesRequest` (aspect 0.15–1.0, one observation) as a fallback. About 40 ms per photo, so 30 photos add under 2 s of background work hidden behind the thumbnail decode. The row appears only once its thumbnail is ready, so no row changes after it is shown. |
| Accept | Candidate must be **taller than wide** (a receipt is portrait; Vision returned a wrong wide box over a wood table at confidence 1.0 on IMG_1463, so confidence alone is not trusted), cover **at least 15 % of the frame** (real receipt photos fill only 25–38 % of it; 35 % rejected most of them), be convex, and have its corners clamped inside the image. Document segmentation is tried first, then the rectangles fallback with the same rules. If neither passes: *edges not found*, leave the photo untouched. |
| Skip when already square | Only when the outline is straight (every corner within 1.5 % of an axis-aligned rectangle) **and** it covers more than 90 % of the frame, i.e. the photo is already a flat, cropped receipt. A straight outline that covers less (a straight photo with a lot of table around it) still gets cropped. |
| Apply | In the upload queue's `preparing` step (§6), full resolution, `CIPerspectiveCorrection` from the stored normalised quad (`UploadItem.cropQuad`, `CropQuad` in `Design/Components/CropOverlay.swift`); output size from the mean of opposite edge lengths, long side capped at 2400 px, then stitching as today. About 100–150 ms per photo, off the main thread. S3 shows the corrected preview (same correction on the 1024 px copy) so what the user sees is what uploads. |
| If detection has not finished when Import is tapped | Import does not wait; the queue's `preparing` step runs detection itself. |
| Settings | **Settings → Straighten imported photos** (toggle, default on). Off = send the photo as picked. |

Detection can still be wrong, so **the preview and a one-tap way back matter as much as the detection itself**. What the user sees (still no extra step):
- **The row thumbnail is the straightened result**, so a wrong crop is visible at a glance, before upload.
- Auto-straightened: a small chip on the page strip (`crop` glyph + **Straightened**, 11 pt `captionMedium`, `ink2` on `sunk`). **One tap toggles it to Original**: the thumbnail swaps back (160 ms cross-fade, the one cross-fade this flow allows because it communicates a change), the chip reads **Original** with a dimmed glyph, `selection` haptic. Tap again to re-apply. The choice is stored with the item (`cropQuads` nil = original) and holds even if the user opens S5.
- Not found: a caption line in the row, `ink3`: **Edges not found**, with an inline **Adjust** link. Not amber: amber means needs review, and this blocks nothing.
- S5 gets a **Straighten & crop** row with the value **Automatic** / **Adjusted** / **Original**; tapping opens S8. Hidden for PDFs and scanner pages.
- A wrong detection that the user never notices still uploads (the receipt may then land in Needs review as today); the Needs-review fix tray for a receipt that came from an import offers **Re-upload original** only if the server keeps the original, which it does today (receipt image), so no extra ask.

### S8 · Adjust screen (full screen, pushed from S5)
A full screen, not a tray, because it needs the largest possible canvas.
- Header: back, title **Straighten & crop**, trailing **Rotate** icon button (90° clockwise; library photos are sometimes sideways).
- Body: the photo on a black backdrop with `CropOverlay` (dimmed outside, white outline, four 44 pt corner handles). Caption under it: **Drag the corners to the receipt's edges.**
- Footer: **Use original** (secondary, small), **Reset** (text; back to the detected quad), **Done** (primary). **Done** returns to S5 and updates the row's strip and badge (**Adjusted**).
- Dragging a corner has no haptic (continuous). **Reset** and **Use original** play `selection`; **Done** plays `light` on touch-down.
- Motion: pushes like any pushed screen; the polygon animates to the Reset position with `Motion.spring`; Reduce Motion: 120 ms fade.

## 6. Progress, queue and failure

### Where the work happens
Tapping **Import** must be instant. Per receipt, the app writes the *original picked data* (HEIC/JPEG/PNG bytes or the PDF page renders) into `uploads/<id>/` and creates an `UploadItem` with state **`preparing`**. The queue worker then converts HEIC → JPEG, straightens and crops picked photos (§5a), stitches pages, enforces the 20 MB cap, builds the multipart body, and moves the item to `waiting`/`uploading`. The import screen closes as soon as the items are on disk, with the Add flow's usual landing: Home tab, toast, and the progress card.

`UploadItem` additions (swift agent): `state` gains `.preparing`; `batchId: UUID`; `source: .scanner | .photos | .files`; `photoTakenAt: Date?`; `shopId: Int64?` (nil = let Shop find out); `pageFiles: [String]`; `cropQuads: [CropQuad?]` (one per page, nil = original).

### Home: `UploadProgressCard`
One card per **active batch** (items sharing a `batchId`), above the receipt list, below the attention card. Component: `UploadProgressCard(state:)` (Design/Components/Import.swift). States:

| State | Title | Visual | Tap |
|---|---|---|---|
| preparing | **Preparing 2 of 6** | blue tint, upload icon, thin blue `ProgressMeter` | opens queue |
| uploading | **Uploading 3 of 6** | blue tint, meter fills as each item is accepted (202/409) | opens queue |
| offline | **6 receipts waiting** + *They upload when you're back online* | neutral tint, `wifi.slash` | opens queue |
| partial | **4 uploaded · 2 didn't** + *Tap to see which ones* | amber tint, alert badge, **Retry** pill | card opens queue; **Retry** re-enqueues the failed ones |
| done | **6 receipts uploaded** + *Reading them now* | green tint, check badge; auto-collapses after **4 s** (240 ms collapse) | opens Home list filtered to *Processing* |

A single scanned receipt (`source == .scanner`, batch of 1) does **not** show the card (keeps today's behaviour: toast + "Reading receipt…" row). The card appears for any batch of ≥ 2, or any import that fails.

Numbers roll with `numeric text` (0.18 s). The card enters with `ease` (slide 8 pt + fade) and leaves with the 240 ms collapse.

### Queue screen (Settings → Upload queue)
Existing structure (Waiting / Didn't upload), with:
- **Batch header** rows when a batch has ≥ 2 items: `DaySectionHeader`-style **Imported from Photos · 3 Oct, 14:02** with trailing **6 receipts** and the batch's own thin meter. Items group under it. (Single scans stay ungrouped as today.)
- Row subtitle: **2 pages · Albert Heijn** (or **Shop to be found out**) with the state: `preparing` shows **Preparing…** (ink2), `waiting` **Waiting**, `uploading` spinner, `failed` red text with the reason.
- Row context menu / swipe: **Retry**, **Remove**. Remove asks nothing for waiting items (it is an undo of the user's own import) and shows an **Undo** toast for 4 s.
- Bottom button **Retry now** / **Retry when online** (existing).

### Offline behaviour
Import works fully offline: S3 and S5 need nothing from the network, the shop list is the cached catalog, "Let Shop find out" needs no network at import time. The footer line says **Offline · uploads when you're back online**; after Import the card shows the *offline* state and the toast is **Saved · uploads when you're online** (existing copy). When the network returns the card flips to *uploading* with no user action. No `warning` haptic for offline queueing: it is expected, not a problem (see §8).

### Failure taxonomy

| Cause | Retries | What the user sees | Action |
|---|---|---|---|
| No network / server unreachable | Automatic (5 s → 30 min backoff, resume on reconnect) | Item stays *waiting* with **Server not reachable** in `ink2` | none needed |
| 5xx / timeout | Automatic, 5 attempts, then *failed* | Red **The server had a problem** | Retry, Remove |
| 401 | Stops the queue | Existing session-expired flow; items stay queued | Re-pair, then queue resumes |
| 413 / file too big after compression | Never | Red **This file is too large** | Remove (or re-import a smaller photo) |
| 415 / can't decode | Never | Red **This file isn't a readable image** | Remove |
| 409 (already uploaded) | Never | Amber **Same receipt was already uploaded**, `Open receipt` link to the existing one | Open, Dismiss |
| Disk full while preparing | Never | Red **Not enough space to prepare this** | Remove |

Partial batch failure: successes proceed normally; the card shows *partial*. One `uploadFailed` haptic per batch, not per item.

## 7. After upload

Each uploaded receipt becomes a normal Home row (day-grouped by the receipt's date, or by upload day while it has none). Row states, using the existing `ReceiptRow` and status colours:

| Receipt state | Row title / subtitle | Colour |
|---|---|---|
| processing | shop + **Reading receipt…** + mini spinner | `blueText` |
| ok | shop + **18:42 · 12 items** | `ink2` |
| needs review | shop + reason (**Total doesn't match**, **No date found**, **3 items to link**) | `amberText` |
| failed | shop + **Couldn't read this receipt** | `redText` |
| duplicate (409) | not a new row; shown in queue as amber (see §6) | |
| **unknown shop, suggestion ready** | **Unknown shop** with `UnknownShopMark`, subtitle **Looks like Aldi · tap to add** | `blueText` |
| **unknown shop, no suggestion** | **Unknown shop**, subtitle **Pick a shop** | `amberText` |

Use `ReceiptRow(isUnknownShop: true, …)` for the last two. Amounts show normally once read. Unknown-shop receipts count as *needs review* in the Home attention count only when there is **no** suggestion; with a suggestion they are *information*, counted by the card below, so the attention number does not inflate.

### Shop suggestion (backend: "Unassigned" + suggested shop)

**On Home**, when ≥ 1 suggestion is pending: an `AttentionCard(count: n, title: "New shop: Aldi", hint: "Add", tone: .blue)` directly under the upload progress card and above the amber review card. `count` = receipts waiting on that shop. If several different shops are suggested, one card per shop (max 2; a third collapses into **3 new shops** opening the queue of suggestions via the first receipt). Tapping opens the receipt that arrived first.

**On the receipt** (the screen the card opens, or a tapped Unknown-shop row): `ShopSuggestionCard` as the first element under the header block, above any amber banner.
- Title **This looks like Aldi**, subtitle **Utrecht · aldi.nl · 3 receipts are waiting for it** (parts omitted when unknown; with one receipt: **Not in your shops yet**). `ShopMark` shows the logo (the backend serves it) or monogram.
- **Add Aldi · 3 receipts** (blue solid, small) — one tap does everything: creates the shop, links every waiting receipt. Plays `medium` on touch-down; on success `success` + toast **Aldi added · 3 receipts linked**. While the request runs the button shows the spinner (`LoadingLabel`).
- **Edit** (secondary) — opens the existing `ShopFormTray` prefilled (name, chain/branch, domain, logo preview), replacing nothing on screen; **Save** has the same success outcome. Editing is for typos in the name or the branch.
- **Not now** (text) — hides the card for this receipt this session, leaves the row as **Unknown shop · Pick a shop** (amber) and does not count in the badge twice. A **Pick another shop** option is inside the receipt's normal shop picker (the shop chip in the header).
- After accepting: the card collapses (240 ms), the header shop mark cross-fades from the dashed "?" to the shop mark (160 ms), and on Home every waiting row's mark cross-fades the same way, in place (no reorder, no jump). The `AttentionCard` count rolls to its next value or collapses.
- Failure (offline or 5xx): button returns, `error` haptic, toast **Couldn't add the shop. Try again.** Offline: the button is disabled with the subtitle **Needs a connection**.

**If the suggestion is wrong**: **Edit** (rename/domain) or **Pick another shop**; "Not now" never deletes anything on the server.

## 8. Haptics

All from the existing taxonomy (`Design/Haptics.swift`); no new events. Max one per user action; batch outcomes fire once.

| Moment | Haptic |
|---|---|
| Tap "Import receipts" card / option rows / chips / shop chip / date chip | per `.press` default: card `light` on touch-down; chips and pills `selection` |
| Source tray replaces Add tray | none (the Add tray already played `trayOpen`) |
| Photos picked, S3 appears | none (system picker owns it; the screen's arrival is not an action) |
| Join / split pages, grouping segmented control, shop/date choice, reorder step | `selection` |
| Remove a receipt row | `soft` |
| Tap **Import N receipts** | `uploadQueued` on touch-up (once, after the files are on disk) — not `medium` on touch-down, so the button uses `haptic: nil` |
| Batch fully uploaded (card → done) | `success` once for the batch, only if the app is in the foreground |
| Some failed (card → partial) | `uploadFailed` (once) |
| Went offline / queued offline | none |
| Duplicate found (409) | `warning` once per batch |
| A receipt reaches *needs review* from this batch | none (users are not touching the phone) |
| Add suggested shop (Accept) | `medium` on touch-down, `success` on completion |
| Add suggested shop fails | `error` |
| Not now | `selection` |
| Straighten & crop: chip toggle (Straightened ⇄ Original), Reset / Use original / Rotate | `selection` |
| Straighten & crop: Done | `light` (button touch-down) |
| Corner drag, auto-straighten | none |
| Discard confirmation | `error` on **Discard** (destructive confirmed) |

## 9. Motion

| Element | Motion |
|---|---|
| Add tray → source tray | tray replace: height 360 ms spring, content cross-fade 160 ms |
| Source tray → system picker → S3 | `fullScreenCover` default slide; S3 content first paint: rows stagger (8 max, 24 ms) |
| Thumbnail loading | replace in place, no fade-in stagger |
| Join pages | merged row height 360 ms spring; thumbnails slide 160 ms; `JoinPill` icon swaps with `pop` |
| Segmented control | existing knob spring; rows join/split with the same height spring |
| Remove row | 240 ms collapse (`Motion.collapse`) |
| Footer count | `numericText` roll 180 ms; button label morph when the count changes |
| Progress card | enter `ease` (8 pt + fade), meter `numberRoll`, number roll; done collapses after 4 s with `collapse` |
| Suggestion accepted | card collapse 240 ms; mark cross-fade 160 ms; AttentionCard number roll |
| Reduce Motion | drop slides, springs and stagger; 120 ms fades; keep all haptics |

## 10. Copy deck

| Where | Text |
|---|---|
| Add tray card | **Import receipts** / From Photos or Files, old ones too |
| Source tray | **Import receipts** · **Photo library** (Pick as many as you like) · **Files** (JPEG, PNG, HEIC or PDF) · Receipts are read on your own server. Nothing leaves your home network. |
| S3 header | **Import receipts** · "6 photos" |
| Grouping | **Separate receipts** · **One long receipt** · hint *Up to 5 pages* |
| Shop row | **Shop** · **Let Shop find out** · **Pick a shop** |
| Date | **Date from receipt** · **Set a date for all** · **Photo from 3 Oct** |
| Join pill | **Join pages** / **Same receipt** |
| Footer | **Import 6 receipts** / **Import receipt** · Uploads in the background · Offline · uploads when you're back online · Choose a shop for 2 receipts · 2 files can't be read |
| Details tray | **Receipt 3** · 2 pages · **Add page** · **Done** · **Remove this receipt** |
| Date captions | *Shop reads the date printed on it. If there isn't one, you'll be asked later.* · *The day you took this photo. It can differ from the day you shopped.* |
| Discard | **Discard 6 photos?** · **Discard** · **Keep editing** |
| Toasts | **Receipts added · uploading** · **Saved · uploads when you're online** · **Skipped a duplicate** · **Aldi added · 3 receipts linked** · **Couldn't add the shop. Try again.** |
| Progress card | **Preparing 2 of 6** · **Uploading 3 of 6** · **6 receipts waiting** · **4 uploaded · 2 didn't** · **6 receipts uploaded** |
| Unknown shop rows | **Unknown shop** · Looks like Aldi · tap to add · Pick a shop |
| Suggestion card | **This looks like Aldi** · Utrecht · aldi.nl · 3 receipts are waiting for it · **Add Aldi · 3 receipts** · **Edit** · **Not now** |
| Straighten | **Straighten & crop** · Automatic / Adjusted / Original · **Drag the corners to the receipt's edges.** · **Use original** · **Reset** · **Done** · Straightened (chip) / Original (chip, after one tap) · Edges not found · Adjust · VoiceOver: **Straightened, double tap to use the original** · Settings: **Straighten imported photos** |
| No-date banner | **No date found.** Pick a date so it counts in your totals. |

## 11. Accessibility

- Every `ImportReceiptRow` is one container with the title as its label and actions exposed as custom actions (**Pick shop**, **Set date**, **Remove**, **Join with next**). The page strip is hidden from VoiceOver; the title says the page count.
- `JoinPill` reads **Join this photo with the next one** / **Pages above and below are one receipt**.
- Progress card is one element: its title is the label; state changes post an announcement (**Uploading 3 of 6**, throttled to every 2 items, and **6 receipts uploaded**).
- Tap targets ≥ 44 pt: chips are 24 pt visually but have a 44 pt `contentShape` in the row; the remove button is 30 pt with a 44 pt hit area.
- Dynamic Type: rows reflow chips under the title; footer text wraps; thumbnails cap at 56 pt.

## 12. Components

Added (done, in `Design/Components/Import.swift`, previews in `Design/Catalog/CatalogView.swift` → "Import"):

| Component | Props | Used in |
|---|---|---|
| `UnknownShopMark(size:)` | dashed square with "?" | S4, S7, unknown-shop rows |
| `PageStrip(pages:height:)` | up to 3 fanned thumbs | import rows, details tray |
| `ImportReceiptRow(pages:title:shopName:dateText:photoDateHint:straighten:onToggleStraighten:onAdjust:onShop:onDetails:onRemove:)` | flat row; `straighten` is `.straightened`, `.original` or `.notFound` | S3 |
| `JoinPill(isJoined:isEnabled:action:)` | link toggle on the hairline | S3 |
| `UploadProgressCard(state:onOpen:onRetry:)` | `preparing/uploading/offline/partial/done` | S6 (Home) |
| `ShopSuggestionCard(name:detail:waiting:isWorking:onAccept:onEdit:onNotNow:)` | blue-tint card | S7 receipt, optionally Home |
| `CropQuad`, `CropOverlay(image:quad:)` | document outline with four draggable corners | S8 (Adjust) |
| `ReceiptRow(isUnknownShop:)` | new parameter, default false | S7 rows |

Reused unchanged: `AttentionCard` (tone `.blue`), `OptionRow`, `SegmentedControl`, `Banner`, `TrayHeader`, `ShopMark`, `ShopFormTray` (Features), `ReceiptDateTray` date picker, `LoadingLabel`, `ProgressMeter`, `Toast`.

Still to build in Features (not mine): the S3 screen, S2/S4/S5 trays, queue changes, Home card placement.

## 13. Asks from the backend and Core (confirm before building)

1. **Shop optional on upload.** `shop_id` is required today. For "Let Shop find out" the upload must accept no shop and store the receipt as *unassigned*. Until that lands, S3 falls back to: single photo → last used shop preselected; several photos → shop required (amber footer), and **Let Shop find out** is hidden.
2. **Suggestion payload** on `Receipt` (names are a proposal for the backend agent): `shop_suggestion: { name, chain, branch, domain, logo_url?, waiting_receipts }`. **Accept** = `POST /v1/shop-suggestions/{id}/accept` (optional overrides `name`, `branch`, `domain`) returning the shop and the linked receipt ids; **Not now** is client-only.
3. **Photo date.** Optional multipart field `photo_taken_at` stored with the receipt (not used for `purchased_at`), so the no-date fix tray can offer it later.
4. **Batch id.** Local only (`UploadItem.batchId`); the server does not need it.
5. **PDF.** Rendered to JPEG on the device (the API stays JPEG/PNG). Multi-page PDFs stitch like photos (≤ 5 pages per receipt).
6. **Straighten (Core, no backend change).** Vision detection at 1024 px, `CIPerspectiveCorrection` in the queue's preparing step, `Settings.straightenImports`. If the backend can also flag a skewed receipt (for example a `needs_review` reason `skewed`), the Needs-review row could offer **Straighten and re-upload**; not required for this build.
7. **EXIF.** Read `DateTimeOriginal` from the original picked data before conversion; keep it in `UploadItem.photoTakenAt`.

## 14. Out of scope here

Share-sheet extension (E5), automatic grouping by capture time or by content, drag-to-reorder across receipts, importing a whole album, cloud storage providers beyond what the Files picker offers.

## 15. Acceptance checklist (for the swift agent)

1. From a cold Home: + → Import receipts → Photo library → pick 6 photos → tap Import → back on Home with an `UploadProgressCard` in under 300 ms, nothing blocking.
2. Offline: same flow works; card shows *waiting*; reconnect uploads without user action.
3. Pick 8 photos where 2 are pages of one receipt: join them, import: 7 receipts, one with 2 pages.
4. Kill the app after Import and before upload finishes: items are still in the queue and resume.
5. One 415 and one 409 in a batch: card ends *partial* with the right queue copy; a single `uploadFailed`/`warning` haptic.
6. A receipt with an unknown shop and a suggestion shows the dashed mark row, the blue Home card, and the suggestion card; **Add** links all waiting receipts with in-place cross-fades and no list jump.
7. A receipt with no date lands in Needs review with the *No date found* reason and the three-way date fix.
8. Reduce Motion: no slides/springs; haptics unchanged. VoiceOver can reach every action on an import row.
9. Import the skewed library photo (IMG_1475): the row shows the straightened preview with the crop badge, the uploaded image has the price column aligned with the descriptions, and the receipt parses. A flat, already-cropped receipt photo (straight outline covering more than 90 % of the frame) is not resampled; a straight outline that covers less is still cropped. IMG_1463 (wide wrong box over the table) is rejected and falls back to rectangles or *edges not found*. Tapping the **Straightened** chip swaps the thumbnail to the original and uploads the original. Import of 30 photos stays responsive (rows ready under 2 s on an iPhone 15).
10. A photo with no detectable edges shows **Edges not found · Adjust**; Adjust opens S8 with a full-image quad; **Done** updates the row to **Adjusted**.
