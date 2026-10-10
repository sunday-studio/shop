import Foundation
import Observation
import SwiftUI

/// Barcodes scanned while the network was down, kept on disk until they can be created
/// (`POST /v1/barcodes/{code}/product` is safe to repeat).
@MainActor
enum PendingScans {
    private static let name = "pending-scans.json"

    static var codes: [String] { DiskStore.load([String].self, from: name) ?? [] }

    static func add(_ code: String) {
        var all = codes
        guard !all.contains(code) else { return }
        all.append(code)
        DiskStore.save(all, to: name)
    }

    static func remove(_ code: String) {
        DiskStore.save(codes.filter { $0 != code }, to: name)
    }
}

/// C.3: one unpacking session (docs/DESIGN-PRODUCT-AUTOPOPULATE.md §5). Every distinct barcode
/// becomes an entry, and scanning a code now *creates* the product when it isn't known: the
/// scanner never stops for a form.
@MainActor
@Observable
final class UnpackModel {
    struct Entry: Identifiable, Equatable {
        let id = UUID()
        let code: String
        var outcome: ScanOutcome
        var product: Product?
        /// "Same as AH BIO MELK?" until answered.
        var mergeSuggestion: SuggestedMerge?
        var receiptLines: [UnmatchedLine] = []
        /// True when this scan created the product (it counts as "added").
        var createdNow = false
        var needsName = false
        var scannedAt = Date()

        static func == (a: Entry, b: Entry) -> Bool { a.id == b.id && a.outcome == b.outcome && a.mergeSuggestion?.productId == b.mergeSuggestion?.productId && a.receiptLines.count == b.receiptLines.count }
    }

    private(set) var entries: [Entry] = []
    @ObservationIgnored private let app: AppModel
    /// Same code within this window moves the row to the top without a haptic.
    @ObservationIgnored private let repeatWindow: TimeInterval = 2.5

    init(app: AppModel) {
        self.app = app
        // Codes left over from an offline session show up as waiting rows.
        for code in PendingScans.codes where !entries.contains(where: { $0.code == code }) {
            entries.append(Entry(code: code, outcome: .queued))
        }
        if !entries.isEmpty && app.network.isOnline { Task { await flushQueued() } }
    }

    // MARK: Tally

    var knownCount: Int { entries.filter { if case .known = $0.outcome { return true }; return false }.count }
    var addedCount: Int { entries.filter { $0.createdNow && $0.outcome != .queued && !$0.needsName }.count }
    var toNameCount: Int { entries.filter(\.needsName).count }
    /// Created in this session and still waiting for a look in the review queue.
    var reviewCount: Int { entries.filter { $0.createdNow && ($0.product?.needsReview ?? false) }.count }
    var linkableCount: Int { entries.filter { !$0.receiptLines.isEmpty }.count }
    /// Something was created, so Done shows the summary.
    var createdAny: Bool { entries.contains { $0.createdNow } }

    // MARK: Scanning

    func scanned(_ code: String) {
        if let i = entries.firstIndex(where: { $0.code == code }) {
            var entry = entries.remove(at: i)
            let recent = Date().timeIntervalSince(entry.scannedAt) < repeatWindow
            entry.scannedAt = Date()
            withAnimation(Motion.pop) { entries.insert(entry, at: 0) }
            if !recent { Haptics.play(.scanKnown) }
            return
        }
        // Instant answer from the local catalog when we have it.
        if let local = app.catalog.products.values.first(where: { $0.barcode == code }) {
            var entry = Entry(code: code, outcome: .known(name: local.name, meta: local.sizeText))
            entry.product = local
            entries.insert(entry, at: 0)
            Haptics.play(.scanKnown)
            return
        }
        if !app.network.isOnline {
            queue(code)
            return
        }
        entries.insert(Entry(code: code, outcome: .looking), at: 0)
        Task { await create(code) }
    }

    private func queue(_ code: String) {
        PendingScans.add(code)
        entries.insert(Entry(code: code, outcome: .queued), at: 0)
        Haptics.play(.uploadQueued)
    }

    /// Back online: resolve the codes that were waiting, in place and without a haptic.
    func flushQueued() async {
        for entry in entries where entry.outcome == .queued {
            await create(entry.code, silent: true)
        }
    }

    private func create(_ code: String, silent: Bool = false) async {
        do {
            let response = try await app.api.createBarcodeProduct(code)
            PendingScans.remove(code)
            apply(response, for: code, silent: silent)
        } catch let error as APIError {
            if error.isConnectivity {
                // Went offline mid-lookup: keep it for later.
                if !entries.contains(where: { $0.code == code && $0.outcome == .queued }) { PendingScans.add(code) }
                update(code) { $0.outcome = .queued }
                if !silent { Haptics.play(.uploadQueued) }
                return
            }
            update(code) { $0.outcome = .failed(message: error.message) }
            if !silent { Haptics.play(.error) }
        } catch {
            update(code) { $0.outcome = .failed(message: error.localizedDescription) }
            if !silent { Haptics.play(.error) }
        }
    }

