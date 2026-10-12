import SwiftUI

// Possible duplicate receipts (notes/design-duplicate-receipt.md; backend openapi 0.17.0).
// The server flags the NEWER receipt. The user removes it (explicit delete, cannot be undone)
// or keeps both (remembered by the server, undoable).

// MARK: - Text

nonisolated enum DuplicateText {
    nonisolated(unsafe) private static let dayF: DateFormatter = make("d MMM")
    nonisolated(unsafe) private static let dayYearF: DateFormatter = make("d MMM yyyy")

    private static func make(_ format: String) -> DateFormatter {
        let f = DateFormatter()
        f.calendar = Dates.calendar
        f.timeZone = Dates.timeZone
        f.locale = Locale(identifier: "en_GB")
        f.dateFormat = format
        return f
    }

    /// "2 Aug", with the year when it is not this year: "2 Aug 2025".
    static func date(_ date: Date?, now: Date = .now) -> String {
        guard let date else { return "earlier" }
        let sameYear = Dates.calendar.component(.year, from: date) == Dates.calendar.component(.year, from: now)
        return (sameYear ? dayF : dayYearF).string(from: date)
    }
}

// MARK: - Line comparison

nonisolated struct DuplicateRow: Identifiable, Hashable {
    enum Side { case both, thisOnly, otherOnly }
    let id: Int
    let side: Side
    let text: String
    let cents: Int64
}

nonisolated enum DuplicateCompare {
    /// Aligns the item lines of both receipts by name (case and spacing ignored; a name that appears
    /// twice matches twice). Matched lines come first, in the order of this receipt, then the lines
    /// that exist on one side only.
    static func rows(this: [ReceiptLine], other: [ReceiptLine]) -> [DuplicateRow] {
        func key(_ l: ReceiptLine) -> String {
            l.rawText.lowercased().split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
        }
        let mine = this.filter { $0.kind == .item }.sorted { $0.lineNo < $1.lineNo }
        var theirs = other.filter { $0.kind == .item }.sorted { $0.lineNo < $1.lineNo }
        var rows: [DuplicateRow] = []
        var onlyThis: [ReceiptLine] = []
        var n = 0
        for line in mine {
            if let i = theirs.firstIndex(where: { key($0) == key(line) }) {
                theirs.remove(at: i)
                rows.append(DuplicateRow(id: n, side: .both, text: line.rawText, cents: line.lineTotalCents))
                n += 1
            } else {
                onlyThis.append(line)
            }
        }
        for line in onlyThis { rows.append(DuplicateRow(id: n, side: .thisOnly, text: line.rawText, cents: line.lineTotalCents)); n += 1 }
        for line in theirs { rows.append(DuplicateRow(id: n, side: .otherOnly, text: line.rawText, cents: line.lineTotalCents)); n += 1 }
        return rows
    }

    static func differing(_ rows: [DuplicateRow]) -> Int { rows.filter { $0.side != .both }.count }
}

// MARK: - Remembered decisions (works offline)

/// "Keep both" and its Undo are applied on screen straight away and sent when there is a
/// connection. Only the last choice per receipt is kept.
@MainActor
@Observable
final class DuplicateDecisions {
    private static let file = "duplicate-decisions.json"
    /// receipt id → true (keep both) or false (undo), not yet confirmed by the server.
    private(set) var pending: [String: Bool]
    @ObservationIgnored private var flushing = false

    init() {
        pending = DiskStore.load([String: Bool].self, from: Self.file) ?? [:]
    }

    func isKept(_ id: Int64) -> Bool { pending[String(id)] == true }

    func set(_ id: Int64, keep: Bool, api: APIClient) {
        pending[String(id)] = keep
        DiskStore.save(pending, to: Self.file)
        Task { await flush(api: api) }
    }

    func flush(api: APIClient) async {
        guard !flushing, !pending.isEmpty else { return }
        flushing = true
        defer { flushing = false }
        for (key, keep) in pending {
            guard let id = Int64(key) else { pending[key] = nil; continue }
            do {
                if keep { try await api.keepDuplicate(id) } else { try await api.undoKeepDuplicate(id) }
                if pending[key] == keep { pending[key] = nil }
            } catch let e as APIError where !e.isConnectivity {
                // 404: the receipt is gone, nothing to remember.
                pending[key] = nil
            } catch {}
        }
        DiskStore.save(pending, to: Self.file)
    }
}

// MARK: - Model

extension ReceiptModel {
    /// The banner's content: hidden while a "Keep both" waits to be sent.
    var duplicate: PossibleDuplicate? {
        guard let d = receipt?.possibleDuplicateOf, receipt?.status == .ok || receipt?.status == .needsReview else { return nil }
        return app.duplicates.isKept(receiptId) ? nil : d
    }

