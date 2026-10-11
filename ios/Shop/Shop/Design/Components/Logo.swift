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

/// The Home header logo: the app icon in miniature next to a bold "Shop". `wiggle` is bumped by
/// the screen (first launch, pull to refresh) to play one short tilt; no loop; none with Reduce Motion.
struct ShopLogo: View {
    var height: CGFloat = 30
    var wiggle: Int = 0

    @Environment(\.accessibilityReduceMotion) private var reduce
    @State private var tilt: Double = 0

    var body: some View {
        HStack(spacing: 8) {
            ShopIconBadge(size: height, tilt: -9 + tilt)
            Text("Shop")
                .font(.custom(ShopFont.bold, size: height * 0.72))
                .tracking(-height * 0.72 * 0.04)
                .foregroundStyle(Palette.ink)
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
}

#Preview("Logo") {
    ShopLogo().padding(Space.gutter).background(Palette.bg)
}
