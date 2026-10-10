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