    private func apply(_ response: BarcodeCreateResponse, for code: String, silent: Bool) {
        let product = response.product
        app.catalog.upsert(product)
        let category = app.catalog.category(product.categoryId)?.name
        var outcome: ScanOutcome
        var needsName = false
        var haptic: HapticEvent
        switch (response.status, response.offStatus) {
        case ("known", _):
            outcome = .known(name: product.name, meta: product.sizeText)
            haptic = .scanKnown
        case ("created", _):
            outcome = .created(name: product.name, category: category)
            haptic = .scanNew
        case (_, "unavailable"):
            outcome = .offUnavailable(name: product.name)
            haptic = .scanNew
        default:
            outcome = .createdMinimal(name: product.name)
            needsName = true
            haptic = .warning
        }
        update(code) {
            $0.outcome = outcome
            $0.product = product
            $0.createdNow = response.createdNow
            $0.needsName = needsName
            $0.mergeSuggestion = response.mergeSuggestion
            $0.receiptLines = response.unmatchedLines
        }
        if !silent { Haptics.play(haptic) }
        if response.createdNow { Task { await app.productReview.refreshCount() } }
    }

    private func update(_ code: String, _ change: (inout Entry) -> Void) {
        guard let i = entries.firstIndex(where: { $0.code == code }) else { return }
        change(&entries[i])
    }

    func retry(_ entry: Entry) {
        update(entry.code) { $0.outcome = .looking }
        Task { await create(entry.code) }
    }

    // MARK: Merge prompt

    /// "Merge": the scanned (barcode) product survives, the receipt product moves into it.
    func merge(_ entry: Entry) async {
        guard let suggestion = entry.mergeSuggestion, let product = entry.product else { return }
        update(entry.code) { $0.mergeSuggestion = nil }
        do {
            let result = try await app.api.mergeProduct(suggestion.productId, into: product.id)
            app.catalog.upsert(result.product)
            update(entry.code) { $0.product = result.product; $0.outcome = .created(name: result.product.name, category: app.catalog.category(result.product.categoryId)?.name) }
            if let undoId = result.undoId {
                app.toasts.undoable("Merged \(result.mergedAway?.name ?? suggestion.name) into \(result.product.name)") { [weak self] in
                    Task { await self?.undoMerge(undoId, entry: entry, suggestion: suggestion) }
                }
            }
            await app.productReview.refreshCount()
        } catch {
            update(entry.code) { $0.mergeSuggestion = suggestion }
            Haptics.play(.error)
            app.toasts.error("Couldn't merge. Try again.")
        }
    }

    private func undoMerge(_ id: Int64, entry: Entry, suggestion: SuggestedMerge) async {
        do {
            let result = try await app.api.undoProductChange(id)
            app.toasts.show("Restored \(result.product.name)")
            update(entry.code) { $0.mergeSuggestion = suggestion }
            await app.productReview.refreshCount()
        } catch APIError.conflict(_) {
            Haptics.play(.warning)
            app.toasts.show("Can't undo this one anymore")
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't undo. Try again.")
        }
    }

    /// "No": keep both, quietly.
    func dismissMerge(_ entry: Entry) {
        guard let product = entry.product else { return }
        update(entry.code) { $0.mergeSuggestion = nil }
        Task {
            _ = try? await app.api.reviewProduct(product.id, ReviewBody(action: "keep_separate", categoryId: nil, name: nil, intoProductId: nil))
            await app.productReview.refreshCount()
        }
    }

    // MARK: Naming

    /// "Name it" / "Add name": saves the name and takes the product off the review list.
    func name(_ entry: Entry, _ text: String) async {
        guard let product = entry.product else { return }
        var input = product.input
        input.name = text
        do {
            let saved = try await app.api.updateProduct(product.id, input)
            _ = try? await app.api.reviewProduct(product.id, ReviewBody(action: "accept", categoryId: nil, name: nil, intoProductId: nil))
            app.catalog.upsert(saved)
            update(entry.code) {
                $0.product = saved
                $0.needsName = false
                $0.outcome = .created(name: saved.name, category: app.catalog.category(saved.categoryId)?.name)
            }
            await app.productReview.refreshCount()
        } catch let error as APIError {
            Haptics.play(.error)
            app.toasts.error(error.message)
        } catch {}
    }

    func saved(_ product: Product, for code: String) {
        update(code) { $0.product = product }
    }
}
