import SwiftUI

// Spacing, radii, sizes and elevation (docs/DESIGN-SYSTEM.md §3).

enum Space {
    static let xxs: CGFloat = 2
    static let xs: CGFloat = 4
    static let s: CGFloat = 6
    static let m: CGFloat = 8
    static let ml: CGFloat = 10
    static let l: CGFloat = 12
    static let lx: CGFloat = 14
    static let xl: CGFloat = 16
    static let xxl: CGFloat = 20
    static let xxxl: CGFloat = 24

    /// Screen side gutter for content.
    static let gutter: CGFloat = 20
    /// Header, banners and cards sit at 16.
    static let edge: CGFloat = 16
    /// Trays float 8pt from the screen edges.
    static let trayInset: CGFloat = 8
    /// Space reserved at the bottom of tab root scroll views for the floating tab bar.
    static let tabBarClearance: CGFloat = 112
}

enum Radius {
    static let xs: CGFloat = 3
    static let s: CGFloat = 8
    static let m: CGFloat = 11
    static let ml: CGFloat = 12
    static let l: CGFloat = 16
    static let xl: CGFloat = 18
    static let card: CGFloat = 26
    static let feature: CGFloat = 28
    static let bar: CGFloat = 34
    static let tray: CGFloat = 36
}

enum Size {
    static let iconButton: CGFloat = 36
    static let iconButtonSmall: CGFloat = 32
    static let avatar: CGFloat = 36
    static let shopMark: CGFloat = 22
    static let shopMarkHeader: CGFloat = 28
    static let tile: CGFloat = 36
    static let rowReceipt: CGFloat = 54
    static let rowProduct: CGFloat = 52
    static let rowList: CGFloat = 50
    static let rowLine: CGFloat = 46
    static let rowCompact: CGFloat = 44
    static let chip: CGFloat = 32
    static let chipLarge: CGFloat = 36
    static let chipSmall: CGFloat = 24
    static let button: CGFloat = 54
    static let buttonLarge: CGFloat = 56
    static let buttonConfirm: CGFloat = 50
    static let tabBar: CGFloat = 62
    static let attention: CGFloat = 48
    static let segment: CGFloat = 34
    static let search: CGFloat = 44
}

// MARK: - Elevation

enum Elevation {
    case bar, tray, segment
}

private struct ElevationModifier: ViewModifier {
    let level: Elevation
    @Environment(\.colorScheme) private var scheme

    func body(content: Content) -> some View {
        let dark = scheme == .dark
        switch level {
        case .bar:
            content
                .shadow(color: dark ? .black.opacity(0.5) : Color(hex: 0x1D1D1B, alpha: 0.12), radius: 15, y: 10)
        case .tray:
            content
                .shadow(color: dark ? .black.opacity(0.6) : Color(hex: 0x1D1D1B, alpha: 0.16), radius: dark ? 15 : 20, y: dark ? -4 : 10)
        case .segment:
            content
                .shadow(color: dark ? .clear : Color(hex: 0x1D1D1B, alpha: 0.12), radius: 1.5, y: 1)
        }
    }
}

extension View {
    func elevation(_ level: Elevation) -> some View {
        modifier(ElevationModifier(level: level))
    }

    /// The faint 1pt ring the wireframes add around floating light-mode surfaces.
    func elevationRing<S: InsettableShape>(_ shape: S) -> some View {
        modifier(RingModifier(shape: shape))
    }
}

private struct RingModifier<S: InsettableShape>: ViewModifier {
    let shape: S
    @Environment(\.colorScheme) private var scheme

    func body(content: Content) -> some View {
        content.overlay(
            shape.strokeBorder(Color(hex: 0x1D1D1B, alpha: scheme == .dark ? 0 : 0.05), lineWidth: 1)
        )
    }
}

// MARK: - Common shapes

extension RoundedRectangle {
    static func continuous(_ radius: CGFloat) -> RoundedRectangle {
        RoundedRectangle(cornerRadius: radius, style: .continuous)
    }
}

extension View {
    /// Hairline divider under a row.
    func hairline(_ edge: VerticalEdge = .bottom, inset: CGFloat = 0) -> some View {
        overlay(alignment: edge == .bottom ? .bottom : .top) {
            Palette.line
                .frame(height: 1)
                .padding(.leading, inset)
        }
    }
}
