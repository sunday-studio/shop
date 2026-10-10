# Design status (handoff)

Written for an agent with no context from the design conversation. Last updated 2026-10-10.

**Caveat first:** the wireframe canvas has never been visually checked by the author (the viewer needs a sign-in the agent did not have). Layout bugs are likely. Open it and look before building on it.

## 1. Where the design lives

### The canvas (current, the main deliverable)
**Shop Wireframes**: https://claude.ai/artifact/LVSTnG3RYKArPtpCcCu4mG (Design-type artifact, 128 interactive 390-wide frames; every frame has a `D-<name>` dark twin; frames link to each other, so it is clickable). Sections, top to bottom:

1. Onboarding: connect to your server (O1 to O4)
2. Home & receipt review (Main, H1 receipt, H13 edit line, H14 link product, H15 actions)
3. Add: scan receipts, unpack groceries (C-Add, C2, C3, C31 to C33)
4. Analytics (A, A1 shop, A2 category, A3 price changes, A4 range)
5. Products & shopping list (P, P1, P14, L, L1)
6. Settings, sync & search (S, S1 to S4, X1)
7. States: onboarding & sync (unreachable, wrong password, locked, session expired, upload failed)
8. States: home & receipts (loading, failed, duplicate, processing)
9. States: add & analytics (camera denied, not found, offline, empty, loading)
10. Trays & states: shops, categories, products, list (add/edit shop, add category, empty/no-results)
11. English names (E1 queue, E1b, E2 teach word, E2b updated, E3 edit English, E4 glossary, E4b, E5 all done, E6 badges)

The canvas files are not in the repo. Edit them through the Artifact tool (read the artifact first, then publish with `root`, `file_path`, `files`).

