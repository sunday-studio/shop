import SwiftUI

// MARK: - Field tile

/// Label above input on a rounded tile (radius 16; 20 for the large onboarding field).
/// Keyboard type, content type and autocapitalisation propagate from modifiers on the tile.
struct FieldTile: View {
    let label: String
    @Binding var text: String
    var placeholder: String = ""
    var mono: Bool = false
    var emphasised: Bool = false
    var large: Bool = false
    var isSecure: Bool = false
    var error: Bool = false
    var background: Color = Palette.sunk
    var onSubmit: () -> Void = {}

    @FocusState private var focused: Bool
    @State private var revealed = false

    var body: some View {
        HStack(spacing: Space.m) {
            VStack(alignment: .leading, spacing: large ? 3 : 2) {
                Text(label)
                    .textStyle(large ? .captionMedium : .caption)
                    .foregroundStyle(error ? Palette.redText : Palette.ink2)
                input
                    .textStyle(inputStyle)
                    .foregroundStyle(Palette.ink)
                    .tint(Accent.blue)
                    .focused($focused)
                    .onSubmit(onSubmit)
            }
            if isSecure {
                Button {
                    Haptics.play(.selection)
                    revealed.toggle()
                } label: {
                    IconView(revealed ? .eyeSlash : .eye, size: 18)
                        .foregroundStyle(Palette.ink2)
                        .frame(width: 32, height: 32)
                }
                .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
                .accessibilityLabel(revealed ? "Hide password" : "Show password")
            }
        }
        .padding(.vertical, large ? 12 : 10)
        .padding(.horizontal, large ? 16 : 14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(error ? Palette.redTint : background, in: RoundedRectangle.continuous(large ? 20 : Radius.l))
        .overlay {
            RoundedRectangle.continuous(large ? 20 : Radius.l)
                .strokeBorder(focusColor, lineWidth: 2)
                .opacity(focused || error ? 1 : 0)
        }
        .contentShape(Rectangle())
        .onTapGesture { focused = true }
        .animation(Motion.fade, value: focused)
        .animation(Motion.fade, value: error)
    }

    @ViewBuilder
    private var input: some View {
        if isSecure && !revealed {
            SecureField(placeholder, text: $text)
        } else {
            TextField(placeholder, text: $text)
        }
    }

    private var inputStyle: TypeStyle {
        if mono { return TypeStyle.mono.sized(large ? 15.5 : 16) }
        let base = TypeStyle.input.sized(17)
        return emphasised ? base.withFace(.medium) : base
    }

    private var focusColor: Color { error ? Palette.redText : Accent.blue }
}

// MARK: - Search field

/// 44pt capsule with a magnifier, used at the top of P, L.1, X.1 and Link product.
struct SearchField: View {
    var placeholder: String = "Search"
    @Binding var text: String
    var background: Color = Palette.sunk
    var autofocus: Bool = false
    var onSubmit: () -> Void = {}

    @FocusState private var focused: Bool
    @Environment(\.isInTray) private var inTray

    var body: some View {
        HStack(spacing: Space.ml) {
            IconView(.search, size: 17, weight: .semibold)
                .foregroundStyle(Palette.ink2)
            TextField(placeholder, text: $text)
                .textStyle(TypeStyle.input.sized(15))
                .foregroundStyle(Palette.ink)
                .tint(Accent.blue)
                .focused($focused)
                .submitLabel(.search)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .onSubmit(onSubmit)
            if !text.isEmpty {
                Button {
                    text = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 16))
                        .foregroundStyle(Palette.ink3)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Clear")
            }
        }
        .padding(.horizontal, Space.xl)
        .frame(height: Size.search)
        .background(background, in: Capsule())
        .onAppear {
            guard autofocus else { return }
            guard inTray else { focused = true; return }
            // Inside a tray: wait for the 0.45 s enter animation, then focus.
            Task {
                try? await Task.sleep(for: .milliseconds(450))
                focused = true
            }
        }
    }
}

/// Tappable capsule that looks like a search field ("+ Add an item").
struct FieldButton: View {
    let title: String
    var icon: Icon = .plus
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Space.ml) {
                IconView(icon, size: 17, weight: .semibold)
                TypeStyle.input.sized(15).text(title)
                Spacer(minLength: 0)
            }
            .foregroundStyle(Palette.ink2)
            .padding(.horizontal, Space.xl)
            .frame(height: Size.search)
            .background(Palette.sunk, in: Capsule())
        }
        .buttonStyle(.press(haptic: .light))
    }
}

// MARK: - Raw row tile

/// "Row 14 on the receipt / ST#TIEG&LD ……… 0,25" — the source text being edited.
struct RawRowTile: View {
    let caption: String
    let raw: String
    var amount: String? = nil
    var background: Color = Palette.trayField

    var body: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 2) {
                TypeStyle.caption.text(caption).foregroundStyle(Palette.ink2)
                TypeStyle.mono.sized(15).text(raw).foregroundStyle(Palette.ink)
                    .lineLimit(2)
            }
            Spacer(minLength: Space.m)
            if let amount {
                TypeStyle.mono.sized(15).text(amount).foregroundStyle(Palette.ink)
            }
        }
        .padding(.vertical, Space.l)
        .padding(.horizontal, Space.lx)
        .background(background, in: RoundedRectangle.continuous(Radius.l))
    }
}

#Preview("Fields") {
    @Previewable @State var url = "http://shop.tail3a9f.ts.net:8080"
    @Previewable @State var pw = "hunter2"
    @Previewable @State var q = ""
    @Previewable @State var qty = "1"
    VStack(spacing: 10) {
        FieldTile(label: "Server URL", text: $url, mono: true, large: true)
        FieldTile(label: "Password", text: $pw, large: true, isSecure: true)
        FieldTile(label: "Password", text: $pw, large: true, isSecure: true, error: true)
        HStack {
            FieldTile(label: "Quantity · pc", text: $qty)
            FieldTile(label: "Line total", text: $qty, emphasised: true)
        }
        SearchField(placeholder: "Products or receipt names", text: $q)
        FieldButton(title: "Add an item") {}
        RawRowTile(caption: "Row 14 on the receipt", raw: "ST#TIEG&LD", amount: "0,25")
    }
    .padding(16)
    .background(Palette.bg)
}
