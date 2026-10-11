import Foundation
import Observation

/// A change made to the list on the phone, replayed against the server in order.
nonisolated struct ListOp: Codable, Sendable, Identifiable, Hashable {
    enum Kind: String, Codable, Sendable {
        case add, update, check, uncheck, delete, clearChecked
    }

    var id = UUID()
    var kind: Kind
    /// Server id, or a negative temporary id for items added offline.
    var itemId: Int64
    var input: ListItemInput?
    var createdAt = Date()
}

/// Offline-first shopping list (PLAN §9 List). Every change applies locally first and is
/// queued; `sync()` replays the queue and then refetches the server's list.
@MainActor
@Observable
final class ShoppingListStore {
    private(set) var items: [ListItem] = []
    private(set) var ops: [ListOp] = []
    private(set) var isSyncing = false
    private(set) var lastSync: Date?
    private(set) var lastError: APIError?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private let catalog: Catalog

    private static let itemsFile = "list.json"
    private static let opsFile = "list-ops.json"
    private static let tempKey = "list.nextTempId"

    var open: [ListItem] { items.filter { !$0.isChecked } }
    var checked: [ListItem] { items.filter(\.isChecked) }
    var pendingCount: Int { ops.count }

    /// Sum of cheapest known prices for open items ("about €21").
    var estimateCents: Int64? {
        var total: Int64 = 0
        var any = false
        for item in open {
            guard let cheapest = item.cheapest, let product = catalog.product(item.productId) else { continue }
            let units = Self.baseUnits(qty: item.qty ?? 1, product: product, base: cheapest.baseUnit)
            total += Int64((Double(cheapest.latestUnitCents) * units).rounded())
            any = true
        }
        return any ? total : nil
    }

    init(api: APIClient, catalog: Catalog) {
        self.api = api
        self.catalog = catalog
        items = DiskStore.load([ListItem].self, from: Self.itemsFile) ?? []
        ops = DiskStore.load([ListOp].self, from: Self.opsFile) ?? []
        lastGoodSync = UserDefaults.standard.object(forKey: Self.lastSyncKey) as? Date
    }

    private static let lastSyncKey = "list.lastGoodSync"
    /// Last time the list was refetched successfully, kept across launches (`lastSync` is per launch).
    private(set) var lastGoodSync: Date?

    /// The saved list is more than an hour old (or was never synced): worth a banner after a failure.
    var isStale: Bool { lastGoodSync.map { Date().timeIntervalSince($0) > 3600 } ?? true }

    // MARK: Local changes

