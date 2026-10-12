import SwiftUI

struct TabBarItem<ID: Hashable>: Identifiable {
    let id: ID
    let title: String
    let icon: Icon
    let selectedIcon: Icon
    var badge: Int = 0
    /// Quiet 8pt blue dot ("something here is waiting", no count): List tab when receipts suggest items.
    /// Ignored when `badge` is above 0.
    var dot: Bool = false
}

/// Floating 62pt pill (tray colour, bar shadow) with four tabs, and a detached 62pt Add button.
/// The active icon fills with a 120ms pop; tab change fires a selection tick.
struct PillTabBar<ID: Hashable>: View {
    let items: [TabBarItem<ID>]
    @Binding var selection: ID
    let onAdd: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduce
    @Environment(\.navIconStyle) private var iconStyle
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        HStack(spacing: Space.ml) {
            HStack(spacing: 0) {
                ForEach(items) { item in
                    tab(item)
                }
            }
            .padding(.horizontal, Space.m)
            .frame(height: Size.tabBar)
            .frame(maxWidth: .infinity)
            .background(Palette.tray, in: Capsule())
            .elevationRing(Capsule())
            .elevation(.bar)

            Button(action: onAdd) {
                IconView(.plus, size: 26, weight: .semibold)
                    .foregroundStyle(Palette.btnInk)
                    .frame(width: Size.tabBar, height: Size.tabBar)
                    .background(Palette.btn, in: Circle())
                    .elevation(.bar)
            }
            .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection)) // a second impact within 90 ms is dropped, so the Add tray does not double up
            .accessibilityLabel("Add")
        }
        .padding(.horizontal, Space.edge)
    }

    @ViewBuilder
    private func tabIcon(_ item: TabBarItem<ID>, on: Bool) -> some View {
        if let nav = NavIcon(item.icon) {
            NavIconView(icon: nav, selected: on, size: 24, style: iconStyle)
        } else {
            IconView(on ? item.selectedIcon : item.icon, size: 22, weight: on ? .semibold : .medium)
        }
    }

    private func tab(_ item: TabBarItem<ID>) -> some View {
        let on = item.id == selection
        return Button {
            guard !on else { return }
            Haptics.play(.selection)
            selection = item.id
        } label: {
            VStack(spacing: 3) {
                tabIcon(item, on: on)
                    .scaleEffect(on && !reduce ? 1 : 0.96)
                    .animation(reduce ? Motion.reducedFade : Motion.pop, value: on)
                // Labels would truncate to "H…" at the large sizes: icons only from XXXL, the name stays for VoiceOver.
                if typeSize < .xxxLarge {
                    TypeStyle.tab.text(item.title).lineLimit(1).minimumScaleFactor(0.8)
                }
            }
            .foregroundStyle(on ? Palette.ink : Palette.ink3)
            .frame(maxWidth: .infinity)
            .frame(height: Size.tabBar)
            .contentShape(Rectangle())
            .overlay(alignment: .top) {
                if item.badge > 0 {
                    CountBadge(count: item.badge, ring: Palette.tray)
                        .offset(x: 13, y: 8)
                } else if item.dot {
                    Circle().fill(Accent.blue)
                        .frame(width: 8, height: 8)
                        .overlay(Circle().strokeBorder(Palette.tray, lineWidth: 2).padding(-2))
                        .offset(x: 11, y: 10)
                }
            }
        }
        .buttonStyle(.plain)
        .accessibilityLabel(item.badge > 0 ? "\(item.title), \(item.badge) to review" : (item.dot ? "\(item.title), new suggestions" : item.title))
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

/// 140pt gradient behind the floating bar so content fades out under it.
struct BottomFade: View {
    var height: CGFloat = 140

    var body: some View {
        LinearGradient(
            stops: [.init(color: Palette.bg.opacity(0), location: 0), .init(color: Palette.bg, location: 0.58)],
            startPoint: .top, endPoint: .bottom
        )
        .frame(height: height)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

#Preview("Tab bar") {
    @Previewable @State var tab = 0
    VStack {
        Spacer()
        ZStack(alignment: .bottom) {
            BottomFade()
            PillTabBar(items: [
                TabBarItem(id: 0, title: "Home", icon: .home, selectedIcon: .homeFill, badge: 2),
                TabBarItem(id: 1, title: "Analytics", icon: .analytics, selectedIcon: .analyticsFill),
                TabBarItem(id: 2, title: "Products", icon: .products, selectedIcon: .productsFill),
                TabBarItem(id: 3, title: "List", icon: .list, selectedIcon: .listFill),
            ], selection: $tab) {}
                .padding(.bottom, 10)
        }
    }
    .background(Palette.bg)
}
