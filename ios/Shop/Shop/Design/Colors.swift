import SwiftUI
import UIKit

// Colour tokens. Values come from docs/DESIGN-SYSTEM.md §1 (measured from the wireframes).
// Every colour in feature code must come from here. Light/dark resolve through UIColor
// dynamic providers, so `.preferredColorScheme` and previews both work.

extension UIColor {
    nonisolated convenience init(hex: UInt32, alpha: CGFloat = 1) {
        self.init(
            red: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255,
            alpha: alpha
        )
    }
}

extension Color {
    nonisolated init(hex: UInt32, alpha: Double = 1) {
        self.init(uiColor: UIColor(hex: hex, alpha: CGFloat(alpha)))
    }

    /// A colour that resolves per colour scheme.
    nonisolated static func dynamic(light: UInt32, dark: UInt32, lightAlpha: CGFloat = 1, darkAlpha: CGFloat = 1) -> Color {
        Color(uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(hex: dark, alpha: darkAlpha)
                : UIColor(hex: light, alpha: lightAlpha)
        })
    }
}

/// Surface, ink and semantic colours.
enum Palette {
    // Surfaces
    static let bg = Color.dynamic(light: 0xFFFFFF, dark: 0x0C0C0B)
    static let sunk = Color.dynamic(light: 0xF4F4F3, dark: 0x1C1C1A)
    /// Skeleton bars on the page background; on a `sunk` card they use `bg` (`skeletonOnCard`).
    static let skeleton = Color.dynamic(light: 0xEAEAE7, dark: 0x262624)
    static let tray = Color.dynamic(light: 0xFFFFFF, dark: 0x1C1C1A)
    /// Fields and option rows inside a tray (one step lighter than the tray in dark).
    static let trayField = Color.dynamic(light: 0xF4F4F3, dark: 0x262624)
    static let line = Color.dynamic(light: 0xF0F0EE, dark: 0x1F1F1D)

    // Ink
    static let ink = Color.dynamic(light: 0x1D1D1B, dark: 0xF4F4F1)
    static let ink2 = Color.dynamic(light: 0x73736F, dark: 0x8F8F8A)
    static let ink3 = Color.dynamic(light: 0xB4B4AF, dark: 0x5C5C58)

    // Primary button (inverts per theme)
    static let btn = Color.dynamic(light: 0x1D1D1B, dark: 0xF4F4F1)
    static let btnInk = Color.dynamic(light: 0xFFFFFF, dark: 0x0C0C0B)

    static let scrim = Color.dynamic(light: 0x1D1D1B, dark: 0x000000, lightAlpha: 0.34, darkAlpha: 0.62)
    static let seg = Color.dynamic(light: 0xFFFFFF, dark: 0x3A3A37)
    /// Paper colour used for receipt thumbnails (same in both themes).
    static let paper = Color(hex: 0xF7F6F1)
    static let paperLine = Color(hex: 0xDADAD5)

    // Semantic text / tint pairs
    static let amberText = Color.dynamic(light: 0xB25E00, dark: 0xFFB340)
    static let amberTint = Color.dynamic(light: 0xFFF0D9, dark: 0x33230A)
    static let greenText = Color.dynamic(light: 0x0B8A47, dark: 0x3DDC84)
    static let greenTint = Color.dynamic(light: 0xE2F7EA, dark: 0x0F2A1B)
    static let redText = Color.dynamic(light: 0xD1281C, dark: 0xFF6B61)
    static let redTint = Color.dynamic(light: 0xFFE8E6, dark: 0x3A1411)
    static let blueText = Color.dynamic(light: 0x0071E3, dark: 0x4DA3FF)
    static let blueTint = Color.dynamic(light: 0xE5F1FF, dark: 0x0E2236)

    /// Neutral grey used for "Other" / "everything else" swatches.
    static let neutral = Color.dynamic(light: 0xD6D6D2, dark: 0x3A3A37)
}

/// Saturated accents, identical in both themes.
enum Accent {
    static let blue = Color(hex: 0x0A84FF)
    static let green = Color(hex: 0x18B663)
    static let red = Color(hex: 0xFF3B5C)
    static let yellow = Color(hex: 0xFFCC1A)
    static let orange = Color(hex: 0xFF8A00)
    static let pink = Color(hex: 0xFF4FA3)
    static let purple = Color(hex: 0x7C5CFF)
    static let cyan = Color(hex: 0x14C3E0)
    static let teal = Color(hex: 0x00B8A9)
    /// "Needs review" everywhere.
    static let amber = Color(hex: 0xFF9500)
    static let avatar = Color(hex: 0xFFD84D)
    static let white = Color(hex: 0xFFFFFF)
    static let onLight = Color(hex: 0x1D1D1B)

    /// Rotation for anything without a fixed colour (hashed by name).
    static let rotation: [Color] = [blue, green, red, yellow, orange, pink, purple, cyan, teal]

    static func hashed(_ key: String) -> Color {
        rotation[StableHash.index(of: key, count: rotation.count)]
    }
}

