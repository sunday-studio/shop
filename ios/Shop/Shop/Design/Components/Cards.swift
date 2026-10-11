import SwiftUI

/// Small figure card (P.1): caption, big value with a dim unit, footnote.
/// `.highlight` uses a saturated fill with dark ink (yellow "Cheapest at" card).
struct StatCard: View {
    enum Style {
        case plain
        case highlight(Color)
    }

    let caption: String
    let value: String
    var unit: String? = nil
    var footnote: String? = nil
    var style: Style = .plain

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(caption)
                .textStyle(TypeStyle.captionMedium.sized(12.5))
                .foregroundStyle(secondary)
                .lineLimit(1)
            HStack(alignment: .firstTextBaseline, spacing: 1) {
                Text(value).textStyle(TypeStyle.emptyTitle.sized(26).withFace(.regular))
                    .tracking(-0.04 * 26)
                if let unit {
                    Text(unit).textStyle(TypeStyle.input.sized(15)).foregroundStyle(secondary)
                }
            }
            .foregroundStyle(primary)
            .lineLimit(1)
            .minimumScaleFactor(0.7)
            if let footnote {
                Text(footnote).textStyle(.caption).foregroundStyle(secondary).lineLimit(1)
            }
        }
        .padding(.vertical, Space.lx)
        .padding(.horizontal, Space.xl)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(background, in: RoundedRectangle.continuous(22))
        .accessibilityElement(children: .combine)
    }

    private var primary: Color {
        switch style {
        case .plain: Palette.ink
        case .highlight: Accent.onLight
        }
    }

    private var secondary: Color {
        switch style {
        case .plain: Palette.ink2
        case .highlight: Accent.onLight.opacity(0.72)
        }
    }

    private var background: Color {
        switch style {
        case .plain: Palette.sunk
        case .highlight(let c): c
        }
    }
}

/// Big product art tile (P.1 header): category tint with the photo or a drawn pack.
struct ProductHero: View {
    let name: String
    var category: String? = nil
    /// The category's stored colour token; wins over the name.
    var categoryToken: String? = nil
    var imageURL: URL? = nil
    var size: CGFloat = 84

    var body: some View {
        let swatch = CategoryColor.swatch(token: categoryToken, name: category)
        ZStack(alignment: .bottom) {
            RoundedRectangle.continuous(24).fill(swatch.tile)
            CachedImage(url: imageURL) { image in
                image.resizable().scaledToFill()
            } placeholder: {
                UnevenRoundedRectangle(topLeadingRadius: 10, bottomLeadingRadius: 4, bottomTrailingRadius: 4, topTrailingRadius: 10, style: .continuous)
                    .fill(Accent.white)
                    .frame(width: size * 0.48, height: size * 0.79)
                    .overlay(alignment: .top) {
                        RoundedRectangle.continuous(4)
                            .fill(swatch.color)
                            .frame(height: size * 0.24)
                            .padding(.horizontal, 4)
                            .padding(.top, size * 0.29)
                    }
                    .shadow(color: swatch.color.opacity(0.25), radius: 7, y: 6)
            }
        }
        .frame(width: size, height: size)
        .clipShape(RoundedRectangle.continuous(24))
        .accessibilityHidden(true)
    }
}

#Preview("Cards") {
    VStack(spacing: 16) {
        HStack(spacing: 8) {
            StatCard(caption: "Cheapest at Lidl", value: "€0,85", unit: "/l", footnote: "Seen Mon 5 Oct", style: .highlight(Accent.yellow))
            StatCard(caption: "You usually pay", value: "€0,89", unit: "/l", footnote: "Mostly at Albert Heijn")
        }
        ProductHero(name: "Halfvolle melk", category: "Dairy")
    }
    .padding(16)
    .background(Palette.bg)
}
