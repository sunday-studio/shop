# Shop iOS — Xcode setup

The repo holds only Swift sources (`ios/Shop/`) and the fonts. Create the Xcode project once, then every later change is just files.

Requires **Xcode 27** (Swift 6), deployment target **iOS 17.0**.

## 1. Create the project

1. Xcode → File → New → Project → iOS → **App**.
2. Product Name **Shop**, Organization Identifier **nl.casprine** (bundle id `nl.casprine.shop`), Interface **SwiftUI**, Language **Swift**, Storage **None**, no tests.
3. Save it in `ios/` (so you get `ios/Shop.xcodeproj` next to `ios/Shop/`). Xcode's template adds `ShopApp.swift`, `ContentView.swift` and `Assets.xcassets`: delete all three (move to trash); keep the target.
4. Drag the folders `ios/Shop/App`, `ios/Shop/Core`, `ios/Shop/Design`, `ios/Shop/Features` and `ios/Shop/Resources` into the project navigator → "Create folders" (Xcode 16+ folder references pick up new files automatically) → target **Shop** ticked.
5. Target → General → Minimum Deployments: **iOS 17.0**. iPhone only.
6. Target → Build Settings: Swift Language Version **6**. Leave "Default Actor Isolation" at the template default (MainActor); the code compiles either way.
7. `ios/Shop/Resources/Assets.xcassets` (added in step 4) holds the **AppIcon** (Any / Dark / Tinted, 1024 px, rendered from `design/logo/icon-final-*.svg`) and **AccentColor** (#0A84FF). Target → General → App Icons: `AppIcon`; Build Settings → Global Accent Color Name: `AccentColor` (both are the template defaults).
8. Signing & Capabilities → your team. **Background Modes** is *not* needed (background `URLSession` uploads work without it).

## 2. Info tab keys

| Key | Value |
|---|---|
| `UIAppFonts` (Fonts provided by application) | `GTAmerica-Regular.otf`, `GTAmerica-Medium.otf`, `GTAmerica-Bold.otf`, `GTAmericaMono-Regular.otf`, `GTAmericaMono-Medium.otf` |
| `NSCameraUsageDescription` | Scan receipts and product barcodes. |
| `NSPhotoLibraryUsageDescription` | Pick a photo of a receipt or product. |
| `NSLocalNetworkUsageDescription` | Connect to your Shop server on this network. |
| `NSAppTransportSecurity` → `NSAllowsLocalNetworking` | YES (LAN, `.local`, IP addresses over http) |
| `NSAppTransportSecurity` → `NSExceptionDomains` → `ts.net` → `NSIncludesSubdomains` YES, `NSExceptionAllowsInsecureHTTPLoads` YES | Tailscale over plain http (not needed with `tailscale serve` https) |
| `UILaunchScreen` | empty dictionary (white/black launch) |
| `UIUserInterfaceStyle` | leave unset (the app has its own Appearance setting) |

If the fonts aren't found at launch, the debug console prints `⚠️ Font … is not registered`: check the file names in `UIAppFonts` and that the `.otf` files are in the target.

## 3. Run

- **Mock mode** (no server needed): Product → Scheme → Edit Scheme → Run → Arguments → add `-mock`. The whole app runs against an in-memory server with sample data (password `wrong` shows the wrong-password state).
- **Real server**: run without `-mock`, enter the server URL (LAN or Tailscale) and the pairing password (`make setup` printed it; `shop password generate` makes a new one).
- **Previews**: every screen and component has light and dark `#Preview`s. The design catalog is also in the app: Settings → About Shop.

## 4. On-device checklists

Per milestone, in [IOS-PLAN.md](IOS-PLAN.md) §4.
