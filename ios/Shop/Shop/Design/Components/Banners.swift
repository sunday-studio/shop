import SwiftUI

/// Semantic tone shared by banners, toasts and tinted buttons.
enum Tone {
    case amber, red, green, blue, neutral

    var text: Color {
        switch self {
        case .amber: Palette.amberText
        case .red: Palette.redText
        case .green: Palette.greenText
        case .blue: Palette.blueText
        case .neutral: Palette.ink
        }
    }

    var tint: Color {
        switch self {
        case .amber: Palette.amberTint
        case .red: Palette.redTint
        case .green: Palette.greenTint
        case .blue: Palette.blueTint
        case .neutral: Palette.sunk
        }
    }

    /// Solid badge colour.
    var solid: Color {
        switch self {
        case .amber: Accent.amber
        case .red: Accent.red
        case .green: Accent.green
        case .blue: Accent.blue
        case .neutral: Palette.ink3
        }
    }
}

// MARK: - Attention card

/// Home: "② Receipts to review · about 2 min ›". 48pt, radius 16, amber tint.
struct AttentionCard: View {
    let count: Int
    let title: String
    var hint: String? = nil
    var tone: Tone = .amber
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Space.ml) {
                CountBadge(count: count, size: 22, color: tone.solid)
                TypeStyle.rowTitle.text(title)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let hint { TypeStyle.body.sized(13).text(hint) }
                IconView(.forward, size: 14, weight: .bold)
            }
            .foregroundStyle(tone.text)
            .padding(.horizontal, Space.lx)
            .frame(minHeight: Size.attention)
            .background(tone.tint, in: RoundedRectangle.continuous(Radius.l))
        }
        .buttonStyle(.press(haptic: .light))
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Banner

/// Radius 18 tinted note: "! €0,25 short of the printed total. One row couldn't be read…"
struct Banner: View {
    var tone: Tone = .amber
    var glyph: GlyphBadge.Glyph = .alert
    /// Medium-weight first sentence.
    var lead: String? = nil
    var message: String
    var action: (title: String, run: () -> Void)? = nil

    var body: some View {
        HStack(alignment: .top, spacing: Space.ml) {
            GlyphBadge(glyph: glyph, color: tone.solid, size: 20)
            VStack(alignment: .leading, spacing: Space.s) {
                Text(attributed)
                    .textStyle(.body)
                    .lineSpacing(2)
                    .fixedSize(horizontal: false, vertical: true)
                if let action {
                    // Real chevron and a 44pt target; a trailing "›" typed into the title is dropped
                    // so callers that already add one don't get two.
                    Button(action: action.run) {
                        HStack(spacing: 4) {
                            TypeStyle.label.sized(14).text(action.title.replacingOccurrences(of: " ›", with: ""))
                            IconView(.forward, size: 11, weight: .bold)
                        }
                        .frame(minHeight: 44, alignment: .leading)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: .light))
                    .padding(.top, -Space.s)
                    .padding(.bottom, -Space.m)
                }
            }
            Spacer(minLength: 0)
        }
        .foregroundStyle(tone.text)
        .padding(.vertical, Space.l)
        .padding(.horizontal, Space.lx)
        .background(tone.tint, in: RoundedRectangle.continuous(Radius.xl))
    }

    private var attributed: AttributedString {
        var out = AttributedString()
        if let lead {
            var l = AttributedString(lead + " ")
            l.font = TypeStyle.body.withFace(.medium).font
            out += l
        }
        out += AttributedString(message)
        return out
    }
}

/// Short tinted row used in Settings ("! NAS backup is 2 days old ›").
struct NoticeRow: View {
    var tone: Tone = .amber
    let title: String
    var action: (() -> Void)? = nil

    var body: some View {
        let content = HStack(spacing: Space.ml) {
            GlyphBadge(color: tone.solid)
            TypeStyle.rowTitle.sized(14.5).text(title)
                .frame(maxWidth: .infinity, alignment: .leading)
            if action != nil { IconView(.forward, size: 14, weight: .bold) }
        }
        .foregroundStyle(tone.text)
        .padding(.horizontal, Space.lx)
        .frame(minHeight: Size.attention)
        .background(tone.tint, in: RoundedRectangle.continuous(Radius.l))

        if let action {
            Button(action: action) { content }.buttonStyle(.press(haptic: .light))
        } else {
            content
        }
    }
}

