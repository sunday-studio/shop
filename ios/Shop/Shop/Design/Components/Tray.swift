import SwiftUI

// Trays (design/MOTION.md "Tray or full screen"): short, self-contained decisions that return
// to where the user was. A tray never hosts navigation and never stacks: presenting while one
// is open replaces it, the container height springs and the content cross-fades (160ms).

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
        withAnimation(replacing ? Motion.heightSpring : Motion.spring) {
            current = next
        }
        self.onDismiss = onDismiss
    }

    func dismiss() {
        guard current != nil else { return }
        let callback = onDismiss
        onDismiss = nil
        withAnimation(Motion.ease) { current = nil }
        callback?()
    }
}

private struct TrayHost: ViewModifier {
    let coordinator: TrayCoordinator
    @State private var drag: CGFloat = 0
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

                        VStack(spacing: 0) {
                            tray.content
                                .id(tray.id)
                                .transition(.opacity.animation(Motion.fade))
                        }
                        .padding(.top, 22)
                        .padding(.horizontal, Space.xl)
                        .padding(.bottom, Space.xl)
                        .frame(maxWidth: .infinity)
                        .background(Palette.tray, in: RoundedRectangle.continuous(Radius.tray))
                        .elevationRing(RoundedRectangle.continuous(Radius.tray))
                        .padding(.horizontal, Space.trayInset)
                        .padding(.bottom, Space.trayInset)
                        .padding(.top, 54)
                        .offset(y: max(0, drag))
                        .gesture(dragGesture(dismissible: tray.dismissible))
                        .transition(reduce ? .opacity : .tray)
                        .environment(coordinator)
                    }
                }
                .ignoresSafeArea(.container, edges: .bottom)
            }
            .onChange(of: coordinator.current?.id) { _, id in
                if id != nil { drag = 0 }
            }
    }

    private func dragGesture(dismissible: Bool) -> some Gesture {
        DragGesture(minimumDistance: 12)
            .onChanged { value in
                let dy = value.translation.height
                // Resist upward drags; follow downward ones.
                drag = dy > 0 ? (dismissible ? dy : dy * 0.2) : dy * 0.1
            }
            .onEnded { value in
                let shouldClose = dismissible && (value.translation.height > 120 || value.predictedEndTranslation.height > 260)
                if shouldClose {
                    Haptics.play(.trayDismiss)
                    // Leave `drag` where the finger left it so the tray fades out from there;
                    // resetting it here snapped the tray back up before the exit transition.
                    coordinator.dismiss()
                } else {
                    withAnimation(Motion.spring) { drag = 0 }
                }
            }
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
                Text(title).textStyle(.trayTitle).foregroundStyle(Palette.ink)
                    .accessibilityAddTraits(.isHeader)
                if let subtitle {
                    Text(subtitle).textStyle(.rowSub).foregroundStyle(Palette.ink2)
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
                    Text(title).textStyle(.rowTitle)
                    if let hint {
                        Text(hint).textStyle(.rowSub)
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
                    Text(title).textStyle(TypeStyle.rowTitle.sized(17))
                    if let caption {
                        Text(caption).textStyle(TypeStyle.body.sized(13))
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
