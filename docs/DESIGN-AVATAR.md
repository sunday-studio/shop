# Home header: user button and logo

**Decided:** person glyph (option C) on the left, the blue app-icon badge + "Shop" logo in the middle, search on the right. Frame: `docs/design-frames/avatar/home-header.jpg`. The other avatar ideas (sticker, receipt face, tinted initials, plain initials, yellow square) are dropped.

- User button: `Avatar(badge:action:)` (`Design/Components/Buttons.swift`): 36 pt grey circle (same as the search button) with the duotone person icon (Phosphor, MIT, same set as the tab icons). Optional `badge:` colour puts a 10 pt dot on its corner for "something in Settings needs attention" (uploads waiting, offline, old backup); VoiceOver then says "Settings, needs attention".
- Logo: `ShopLogo(height: 30)`, not tappable; plays one short tilt on appear (none with Reduce Motion).
- `HomeHeader` (Design/Components/Headers.swift) lays it out. The sync pill used to sit in the middle; now the **logo is the resting state** and the pill takes the middle only while a status shows. Pass `showsStatus: app.syncStatus != nil` (the `center` closure still holds the `SyncPill`); without it the pill stays hidden.
- Optional: pass `badge: Accent.amber` to `HomeHeader` when uploads are waiting or the backup is old.
