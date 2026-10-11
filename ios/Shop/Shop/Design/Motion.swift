import SwiftUI

// Motion tokens. CSS equivalents live in design/motion.html.
//
//   --spring  cubic-bezier(0.32, 1.28, 0.5, 1) ~420ms  -> Motion.spring   (trays, toggles)
//   --ease    cubic-bezier(0.22, 1, 0.36, 1)    240ms  -> Motion.ease     (fades, slides)
//   --press   90ms ease-out                            -> Motion.press
//   tray-to-tray height 360ms spring, content fade 160ms
//   check circle fill 200ms spring, strike after 160ms, collapse 240ms
//   tab slide 12pt, 200ms ease; list stagger 24ms, 8pt, max 8 rows

enum Motion {
    /// Slight overshoot, about 420ms. Trays, toggles.
    static let spring = Animation.spring(response: 0.42, dampingFraction: 0.78)
    /// Used for the tray-to-tray height morph.
    static let heightSpring = Animation.spring(response: 0.36, dampingFraction: 0.82)
    /// Quick pop for the check circle fill and tab icon pop.
    static let pop = Animation.spring(response: 0.2, dampingFraction: 0.7)
    /// Fades and slides, 240ms, ease out (cubic-bezier 0.22, 1, 0.36, 1).
    static let ease = Animation.timingCurve(0.22, 1, 0.36, 1, duration: 0.24)
    static let tabSlide = Animation.timingCurve(0.22, 1, 0.36, 1, duration: 0.2)
    static let fade = Animation.easeOut(duration: 0.16)
    static let trayFade = Animation.easeOut(duration: 0.1)
    /// The sheet curve (iOS sheet / Vaul): fast start, long soft landing, no overshoot.
    /// One curve for the whole tray surface so nothing inside it moves on its own timing.
    static let sheetCurve = (0.32, 0.72, 0.0, 1.0)
    static let sheet = Animation.timingCurve(0.32, 0.72, 0, 1, duration: 0.45)
    static let sheetExit = Animation.timingCurve(0.32, 0.72, 0, 1, duration: 0.35)
    /// Tray-to-tray: height and content swap on the same curve.
    static let sheetSwap = Animation.timingCurve(0.32, 0.72, 0, 1, duration: 0.36)
    static let reducedFade = Animation.easeOut(duration: 0.12)
    static let press = Animation.easeOut(duration: 0.09)
    static let numberRoll = Animation.timingCurve(0.22, 1, 0.36, 1, duration: 0.18)
    static let collapse = Animation.timingCurve(0.22, 1, 0.36, 1, duration: 0.24)

    /// Loading states (see docs/DESIGN-LOADING-STATES.md). Nothing shows for the first 250 ms,
    /// a skeleton that appeared stays at least 400 ms, then it cross-fades to content in 200 ms.
    static let skeletonDelay: Duration = .milliseconds(250)
    static let skeletonMinimum: Duration = .milliseconds(400)
    static let skeletonPulse = Animation.easeInOut(duration: 1.1).repeatForever(autoreverses: true)
    static let stateSwap = Animation.easeOut(duration: 0.2)

    /// The Unpack scan line: one pass 1.4 s, back and forth.
    static let scanSweep = Animation.easeInOut(duration: 1.4).repeatForever(autoreverses: true)

    static let pressScale: CGFloat = 0.97
    static let pillScale: CGFloat = 0.94
    static let staggerStep: Double = 0.024
    static let staggerMax = 8
    static let slideDistance: CGFloat = 12
    static let listOffset: CGFloat = 8
}

// MARK: - Reduce motion

/// Picks `animation` normally, or a short fade when Reduce Motion is on.
private struct MotionAware: ViewModifier {
    @Environment(\.accessibilityReduceMotion) private var reduce
    let animation: Animation
    let value: AnyHashable

    func body(content: Content) -> some View {
        content.animation(reduce ? Motion.reducedFade : animation, value: value)
    }
}

extension View {
    func motion<V: Hashable>(_ animation: Animation = Motion.spring, value: V) -> some View {
        modifier(MotionAware(animation: animation, value: AnyHashable(value)))
    }
}

// MARK: - Press style

/// Every tappable scales to 0.97 in 90ms, springs back. Pills and + use 0.94.
/// Fires its haptic on touch-down so it feels instant. Pass nil for no haptic.
struct PressStyle: ButtonStyle {
    var scale: CGFloat = Motion.pressScale
    var haptic: HapticEvent? = .light
    @Environment(\.accessibilityReduceMotion) private var reduce

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed && !reduce ? scale : 1)
            .animation(configuration.isPressed ? Motion.press : Motion.spring, value: configuration.isPressed)
            .onChange(of: configuration.isPressed) { _, pressed in
                if pressed, let haptic { Haptics.play(haptic) }
            }
    }
}

