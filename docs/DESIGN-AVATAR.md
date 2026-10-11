# Home user mark (the avatar)

Decision so far: the blue **icon badge + "Shop" wordmark** is kept as the app logo (option A, `ShopLogo`); options B and C are removed. The thing to make fun is the **user icon**, top left of Home (and the same component in Settings). Frames: `docs/design-frames/avatar/avatar-options-1.jpg`, `-2.jpg` (Home-header mock, light and dark, seven sample names each).

The colour comes from the user's initial (eight accents), so it is stable and every household member gets a different one. Tapping it plays one short tilt (spring, no loop; none with Reduce Motion) before opening Settings.

| | Look | Notes |
|---|---|---|
| Today (`plain`) | Yellow square, plain initial | Everyone is yellow, no personality |
| 1 Sticker (`sticker`) | Tilted -6°, bold initial, peeled corner | Simple, keeps the initial; fun but quiet |
| **2 Receipt buddy (`buddy`, recommended)** | The icon's tilted receipt with a face (two dot eyes, smile, yellow total bar), on the user's colour; no initial | The most characterful, directly the app icon's character, still tiny and cheap. Cost: the initial is gone, so two people with the same colour look identical (colour is by initial, so same initial = same colour only) |
| 3 Receipt initial (`receipt`) | Tilted mini receipt carrying the initial on the user's colour | Keeps the initial and the receipt link; busiest at 36 pt |

Component: `Avatar(initial:style:size:action:)` in `Design/Components/Buttons.swift`; Catalog section "User avatar options". The default style is still `plain` so nothing changes until you pick. Once picked, I change the default and the swift agent needs no change (the Home and Settings call sites already use `Avatar(initial:)`).
