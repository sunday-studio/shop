import SwiftUI

// Headers sit 12pt under the status bar (wireframe 56pt from the top on a 44pt status bar).

// MARK: - Sync pill

/// Only on screen when something needs attention ("Offline · 1 waiting", "Uploading 3").
struct SyncPill: View {
    let title: String
    var dot: Color = Palette.ink3
    var isBusy: Bool = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 7) {
                if isBusy {
                    ProgressView().controlSize(.mini).tint(Palette.ink2)
                } else {
                    StatusDot(color: dot, size: 8)
                }
                Text(title).textStyle(.label).lineLimit(1)
            }
            .foregroundStyle(Palette.ink)
            .padding(.leading, 10)
            .padding(.trailing, 12)
            .frame(height: Size.chip)
            .background(Palette.sunk, in: Capsule())
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: .light))
        .transition(.opacity.combined(with: .scale(scale: 0.9)))
    }
}

// MARK: - Tab root header (Home)

/// Avatar (Settings) · optional sync pill · search.
struct HomeHeader<Center: View>: View {
    var initial: String = "S"
    let onSettings: () -> Void
    let onSearch: () -> Void
    @ViewBuilder var center: () -> Center

    var body: some View {
        HStack {
            Avatar(initial: initial, action: onSettings)
            Spacer()
            center()
            Spacer()
            IconButton(icon: .search, label: "Search", action: onSearch)
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.l)
    }
}

extension HomeHeader where Center == EmptyView {
    init(initial: String = "S", onSettings: @escaping () -> Void, onSearch: @escaping () -> Void) {
        self.initial = initial
        self.onSettings = onSettings
        self.onSearch = onSearch
        self.center = { EmptyView() }
    }
}

// MARK: - Large title header (Analytics, Products, List)

/// "Products  248" with an optional trailing control.
struct LargeTitleHeader<Trailing: View>: View {
    let title: String
    var detail: String? = nil
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(alignment: .center) {
            HStack(alignment: .firstTextBaseline, spacing: Space.m) {
                Text(title).textStyle(.screenTitle).foregroundStyle(Palette.ink)
                    .accessibilityAddTraits(.isHeader)
                if let detail {
                    Text(detail).textStyle(.body).foregroundStyle(Palette.ink2)
                        .contentTransition(.numericText())
                }
            }
            Spacer(minLength: Space.m)
            trailing()
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.l)
        .frame(minHeight: Size.iconButton + Space.l)
    }
}

extension LargeTitleHeader where Trailing == EmptyView {
    init(title: String, detail: String? = nil) {
        self.title = title
        self.detail = detail
        self.trailing = { EmptyView() }
    }
}

// MARK: - Pushed screen header

/// Back circle · centred 16pt title · trailing action (or a 36pt spacer).
struct NavHeader<Trailing: View>: View {
    var title: String = ""
    var onBack: (() -> Void)? = nil
    @ViewBuilder var trailing: () -> Trailing

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Text(title)
                .textStyle(.navTitle)
                .foregroundStyle(Palette.ink)
                .lineLimit(1)
                .padding(.horizontal, 56)
                .accessibilityAddTraits(.isHeader)
            HStack {
                IconButton.back { if let onBack { onBack() } else { dismiss() } }
                Spacer()
                trailing()
                    .frame(minWidth: Size.iconButton, alignment: .trailing)
            }
        }
        .padding(.horizontal, Space.edge)
        .padding(.top, Space.l)
    }
}

/// Fixed 36pt blank that balances the back button.
struct HeaderSpacer: View {
    var body: some View { Color.clear.frame(width: Size.iconButton, height: Size.iconButton) }
}

extension NavHeader where Trailing == HeaderSpacer {
    init(title: String = "", onBack: (() -> Void)? = nil) {
        self.title = title
        self.onBack = onBack
        self.trailing = { HeaderSpacer() }
    }
}

// MARK: - Onboarding step header

struct StepHeader: View {
    let step: String
    var onBack: (() -> Void)? = nil

    var body: some View {
        ZStack {
            Text(step).textStyle(.label).foregroundStyle(Palette.ink2)
            HStack {
                if let onBack { IconButton.back(action: onBack) }
                Spacer()
            }
        }
        .frame(height: Size.iconButton)
        .padding(.horizontal, Space.edge)
        .padding(.top, Space.l)
    }
}

/// Onboarding headline + paragraph block.
struct HeadlineBlock: View {
    let title: String
    var message: String? = nil
    var size: CGFloat = 32

    var body: some View {
        VStack(alignment: .leading, spacing: Space.ml) {
            Text(title)
                .textStyle(TypeStyle.display.sized(size))
                .foregroundStyle(Palette.ink)
                .lineSpacing(-2)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityAddTraits(.isHeader)
            if let message {
                Text(message)
                    .textStyle(.bodyLarge)
                    .foregroundStyle(Palette.ink2)
                    .lineSpacing(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

#Preview("Headers") {
    VStack(spacing: 20) {
        HomeHeader(onSettings: {}, onSearch: {}) {
            SyncPill(title: "Offline · 1 waiting") {}
        }
        LargeTitleHeader(title: "Products", detail: "248") {
            IconButton(icon: .barcode, label: "Scan barcode") {}
        }
        LargeTitleHeader(title: "List", detail: "8 to buy · about €21")
        NavHeader(title: "Receipt") {
            IconButton(icon: .more, label: "More actions") {}
        }
        NavHeader(title: "Settings")
        StepHeader(step: "Step 1 of 2") {}
        HeadlineBlock(title: "Where is your server?", message: "The address of the Mac mini running Shop.")
            .padding(.horizontal, 24)
        Spacer()
    }
    .background(Palette.bg)
}
