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
        .onAppear { SwipeTick.reveal() }
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
    func shopListStyle() -> some View {
        listStyle(.plain)
            .scrollContentBackground(.hidden)
            .scrollIndicators(.hidden)
            .environment(\.defaultMinListRowHeight, 1)
    }
}

/// The system plays no haptic while a row is swiped. The action buttons are only created when
/// a swipe opens them, so their appearance is the "revealed" moment: one light tick for the
/// whole group (not one per button). A drag gesture on the row would break its tap, so this
/// doesn't read the touch at all.
@MainActor
enum SwipeTick {
    private static var last = Date.distantPast

    static func reveal() {
        let now = Date()
        guard now.timeIntervalSince(last) > 0.4 else { return }
        last = now
        Haptics.play(.selection)
    }
}
