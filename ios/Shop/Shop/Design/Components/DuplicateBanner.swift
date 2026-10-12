import SwiftUI

/// Receipt screen, newer receipt only: "Looks like the receipt from 2 Aug (€84,68)".
/// Never acts on its own: the user removes this one or keeps both. Spec: notes/design-duplicate-receipt.md.
struct DuplicateReceiptBanner: View {
    enum Strength {
        /// Same shop, day and total. Could be a second trip, so the wording stays open.
        case totalAndDate
        /// Same shop, day, total and items. Almost certainly the same receipt.
        case totalDateAndLines
    }

    /// "2 Aug" (add the year when it is not this year). The caller formats it.
    let otherDate: String
    /// "€84,68"
    let otherTotal: String
    var strength: Strength = .totalAndDate
    var isBusy: Bool = false
    let onCompare: () -> Void
    let onRemoveThis: () -> Void
    let onKeepBoth: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Space.ml) {
            Button {
                onCompare()
            } label: {
                HStack(alignment: .top, spacing: Space.ml) {
                    GlyphBadge(glyph: .alert, color: Accent.amber, size: 20)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(title).textStyle(.rowTitle).multilineTextAlignment(.leading)
                        TypeStyle.body.sized(13).text(detail)
                            .foregroundStyle(Palette.amberText.opacity(0.85))
                            .multilineTextAlignment(.leading)
                    }
                    Spacer(minLength: 0)
                    IconView(.forward, size: 14, weight: .bold).padding(.top, 3)
                }
                .foregroundStyle(Palette.amberText)
                .contentShape(Rectangle())
            }
            .buttonStyle(.press(haptic: .light))
            .accessibilityHint("Compare the two receipts")

            VStack(spacing: Space.ml) {
                Button("Same receipt, remove this one", action: onRemoveThis)
                    .buttonStyle(.shop(strength == .totalDateAndLines ? .primary : .secondary, size: .regular, fullWidth: true))
                Button("Keep both", action: onKeepBoth)
                    .buttonStyle(.shop(.secondary, size: .regular, fullWidth: true))
            }
            .disabled(isBusy)
        }
        .padding(Space.lx)
        .background(Palette.amberTint, in: RoundedRectangle.continuous(Radius.xl))
        .accessibilityElement(children: .contain)
    }

    private var title: String { "Looks like the receipt from \(otherDate) (\(otherTotal))" }

    private var detail: String {
        switch strength {
        case .totalAndDate: "Same shop, day and total. It could be a second trip."
        case .totalDateAndLines: "Same shop, day, total and items."
        }
    }
}