// MARK: - Toast

struct ToastMessage: Identifiable, Equatable {
    let id = UUID()
    var text: String
    var tone: Tone = .neutral
    /// Optional trailing button, e.g. "Undo". Runs once, then the toast closes.
    var action: (title: String, run: () -> Void)? = nil

    static func == (a: ToastMessage, b: ToastMessage) -> Bool { a.id == b.id }
}

/// Small capsule above the tab bar. One at a time; auto-dismisses.
@MainActor
@Observable
final class ToastCenter {
    var current: ToastMessage?
    private var dismissTask: Task<Void, Never>?

    func show(_ text: String, tone: Tone = .neutral, haptic: HapticEvent? = nil, duration: Duration = .seconds(2.6), action: (title: String, run: () -> Void)? = nil) {
        if let haptic { Haptics.play(haptic) }
        let message = ToastMessage(text: text, tone: tone, action: action)
        withAnimation(Motion.spring) { current = message }
        dismissTask?.cancel()
        dismissTask = Task { [weak self] in
            try? await Task.sleep(for: duration)
            guard !Task.isCancelled else { return }
            withAnimation(Motion.ease) {
                if self?.current == message { self?.current = nil }
            }
        }
    }

    func error(_ text: String) { show(text, tone: .red, haptic: .error) }

    /// "Merged into AH BIO MELK · Undo": stays 8 s so a second thought still works.
    /// The button plays `soft` (a restore, not an outcome) and closes the toast.
    func undoable(_ text: String, run: @escaping () -> Void) {
        show(text, duration: .seconds(8), action: (title: "Undo", run: { [weak self] in
            Haptics.play(.soft)
            withAnimation(Motion.ease) { self?.current = nil }
            run()
        }))
    }

    func dismiss() { withAnimation(Motion.ease) { current = nil } }
}

struct ToastView: View {
    let message: ToastMessage

    var body: some View {
        HStack(spacing: Space.m) {
            if message.tone != .neutral { StatusDot(color: message.tone.solid, size: 8) }
            TypeStyle.label.sized(14).text(message.text)
                .foregroundStyle(message.tone == .neutral ? Palette.btnInk : Palette.ink)
                .lineLimit(2)
            if let action = message.action {
                Button(action.title, action: action.run)
                    .textStyle(TypeStyle.label.sized(14))
                    .foregroundStyle(message.tone == .neutral ? Accent.blue : Palette.blueText)
                    .padding(.leading, Space.m)
                    .frame(minHeight: 36)
                    .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
            }
        }
        .padding(.leading, Space.xl)
        .padding(.trailing, message.action == nil ? Space.xl : Space.l)
        .padding(.vertical, message.action == nil ? Space.l : Space.xs)
        .background(message.tone == .neutral ? Palette.btn : Palette.tray, in: Capsule())
        .elevation(.bar)
        .accessibilityAddTraits(.isStaticText)
    }
}

extension View {
    /// Shows `center.current` near the bottom, above the tab bar.
    func toastHost(_ center: ToastCenter, bottomInset: CGFloat = 96) -> some View {
        overlay(alignment: .bottom) {
            if let message = center.current {
                ToastView(message: message)
                    .padding(.horizontal, Space.gutter)
                    .padding(.bottom, bottomInset)
                    .transition(.offset(y: 16).combined(with: .opacity))
                    .onTapGesture {
                        Haptics.play(.selection)
                        withAnimation(Motion.ease) { center.current = nil }
                    }
                    .id(message.id)
            }
        }
    }
}

#Preview("Banners") {
    VStack(spacing: 12) {
        AttentionCard(count: 2, title: "Receipts to review", hint: "about 2 min") {}
        Banner(lead: "€0,25 short of the printed total.", message: "One row couldn't be read, it's marked below.")
        Banner(tone: .red, lead: "No backup for more than 36 hours.", message: "Check the NAS share is mounted on the Mac mini.")
        NoticeRow(title: "NAS backup is 2 days old") {}
        ToastView(message: ToastMessage(text: "Receipt confirmed"))
        ToastView(message: ToastMessage(text: "Upload failed", tone: .red))
    }
    .padding(16)
    .background(Palette.bg)
}
