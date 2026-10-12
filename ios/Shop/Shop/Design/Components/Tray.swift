import SwiftUI

// Trays (design/MOTION.md "Tray or full screen"): short, self-contained decisions that return
// to where the user was. A tray never hosts navigation and never stacks: presenting while one
// is open replaces it: the surface height and the content swap share one curve (Motion.sheetSwap).

@MainActor
@Observable
final class TrayCoordinator {
    struct Presented: Identifiable {
        let id: String
        let content: AnyView
        var dismissible: Bool
    }

    private(set) var current: Presented?
    /// Called when a tray is dismissed (scrim tap, drag, close or programmatically).
    private var onDismiss: (() -> Void)?

    var isPresented: Bool { current != nil }

    func present<Content: View>(
        _ id: String,
        dismissible: Bool = true,
        onDismiss: (() -> Void)? = nil,
        @ViewBuilder content: () -> Content
    ) {
        let replacing = current != nil
        let next = Presented(id: id, content: AnyView(content()), dismissible: dismissible)
        if !replacing { Haptics.play(.trayOpen) }
        withAnimation(replacing ? Motion.sheetSwap : Motion.sheet) {
            current = next
        }
        self.onDismiss = onDismiss
    }

    func dismiss() {
        guard current != nil else { return }
        let callback = onDismiss
        onDismiss = nil
        withAnimation(Motion.sheetExit) { current = nil }
        callback?()
    }
}

private struct InTrayKey: EnvironmentKey {
    static let defaultValue = false
}

extension EnvironmentValues {
    /// True for views inside a tray. Fields use it to wait for the enter animation before taking
    /// focus (the keyboard would move the tray while it is still sliding in).
    var isInTray: Bool {
        get { self[InTrayKey.self] }
        set { self[InTrayKey.self] = newValue }
    }
}

private struct TrayHost: ViewModifier {
    let coordinator: TrayCoordinator
    @Environment(\.accessibilityReduceMotion) private var reduce

    func body(content: Content) -> some View {
        content
            .overlay {
                ZStack(alignment: .bottom) {
                    if let tray = coordinator.current {
                        Palette.scrim
                            .ignoresSafeArea()
                            .onTapGesture { if tray.dismissible { coordinator.dismiss() } }
                            .transition(.opacity)
                            .accessibilityAddTraits(.isButton)
                            .accessibilityLabel("Close")

                        TraySurface(tray: tray, coordinator: coordinator)
                            .transition(reduce ? .opacity : .tray)
                            .environment(coordinator)
                    }
                }
                .ignoresSafeArea(.container, edges: .bottom)
            }
    }
}

/// The tray itself, built like a Vaul drawer: ONE surface (background, ring, content) that is
/// moved as a whole with a single transform. The content never has its own entrance animation, so
/// text and fields can't drift apart. Content swaps (tray replaced by tray) overlay the old and
/// new content in a ZStack so the two never stack in layout, and the height change rides
/// `Motion.sheetSwap`.
private struct TraySurface: View {
    let tray: TrayCoordinator.Presented
    let coordinator: TrayCoordinator

    var body: some View {
        ClampedHeight {
            ScrollView {
                ZStack(alignment: .top) {
                    tray.content
                        .id(tray.id)
                        .environment(\.isInTray, true)
                        .transition(.traySwap)
                }
            }
            .scrollBounceBehavior(.basedOnSize)
            .scrollClipDisabled()
            .scrollDismissesKeyboard(.interactively)
        }
        .padding(.top, 22)
        .padding(.horizontal, Space.xl)
        .padding(.bottom, Space.xl)
        .frame(maxWidth: .infinity)
        .background(Palette.tray, in: RoundedRectangle.continuous(Radius.tray))
        .elevationRing(RoundedRectangle.continuous(Radius.tray))
        .modifier(TrayDrag(resetKey: tray.id, dismissible: tray.dismissible, onDismiss: { coordinator.dismiss() }))
        .padding(.horizontal, Space.trayInset)
        .padding(.bottom, Space.trayInset)
        .padding(.top, 54)
    }
}

