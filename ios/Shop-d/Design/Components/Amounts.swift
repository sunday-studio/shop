import SwiftUI

// MARK: - Hero amount

/// "€412,38" with the symbol and cents in ink3 at 2/3 size. Digits roll when the value changes.
struct HeroAmount: View {
    let cents: Int64
    /// Size of the euros digits (60 on Home, 54 Analytics, 48 receipt).
    var size: CGFloat = 60
    var color: Color = Palette.ink
    var minorColor: Color = Palette.ink3

    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        let parts = Money.heroParts(cents)
        let major = TypeStyle.hero.sized(size)
        let minor = TypeStyle.heroMinor.sized((size * 2 / 3).rounded())
        HStack(alignment: .firstTextBaseline, spacing: 2) {
            Text(parts.symbol)
                .font(minor.font).tracking(minor.tracking)
                .foregroundStyle(minorColor)
            Text(parts.whole)
                .font(major.font).tracking(major.tracking)
                .foregroundStyle(color)
                .contentTransition(reduce ? .opacity : .numericText(value: Double(cents)))
            Text(parts.fraction)
                .font(minor.font).tracking(minor.tracking)
                .foregroundStyle(minorColor)
                .contentTransition(reduce ? .opacity : .numericText(value: Double(cents)))
        }
        .monospacedDigit()
        .lineLimit(1)
        .minimumScaleFactor(0.5)
        .animation(reduce ? Motion.reducedFade : Motion.numberRoll, value: cents)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Money.format(cents))
    }
}

// MARK: - Delta chip

/// "↓ €42,72" on a green (less) or red (more) tint, followed by an explanation.
struct DeltaChip: View {
    /// Current minus previous. Negative = spent less.
    let deltaCents: Int64
    var caption: String? = nil

    var body: some View {
        let less = deltaCents <= 0
        HStack(spacing: Space.m) {
            HStack(spacing: 3) {
                IconView(less ? .arrowDown : .arrowUp, size: 12, weight: .heavy)
                Text(Money.format(Int64(deltaCents.magnitude)))
            }
            .textStyle(TypeStyle.label.sized(13.5))
            .foregroundStyle(less ? Palette.greenText : Palette.redText)
            .padding(.leading, 6)
            .padding(.trailing, 8)
            .padding(.vertical, 3)
            .background(less ? Palette.greenTint : Palette.redTint, in: RoundedRectangle.continuous(Radius.s))
            if let caption {
                Text(caption)
                    .textStyle(TypeStyle.body.sized(13.5))
                    .foregroundStyle(Palette.ink2)
                    .lineLimit(2)
            }
        }
    }
}

// MARK: - Category bar

struct CategoryShare: Identifiable, Hashable {
    let id: String
    let name: String
    let cents: Int64
    let color: Color

    init(name: String, cents: Int64, color: Color) {
        self.id = name
        self.name = name
        self.cents = cents
        self.color = color
    }
}

/// 10pt stacked bar with 3pt gaps, plus an optional legend ("● Zuivel €78").
struct CategoryBar: View {
    let shares: [CategoryShare]
    var height: CGFloat = 10
    var showsLegend: Bool = true

    var body: some View {
        VStack(alignment: .leading, spacing: Space.ml) {
            GeometryReader { geo in
                let total = max(1, shares.reduce(Int64(0)) { $0 + max(0, $1.cents) })
                let gaps = CGFloat(max(0, shares.count - 1)) * 3
                let width = max(0, geo.size.width - gaps)
                HStack(spacing: 3) {
                    ForEach(shares) { share in
                        RoundedRectangle.continuous(Radius.xs)
                            .fill(share.color)
                            .frame(width: max(2, width * CGFloat(max(0, share.cents)) / CGFloat(total)))
                    }
                }
            }
            .frame(height: height)
            .accessibilityHidden(true)

            if showsLegend {
                FlowLayout(spacing: 14, lineSpacing: 6) {
                    ForEach(shares) { share in
                        HStack(spacing: 5) {
                            CategoryDot(color: share.color)
                            Text(share.name).foregroundStyle(Palette.ink2)
                            Text(Money.whole(share.cents)).foregroundStyle(Palette.ink)
                        }
                        .textStyle(.rowSub)
                        .accessibilityElement(children: .combine)
                    }
                }
            }
        }
    }
}

// MARK: - Share bar (thin progress bar inside category rows)

struct ShareBar: View {
    let fraction: Double
    var color: Color
    var height: CGFloat = 4

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Palette.sunk)
                Capsule().fill(color).frame(width: geo.size.width * CGFloat(min(1, max(0, fraction))))
            }
        }
        .frame(height: height)
        .accessibilityHidden(true)
    }
}

// MARK: - Flow layout

/// Wraps children onto new lines (legends, chips).
struct FlowLayout: Layout {
    var spacing: CGFloat = 8
    var lineSpacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? .infinity
        var x: CGFloat = 0
        var y: CGFloat = 0
        var lineHeight: CGFloat = 0
        var widest: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > 0 && x + size.width > maxWidth {
                y += lineHeight + lineSpacing
                x = 0
                lineHeight = 0
            }
            x += size.width + spacing
            widest = max(widest, x - spacing)
            lineHeight = max(lineHeight, size.height)
        }
        return CGSize(width: proposal.width ?? widest, height: y + lineHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX
        var y = bounds.minY
        var lineHeight: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > bounds.minX && x + size.width > bounds.maxX {
                y += lineHeight + lineSpacing
                x = bounds.minX
                lineHeight = 0
            }
            view.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            lineHeight = max(lineHeight, size.height)
        }
    }
}

#Preview("Amounts") {
    VStack(alignment: .leading, spacing: 16) {
        HeroAmount(cents: 41238)
        HeroAmount(cents: 261490, size: 54)
        DeltaChip(deltaCents: -4272, caption: "less than this time in September")
        DeltaChip(deltaCents: 1250, caption: "more than September")
        CategoryBar(shares: [
            .init(name: "Zuivel", cents: 7840, color: Accent.blue),
            .init(name: "Groente", cents: 7120, color: Accent.green),
            .init(name: "Vlees", cents: 6690, color: Accent.red),
            .init(name: "Voorraad", cents: 5830, color: Accent.yellow),
            .init(name: "Brood", cents: 4160, color: Accent.orange),
            .init(name: "Overig", cents: 9600, color: Palette.neutral),
        ])
        ShareBar(fraction: 0.19, color: Accent.blue)
    }
    .padding(20)
    .background(Palette.bg)
}
