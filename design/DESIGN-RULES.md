# Shop design rules (v3, shared by all screen work)

Source of truth for every wireframe and the SwiftUI app. If a rule here conflicts with a screen, the rule wins.

## Direction
Family app (trays, soft tactile controls, fluid motion), Honk (colour carries meaning, tiny physical details), Expense AI (dense receipt rows, saturated accents on white). Font: GT America + GT America Mono (already uploaded, see Fonts).

## Type
- **Numbers are tabular everywhere.** The bundled trial GT America files have no OpenType features at all (no `tnum`, proportional digits, no € glyph), so `font-variant-numeric: tabular-nums` does nothing with them. In the wireframes, digits 0-9 come from GT America Mono (genuinely fixed-width) through a `unicode-range` face named `GT America Num`, first in every artboard's `font-family` stack: `'GT America Num', 'GT America', ...`. Keep `font-variant-numeric: tabular-nums` on the root as well, so a licensed font with `tnum` also works. Weights: 400 and 500 use Mono Regular, 700 uses Mono Medium. SwiftUI: `.monospacedDigit()` on every Text with digits only works if the licensed font has `tnum`; verify the licensed OTFs with fontTools (`GSUB` features include `tnum`) before relying on it, and fall back to rendering digit runs in GT America Mono via AttributedString if not.
- **Bold (700) is only for screen and tray headers and the hero number.** Rows, labels, buttons, chips, tab labels: Regular 400 or Medium 500 only. Differentiate with colour (ink, ink2, ink3, accents) and size, not weight.
- Sizes: hero number 54–60, screen title 28, tray title 19, row title 15, row subtitle 12.5, caption 12. Hero and screen title letter-spacing -0.04em to -0.05em.
- Raw receipt text and barcodes use GT America Mono 500.
- Font faces (copy this block into `<helmet><style>`, as in `project/Main.dc.html`): the five `@font-face` rules with the `/_blob/...` urls already used there.

## Colour
Light: bg #FFFFFF, sunk #F4F4F3, line #F0F0EE, ink #1D1D1B, ink2 #73736F, ink3 #B4B4AF.
Dark: bg #0C0C0B, sunk #1C1C1A (trays #1C1C1A, inputs inside trays #262624), line #1F1F1D, ink #F4F4F1, ink2 #8F8F8A, ink3 #5C5C58.
Accents (same in both themes): blue #0A84FF, green #18B663, red #FF3B5C, yellow #FFCC1A, orange #FF8A00, pink #FF4FA3, purple #7C5CFF, amber (needs review) #FF9500.
Category colours: Zuivel blue, Groente & fruit green, Vlees & vis red, Brood orange, Voorraad yellow, Diepvries #14C3E0, Dranken purple, Huishouden #00B8A9, Verzorging pink, Overig grey.
Amber means "needs review" everywhere. A tray's tint carries meaning (destructive trays use red tint rows, as in H15).
Theme: each screen reads a `theme` prop (light|dark); the dark artboard `D-<name>.dc.html` is a `<dc-import name="<name>" theme="dark">` wrapper. Keep that pattern.