/// Takes the height its content wants, but never more than it is offered. A tray is a short
/// `ScrollView` that only scrolls when it has to: with the keyboard up (the offered height shrinks
/// by the keyboard) a tall tray scrolls instead of running off the top of the screen. Measuring
/// the content's ideal height in the layout (not in state) means there is no first-frame jump.
private struct ClampedHeight: Layout {
    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard let view = subviews.first else { return .zero }
        let ideal = view.sizeThatFits(ProposedViewSize(width: proposal.width, height: nil))
        let height = proposal.height.map { min(ideal.height, $0) } ?? ideal.height
        return CGSize(width: proposal.width ?? ideal.width, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, anchor: .topLeading, proposal: ProposedViewSize(width: bounds.width, height: bounds.height))
    }
}

/// Drag to dismiss. Drag state lives here, in a modifier, so moving the finger invalidates only
/// this tiny body and never re-evaluates the tray content (Vaul: updating shared state per frame
/// made big drawers drop frames). The tray follows the finger 1:1 downwards, resists upwards
/// (the further you pull the less it moves), and on release closes if it is flicked (velocity) or
/// dragged a quarter of its height.
private struct TrayDrag: ViewModifier {
    let resetKey: String
    let dismissible: Bool
    let onDismiss: () -> Void

    @State private var drag: CGFloat = 0
    /// The tray's height, read only when a drag ends. A plain box, not @State: while a tray swaps,
    /// the height changes every frame and writing @State would re-run this body 60 to 120 times
    /// a second for nothing.
    @State private var height = HeightBox()

    func body(content: Content) -> some View {
        content
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height.value = $0 }
            .offset(y: drag)
            .gesture(
                DragGesture(minimumDistance: 8)
                    .onChanged { value in
                        let dy = value.translation.height
                        if dy >= 0 {
                            drag = dismissible ? dy : Self.rubber(dy, limit: 40)
                        } else {
                            drag = -Self.rubber(-dy, limit: 24)
                        }
                    }
                    .onEnded { value in
                        let velocity = value.velocity.height
                        let shouldClose = dismissible && (velocity > 400 || value.translation.height > height.value * 0.25)
                        if shouldClose {
                            Haptics.play(.trayDismiss)
                            // Keep `drag` where the finger left it: the exit slides on from there.
                            onDismiss()
                        } else {
                            withAnimation(Motion.sheetExit) { drag = 0 }
                        }
                    }
            )
            .onChange(of: resetKey) { _, _ in drag = 0 }
    }

    private final class HeightBox { var value: CGFloat = 400 }

    /// Diminishing movement: the first points follow, later ones barely move.
    private static func rubber(_ x: CGFloat, limit: CGFloat) -> CGFloat {
        limit * (1 - 1 / (x / limit + 1))
    }
}

extension View {
    /// Hosts trays for everything underneath (apply at the app root and inside full-screen covers).
    func trayHost(_ coordinator: TrayCoordinator) -> some View {
        modifier(TrayHost(coordinator: coordinator))
            .environment(coordinator)
    }
}

// MARK: - Tray header

/// "Add line                                   ✕"
struct TrayHeader: View {
    let title: String
    var subtitle: String? = nil
    var onClose: (() -> Void)? = nil
    @Environment(TrayCoordinator.self) private var trays

    var body: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 2) {
                TypeStyle.trayTitle.text(title).foregroundStyle(Palette.ink)
                    .accessibilityAddTraits(.isHeader)
                if let subtitle {
                    TypeStyle.rowSub.text(subtitle).foregroundStyle(Palette.ink2)
                }
            }
            Spacer()
            IconButton.close {
                if let onClose { onClose() } else { trays.dismiss() }
            }
        }
        .padding(.horizontal, Space.xs)
    }
}

// MARK: - Option row

