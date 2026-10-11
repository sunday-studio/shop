# Navigation icons

Decision: **duotone, Phosphor Icons**, as template SVG assets. Frame: `docs/design-frames/nav-icons/duotone-vs-symbols.jpg` (duotone vs SF Symbols palette, light and dark, selected and unselected, in the pill tab bar).

## Options checked
| Option | Licence | Verdict |
|---|---|---|
| **Phosphor Icons, duotone weight** (`@phosphor-icons/core` 2.1.1) | MIT, copy in `docs/licenses/phosphor-icons-MIT.txt`. Free for commercial use, no attribution screen required (keep the licence file) | **Chosen.** Real two-layer duotone: outline plus a 20 % fill of the same colour |
| SF Symbols, palette rendering | Apple's SF Symbols licence: usable in apps on Apple platforms only, not editable or redistributable | Fallback. Most symbols have one layer, so "palette" does not give a real duotone; the selected `.fill` variants read heavy next to the rest of the app |
| Solar (CC BY 4.0), Iconsax | Attribution required / unclear | Skipped |

## How it works
- Assets: `Assets.xcassets/Nav/nav-{home,analytics,products,list}.imageset`, SVG, "Preserve vector data", template rendering. One `foregroundStyle` tints both layers, so light/dark and selected/unselected need no extra assets.
- Mapping: house = Home, chart-bar = Analytics, package = Products, list-checks = List.
- Code: `Design/NavIcons.swift` (`NavIcon`, `NavIconView`, `navIconStyle` environment, default `.duotone`) and `PillTabBar` (maps the existing `Icon.home/.analytics/.products/.list` to the duotone twin, so `RootView` needs no change).
- States: selected = `Palette.ink` (outline solid, fill 20 % ink) with the existing 120 ms pop; unselected = `Palette.ink3`. Size 24 pt. Badge position unchanged.
- Switching back: `.environment(\.navIconStyle, .symbol)`.
- Catalog: section "Navigation icons".

## Adding more duotone icons later
Download the `-duotone.svg` from the same package, add an imageset with template rendering, add a case to `NavIcon` (or a sibling enum). Keep one weight per context: duotone for navigation, SF Symbols for in-row UI icons, so the tab bar is the only place with a two-layer look.
