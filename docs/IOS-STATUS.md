# Shop iOS — Status & handoff

_Last updated 2026-10-11. Read this first; then [IOS-PLAN.md](IOS-PLAN.md) (milestones), [DESIGN-SYSTEM.md](DESIGN-SYSTEM.md) (tokens/components), [IOS-SETUP.md](IOS-SETUP.md) (Xcode project), [IOS-BACKEND-NEEDS.md](IOS-BACKEND-NEEDS.md) (API asks)._

## TL;DR

- **It builds and runs.** The Xcode project is committed (`ios/Shop/Shop.xcodeproj`, scheme `Shop`, bundle id `com.sundaystudi.Shop`, Swift 6, iOS 17). `xcodebuild -project Shop.xcodeproj -scheme Shop -destination 'generic/platform=iOS Simulator' build` succeeds.
- **Verified on the iPhone 17 Pro simulator against the mock server only** (`-mock`, see §2 for the launch flags). Every screen and flow listed in §1 was exercised there. **Nothing has been verified against the real server yet** (the simulator isn't paired), and **nothing has run on a physical device**.
- **There are still no automated tests** (no XCTest or UI test target); checking is by simulator walk-throughs and previews.
- The app targets **`openapi.yaml` 0.12.0** (shop optional on upload, `photo_taken_at`, shop suggestions, product review queue, undo, merge, split, barcode create/refresh).
- **Open question for the user:** the bundled GT America fonts register under PostScript names `GTAmericaTrial-*` (see `ios/Shop/Design/Typography.swift`), so the files in `ios/Shop/Resources/Fonts/` and `design/fonts/` look like **trial licences**. The user said GT America may be used; they still need to confirm the licence covers these files before any distribution.
- **Real open gaps:** the M10 app part (the "needs English" badge, highlighted unknown words, teach-a-word sheet; no UI for `/v1/glossary` or `/v1/translations/todo`), the font licence, and every unverified item in §4.

## 1. Milestones (IOS-PLAN.md §4)

"Done" below means built and exercised in the simulator with the mock server. Nothing is verified against the real server or on a device, and none of the on-device checklists in IOS-PLAN.md has been run.

| Milestone | Status | Notes |
|---|---|---|
| iM0 Skeleton | Done | Xcode project committed; Info.plist keys in `Config/ATS.plist` (ATS, camera, local network) and the build settings. |
| iM1 Design system | Done | `Design/` tokens + components + `CatalogView` (Settings → About Shop). Owned by the design agent. |
| iM2 API, pairing, shell | Done on mock | `POST /v1/auth/pair`; sign out = revoke own device. Real pairing not yet run. |
| iM3 Home & inbox | Done on mock | Counts, polling every 3 s while processing, filter, spend header with cached placeholder, swipe actions. |
| iM4 Capture & upload queue | Done on mock | VisionKit scanner, stitching, disk-backed queue with `preparing`/`waiting`/`uploading`/`failed`/`done`, background `URLSession`. **Background relaunch is untested.** |
| iM5 Receipt review | Done on mock | All trays, skeleton lines, one banner per problem, auto-link at 80 %+, "Review N of M" run through all receipts. |
| iM6 Products & search | Done on mock | List, detail, history, aliases, search, product form with barcode scan and nutrition, swipe actions. |
| iM7 Unpack | Done on mock | DataScanner EAN; scanning creates the product (no form), outcome rows, merge strip, offline queue (`PendingScans`). **Torch untested.** |
| iM8 Analytics | Done on mock | Spend bars, shop, category, price changes, range tray. |
| iM9 Shopping list offline | Done on mock | Local items + op log, `client_id` idempotency; **offline reconnect resume not verified**. |
| iM10 Polish & audit | Partly | App icon and accent done. **Not done:** VoiceOver pass (swipe rows have accessibility actions, never tried with VoiceOver), Dynamic Type audit at XXL, 390 pt width check, design audit vs wireframes, launch screen beyond `UILaunchScreen`. |
| M10 app part (English) | **Not started** | "needs English" badge, highlighted unknown words, teach-a-word sheet, review queue. Backend is done. |
| M11 iOS items | Done on mock | See "M11 iOS progress" at the end. |

## 2. Build & run

1. **Xcode 27** (Sep 2026; 27.1 fine). Swift 6 language mode, deployment target **iOS 17.0**, bundle id **`com.sundaystudi.Shop`**.
2. Open `ios/Shop/Shop.xcodeproj` (sources are a file-system synchronized group, so new files are picked up automatically). Set the signing team for a device build. [IOS-SETUP.md](IOS-SETUP.md) describes how the project was set up.
3. **Without a server:** add the launch argument `-mock` (Scheme → Run → Arguments, or `xcrun simctl launch <udid> com.sundaystudi.Shop -mock`). Other flags: `-offline`, `-mock-fail`, `-mock-auto`, `-mock-empty-queue`, `-mock-scan`. `Core/Mock/MockServer.swift` fakes every endpoint; the password `wrong` shows the wrong-password state; an upload turns from processing to needs-review after about 4 s.
4. **Against the local server:**
   - Start it: `make up` (or `make setup` the first time, which prints the pairing password once). New password: `docker compose -f deploy/docker-compose.yml exec api shop password generate`.
   - Server URL in onboarding: the simulator can use `http://localhost:8080`. A phone needs the Mac's LAN address (`http://<mac>.local:8080` or `http://192.168.x.y:8080`) or Tailscale (`http://<host>.<tailnet>.ts.net:8080`). ATS allows these through the plist keys in IOS-SETUP.
   - Then enter the pairing password. The app stores only the returned device token (Keychain), never the password.
5. **Previews:** most files end with light/dark `#Preview`s that use `AppModel(mock: true)`.

## 3. API status (openapi.yaml 0.12.0)

All calls go through `Core/API/Endpoints.swift`; models are in `Core/API/Models.swift` (snake_case ↔ camelCase via the coders).

**Wired and used by screens:** `/healthz`, `/version`, `auth/status`, `auth/pair`, `auth/devices` (+ revoke self), shops CRUD (incl. `domain`, `logo_url`, `GET /v1/shops/{id}/logo` via `ShopLogos` + `ImageCache`), categories CRUD, receipts list/get/patch/delete/confirm/reparse/reocr/image/upload (multipart, background), `receipts/counts`, lines create/update/delete/link, products list (`q`, `category_id`, `sort`)/get/create/update/delete/image GET+PUT/nutrition PUT+DELETE, `barcodes/{code}`, aliases list/delete, `unmatched-lines`, `search`, stats `spend` (incl. `split_by`, `group_by=product`, `category_id`), `prices`, `compare`, `price-changes`, `frequent-items`, list get/add (`client_id`)/patch/delete/check/uncheck/clear checked.

**Added since 0.9.0 (all wired):** shop suggestions (`shopSuggestion`, accept, reject), `POST /v1/barcodes/{code}/product`, review queue + review/merge/undo/split/refresh, `photo_taken_at` and optional `shop_id` on upload.

**Defined but unused:** `auth/me`; `api.receiptImageURL` covers one image per receipt.

**Not wired:** `GET /v1/list/changes`. `ShoppingListStore.sync()` replays its op log, then refetches the whole list (`GET /v1/list?include_checked=true`). That's fine at list size; switch if it gets slow. `/v1/glossary` (English-names admin) has no UI.

## 4. Known gaps, stubs, open decisions

- **Unverified:** real-server behaviour for everything above (mock only); background-upload relaunch; torch; offline-reconnect resume; VoiceOver; Dynamic Type at XXL; 390 pt width; the Settings "Straighten imported photos" nudge visual; all real-device checks (HEIC from the camera roll, camera, haptics feel). The "Suggested" row in the shop picker after "Not now" and the first-run nudge were deliberately not built.
- **Previously uncompiled code, now compiling** (kept for context): (Swift 6, MainActor-default isolation): `nonisolated` on `Shake`, `Line` and `CornerShape`; the `UIColor` dynamic provider in `Design/Colors.swift`; `FlowLayout` (`Design/Components/Amounts.swift`); `@Observable` classes; `Text + Text` concatenation (deprecated in newer SDKs, warning only); Swift Charts axis code in `Design/Components/Charts.swift`.
- **No automated tests.** The user chose previews plus on-device checklists over automated tests (IOS-PLAN §0).
- **Review edits need a connection** (decision): only scans and the shopping list work offline.
- `ImageCache` negative-caches every failed load (including offline failures) until relaunch (`Core/Cache/ImageCache.swift`).
- `ShopLogos` is a static map refreshed when the catalog syncs; `ShopMark` doesn't observe it, so a logo shows up on the next re-render (`Core/Cache/ShopLogos.swift`).
- The receipt thumbnail always says "1 page"; the server doesn't return the page count (`Features/Receipt/ReceiptView.swift`).
- The P.1 "Recent purchases" rows can't open their receipt: `PricePoint` has `line_id` but no `receipt_id` (possible backend ask).
- A.2's per-shop split bar makes one `spend` call per shop; `split_by` is only allowed with time groupings.
- The torch toggle (`Features/Unpack/BarcodeScanner.swift` `Torch`) drives `AVCaptureDevice` directly while DataScanner runs; untested.
- Background-upload relaunch (`App/ShopApp.swift` `AppDelegate`, `Core/Upload/UploadQueue.swift`) is untested.
- JSON files in Application Support replace SwiftData (deliberate, documented in IOS-PLAN).
- `docs/DESIGN-SYSTEM.md` still says "draft" in places and mentions `DesignSystem/`; the code lives in `ios/Shop/Design/`.
- No in-app wordmark/logo yet (only the app icon); ask the user whether they want one.
- **Fonts: trial licence?** (see TL;DR) — user to confirm.

## 5. Next tasks, in order

1. **Pair the simulator with the real server** (`make up`, `shop password generate`) and re-walk Home, receipt review, import (HEIC, PDF, batches), product review and Unpack; fix what differs from the mock.
2. **Run on a real phone** (iM4 checklist): background upload and relaunch, airplane mode, force-quit mid-upload, torch, camera, haptics.
3. **M10 app part:** "needs English" badge, unknown words, teach-a-word sheet (design thread first).
4. **iM10:** VoiceOver, Dynamic Type XXL, 390 pt, launch screen.
5. Optional: an XCTest target for `Money`, `Dates`, `ProductDraft.parseSize`, `UploadQueue.receiptId(from:)`, `isValidBarcode`, and a UI test for scan → upload against `-mock`.

## 6. Don't undo

- **Design rules win over wireframes** (`design/DESIGN-RULES.md`, `design/MOTION.md`):
  - Tabular digits everywhere.
  - Bold only for screen titles and the hero.
  - Amber = needs review, always.
  - Shop marks 22 pt (28 in headers, ≤ 32) with the monogram fallback.
  - Trays never stack: they replace themselves, with a height morph.
  - Reduce Motion drops displacement and keeps 120 ms fades.
  - One haptic per user action, never on scroll.
- **Tokens only:** no raw hex, font sizes or radii in feature code. Use `Design/` (`Palette`, `Accent`, `TypeStyle`, `Space`, `Radius`, `Size`, `Motion`, `Haptics`).
- **Accessibility:** fonts scale with Dynamic Type (`relativeTo:`); icon buttons have labels; Reduce Motion is respected in `Motion.swift` helpers; destructive actions confirm in a red tray.
- **Local only:** the phone talks only to the Shop server. Open Food Facts and logos come through the backend. Add any new server-side need to IOS-BACKEND-NEEDS.md instead of calling a third party.
- **Money** is integer cents, formatted Dutch style (`€2.614,90`, minus sign U+2212) through `Core/Money/Money.swift`.
- **Auth:** pairing password → device token in the Keychain; the password is never stored.
- No third-party Swift packages. Descriptive names (no abbreviations). Bundle id `com.sundaystudi.Shop`. All docs live under `docs/`.
- Never commit `deploy/.env` (it holds the logo.dev key; git-ignored).

## M11 iOS progress (swift app agent)
All done and verified in the simulator on the mock server; **not** verified against the real server or on a device.
- Import flow: Photos, Files and PDF, batches (max 30 pages, 5 per receipt, dedupe by SHA-256), straighten and crop (Vision + CoreImage, adjust screen, Settings toggle), HEIC always re-encoded to JPEG, `photo_taken_at` from EXIF, shop optional on upload.
- Unknown shop: "Let Shop find out" is the default; shop suggestion cards on Home, "Other shop", create a shop from the picker, banners "Pick a shop ›" / "Set the date ›" fix trays.
- Product auto-populate: "New products" review queue with reasons, undo (8 s), merge, split line, Try now, photo, 80 %+ auto-link, "Review N of M" through all receipts.
- Unpack creates products on scan; barcode scan inside the product form (digits only, 8/12/13/14).
- Swipe actions with haptics on Home receipts, Products, Shopping list, Retry queue and Manage lists; undoable removal for list and queue.
- English category names throughout Core/Features.
- Not done: the M10 English UI; share extension; real-HEIC device test.
