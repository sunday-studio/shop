# Shop iOS — Build Plan

Detailed plan for the iPhone app. It replaces the M6/M7 bullets in [PLAN.md](PLAN.md) §11 (M6 = iM0–iM5, M7 = iM6–iM10).
Design sources: the **Shop Wireframes** design project (32 screens, light + dark), [design/DESIGN-RULES.md](../design/DESIGN-RULES.md), [design/ia.html](../design/ia.html). The design system extracted from them is in [DESIGN-SYSTEM.md](DESIGN-SYSTEM.md). Things the backend must add are in [IOS-BACKEND-NEEDS.md](IOS-BACKEND-NEEDS.md).

**Status (2026-10-10): approved; all screens written (iM0–iM10 code), not yet built.** Sources in `ios/Shop/` (~14k lines). Project setup: [IOS-SETUP.md](IOS-SETUP.md). Next: you build on Xcode 27, send compile errors, then walk the per-milestone checklists in §4 on the phone.

Changes from the draft while writing:
- Design system lives in `ios/Shop/Design/` (not `DesignSystem/`), next to the motion/haptics files that already existed there.
- Offline cache, upload queue and list op log are small **JSON files** (Application Support), not SwiftData: less to go wrong under Swift 6 strict concurrency and nothing to migrate.
- Auth is `POST /v1/auth/pair` (password → device token); sign out revokes this device via `DELETE /v1/auth/devices/{id}`.
- Shop logos come from the backend (`logo_url`, `GET /v1/shops/{id}/logo`), cached on the phone; monogram fallback.
- Mock mode: launch argument `-mock` runs every screen against an in-memory server.

---

## 0. Ground rules

- **Scope of this work: Swift and SwiftUI source only.** No Xcode project generation, Makefile targets, CI, or test suites. You create the Xcode project once (§2) and build/run on your Mac and phone.
- **No testing here.** I don't build or run anything on this machine. Each milestone is verified by you with Xcode Previews and a checklist on your phone (§4).
- **Everything local.** The app talks only to your Shop server (LAN or Tailscale). No third-party calls from the phone: Open Food Facts lookups, logos and anything else external go through the backend. When a screen needs something the server can't provide yet, I write it down in [IOS-BACKEND-NEEDS.md](IOS-BACKEND-NEEDS.md) instead of working around it in the app.
- **Toolchain:** latest **Xcode 27** (27A266a, Sep 2026; 27.1 also fine), Swift 6 language mode, iOS 27 SDK. Deployment target stays **iOS 17**; newer APIs only behind `#available`.

---

## 1. Decisions

| Area | Decision | Why |
|---|---|---|
| Min iOS | **17.0** (built with Xcode 27) | `@Observable`, SwiftData, DataScanner, Swift Charts, `#Preview`. |
| Language | Swift 6 language mode, strict concurrency | Catches data races in the upload queue at compile time. |
| UI | SwiftUI. UIKit only where Apple has no SwiftUI view: `VNDocumentCameraViewController`, `DataScannerViewController` | |
| State | `@Observable` models per feature, injected via `Environment` | Small app, one user. |
| Project shape | **One app target, organised by folders** (`Design/`, `Core/`, `Features/`). No local packages | Least setup for you: create project, add the folder, done. |
| Dependencies | **None.** API client is hand-written `Codable` models + a small `URLSession` client, mirroring `openapi.yaml` | No package or build-plugin setup; works on older Xcode. |
| Offline | Scans (upload queue) and shopping list are offline-first. Products, shops and categories are cached read-only. Review edits need a connection (clear error and retry if offline) | Avoids conflict handling on review edits. Say if you want them queued too. |
| Updates | Polling (no push): every 3 s while a receipt is `processing`, otherwise on foreground and pull-to-refresh | IA "No push". |
| Shop logos | **Monogram letters now** (22 pt mark, brand colour). `ShopLogo` loads an image from a URL you'll send, **cached on disk** (+ memory), monogram while loading or on failure | Rules v2; you'll send the connection URL. |
| Previews | Every component and every screen gets `#Preview`s in light and dark, fed by `MockData` | This is how you check design fidelity without the server. |
| Mock mode | Launch argument `-mock` swaps the API for an in-memory fake server | Run any screen on the phone without the Mac mini. |
| Fonts | GT America 400/500/700 + Mono 400/500 bundled (already in `ios/Pantry/Resources/Fonts`) | Licence confirmed. |
| Naming | Folder `ios/Shop`, app name **Shop**, bundle id **`nl.casprine.shop`** | |

### Where the wireframes and the rules disagree (rules win, per DESIGN-RULES)
1. **Pairing:** O.3 shows "paste an access token"; the backend and rules v2 use **URL + password** (`POST /v1/auth/pair`). The password screen reuses the O.3 layout (field with show/hide, Connect, shake on wrong password).
2. **Shop marks:** Home/review rows draw 36 pt coloured tiles; rules v2 say **22 pt mark** (28 pt in headers).
3. **Bold:** some badges/initials use 700; the rules allow bold only for screen/tray headers and the hero. Badges → 500.
4. **Names:** wireframe copy says `pantry token create` / `pantry.tail…`; the app says Shop.

