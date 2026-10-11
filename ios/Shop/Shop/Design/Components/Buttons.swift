import SwiftUI

// MARK: - Pill buttons

enum ButtonKind {
    /// Ink fill (inverts in dark).
    case primary
    /// Sunk fill, ink text.
    case secondary
    /// Red tint, red text.
    case destructive
    /// Any tint/text pair (e.g. amber, green).
    case tinted(text: Color, tint: Color)
    /// Solid colour fill with white text (feature cards).
    case solid(Color)
}

enum ButtonSize {
    /// 56pt, onboarding.
    case large
    /// 54pt, trays.
    case regular
    /// 50pt, confirm bar.
    case confirm
    /// 36pt, inline.
    case small

    var height: CGFloat {
        switch self {
        case .large: Size.buttonLarge
        case .regular: Size.button
        case .confirm: Size.buttonConfirm
        case .small: Size.chipLarge
        }
    }

    var type: TypeStyle {
        switch self {
        case .large: TypeStyle.button.sized(16.5)
        case .regular, .confirm: .button
        case .small: TypeStyle.label.sized(14)
        }
    }
}

/// Capsule buttons from the wireframes. Presses scale to 0.97 and fire their haptic on touch-down.
struct ShopButtonStyle: ButtonStyle {
    var kind: ButtonKind = .primary
    var size: ButtonSize = .regular
    var fullWidth: Bool = true
    var haptic: HapticEvent? = .light

    @Environment(\.isEnabled) private var isEnabled
    @Environment(\.accessibilityReduceMotion) private var reduce

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .textStyle(size.type)
            .lineLimit(1)
            .foregroundStyle(foreground)
            .frame(maxWidth: fullWidth ? .infinity : nil)
            .padding(.horizontal, fullWidth ? Space.xl : (size == .small ? Space.lx : Space.xxxl))
            .frame(height: size.height)
            .background(background, in: Capsule())
            .contentShape(Capsule())
            .opacity(isEnabled || isFlatDisabled ? 1 : 0.4)
            .scaleEffect(configuration.isPressed && !reduce ? Motion.pressScale : 1)
            .animation(configuration.isPressed ? Motion.press : Motion.spring, value: configuration.isPressed)
            .onChange(of: configuration.isPressed) { _, pressed in
                if pressed, isEnabled, let haptic { Haptics.play(haptic) }
            }
    }

    /// Primary buttons go flat grey when disabled (the design's disabled Save), not a faded black pill.
    private var isFlatDisabled: Bool {
        if case .primary = kind { return true }
        return false
    }

    private var foreground: Color {
        if !isEnabled, isFlatDisabled { return Palette.btnDisabledInk }
        return switch kind {
        case .primary: Palette.btnInk
        case .secondary: Palette.ink
        case .destructive: Palette.redText
        case .tinted(let text, _): text
        case .solid: Accent.white
        }
    }

    private var background: Color {
        if !isEnabled, isFlatDisabled { return Palette.btnDisabled }
        return switch kind {
        case .primary: Palette.btn
        case .secondary: Palette.sunk
        case .destructive: Palette.redTint
        case .tinted(_, let tint): tint
        case .solid(let c): c
        }
    }
}

extension ButtonStyle where Self == ShopButtonStyle {
    static func shop(
        _ kind: ButtonKind = .primary,
        size: ButtonSize = .regular,
        fullWidth: Bool = true,
        haptic: HapticEvent? = .light
    ) -> ShopButtonStyle {
        ShopButtonStyle(kind: kind, size: size, fullWidth: fullWidth, haptic: haptic)
    }
}

/// Primary capsule that shows a spinner while `isLoading`.
struct LoadingLabel: View {
    let title: String
    var isLoading: Bool

    var body: some View {
        ZStack {
            Text(title).opacity(isLoading ? 0 : 1)
            if isLoading {
                ProgressView().tint(Palette.btnInk)
            }
        }
        .animation(Motion.fade, value: isLoading)
    }
}

// MARK: - Link button ("+ Add line")

struct LinkButton: View {
    let title: String
    var icon: Icon? = .plus
    var color: Color = Palette.blueText
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Space.m) {
                if let icon { IconView(icon, size: 16, weight: .semibold) }
                Text(title).textStyle(TypeStyle.label.sized(14))
            }
            .foregroundStyle(color)
            .frame(minHeight: Size.rowCompact, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: .light))
    }
}

// MARK: - Icon button

/// 36pt circle (32 inside trays) with an 18pt glyph.
struct IconButton: View {
    let icon: Icon
    let label: String
    var size: CGFloat = Size.iconButton
    var glyph: CGFloat = 18
    var foreground: Color = Palette.ink
    var background: Color = Palette.sunk
    var haptic: HapticEvent? = nil
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            IconView(icon, size: glyph, weight: .semibold)
                .foregroundStyle(foreground)
                .frame(width: size, height: size)
                .background(background, in: Circle())
                .contentShape(Circle())
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: haptic))
        .accessibilityLabel(label)
    }

    /// The small close button used in tray headers.
    static func close(action: @escaping () -> Void) -> IconButton {
        IconButton(icon: .close, label: "Close", size: Size.iconButtonSmall, glyph: 13, foreground: Palette.ink2, background: Palette.trayField, action: action)
    }

    static func back(action: @escaping () -> Void) -> IconButton {
        IconButton(icon: .back, label: "Back", action: action)
    }
}

// MARK: - Avatar

/// Top-left yellow square that opens Settings.
struct Avatar: View {
    var initial: String = "S"
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(initial.prefix(1).uppercased())
                .textStyle(TypeStyle.rowTitle)
                .foregroundStyle(Accent.onLight)
                .frame(width: Size.avatar, height: Size.avatar)
                .background(Accent.avatar, in: RoundedRectangle.continuous(Radius.ml))
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
        .accessibilityLabel("Settings")
    }
}

// MARK: - Capsule menu button ("Last 6 months ⌄")

struct PillMenuButton: View {
    let title: String
    var icon: Icon = .down
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Space.s) {
                Text(title).textStyle(TypeStyle.label.sized(13.5))
                IconView(icon, size: 12, weight: .bold)
            }
            .foregroundStyle(Palette.ink)
            .padding(.leading, Space.lx)
            .padding(.trailing, Space.l)
            .frame(height: Size.segment)
            .background(Palette.sunk, in: Capsule())
        }
        .buttonStyle(.pill)
    }
}

#Preview("Buttons") {
    VStack(spacing: 12) {
        Button("Connect your server") {}.buttonStyle(.shop(size: .large))
        Button("Ignore row") {}.buttonStyle(.shop(.secondary))
        Button("Delete receipt") {}.buttonStyle(.shop(.destructive))
        Button("Confirm") {}.buttonStyle(.shop(size: .confirm, fullWidth: false))
        HStack {
            IconButton.back {}
            IconButton(icon: .more, label: "More") {}
            IconButton.close {}
            Avatar {}
            PillMenuButton(title: "Last 6 months") {}
        }
        LinkButton(title: "Add line") {}
    }
    .padding()
    .background(Palette.bg)
}
