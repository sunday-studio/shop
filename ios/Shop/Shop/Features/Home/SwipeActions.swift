import SwiftUI

/// One button revealed by swiping a row.
struct SwipeAction {
    var title: String
    var icon: Icon
    var color: Color
    /// Played when the action runs. nil when the action opens something that plays its own.
    var haptic: HapticEvent? = .light
    var run: () -> Void
}

/// Swipe a list row sideways to reveal an action (right → `leading`, left → `trailing`). The rows
/// live in a plain ScrollView, so this is a small drag gesture that only wins when the move is
/// clearly horizontal. A long swipe runs the action; a short one leaves the button open.
struct SwipeRow<Content: View>: View {
    var leading: SwipeAction? = nil
    var trailing: SwipeAction? = nil
    @ViewBuilder var content: () -> Content

    @State private var offset: CGFloat = 0
    @State private var dragging = false
    /// Stays true a moment after a drag so the row's own tap doesn't fire on release.
    @State private var suppressTap = false
    @State private var pastCommit = false
    @Environment(\.accessibilityReduceMotion) private var reduce
    @Environment(AppModel.self) private var app

    private let reveal: CGFloat = 92
    private let commit: CGFloat = 190

    var body: some View {
        ZStack {
            content()
                .background(Palette.bg)
                .offset(x: offset)
                .allowsHitTesting(!suppressTap && offset == 0)
                .overlay {
                    // While open, a tap on the row itself closes it.
                    if offset != 0 && !dragging { Color.clear.contentShape(Rectangle()).onTapGesture { close() } }
                }
            // The revealed button sits above the row so its tap is never swallowed.
            if let leading, offset > 0 { button(leading, alignment: .leading).zIndex(2) }
            if let trailing, offset < 0 { button(trailing, alignment: .trailing).zIndex(2) }
        }
        .clipped()
        .simultaneousGesture(
            DragGesture(minimumDistance: 18)
                .onChanged { value in
                    let horizontal = abs(value.translation.width) > abs(value.translation.height) * 1.6
                    guard dragging || horizontal else { return }
                    dragging = true
                    suppressTap = true
                    app.rowSwipeActive = true
                    var x = value.translation.width
                    if x > 0 && leading == nil { x = 0 }
                    if x < 0 && trailing == nil { x = 0 }
                    offset = max(-commit - 30, min(commit + 30, x))
                    // One tick when the drag crosses the "it will fire now" line, and back.
                    let past = abs(offset) >= commit
                    if past != pastCommit { pastCommit = past; Haptics.play(.selection) }
                }
                .onEnded { _ in
                    guard dragging else { return }
                    dragging = false
                    pastCommit = false
                    Task {
                        try? await Task.sleep(for: .milliseconds(250))
                        suppressTap = false
                        app.rowSwipeActive = false
                    }
                    if offset >= commit, let leading { fire(leading) }
                    else if offset <= -commit, let trailing { fire(trailing) }
                    else if offset > reveal / 2 { settle(reveal) }
                    else if offset < -reveal / 2 { settle(-reveal) }
                    else { close() }
                }
        )
    }

    private func button(_ action: SwipeAction, alignment: Alignment) -> some View {
        Button {
            fire(action)
        } label: {
            VStack(spacing: 4) {
                IconView(action.icon, size: 18, weight: .semibold)
                Text(action.title).textStyle(.captionMedium)
            }
            .foregroundStyle(.white)
            .frame(width: max(reveal, abs(offset)), alignment: alignment == .leading ? .leading : .trailing)
            .padding(alignment == .leading ? .leading : .trailing, 26)
            .frame(maxHeight: .infinity)
            .background(action.color)
        }
        .buttonStyle(.press(haptic: nil))
        .frame(maxWidth: .infinity, alignment: alignment)
    }

    private func fire(_ action: SwipeAction) {
        if let haptic = action.haptic { Haptics.play(haptic) }
        close()
        action.run()
    }

    private func settle(_ value: CGFloat) {
        withAnimation(reduce ? Motion.reducedFade : Motion.spring) { offset = value }
    }

    private func close() {
        withAnimation(reduce ? Motion.reducedFade : Motion.spring) { offset = 0 }
    }
}
