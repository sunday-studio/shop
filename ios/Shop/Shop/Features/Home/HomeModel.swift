import Foundation
import Observation
import SwiftUI

/// Home (H): this month's spend above the receipts inbox. Owned by AppModel so uploads and
/// confirmations can refresh it and the tab badge stays current.
@MainActor
@Observable
final class HomeModel {
    struct Filter: Equatable {
        var status: ReceiptStatus?
        var shopId: Int64?
        var isActive: Bool { status != nil || shopId != nil }
    }

    struct DayGroup: Identifiable {
        let id: Date
        let title: String
        let totalCents: Int64
        let receipts: [Receipt]
    }

    private(set) var receipts: [Receipt] = []
    private(set) var nextCursor: String?
    private(set) var monthSpend: Spend?
    private(set) var lastMonthCents: Int64?
    private(set) var attention: [Receipt] = []
    private(set) var counts: ReceiptCounts?
    private(set) var isLoading = false
    private(set) var hasLoaded = false
    /// True once the first month-spend request has finished (success or not). The hero waits for it.
    private(set) var spendLoaded = false
    /// Shop suggestions found for recent unconfirmed receipts, by receipt id.
    private(set) var suggestions: [Int64: ShopSuggestion] = [:]
    /// Receipt ids whose suggestion was already asked for (a suggestion can be absent).
    @ObservationIgnored private var suggestionChecked: Set<Int64> = []
    /// "Not now": suggestion groups hidden for this session.
    private(set) var hiddenSuggestionKeys: Set<String> = []
    private(set) var error: APIError?
    private(set) var filter = Filter()

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private let catalog: Catalog
    @ObservationIgnored private var pollTask: Task<Void, Never>?

    /// The last month figure, kept on disk so Home never shows €0,00 just because it's offline.
    private struct CachedSpend: Codable {
        var monthKey: String
        var spend: Spend
        var lastMonthCents: Int64?
        var savedAt: Date
    }

    private static let spendCacheName = "spend.json"
    private static func monthKey(_ date: Date) -> String { Dates.startOfMonth(date).formatted(.iso8601.year().month()) }

    /// The figure on screen comes from disk (or the last good fetch), not from this launch.
    private(set) var spendIsStale = false
    private(set) var spendUpdatedAt: Date?
    /// The first spend request failed and there is nothing cached for this month.
    private(set) var spendUnavailable = false

    init(api: APIClient, catalog: Catalog) {
        self.api = api
        self.catalog = catalog
        if let cached = DiskStore.load(CachedSpend.self, from: Self.spendCacheName), cached.monthKey == Self.monthKey(.now) {
            monthSpend = cached.spend
            lastMonthCents = cached.lastMonthCents
            spendIsStale = true
            spendUpdatedAt = cached.savedAt
            spendLoaded = true
        }
        if let cached = DiskStore.load([Receipt].self, from: "inbox.json") {
            receipts = cached
            attention = cached.filter { $0.status == .needsReview || $0.status == .failed }
        }
    }

    /// Unassigned receipts the server doesn't count as "needs review": they still need a shop.
    var unassignedCount: Int {
        attention.filter { $0.shopId == nil && $0.status != .needsReview && $0.status != .failed }.count
    }
    var attentionCount: Int { (counts.map { $0.needsReview + $0.failed } ?? attention.count) + unassignedCount }
    var processingCount: Int { receipts.filter { $0.status == .processing || $0.status == .pending }.count }

    /// Rough review time: about a minute per receipt.
    var attentionHint: String {
        let minutes = max(1, attention.count)
        return "about \(minutes) min"
    }

    var monthDeltaCents: Int64? {
        guard let monthSpend, let lastMonthCents else { return nil }
        return monthSpend.totalCents - lastMonthCents
    }

    var dayGroups: [DayGroup] {
        let grouped = Dictionary(grouping: receipts) { Dates.startOfDay($0.displayDate) }
        return grouped.keys.sorted(by: >).map { day in
            let items = (grouped[day] ?? []).sorted { $0.displayDate > $1.displayDate }
            let total = items.reduce(Int64(0)) { $0 + ($1.totalCents ?? 0) }
            return DayGroup(id: day, title: Dates.dayLabel(day), totalCents: total, receipts: items)
        }
    }

    private static func legendColor(for bucket: SpendBucket, catalog: Catalog) -> Color {
        if let id = Int64(bucket.key), let category = catalog.category(id) { return CategoryColor.swatch(for: category).color }
        return CategoryColor.swatch(for: bucket.label).color
    }