    func keepBoth() {
        guard let d = duplicate else { return }
        app.duplicates.set(receiptId, keep: true, api: api)
        setDuplicateLocally(nil)
        Haptics.play(.light)
        app.toasts.show("Keeping both", duration: .seconds(6), action: (title: "Undo", run: { [weak self] in
            guard let self else { return }
            Haptics.play(.soft)
            self.restoreDuplicate(d)
        }))
    }

    private func restoreDuplicate(_ d: PossibleDuplicate) {
        app.duplicates.set(receiptId, keep: false, api: api)
        setDuplicateLocally(d)
    }

    func askRemoveDuplicate(onRemoved: @escaping () -> Void) {
        guard let d = duplicate else { return }
        let confirmed = receipt?.confirmedAt != nil
        var message = "It is the same as the receipt from \(DuplicateText.date(d.purchasedAt)). Your totals and price history will only count that one."
        if confirmed { message += " Items it checked on your list will be unchecked." }
        message += " This can't be undone."
        let app = app
        app.trays.present("remove-duplicate") {
            ConfirmTray(title: "Remove this receipt?", message: message, confirmTitle: "Remove this receipt", destructive: true) {
                guard app.network.isOnline else { app.toasts.error("Needs a connection"); return false }
                return await self.delete()
            } afterDismiss: {
                app.toasts.show("Receipt removed")
                onRemoved()
            }
        }
    }
}

// MARK: - Banner wiring

struct DuplicateBannerSection: View {
    let model: ReceiptModel
    let duplicate: PossibleDuplicate
    let onRemoved: () -> Void
    @Environment(AppModel.self) private var app

    var body: some View {
        DuplicateReceiptBanner(
            otherDate: DuplicateText.date(duplicate.purchasedAt),
            otherTotal: Money.format(duplicate.totalCents),
            strength: duplicate.isStrong ? .totalDateAndLines : .totalAndDate,
            onCompare: { app.trays.present("compare-receipts") { CompareReceiptsTray(model: model, duplicate: duplicate, onRemoved: onRemoved) } },
            onRemoveThis: { remove() },
            onKeepBoth: { model.keepBoth() }
        )
        .transition(.opacity.combined(with: .scale(scale: 0.98, anchor: .top)))
    }

    private func remove() {
        guard app.network.isOnline else { app.toasts.error("Needs a connection to remove a receipt"); return }
        model.askRemoveDuplicate(onRemoved: onRemoved)
    }
}

// MARK: - Compare view

struct CompareReceiptsTray: View {
    let model: ReceiptModel
    let duplicate: PossibleDuplicate
    let onRemoved: () -> Void

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var other: Receipt?
    @State private var loadFailed = false

    private var mine: Receipt? { model.receipt }
    private var rows: [DuplicateRow]? {
        guard let a = mine?.lines, let b = other?.lines else { return nil }
        return DuplicateCompare.rows(this: a, other: b)
    }

    var body: some View {
        VStack(spacing: 0) {
            TrayHeader(title: "Compare receipts")
                .padding(.bottom, Space.l)
            ScrollView {
                VStack(alignment: .leading, spacing: Space.xl) {
                    grid
                    Button { openOther() } label: {
                        TypeStyle.label.sized(14).text("Open the other receipt").foregroundStyle(Palette.blueText)
                    }
                    items
                }
                .padding(.bottom, Space.xl)
            }
            .scrollIndicators(.hidden)
            VStack(spacing: Space.m) {
                Button("Same receipt, remove this one") { act { model.askRemoveDuplicate(onRemoved: onRemoved) } }
                    .buttonStyle(.shop(duplicate.isStrong ? .primary : .secondary, size: .regular, fullWidth: true))
                Button("Keep both") { act { model.keepBoth() } }
                    .buttonStyle(.shop(.secondary, size: .regular, fullWidth: true))
            }
            .padding(.top, Space.l)
        }
        .frame(height: min(UIScreen.main.bounds.height * 0.86, 760))
        .task { await loadOther() }
    }

    private func act(_ run: @escaping () -> Void) {
        trays.dismiss()
        Task {
            try? await Task.sleep(for: .milliseconds(380))
            run()
        }
    }

    private func openOther() {
        trays.dismiss()
        app.push(.receipt(duplicate.receiptId))
    }

    private func loadOther() async {
        if let cached = app.home.receipts.first(where: { $0.id == duplicate.receiptId }) { other = cached }
        do {
            other = try await app.api.receipt(duplicate.receiptId)
            loadFailed = false
        } catch { loadFailed = other?.lines == nil }
    }