### Repo files
| File | Status | What it is |
|---|---|---|
| `design/DESIGN-RULES.md` (v3) | **Current, wins over everything** | Type, colour, density, motion, haptics, mobile states, file ownership |
| `design/ia.html` (v0.4) | **Current but stale in places**: still lacks the state frames and English names; the auth wording was updated to pair. Its "Review" line says "one review screen, not a queue" | Navigation model, sitemap, flows, screen inventory |
| `design/MOTION.md`, `design/motion.html` | Current | Spring/ease tokens, tray behaviour, live motion demo |
| `docs/DESIGN-SYSTEM.md` | **Draft, partly superseded**: its line "extracted from the 32 screens" predates the 128-frame canvas; it says `ios/Shop/DesignSystem/` while the code is in `ios/Shop/Design/`; its `line` colour (#F0F0EE / #1F1F1D) is outdated, see section 3 | SwiftUI token and component spec |
| `docs/IOS-PLAN.md` | Current plan for the app | Architecture and build order |
| `design/logo/` | **Current**: `icon-final-light/dark/tinted.svg` (tilted receipt on grain) and `ios/Shop/Resources/Assets.xcassets/AppIcon.appiconset` PNGs. Everything else in the folder (`icon-01` to `-06`, `receipt-*`, `receipt2-*`, `receipt3-*`) is exploration, **superseded** | App icon |
| `design/fonts/*.woff2`, `ios/Shop/Resources/Fonts/*.otf` | Current, but see open question 1 | GT America files |

## 2. What is designed

Legend: **Final** = iterated with the user and no open complaints. **Draft** = built, not reviewed after the last changes. **Not designed** = nothing exists.

| Area | Status | Notes |
|---|---|---|
| Onboarding / pairing | Final (flow), Draft (states) | URL, test connection, password, ready; unreachable, wrong password, locked, session expired as own frames |
| Home / receipts list | Final | Spending this month hero, category bar, day-grouped dense rows, amber review banner |
| Receipt review | Final (main), Draft (states) | One screen per receipt: image, lines, sticky sum vs total bar; failed, duplicate, processing are Draft |
| Products | Final | List with English-name banner, product detail with price history and English name, receipt names |
| Unpack / barcode | Draft | C3 unpack, C31 quick add, C32 nutrition, C33 link line, not-found. Never reviewed in detail by the user |
| Shopping list | Draft | L, L1, empty |
| Analytics | Final (main), Draft (rest) | Own tab; A1 shop detail always opens on Albert Heijn (links cannot carry parameters, the chips switch shops); A-Empty and A-Loading Draft |
| Settings | Final | Server & sync, queue, shops, categories, search |
| English names E1 to E6 | Draft | Glossary (E4) was redone twice in the last session and not looked at after; matches backend spec §6a / openapi 0.6.0 |
| Tab bar | Draft | Redesigned last session: active tab is a filled capsule with label, others icon-only |
| App icon | Final pick, Draft assets | Tilted receipt; PNGs exist, never opened in Xcode (no project yet) |
| Dark mode | Draft | Every frame has a twin generated from the same file with a `theme` prop |
| Tablet / landscape / iPad | Not designed | Phone portrait only |
| Widgets, share sheet, notifications, Dynamic Type sizes, VoiceOver order, localisation | Not designed | |
| Device management screen (list/unpair phones) | Not designed | Backend has `GET /v1/auth/devices` and `DELETE /v1/auth/devices/{id}`; Settings only shows "Re-pair" and "Unpair this phone" |
| Real logos | Not designed | Frames use monogram placeholders; the real app loads logo.dev (key not supplied, see open questions) |

## 3. Decisions and why

Easy to undo by accident, so read these:

- **Auth is pairing only.** The app sends URL + password to `POST /v1/auth/pair` and gets a per-device token (Keychain). Unpair is `DELETE /v1/auth/devices/{id}` (find the `current` device via `GET /v1/auth/devices`). `/auth/login` and `/auth/logout` were deliberately removed: they duplicated working, tested code. The password is never stored on the phone. Do not reintroduce a login or token-paste screen.
- **Review is one screen per receipt, not a swipe queue.** `ia.html` says this explicitly. Home lists receipts that need review and each opens its own review screen. (A teammate message described it as "a queue"; the design does not do that. Confirm with the user before changing.)
- **Every price is per unit** (per kg, l or piece) after linked discounts, formatted the Dutch way (`€ 1,29`), wherever shops are compared.
- **Light/dark parity.** Every screen reads a `theme` prop and has a dark twin; a change to a screen must be checked in both. Dark trays are #1C1C1A, inputs inside trays #262624.
- **Tabular digits everywhere.** The trial GT America files have no `tnum`, so the wireframes draw digits 0 to 9 from GT America Mono via a `GT America Num` unicode-range face. In SwiftUI, verify the licensed font has `tnum` before relying on `.monospacedDigit()`, otherwise render digit runs in the Mono font.
- **Bold (700) only for screen/tray titles and the hero number.** Everything else is Regular or Medium; differentiate with colour and size.
- **Divider and header bands (last session):** `line` is now #E2E2DE light / #30302D dark (darker than the rules file and `DESIGN-SYSTEM.md` say), and group headers sit on a rounded `sunk` band. These two files still show the old values; update them to match.
- **Family-style trays, no hover states** (it is a phone). Important states (loading, empty, error, offline, permission denied, session expired) are their own frames, not demo toggles.
- **Paper receipts only,** via the document scanner; no manual receipt entry, no share sheet import. Capture and sharing are one flow.
- **Name is Shop** (not Pantry). One user, single server, the server is home and the phone a cache.
- **English names:** amber means "needs English"; teaching is single words only; "Keep as is" can leave text unchanged but the product still leaves the queue, so "Updated N products" is accurate.
- **Colour pops on white.** Saturated accents, no grey page background.

## 4. Open questions for the user

1. **GT America may be a trial licence.** The files were `GTAmericaTrial-*` renamed without the suffix and the trial licence does not cover embedding. Confirm or buy a licence **before the repo is shared or published** (the fonts are committed in `design/fonts/` and `ios/Shop/Resources/Fonts/`).
2. **logo.dev publishable key** has not been supplied; real shop logos cannot load until it exists.
3. Review: keep one screen per receipt (current) or add a queue mode?
4. Does the user want a device-management screen (list and unpair other phones)?
5. Has the user looked at the canvas since the tab bar, divider and glossary changes? Nobody has confirmed they look right.

## 5. Next design tasks, in order

1. **Look at the canvas** (open it, check light and dark) and fix layout breakage, starting with E4 glossary, the new tab bar and the grey header bands.
2. **Sync the written docs** to the canvas: update `ia.html` (state frames, English names, new tab bar) and `DESIGN-RULES.md` / `DESIGN-SYSTEM.md` (new `line` colours, header bands, tab bar spec, `Design/` path).
3. **Review the Draft areas with the user**: unpack and barcode flow, shopping list, analytics states.
4. **Per-shop Analytics screen** so each shop row opens its own shop instead of Albert Heijn.
5. **Device management and accessibility pass** (device list, Dynamic Type, VoiceOver order), then hand the canvas to the SwiftUI implementation.

## Experience pass (iOS feel + haptics), 2026-10-10
- Haptics DONE in Design/: new events trayOpen, trayDismiss, scanKnown, scanNew, uploadQueued, uploadFailed; Haptics.play drops impacts <90ms after another and anything <250ms after a notification (no double fire). Tray open uses trayOpen, drag-dismiss uses trayDismiss; Add (+) no longer fires light before the tray's own haptic.
- Call sites in Features/ still to switch (routed via coordinator): UnpackModel.swift:38,44,72-74 -> scanKnown/scanNew/error; AddViews.swift:330 -> uploadQueued (not .light); upload-failure paths -> uploadFailed.
- Simulator feel testing: NOT started (no booted simulator; waiting for swift agent build). Next: boot iPhone 17 Pro, launch with -mock, walk flows, list hiccups here.
- Hiccups fixed in Design/ (static read, unverified in sim): (1) Tray drag-dismiss reset `drag = 0` before the exit transition, so the tray snapped back up then faded; now it fades from where the finger left it and resets on next present. (2) StaggeredAppear replayed its fade whenever a lazy row was recreated on scroll back; rows past the stagger cap now start shown.
- Still to check in sim: tray DragGesture(minimumDistance 12) vs scroll views inside trays; tab slide transition usage; loading-state flashes.
- Sim pass 1 (iPhone 17 Pro, -mock, ~8 fps frame grabs via `simctl io screenshot`; coarse, can't see single dropped frames). Walked: Home, receipt open, Add tray, Analytics, Products, List tab switch. NOT yet walked: unpack/scan, Settings, search, keyboard, rows swipe, list check-off.
  Design/ fixes: tray insertion faded on the 420ms spring so the list showed through the card (Motion.trayFade, 100ms, needs sim re-check after rebuild).
  Features/Core findings sent to coordinator (camera prompt on launch, receipt detail placeholder flash).
- Sim pass 2: Animation Hitches in Instruments is "not supported on this platform" on the simulator (xctrace), so Home-scroll/tab-switch hitch numbers need a physical device. Walked Settings and Search: Search pushes in, keyboard arrives after the push (fine). Settings Haptics toggle sits ~4pt past the row hairlines' right edge (cosmetic, Features/Settings).
- Still not walked: unpack/scan flow, list check-off, swipe rows, typing in search, Add > Scan. Re-verify tray fixes 1-3 after the swift agent's next build.
- Sim pass 3 (build with ConfirmBar isLoading + cross-fade): receipt opens with summary, bar shows "Receipt €38,13 · Reading lines…" with Confirm disabled, then fills in with no garbled digits in the frames I caught. Note: the lines area is blank for ~1-2 frames before rows appear (skeleton rows would hide it; Features/Receipt). One run showed an unrequested "Edit line" tray after a single tap; a clean re-run didn't reproduce it, treated as a tap-injection artefact.
- Still not walked: unpack/scan, list check-off, swipe rows, typing in search.
- Sim pass 4 (latest build): verified tray drag-dismiss (slides and fades, no snap-back), list check-off (circle fills green at once, strike at 160ms, row collapses after the screen's 420ms), search typing (results update live, filter chips fine). Double haptics fixed: SuggestionChip touch-down removed (ShoppingListView:27 plays its own), ProductDetailView addToList duplicate removed by swift agent. Open Features item: Home cold start flashes "€0,00 / Totals show up once a receipt is read" then rolls to €143,41 (hero needs a loading state, no roll from 0). Not testable in sim: unpack/scan (no camera), swipe rows (only ProductDetailView has swipeActions), real hitches (needs device).
- Upload flow design (2026-10-10): spec in docs/DESIGN-UPLOAD-FLOW.md (entry points, import screen with grouping/shop/date, straighten & crop, progress/queue/failures, unknown-shop suggestion, haptics, motion, copy, backend asks, acceptance checklist). New Design/ components: Components/Import.swift (UnknownShopMark, PageStrip, ImportReceiptRow, JoinPill, UploadProgressCard, ShopSuggestionCard), Components/CropOverlay.swift (CropQuad, CropOverlay), ReceiptRow(isUnknownShop:), Catalog "Import" section. Compiles (separate DerivedData build). Features/ and Core/ untouched.