    /// Top five categories plus "Other" for the rest.
    var categoryShares: [CategoryShare] {
        guard let spend = monthSpend, spend.totalCents > 0 else { return [] }
        let buckets = spend.buckets.filter { $0.cents > 0 }.sorted { $0.cents > $1.cents }
        let named = buckets.filter { $0.key != "none" }.prefix(5)
        var shares = named.map { CategoryShare(name: Self.shortName($0.label), cents: $0.cents, color: Self.legendColor(for: $0, catalog: catalog)) }
        let rest = spend.totalCents - named.reduce(Int64(0)) { $0 + $1.cents }
        if rest > 0 { shares.append(CategoryShare(name: "Other", cents: rest, color: Palette.neutral)) }
        return shares
    }

    static func shortName(_ name: String) -> String {
        // "Meat & fish" → "Meat" to fit the legend, like the wireframe.
        name.components(separatedBy: " & ").first ?? name
    }

    func setFilter(_ new: Filter) {
        guard new != filter else { return }
        filter = new
        Task { await refresh() }
    }

    // MARK: Loading

    func refresh() async {
        guard !isLoading else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            async let page = api.receipts(shopId: filter.shopId, status: filter.status, limit: 50)
            async let review = api.receipts(status: .needsReview, limit: 200)
            async let failed = api.receipts(status: .failed, limit: 50)
            let (p, r, f) = try await (page, review, failed)
            receipts = p.items
            nextCursor = p.nextCursor
            // Receipts without a shop (and no suggestion accepted yet) go to the review list too.
            let flagged = Set((r.items + f.items).map(\.id))
            let unassigned = p.items.filter { $0.shopId == nil && $0.confirmedAt == nil && $0.status == .ok && !flagged.contains($0.id) }
            attention = (r.items + f.items + unassigned).sorted { $0.displayDate > $1.displayDate }
            error = nil
            hasLoaded = true
            if !filter.isActive { DiskStore.save(p.items, to: "inbox.json") }
        } catch let e as APIError {
            error = e
        } catch {}
        counts = try? await api.receiptCounts()
        await refreshSpend()
        updatePolling()
        await refreshSuggestions()
    }

    /// Asks the server about the newest unconfirmed receipts (the list payload doesn't carry
    /// suggestions), a few at a time.
    func refreshSuggestions() async {
        let candidates = receipts
            .filter { $0.confirmedAt == nil && ($0.status == .ok || $0.status == .needsReview) && !suggestionChecked.contains($0.id) }
            .prefix(12)
        guard !candidates.isEmpty else { return }
        for receipt in candidates {
            suggestionChecked.insert(receipt.id)
            if let suggestion = try? await api.shopSuggestion(receiptId: receipt.id) { suggestions[receipt.id] = suggestion }
        }
    }

    /// One Home card per suggested shop (at most two).
    struct SuggestionGroup: Identifiable {
        var id: String { suggestion.groupKey }
        var suggestion: ShopSuggestion
        var firstReceiptId: Int64
        var count: Int
    }

    var suggestionGroups: [SuggestionGroup] {
        var groups: [SuggestionGroup] = []
        for receipt in receipts {
            guard let suggestion = suggestions[receipt.id], !hiddenSuggestionKeys.contains(suggestion.groupKey) else { continue }
            if let i = groups.firstIndex(where: { $0.id == suggestion.groupKey }) {
                groups[i].count = max(groups[i].count, suggestion.receiptCount)
            } else {
                groups.append(SuggestionGroup(suggestion: suggestion, firstReceiptId: receipt.id, count: suggestion.receiptCount))
            }
        }
        return Array(groups.prefix(2))
    }

    func hideSuggestion(_ key: String) { hiddenSuggestionKeys.insert(key) }

    /// After accepting: forget the suggestion for the moved receipts and re-read them.
    func suggestionAccepted(linked: [Int64]) async {
        for id in linked { suggestions[id] = nil }
        await refresh()
    }

    func loadMore() async {
        guard let cursor = nextCursor, !isLoading else { return }
        isLoading = true
        defer { isLoading = false }
        if let page = try? await api.receipts(shopId: filter.shopId, status: filter.status, limit: 50, cursor: cursor) {
            let known = Set(receipts.map(\.id))
            receipts += page.items.filter { !known.contains($0.id) }
            nextCursor = page.nextCursor
        }
    }

    private func refreshSpend() async {
        let now = Date.now
        let monthStart = Dates.startOfMonth(now)
        let lastStart = Dates.adding(.month, -1, to: monthStart)
        let dayOfMonth = Dates.calendar.component(.day, from: now)
        let lastMonthDays = Dates.calendar.range(of: .day, in: .month, for: lastStart)?.count ?? 30
        let lastSameDay = Dates.adding(.day, min(dayOfMonth, lastMonthDays) - 1, to: lastStart)
        async let current = api.spend(from: monthStart, to: now, groupBy: .category)
        async let previous = api.spend(from: lastStart, to: lastSameDay, groupBy: .month)
        let fresh = try? await current
        let freshPrevious = try? await previous
        if let fresh {
            monthSpend = fresh
            lastMonthCents = freshPrevious?.totalCents
            spendIsStale = false
            spendUnavailable = false
            spendUpdatedAt = .now
            DiskStore.save(CachedSpend(monthKey: Self.monthKey(now), spend: fresh, lastMonthCents: lastMonthCents, savedAt: .now), to: Self.spendCacheName)
        } else if monthSpend != nil {
            spendIsStale = true   // keep the last known figure
        } else {
            spendUnavailable = true
        }
        spendLoaded = true
    }

    // MARK: Polling

    /// Every 3 s while a receipt is processing on the server (no push, IA "No push").
    private func updatePolling() {
        let processing = receipts.contains { $0.status == .processing || $0.status == .pending }
        if processing, pollTask == nil {
            pollTask = Task { [weak self] in
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(3))
                    guard let self, !Task.isCancelled else { return }
                    await self.pollOnce()
                }
            }
        } else if !processing {
            pollTask?.cancel()
            pollTask = nil
        }
    }

    private func pollOnce() async {
        let ids = receipts.filter { $0.status == .processing || $0.status == .pending }.map(\.id)
        guard !ids.isEmpty else { updatePolling(); return }
        var changed = false
        for id in ids {
            guard let fresh = try? await api.receipt(id) else { continue }
            if let i = receipts.firstIndex(where: { $0.id == id }), receipts[i].status != fresh.status {
                var r = fresh
                r.lines = nil
                receipts[i] = r
                changed = true
                Haptics.play(fresh.status == .ok ? .success : (fresh.status == .needsReview ? .warning : .error))
            }
        }
        if changed {
            pollTask?.cancel()
            pollTask = nil
            await refresh()
        }
    }

    // MARK: Local updates

    /// After a confirm/edit on the detail screen.
    func apply(_ receipt: Receipt) {
        var r = receipt
        r.lines = nil
        if let i = receipts.firstIndex(where: { $0.id == r.id }) { receipts[i] = r }
        attention.removeAll { $0.id == r.id }
        if r.status == .needsReview || r.status == .failed || (r.shopId == nil && r.confirmedAt == nil && r.status == .ok) { attention.insert(r, at: 0) }
        Task { counts = try? await api.receiptCounts() }
        updatePolling()
    }

    func remove(_ id: Int64) {
        receipts.removeAll { $0.id == id }
        attention.removeAll { $0.id == id }
        Task { counts = try? await api.receiptCounts() }
    }

    func reset() {
        pollTask?.cancel()
        pollTask = nil
        receipts = []
        attention = []
        counts = nil
        monthSpend = nil
        lastMonthCents = nil
        spendIsStale = false
        spendUnavailable = false
        spendUpdatedAt = nil
        DiskStore.remove(Self.spendCacheName)
        hasLoaded = false
        spendLoaded = false
        suggestions = [:]
        suggestionChecked = []
        hiddenSuggestionKeys = []
        filter = Filter()
    }
}

