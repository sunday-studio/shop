# Home header logo: three options

Frame: `docs/design-frames/logo/home-logo-options.jpg` (each option on a Home-header mock, light and dark). Catalog section "Home logo options"; tap a mock to replay the tilt. Component: `Design/Components/Logo.swift` (`ShopLogo(style:height:wiggle:)`).

| | Idea | Why / why not |
|---|---|---|
| **A Badge + wordmark (recommended)** | The app icon in miniature (blue squircle, tilted receipt, green dot, yellow total) next to a bold "Shop" in GT America | Reads as the app icon at once, works light and dark unchanged, the blue is the app's primary accent, the only one that is clearly "the app's logo" and not a decoration |
| B Torn-receipt wordmark | Lowercase ink "shop" with a green dot and a yellow torn edge under it, tilted -3° | Lightest and most playful, but the sawtooth gets busy under 24 pt and it has no link to the icon's blue |
| C Total-bar pill | The icon's yellow total bar as a tilted pill with "shop" and barcode ticks, a green dot on its corner | Most fun; but it sits next to the yellow avatar square and the two yellows compete |

Motion (all three): a single 0.5 s tilt that settles with a spring (-7° to 0), played on appear and whenever `wiggle` changes. No loop. Reduce Motion: none. Cost: one rotation transform.

Where it goes (for the swift agent, Home header): the header row is `Avatar … Search`. Put `ShopLogo(style: .badge, height: 30, wiggle: refreshCount)` centred between them (`Spacer()` either side), `refreshCount` an Int the Home screen bumps when a pull to refresh finishes and on first launch after onboarding. It is not tappable and has the accessibility label "Shop".

Pick A, B or C; the other two stay in the Catalog until you decide, then I delete them.
