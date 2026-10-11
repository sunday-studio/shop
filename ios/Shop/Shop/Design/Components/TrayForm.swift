import SwiftUI

// Form and picker pieces for trays, taken from the design canvas (S3a Add shop, S4a Add category):
// grouped inline-label rows, a live preview chip, a colour grid, tile choice rows.
// `CategoryFormLayout` and `MoveProductsLayout` are the complete tray bodies; features own the
// state and the network calls and just hand them in.

// MARK: - Inline fields

/// One rounded block (radius 20, tray field colour) holding label/value rows.
struct InlineFieldGroup<Content: View>: View {
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(spacing: 0, content: content)
            .background(Palette.trayField, in: RoundedRectangle.continuous(20))
            .clipShape(RoundedRectangle.continuous(20))
    }
}

/// 50 pt row: 13 pt label in a 70 pt column on the left, 16 pt input on the right, hairline below.
struct InlineField: View {
    let label: String
    @Binding var text: String
    var placeholder: String = ""
    var error = false
    var divider = true
    var autofocus = false
    var mono = false
    var onSubmit: () -> Void = {}

    @FocusState private var focused: Bool
    @Environment(\.isInTray) private var inTray

    var body: some View {
        HStack(spacing: 12) {
            Text(label)
                .textStyle(.caption)
                .foregroundStyle(error ? Palette.redText : Palette.ink2)
                .frame(width: 70, alignment: .leading)
            TextField(placeholder, text: $text)
                .textStyle(mono ? TypeStyle.input.sized(16) : TypeStyle.input.sized(16))
                .foregroundStyle(Palette.ink)
                .tint(Accent.blue)
                .focused($focused)
                .submitLabel(.done)
                .onSubmit(onSubmit)
        }
        .padding(.horizontal, 16)
        .frame(height: 50)
        .background(error ? Palette.redTint : Color.clear)
        .overlay(alignment: .bottom) {
            if divider { Rectangle().fill(Palette.line).frame(height: 1) }
        }
        .contentShape(Rectangle())
        .onTapGesture { focused = true }
        .onAppear {
            guard autofocus else { return }
            guard inTray else { focused = true; return }
            Task {
                try? await Task.sleep(for: .milliseconds(450))
                focused = true
            }
        }
    }
}

// MARK: - Category pieces

/// Centred live preview of how the category will look as a chip: colour square and name.
struct CategoryPreviewChip: View {
    let name: String
    let color: Color

    var body: some View {
        HStack(spacing: 8) {
            RoundedRectangle.continuous(5).fill(color).frame(width: 14, height: 14)
            Text(name.isEmpty ? "Category name" : name)
                .textStyle(TypeStyle.label.sized(15))
                .foregroundStyle(name.isEmpty ? Palette.ink3 : Palette.ink)
                .lineLimit(1)
        }
        .padding(.leading, 12)
        .padding(.trailing, 16)
        .frame(height: 36)
        .background(Palette.trayField, in: Capsule())
        .frame(maxWidth: .infinity)
        .animation(Motion.fade, value: name.isEmpty)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Preview: \(name.isEmpty ? "no name yet" : name)")
    }
}

/// Ten colour tokens in two rows of five, 40 pt each. The selected one shows a white check and a
/// ring that sits 3 pt off the circle (the gap is the tray colour).
struct CategoryColourGrid: View {
    @Binding var selection: String?
    /// The token shown as selected when `selection` is nil (what the server will assign).
    var fallback: String = "blue"
    private let columns = Array(repeating: GridItem(.flexible(), spacing: 0), count: 5)

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Colour").textStyle(TypeStyle.label.sized(13)).foregroundStyle(Palette.ink2)
                .padding(.horizontal, Space.xs)
            LazyVGrid(columns: columns, spacing: 12) {
                ForEach(CategoryTone.tokens, id: \.self) { token in
                    let on = (selection ?? fallback) == token
                    let color = CategoryColor.swatch(token: token)?.color ?? Palette.neutral
                    Button {
                        Haptics.play(.selection)
                        selection = token
                    } label: {
                        Circle()
                            .fill(color)
                            .frame(width: 40, height: 40)
                            .overlay {
                                Image(icon: .check)
                                    .font(.system(size: 16, weight: .heavy))
                                    .foregroundStyle(.white)
                                    .opacity(on ? 1 : 0)
                                    .scaleEffect(on ? 1 : 0.4)
                            }
                            .background {
                                Circle().strokeBorder(color, lineWidth: 2)
                                    .padding(-5)
                                    .opacity(on ? 1 : 0)
                            }
                            .scaleEffect(on ? 1 : 0.92)
                            .animation(Motion.pop, value: on)
                            .frame(maxWidth: .infinity, minHeight: 48)
                    }
                    .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
                    .accessibilityLabel(token.capitalized)
                    .accessibilityAddTraits(on ? .isSelected : [])
                }
            }
        }
    }
}

