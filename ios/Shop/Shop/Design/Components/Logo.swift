import SwiftUI

// Home header logo options (design/logo/NOTES.md: tilted receipt on grain). Three lockups at header
// height. `wiggle` is bumped by the screen (first launch, pull to refresh): one short tilt, no loop.
// With Reduce Motion on, nothing moves.

/// Receipt with a toothed bottom edge, drawn in the unit square of `rect`.
struct ReceiptShape: Shape {
    var teeth = 5

    func path(in rect: CGRect) -> Path {
        var p = Path()
        let r = min(rect.width, rect.height) * 0.12
        let toothW = rect.width / CGFloat(teeth)
        let toothH = rect.height * 0.08
        let bottom = rect.maxY - toothH
        p.move(to: CGPoint(x: rect.minX + r, y: rect.minY))
        p.addLine(to: CGPoint(x: rect.maxX - r, y: rect.minY))
        p.addQuadCurve(to: CGPoint(x: rect.maxX, y: rect.minY + r), control: CGPoint(x: rect.maxX, y: rect.minY))
        p.addLine(to: CGPoint(x: rect.maxX, y: bottom))
        for i in 0..<teeth {
            let x0 = rect.maxX - CGFloat(i) * toothW
            p.addLine(to: CGPoint(x: x0 - toothW / 2, y: rect.maxY))
            p.addLine(to: CGPoint(x: x0 - toothW, y: bottom))
        }
        p.addLine(to: CGPoint(x: rect.minX, y: rect.minY + r))
        p.addQuadCurve(to: CGPoint(x: rect.minX + r, y: rect.minY), control: CGPoint(x: rect.minX, y: rect.minY))
        p.closeSubpath()
        return p
    }
}

/// A row of triangles pointing down: the torn edge of a receipt.
struct ToothEdge: Shape {
    var teeth = 10

    func path(in rect: CGRect) -> Path {
        var p = Path()
        let w = rect.width / CGFloat(teeth)
        p.move(to: CGPoint(x: rect.minX, y: rect.minY))
        p.addLine(to: CGPoint(x: rect.maxX, y: rect.minY))
        for i in 0..<teeth {
            let x0 = rect.maxX - CGFloat(i) * w
            p.addLine(to: CGPoint(x: x0 - w / 2, y: rect.maxY))
            p.addLine(to: CGPoint(x: x0 - w, y: rect.minY))
        }
        p.closeSubpath()
        return p
    }
}

/// The app icon in miniature: blue squircle, tilted receipt with a green dot, line items and the yellow total.
struct ShopIconBadge: View {
    var size: CGFloat = 32
    var tilt: Double = -9

    var body: some View {
        let w = size * 0.46
        let h = size * 0.62
        ZStack {
            RoundedRectangle.continuous(size * 0.28).fill(Accent.blue)
            ZStack(alignment: .topLeading) {
                ReceiptShape().fill(Color(hex: 0xF7F9FF))
                Circle().fill(Accent.green).frame(width: w * 0.14, height: w * 0.14).offset(x: w * 0.72, y: h * 0.1)
                RoundedRectangle.continuous(1.5).fill(Color(hex: 0x1D1D1B)).frame(width: w * 0.42, height: h * 0.07).offset(x: w * 0.12, y: h * 0.11)
                ForEach(0..<2, id: \.self) { i in
                    RoundedRectangle.continuous(1).fill(Color(hex: 0xCBD3E0)).frame(width: w * 0.62, height: h * 0.05)
                        .offset(x: w * 0.12, y: h * (0.28 + 0.13 * CGFloat(i)))
                }
                RoundedRectangle.continuous(h * 0.04).fill(Accent.yellow).frame(width: w * 0.76, height: h * 0.2)
                    .offset(x: w * 0.12, y: h * 0.56)
            }
            .frame(width: w, height: h)
            .rotationEffect(.degrees(tilt))
            .offset(y: -size * 0.04)
        }
        .frame(width: size, height: size)
        .clipShape(RoundedRectangle.continuous(size * 0.28))
    }
}

enum ShopLogoStyle: CaseIterable {
    /// A: icon badge plus bold wordmark.
    case badge
    /// B: ink wordmark with a torn-receipt edge under it and a green dot.
    case tear
    /// C: the icon's yellow total bar as a tilted pill with the wordmark and barcode ticks.
    case total
}

struct ShopLogo: View {
    var style: ShopLogoStyle = .badge
    var height: CGFloat = 32
    /// Change this value to play one short tilt (first launch, pull to refresh).
    var wiggle: Int = 0

    @Environment(\.accessibilityReduceMotion) private var reduce
    @State private var tilt: Double = 0

    var body: some View {
        Group {
            switch style {
            case .badge: badge
            case .tear: tear
            case .total: total
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Shop")
        .onChange(of: wiggle) { _, _ in play() }
        .onAppear { play() }
    }

    private func play() {
        guard !reduce else { return }
        tilt = -7
        withAnimation(.spring(response: 0.5, dampingFraction: 0.45)) { tilt = 0 }
    }

    private var badge: some View {
        HStack(spacing: 8) {
            ShopIconBadge(size: height, tilt: -9 + tilt)
            Text("Shop")
                .font(.custom(ShopFont.bold, size: height * 0.72))
                .tracking(-height * 0.72 * 0.04)
                .foregroundStyle(Palette.ink)
        }
    }

    private var tear: some View {
        VStack(spacing: height * 0.06) {
            HStack(alignment: .lastTextBaseline, spacing: 2) {
                Text("shop")
                    .font(.custom(ShopFont.bold, size: height * 0.84))
                    .tracking(-height * 0.84 * 0.04)
                    .foregroundStyle(Palette.ink)
                Circle().fill(Accent.green).frame(width: height * 0.18, height: height * 0.18)
            }
            ToothEdge(teeth: 12)
                .fill(Accent.yellow)
                .frame(width: height * 2.1, height: height * 0.16)
        }
        .rotationEffect(.degrees(-3 + tilt))
    }

    private var total: some View {
        HStack(spacing: height * 0.22) {
            Text("shop")
                .font(.custom(ShopFont.bold, size: height * 0.62))
                .tracking(-height * 0.62 * 0.04)
            HStack(spacing: 1.5) {
                ForEach([2.0, 1.0, 3.0, 1.0, 2.0, 1.0], id: \.self) { w in
                    Rectangle().frame(width: w * height * 0.04, height: height * 0.4)
                }
            }
            .opacity(0.8)
        }
        .foregroundStyle(Accent.onLight)
        .padding(.horizontal, height * 0.34)
        .frame(height: height)
        .background(Accent.yellow, in: RoundedRectangle.continuous(height * 0.34))
        .overlay(alignment: .leading) {
            Circle().fill(Accent.green).frame(width: height * 0.2, height: height * 0.2).offset(x: -height * 0.06, y: -height * 0.3)
        }
        .rotationEffect(.degrees(-4 + tilt))
    }
}

#Preview("Logo options") {
    VStack(alignment: .leading, spacing: 24) {
        ForEach(ShopLogoStyle.allCases, id: \.self) { ShopLogo(style: $0) }
    }
    .padding(Space.gutter)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(Palette.bg)
}