    func add(text: String, productId: Int64? = nil, qty: Double? = nil, note: String? = nil) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty || productId != nil else { return }
        // Re-adding an open product bumps its quantity (same as the server).
        if let productId, let i = items.firstIndex(where: { $0.productId == productId && !$0.isChecked }) {
            let newQty = (items[i].qty ?? 1) + (qty ?? 1)
            items[i].qty = newQty
            enqueue(ListOp(kind: .update, itemId: items[i].id, input: input(for: items[i])))
            persist()
            return
        }
        let product = catalog.product(productId)
        let temp = nextTempId()
        let item = ListItem(id: temp, text: trimmed.isEmpty ? (product?.name ?? "") : trimmed, productId: productId,
                            productName: product?.name, qty: qty, note: note, checkedAt: nil, createdAt: .now, cheapest: nil)
        items.insert(item, at: 0)
        enqueue(ListOp(kind: .add, itemId: temp, input: ListItemInput(text: item.text, productId: productId, qty: qty, note: note, clientId: UUID().uuidString)))
        persist()
    }

    func setChecked(_ item: ListItem, _ checked: Bool) {
        guard let i = items.firstIndex(where: { $0.id == item.id }) else { return }
        items[i].checkedAt = checked ? .now : nil
        enqueue(ListOp(kind: checked ? .check : .uncheck, itemId: item.id))
        persist()
    }

    func update(_ item: ListItem, text: String, qty: Double?, note: String?) {
        guard let i = items.firstIndex(where: { $0.id == item.id }) else { return }
        items[i].text = text
        items[i].qty = qty
        items[i].note = note
        if items[i].productId == nil { items[i].productName = nil }
        enqueue(ListOp(kind: .update, itemId: item.id, input: input(for: items[i])))
        persist()
    }

    func delete(_ item: ListItem) {
        items.removeAll { $0.id == item.id }
        if item.id < 0 {
            // Never reached the server: drop its queued ops instead of sending a delete.
            ops.removeAll { $0.itemId == item.id }
        } else {
            enqueue(ListOp(kind: .delete, itemId: item.id))
        }
        persist()
    }

    func clearChecked() {
        let tempChecked = items.filter { $0.isChecked && $0.id < 0 }.map(\.id)
        items.removeAll(where: \.isChecked)
        ops.removeAll { tempChecked.contains($0.itemId) }
        enqueue(ListOp(kind: .clearChecked, itemId: 0))
        persist()
    }

    // MARK: Sync

    func sync() async {
        guard !isSyncing else { return }
        isSyncing = true
        defer { isSyncing = false }
        lastError = nil

        while let op = ops.first {
            do {
                try await apply(op)
                ops.removeFirst()
                persistOps()
            } catch let error as APIError where error.isConnectivity || error == .unauthorized {
                lastError = error
                return
            } catch {
                // The server refused this change (gone, invalid). Drop it and carry on.
                ops.removeFirst()
                persistOps()
            }
        }

        do {
            let fresh = try await api.shoppingList(includeChecked: true)
            items = fresh
            lastSync = .now
            lastGoodSync = .now
            UserDefaults.standard.set(Date.now, forKey: Self.lastSyncKey)
            persist()
        } catch let error as APIError {
            lastError = error
        } catch {}
    }

    private func apply(_ op: ListOp) async throws {
        switch op.kind {
        case .add:
            guard let input = op.input else { return }
            let saved = try await api.addListItem(input)
            remap(temp: op.itemId, to: saved)
        case .update:
            guard op.itemId > 0, let input = op.input else { return }
            _ = try await api.updateListItem(op.itemId, input)
        case .check:
            guard op.itemId > 0 else { return }
            _ = try await api.checkListItem(op.itemId)
        case .uncheck:
            guard op.itemId > 0 else { return }
            _ = try await api.uncheckListItem(op.itemId)
        case .delete:
            guard op.itemId > 0 else { return }
            do { try await api.deleteListItem(op.itemId) } catch APIError.notFound {}
        case .clearChecked:
            _ = try await api.clearCheckedItems()
        }
    }

    /// Swaps a temporary id for the server's in local items and queued ops.
    private func remap(temp: Int64, to saved: ListItem) {
        if let i = items.firstIndex(where: { $0.id == temp }) {
            var merged = saved
            merged.checkedAt = items[i].checkedAt
            items[i] = merged
        }
        // The server may have merged into an existing item: drop the duplicate locally.
        var seen = Set<Int64>()
        items = items.filter { seen.insert($0.id).inserted }
        for j in ops.indices where ops[j].itemId == temp { ops[j].itemId = saved.id }
        persist()
    }

    // MARK: Helpers

    private func enqueue(_ op: ListOp) {
        // Collapse consecutive check/uncheck on the same item.
        if (op.kind == .check || op.kind == .uncheck), let last = ops.last, last.itemId == op.itemId,
           last.kind == .check || last.kind == .uncheck {
            ops.removeLast()
        }
        ops.append(op)
    }

    private func input(for item: ListItem) -> ListItemInput {
        ListItemInput(text: item.text, productId: item.productId, qty: item.qty, note: item.note)
    }

    private func nextTempId() -> Int64 {
        let current = UserDefaults.standard.object(forKey: Self.tempKey) as? Int64 ?? -1
        UserDefaults.standard.set(current - 1, forKey: Self.tempKey)
        return current
    }

    private func persist() {
        DiskStore.save(items, to: Self.itemsFile)
        persistOps()
    }

    private func persistOps() {
        DiskStore.save(ops, to: Self.opsFile)
    }

    func reset() {
        items = []
        ops = []
        persist()
    }

    /// How many base units (kg / l / pc) a list quantity represents.
    static func baseUnits(qty: Double, product: Product, base: BaseUnit) -> Double {
        guard let size = product.netQuantity, let unit = product.netUnit else { return qty }
        switch (unit, base) {
        case ("g", .kg): return qty * size / 1000
        case ("ml", .l): return qty * size / 1000
        case ("pc", .pc): return qty * size
        default: return qty
        }
    }
}
