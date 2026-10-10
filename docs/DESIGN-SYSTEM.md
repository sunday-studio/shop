# Shop Design System (SwiftUI)

Extracted from the 32 screens of the **Shop Wireframes** design project and [design/DESIGN-RULES.md](../design/DESIGN-RULES.md). Every value below was measured from the wireframe sources (counts in brackets show how often it occurs). Where wireframes and rules disagree, the rules win.

Lives in `ios/Shop/DesignSystem/` (Tokens, Components, Catalog). Screens may only use these tokens and components; a raw hex, font size or radius in feature code is a review failure.

**Status: draft, part of [IOS-PLAN.md](IOS-PLAN.md), waiting for approval.**

---

## 1. Colour

Semantic tokens, resolved per colour scheme (`Color(light:dark:)` via `UIColor { traits in … }`).

### Surfaces & ink
| Token | Light | Dark | Use |
|---|---|---|---|
| `bg` | #FFFFFF | #0C0C0B | Screen background |
| `sunk` | #F4F4F3 | #1C1C1A | Icon buttons, chips, fields on screens, meter track |
| `tray` | #FFFFFF | #1C1C1A | Trays, tab bar (`bar`), confirm bar |
| `trayField` | #F4F4F3 | #262624 | Fields/options **inside** a tray |
| `line` | #F0F0EE | #1F1F1D | Hairline dividers |
| `ink` | #1D1D1B | #F4F4F1 | Primary text |
| `ink2` | #73736F | #8F8F8A | Secondary text, labels |
| `ink3` | #B4B4AF | #5C5C58 | Tertiary, currency symbol/cents in hero, inactive tab |
| `btn` / `btnInk` | #1D1D1B / #FFFFFF | #F4F4F1 / #0C0C0B | Primary button, Add button, selected chip |
| `scrim` | rgba(29,29,27,.34) | rgba(0,0,0,.62) | Behind trays |
| `seg` | #FFFFFF | #3A3A37 | Selected segment knob |

### Semantic tints (status carries colour; text/tint pairs)
| Token | Light text / tint | Dark text / tint | Meaning |
|---|---|---|---|
| `amber` | #B25E00 / #FFF0D9 | #FFB340 / #33230A | **Needs review**, everywhere. Solid dot/badge #FF9500 |
| `green` | #0B8A47 / #E2F7EA | #3DDC84 / #0F2A1B | Saved money, discounts, "adds up", success |
| `red` | #D1281C / #FFE8E6 | #FF6B61 / #3A1411 | Destructive, failed, error |
| `blue` | #0071E3 / — | #4DA3FF / — | Links ("Add line"), processing sub-line |

### Accents (same both themes)
blue #0A84FF · green #18B663 · red #FF3B5C · yellow #FFCC1A · orange #FF8A00 · pink #FF4FA3 · purple #7C5CFF · amber #FF9500 · avatar #FFD84D.

### Category colours (`CategoryColor`)
Zuivel blue · Groente & fruit green · Vlees & vis red · Brood orange · Voorraad yellow · Diepvries #14C3E0 · Dranken purple · Huishouden #00B8A9 · Verzorging pink · Overig #D6D6D2 / dark #3A3A37. Unknown categories hash to the accent list.

### Shop brand colours (monogram fallback)
AH #0A84FF "AH" · Lidl #0050AA "Li" · Jumbo #FFB000 "J" · Dirk #E2001A "D" · Ekoplaza #2E7D32 "E". Other shops: first letter, colour hashed from name.

---

## 2. Typography

Fonts: **GT America** 400/500/700, **GT America Mono** 400/500 (bundled). All text scales with Dynamic Type via `Font.custom(_:size:relativeTo:)`. **Every `Text` with digits uses `.monospacedDigit()`** — built into the style, not opt-in.

| Style | Size | Weight | Tracking | Relative to | Use |
|---|---|---|---|---|---|
| `hero` | 60 (symbol/cents 40, `ink3`) | 400 | −0.05em | largeTitle | Home, Analytics totals |
| `heroDetail` | 48 (30) | 400 | −0.05em | largeTitle | Receipt total |
| `screenTitle` | 28 | 700 | −0.04em | title | Screen headers |
| `emptyTitle` | 22 | 400 | −0.03em | title2 | Empty-state headline |
| `trayTitle` | 19 | 400 | −0.02em | title3 | Tray headers |
| `navTitle` | 16 | 500 | 0 | headline | Centered pushed-screen title |
| `button` | 16 | 500 | 0 | body | Buttons |
| `input` | 16–17 | 400 | 0 | body | Fields |
| `rowTitle` | 15 | 500 | 0 | body | Row titles, amounts |
| `body` | 14–14.5 | 400 | 0 | callout | Banner text, paragraphs |
| `label` | 13 | 500 | 0 | subheadline | Day headers, section labels |
| `rowSub` | 12.5 | 400 | 0 | footnote | Row subtitles |
| `caption` | 12 | 400/500 | 0 | caption | Field labels, chips |
| `tab` | 10.5 | 500 | 0 | caption2 | Tab labels |
| `mono` | 13.5–15.5 | Mono 500 | 0 | body | Raw receipt text, URLs, barcodes, CLI commands |

