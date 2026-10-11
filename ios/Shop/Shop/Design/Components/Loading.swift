import SwiftUI

// One system for loading, refreshing, empty and error states.
// Rules and the page-by-page list: docs/DESIGN-LOADING-STATES.md.
//
//   first load, shape known  -> skeleton (after 250 ms, held 400 ms), never a spinner
//   cached data              -> show it, refresh silently, never a skeleton
//   refresh                  -> the system pull-to-refresh only
//   action in progress       -> spinner inside the control (LoadingLabel)
//   nothing to show          -> EmptyState; failed with no cache -> ErrorState; failed with cache -> StaleBanner

// MARK: - Phase and container

enum ScreenPhase: Equatable {
    /// No data yet and a request is running.
    case loading
    case content
    /// Loaded, nothing to show.
    case empty
    /// Failed with nothing cached to fall back on.
    case failed(String)
}

/// Switches between skeleton, content, empty and error without flashing:
/// a load that finishes inside `Motion.skeletonDelay` never shows a skeleton, and a skeleton that
/// did appear stays for `Motion.skeletonMinimum` so it doesn't blink.
struct StatefulContent<Skeleton: View, Empty: View, Content: View>: View {
    let phase: ScreenPhase
    var retry: (() -> Void)? = nil
    @ViewBuilder var skeleton: () -> Skeleton
    @ViewBuilder var empty: () -> Empty
    @ViewBuilder var content: () -> Content

    @State private var shown: ScreenPhase
    @State private var skeletonVisible = false
    @State private var skeletonSince: ContinuousClock.Instant?

    init(phase: ScreenPhase, retry: (() -> Void)? = nil,
         @ViewBuilder skeleton: @escaping () -> Skeleton,
         @ViewBuilder empty: @escaping () -> Empty,
         @ViewBuilder content: @escaping () -> Content) {
        self.phase = phase
        self.retry = retry
        self.skeleton = skeleton
        self.empty = empty
        self.content = content
        _shown = State(initialValue: phase)
    }

    var body: some View {
        Group {
            switch shown {
            case .loading:
                if skeletonVisible { skeleton().transition(.opacity) } else { Color.clear.frame(height: 1) }
            case .content:
                content().transition(.opacity)
            case .empty:
                empty().transition(.opacity)
            case .failed(let message):
                ErrorState(message: message, retry: retry).transition(.opacity)
            }
        }
        .task(id: phase) { await settle() }
    }

    private func settle() async {
        if phase == .loading {
            shown = .loading
            skeletonVisible = false
            try? await Task.sleep(for: Motion.skeletonDelay)
            guard !Task.isCancelled else { return }
            withAnimation(Motion.stateSwap) { skeletonVisible = true }
            skeletonSince = .now
        } else {
            if skeletonVisible, let since = skeletonSince {
                let left = Motion.skeletonMinimum - (ContinuousClock.now - since)
                if left > .zero { try? await Task.sleep(for: left) }
                guard !Task.isCancelled else { return }
            }
            withAnimation(Motion.stateSwap) {
                shown = phase
                skeletonVisible = false
            }
        }
    }
}

// MARK: - Skeleton primitives

private struct SkeletonOnCardKey: EnvironmentKey { static let defaultValue = false }
extension EnvironmentValues {
    /// Bars sit on a `Palette.sunk` card: draw them in `Palette.bg` instead.
    var skeletonOnCard: Bool {
        get { self[SkeletonOnCardKey.self] }
        set { self[SkeletonOnCardKey.self] = newValue }
    }
}

struct SkeletonBar: View {
    var width: CGFloat? = nil
    var height: CGFloat = 12
    @Environment(\.skeletonOnCard) private var onCard

    var body: some View {
        RoundedRectangle.continuous(height / 2)
            .fill(onCard ? Palette.bg : Palette.skeleton)
            .frame(width: width, height: height)
    }
}

struct SkeletonBlock: View {
    var width: CGFloat? = nil
    var height: CGFloat
    var radius: CGFloat = Radius.m
    @Environment(\.skeletonOnCard) private var onCard

    var body: some View {
        RoundedRectangle.continuous(radius)
            .fill(onCard ? Palette.bg : Palette.skeleton)
            .frame(width: width, height: height)
    }
}

/// One calm opacity pulse for the whole skeleton (not a shimmer sweep: cheaper on the GPU and
/// readable with Reduce Motion, where it stays still).
private struct SkeletonPulse: ViewModifier {
    @Environment(\.accessibilityReduceMotion) private var reduce
    @State private var dim = false

    func body(content: Content) -> some View {
        content
            .opacity(dim && !reduce ? 0.5 : 1)
            .onAppear { withAnimation(Motion.skeletonPulse) { dim = true } }
            .accessibilityHidden(true)
    }
}

extension View {
    /// Marks a view as a skeleton: pulse, hidden from VoiceOver.
    func skeleton() -> some View { modifier(SkeletonPulse()) }
}

// MARK: - Skeleton rows (same footprint as the real rows, so content replaces them in place)

