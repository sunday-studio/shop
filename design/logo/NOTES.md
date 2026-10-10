# Shop icon: notes

Open `index.html` for the contact sheet (squircle mask, 1024/180/120/60/29 px, light and dark, wordmark lockups). Sources are 1024x1024 full-bleed SVGs with no text and no baked-in mask; iOS applies the squircle.

## Directions

| File | Idea | Accents |
| --- | --- | --- |
| `icon-01-receipt.svg` | Zig-zag torn receipt, three line items, yellow total | blue, yellow |
| `icon-02-euro.svg` | Geometric euro sign, drawn as paths (trial font has no €) | green, yellow |
| `icon-03-bag.svg` | Ink grocery bag with a red sticker | yellow, red |
| `icon-04-tag.svg` | Tilted price tag with punched hole | pink, yellow |
| `icon-05-sfold.svg` | Ribbon folded into an S, yellow folds | purple, yellow |
| `icon-06-basket.svg` | Woven basket with a lemon | orange, yellow |

All use a flat offset "under-layer" in a darker tone for the tactile, Family-app feel.

## Recommendation: 01 Receipt

- It is the one object the app is about. Bag, basket and tag say "shopping"; the receipt says "I scan these and keep them", which is the product.
- Silhouette survives 29 px: a white upright slab with a toothed bottom on a saturated field. The line items are detail that disappears gracefully; the yellow total bar is the last thing to go.
- Matches the rules: saturated accent on white (blue #0A84FF, the primary accent, plus yellow #FFCC1A), tiny physical detail (the teeth, the shadow lip), no gradients, no text.
- A receipt is a generic object, so there is no trademark overlap with a supermarket brand; blue is also the app's primary accent, so icon and UI share one colour.
- The row structure (label bar + price bar, total bar) echoes the in-app dense receipt rows, so icon and UI feel like one system.

Runners-up: 05 S fold (most ownable, but the S gets blobby at 29 px) and 02 Euro (strong at small sizes, but reads as a banking app).

Caveats: the euro in 02 and the S in 05 are geometry, not the GT America glyphs, so they do not depend on the trial font. Wordmark "Shop" on the sheet uses GT America Bold, -0.04em; ship it as outlined paths in the final logo since the trial font licence does not cover embedding.

## iOS 18 variants (winner only)

| File | Appearance | Treatment |
| --- | --- | --- |
| `icon-01-receipt-light.svg` | Light / default | Blue #0A84FF field, white receipt, darker blue under-layer #0066D6 |
| `icon-01-receipt-dark.svg` | Dark | Field #0C0C0B, receipt #F4F4F1, under-layer flips to the blue accent so the colour survives on black. Total bar stays yellow |
| `icon-01-receipt-tinted.svg` | Tinted | Pure black field, greyscale artwork only (receipt #FFFFFF, lines #9A9A9A, total bar #C8C8C8). iOS maps luminance to the user's tint, so there is no colour and contrast comes from value alone |

When the Xcode project exists: Light goes in the `Any` slot, Dark in `Dark`, Tinted in `Tinted` of the single 1024 App Icon image set. Nothing is wired up yet (no asset catalog by request).

## Open points

- Check the tinted variant on a real device; the total bar (#C8C8C8 on #FFFFFF receipt) is deliberately close in value and may want a darker bar if it merges at 60 px.
- Final wordmark needs a licensed GT America and a € glyph if prices appear next to it.

## Update: chosen icon (grain + tilted receipt)

The blue receipt (01) was developed in rounds: glow and patterns (`glow.html`), grain and receipt redraws (`grain.html`), tilted receipts (`tilt.html`). Chosen: **tilt A**, `receipt3-a-tilt-9.svg`: thermal-line background with film grain, receipt tilted -9 degrees and running off the top edge, green dot header, dashed tear line, yellow total, barcode, soft drop shadow.

Final set, previewed in `final.html`:

| File | Appearance | Treatment |
| --- | --- | --- |
| `icon-final-light.svg` | Light | Blue gradient field, grain, thermal lines, white-blue halo |
| `icon-final-dark.svg` | Dark | Near-black field with a blue edge glow, warm-white paper, light grain |
| `icon-final-tinted.svg` | Tinted | Greyscale only (yellow becomes mid grey, green becomes darker grey), light grain |

These supersede the `icon-01-receipt-*` variants above. Caveats: grain and the drop shadow use SVG filters (feTurbulence, feDropShadow), so export the 1024 PNGs from a browser or a tool that supports filters and check them before they go into the asset catalog. Below about 60 px the barcode and tear line blur out; the receipt silhouette, yellow bar and tilt carry the icon at 29 px.
