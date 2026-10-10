# Swipe actions

Decision: use native `List` + `.swipeActions` (iOS 26 floating capsules), **icon only** for now. No custom gesture code.

Evidence: `docs/design-frames/swipe/` (current custom slab vs native). Mobbin refs: Apple Mail, Messages, Fiverr (floating); Shopee, Telegram, Yuka (full-bleed, rejected).

## Rules
- Max 2 actions per side.
- Leading edge = safe, positive action. Only this one may full-swipe.
- Trailing edge = Edit (blue) and Delete (red). Delete never full-swipes; it opens the existing confirmation tray.
- Icon-only: every action needs `.accessibilityLabel` (use `Label(...).labelStyle(.iconOnly)`, which keeps the VoiceOver name).
- Tints: green = confirm/check off/add, blue = neutral, red = destructive. Set `.tint` explicitly (Delete renders blue otherwise).
- Rows in a List: `.listRowInsets(EdgeInsets())`, `.listRowSeparator(.hidden)`, `.listRowBackground(Color.clear)`, `.listStyle(.plain)`, `.scrollContentBackground(.hidden)`.

## Per list
| List | Leading (full swipe) | Trailing |
|---|---|---|
| Home receipts needing review | Confirm `checkmark` | Edit `pencil`, Delete `trash` |
| Shopping list | Check off `checkmark` | Delete `trash` |
| Products | Add to list `cart.badge.plus` | Delete `trash` |
| Upload queue | Retry `arrow.clockwise` (failed only) | Remove `xmark` |
| Receipt lines | none | Edit `pencil`, Delete `trash` |

## Haptics
System plays the threshold tick. We play only the committed action's own haptic (Confirm = success, Check off = light, Delete = warning via tray).