/// Category swatches. Matched by name (Dutch seed names and common English ones).
enum CategoryColor {
    struct Swatch {
        let color: Color
        /// Light tile behind product initials (P list).
        let tile: Color
    }

    /// The swatch for a stored category token (`CategoryTone.tokens`: blue green red orange yellow
    /// cyan purple teal pink grey). Unknown or nil tokens return nil so callers can fall back.
    static func swatch(token: String?) -> Swatch? {
        switch token {
        case "blue": Swatch(color: Accent.blue, tile: .dynamic(light: 0xCFE6FF, dark: 0x0E2236))
        case "green": Swatch(color: Accent.green, tile: .dynamic(light: 0xC9F2DA, dark: 0x0F2A1B))
        case "red": Swatch(color: Accent.red, tile: .dynamic(light: 0xFFD3DB, dark: 0x3A1418))
        case "orange": Swatch(color: Accent.orange, tile: .dynamic(light: 0xFFE0BF, dark: 0x36230D))
        case "yellow": Swatch(color: Accent.yellow, tile: .dynamic(light: 0xFFF0B3, dark: 0x332A0A))
        case "cyan": Swatch(color: Accent.cyan, tile: .dynamic(light: 0xC8F2F9, dark: 0x0A2A30))
        case "purple": Swatch(color: Accent.purple, tile: .dynamic(light: 0xE3DCFF, dark: 0x1F1838))
        case "teal": Swatch(color: Accent.teal, tile: .dynamic(light: 0xC6F0EC, dark: 0x0A2A27))
        case "pink": Swatch(color: Accent.pink, tile: .dynamic(light: 0xFFD6EA, dark: 0x3A1427))
        case "grey": other
        default: nil
        }
    }

    /// The token the old name mapping would have picked, for cached categories that have no stored
    /// token yet. Nil when the name matches nothing (those fall back to a hashed accent).
    static func token(forName name: String) -> String? {
        let key = name.lowercased()
        func has(_ words: String...) -> Bool { words.contains { key.contains($0) } }
        if has("zuivel", "dairy") { return "blue" }
        if has("groente", "fruit", "produce", "veg") { return "green" }
        if has("vlees", "vis", "meat", "fish") { return "red" }
        if has("brood", "bread", "bakery") { return "orange" }
        if has("voorraad", "pantry", "stock") { return "yellow" }
        if has("diepvries", "frozen") { return "cyan" }
        if has("dranken", "drink", "beverage") { return "purple" }
        if has("huishoud", "household") { return "teal" }
        if has("verzorging", "care", "hygiene") { return "pink" }
        if has("overig", "other", "else") { return "grey" }
        return nil
    }

    /// Stored token first (it survives renames); the name mapping only for old cache data.
    static func swatch(token: String?, name: String?) -> Swatch {
        if let found = swatch(token: token) { return found }
        return swatch(for: name)
    }

    static func swatch(for category: Category?) -> Swatch {
        swatch(token: category?.color, name: category?.name)
    }

    /// Name-only mapping: fallback for cache data without a token, and for labels that are not
    /// categories in the catalog (analytics bucket labels).
    static func swatch(for name: String?) -> Swatch {
        guard let name, !name.isEmpty else { return other }
        if let token = token(forName: name), let found = swatch(token: token) { return found }
        let c = Accent.hashed(name.lowercased())
        return Swatch(color: c, tile: c.opacity(0.18))
    }

    static let other = Swatch(color: Palette.neutral, tile: .dynamic(light: 0xE6E6E2, dark: 0x262624))
}

/// Shop brand marks (monogram fallback until a logo is cached).
enum ShopBrand {
    struct Mark {
        let monogram: String
        let color: Color
    }

    static func mark(for name: String) -> Mark {
        let key = name.lowercased()
        if key.contains("albert heijn") || key == "ah" || key.hasPrefix("ah ") { return Mark(monogram: "AH", color: Color(hex: 0x0A84FF)) }
        if key.contains("lidl") { return Mark(monogram: "Li", color: Color(hex: 0x0050AA)) }
        if key.contains("jumbo") { return Mark(monogram: "J", color: Color(hex: 0xFFB000)) }
        if key.contains("dirk") { return Mark(monogram: "D", color: Color(hex: 0xE2001A)) }
        if key.contains("ekoplaza") { return Mark(monogram: "E", color: Color(hex: 0x2E7D32)) }
        let letter = name.trimmingCharacters(in: .whitespaces).first.map { String($0).uppercased() } ?? "?"
        return Mark(monogram: letter, color: Accent.hashed(key))
    }
}

/// Deterministic hash (String.hashValue is randomised per launch).
nonisolated enum StableHash {
    static func index(of key: String, count: Int) -> Int {
        guard count > 0 else { return 0 }
        var h: UInt64 = 1469598103934665603
        for b in key.utf8 {
            h ^= UInt64(b)
            h = h &* 1099511628211
        }
        return Int(h % UInt64(count))
    }
}
