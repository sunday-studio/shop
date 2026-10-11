import SwiftUI

/// The round check used by every pick-list row (link candidates, unpack and import choices):
/// outlined when off, accent-filled with a check when on.
struct SelectionCircle: View {
    let isOn: Bool

    var body: some View {
        ZStack {
            Circle().strokeBorder(Palette.ink3, lineWidth: 1.5).opacity(isOn ? 0 : 1)
            Circle().fill(Accent.blue).opacity(isOn ? 1 : 0)
            IconView(.check, size: 10, weight: .heavy).foregroundStyle(Accent.white).opacity(isOn ? 1 : 0)
        }
        .frame(width: 22, height: 22)
        .animation(Motion.pop, value: isOn)
    }
}