/// 52pt tray option: icon, title, hint. Destructive rows use the red tint (H.1.5).
struct OptionRow: View {
    let icon: Icon
    let title: String
    var hint: String? = nil
    var destructive: Bool = false
    var haptic: HapticEvent? = .light
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Space.l) {
                IconView(icon, size: 18)
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text(title)
                    if let hint {
                        TypeStyle.rowSub.text(hint)
                            .foregroundStyle(destructive ? Palette.redText : Palette.ink2)
                    }
                }
                Spacer(minLength: 0)
            }
            .foregroundStyle(destructive ? Palette.redText : Palette.ink)
            .padding(.vertical, Space.m)
            .padding(.horizontal, Space.lx)
            .frame(minHeight: 52)
            .background(destructive ? Palette.redTint : Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            .contentShape(RoundedRectangle.continuous(Radius.l))
        }
        .buttonStyle(.press(haptic: haptic))
    }
}

// MARK: - Feature card (C tray tiles, Settings server card)

/// Saturated rounded card with a translucent icon tile, title and caption.
struct FeatureCard<Accessory: View>: View {
    let icon: Icon
    let title: String
    var caption: String? = nil
    var color: Color
    var height: CGFloat = 168
    @ViewBuilder var accessory: () -> Accessory
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 0) {
                HStack(alignment: .top) {
                    IconTile(icon: icon, color: .white.opacity(0.2), size: 44)
                    Spacer()
                    accessory()
                }
                Spacer(minLength: Space.l)
                VStack(alignment: .leading, spacing: 3) {
                    TypeStyle.rowTitle.sized(17).text(title)
                        .lineLimit(2)
                        .minimumScaleFactor(0.75)
                    if let caption {
                        TypeStyle.body.sized(13).text(caption)
                            .opacity(0.82)
                            .lineSpacing(1)
                            .multilineTextAlignment(.leading)
                    }
                }
            }
            .foregroundStyle(Accent.white)
            .padding(Space.xl)
            .frame(maxWidth: .infinity, minHeight: height, alignment: .leading)
            // Hug the content (at least `height`) instead of letting the Spacer fill a tray.
            .fixedSize(horizontal: false, vertical: true)
            .background(color, in: RoundedRectangle.continuous(Radius.card))
        }
        .buttonStyle(.press(haptic: .light))
    }
}

extension DynamicTypeSize {
    /// Two-up grids of cards (Add tray) should become one column from here, or words break mid-word.
    var prefersSingleColumn: Bool { self >= .xxLarge }
}

extension FeatureCard where Accessory == EmptyView {
    init(icon: Icon, title: String, caption: String? = nil, color: Color, height: CGFloat = 168, action: @escaping () -> Void) {
        self.icon = icon
        self.title = title
        self.caption = caption
        self.color = color
        self.height = height
        self.accessory = { EmptyView() }
        self.action = action
    }
}

#Preview("Tray") {
    @Previewable @State var trays = TrayCoordinator()
    VStack(spacing: 16) {
        Button("Open options") {
            trays.present("options") {
                VStack(spacing: 14) {
                    TrayHeader(title: "Options")
                    VStack(spacing: 6) {
                        OptionRow(icon: .edit, title: "Edit details", hint: "Shop, date and printed total") {}
                        OptionRow(icon: .reparse, title: "Reparse", hint: "Apply the latest rules · keeps your edits") {
                            trays.present("confirm") {
                                VStack(spacing: 14) {
                                    TrayHeader(title: "Reparse receipt?")
                                    Button("Reparse") { trays.dismiss() }.buttonStyle(.shop())
                                }
                            }
                        }
                        OptionRow(icon: .trash, title: "Delete receipt", hint: "Removes the receipt and its image", destructive: true) {}
                    }
                }
            }
        }
        .buttonStyle(.shop())
        HStack(spacing: 8) {
            FeatureCard(icon: .scan, title: "Scan receipt", caption: "Paper receipt, add pages for long ones", color: Accent.blue) {}
            FeatureCard(icon: .barcode, title: "Unpack groceries", caption: "Scan barcodes, add new products", color: Accent.green) {}
        }
    }
    .padding(16)
    .frame(maxHeight: .infinity)
    .background(Palette.bg)
    .trayHost(trays)
}
