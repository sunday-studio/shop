import SwiftUI

/// Cheapest store: the "By store" view of the list (summary card, store sections), the per-item
/// Why sheet and the "How we work this out" sheet. Wording rule: never a bare number and never
/// "you will save": figures are "about", with the count of items they rest on.

// MARK: - Wording

enum PlanText {
    static func confidenceTitle(_ code: String) -> String {
        switch code {
        case "seen_once": "Seen once"
        case "old_price": "Old price"
        case "different_pack": "Different pack size"
        default: "Not enough data"
        }
    }

    static func confidenceExplanation(_ code: String) -> String {
        switch code {
        case "seen_once": "The price rests on one purchase, so it may not be typical."
        case "old_price": "The latest price is old. It may have changed."
        case "different_pack": "The pack size is unknown or differs, so the comparison is per piece."
        default: "There aren't enough receipts to compare this item."
        }
    }

    /// "Tue" style day for "Plan is from Tuesday".
    static func weekday(_ date: Date) -> String { Dates.dayLabel(date) }
}

struct ConfidenceChip: View {
    let code: String
    var body: some View {
        TypeStyle.captionMedium.sized(12.5).text(PlanText.confidenceTitle(code))
            .foregroundStyle(Palette.amberText)
            .padding(.horizontal, 9).padding(.vertical, 3)
            .background(Palette.amberTint, in: Capsule())
    }
}

// MARK: - Summary card

struct PlanSummaryCard: View {
    let plan: ListPlan
    let loading: Bool
    let onHow: () -> Void
    let onChangeStop: () -> Void

    private var s: PlanSummary { plan.summary }
    private var splits: Bool { plan.status == "split" }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.s) {
            TypeStyle.rowTitle.sized(17).text(title).foregroundStyle(Palette.ink)
            TypeStyle.rowSub.text(detail).foregroundStyle(Palette.ink2).fixedSize(horizontal: false, vertical: true)
            if splits && s.extraStopCents > 0 {
                HStack(spacing: Space.s) {
                    TypeStyle.rowSub.text("Includes \(Money.format(s.extraStopTotalCents)) for the extra stop").foregroundStyle(Palette.ink2)
                    Button("Change", action: onChangeStop)
                        .textStyle(TypeStyle.label.sized(13.5)).foregroundStyle(Palette.blueText)
                        .buttonStyle(.press(haptic: .selection))
                }
            }
            Button("How we work this out", action: onHow)
                .textStyle(TypeStyle.label.sized(13.5)).foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: .selection))
                .padding(.top, 2)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(Space.lx)
        .background(splits ? Palette.greenTint : Palette.sunk, in: RoundedRectangle.continuous(Radius.art))
        .opacity(loading ? 0.6 : 1)
        .animation(Motion.stateSwap, value: loading)
    }

    private var title: String {
        switch plan.status {
        case "split": "About \(Money.format(s.savingCents)) cheaper than one shop"
        case "one_shop_fine": "One shop is fine"
        default: "Not enough prices yet"
        }
    }

    private var detail: String {
        switch plan.status {
        case "split":
            return "Based on \(s.comparedCount) of \(s.itemCount) items we have prices for." + (s.noPriceCount > 0 ? " \(s.noPriceCount) have no price yet." : "")
        case "one_shop_fine":
            return "Splitting would save about \(Money.format(s.splitSavingCents)), less than the \(Money.format(s.extraStopCents)) an extra stop costs."
        default:
            return "Plans need prices for at least two items at two shops. Scan more receipts and this fills in."
        }
    }
}

// MARK: - Store header

