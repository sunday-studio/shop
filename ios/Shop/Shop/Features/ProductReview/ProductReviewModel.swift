import Foundation
import Observation
import SwiftUI
import UIKit

/// The "New products" queue (docs/DESIGN-PRODUCT-AUTOPOPULATE.md): products Shop created itself
/// that it isn't sure about. Also the source of the Products tab badge.
@MainActor
@Observable
final class ProductReviewModel {
    private static let cacheName = "review-queue.json"
    static let reasonOrder: [ReviewReasonKind] = [.nearMatch, .barcodeNameMatch, .oddName, .noCategory, .barcodeMinimal, .barcodeOffUnavailable]

    private(set) var items: [ReviewItem] = []
    /// Queue size, for the tab badge and the Products card. Kept even before the list loads.
    private(set) var total = 0
    private(set) var counts: [String: Int] = [:]
    private(set) var hasLoaded = false
    private(set) var isLoading = false
    private(set) var isLoadingMore = false
    private(set) var loadMoreFailed = false
    private(set) var loadError: APIError?
    private(set) var workingIds: Set<Int64> = []
    /// Category / name picked on a card but not sent yet.
    private(set) var pickedCategory: [Int64: Int64] = [:]
    private(set) var pickedName: [Int64: String] = [:]
    /// Bumped when the last card was handled in this visit, so the screen can play `success` once.
    private(set) var clearedToEmpty = false

    @ObservationIgnored private let app: AppModel
    @ObservationIgnored private let pageSize = 50

    init(app: AppModel) {
        self.app = app
        if let cached = DiskStore.load(ReviewQueueResponse.self, from: Self.cacheName) {
            items = cached.items
            total = cached.total
            counts = cached.counts
        }
    }

    var api: APIClient { app.api }
    var isOffline: Bool { !app.network.isOnline }
    var hasMore: Bool { items.count < (visibleTotal) }
    private var visibleTotal: Int { counts.values.reduce(0, +) > 0 ? counts.values.reduce(0, +) : total }

    /// Sections in the server's reason order, only for reasons that have cards.
    var sections: [(reason: ReviewReasonKind, count: Int, items: [ReviewItem])] {
        Self.reasonOrder.compactMap { reason in
            let group = items.filter { $0.reason == reason.rawValue }
            guard !group.isEmpty else { return nil }
            return (reason, counts[reason.rawValue] ?? group.count, group)
        }
    }

    // MARK: Loading

    /// Cheap: only the counts (`limit=0`). Used for the badge on foreground and after scans.
    func refreshCount() async {
        guard let response = try? await api.reviewQueue(limit: 0) else { return }
        total = response.total
        counts = response.counts
    }

