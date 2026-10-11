import SwiftUI

// Pull-to-act rows (replaced the native swipe actions after device testing):
// one action per side, a full-bleed colour panel that grows with the finger, the icon pinned near
// the revealed edge, the panel going from a soft tint to the full colour when the row is "armed"
// (selection tick), and letting go while armed commits. Nothing stays open.

struct SwipeAction {
    /// SF Symbol name.
    let icon: String
    /// VoiceOver name.
    let title: String
    let tint: Color
    /// True when the row leaves the list (the row slides fully off before `perform` runs). False
    /// for actions that open a tray or only change state: the row springs back and `perform` runs.
    var removesRow = false
    /// The action's own haptic on commit (nil when it opens a tray, which plays `trayOpen`).
    var haptic: HapticEvent? = nil
    let perform: () -> Void
}

struct SwipeActionRow<Content: View>: View {
    var leading: SwipeAction? = nil
    var trailing: SwipeAction? = nil
    @ViewBuilder var content: () -> Content

    @Environment(\.accessibilityReduceMotion) private var reduce
    @State private var offset: CGFloat = 0
    @State private var armed = false
    @State private var width = WidthBox()

    private let armFraction: CGFloat = 0.28
    private let minArm: CGFloat = 96
    private let flickVelocity: CGFloat = 650
    private let edgeGuard: CGFloat = 20   // leave the system back swipe alone

    private final class WidthBox { var value: CGFloat = 390 }

    private var armDistance: CGFloat { max(minArm, width.value * armFraction) }
    private var active: SwipeAction? { offset > 0 ? leading : (offset < 0 ? trailing : nil) }

    var body: some View {
        ZStack {
            panel
            content()
                .background(Palette.bg)
                .offset(x: offset)
        }
        .contentShape(Rectangle())
        .clipped()
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width.value = $0 }
        .background(SwipePan(edgeGuard: edgeGuard,
                             onChange: { change($0) },
                             onEnd: { end(dx: $0, vx: $1) },
                             onCancel: { snapBack() }))
        .accessibilityElement(children: .contain)
        .accessibilityActions {
            if let leading { Button(leading.title) { leading.perform() } }
            if let trailing { Button(trailing.title) { trailing.perform() } }
        }
    }

    // MARK: Panel

    @ViewBuilder private var panel: some View {
        if let action = active {
            let isLeading = offset > 0
            let shown = abs(offset)
            ZStack(alignment: isLeading ? .leading : .trailing) {
                (armed ? action.tint : action.tint.opacity(0.55))
                Image(systemName: action.icon)
                    .font(.system(size: 22, weight: .semibold))
                    .foregroundStyle(.white)
                    .scaleEffect(armed ? 1.12 : 0.9)
                    .opacity(min(1, max(0, (shown - 12) / 36)))
                    .frame(width: 56)
                    .padding(isLeading ? .leading : .trailing, 8)
            }
            .animation(Motion.pop, value: armed)
            .accessibilityHidden(true)
        }
    }

    // MARK: Gesture (a UIKit pan, see SwipePan: it only begins for a horizontal move and cancels
    // the Button inside the row, so a swipe never also taps)

    private func change(_ dx: CGFloat) {
        var x = dx
        if (x > 0 && leading == nil) || (x < 0 && trailing == nil) { x = rubber(x, limit: 24) }
        else { x = stretch(x) }
        offset = x
        let nowArmed = abs(x) >= armDistance && active != nil
        if nowArmed != armed {
            armed = nowArmed
            if nowArmed { Haptics.play(.selection) }
        }
    }

    private func end(dx: CGFloat, vx: CGFloat) {
        guard let action = active else { snapBack(); return }
        let flick = (offset > 0 ? vx : -vx) > flickVelocity && abs(offset) > width.value * 0.12
        if armed || flick { commit(action) } else { snapBack() }
    }

    private func commit(_ action: SwipeAction) {
        if let h = action.haptic { Haptics.play(h) }
        if action.removesRow {
            let dir: CGFloat = offset > 0 ? 1 : -1
            withAnimation(reduce ? Motion.reducedFade : .easeOut(duration: 0.2)) { offset = dir * (width.value + 24) }
            Task { @MainActor in
                try? await Task.sleep(for: .milliseconds(200))
                action.perform()
            }
        } else {
            action.perform()
            snapBack()
        }
    }

    private func snapBack() {
        armed = false
        withAnimation(reduce ? Motion.reducedFade : .spring(response: 0.34, dampingFraction: 0.82)) { offset = 0 }
    }

    /// 1:1 up to the arm point, then it gets heavier, so overshooting costs effort.
    private func stretch(_ x: CGFloat) -> CGFloat {
        let sign: CGFloat = x < 0 ? -1 : 1
        let a = abs(x)
        let knee = armDistance * 1.6
        guard a > knee else { return x }
        return sign * (knee + (a - knee) * 0.35)
    }

    private func rubber(_ x: CGFloat, limit: CGFloat) -> CGFloat {
        let sign: CGFloat = x < 0 ? -1 : 1
        let a = abs(x)
        return sign * limit * (1 - 1 / (a / limit + 1))
    }
}