    // MARK: Header grid

    private var grid: some View {
        let shopName = duplicate.shopName ?? duplicate.shop?.name ?? "Unknown shop"
        let thisShop = app.catalog.shop(mine?.shopId)?.name ?? "Unknown shop"
        let thisDate = mine?.purchasedAt
        let sameDay = thisDate.map { d in duplicate.purchasedAt.map { Dates.calendar.isDate(d, inSameDayAs: $0) } ?? false } ?? false
        let sameTime = duplicate.timeMatches
        return HStack(alignment: .top, spacing: Space.l) {
            column(title: "This receipt", id: model.receiptId, shop: thisShop, date: thisDate, differsDay: !sameDay, differsTime: !sameTime,
                   total: mine?.totalCents, differsTotal: mine?.totalCents != duplicate.totalCents, items: duplicate.itemCount, differsItems: duplicate.itemCount != duplicate.otherItemCount, differsShop: thisShop != shopName)
            column(title: "From \(DuplicateText.date(duplicate.purchasedAt))", id: duplicate.receiptId, shop: shopName, date: duplicate.purchasedAt, differsDay: !sameDay, differsTime: !sameTime,
                   total: duplicate.totalCents, differsTotal: mine?.totalCents != duplicate.totalCents, items: duplicate.otherItemCount, differsItems: duplicate.itemCount != duplicate.otherItemCount, differsShop: thisShop != shopName)
        }
    }

    private func column(title: String, id: Int64, shop: String, date: Date?, differsDay: Bool, differsTime: Bool, total: Int64?, differsTotal: Bool, items: Int, differsItems: Bool, differsShop: Bool) -> some View {
        VStack(alignment: .leading, spacing: Space.s) {
            TypeStyle.caption.text(title).foregroundStyle(Palette.ink2)
            CachedImage(url: app.api.receiptImageURL(id), key: "receipt:\(id)", maxPixel: 400) { image in
                image.resizable().scaledToFill()
            } placeholder: {
                Palette.sunk
            }
            .frame(maxWidth: .infinity)
            .aspectRatio(4.0 / 5.0, contentMode: .fit)
            .clipShape(RoundedRectangle.continuous(12))
            .onTapGesture { trays.dismiss(); app.push(.receiptImage(id)) }
            .accessibilityLabel("Open image of \(title.lowercased())")
            .accessibilityAddTraits(.isButton)
            HStack(spacing: Space.s) {
                ShopMark(name: shop, size: 18)
                value(shop, differs: differsShop)
            }
            value(date.map { (Dates.shortDay($0) + " " + Dates.time($0)) } ?? "No date", differs: differsDay || differsTime)
            value(total.map(Money.format) ?? "No total", differs: differsTotal)
            value("\(items) item\(items == 1 ? "" : "s")", differs: differsItems)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func value(_ text: String, differs: Bool) -> some View {
        (differs ? TypeStyle.label.sized(14) : TypeStyle.body.sized(14)).text(text)
            .foregroundStyle(differs ? Palette.amberText : Palette.ink)
            .lineLimit(1)
    }

    // MARK: Items

    @ViewBuilder
    private var items: some View {
        if let rows {
            let n = DuplicateCompare.differing(rows)
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    TypeStyle.rowTitle.text("Items").foregroundStyle(Palette.ink)
                    Spacer()
                    TypeStyle.label.sized(13).text(n == 0 ? "All items match" : "\(n) item\(n == 1 ? "" : "s") differ")
                        .foregroundStyle(n == 0 ? Palette.greenText : Palette.amberText)
                }
                .padding(.bottom, Space.s)
                ForEach(rows) { row in
                    HStack(spacing: Space.s) {
                        Circle().fill(row.side == .both ? Color.clear : Accent.amber).frame(width: 7, height: 7)
                        TypeStyle.body.sized(14).text(row.text).foregroundStyle(row.side == .both ? Palette.ink2 : Palette.ink).lineLimit(1)
                        Spacer(minLength: Space.s)
                        TypeStyle.body.sized(14).text(Money.format(row.cents)).foregroundStyle(row.side == .both ? Palette.ink2 : Palette.ink)
                        if row.side != .both {
                            TypeStyle.caption.text(row.side == .thisOnly ? "this only" : "other only").foregroundStyle(Palette.amberText)
                        }
                    }
                    .frame(minHeight: 34)
                    .accessibilityElement(children: .combine)
                }
            }
        } else if loadFailed || !app.network.isOnline {
            TypeStyle.rowSub.text("Connect to compare items").foregroundStyle(Palette.ink2)
        } else {
            SkeletonRows { SkeletonLineRow() }
        }
    }
}
