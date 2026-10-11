# Shop iOS — Xcode setup

The Xcode project is committed: open `ios/Shop/Shop.xcodeproj`. Requires **Xcode 27** (Swift 6), deployment target **iOS 17.0**, iPhone only.

## 1. Open and sign

1. Open `ios/Shop/Shop.xcodeproj`.
2. Target → Signing & Capabilities → pick your team and, if needed, change the bundle identifier (`com.sundaystudi.Shop` by default).
3. The app icon and accent colour (#0A84FF) live in `ios/Shop/Shop/Assets.xcassets`; the fonts are in `ios/Shop/Shop/Resources/Fonts`.
4. **Background Modes** is *not* needed (background `URLSession` uploads work without it).

## 2. Info tab keys

| Key | Value |
|---|---|
| `UIAppFonts` (Fonts provided by application) | `GTAmerica-Regular.otf`, `GTAmerica-Medium.otf`, `GTAmerica-Bold.otf`, `GTAmericaMono-Regular.otf`, `GTAmericaMono-Medium.otf` |
| `NSCameraUsageDescription` | Scan receipts and product barcodes. |
| `NSPhotoLibraryUsageDescription` | Pick a photo of a receipt or product. |
| `NSLocalNetworkUsageDescription` | Connect to your Shop server on this network. |
| `NSAppTransportSecurity` → `NSAllowsLocalNetworking` | YES (LAN, `.local`, IP addresses over http). Already in the project: `ios/Shop/Config/ATS.plist` is merged into the generated Info.plist (`INFOPLIST_FILE`, with `GENERATE_INFOPLIST_FILE = YES`), because a dictionary can't be set with `INFOPLIST_KEY_` build settings. |
| `NSAppTransportSecurity` → `NSExceptionDomains` → `ts.net` → `NSIncludesSubdomains` YES, `NSExceptionAllowsInsecureHTTPLoads` YES | Tailscale over plain http (not needed with `tailscale serve` https). Also in `Config/ATS.plist`. |
| `UILaunchScreen` | empty dictionary (white/black launch) |
| `UIUserInterfaceStyle` | leave unset (the app has its own Appearance setting) |

The keys below are already set in the committed project; this table is the reference. If the fonts aren't found at launch, the debug console prints `⚠️ Font … is not registered`: check the file names in `UIAppFonts` and that the `.otf` files are in the target.

## 3. Run

- **Mock mode** (no server needed): Product → Scheme → Edit Scheme → Run → Arguments → add `-mock`. The whole app runs against an in-memory server with sample data (password `wrong` shows the wrong-password state).
- **Real server**: run without `-mock`, enter the server URL (LAN or Tailscale) and the pairing password (`make setup` printed it; `shop password generate` makes a new one).
- **Previews**: every screen and component has light and dark `#Preview`s. The design catalog is also in the app: Settings → About Shop.

## 4. On-device checklists

Per milestone, in [IOS-PLAN.md](IOS-PLAN.md) §4.