## Shop marks (replaces the big coloured shop tiles)
- Smaller: **22px** rounded-6px square in lists, 28px in a header, never bigger than 32px.
- Real app: logo from logo.dev (`https://img.logo.dev/<domain>?token=<publishable key>&size=64&format=png`, cached on disk per shop). Domains: AH `ah.nl`, Lidl `lidl.nl`, Jumbo `jumbo.com`, Dirk `dirk.nl`, Ekoplaza `ekoplaza.nl`.
- The wireframes cannot load external images and logo.dev needs a token we do not have, so draw each mark as a 22px square with a 1px inner border (`{{t.line}}`), white fill (dark theme: #F4F4F1 fill, so real logos on white stay legible), and the shop's monogram in its brand colour at 9.5px Medium: AH #0A84FF "AH", Lidl #0050AA "Li", Jumbo #FFB000 "J", Dirk #E2001A "D", Ekoplaza #2E7D32 "E". Put the markup in an element with `data-shop-mark="ah"` so a real `<img>` can replace it later.
- Anywhere a shop is the row's lead element, use the small mark inline before the name, not a 36–40px tile.

## Density
Flat rows, 44–52px, hairline dividers, no card backgrounds for lists. Group by day with a day-total header. Aim for 9–12 rows visible on a 844px screen.

## Motion
Springs and curves (use CSS custom properties on the artboard root):
- `--spring: cubic-bezier(0.32, 1.28, 0.5, 1)` for trays and toggles (about 420ms), `--ease: cubic-bezier(0.22, 1, 0.36, 1)` for fades and slides (240ms), `--press: 90ms ease-out`.
- Tray enter: translateY(24px) + opacity 0 to rest, 420ms spring, scrim fades 240ms. Tray exit: 240ms ease, reverse.
- Tray-to-tray inside one artboard: height animates (`transition: height 360ms var(--spring)`) while content cross-fades 160ms. Heights must differ between consecutive trays.
- Press: every tappable scales to 0.97 on `:active`, 90ms in, spring back out. Pills and the + button scale 0.94.
- Numbers morph by tweening digits (roll up/down 180ms) when totals change (Confirm bar sum, tally). Labels morph when a button changes meaning (Continue to Confirm): shared letters stay put.
- Lists: rows fade+slide in 8px with a 24ms stagger on first paint (max 8 rows). Checking off an item: circle fills with a 200ms spring, row text strikes and fades to ink3 after 160ms, then the row collapses over 240ms.
- Tabs: content slides 12px in the direction of the tapped tab, 200ms ease. The active tab icon fills with a 120ms scale pop.
- Respect `@media (prefers-reduced-motion: reduce)`: drop scale/slide, keep 120ms fades.
Cross-artboard links replace the whole artboard, so shared-element morphs cannot be shown across artboards. Show them only inside one artboard through state.

## Haptics (annotate with `data-haptic="..."` on the element, no visual change)
- `selection` tick: segmented controls, filter chips, category/shop picks, tab change, pickers, list reorder steps.
- `light` impact: tap on primary buttons, + button, opening a tray.
- `medium` impact: Confirm receipt, Link product, Save product.
- `soft` impact: check off a list item, swipe row snaps.
- `success` notification: receipt reaches ok, barcode known, sync finished, pairing succeeded.
- `warning` notification: total mismatch appears, duplicate receipt, offline, sync pill turns amber.
- `error` notification: wrong password, delete confirmed, upload failed.
- Barcode scanning: `success` for a known product, `warning` for a new one.

## Interactivity
- Every tappable thing is a real `<a href>` to the right artboard (see `design/ia.html` flows) or a `<button>` with an `onClick` that changes `state` (toggles, selections, check-offs, segmented controls, tray open/close). Selected states must visibly change.
- Inputs are real `<input>`s with stable ids.
- This is a mobile app: no hover states. Pressed (scale), selected, disabled, focus-visible (outline 2px #0A84FF) only.

## Auth and device setup
URL + password. The app posts the password to the server, the server returns a bearer token, the token is kept in the Keychain. The password is never stored. Flow: Welcome, Server (URL, test connection), Password (field with show/hide, "Connect"), Ready. States to show: testing, wrong password (red tint, error haptic, field shakes once), server unreachable, connected. Settings: Server & sync shows URL, "Signed in", Change URL, Re-pair / Unpair this phone.

## File ownership (agents must not touch each other's files)
- Onboarding/sync: O1-O4, D-O1-O4, S1, D-S1, S2, D-S2 (+ any new `O3b-*`, `D-O3b-*`).
- Home/review: Main, D-Main, H-Empty, D-H-Empty, H1, D-H1, H13, D-H13, H14, D-H14, H15, D-H15.
- Add/Analytics: C*, D-C*, A*, D-A*.
- Products/List/Settings/Search: P*, D-P*, L*, D-L*, S-Settings, D-S-Settings, S3, D-S3, S4, D-S4, X1, D-X1.
- `project/canvas.json` is owned by the lead. Never edit or publish it. Report new files and sizes in your final message instead.

## Mobile states (important states exist as their own frames, not as demo toggles)
Do not put prototype-only controls (such as a 'Demo outcome' switcher) on a screen. A state that matters on a phone gets its own artboard (and a dark twin), reachable by a real link where the flow allows. States to cover per screen family: loading (skeleton rows, not spinners alone), empty (first run and no results), error (inline and full-screen with a clear next step), offline, permission denied (camera, notifications), disabled/in-progress buttons, session expired, and server-unreachable. Hover states are not a concern.
