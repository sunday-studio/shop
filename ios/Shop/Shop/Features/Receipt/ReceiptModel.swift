import Foundation
import Observation
import SwiftUI

/// H.1: detail and review in one screen.
@MainActor
@Observable
final class ReceiptModel {
    enum LineFilter: Hashable { case all, toFix }

    struct LinePresentation {
        let title: String
        let isRaw: Bool
        let subtitle: String?
        let subtitleColor: Color
        let amount: String
        let titleColor: Color
        let amountColor: Color
        let dot: Color
        let indented: Bool
    }

    let receiptId: Int64
    private(set) var receipt: Receipt?
    private(set) var isLoading = false
    private(set) var error: APIError?
    private(set) var isConfirming = false
    private(set) var isAcceptingSuggestion = false
    private(set) var suggestionDismissed = false
    var lineFilter: LineFilter = .all

    @ObservationIgnored let app: AppModel
    @ObservationIgnored private var pollTask: Task<Void, Never>?

    init(receiptId: Int64, app: AppModel) {
        self.receiptId = receiptId
        self.app = app
        // Summary from Home (without lines) so the first frame has real content.
        receipt = app.home.receipts.first { $0.id == receiptId } ?? app.home.attention.first { $0.id == receiptId }
    }

    var api: APIClient { app.api }
    var catalog: Catalog { app.catalog }
    var shop: Shop? { catalog.shop(receipt?.shopId) }
    var lines: [ReceiptLine] { (receipt?.lines ?? []).sorted { $0.lineNo < $1.lineNo } }

    /// Lines counted toward the sum: everything except unread "other" rows.
    var countedCents: Int64 { lines.filter { $0.kind != .other }.reduce(0) { $0 + $1.lineTotalCents } }

    var toFix: [ReceiptLine] { lines.filter(needsFix) }

    var visibleLines: [ReceiptLine] {
        let source = lineFilter == .toFix ? toFix : lines
        // Discounts sit right under their item.
        var ordered: [ReceiptLine] = []
        let children = Dictionary(grouping: source.filter { $0.parentLineId != nil }, by: { $0.parentLineId! })
        let ids = Set(source.map(\.id))
        for line in source where line.parentLineId == nil || !ids.contains(line.parentLineId!) {
            ordered.append(line)
            ordered += children[line.id] ?? []
        }
        return ordered
    }

    func needsFix(_ line: ReceiptLine) -> Bool {
        (line.kind == .item && line.productId == nil) || line.kind == .other
    }

    // MARK: Loading

    func load() async {
        isLoading = receipt == nil
        defer { isLoading = false }
        do {
            let fresh = try await api.receipt(receiptId)
            receipt = fresh
            error = nil
            app.home.apply(fresh)
            for line in fresh.lines ?? [] where line.productId != nil && catalog.product(line.productId) == nil {
                if let p = try? await api.product(line.productId!) { catalog.upsert(p) }
            }
            if await autoLinkStrongMatches(in: fresh) { return await load() }
        } catch let e as APIError {
            error = e
        } catch {}
        updatePolling()
    }

    /// Lines the server matched at 80 % or better are linked without asking. Each line is tried
    /// once per screen so a refused link can't loop. Returns true when something was linked.
    static let autoLinkScore = 0.8
    @ObservationIgnored private var autoLinkTried: Set<Int64> = []

    private func autoLinkStrongMatches(in receipt: Receipt) async -> Bool {
        guard receipt.confirmedAt == nil, receipt.status != .processing, receipt.status != .pending else { return false }
        var linked = false
        for line in receipt.lines ?? [] where line.kind == .item && line.productId == nil && !autoLinkTried.contains(line.id) {
            guard let best = line.suggestions?.first, best.score >= Self.autoLinkScore else { continue }
            autoLinkTried.insert(line.id)
            if (try? await api.linkLine(line.id, productId: best.productId, remember: false)) != nil { linked = true }
        }
        return linked
    }

