import SwiftUI

/// Swipe actions use the system `List` + `.swipeActions` (docs/DESIGN-SWIPE-ACTIONS.md): icon
/// only, at most two actions per side, only the leading (safe) action may full-swipe, and Delete
/// never full-swipes: it opens the existing confirmation tray.
///
/// Haptics: the system plays the threshold tick. An action plays only its own haptic, the same
/// one its button or tap equivalent plays (nil when it opens a tray, which plays `trayOpen`).
/// Nothing removes data instantly: a removal opens a confirmation tray or shows an Undo toast.

/// One icon-only swipe button. The label keeps its title for VoiceOver.
struct SwipeButton: View {
    let title: String
    let systemImage: String
    let tint: Color
    var haptic: HapticEvent? = nil
    let run: () -> Void

    var body: some View {
        Button {
            if let haptic { Haptics.play(haptic) }
            run()
        } label: {
            Label(title, systemImage: systemImage).labelStyle(.iconOnly)
        }
        .tint(tint)
    }
}

extension View {
    /// Row chrome for rows that live in a plain full-width `List`.
    func shopListRow() -> some View {
        listRowInsets(EdgeInsets())
            .listRowSeparator(.hidden)
            .listRowBackground(Color.clear)
    }

    /// A `List` that looks like the surrounding scroll view.
    ///
    /// `leading` / `trailing`: how many swipe actions the rows on that side have, so the swipe
    /// haptic can fire when all of them are showing.
    func shopListStyle(leading: Int = 0, trailing: Int = 0) -> some View {
        background(SwipeHapticHost(leading: leading, trailing: trailing).allowsHitTesting(false))
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .scrollIndicators(.hidden)
            .environment(\.defaultMinListRowHeight, 1)
    }
}

/// The system plays no haptic while a row is swiped. This watches the list's touches (a pan
/// recognizer that never cancels or blocks anything, so taps and the system swipe are
/// untouched) and plays one `selection` tick when the finger has pulled the row far enough
/// that every action on that side is showing. Once per drag.
private struct SwipeHapticHost: UIViewRepresentable {
    let leading: Int
    let trailing: Int

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> UIView {
        let view = HostView()
        view.coordinator = context.coordinator
        return view
    }

    func updateUIView(_ view: UIView, context: Context) {
        context.coordinator.leading = leading
        context.coordinator.trailing = trailing
    }

    final class HostView: UIView {
        weak var coordinator: Coordinator?
        override func didMoveToWindow() {
            super.didMoveToWindow()
            guard window != nil else { return }
            // The list's scroll view is a sibling of this background view.
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                // Walk up until an ancestor contains the list's scroll view.
                var ancestor = self.superview
                while let current = ancestor {
                    if let scroll = Self.firstScrollView(in: current, excluding: self) {
                        self.coordinator?.attach(to: scroll)
                        return
                    }
                    ancestor = current.superview
                }
            }
        }
        static func firstScrollView(in view: UIView, excluding host: UIView) -> UIScrollView? {
            if let scroll = view as? UIScrollView { return scroll }
            for sub in view.subviews where sub !== host {
                if let found = firstScrollView(in: sub, excluding: host) { return found }
            }
            return nil
        }
    }

    @MainActor
    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var leading = 0
        var trailing = 0
        private weak var scroll: UIScrollView?
        private var pan: UIPanGestureRecognizer?
        private var fired = false
        /// Pull at which n icon-only action capsules are showing (just before fully open).
        private func width(for count: Int) -> CGFloat { CGFloat(count) * 56 }

        func attach(to scrollView: UIScrollView) {
            guard scroll !== scrollView else { return }
            if let pan, let old = scroll { old.removeGestureRecognizer(pan) }
            let recognizer = UIPanGestureRecognizer(target: self, action: #selector(handle(_:)))
            recognizer.cancelsTouchesInView = false
            recognizer.delaysTouchesBegan = false
            recognizer.delaysTouchesEnded = false
            recognizer.delegate = self
            scrollView.addGestureRecognizer(recognizer)
            scroll = scrollView
            pan = recognizer
        }

        func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool { true }

        @objc private func handle(_ g: UIPanGestureRecognizer) {
            switch g.state {
            case .began: fired = false
            case .changed:
                guard !fired, let view = g.view else { return }
                let t = g.translation(in: view)
                guard abs(t.x) > abs(t.y) * 1.5 else { return }
                let count = t.x > 0 ? leading : trailing
                guard count > 0, abs(t.x) >= width(for: count) else { return }
                fired = true
                Haptics.play(.selection)
            default: break
            }
        }
    }
}
