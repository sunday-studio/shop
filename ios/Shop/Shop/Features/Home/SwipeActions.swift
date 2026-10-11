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
    func shopListStyle() -> some View {
        listStyle(.plain)
            .scrollContentBackground(.hidden)
            .scrollIndicators(.hidden)
            .environment(\.defaultMinListRowHeight, 1)
    }
}
