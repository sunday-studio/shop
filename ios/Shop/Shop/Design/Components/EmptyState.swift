import SwiftUI

// MARK: - Illustrations

/// Three tilted cards: pink and green behind a white receipt (H-Empty).
struct ReceiptStackIllustration: View {
    var body: some View {
        ZStack {
            card(Accent.pink, width: 66, height: 80)
                .rotationEffect(.degrees(-14))
                .offset(x: -36, y: 6)
            card(Accent.green, width: 62, height: 76)
                .rotationEffect(.degrees(12))
                .offset(x: 38, y: 2)
            PaperReceipt(lines: 4, highlight: Accent.yellow)
                .frame(width: 72, height: 100)
                .overlay(RoundedRectangle.continuous(10).strokeBorder(Palette.bg, lineWidth: 4))
                .shadow(color: .black.opacity(0.16), radius: 12, y: 10)
        }
        .frame(width: 150, height: 110)
        .accessibilityHidden(true)
    }

    private func card(_ color: Color, width: CGFloat, height: CGFloat) -> some View {
        RoundedRectangle.continuous(10)
            .fill(color)
            .frame(width: width, height: height)
            .overlay(RoundedRectangle.continuous(10).strokeBorder(Palette.bg, lineWidth: 4))
            .shadow(color: .black.opacity(0.12), radius: 9, y: 8)
    }
}

/// White paper with a dark title bar and grey lines; one line can be highlighted.
struct PaperReceipt: View {
    var lines: Int = 5
    var highlight: Color? = nil
    var highlightIndex: Int? = nil

    var body: some View {
        VStack(spacing: 6) {
            Capsule().fill(Accent.onLight).frame(height: 4).frame(maxWidth: .infinity).padding(.horizontal, 14)
            ForEach(0..<lines, id: \.self) { i in
                Capsule()
                    .fill(i == highlightIndex ? (highlight ?? Accent.amber) : Color(hex: 0xE4E4E1))
                    .frame(height: 3)
                    .padding(.trailing, i.isMultiple(of: 2) ? 0 : 12)
            }
            if let highlight, highlightIndex == nil {
                Capsule().fill(highlight).frame(height: 5).padding(.trailing, 28).padding(.top, 3)
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, 12)
        .padding(.horizontal, 9)
        .background(Accent.white, in: RoundedRectangle.continuous(10))
    }
}

/// Small tilted receipt (H.1 header). Tap opens the image.
struct ReceiptThumb: View {
    var pages: Int = 1
    var highlightIndex: Int? = 2

    var body: some View {
        VStack(spacing: 5) {
            Capsule().fill(Accent.onLight).frame(height: 3).padding(.horizontal, 12)
            ForEach(0..<5, id: \.self) { i in
                Rectangle()
                    .fill(i == highlightIndex ? Accent.amber : Palette.paperLine)
                    .frame(height: 2)
                    .padding(.trailing, i == 1 || i == 3 ? 10 : 0)
            }
            Spacer(minLength: 0)
            Text(pages == 1 ? "1 page" : "\(pages) pages")
                .font(.custom(ShopFont.medium, size: 9))
                .foregroundStyle(Color(hex: 0x73736F))
        }
        .padding(.vertical, 9)
        .padding(.horizontal, 7)
        .frame(width: 64, height: 84)
        .background(Palette.paper, in: RoundedRectangle.continuous(Radius.ml))
        .overlay(RoundedRectangle.continuous(Radius.ml).strokeBorder(Palette.line, lineWidth: 1))
        .rotationEffect(.degrees(3))
        .shadow(color: .black.opacity(0.10), radius: 8, y: 6)
    }
}

// MARK: - Empty state

struct EmptyState<Illustration: View>: View {
    let title: String
    var message: String? = nil
    var action: (title: String, run: () -> Void)? = nil
    @ViewBuilder var illustration: () -> Illustration

    var body: some View {
        VStack(spacing: Space.l) {
            illustration()
            Text(title)
                .textStyle(.emptyTitle)
                .foregroundStyle(Palette.ink)
                .multilineTextAlignment(.center)
            if let message {
                Text(message)
                    .textStyle(TypeStyle.body.sized(14.5))
                    .foregroundStyle(Palette.ink2)
                    .multilineTextAlignment(.center)
                    .lineSpacing(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let action {
                Button(action.title, action: action.run)
                    .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    .padding(.top, Space.xs)
            }
        }
        .padding(.horizontal, 36)
        .frame(maxWidth: .infinity)
    }
}

extension EmptyState where Illustration == ReceiptStackIllustration {
    init(title: String, message: String? = nil, action: (title: String, run: () -> Void)? = nil) {
        self.title = title
        self.message = message
        self.action = action
        self.illustration = { ReceiptStackIllustration() }
    }
}

/// Simple glyph-in-circle illustration for smaller empty states.
struct GlyphIllustration: View {
    let icon: Icon
    var color: Color = Accent.blue

    var body: some View {
        IconView(icon, size: 30, weight: .semibold)
            .foregroundStyle(color)
            .frame(width: 72, height: 72)
            .background(color.opacity(0.14), in: RoundedRectangle.continuous(22))
            .accessibilityHidden(true)
    }
}

/// Inline loading placeholder.
struct LoadingState: View {
    var title: String? = nil

    var body: some View {
        VStack(spacing: Space.m) {
            ProgressView().tint(Palette.ink2)
            if let title { Text(title).textStyle(.rowSub).foregroundStyle(Palette.ink2) }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 40)
    }
}

/// Inline error with retry. Only for screens with nothing cached; with a saved copy on screen use `StaleBanner`.
/// Offline gets its own title and glyph: it is the user's situation, not a fault.
struct ErrorState: View {
    let message: String
    var offline = false
    var retry: (() -> Void)? = nil

    var body: some View {
        EmptyState(title: offline ? "You're offline" : "Couldn't load",
                   message: message,
                   action: retry.map { (title: "Try again", run: $0) }) {
            GlyphIllustration(icon: offline ? .wifiSlash : .server, color: offline ? Accent.amber : Accent.red)
        }
        .padding(.vertical, 24)
    }
}

#Preview("Empty states") {
    ScrollView {
        VStack(spacing: 40) {
            EmptyState(title: "Scan receipts on the day", message: "Thermal paper fades in weeks. Scan now and review whenever you like. Scans upload as soon as you're back online.")
            HStack(spacing: 30) { ReceiptThumb(pages: 2); PaperReceipt(highlight: Accent.yellow).frame(width: 72, height: 100) }
            ErrorState(message: "The server didn't answer.") {}
            ErrorState(message: "Saved receipts still work. This needs a connection.", offline: true) {}
            LoadingState(title: "Loading receipts")
        }
        .padding(.vertical, 30)
    }
    .background(Palette.bg)
}
