# Shop iOS — Status & handoff

_Last updated 2026-10-10. Read this first; then [IOS-PLAN.md](IOS-PLAN.md) (milestones), [DESIGN-SYSTEM.md](DESIGN-SYSTEM.md) (tokens/components), [IOS-SETUP.md](IOS-SETUP.md) (Xcode project), [IOS-BACKEND-NEEDS.md](IOS-BACKEND-NEEDS.md) (API asks)._

## TL;DR

- **All screens are written** (`ios/Shop/`, ~14.5k lines Swift, 64 files, commit `0c888f7`).
- **It has never been compiled.** No Xcode project is committed, nothing has run in the simulator or on a device, and **there are no tests**. The code was written blind for Xcode 27 / Swift 6 / iOS 17. Expect a round of compile fixes on the first build.
- The app targets **`openapi.yaml` 0.9.0**; every backend need (#1–#11) is live and wired.
- **Open question for the user:** the bundled GT America fonts register under PostScript names `GTAmericaTrial-*` (see `ios/Shop/Design/Typography.swift`), so the files in `ios/Shop/Resources/Fonts/` and `design/fonts/` look like **trial licences**. The user said GT America may be used; they still need to confirm the licence covers these files before any distribution.

## 1. Milestones (IOS-PLAN.md §4)

"Written" = code exists. None of it has been verified by building, previews, or the on-device checklists in IOS-PLAN.md.

| Milestone | Code | Verified | Notes |
|---|---|---|---|
| iM0 Skeleton | Written | No | No `.xcodeproj` in the repo by design (user creates it, see IOS-SETUP). Fonts + Info.plist keys documented. |
| iM1 Design system | Written | No | `Design/` tokens + ~25 components + `CatalogView` (Settings → About Shop). Previews in every component file. |
| iM2 API, pairing, shell | Written | No | `POST /v1/auth/pair`; sign out = revoke own device. Token-rejected screen. |
| iM3 Home & inbox | Written | No | Counts, line/unlinked counts, polling every 3 s while processing, H.2 filter. |
| iM4 Capture & upload queue | Written | No | VisionKit document scanner, page stitching, background `URLSession` queue with backoff, 409 duplicate handling, S.2 queue. Background relaunch path is untested. |
| iM5 Receipt review | Written | No | H.1 + all trays (edit/add line, link product, actions, edit details, image zoom). Confirm always enabled. |
| iM6 Products & search | Written | No | P list (sort by last bought, `Product.cheapest`), P.1 detail, P.1.1 history, P.1.4 aliases (swipe delete), X.1 search on `/v1/search`. |
| iM7 Unpack | Written | No | DataScanner EAN, quick add with Open Food Facts prefill, nutrition form, link-to-line. |
| iM8 Analytics | Written | No | A (split_by=category bars), A.1 shop, A.2 category (group_by=product table), A.3 price changes, A.4 range tray. |
| iM9 Shopping list offline | Written | No | Local items + op log, temp ids, `client_id` idempotency. |
| iM10 Polish & audit | Partly | No | App icon (light/dark/tinted) and accent colour done. Not done: VoiceOver pass, Dynamic Type audit at XXL, design audit vs wireframes, launch screen beyond `UILaunchScreen`. |

## 2. Build & run

1. **Xcode 27** (Sep 2026; 27.1 fine). Swift 6 language mode, deployment target **iOS 17.0**, bundle id **`nl.casprine.shop`**.
2. Create the project once by following [IOS-SETUP.md](IOS-SETUP.md). All of it is manual: new App project in `ios/`, delete the template's `ShopApp.swift` / `ContentView.swift` / `Assets.xcassets`, drag in `App`, `Core`, `Design`, `Features`, `Resources`, add the Info.plist keys (fonts, camera, photo library, local network, ATS), set the signing team. Scheme: **Shop** (the default).
3. **Without a server:** add the launch argument `-mock` (Scheme → Run → Arguments). `Core/Mock/MockServer.swift` fakes every endpoint; the password `wrong` shows the wrong-password state; an upload turns from processing to needs-review after about 4 s.
4. **Against the local server:**
   - Start it: `make up` (or `make setup` the first time, which prints the pairing password once). New password: `docker compose -f deploy/docker-compose.yml exec api shop password generate`.
   - Server URL in onboarding: the simulator can use `http://localhost:8080`. A phone needs the Mac's LAN address (`http://<mac>.local:8080` or `http://192.168.x.y:8080`) or Tailscale (`http://<host>.<tailnet>.ts.net:8080`). ATS allows these through the plist keys in IOS-SETUP.
   - Then enter the pairing password. The app stores only the returned device token (Keychain), never the password.
5. **Previews:** most files end with light/dark `#Preview`s that use `AppModel(mock: true)`.

## 3. API status (openapi.yaml 0.9.0)

All calls go through `Core/API/Endpoints.swift`; models are in `Core/API/Models.swift` (snake_case ↔ camelCase via the coders).

**Wired and used by screens:** `/healthz`, `/version`, `auth/status`, `auth/pair`, `auth/devices` (+ revoke self), shops CRUD (incl. `domain`, `logo_url`, `GET /v1/shops/{id}/logo` via `ShopLogos` + `ImageCache`), categories CRUD, receipts list/get/patch/delete/confirm/reparse/reocr/image/upload (multipart, background), `receipts/counts`, lines create/update/delete/link, products list (`q`, `category_id`, `sort`)/get/create/update/delete/image GET+PUT/nutrition PUT+DELETE, `barcodes/{code}`, aliases list/delete, `unmatched-lines`, `search`, stats `spend` (incl. `split_by`, `group_by=product`, `category_id`), `prices`, `compare`, `price-changes`, `frequent-items`, list get/add (`client_id`)/patch/delete/check/uncheck/clear checked.

**Defined but unused:** `auth/me`; `api.receiptImageURL` covers one image per receipt.

**Not wired:** `GET /v1/list/changes`. `ShoppingListStore.sync()` replays its op log, then refetches the whole list (`GET /v1/list?include_checked=true`). That's fine at list size; switch if it gets slow. `/v1/glossary` (English-names admin) has no UI.

## 4. Known gaps, stubs, open decisions

- **Uncompiled code, likely trouble spots** (Swift 6, MainActor-default isolation): `nonisolated` on `Shake`, `Line` and `CornerShape`; the `UIColor` dynamic provider in `Design/Colors.swift`; `FlowLayout` (`Design/Components/Amounts.swift`); `@Observable` classes; `Text + Text` concatenation (deprecated in newer SDKs, warning only); Swift Charts axis code in `Design/Components/Charts.swift`.
- **No tests at all.** The user chose previews plus on-device checklists over automated tests (IOS-PLAN §0).
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

1. **Create the Xcode project and get a clean build** (IOS-SETUP.md). Fix the compile errors, keeping the token and naming rules in §6.
2. **Run in the simulator with `-mock`.** Walk the Catalog screen and every tab in light and dark; fix layout against the wireframes (`Pantry Wireframes` design project, https://claude.ai/artifact/LVSTnG3RYKArPtpCcCu4mG).
3. **Pair with the real server** (`make up`, `shop password generate`) and run the iM2/iM3 checklists in IOS-PLAN.md §4.
4. **Scan on a real phone** (iM4 checklist): background upload, airplane mode, duplicate scan, force-quit mid-upload.
5. Optional but cheap: add a small XCTest target for `Money.parse/format`, `Dates`, `ProductDraft.parseSize`, `UploadQueue.receiptId(from:)`, if the user wants tests.

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
- No third-party Swift packages. Descriptive names (no abbreviations). Bundle id `nl.casprine.shop`. All docs live under `docs/`.
- Never commit `deploy/.env` (it holds the logo.dev key; git-ignored).