/// A pan recognizer attached to the row's hosting view. It only begins when the move is clearly
/// horizontal and starts inside the row (and not at the screen's left edge), so list scrolling and
/// the system back swipe are untouched. It does not run alongside the other recognizers, so when
/// it begins the Button inside the row is cancelled and the touch-up never counts as a tap.
private struct SwipePan: UIViewRepresentable {
    var edgeGuard: CGFloat
    var onChange: (CGFloat) -> Void
    var onEnd: (CGFloat, CGFloat) -> Void
    var onCancel: () -> Void

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> Anchor {
        let v = Anchor()
        v.isUserInteractionEnabled = false
        v.coordinator = context.coordinator
        return v
    }

    func updateUIView(_ view: Anchor, context: Context) {
        let c = context.coordinator
        c.edgeGuard = edgeGuard
        c.onChange = onChange
        c.onEnd = onEnd
        c.onCancel = onCancel
    }

    final class Anchor: UIView {
        weak var coordinator: Coordinator?
        override func didMoveToWindow() {
            super.didMoveToWindow()
            guard window != nil else { coordinator?.detach(); return }
            var host: UIView? = superview
            var v: UIView? = superview
            while let s = v {
                if String(describing: type(of: s)).contains("HostingView") { host = s; break }
                v = s.superview
            }
            if let host { coordinator?.attach(to: host, anchor: self) }
        }
    }

    @MainActor
    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var edgeGuard: CGFloat = 20
        var onChange: (CGFloat) -> Void = { _ in }
        var onEnd: (CGFloat, CGFloat) -> Void = { _, _ in }
        var onCancel: () -> Void = {}
        private weak var anchor: UIView?
        private var pan: UIPanGestureRecognizer?

        func attach(to host: UIView, anchor: UIView) {
            detach()
            self.anchor = anchor
            let p = UIPanGestureRecognizer(target: self, action: #selector(handle(_:)))
            p.delegate = self
            host.addGestureRecognizer(p)
            pan = p
        }

        func detach() {
            if let pan { pan.view?.removeGestureRecognizer(pan) }
            pan = nil
        }

        @objc private func handle(_ g: UIPanGestureRecognizer) {
            let dx = g.translation(in: g.view).x
            switch g.state {
            case .began, .changed: onChange(dx)
            case .ended: onEnd(dx, g.velocity(in: g.view).x)
            case .cancelled, .failed: onCancel()
            default: break
            }
        }

        func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
            guard let pan = g as? UIPanGestureRecognizer, let anchor else { return false }
            let v = pan.velocity(in: pan.view)
            guard abs(v.x) > abs(v.y) * 1.6 else { return false }
            let p = pan.location(in: anchor)
            return anchor.bounds.contains(p) && p.x - abs(pan.translation(in: pan.view).x) > edgeGuard - 1
        }

        func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool { false }
    }
}
