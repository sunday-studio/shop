import SwiftUI

// MARK: - Shop mark

/// 22pt rounded square (28 in headers, never above 32). White fill with a hairline ring,
/// monogram in the brand colour; a cached logo replaces it once loaded.
struct ShopMark: View {
    let name: String
    var size: CGFloat = Size.shopMark

    private var mark: ShopBrand.Mark { ShopBrand.mark(for: name) }
    private var side: CGFloat { min(size, 32) }

    var body: some View {
        let shape = RoundedRectangle.continuous(side * 6 / 22)
        ZStack {
            shape.fill(Color.dynamic(light: 0xFFFFFF, dark: 0xF4F4F1))
            CachedImage(url: ShopLogos.url(for: name), key: ShopLogos.cacheKey(for: name)) { image in
                image.resizable().scaledToFit().padding(side * 0.12)
            } placeholder: {
                Text(mark.monogram)
                    .font(.custom(ShopFont.medium, size: side * 9.5 / 22))
                    .tracking(-0.2)
                    .foregroundStyle(mark.color)
                    .minimumScaleFactor(0.6)
                    .lineLimit(1)
            }
        }
        .frame(width: side, height: side)
        .clipShape(shape)
        .overlay(shape.strokeBorder(Palette.line, lineWidth: 1))
        .accessibilityHidden(true)
    }
}

// MARK: - Category dot

struct CategoryDot: View {
    var color: Color
    var size: CGFloat = 8

    var body: some View {
        RoundedRectangle.continuous(size >= 12 ? 4 : Radius.xs)
            .fill(color)
            .frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}

// MARK: - Product tile

/// 36pt tile with the category tint and three-letter initials (P list), or the product photo.
struct ProductTile: View {
    let name: String
    var category: String? = nil
    var imageURL: URL? = nil
    var size: CGFloat = Size.tile

    var body: some View {
        let shape = RoundedRectangle.continuous(size * 11 / 36)
        ZStack {
            shape.fill(CategoryColor.swatch(for: category).tile)
            CachedImage(url: imageURL) { image in
                image.resizable().scaledToFill()
            } placeholder: {
                Text(Self.initials(name))
                    .font(.custom(ShopFont.bold, size: size * 9.5 / 36))
                    .foregroundStyle(Color.dynamic(light: 0x000000, dark: 0xFFFFFF, lightAlpha: 0.5, darkAlpha: 0.6))
            }
        }
        .frame(width: size, height: size)
        .clipShape(shape)
        .accessibilityHidden(true)
    }

    /// "Halfvolle melk" → "HAL", "Eieren" → "EIE", "Ei" → "EI".
    static func initials(_ name: String) -> String {
        let letters = name.uppercased().filter(\.isLetter)
        return String(letters.prefix(3))
    }
}

// MARK: - Count badge

/// Amber count badge (tab bar, attention card). Medium weight per the rules.
struct CountBadge: View {
    let count: Int
    var size: CGFloat = 16
    var color: Color = Accent.amber
    var ring: Color? = nil

    var body: some View {
        Text(count > 99 ? "99+" : "\(count)")
            .font(.custom(ShopFont.medium, size: size * 0.625))
            .monospacedDigit()
            .foregroundStyle(Accent.white)
            .padding(.horizontal, count > 9 ? 4 : 0)
            .frame(minWidth: size, minHeight: size)
            .background(color, in: Capsule())
            .overlay {
                if let ring { Capsule().strokeBorder(ring, lineWidth: 2) }
            }
    }
}

/// Round glyph badge ("!" on amber banners, "✓" on success).
struct GlyphBadge: View {
    enum Glyph { case alert, check, info }
    var glyph: Glyph = .alert
    var color: Color = Accent.amber
    var size: CGFloat = 20

    var body: some View {
        Group {
            switch glyph {
            case .alert:
                Text("!").font(.custom(ShopFont.bold, size: size * 0.6))
            case .check:
                Image(icon: .check).font(.system(size: size * 0.55, weight: .heavy))
            case .info:
                Text("i").font(.custom(ShopFont.bold, size: size * 0.6))
            }
        }
        .foregroundStyle(Accent.white)
        .frame(width: size, height: size)
        .background(color, in: Circle())
        .accessibilityHidden(true)
    }
}

// MARK: - Status dot

struct StatusDot: View {
    var color: Color
    var size: CGFloat = 7

    var body: some View {
        Circle().fill(color).frame(width: size, height: size).accessibilityHidden(true)
    }
}

// MARK: - Icon tile (settings rows, feature cards)

/// Coloured rounded square holding a white glyph (S rows: 32pt r10; cards: 44pt r14 translucent).
struct IconTile: View {
    let icon: Icon
    var color: Color
    var size: CGFloat = 32
    var glyphColor: Color = Accent.white

    var body: some View {
        IconView(icon, size: size * 0.5, weight: .semibold)
            .foregroundStyle(glyphColor)
            .frame(width: size, height: size)
            .background(color, in: RoundedRectangle.continuous(size * 10 / 32))
    }
}

#Preview("Marks") {
    VStack(alignment: .leading, spacing: 12) {
        HStack {
            ForEach(["Albert Heijn", "Lidl", "Jumbo", "Dirk", "Ekoplaza", "Marqt"], id: \.self) { ShopMark(name: $0) }
            ShopMark(name: "Albert Heijn", size: 28)
        }
        HStack {
            ProductTile(name: "Halfvolle melk", category: "Dairy")
            ProductTile(name: "Bananen", category: "Produce")
            ProductTile(name: "Kipfilet", category: "Meat & fish")
            CountBadge(count: 2, ring: Palette.tray)
            CountBadge(count: 2, size: 22)
            GlyphBadge()
            GlyphBadge(glyph: .check, color: Accent.green)
        }
        HStack {
            CategoryDot(color: Accent.blue)
            CategoryDot(color: Accent.green, size: 12)
            StatusDot(color: Accent.green)
            IconTile(icon: .upload, color: Accent.blue)
            IconTile(icon: .server, color: .white.opacity(0.2), size: 44)
        }
    }
    .padding()
    .background(Palette.bg)
}
