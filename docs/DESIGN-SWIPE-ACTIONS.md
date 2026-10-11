# Swipe actions: pull to act (version 2)

Decision after device testing: native `.swipeActions` "doesn't feel good" (user, on a real iPhone, pointing at Outlook mobile). Replaced by a **pull-to-act row**: one action per side, a full-bleed colour panel behind the row that grows with the finger, release while armed commits. Component: `SwipeActionRow` in `Design/Components/SwipeRow.swift`, prototype at the top of the Catalog. Frame: `docs/design-frames/swipe/pull-to-act-armed.jpg` (green panel, armed). The native version (version 1) is superseded; its frames stay for the record.

What I could and could not confirm about Outlook: static Mobbin screens don't show the swipe, and nothing public describes its thresholds. So the numbers below are my design, built from what is known (one action per side, panel and icon follow the finger, colour change and haptic at a threshold, release commits), and they need a device pass.

## Behaviour
- **One action per side.** Right swipe (leading): the safe, positive action. Left swipe (trailing): Delete. No Edit on swipe (tap opens the row's edit).
- **Panel:** full-bleed behind the row, the action's tint. Before the threshold it is the tint at 55 %, icon small and fading in; at the threshold it becomes the full colour and the icon pops (scale 1.12, `Motion.pop`). The icon is 22 pt, white, pinned 8 + 28 pt from the revealed edge. No label (icon only), VoiceOver gets the title and custom actions.
- **Arm threshold:** 28 % of the row width, never less than 96 pt (112 pt on a 402 pt screen).
- **Haptic:** one `selection` tick when the threshold is crossed (again if you cross it back and forth). Release while armed plays the action's own haptic (Confirm `medium`, Check off `soft`, Add to list `light`; Delete none, its tray plays `trayOpen`). Release before the threshold: no haptic.
- **Flick:** a flick faster than 650 pt/s in the swipe direction commits even below the threshold, once the row has moved at least 12 % of the width.
- **Physics:** the row follows 1:1 up to 1.6 × the threshold, then at 35 % (heavier). Swiping on a side with no action rubber-bands to 24 pt. Release below threshold springs back (response 0.34, damping 0.82).
- **After commit:** actions that remove the row (`removesRow: true`) slide the row fully off in 0.2 s, then run; the list collapses the row. Actions that open a tray or only change state spring back and run at once. Delete always opens the confirm tray (or shows an Undo toast), so it springs back.
- **Gesture rules:** a UIKit pan on the row's hosting view that only begins for a horizontal move (width at least 1.6 × height) inside the row, at least 20 pt from the left edge so the system back swipe still works. It does not run alongside other recognizers, so it cancels the Button inside the row: a swipe never also taps (checked: swipe leaves the tap count at 0, a tap counts 1). No flag for the rows to check. Reduce Motion: no pop, 0.12 s fades instead of springs.

## Per list
| List | Right (leading) | Left (trailing) |
|---|---|---|
| Home receipts | Confirm `checkmark`, green, `medium`, row leaves | Delete `trash`, red, opens the confirm tray |
| Shopping list | Check off `checkmark`, green, `soft` | Delete `trash`, red, Undo toast |
| Products | Add to list `cart.badge.plus`, green, `light` | Delete `trash`, red, confirm tray |
| Upload queue (waiting) | none | Remove `xmark`, red, Undo toast |
| Receipt lines | none | Delete `trash`, red |
| Shops, Categories | none | Delete `trash`, red, confirm tray (category with products opens the move tray) |

Edit and Rename leave the swipe: tapping the row opens it (receipt, product, shop and category rows already do). That is the trade for the Outlook feel.

## How to use it (swift agent)
```swift
SwipeActionRow(
    leading: SwipeAction(icon: "checkmark", title: "Confirm", tint: Accent.green, removesRow: true, haptic: .medium) { confirm(receipt) },
    trailing: SwipeAction(icon: "trash", title: "Delete", tint: Accent.red) { askDelete(receipt) }
) {
    ReceiptRow(...)           // the row content, full width, with its own horizontal padding
}
.shopListRow()               // still a List row: insets zero, separator hidden, clear background
```
Remove `.swipeActions`, `SwipeButton` and the touch-watching `SwipeHapticHost`; the component plays its own haptics. The row content needs its own `Palette.bg` backdrop only if the list background is not white (the component already paints `Palette.bg` behind the content).
