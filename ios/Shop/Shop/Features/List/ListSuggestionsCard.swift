import SwiftUI

/// "Looks bought?": list items a confirmed receipt probably covers, each with Yes / Not this.
/// Never checked automatically; shown only when there are suggestions, so nothing shifts while loading.
struct ListSuggestionsCard: View {
    @Environment(AppModel.self) private var app
    @State private var showAll = false

    private var suggestions: [ListSuggestion] { app.list.suggestions }
    private var shown: [ListSuggestion] { showAll ? suggestions : Array(suggestions.prefix(3)) }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.m) {
            HStack(spacing: Space.s) {
                TypeStyle.label.text("Looks bought?").foregroundStyle(Palette.ink2)
                TypeStyle.captionMedium.text("\(suggestions.count)")
                    .foregroundStyle(Palette.blueText)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(Palette.blueTint, in: Capsule())
                Spacer()
            }
            VStack(spacing: 0) {
                ForEach(shown) { suggestion in
                    row(suggestion)
                }
            }
            if suggestions.count > 3 {
                Button(showAll ? "Show fewer" : "Show \(suggestions.count - 3) more") {
                    withAnimation(Motion.collapse) { showAll.toggle() }
                }
                .textStyle(.label)
                .foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: .selection))
            }
        }
        .padding(Space.lx)
        .background(Palette.sunk, in: RoundedRectangle.continuous(22))
        .animation(Motion.collapse, value: suggestions.map(\.id))
    }

    private func row(_ s: ListSuggestion) -> some View {
        let detail = detailText(s)
        return HStack(spacing: Space.m) {
            VStack(alignment: .leading, spacing: 2) {
                TypeStyle.rowTitle.text(s.itemText).foregroundStyle(Palette.ink).lineLimit(1)
                TypeStyle.rowSub.text(detail).foregroundStyle(Palette.ink2).lineLimit(2)
            }
            Spacer(minLength: Space.s)
            Button {
                Haptics.play(.selection)
                withAnimation(Motion.collapse) { app.list.dismiss(s) }
                Task { await app.list.sync() }
            } label: {
                IconView(.close, size: 13, weight: .bold)
                    .foregroundStyle(Palette.ink2)
                    .frame(width: 36, height: 36)
                    .background(Palette.bg, in: Circle())
            }
            .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
            .accessibilityLabel("Not this")
            Button {
                Haptics.play(.light)
                withAnimation(Motion.collapse) { app.list.accept(s) }
                Task { await app.list.sync() }
            } label: {
                TypeStyle.label.sized(14).text("Yes")
                    .foregroundStyle(Palette.btnInk)
                    .padding(.horizontal, Space.l)
                    .frame(height: 36)
                    .background(Palette.btn, in: Capsule())
            }
            .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
            .accessibilityLabel("Yes, bought")
        }
        .frame(minHeight: 64)
        .accessibilityElement(children: .combine)
        .accessibilityAction(named: "Yes") { app.list.accept(s); Task { await app.list.sync() } }
        .accessibilityAction(named: "Not this") { app.list.dismiss(s); Task { await app.list.sync() } }
    }

    /// `Maybe "AFWASMID CITRUS 500ML" · Corner Market · Tue`
    private func detailText(_ s: ListSuggestion) -> String {
        var parts = ["Maybe “\(s.lineText)”"]
        if let receipt = app.home.receipts.first(where: { $0.id == s.receiptId }) {
            if let shop = receipt.shopId { parts.append(app.catalog.shopName(shop)) }
            parts.append(Dates.shortDay(receipt.displayDate))
        }
        return parts.joined(separator: " · ")
    }
}