### Screens with no wireframe
H.1.1 receipt image viewer · H.1.2 edit details · H.2 filter · P.1.1 full price history · P.1.2 edit product · P.1.3 nutrition view · P.2 new product (= C.3.1 form) · L.2 suggestions · O.3 password variant.
I design these from the system; you review them in Previews in the milestone that adds them.

---

## 2. One-time setup (you)

1. Move fonts: `ios/Pantry/Resources/Fonts` → `ios/Shop/Resources/Fonts` (I do the move in iM0, since that's just files).
2. Xcode → File → New → Project → iOS App: name **Shop**, bundle id `nl.casprine.shop`, SwiftUI, **no** tests, minimum iOS 17. Save the `.xcodeproj` in `ios/`.
3. Delete the template `ContentView.swift`/`ShopApp.swift`, then drag the `ios/Shop/` folder into the project (create groups, add to the Shop target).
4. Info tab, add these keys (also listed in `ios/Shop/Resources/INFO-PLIST.md`):
   - `UIAppFonts`: the 5 `.otf` file names
   - `NSCameraUsageDescription`: "Scan receipts and barcodes."
   - `NSLocalNetworkUsageDescription`: "Connect to your Shop server on this network."
   - `NSAppTransportSecurity` → `NSAllowsLocalNetworking = YES`, plus `NSExceptionDomains` → `ts.net` (`NSIncludesSubdomains`, `NSExceptionAllowsInsecureHTTPLoads`) for Tailscale over plain http
5. Signing & Capabilities: your team. Phone: Developer Mode on.

Files I add later are added to the target the same way (or automatically, if your Xcode uses folder references).

---

## 3. Source layout

```
ios/Shop/
├─ App/              ShopApp, AppModel, ConnectionGate, RootTabView (pill bar + Add), Router, SyncPill host
├─ Design/
│  ├─ Tokens/        Colors, Typography, Spacing, Radii, Elevation, Motion, Haptics, Fonts registration
│  ├─ Components/    see DESIGN-SYSTEM.md §4
│  └─ Catalog/       CatalogView (every component, every state; reachable from Settings in debug)
├─ Core/
│  ├─ API/           APIClient, Endpoints, Models (Codable, mirrors openapi.yaml), Problem errors, MockServer
│  ├─ Auth/          Keychain token store, pairing
│  ├─ Money/         Cents, nl_NL formatting, per-unit prices
│  ├─ Dates/         Europe/Amsterdam day grouping, relative labels
│  ├─ Upload/        UploadQueue (files + JSON records, background URLSession, backoff), page stitcher
│  ├─ Cache/         JSON caches (shops, categories, products, list + op log), ImageCache (disk + memory)
│  └─ Mock/          MockData for previews and -mock mode
├─ Features/
│  ├─ Onboarding/    O.1–O.4
│  ├─ Home/          H, H.2
│  ├─ Receipt/       H.1, H.1.1–H.1.5
│  ├─ Add/           C, C.1 scanner + stitch, C.2
│  ├─ Unpack/        C.3, C.3.1–C.3.3
│  ├─ Analytics/     A, A.1–A.4
│  ├─ Products/      P, P.1, P.1.1–P.1.4, P.2
│  ├─ List/          L, L.1, L.2
│  ├─ Search/        X.1
│  └─ Settings/      S, S.1–S.4
└─ Resources/        Fonts, Assets (app icon, accent), INFO-PLIST.md
```

---

## 4. Milestones

At the end of each milestone I hand you a list of new/changed files, which Previews to look at, and a short **on-device checklist**. You build, run and tick it, and send back any compile errors or issues. I don't start the next milestone until you accept the current one. Estimates are focused days of writing.

### iM0 — Skeleton (0.5 d)
- `ios/Pantry` → `ios/Shop`; folder layout; `ShopApp` with a placeholder screen in GT America; font registration check; `INFO-PLIST.md` with setup steps.

**You check:** the project builds; the placeholder shows GT America on your phone (not the system font).

### iM1 — Design system (2 d)
- Tokens exactly as in DESIGN-SYSTEM.md: colour (light/dark + semantic tints), type scale with Dynamic Type, spacing, radii, elevation, motion (with reduced-motion fallbacks), haptics.
- Components (DESIGN-SYSTEM.md §4): HeroAmount, DeltaChip, CategoryBar, PillTabBar + AddButton, IconButton, Avatar, SyncPill, ShopMark (monogram + cached-image slot), CategoryDot, Row family, DaySectionHeader, AttentionCard, Banner, Chip/FilterChips, SegmentedControl, Field, Buttons, Tray, OptionRow, ConfirmBar, ProgressMeter, StatusLabel, EmptyState, Toast.
- CatalogView, plus Previews per component (light + dark).

**You check:** CatalogView on your phone in light and dark; compare the tab bar, a Tray and the rows with the Main/H13 wireframes; turning on Reduce Motion stops the scale/slide animations.

### iM2 — API client, pairing & app shell (2 d)
- Codable models + `APIClient` (base URL, bearer token, `problem+json` → typed errors, timeouts), Keychain token store, MockServer.
- Onboarding O.1 Welcome → O.2 Server (URL, Test connection via `/v1/auth/status`) → O.3 Password (pair, show/hide, shake + error haptic on wrong password, message on 429 lockout) → O.4 Ready (first sync of shops/categories).
- ConnectionGate; a token-rejected screen (pair again, local data kept).
- RootTabView with 4 tabs (placeholders) + Add; Settings S + S.1 Server & sync (health, stale-backup warning, change URL, sign out = revoke this device).

**You check:** pair with the Mac mini over LAN (and Tailscale); a wrong password shakes; revoking the device on the server sends the app back to pairing; S.1 shows live health.

### iM3 — Home & receipts inbox (1.5 d)
- Home: "Spent in <month>" hero, delta vs last month, category bar + legend, attention card, receipts grouped by day with day totals, status sub-lines, tab badge.
- H-Empty first-run state, pull to refresh, polling while processing, H.2 filter (status, shop), paging.

**You check:** `shop import` some receipts → they appear and turn from processing to ok/needs review without touching the phone; the month total matches the server.

### iM4 — Capture & upload queue (2 d)
- Add tray C. Document scanner C.1, multi-page stitched vertically, JPEG ≤ 20 MB.
- C.2 Shop & date (last shop preselected). **S.3 Shops** list/add/edit, because capture needs shops.
- UploadQueue: persisted files + records, background `URLSession`, exponential backoff, survives app kill; 409 duplicate → warning + link to the existing receipt.
- SyncPill states and S.2 Upload queue (retry, delete).

**You check:** scan real receipts → they land on the server; a scan in airplane mode uploads after reconnecting; force-quitting mid-upload still finishes; scanning the same receipt twice shows the duplicate warning.

### iM5 — Receipt review (3 d)
- H.1 (header, reason banner, line list with category dots and mono raw text, indented discounts, All / To fix chips, Add line) + ConfirmBar (Σ lines vs total, meter, rolling numbers, Confirm always enabled).
- H.1.1 image viewer, H.1.2 edit details, H.1.3 edit/add line, H.1.4 link product (3 suggestions + search + new), H.1.5 actions (reparse, re-OCR, delete).

**You check:** review a real needs-review receipt in **under 90 s**; a line you link auto-links on the next receipt from that shop; edits survive Reparse.
**→ PLAN M6 is done here.** You use the app for every receipt while iM6+ is written.

### iM6 — Products & search (2 d)
- P list (search, category chips), P.1 detail (cheapest at, "you usually pay", price-per-unit chart per shop, recent purchases, receipt names, Add to list), P.1.1–P.1.4, S.4 Categories (rename, reorder), X.1 global search.

**You check:** "melk" finds products, receipt names and receipts; deleting a bad alias stops it auto-linking.

### iM7 — Unpack (1.5 d)
- C.3 continuous EAN scanner (known → success haptic + "✓ name", new → warning haptic), session tally; C.3.1 quick add with server-side Open Food Facts prefill + photo; C.3.2 nutrition; C.3.3 link to unmatched lines from the last 14 days.

**You check:** unpack a full haul; every new product gets added and linked; AH/Jumbo/Lidl barcodes come back prefilled.

### iM8 — Analytics (1.5 d)
- A (range, hero, monthly bars, by category / by shop, price-changes teaser), A.1 shop, A.2 category, A.3 price changes, A.4 range picker.

**You check:** for 3 ranges, app totals equal what the assistant reports.

### iM9 — Shopping list, offline (1.5 d)
- L (cheapest-at hints, "usually bought by now", check-off animation, clear checked), L.1 add, L.2 suggestions. JSON cache + operation log, sync on reconnect.

**You check:** in airplane mode, add, check and remove items; after reconnecting the server matches; an item the assistant adds shows up after a refresh.

### iM10 — Polish & audit (1.5 d)
- Motion and haptics pass, empty/error/toast states everywhere, Dynamic Type, VoiceOver labels, app icon, launch screen; screen-by-screen audit of the Previews against the wireframes, light and dark.

**You check:** the audit list; a week of daily use without a crash.

**Total ≈ 19 focused days.**

---

## 5. Risks
- **I can't compile here** (this Mac has Xcode 26.3, not 27). Mistakes show up on your first build, so expect a short fix-up round per milestone.
- **Folder references:** if your Xcode doesn't auto-add new files, each milestone's file list tells you what to drag in.

---

## 6. Questions
1. **Logo URL:** send it when ready; until then, monograms.
