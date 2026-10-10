import SwiftUI

// MARK: - Chip

/// Filter / choice chip. Selected = ink fill. Optional leading category dot.
struct Chip: View {
    let title: String
    var isSelected: Bool
    var dot: Color? = nil
    var size: ChipSize = .regular
    /// Background when not selected (sunk on screens, trayField inside trays).
    var idleBackground: Color = Palette.sunk
    let action: () -> Void

    enum ChipSize {
        /// 32pt, 13pt text (P filters, suggestions).
        case regular
        /// 36pt, 14pt text (line kinds in a tray).
        case large
        /// 24pt, radius 8, 12pt text ("All 21", "To fix 3").
        case small
    }

    var body: some View {
        Button {
            Haptics.play(.selection)
            action()
        } label: {
            HStack(spacing: 6) {
                if let dot {
                    CategoryDot(color: isSelected ? Palette.btnInk : dot)
                }
                Text(title).textStyle(textStyle)
            }
            .foregroundStyle(isSelected ? Palette.btnInk : Palette.ink)
            .padding(.leading, dot == nil ? hPad : hPad - 2)
            .padding(.trailing, hPad)
            .frame(height: height)
            .background(isSelected ? Palette.btn : idleBackground, in: shape)
            .contentShape(shape)
            .animation(Motion.fade, value: isSelected)
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
        .accessibilityAddTraits(isSelected ? .isSelected : [])
    }

    private var height: CGFloat {
        switch size {
        case .regular: Size.chip
        case .large: Size.chipLarge
        case .small: Size.chipSmall
        }
    }

    private var hPad: CGFloat {
        switch size {
        case .regular: 12
        case .large: 14
        case .small: 8
        }
    }

    private var textStyle: TypeStyle {
        switch size {
        case .regular: TypeStyle.label
        case .large: TypeStyle.label.sized(14)
        case .small: TypeStyle.captionMedium
        }
    }

    private var shape: AnyShape {
        size == .small ? AnyShape(RoundedRectangle.continuous(Radius.s)) : AnyShape(Capsule())
    }
}

/// Small static chip with a custom tone ("To fix 3" amber).
struct ToneChip: View {
    let title: String
    var tone: Tone = .amber
    var isSelected: Bool = false
    let action: () -> Void

    var body: some View {
        Button {
            Haptics.play(.selection)
            action()
        } label: {
            Text(title)
                .textStyle(.captionMedium)
                .foregroundStyle(isSelected ? Palette.bg : tone.text)
                .padding(.horizontal, 8)
                .frame(height: Size.chipSmall)
                .background(isSelected ? tone.solid : tone.tint, in: RoundedRectangle.continuous(Radius.s))
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
    }
}

/// Horizontal scrolling chip row.
struct ChipRow<Item: Hashable>: View {
    let items: [Item]
    let title: (Item) -> String
    var dot: ((Item) -> Color?)? = nil
    @Binding var selection: Item

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(items, id: \.self) { item in
                    Chip(title: title(item), isSelected: item == selection, dot: dot?(item)) {
                        selection = item
                    }
                }
            }
            .padding(.horizontal, Space.edge)
        }
        .scrollClipDisabled()
    }
}

// MARK: - Suggestion chip

/// "+ Eieren" on a soft colour tint (L: usually bought by now).
struct SuggestionChip: View {
    let title: String
    var color: Color = Accent.blue
    var icon: Icon = .plus
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                IconView(icon, size: 13, weight: .bold)
                Text(title).textStyle(TypeStyle.label.sized(13.5))
            }
            .foregroundStyle(Palette.ink)
            .padding(.leading, 8)
            .padding(.trailing, 12)
            .frame(height: Size.chip)
            .background(color.opacity(0.22), in: Capsule())
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: nil)) // the add action plays its own light tap; touch-down + action was a double
    }
}

// MARK: - Segmented control

/// Sunk track (radius 14, 3pt inset) with a 34pt white knob that springs between options.
struct SegmentedControl<Option: Hashable>: View {
    let options: [Option]
    let title: (Option) -> String
    @Binding var selection: Option
    var track: Color = Palette.sunk

    @Namespace private var knob

    var body: some View {
        HStack(spacing: 2) {
            ForEach(options, id: \.self) { option in
                let on = option == selection
                Button {
                    guard !on else { return }
                    Haptics.play(.selection)
                    withAnimation(Motion.spring) { selection = option }
                } label: {
                    Text(title(option))
                        .textStyle(TypeStyle.label.sized(14))
                        .foregroundStyle(on ? Palette.ink : Palette.ink2)
                        .frame(maxWidth: .infinity)
                        .frame(height: Size.segment)
                        .background {
                            if on {
                                RoundedRectangle.continuous(Radius.m)
                                    .fill(Palette.seg)
                                    .elevation(.segment)
                                    .matchedGeometryEffect(id: "knob", in: knob)
                            }
                        }
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
        .padding(3)
        .background(track, in: RoundedRectangle.continuous(14))
    }
}

#Preview("Chips") {
    @Previewable @State var cat = "All"
    @Previewable @State var seg = "By category"
    VStack(alignment: .leading, spacing: 14) {
        ChipRow(items: ["All", "Dairy", "Produce", "Meat & fish", "Bakery"], title: { $0 }, dot: { $0 == "All" ? nil : CategoryColor.swatch(for: $0).color }, selection: $cat)
        HStack {
            Chip(title: "All 21", isSelected: true, size: .small) {}
            ToneChip(title: "To fix 3") {}
            Chip(title: "Deposit", isSelected: true, size: .large) {}
            Chip(title: "Item", isSelected: false, size: .large) {}
        }
        HStack {
            SuggestionChip(title: "Eieren", color: Accent.blue) {}
            SuggestionChip(title: "Koffiebonen", color: Accent.yellow) {}
        }
        SegmentedControl(options: ["By category", "By shop"], title: { $0 }, selection: $seg)
            .padding(.horizontal, 16)
    }
    .padding(.vertical)
    .background(Palette.bg)
}
