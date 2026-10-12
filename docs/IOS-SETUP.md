# Shop iOS — Xcode setup

The Xcode project is committed: open `ios/Shop/Shop.xcodeproj`. Requires **Xcode 27** (Swift 6), deployment target **iOS 26.5** (as set in the committed project), iPhone only.

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

## 5. Automated tests

Two test targets in `ios/Shop/Shop.xcodeproj`, both on the scheme **Shop**:

- **ShopTests** (unit, about a second): money and date formatting, product size and barcode parsing, the shopping list's offline queue and temporary-id remapping, the `since_rev` sync, plan and list-change decoding, and the import pipeline's pure parts (straighten, crop, rotate, stitch).
- **ShopUITests** (about 2.5 minutes): end-to-end flows on the app's built-in mock server. Home, tabs, opening and confirming a receipt, the Add tray, the whole import flow (it uses generated pictures through `-mock-import-demo`, so no photo library is needed), shopping list check-off, groups and By store, the products review queue with Undo, Settings. Each test launches with `-mock -mock-fresh`, which wipes saved state first.

Run everything headless (no window, no server, no camera):

```bash
cd ios/Shop
xcodebuild test -project Shop.xcodeproj -scheme Shop \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro'
```

Only the unit tests: add `-only-testing:ShopTests`. Only the UI tests: `-only-testing:ShopUITests`.

If other projects run UI tests on the same Mac, give Shop its own simulator so they don't share one (a shared one makes the UI tests flaky): `xcrun simctl create "Shop Tests" "iPhone 17 Pro"`, then use `-destination 'platform=iOS Simulator,id=<the id it prints>'`.

When a UI test fails it prints what was on screen. Launch aids the tests use: `-mock`, `-mock-fresh` (forget saved state), `-mock-import-demo` (skip the photo picker), `-mock-large` (1,500 products), `-bench` (log timings).