struct SkeletonReceiptRow: View {
    var body: some View {
        HStack(spacing: Space.l) {
            SkeletonBlock(width: Size.shopMarkHeader, height: Size.shopMarkHeader, radius: Radius.m)
            VStack(alignment: .leading, spacing: 7) {
                SkeletonBar(width: 130, height: 13)
                SkeletonBar(width: 84, height: 10)
            }
            Spacer()
            SkeletonBar(width: 52, height: 13)
        }
        .frame(height: Size.rowReceipt)
        .hairline()
    }
}

struct SkeletonProductRow: View {
    var body: some View {
        HStack(spacing: Space.l) {
            SkeletonBlock(width: Size.tile, height: Size.tile)
            VStack(alignment: .leading, spacing: 7) {
                SkeletonBar(width: 150, height: 13)
                SkeletonBar(width: 100, height: 10)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 7) {
                SkeletonBar(width: 56, height: 13)
                SkeletonBar(width: 36, height: 10)
            }
        }
        .frame(height: Size.rowProduct)
        .hairline()
    }
}

/// Dot + name + count: categories, shops, simple settings lists.
struct SkeletonSimpleRow: View {
    var body: some View {
        HStack(spacing: Space.l) {
            SkeletonBlock(width: 12, height: 12, radius: Radius.xs)
            SkeletonBar(width: 120, height: 13)
            Spacer()
            SkeletonBar(width: 22, height: 12)
        }
        .frame(height: 46)
        .hairline()
    }
}

/// Check circle + title: shopping list.
struct SkeletonListRow: View {
    var body: some View {
        HStack(spacing: Space.l) {
            Circle().fill(Palette.skeleton).frame(width: 24, height: 24)
            SkeletonBar(width: 140, height: 13)
            Spacer()
            SkeletonBar(width: 40, height: 10)
        }
        .frame(height: Size.rowList)
        .hairline()
    }
}

/// Label/value rows on detail screens.
struct SkeletonLineRow: View {
    var body: some View {
        HStack(spacing: Space.l) {
            SkeletonBar(width: 170, height: 13)
            Spacer()
            SkeletonBar(width: 48, height: 13)
        }
        .frame(height: Size.rowLine)
        .hairline()
    }
}

/// Card-sized block (hero, chart, total): give it the real card's height.
struct SkeletonCard: View {
    var height: CGFloat
    var body: some View {
        RoundedRectangle.continuous(Radius.card)
            .fill(Palette.skeleton)
            .frame(height: height)
    }
}

/// Bars-shaped placeholder for the analytics chart.
struct SkeletonChart: View {
    var height: CGFloat = 160
    private let fractions: [CGFloat] = [0.45, 0.7, 0.55, 0.9, 0.62, 0.8]

    var body: some View {
        HStack(alignment: .bottom, spacing: Space.m) {
            ForEach(fractions.indices, id: \.self) { i in
                SkeletonBlock(height: height * fractions[i], radius: Radius.s)
            }
        }
        .frame(height: height, alignment: .bottom)
    }
}

/// A column of identical skeleton rows. Pass the row you need.
struct SkeletonRows<Row: View>: View {
    var count = 6
    @ViewBuilder var row: () -> Row

    var body: some View {
        VStack(spacing: 0) {
            ForEach(0..<count, id: \.self) { _ in row() }
        }
        .skeleton()
    }
}

// MARK: - Stale data and paging

/// Failed to refresh, but a saved copy is on screen. Sits at the top of the list; clears itself on the next success.
struct StaleBanner: View {
    /// "products", "receipts", "categories"
    let what: String
    var offline = false
    var retry: () -> Void

    var body: some View {
        Banner(tone: .amber, glyph: .alert,
               message: offline ? "You're offline. Showing saved \(what)." : "Couldn't reach the server. Showing saved \(what).",
               action: (title: "Try again", run: retry))
    }
}

/// Footer under a paged list: spinner while loading more, a retry row when it failed.
struct LoadMoreFooter: View {
    enum State { case idle, loading, failed }
    let state: State
    var retry: () -> Void

    var body: some View {
        switch state {
        case .idle:
            EmptyView()
        case .loading:
            ProgressView().controlSize(.small).tint(Palette.ink2)
                .frame(maxWidth: .infinity, minHeight: 56)
                .accessibilityLabel("Loading more")
        case .failed:
            Button(action: retry) {
                HStack(spacing: 6) {
                    TypeStyle.body.text("Couldn't load more").foregroundStyle(Palette.ink2)
                    TypeStyle.label.sized(14).text("Try again").foregroundStyle(Palette.blueText)
                }
                .frame(maxWidth: .infinity, minHeight: 56)
                .contentShape(Rectangle())
            }
            .buttonStyle(.press(haptic: .light))
        }
    }
}

// MARK: - Preview

#Preview("Loading states") {
    ScrollView {
        VStack(alignment: .leading, spacing: 28) {
            SkeletonRows { SkeletonReceiptRow() }
            SkeletonRows { SkeletonProductRow() }
            SkeletonChart()
            StaleBanner(what: "products", retry: {})
        }
        .padding(Space.gutter)
    }
    .background(Palette.bg)
}