/// New / rename category: header, live preview, one Name row, colour grid, Save.
/// The caller owns state and calls; `errorText` shows under the field (e.g. duplicate name).
struct CategoryFormLayout: View {
    let title: String
    @Binding var name: String
    @Binding var colour: String?
    var errorText: String? = nil
    var canSave: Bool
    var saving: Bool
    var onSubmit: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: title)
            CategoryPreviewChip(
                name: name.trimmingCharacters(in: .whitespaces),
                color: CategoryColor.swatch(token: colour ?? "blue")?.color ?? Accent.blue
            )
            VStack(alignment: .leading, spacing: Space.s) {
                InlineFieldGroup {
                    InlineField(label: "Name", text: $name, placeholder: "Category name",
                                error: errorText != nil, divider: false, autofocus: true, onSubmit: onSubmit)
                }
                if let errorText {
                    Text(errorText).textStyle(.caption).foregroundStyle(Palette.redText)
                        .padding(.horizontal, Space.xs)
                        .transition(.opacity)
                }
            }
            CategoryColourGrid(selection: $colour)
            Button(action: onSubmit) {
                LoadingLabel(title: "Save category", isLoading: saving)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(!canSave)
        }
        .padding(.horizontal, Space.xs)
    }
}

// MARK: - Choice rows and the move-products tray

/// 52 pt tile row (radius 16, tray field colour) with a leading colour square, a title, and a tick
/// when chosen. Same family as `OptionRow`.
struct ChoiceRow: View {
    let title: String
    var dot: Color? = nil
    var selected: Bool
    let action: () -> Void

    var body: some View {
        Button {
            Haptics.play(.selection)
            action()
        } label: {
            HStack(spacing: Space.l) {
                if let dot { RoundedRectangle.continuous(5).fill(dot).frame(width: 14, height: 14) }
                Text(title).textStyle(.rowTitle).foregroundStyle(Palette.ink)
                Spacer(minLength: 0)
                Image(icon: .check)
                    .font(.system(size: 14, weight: .heavy))
                    .foregroundStyle(Palette.ink)
                    .opacity(selected ? 1 : 0)
                    .scaleEffect(selected ? 1 : 0.5)
            }
            .padding(.horizontal, Space.lx)
            .frame(minHeight: 52)
            .background(selected ? Palette.sunk : Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            .overlay {
                RoundedRectangle.continuous(Radius.l)
                    .strokeBorder(Palette.ink, lineWidth: 1.5)
                    .opacity(selected ? 1 : 0)
            }
            .contentShape(RoundedRectangle.continuous(Radius.l))
            .animation(Motion.fade, value: selected)
        }
        .buttonStyle(.press(haptic: nil))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// Delete a category that still has products: choose where they go. "No category" is the first
/// row and starts selected, so the choice is visible without scrolling.
struct MoveProductsLayout: View {
    struct Destination: Identifiable, Hashable {
        /// 0 = no category.
        let id: Int64
        let name: String
        let colour: Color?
        func hash(into h: inout Hasher) { h.combine(id) }
        static func == (a: Destination, b: Destination) -> Bool { a.id == b.id }
    }

    let categoryName: String
    let count: Int
    let destinations: [Destination]
    @Binding var destination: Int64
    var working: Bool
    var onCancel: () -> Void
    var onDelete: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Delete \(categoryName)?",
                       subtitle: "\(count) product\(count == 1 ? "" : "s") use it. Pick where they go.")
            ScrollView {
                VStack(spacing: 6) {
                    ChoiceRow(title: "No category", dot: Palette.neutral, selected: destination == 0) { destination = 0 }
                    ForEach(destinations) { d in
                        ChoiceRow(title: d.name, dot: d.colour, selected: destination == d.id) { destination = d.id }
                    }
                }
            }
            .frame(maxHeight: 5 * 58)
            .scrollBounceBehavior(.basedOnSize)
            .mask(
                // Fade the last row out only when there is more below.
                LinearGradient(stops: [.init(color: .black, location: 0), .init(color: .black, location: 0.92), .init(color: .black.opacity(0.35), location: 1)],
                               startPoint: .top, endPoint: .bottom)
            )
            HStack(spacing: Space.m) {
                Button("Cancel", action: onCancel).buttonStyle(.shop(.secondary))
                Button(action: onDelete) {
                    LoadingLabel(title: "Delete and move \(count)", isLoading: working)
                }
                .buttonStyle(.shop(.solid(Accent.red), haptic: .medium))
                .disabled(working)
            }
        }
    }
}

#Preview("Category tray") {
    @Previewable @State var name = ""
    @Previewable @State var colour: String? = nil
    CategoryFormLayout(title: "Add category", name: $name, colour: $colour, errorText: nil, canSave: !name.isEmpty, saving: false) {}
        .padding(Space.xl)
        .background(Palette.tray)
}
