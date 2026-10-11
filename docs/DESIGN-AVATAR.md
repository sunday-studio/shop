# Home header: user button and logo

Decided: the **logo is the centre of the header** (blue app-icon badge + "Shop", `ShopLogo`). The earlier playful avatar ideas (sticker, receipt with a face, receipt initial) and the yellow square are rejected.

Direction now: stop decorating the user button. It is a button that opens Settings, so it should look like the other header buttons (36 pt circle, same fill as the search button) and let the logo be the fun part. References (Mobbin): [Perplexity](https://mobbin.com/screens/8b77ceb4-4427-4def-91c1-7126b89cd5e7) (plain initial circle top left), [Mindtrip](https://mobbin.com/screens/a3e98124-f000-4a3b-a57f-b338ee1344ac) (tinted initials circle), [Once](https://mobbin.com/screens/aa0339b9-639e-488b-ac56-92f96cc267df) (plain person glyph).

Frame: `docs/design-frames/avatar/profile-options-2.jpg` (B and C shown; A is the neutral version at the top), each on a Home-header mock, light and dark, seven sample names.

| | Look | Notes |
|---|---|---|
| Today | Yellow rounded square, initial | Everyone is the same yellow; doesn't match the circle buttons |
| A Initials, neutral | Same grey circle as the search button, initial in ink | Quietest; the header reads as three matching controls |
| **B Initials, tinted (recommended)** | Circle tinted with the user's colour (six tones from the initial), initial in that colour | Identity without decoration; light and dark tints are defined per tone. A household with two people gets two colours |
| C Person glyph | Grey circle, duotone person icon (Phosphor, MIT, same set as the tab icons) | Says "account", no identity; same for every user |

All variants support a **corner dot** (`badge:`): amber when something in Settings needs attention (uploads waiting, offline, backup old), nothing otherwise. The VoiceOver label becomes "Settings, needs attention". Shown on B in the frame.

Component: `Avatar(initial:style:badge:action:)` (`Design/Components/Buttons.swift`), Catalog section "User avatar options". `HomeHeader` (Design) already uses it and the default is still the old square, so nothing changes until you pick; then I switch the default and wire `badge` from the upload queue state (a small change for the swift agent in the Home screen).
