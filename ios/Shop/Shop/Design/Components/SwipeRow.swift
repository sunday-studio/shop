import SwiftUI

// Pull-to-act rows (docs/DESIGN-SWIPE-ACTIONS.md, second version, after device testing):
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
    @State private var locked: Bool?   // nil undecided, true horizontal, false vertical
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
        .clipped()
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width.value = $0 }
        .simultaneousGesture(drag)
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

    // MARK: Gesture

    private var drag: some Gesture {
        DragGesture(minimumDistance: 12)
            .onChanged { value in
                if locked == nil {
                    let h = abs(value.translation.width), v = abs(value.translation.height)
                    guard h > 12 || v > 12 else { return }
                    locked = h > v * 1.6 && value.startLocation.x > edgeGuard
                }
                guard locked == true else { return }
                var x = value.translation.width
                if (x > 0 && leading == nil) || (x < 0 && trailing == nil) { x = rubber(x, limit: 24) }
                else { x = stretch(x) }
                offset = x
                let nowArmed = abs(x) >= armDistance && active != nil
                if nowArmed != armed {
                    armed = nowArmed
                    if nowArmed { Haptics.play(.selection) }
                }
            }
            .onEnded { value in
                defer { locked = nil }
                guard locked == true, let action = active else { snapBack(); return }
                let v = value.velocity.width
                let flick = (offset > 0 ? v : -v) > flickVelocity && abs(offset) > width.value * 0.12
                if armed || flick { commit(action) } else { snapBack() }
            }
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
