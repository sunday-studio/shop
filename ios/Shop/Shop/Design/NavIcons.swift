import SwiftUI

// Navigation icons. Duotone (Phosphor Icons, MIT, docs/licenses/phosphor-icons-MIT.txt) is the default:
// a solid outline plus a 20 % fill of the same colour, drawn from template SVG assets, so one
// foregroundStyle tints both layers and light/dark and selected/unselected come for free.
// The SF Symbol variant (palette rendering) stays for comparison in the Catalog and as a fallback.

enum NavIcon: String, CaseIterable {
    case home = "nav-home"
    case analytics = "nav-analytics"
    case products = "nav-products"
    case list = "nav-list"

    /// The SF Symbol equivalents used before, for the comparison and as a fallback.
    var symbol: (off: String, on: String) {
        switch self {
        case .home: ("house", "house.fill")
        case .analytics: ("chart.bar", "chart.bar.fill")
        case .products: ("shippingbox", "shippingbox.fill")
        case .list: ("checklist", "checklist.checked")
        }
    }

    /// Maps the SF-based `Icon` a `TabBarItem` carries to its duotone twin.
    init?(_ icon: Icon) {
        switch icon {
        case .home, .homeFill: self = .home
        case .analytics, .analyticsFill: self = .analytics
        case .products, .productsFill: self = .products
        case .list, .listFill: self = .list
        default: return nil
        }
    }
}

enum NavIconStyle {
    case duotone
    case symbol
}

private struct NavIconStyleKey: EnvironmentKey { static let defaultValue = NavIconStyle.duotone }
extension EnvironmentValues {
    var navIconStyle: NavIconStyle {
        get { self[NavIconStyleKey.self] }
        set { self[NavIconStyleKey.self] = newValue }
    }
}

struct NavIconView: View {
    let icon: NavIcon
    var selected = false
    var size: CGFloat = 24
    var style: NavIconStyle = .duotone

    var body: some View {
        Group {
            switch style {
            case .duotone:
                Image(icon.rawValue)
                    .renderingMode(.template)
                    .resizable()
                    .scaledToFit()
                    .frame(width: size, height: size)
            case .symbol:
                Image(systemName: selected ? icon.symbol.on : icon.symbol.off)
                    .symbolRenderingMode(.palette)
                    .font(.system(size: size * 0.92, weight: selected ? .semibold : .medium))
                    .frame(width: size, height: size)
            }
        }
        .accessibilityHidden(true)
    }
}