struct StoreSectionHeader: View {
    let store: PlanStore
    var body: some View {
        HStack(spacing: Space.l) {
            ShopMark(name: store.name, size: Size.shopMarkHeader)
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.rowTitle.sized(17).text(store.name).foregroundStyle(Palette.ink)
                let about = store.estimateCents > 0 ? " · about \(Money.whole(store.estimateCents))" : ""
                TypeStyle.rowSub.text("\(store.itemCount) item\(store.itemCount == 1 ? "" : "s")\(about)").foregroundStyle(Palette.ink2)
            }
            Spacer(minLength: 0)
            TypeStyle.captionMedium.sized(11.5).text(store.isUsual ? "Usual shop" : "Extra stop")
                .foregroundStyle(store.isUsual ? Palette.ink2 : Palette.amberText)
                .padding(.horizontal, 8).padding(.vertical, 3)
                .background(store.isUsual ? Palette.sunk : Palette.amberTint, in: Capsule())
        }
        .padding(.top, Space.l).padding(.bottom, Space.s)
        .frame(minHeight: 56)
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Why sheet

struct PlanWhyTray: View {
    let item: PlanItem
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var open: String?
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: item.text, subtitle: item.qty == 1 ? nil : "× \(Quantity.decimal(item.qty))")
            if item.prices.isEmpty {
                TypeStyle.body.sized(14.5).text(item.explanation).foregroundStyle(Palette.ink2)
                    .fixedSize(horizontal: false, vertical: true).padding(.horizontal, Space.xs)
            } else {
                VStack(spacing: 6) {
                    ForEach(Array(item.prices.enumerated()), id: \.element.id) { index, price in
                        HStack {
                            TypeStyle.rowTitle.text(price.shopName + (price.isUsual ? " (usual)" : "")).foregroundStyle(Palette.ink)
                            Spacer()
                            TypeStyle.rowTitle.text(Money.perUnit(price.unitCents, unit: price.baseUnit))
                                .foregroundStyle(index == 0 && item.prices.count > 1 ? Palette.greenText : Palette.ink)
                        }
                        .padding(.horizontal, Space.lx)
                        .frame(minHeight: 48)
                        .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
                    }
                }
                TypeStyle.rowSub.text(item.explanation).foregroundStyle(Palette.ink2).fixedSize(horizontal: false, vertical: true).padding(.horizontal, Space.xs)
            }
            if !item.confidence.isEmpty {
                FlowLayout(spacing: 6, lineSpacing: 6) {
                    ForEach(item.confidence, id: \.self) { code in
                        Button {
                            Haptics.play(.selection)
                            withAnimation(Motion.sheetSwap) { open = open == code ? nil : code }
                        } label: { ConfidenceChip(code: code) }
                        .buttonStyle(.press(haptic: nil))
                    }
                }
                if let open {
                    TypeStyle.rowSub.text(PlanText.confidenceExplanation(open)).foregroundStyle(Palette.ink2)
                        .fixedSize(horizontal: false, vertical: true).padding(.horizontal, Space.xs)
                }
            }
            if let last = item.lastSeen {
                TypeStyle.rowSub.text("Last seen \(Dates.dayLabel(last))").foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
            }
            if let pair = item.pairConfirmation {
                VStack(alignment: .leading, spacing: Space.m) {
                    TypeStyle.rowTitle.text("Is \(pair.nameB) at \(pair.shopB) the same kind as \(pair.nameA) at \(pair.shopA)?").foregroundStyle(Palette.ink)
                        .fixedSize(horizontal: false, vertical: true)
                    HStack(spacing: Space.m) {
                        Button { Task { await answer(pair, same: true) } } label: { LoadingLabel(title: "Yes, same", isLoading: working) }
                            .buttonStyle(.shop(size: .small, fullWidth: true))
                        Button("No") { Task { await answer(pair, same: false) } }
                            .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    }
                    .disabled(working)
                }
                .padding(Space.lx)
                .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            }
        }
    }

    private func answer(_ pair: PairConfirmation, same: Bool) async {
        working = true
        defer { working = false }
        do {
            if same { _ = try await app.api.confirmSameKind([pair.productA, pair.productB]) } else { _ = try await app.api.denySameKind(pair.productA, pair.productB) }
            trays.dismiss()
            await app.list.refreshPlan()
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't save that. Try again.")
        }
    }
}

// MARK: - How we work this out

struct PlanHowTray: View {
    @Environment(AppModel.self) private var app
    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "How we work this out")
            VStack(alignment: .leading, spacing: Space.m) {
                line("We compare prices per litre, kilo or piece, using the median of your last 3 purchases.")
                line("We only use shops you bought from in the last 90 days (30 for fresh food).")
                line("A saving counts only items priced at both shops.")
                line("Each extra stop costs \(Money.format(Int64(app.list.planSettings.extraStopCents))). You can change that in Settings.")
                line("Items without a price or pack size are left out, so the figure is only an estimate.")
            }
            TypeStyle.rowSub.text("Prices are from your receipts. Promotions can change them.").foregroundStyle(Palette.ink2)
        }
    }

    private func line(_ text: String) -> some View {
        HStack(alignment: .top, spacing: Space.m) {
            Circle().fill(Palette.ink3).frame(width: 5, height: 5).padding(.top, 8)
            TypeStyle.body.sized(14.5).text(text).foregroundStyle(Palette.ink).fixedSize(horizontal: false, vertical: true)
        }
    }
}

// MARK: - Same-kind cards in the review queue

struct SameKindCard: View {
    let suggestion: ProductGroupSuggestion
    let onYes: () -> Void
    let onNo: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Space.m) {
            TypeStyle.rowTitle.text("Same kind of item?").foregroundStyle(Palette.ink)
            VStack(alignment: .leading, spacing: 2) {
                side(suggestion.a)
                side(suggestion.b)
            }
            HStack(spacing: Space.m) {
                Button("Yes, same", action: onYes).buttonStyle(.shop(size: .small, fullWidth: true))
                Button("No", action: onNo).buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
            }
        }
        .padding(Space.lx)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.art))
    }

    private func side(_ p: SuggestedProduct) -> some View {
        TypeStyle.rowSub.text("\(p.nameEn ?? p.name) · \(p.shops.map(\.name).joined(separator: ", "))").foregroundStyle(Palette.ink2).lineLimit(2)
    }
}