Weight rule: 700 only for `screenTitle`, tray headers that are screen-like, and the hero. Everything else 400/500; differentiate with colour and size.

---

## 3. Layout, shape, elevation, motion, haptics

**Spacing** (pt): 2 · 4 · 6 · 8 · 10 · 12 · 14 · 16 · 20 · 22. Screen gutter **20** (16 for headers, banners, trays inset **8** from screen edge). Header top padding follows the safe area (wireframe 56 ≈ status bar + 12).

**Radii** (continuous corners): `xs 3` (category dot/bar) · `s 8` (chips-small, badges) · `m 11–12` (avatar, segment) · `l 16` (fields, option rows, attention card) · `xl 18` (banners, icon buttons = half height) · `tray 36` · `bar 34` (confirm bar) · `pill = h/2` (buttons, tab bar).

**Heights**: icon button 36 (32 inside trays) · row 44–54 (receipt row 54, line row ≥ 46) · chip 36 / 24 small · button 54 (tray), 50 (confirm bar) · tab bar & Add button 62 · attention card 48 · segment 34.

**Elevation**
| Token | Light | Dark |
|---|---|---|
| `barShadow` | 0 10 30 rgba(29,29,27,.12) + 1pt ring rgba(29,29,27,.04) | 0 10 30 rgba(0,0,0,.5) |
| `trayShadow` | 0 10 40 rgba(29,29,27,.16) + ring .05 | 0 −4 30 rgba(0,0,0,.6) |
| `segShadow` | 0 1 3 rgba(29,29,27,.12) | none |

**Motion** (`Motion` namespace; all fall back to 0.12 s opacity under Reduce Motion)
| Token | Curve | Use |
|---|---|---|
| `spring` | `.spring(response: 0.42, dampingFraction: 0.72)` ≈ cubic-bezier(.32,1.28,.5,1) 420 ms | Tray enter, toggles, tray height change (360 ms) |
| `ease` | `.timingCurve(0.22, 1, 0.36, 1, duration: 0.24)` | Fades, slides, tray exit, scrim |
| `press` | 0.09 s ease-out to scale 0.97 (pills / + 0.94), spring back | `PressableButtonStyle` on every tappable |
| `rowStagger` | fade + 8 pt slide, 24 ms stagger, first 8 rows | Lists on first paint |
| `numberRoll` | `.contentTransition(.numericText())`, 0.18 s | Totals, tallies |
| `checkOff` | circle fill spring 0.2 s → strike + fade to ink3 after 0.16 s → collapse 0.24 s | List items |
| `tabSlide` | 12 pt in tap direction, 0.2 s; icon fill pop 0.12 s | Tabs |

**Haptics** (`Haptics.play(.x)`, via `sensoryFeedback` where possible)
`selection` chips, segments, pickers, tab change · `light` primary buttons, +, tray open · `medium` Confirm receipt, Link product, Save product · `soft` list check-off, swipe snap · `success` receipt ok, barcode known, sync done, paired · `warning` total mismatch, duplicate, offline, new barcode · `error` wrong password, delete confirmed, upload failed.

---

## 4. Components

Each component: a SwiftUI view in `DesignSystem/Components`, a Catalog entry, and `#Preview`s (light, dark, XL text).