    func load() async {
        guard !isLoading else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            let response = try await api.reviewQueue(limit: pageSize, offset: 0)
            withAnimation(Motion.collapse) {
                items = response.items
                total = response.total
                counts = response.counts
            }
            loadError = nil
            hasLoaded = true
            DiskStore.save(response, to: Self.cacheName)
        } catch let error as APIError {
            loadError = error
            hasLoaded = true
        } catch {}
    }

    func loadMoreIfNeeded(current item: ReviewItem) async {
        guard hasMore, !isLoadingMore, let index = items.firstIndex(where: { $0.id == item.id }), index >= items.count - 3 else { return }
        guard !loadMoreFailed else { return }
        await loadMore()
    }

    /// Page 2 and later; a failure leaves a "Try again" row instead of retrying on every scroll.
    func loadMore() async {
        guard hasMore, !isLoadingMore else { return }
        isLoadingMore = true
        loadMoreFailed = false
        defer { isLoadingMore = false }
        do {
            let response = try await api.reviewQueue(limit: pageSize, offset: items.count)
            let known = Set(items.map(\.id))
            items += response.items.filter { !known.contains($0.id) }
            total = response.total
            counts = response.counts
        } catch {
            loadMoreFailed = true
        }
    }

    /// Optimistic badge bump (confirm just created products); verified by `refreshCount` right after.
    func bump(by n: Int) { total += n }

    func reset() {
        items = []; total = 0; counts = [:]; hasLoaded = false; pickedCategory = [:]; pickedName = [:]
        DiskStore.remove(Self.cacheName)
    }

    // MARK: Local choices

    func categoryName(for item: ReviewItem) -> String? {
        app.catalog.category(pickedCategory[item.id] ?? item.product.categoryId)?.name
    }

    func displayName(for item: ReviewItem) -> String { pickedName[item.id] ?? item.product.name }

    func pick(category id: Int64, for item: ReviewItem) { withAnimation(Motion.fade) { pickedCategory[item.id] = id } }
    func set(name: String, for item: ReviewItem) { pickedName[item.id] = name }

    // MARK: Actions

    /// The main button of the card.
    func primary(_ item: ReviewItem) async {
        guard let reason = item.kind else { return }
        switch reason {
        case .nearMatch, .barcodeNameMatch: await merge(item)
        case .oddName, .noCategory, .barcodeOffUnavailable: await accept(item)
        case .barcodeMinimal: break // opens the name tray (the view handles it)
        }
    }

    func accept(_ item: ReviewItem, name: String? = nil) async {
        let body = ReviewBody(action: "accept", categoryId: pickedCategory[item.id], name: name ?? pickedName[item.id], intoProductId: nil)
        await run(item, failure: "Couldn't save") { try await $0.api.reviewProduct(item.id, body) } done: { _ in }
    }

    func keepSeparate(_ item: ReviewItem) async {
        if let category = pickedCategory[item.id] {
            // keep_separate carries no category: save it first, then decide.
            var input = item.product.input
            input.categoryId = category
            _ = try? await api.updateProduct(item.id, input)
        }
        let body = ReviewBody(action: "keep_separate", categoryId: nil, name: nil, intoProductId: nil)
        await run(item, failure: "Couldn't save") { try await $0.api.reviewProduct(item.id, body) } done: { _ in }
    }

    func merge(_ item: ReviewItem) async {
        let body = ReviewBody(action: "merge", categoryId: nil, name: nil, intoProductId: item.suggestedMerge?.productId)
        await run(item, failure: "Couldn't merge") { try await $0.api.reviewProduct(item.id, body) } done: { [self] result in
            let text = item.reason == ReviewReasonKind.nearMatch.rawValue
                ? "Merged into \(result.product.name)"
                : "Merged \(result.mergedAway?.name ?? item.product.name) into \(result.product.name)"
            offerUndo(text, result: result)
        }
    }

    /// "Try now" on a card whose Open Food Facts lookup failed: the server fills only empty fields.
    func tryNow(_ item: ReviewItem) async {
        guard !workingIds.contains(item.id) else { return }
        workingIds.insert(item.id)
        defer { workingIds.remove(item.id) }
        do {
            let result = try await api.refreshProduct(item.id)
            app.catalog.upsert(result.product)
            switch result.offStatus {
            case "found":
                // The server filled the details; a card that no longer needs a look leaves the queue.
                if result.product.needsReview == false {
                    remove(item)
                } else if let i = items.firstIndex(where: { $0.id == item.id }) {
                    items[i].product = result.product
                }
                app.toasts.show("Found it on Open Food Facts · \(result.product.name)", tone: .green)
            case "not_found":
                app.toasts.show("Not on Open Food Facts")
            default:
                app.toasts.show("Still can't reach Open Food Facts")
            }
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't try now. Try again.")
        }
    }

    func setPhoto(_ jpeg: Data, for item: ReviewItem) async {
        do {
            let product = try await api.putProductImage(item.id, jpeg: jpeg)
            // Show the photo we just sent without downloading it again.
            if let url = api.productImageURL(product), let image = UIImage(data: jpeg) { ImageCache.shared.store(image, key: url.absoluteString) }
            app.catalog.upsert(product)
            if let i = items.firstIndex(where: { $0.id == item.id }) { items[i].product = product }
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't save the photo.")
        }
    }

    func discard(_ item: ReviewItem) async {
        let body = ReviewBody(action: "discard", categoryId: nil, name: nil, intoProductId: nil)
        await run(item, failure: "Couldn't discard") { try await $0.api.reviewProduct(item.id, body) } done: { [self] result in
            offerUndo("Discarded \(item.product.name)", result: result)
        }
    }

    private func offerUndo(_ text: String, result: ReviewResult) {
        guard let undoId = result.undoId else { return }
        app.toasts.undoable(text) { [weak self] in
            Task { await self?.undo(undoId) }
        }
    }

    func undo(_ id: Int64) async {
        do {
            let result = try await api.undoProductChange(id)
            Haptics.play(.light)
            let name = result.product.name
            app.toasts.show(result.linesSkipped > 0 ? "Restored \(name) · \(result.linesSkipped) lines kept their new product" : "Restored \(name)")
            await load()
        } catch APIError.conflict(_) {
            Haptics.play(.warning)
            app.toasts.show("Can't undo this one anymore")
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't undo. Try again.")
        }
    }

    /// Runs one action: card dims, then collapses on success; 409 collapses quietly.
    private func run(_ item: ReviewItem, failure: String, _ call: (ProductReviewModel) async throws -> ReviewResult, done: (ReviewResult) -> Void) async {
        guard !workingIds.contains(item.id) else { return }
        workingIds.insert(item.id)
        defer { workingIds.remove(item.id) }
        do {
            let result = try await call(self)
            remove(item)
            catalogRefresh(result.product)
            done(result)
        } catch APIError.conflict(_) {
            remove(item)
            app.toasts.show("Already handled")
            await load()
        } catch {
            Haptics.play(.error)
            app.toasts.error("\(failure). Try again.")
        }
    }

    private func remove(_ item: ReviewItem) {
        withAnimation(Motion.collapse) {
            items.removeAll { $0.id == item.id }
            total = max(0, total - 1)
            if let n = counts[item.reason] { counts[item.reason] = max(0, n - 1) }
        }
        pickedCategory[item.id] = nil
        pickedName[item.id] = nil
        if items.isEmpty && total == 0 {
            clearedToEmpty = true
            Haptics.play(.success)
        }
        DiskStore.save(ReviewQueueResponse(total: total, counts: counts, items: items), to: Self.cacheName)
    }

    private func catalogRefresh(_ product: Product) {
        app.catalog.upsert(product)
    }
}