extension ButtonStyle where Self == PressStyle {
    static var press: PressStyle { PressStyle() }
    static func press(scale: CGFloat = Motion.pressScale, haptic: HapticEvent? = .light) -> PressStyle {
        PressStyle(scale: scale, haptic: haptic)
    }
    static var pill: PressStyle { PressStyle(scale: Motion.pillScale, haptic: .selection) }
}

// MARK: - Tray

extension AnyTransition {
    /// The tray is one surface that slides in from the bottom edge and out the same way: a single
    /// translate, no opacity, no scale. The animation comes from the `withAnimation` that changes
    /// the state (`Motion.sheet` / `Motion.sheetExit`).
    static var tray: AnyTransition { .move(edge: .bottom) }

    /// Content swap inside a tray that is replaced by another one: the old content leaves quickly
    /// and the new content fades in a beat later with a touch of blur (masks the layout change),
    /// while the surface height animates on `Motion.sheetSwap`.
    static var traySwap: AnyTransition {
        .asymmetric(
            insertion: .opacity.animation(.easeOut(duration: 0.16).delay(0.06)),
            removal: .opacity.animation(.easeOut(duration: 0.08))
        )
    }
    static var scrim: AnyTransition { .opacity.animation(Motion.ease) }
}

// MARK: - Label and number morph

extension View {
    /// Button label morph (Continue -> Confirm): shared letters stay put.
    /// Change the string inside `withAnimation(Motion.spring)`.
    func labelMorph() -> some View {
        contentTransition(.interpolate)
    }

    // Odometer digits: use RollingText below (keys the animation to the value).
}

/// Preferred way to roll a number: animation keyed to the value.
struct RollingText: View {
    let text: String
    let value: Double
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        Text(text)
            .monospacedDigit()
            .contentTransition(reduce ? .opacity : .numericText(value: value))
            .animation(reduce ? Motion.reducedFade : Motion.numberRoll, value: value)
    }
}

// MARK: - Staggered list appearance

/// First paint only: rows fade and slide in 8pt, 24ms apart, max 8 rows.
struct StaggeredAppear: ViewModifier {
    let index: Int
    // Rows past the stagger cap start shown: lazy stacks recreate rows as they scroll back
    // into view, and replaying the fade there reads as flicker.
    @State private var shown: Bool
    @Environment(\.accessibilityReduceMotion) private var reduce

    init(index: Int) {
        self.index = index
        _shown = State(initialValue: index >= Motion.staggerMax)
    }

    func body(content: Content) -> some View {
        content
            .opacity(shown ? 1 : 0)
            .offset(y: shown || reduce ? 0 : Motion.listOffset)
            .onAppear {
                guard !shown else { return }
                let step = Double(min(index, Motion.staggerMax - 1))
                withAnimation((reduce ? Motion.reducedFade : Motion.ease).delay(step * Motion.staggerStep)) {
                    shown = true
                }
            }
    }
}

extension View {
    func staggeredAppear(index: Int) -> some View { modifier(StaggeredAppear(index: index)) }
}

// MARK: - Tab slide

/// Direction-aware slide: content moves 12pt toward the tapped tab's side, 200ms ease.
func tabTransition(from old: Int, to new: Int) -> AnyTransition {
    let dir: CGFloat = new > old ? 1 : -1
    return .asymmetric(
        insertion: .offset(x: dir * Motion.slideDistance).combined(with: .opacity),
        removal: .offset(x: -dir * Motion.slideDistance).combined(with: .opacity)
    ).animation(Motion.tabSlide)
}

// MARK: - Shake (wrong password)

/// Use: `.modifier(Shake(trigger: attempts))` and bump `attempts` on failure, fire `.error` haptic.
nonisolated struct Shake: GeometryEffect {
    var travel: CGFloat = 8
    var shakes: CGFloat = 3
    var trigger: CGFloat
    var animatableData: CGFloat {
        get { trigger }
        set { trigger = newValue }
    }
    func effectValue(size: CGSize) -> ProjectionTransform {
        ProjectionTransform(CGAffineTransform(translationX: travel * sin(trigger * .pi * shakes), y: 0))
    }
}

/// Reduce Motion drops the displacement; the caller keeps the red tint and error haptic.
private struct ShakeModifier: ViewModifier {
    let attempts: Int
    @Environment(\.accessibilityReduceMotion) private var reduce

    func body(content: Content) -> some View {
        content
            .modifier(Shake(trigger: reduce ? 0 : CGFloat(attempts)))
            .animation(reduce ? nil : .linear(duration: 0.4), value: attempts)
    }
}

extension View {
    func shake(_ attempts: Int) -> some View {
        modifier(ShakeModifier(attempts: attempts))
    }
}
