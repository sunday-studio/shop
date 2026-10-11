# Tabular numbers

The design canvas uses `font-variant-numeric: tabular-nums` with a font stack `'GT America Num'`: GT America with the digits 0 to 9 taken from **GT America Mono** (`unicode-range: U+0030-0039`; Bold text uses Mono Medium). The app had `.monospacedDigit()` everywhere, but it does nothing: GT America Trial has no tabular feature (checked with CoreText: proportional digit widths 14 to 27 pt at 40 pt, no Number Spacing feature). So amounts, times and counts did not line up.

## Fix
`TypeStyle.text(_ string:)` (Design/Typography.swift) builds `Text` with the digit runs in the mono face at the same size and tracking, and everything else in the normal face. Same trick as the canvas. Face mapping: Regular to Mono Regular; Medium, Bold to Mono Medium.

```swift
TypeStyle.rowTitle.text(Money.format(cents)).foregroundStyle(Palette.ink)   // instead of Text(...).textStyle(.rowTitle)
```

`View.textStyle(_:)` still works but cannot change digits; use it only for text that never has digits.

## Done in Design/
All `Text(x).textStyle(style)` in Design/Components now use `style.text(x)` (about 100 places: hero amount, row amounts and prices, quantities, chips, chart labels, confirm bar, counts, dates). The count badge uses it too. `Banner` message text keeps `textStyle` (it is an attributed string).

## For the swift agent
Replace `Text(<anything that can show a number>).textStyle(<style>)` in Features/ with `<style>.text(<same>)`: amounts, prices, per-unit prices, quantities, counts, dates and times, receipt line text, Settings values (server version, queue counts), search result counts. Grep: `\.textStyle(` in Features/ (about 250 uses; plain words don't need changing, but converting all is harmless: a string without digits is one run in the normal face).
Where a `Text` has other modifiers (`.foregroundStyle`, `.lineLimit`, `.contentTransition(.numericText)`), they keep working on the result.
