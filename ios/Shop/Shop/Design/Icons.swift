import SwiftUI

/// The wireframe glyphs, mapped to SF Symbols (stroke weight matched per use).
enum Icon: String, CaseIterable {
    // Navigation
    case back = "chevron.left"
    case forward = "chevron.right"
    case down = "chevron.down"
    case close = "xmark"
    case more = "ellipsis"
    case search = "magnifyingglass"
    case plus = "plus"
    case check = "checkmark"

    // Tabs
    case home = "house"
    case homeFill = "house.fill"
    case analytics = "chart.bar"
    case analyticsFill = "chart.bar.fill"
    case products = "shippingbox"
    case productsFill = "shippingbox.fill"
    case list = "checklist"
    case listFill = "checklist.checked"

    // Actions
    case scan = "doc.viewfinder"
    case barcode = "barcode.viewfinder"
    case camera = "camera"
    case edit = "pencil"
    case image = "photo"
    case reparse = "arrow.triangle.2.circlepath"
    case reocr = "text.viewfinder"
    case trash = "trash"
    case upload = "arrow.up.to.line"
    case link = "link"
    case unlink = "link.badge.plus"
    case retry = "arrow.clockwise"
    case server = "server.rack"
    case shop = "storefront"
    case grid = "square.grid.2x2"
    case info = "info.circle"
    case money = "eurosign"
    case haptics = "hand.tap"
    case appearance = "circle.lefthalf.filled"
    case signOut = "rectangle.portrait.and.arrow.right"
    case trendUp = "arrow.up.right"
    case trendDown = "arrow.down.right"
    case arrowUp = "arrow.up"
    case arrowDown = "arrow.down"
    case calendar = "calendar"
    case eye = "eye"
    case eyeSlash = "eye.slash"
    case wifiSlash = "wifi.slash"
    case warning = "exclamationmark"
    case bolt = "bolt.fill"
    case leaf = "leaf"
    case nutrition = "chart.pie"
    case reorder = "line.3.horizontal"
    case filter = "line.3.horizontal.decrease"
    case clock = "clock"
    case sparkle = "sparkles"
    case flash = "bolt"
    case flashOff = "bolt.slash"
}

extension Image {
    init(icon: Icon) {
        self.init(systemName: icon.rawValue)
    }
}

/// An SF Symbol sized in points with a stroke weight that matches the wireframes' 2–2.4px strokes.
struct IconView: View {
    let icon: Icon
    var size: CGFloat = 18
    var weight: Font.Weight = .medium

    init(_ icon: Icon, size: CGFloat = 18, weight: Font.Weight = .medium) {
        self.icon = icon
        self.size = size
        self.weight = weight
    }

    var body: some View {
        Image(icon: icon)
            .font(.system(size: size * 0.92, weight: weight))
            .frame(width: size, height: size)
            .accessibilityHidden(true)
    }
}