// MARK: - Receipt row presentation

extension Receipt {
    /// Sub-line and colours for inbox rows (H).
    @MainActor
    func rowPresentation() -> (subtitle: String, subtitleColor: Color, amount: String, amountColor: Color, spinner: Bool) {
        let time = purchasedAt.map(Dates.time)
        let items = lineCount.map { $0 == 1 ? "1 item" : "\($0) items" }
        func join(_ parts: String?...) -> String { parts.compactMap { $0 }.joined(separator: " · ") }
        let amount = totalCents.map(Money.format) ?? "—"
        switch status {
        case .pending, .processing:
            return ("Reading receipt…", Palette.blueText, "—", Palette.ink3, true)
        case .failed:
            return ("Couldn't read the image", Palette.redText, amount, Palette.ink3, false)
        case .needsReview:
            let reason: String
            if let unlinked = unlinkedCount, unlinked > 0, reasons.isEmpty {
                reason = unlinked == 1 ? "1 item to link" : "\(unlinked) items to link"
            } else {
                reason = Self.reasonText(reasons.first)
            }
            return (join(reason, time), Palette.amberText, amount, Palette.ink, false)
        case .ok:
            let line = join(time, items)
            return (line.isEmpty ? "Done" : line, Palette.ink2, amount, Palette.ink, false)
        }
    }

    static func reasonText(_ reason: ReviewReason?) -> String {
        switch reason {
        case .totalMismatch: "Total doesn’t match"
        case .noTotal: "No total found"
        case .lineMath: "A line doesn’t add up"
        case .noDate: "No date found"
        case .lowConf: "Hard to read"
        case .unparsedRows: "Rows couldn’t be read"
        case nil: "Needs a look"
        }
    }
}
