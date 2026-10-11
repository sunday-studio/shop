import Foundation
import Observation

/// A change made to the list on the phone, replayed against the server in order.
nonisolated struct ListOp: Codable, Sendable, Identifiable, Hashable {
    enum Kind: String, Codable, Sendable {
        case add, update, check, uncheck, delete, clearChecked
        /// `itemId` is the suggestion id (acceptSuggestion, dismissSuggestion) or the receipt id (undoReceiptChecks).
        case acceptSuggestion, dismissSuggestion, undoReceiptChecks
        /// Groups: `itemId` is the group id (a negative temporary id until it reaches the server).
        case addGroup, updateGroup, deleteGroup
        /// `itemId` is the item; `groupId` the target checklist (nil = the main list).
        case moveItem
    }

    var id = UUID()
    var kind: Kind
    /// Server id, or a negative temporary id for items added offline.
    var itemId: Int64
    var input: ListItemInput?
    var createdAt = Date()
    /// addGroup: the group with its lines, and the temporary ids of the local items made for them.
    var group: ListGroupInput?
    var tempItemIds: [Int64]?
    /// updateGroup
    var patch: ListGroupPatch?
    /// deleteGroup: move its items to the main list (true) or delete them.
    var keepItems: Bool?
    /// moveItem target.
    var groupId: Int64?
}

/// Offline-first shopping list (PLAN §9 List). Every change applies locally first and is
/// queued; `sync()` replays the queue and then refetches the server's list.
@MainActor
@Observable
final class ShoppingListStore {
    /// Every item: the main list (`groupId == nil`) and the checklists' items.
    private(set) var items: [ListItem] = []
    /// Checklists and templates (templates without items: those are read on demand).
    private(set) var groups: [ListGroup] = []
    /// The server's list write counter at the last sync (`since_rev` for the next one).
    private(set) var rev: Int64 = 0
    private(set) var ops: [ListOp] = []
    private(set) var isSyncing = false
    /// "Looks bought?" suggestions from confirmed receipts; the server's open set replaces this on every sync.
    private(set) var suggestions: [ListSuggestion] = []
    private(set) var lastSync: Date?
    private(set) var lastError: APIError?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private let catalog: Catalog

    private static let itemsFile = "list.json"
    private static let opsFile = "list-ops.json"
    private static let suggestionsFile = "list-suggestions.json"
    private static let groupsFile = "list-groups.json"
    private static let revKey = "list.rev"
    private static let tempKey = "list.nextTempId"