    private func updatePolling() {
        let processing = receipt?.status == .processing || receipt?.status == .pending
        if processing, pollTask == nil {
            pollTask = Task { [weak self] in
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(3))
                    guard let self, !Task.isCancelled else { return }
                    let before = self.receipt?.status
                    await self.load()
                    if let now = self.receipt?.status, now != before {
                        Haptics.play(now == .ok ? .success : now == .needsReview ? .warning : .error)
                    }
                }
            }
        } else if !processing {
            pollTask?.cancel()
            pollTask = nil
        }
    }

    func stop() {
        pollTask?.cancel()
        pollTask = nil
    }

    // MARK: Presentation

    var metaLine: String {
        guard let r = receipt else { return "" }
        var parts: [String] = []
        if let d = r.purchasedAt { parts.append(Dates.shortDay(d)); parts.append(Dates.time(d)) } else { parts.append("No date") }
        if let pay = r.paymentMethod, !pay.isEmpty { parts.append(pay) }
        // A summary from Home has no lines yet: leave the count out rather than say "0 lines".
        if r.lines != nil {
            let n = lines.count
            parts.append(n == 1 ? "1 line" : "\(n) lines")
        }
        return parts.joined(separator: " · ")
    }

    static let noDateLead = "No date found."

    /// The reason banner (nil when nothing needs saying).
    var banner: (tone: Tone, lead: String, message: String)? {
        guard let r = receipt else { return nil }
        if r.lines == nil && r.status != .failed { return nil }
        let unread = lines.filter { $0.kind == .other }.count
        let unlinked = lines.filter { $0.kind == .item && $0.productId == nil }.count
        let unreadNote = unread == 0 ? "" : (unread == 1 ? "One row couldn't be read, it's marked below." : "\(unread) rows couldn't be read, they're marked below.")
        switch r.status {
        case .failed:
            return (.red, "Couldn't read this image.", "Re-run OCR, or delete it and scan again.")
        case .processing, .pending:
            return nil
        default:
            break
        }
        if let total = r.totalCents {
            let diff = countedCents - total
            if abs(diff) > 1 {
                let lead = diff < 0 ? "\(Money.format(-diff)) short of the printed total." : "\(Money.format(diff)) over the printed total."
                return (.amber, lead, unreadNote.isEmpty ? "Check the lines against the photo." : unreadNote)
            }
        } else if r.status == .needsReview {
            return (.amber, "No total found.", "Add the printed total in Edit details.")
        }
        if r.reasons.contains(.noDate) { return (.amber, Self.noDateLead, "Pick a date so it counts in your totals.") }
        if r.reasons.contains(.lowConf) { return (.amber, "Hard to read.", "Check the marked lines against the photo.") }
        if r.reasons.contains(.lineMath) { return (.amber, "A line doesn't add up.", "Quantity × price differs from the line total.") }
        if unread > 0 { return (.amber, "", unreadNote) }
        if unlinked > 0 && r.status != .ok {
            return (.amber, unlinked == 1 ? "1 item isn't linked yet." : "\(unlinked) items aren't linked yet.", "Link them so prices are tracked. Confirm works anyway.")
        }
        return nil
    }

    func present(_ line: ReceiptLine) -> LinePresentation {
        let product = catalog.product(line.productId)
        // Prefer the server's name/category on the line; fall back to the cached catalog.
        let categoryName = catalog.category(line.categoryId)?.name ?? catalog.categoryName(forProduct: line.productId)
        let swatch = CategoryColor.swatch(for: catalog.category(line.categoryId ?? product?.categoryId))
        let productName = line.productName ?? product?.name
        let amount = Money.plain(line.lineTotalCents)

        switch line.kind {
        case .discount:
            let parent = lines.first { $0.id == line.parentLineId }
            let parentName = parent.map { $0.productName ?? catalog.product($0.productId)?.name ?? Self.tidy($0.rawText) }
            return LinePresentation(title: Self.discountTitle(line.rawText), isRaw: false,
                                    subtitle: parentName.map { "on \($0)" } ?? "Discount", subtitleColor: Palette.ink2,
                                    amount: amount, titleColor: Palette.greenText, amountColor: Palette.greenText,
                                    dot: parent.map { catalog.categoryColor(forProduct: $0.productId).color } ?? Accent.green, indented: parent != nil)
        case .deposit:
            return LinePresentation(title: "Statiegeld", isRaw: false, subtitle: "Deposit", subtitleColor: Palette.ink2,
                                    amount: amount, titleColor: Palette.ink, amountColor: Palette.ink, dot: Palette.neutral, indented: false)
        case .bag:
            return LinePresentation(title: "Bag", isRaw: false, subtitle: Self.tidy(line.rawText), subtitleColor: Palette.ink2,
                                    amount: amount, titleColor: Palette.ink, amountColor: Palette.ink, dot: Palette.neutral, indented: false)
        case .other:
            return LinePresentation(title: line.rawText, isRaw: true, subtitle: "Couldn’t read this row · not counted", subtitleColor: Palette.amberText,
                                    amount: amount, titleColor: Palette.ink, amountColor: Palette.ink3, dot: Accent.amber, indented: false)
        case .item:
            if let productName {
                return LinePresentation(title: productName, isRaw: false, subtitle: itemDetail(line, product: product, category: categoryName),
                                        subtitleColor: Palette.ink2, amount: amount, titleColor: Palette.ink, amountColor: Palette.ink,
                                        dot: swatch.color, indented: false)
            }
            let sub: String
            if let best = line.suggestions?.first {
                sub = "Link to \(best.name)? \(Int((best.score * 100).rounded()))% match"
            } else {
                sub = "Not linked · tap to pick a product"
            }
            return LinePresentation(title: line.rawText, isRaw: true, subtitle: sub, subtitleColor: Palette.amberText,
                                    amount: amount, titleColor: Palette.ink, amountColor: Palette.ink, dot: Accent.amber, indented: false)
        }
    }

    /// "0,842 kg × €1,99/kg · Produce", "500 g · Meat & fish", "AH HALFV MELK 1,5L · Dairy".
    private func itemDetail(_ line: ReceiptLine, product: Product?, category: String?) -> String {
        var first: String
        if line.unit != .pc, let up = line.unitPriceCents {
            first = "\(Quantity.decimal(line.qty)) \(line.unit.rawValue) × \(Money.format(up))/\(line.unit.rawValue)"
        } else if line.qty > 1, let up = line.unitPriceCents {
            first = "\(Quantity.decimal(line.qty)) × \(Money.format(up))"
        } else {
            first = line.rawText
        }
        if first == line.rawText, let size = product?.sizeText, line.rawText.count > 22 { first = size }
        return [first, category.map(HomeModel.shortName)].compactMap { $0 }.joined(separator: " · ")
    }

    static func discountTitle(_ raw: String) -> String {
        let r = raw.lowercased()
        if r.contains("bonus") { return "Bonus" }
        if r.contains("kassakorting") { return "Kassakorting" }
        if r.contains("korting") { return "Korting" }
        if r.contains("actie") { return "Actie" }
        return "Discount"
    }

    /// "AH HALFV MELK 1,5L" → "Ah halfv melk 1,5l" (used where a raw name stands in for a product).
    static func tidy(_ raw: String) -> String {
        let lower = raw.lowercased()
        return lower.prefix(1).uppercased() + lower.dropFirst()
    }

    // MARK: Shop suggestion

    /// "This looks like Aldi", unless the user said "Not now" or the server has none.
    var suggestion: ShopSuggestion? {
        suggestionDismissed ? nil : receipt?.shopSuggestion
    }

    func dismissSuggestion() {
        guard let key = receipt?.shopSuggestion?.groupKey else { return }
        withAnimation(Motion.collapse) { suggestionDismissed = true }
        app.home.hideSuggestion(key)
    }

    /// One tap: puts this receipt (and the ones waiting on the same branch) on the suggested
    /// shop, creating it first when it's new.
    func acceptSuggestion(name: String? = nil, branch: String? = nil, domain: String? = nil) async {
        guard let s = suggestion, !isAcceptingSuggestion else { return }
        isAcceptingSuggestion = true
        defer { isAcceptingSuggestion = false }
        var body = AcceptShopSuggestionBody()
        if s.kind == .existing {
            body.shopId = s.shopId
        } else {
            body.name = name ?? s.name
            body.branch = branch ?? s.branch
            body.domain = domain ?? s.domain
        }
        do {
            let result = try await api.acceptShopSuggestion(receiptId: receiptId, body)
            catalog.upsert(result.shop)
            Haptics.play(.success)
            let count = result.linkedReceiptIds.count
            let tail = count == 1 ? "1 receipt" : "\(count) receipts"
            app.toasts.show(s.kind == .new ? "\(result.shop.name) added · \(tail) linked"
                            : (count == 1 ? "Moved to \(result.shop.name)" : "\(count) receipts moved to \(result.shop.name)"), tone: .green)
            await load()
            await app.home.suggestionAccepted(linked: result.linkedReceiptIds)
            await catalog.syncAll()
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't add the shop. Try again.")
        }
    }

    // MARK: Actions

    func confirm() async -> Bool {
        isConfirming = true
        defer { isConfirming = false }
        do {
            let r = try await api.confirmReceipt(receiptId)
            replace(r)
            let listText = r.list.map { applyListChecks($0) } ?? nil
            if let listText {
                app.toasts.show(listText, tone: .green, haptic: .success, duration: .seconds(6), action: (title: "Undo", run: { [app, receiptId] in
                    Haptics.play(.light)
                    app.list.undoReceiptChecks(receiptId)
                    Task { await app.list.sync() }
                }))
                if let auto = r.autoProducts, auto.created > 0 {
                    app.productReview.bump(by: auto.needsReview)
                    Task { await app.productReview.refreshCount() }
                }
            } else if let auto = r.autoProducts, auto.created > 0 {
                showAutoProductsToast(auto)
                app.productReview.bump(by: auto.needsReview)
                Task { await app.productReview.refreshCount() }
            } else {
                app.toasts.show("Receipt confirmed", tone: .green)
            }
            return true
        } catch let e as APIError {
            app.toasts.error(e.message)
            return false
        } catch { return false }
    }

    /// Merges what the receipt did to the shopping list; returns the toast text, or nil when it checked nothing.
    private func applyListChecks(_ result: ReceiptListChecks) -> String? {
        app.list.receiptConfirmed(result)
        let checked = result.checked.count, partly = result.reduced.count
        func items(_ n: Int) -> String { n == 1 ? "1 item" : "\(n) items" }
        switch (checked, partly) {
        case (0, 0): return nil
        case (let c, 0): return "Checked \(items(c)) on your list"
        case (0, let p): return "\(items(p)) on your list partly bought"
        case (let c, let p): return "Checked \(items(c)), \(p) partly bought"
        }
    }

    private func showAutoProductsToast(_ auto: AutoProductsSummary) {
        let created: String = auto.created == 1 ? "1 new product" : "\(auto.created) new products"
        var text = "Receipt confirmed · \(created)"
        var action: (title: String, run: () -> Void)?
        if auto.needsReview > 0 {
            text += ", \(auto.needsReview) to check"
            let app = self.app
            action = (title: "Review", run: { app.push(.productReview) })
        }
        app.toasts.show(text, tone: .green, duration: .seconds(4), action: action)
    }

    func saveLine(_ id: Int64?, _ input: LineInput) async -> Bool {
        do {
            if let id {
                let line = try await api.updateLine(id, input)
                replaceLine(line)
            } else {
                let line = try await api.createLine(receiptId: receiptId, input)
                receipt?.lines = (receipt?.lines ?? []) + [line]
            }
            await refreshHeader()
            return true
        } catch let e as APIError {
            app.toasts.error(e.message)
            return false
        } catch { return false }
    }

    /// "This isn't the same product": the line (and others with the same text) gets its own new product.
    func splitProduct(of line: ReceiptLine) async {
        do {
            let result = try await api.splitLineProduct(line.id)
            catalog.upsert(result.product)
            let n = result.linesMoved
            let text = "New product \(result.product.name) · \(n == 1 ? "1 line" : "\(n) lines") moved"
            let mergeId = result.mergeId
            app.toasts.undoable(text) { [weak self] in
                Task { await self?.undoSplit(mergeId) }
            }
            await load()
        } catch let error as APIError {
            Haptics.play(.error)
            app.toasts.error(error.message)
        } catch {}
    }

    private func undoSplit(_ id: Int64) async {
        do {
            let result = try await api.undoProductChange(id)
            app.toasts.show("Restored \(result.into?.name ?? result.product.name)")
            await load()
        } catch APIError.conflict(_) {
            Haptics.play(.warning)
            app.toasts.show("Can't undo this one anymore")
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't undo. Try again.")
        }
    }

    func deleteLine(_ id: Int64) async {
        do {
            try await api.deleteLine(id)
            receipt?.lines?.removeAll { $0.id == id }
            Haptics.play(.warning)
            await refreshHeader()
        } catch let e as APIError {
            app.toasts.error(e.message)
        } catch {}
    }

    func link(_ lineId: Int64, productId: Int64?, remember: Bool) async -> Bool {
        do {
            let result = try await api.linkLine(lineId, productId: productId, remember: remember)
            replaceLine(result.line)
            if result.alsoLinked > 0 {
                app.toasts.show(result.alsoLinked == 1 ? "Also linked 1 other line" : "Also linked \(result.alsoLinked) other lines", tone: .green)
            }
            await refreshHeader()
            return true
        } catch let e as APIError {
            app.toasts.error(e.message)
            return false
        } catch { return false }
    }

    func updateDetails(_ patch: ReceiptPatch) async -> Bool {
        do {
            let r = try await api.updateReceipt(receiptId, patch)
            replace(r, keepLines: true)
            return true
        } catch let e as APIError {
            app.toasts.error(e.message)
            return false
        } catch { return false }
    }

    enum RerunResult { case started, needsForce(String), failed }

    func rerun(ocr: Bool, force: Bool = false) async -> RerunResult {
        do {
            let r: Receipt
            if ocr {
                r = try await api.reocrReceipt(receiptId, force: force)
            } else {
                r = try await api.reparseReceipt(receiptId, force: force)
            }
            replace(r, keepLines: false)
            updatePolling()
            return .started
        } catch APIError.conflict(let problem) {
            return .needsForce(problem.detail ?? problem.title)
        } catch let e as APIError {
            app.toasts.error(e.message)
            return .failed
        } catch { return .failed }
    }

    func delete() async -> Bool {
        do {
            try await api.deleteReceipt(receiptId)
            app.home.remove(receiptId)
            Haptics.play(.warning)
            return true
        } catch let e as APIError {
            app.toasts.error(e.message)
            return false
        } catch { return false }
    }

    // MARK: Helpers

    private func refreshHeader() async {
        if let fresh = try? await api.receipt(receiptId) {
            replace(fresh)
        }
    }

    private func replace(_ r: Receipt, keepLines: Bool = false) {
        var new = r
        if keepLines || new.lines == nil { new.lines = receipt?.lines }
        receipt = new
        app.home.apply(new)
    }

    func setDuplicateLocally(_ d: PossibleDuplicate?) {
        withAnimation(Motion.collapse) { receipt?.possibleDuplicateOf = d }
    }

    private func replaceLine(_ line: ReceiptLine) {
        guard let i = receipt?.lines?.firstIndex(where: { $0.id == line.id }) else { return }
        receipt?.lines?[i] = line
    }
}