| Component | Anatomy (from wireframes) | Screens |
|---|---|---|
| `HeroAmount` | € and cents in `ink3` at 2⁄3 size, euros in `ink`, baseline-aligned, nl_NL grouping (`2.614`) | H, A, H.1, A.1, A.2 |
| `DeltaChip` | Arrow + amount on green/red tint, radius 8, then secondary text | H, A |
| `CategoryBar` + legend | 10 pt stacked bar, 3 pt gaps, radius 3; legend dots 8 pt + name + amount | H, A |
| `PillTabBar` + `AddButton` | 62 pt floating pill (`tray`, `barShadow`), 4 tabs of icon 22 + label; detached 62 pt `btn` circle with +; badge 16 pt amber with 2 pt `bar` ring; bottom fade gradient 140 pt | All tab roots |
| `IconButton` | 36 pt circle `sunk`, 18 pt glyph (back, more, search, close-in-tray 32 pt) | Headers, trays |
| `Avatar` | 36 pt, radius 12, #FFD84D, initial | Header left → Settings |
| `SyncPill` | 32 pt capsule `sunk`, 8 pt status dot + label ("Offline · 1 waiting") | Header centre, only when not connected/idle |
| `ShopMark` | 22 pt (28 header, max 32), radius 6, 1 pt `line` inner border, white fill (#F4F4F1 dark), 9.5 pt Medium monogram in brand colour; when a logo URL exists, the image (disk + memory cached via `ImageCache`) replaces the monogram once loaded | Every shop-led row |
| `CategoryDot` | 8 pt, radius 3 | Line rows, legends |
| `DaySectionHeader` | 13 pt Medium `ink2`, label left, total right | H, receipts lists |
| `Row` family | Flat, hairline bottom, no card. `ReceiptRow` (mark, shop, status sub-line coloured by status, amount), `LineRow` (dot, name or mono raw, sub, amount; indented child for discounts; green for discounts), `ProductRow`, `ListItemRow` (24 pt check circle, 2 pt `ink3` ring) | H, H.1, P, L, X, A.* |
| `AttentionCard` | 48 pt, radius 16, amber tint, count badge 22 pt, label, hint, chevron | H |
| `Banner` | Radius 18, tinted (amber/red/green), 20 pt badge glyph, 14 pt text with Medium lead sentence | H.1, S.1 |
| `Chip` / `FilterChips` | 36 pt capsule, `sunk`/`btn` selected; small variant 24 pt radius 8 (All 21 / To fix 3) | H.1, H.1.3, P, A |
| `SegmentedControl` | Track `sunk` radius 12, 3 pt inset; 34 pt knob `seg` with `segShadow`, spring | A, A.4 |
| `Field` | Tile radius 16 `sunk`/`trayField`, 12 pt `ink2` label above 16–17 pt input; mono variant; grid of 2 | O.2, O.3, H.1.2, H.1.3, C.3.1, C.3.2, L.1 |
| `Button` | `.primary` (`btn`, 54/50 pt pill), `.secondary` (`sunk`), `.link` (blue text + glyph), `.destructive` (red tint) | Everywhere |
| `Tray` | Custom bottom sheet: inset 8, radius 36, `tray`, scrim; header (19 pt title + 32 pt close); spring enter (y+24, fade), ease exit; height animates between trays with 160 ms cross-fade | C, H.1.3–H.1.5, L.1, A.4, C.2 |
| `OptionRow` | 52 pt, radius 16, `trayField`, icon 18 + title + hint; destructive variant red tint | H.1.5, C |
| `ConfirmBar` | Floating radius 34 bar, "Lines €x · Receipt €y", `ProgressMeter`, delta text, primary button | H.1 |
| `ProgressMeter` | 6 pt track `sunk`, fill coloured by state (amber off, green match) | H.1, S.2 |
| `StatusLabel` | One colour + label per receipt status (pending/processing/needs_review/ok/failed) | H, X, A.1 |
| `EmptyState` | Illustration (stacked tilted receipts), 22 pt headline, 14.5 pt body | H-Empty, P, L, X |
| `Toast` | Small capsule above the tab bar, auto-dismiss, haptic | Errors, sync done |
| `PriceChart` | Swift Charts line per shop, 2 pt strokes, dashed reference "you usually pay", minimal axes in `ink3` 11.5 pt | P.1, P.1.1 |
| `SpendBars` | Swift Charts bars, radius 6 top, current month `ink`, others `sunk`, dashed gridlines | A, A.1, A.2 |
| `ScannerOverlay` | Camera viewfinder with rounded cut-out, known/new result row tray, session tally | C.3 |

Formatting helpers (in `Core/Money` and `Core/Dates`, used by components): `Money.format(cents)` → `€ 1,29` / `€2.614,90`, per-unit `€0,85 /l`, signed discounts `−0,45`; dates "Today", "Yesterday", "Thursday 8 Oct", times `18:42` in Europe/Amsterdam.

---

## 5. How fidelity is verified
No automated tests (see IOS-PLAN §0). Instead:
1. **Catalog** screen renders every component and state; open it on the phone in light and dark.
2. **Previews**: every component and screen has light and dark `#Preview`s fed by `MockData`; compare them with the wireframe artboards at 390×844.
3. **Token discipline**: feature code uses only `DesignSystem` tokens. No `Color(red:)`, hex, `.font(.system(` or raw radius numbers outside `DesignSystem/Tokens`. I check this by reading the code before each hand-off.