    /// The main list's items.
    var mainItems: [ListItem] { items.filter { $0.groupId == nil } }
    var open: [ListItem] { mainItems.filter { !$0.isChecked } }
    /// The main list's checked items, those a receipt checked first (newest receipt first).
    var checked: [ListItem] {
        mainItems.filter(\.isChecked).sorted { a, b in
            switch (a.isReceiptChecked, b.isReceiptChecked) {
            case (true, false): return true
            case (false, true): return false
            case (true, true): return (a.checkedReceiptId ?? 0) > (b.checkedReceiptId ?? 0)
            default: return false
            }
        }
    }
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
        suggestions = DiskStore.load([ListSuggestion].self, from: Self.suggestionsFile) ?? []
        groups = DiskStore.load([ListGroup].self, from: Self.groupsFile) ?? []
        rev = Int64(UserDefaults.standard.integer(forKey: Self.revKey))
        lastGoodSync = UserDefaults.standard.object(forKey: Self.lastSyncKey) as? Date
    }

    private static let lastSyncKey = "list.lastGoodSync"
    /// Last time the list was refetched successfully, kept across launches (`lastSync` is per launch).
    private(set) var lastGoodSync: Date?

    /// The saved list is more than an hour old (or was never synced): worth a banner after a failure.
    var isStale: Bool { lastGoodSync.map { Date().timeIntervalSince($0) > 3600 } ?? true }

    // MARK: Local changes

    func add(text: String, productId: Int64? = nil, qty: Double? = nil, note: String? = nil, groupId: Int64? = nil) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty || productId != nil else { return }
        // Re-adding an open product bumps its quantity (same as the server, per group).
        if let productId, let i = items.firstIndex(where: { $0.productId == productId && !$0.isChecked && $0.groupId == groupId }) {
            let newQty = (items[i].qty ?? 1) + (qty ?? 1)
            items[i].qty = newQty
            enqueue(ListOp(kind: .update, itemId: items[i].id, input: input(for: items[i])))
            persist()
            return
        }
        let product = catalog.product(productId)
        let temp = nextTempId()
        let item = ListItem(id: temp, text: trimmed.isEmpty ? (product?.name ?? "") : trimmed, productId: productId,
                            productName: product?.name, qty: qty, note: note, checkedAt: nil, createdAt: .now, cheapest: nil, groupId: groupId)
        items.insert(item, at: 0)
        enqueue(ListOp(kind: .add, itemId: temp, input: ListItemInput(text: item.text, productId: productId, qty: qty, note: note, clientId: UUID().uuidString, groupId: groupId)))
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
        let tempChecked = items.filter { $0.isChecked && $0.id < 0 && $0.groupId == nil }.map(\.id)
        items.removeAll { $0.isChecked && $0.groupId == nil }
        ops.removeAll { tempChecked.contains($0.itemId) }
        enqueue(ListOp(kind: .clearChecked, itemId: 0))
        persist()
    }

    // MARK: Groups

    /// Checklists to show, in order: the open ones by `sort`, finished ones last. Archived are not listed.
    var checklists: [ListGroup] {
        let live = groups.filter { !$0.isTemplate && !$0.isArchived }.sorted { ($0.sort, $0.id) < ($1.sort, $1.id) }
        return live.filter { !isGroupDone($0.id) } + live.filter { isGroupDone($0.id) }
    }
    var archivedGroups: [ListGroup] { groups.filter { !$0.isTemplate && $0.isArchived }.sorted { ($0.archivedAt ?? .distantPast) > ($1.archivedAt ?? .distantPast) } }

    func groupItems(_ id: Int64) -> [ListItem] { items.filter { $0.groupId == id } }
    func group(_ id: Int64) -> ListGroup? { groups.first { $0.id == id } }

    /// Counts come from the phone's own copy (they can't go stale).
    func progress(_ id: Int64) -> (checked: Int, total: Int) {
        let all = groupItems(id)
        return (all.filter(\.isChecked).count, all.count)
    }

    func isGroupDone(_ id: Int64) -> Bool {
        let p = progress(id)
        return p.total > 0 && p.checked == p.total
    }

    /// Where else the same product is open ("Also in BBQ"), derived from the local copy so it is always current.
    func alsoIn(_ item: ListItem) -> [String] {
        guard let productId = item.productId, !item.isChecked else { return [] }
        var names: [String] = []
        for other in items where other.productId == productId && !other.isChecked && other.id != item.id {
            if let g = other.groupId { if let name = group(g)?.name { names.append(name) } } else { names.append("your list") }
        }
        return Array(Set(names)).sorted()
    }

    /// New checklist with its lines, made at once (works offline). Returns the (temporary) group id.
    @discardableResult
    func createGroup(name: String, lines: [String]) -> Int64 {
        let cleanName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let tempGroup = nextTempId()
        let nextSort = (groups.map(\.sort).max() ?? 0) + 1
        var tempIds: [Int64] = []
        var inputs: [ListItemInput] = []
        for line in lines {
            let text = line.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { continue }
            let tempItem = nextTempId()
            tempIds.append(tempItem)
            inputs.append(ListItemInput(text: text))
            items.insert(ListItem(id: tempItem, text: text, productId: nil, productName: nil, qty: nil, note: nil, checkedAt: nil, createdAt: .now, cheapest: nil, groupId: tempGroup), at: 0)
        }
        groups.append(ListGroup(id: tempGroup, name: cleanName, kind: "checklist", source: "user", sort: nextSort, createdAt: .now, updatedAt: .now))
        var op = ListOp(kind: .addGroup, itemId: tempGroup)
        op.group = ListGroupInput(clientId: UUID().uuidString, name: cleanName, sort: nextSort, items: inputs)
        op.tempItemIds = tempIds
        enqueue(op)
        persist()
        return tempGroup
    }

    func renameGroup(_ id: Int64, to name: String) {
        guard let i = groups.firstIndex(where: { $0.id == id }) else { return }
        let clean = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !clean.isEmpty else { return }
        groups[i].name = clean
        if id < 0, let j = ops.firstIndex(where: { $0.kind == .addGroup && $0.itemId == id }) {
            ops[j].group?.name = clean
        } else {
            var op = ListOp(kind: .updateGroup, itemId: id)
            op.patch = ListGroupPatch(name: clean)
            enqueue(op)
        }
        persist()
    }

    func archiveGroup(_ id: Int64, _ archived: Bool) {
        guard let i = groups.firstIndex(where: { $0.id == id }) else { return }
        groups[i].archivedAt = archived ? .now : nil
        var op = ListOp(kind: .updateGroup, itemId: id)
        op.patch = ListGroupPatch(archived: archived)
        enqueue(op)
        persist()
    }

    /// Everything needed to put a deleted group back.
    struct DeletedGroup {
        var group: ListGroup
        var items: [ListItem]
        var keptItems: Bool
        /// The delete reached the server (or was queued): Undo has to ask the server.
        var removedOps: [ListOp]
        var deleteOpId: UUID?
    }

    /// Deletes a group; `keepItems` moves its items to the main list, otherwise they go too.
    func deleteGroup(_ id: Int64, keepItems: Bool) -> DeletedGroup? {
        guard let g = group(id) else { return nil }
        let members = groupItems(id)
        var snapshot = DeletedGroup(group: g, items: members, keptItems: keepItems, removedOps: [], deleteOpId: nil)
        groups.removeAll { $0.id == id }
        if keepItems {
            for i in items.indices where items[i].groupId == id { items[i].groupId = nil }
        } else {
            items.removeAll { $0.groupId == id }
        }
        if id < 0 {
            // Never reached the server: forget it and everything queued for it.
            let memberIds = Set(members.map(\.id))
            snapshot.removedOps = ops.filter { ($0.kind == .addGroup && $0.itemId == id) || ($0.kind != .addGroup && ($0.input?.groupId == id || memberIds.contains($0.itemId) || $0.itemId == id)) }
            let removedIds = Set(snapshot.removedOps.map(\.id))
            ops.removeAll { removedIds.contains($0.id) }
            if keepItems {
                // The items stay as plain main-list items: give them their own adds back.
                for m in members where m.id < 0 {
                    enqueue(ListOp(kind: .add, itemId: m.id, input: ListItemInput(text: m.text, productId: m.productId, qty: m.qty, note: m.note, clientId: UUID().uuidString)))
                }
            }
        } else {
            var op = ListOp(kind: .deleteGroup, itemId: id)
            op.keepItems = keepItems
            snapshot.deleteOpId = op.id
            enqueue(op)
        }
        persist()
        return snapshot
    }

    /// Undo of `deleteGroup`: still queued → just cancel it; already sent → the server restores it.
    func restoreGroup(_ snapshot: DeletedGroup) async {
        let g = snapshot.group
        if g.id < 0 || (snapshot.deleteOpId.map { id in ops.contains { $0.id == id } } ?? false) {
            if let opId = snapshot.deleteOpId { ops.removeAll { $0.id == opId } }
            groups.append(g)
            if snapshot.keptItems {
                let memberIds = Set(snapshot.items.map(\.id))
                for i in items.indices where memberIds.contains(items[i].id) { items[i].groupId = g.id }
                // Their temporary adds (made when a never-synced group was kept) are no longer needed as main-list adds.
                if g.id < 0 { ops.removeAll { $0.kind == .add && memberIds.contains($0.itemId) } }
            } else {
                items.append(contentsOf: snapshot.items)
            }
            ops.append(contentsOf: snapshot.removedOps)
            persist()
            return
        }
        // Wait for the delete to land, then ask the server to put it back.
        await sync()
        do {
            let restored = try await api.restoreListGroup(g.id)
            mergeGroup(restored)
            for item in restored.items ?? [] { merge(item) }
            persist()
        } catch {
            lastError = error as? APIError
        }
    }

    func moveItem(_ item: ListItem, to groupId: Int64?) {
        guard let i = items.firstIndex(where: { $0.id == item.id }), items[i].groupId != groupId else { return }
        items[i].groupId = groupId
        var op = ListOp(kind: .moveItem, itemId: item.id)
        op.groupId = groupId
        enqueue(op)
        persist()
    }

    /// Templates (online): the server keeps them; their items are read on demand.
    func templates() async throws -> [ListGroup] { try await api.listGroups(kind: "template") }

    /// Start a checklist from a template (online). Returns the new group.
    func startList(from template: ListGroup, name: String) async throws -> ListGroup {
        let group = try await api.instantiateListTemplate(template.id, ListGroupCopyInput(clientId: UUID().uuidString, name: name))
        mergeGroup(group)
        for item in group.items ?? [] { merge(item) }
        persist()
        await sync()
        return group
    }

    func saveAsTemplate(_ group: ListGroup, name: String) async throws {
        _ = try await api.saveListGroupAsTemplate(group.id, ListGroupCopyInput(clientId: UUID().uuidString, name: name))
    }

    private func mergeGroup(_ g: ListGroup) {
        var copy = g
        copy.items = nil
        copy.itemIds = nil
        if let i = groups.firstIndex(where: { $0.id == g.id }) { groups[i] = copy } else { groups.append(copy) }
    }

    // MARK: Receipt checks and suggestions

    /// Confirming a receipt answered with what it did to the list: merge it in right away.
    func receiptConfirmed(_ result: ReceiptListChecks) {
        for item in result.checked + result.reduced { merge(item) }
        var known = Set(suggestions.map(\.id))
        for s in result.suggestions where known.insert(s.id).inserted { suggestions.append(s) }
        persist()
    }

    /// "Yes" on a suggestion: the item is checked as bought on that receipt, and it works offline.
    func accept(_ suggestion: ListSuggestion) {
        suggestions.removeAll { $0.id == suggestion.id }
        if let i = items.firstIndex(where: { $0.id == suggestion.itemId }) {
            items[i].checkedAt = .now
            items[i].checkSource = "receipt"
            items[i].checkedReceiptId = suggestion.receiptId
            items[i].checkedLineId = suggestion.lineId
        }
        enqueue(ListOp(kind: .acceptSuggestion, itemId: suggestion.id))
        persist()
    }

    /// "Not this": hidden for good.
    func dismiss(_ suggestion: ListSuggestion) {
        suggestions.removeAll { $0.id == suggestion.id }
        enqueue(ListOp(kind: .dismissSuggestion, itemId: suggestion.id))
        persist()
    }

    /// Items this receipt still owns (checked because of it), for the receipt screen's Undo row.
    func receiptCheckedItems(_ receiptId: Int64) -> [ListItem] {
        items.filter { $0.isChecked && $0.checkedReceiptId == receiptId && $0.checkSource == "receipt" }
    }

    /// Undo what a receipt did: its items reopen at once; the server puts quantities back at sync.
    func undoReceiptChecks(_ receiptId: Int64) {
        for i in items.indices where items[i].checkedReceiptId == receiptId && items[i].checkSource == "receipt" {
            items[i].checkedAt = nil
            items[i].checkSource = nil
            items[i].checkedReceiptId = nil
            items[i].checkedLineId = nil
        }
        suggestions.removeAll { $0.receiptId == receiptId }
        enqueue(ListOp(kind: .undoReceiptChecks, itemId: receiptId))
        persist()
    }

    private func merge(_ item: ListItem) {
        if let i = items.firstIndex(where: { $0.id == item.id }) { items[i] = item } else { items.insert(item, at: 0) }
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
            try await fetchChanges()
            lastSync = .now
            lastGoodSync = .now
            UserDefaults.standard.set(Date.now, forKey: Self.lastSyncKey)
            persist()
        } catch let error as APIError {
            lastError = error
        } catch {}
    }

    /// Pulls what changed on the server since our `rev` (the cursor that can't miss or repeat a write).
    /// `full_resync` means the answer is the complete list and replaces the local copy.
    private func fetchChanges() async throws {
        var changes: ListChanges
        do {
            changes = try await api.listChanges(sinceRev: rev)
        } catch APIError.problem(let problem) where problem.status == 400 {
            // The cursor is ahead of the server (a restored backup): start again from nothing.
            changes = try await api.listChanges(sinceRev: 0)
        } catch APIError.notFound {
            // A server from before groups: the flat list is all there is.
            items = try await api.shoppingList(includeChecked: true)
            groups = []
            if let open = try? await api.listSuggestions() { suggestions = open }
            return
        }
        if changes.fullResync {
            items = changes.items
            groups = changes.groups.map { var g = $0; g.items = nil; return g }
        } else {
            let gone = Set(changes.deletedIds)
            items.removeAll { gone.contains($0.id) }
            for item in changes.items { merge(item) }
            let goneGroups = Set(changes.deletedGroupIds)
            groups.removeAll { goneGroups.contains($0.id) }
            for g in changes.groups { mergeGroup(g) }
        }
        rev = changes.rev
        UserDefaults.standard.set(Int(changes.rev), forKey: Self.revKey)
        // The open suggestions replace the local copy (never merged).
        if let open = changes.suggestions { suggestions = open } else if let open = try? await api.listSuggestions() { suggestions = open }
    }

    private func apply(_ op: ListOp) async throws {
        switch op.kind {
        case .addGroup:
            guard let input = op.group else { return }
            do {
                let created = try await api.createListGroup(input)
                // Same order as the request: the server's ids replace our temporary ones.
                let realItems = created.items ?? []
                for (n, temp) in (op.tempItemIds ?? []).enumerated() {
                    guard let realId = created.itemIds?[safe: n], let real = realItems.first(where: { $0.id == realId }) else { continue }
                    remap(temp: temp, to: real)
                }
                remapGroup(temp: op.itemId, to: created.id)
                mergeGroup(created)
            } catch let error as APIError where !(error.isConnectivity || error == .unauthorized) {
                // Refused (for example a group deleted elsewhere): drop the local copy.
                items.removeAll { $0.groupId == op.itemId }
                groups.removeAll { $0.id == op.itemId }
                throw error
            }
        case .updateGroup:
            guard op.itemId > 0, let patch = op.patch else { return }
            mergeGroup(try await api.updateListGroup(op.itemId, patch))
        case .deleteGroup:
            guard op.itemId > 0 else { return }
            do {
                let result = try await api.deleteListGroup(op.itemId, keepItems: op.keepItems ?? true)
                for item in result.kept { merge(item) }
            } catch APIError.notFound {}
        case .moveItem:
            guard op.itemId > 0, op.groupId.map({ $0 > 0 }) ?? true else { return }
            let moved = try await api.moveListItem(op.itemId, groupId: op.groupId)
            if moved.id != op.itemId { items.removeAll { $0.id == op.itemId } }
            merge(moved)
        case .add:
            guard let input = op.input else { return }
            // A checklist made offline reaches the server first (ops run in order); if it never did, the item can't go in it.
            if let g = input.groupId, g < 0 { return }
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
        case .acceptSuggestion:
            do { merge(try await api.acceptListSuggestion(op.itemId)) } catch APIError.conflict(_) {} catch APIError.notFound {}
        case .dismissSuggestion:
            do { try await api.dismissListSuggestion(op.itemId) } catch APIError.conflict(_) {} catch APIError.notFound {}
        case .undoReceiptChecks:
            let result = try await api.undoReceiptListChecks(op.itemId)
            for item in result.restored { merge(item) }
        }
    }

    /// Swaps a temporary group id for the server's in local items, groups and queued ops.
    private func remapGroup(temp: Int64, to real: Int64) {
        for i in items.indices where items[i].groupId == temp { items[i].groupId = real }
        for i in groups.indices where groups[i].id == temp { groups[i].id = real }
        for j in ops.indices {
            if ops[j].input?.groupId == temp { ops[j].input?.groupId = real }
            if ops[j].groupId == temp { ops[j].groupId = real }
            if (ops[j].kind == .updateGroup || ops[j].kind == .deleteGroup), ops[j].itemId == temp { ops[j].itemId = real }
        }
        persist()
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
        DiskStore.save(suggestions, to: Self.suggestionsFile)
        DiskStore.save(groups, to: Self.groupsFile)
    }

    func reset() {
        items = []
        ops = []
        suggestions = []
        groups = []
        rev = 0
        UserDefaults.standard.removeObject(forKey: Self.revKey)
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

private extension Array {
    subscript(safe index: Int) -> Element? { indices.contains(index) ? self[index] : nil }
}
