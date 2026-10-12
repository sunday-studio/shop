import XCTest
@testable import Shop

@MainActor
final class ListStoreTests: XCTestCase {
    private let emptyChanges = JSON.changes(rev: 1, full: true)

    func testOfflineAddGetsATemporaryIdAndQueuesOneOp() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        store.add(text: "eggs")
        XCTAssertEqual(store.items.count, 1)
        XCTAssertLessThan(store.items[0].id, 0)
        XCTAssertEqual(store.pendingCount, 1)
        XCTAssertEqual(store.ops[0].kind, .add)
    }

    func testCheckAndUncheckOfOneItemCollapse() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        store.add(text: "eggs")
        let item = store.items[0]
        store.setChecked(item, true)
        store.setChecked(store.items[0], false)
        XCTAssertEqual(store.ops.map(\.kind), [.add, .uncheck])
    }

    func testDeletingAnItemThatNeverSyncedDropsItsOps() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        store.add(text: "eggs")
        store.delete(store.items[0])
        XCTAssertTrue(store.items.isEmpty)
        XCTAssertTrue(store.ops.isEmpty)
    }

    func testSyncSendsTheAddAndRemapsTheTemporaryId() async {
        let (store, transport) = makeListStore { call in
            if call.method == "POST" && call.path == "/v1/list" { return (201, JSON.item(77, "eggs", nil)) }
            if call.path == "/v1/list/changes" { return (200, JSON.changes(items: [JSON.item(77, "eggs", nil)], rev: 3, full: true)) }
            if call.path == "/v1/list/suggestions" { return (200, "[]") }
            return (404, "{\"title\":\"x\",\"status\":404}")
        }
        store.add(text: "eggs")
        await store.sync()
        XCTAssertEqual(store.items.map(\.id), [77])
        XCTAssertTrue(store.ops.isEmpty)
        XCTAssertEqual(transport.paths("POST"), ["POST /v1/list"])
        XCTAssertTrue(transport.calls.first { $0.method == "POST" }!.body.contains("client_id"), "adds are idempotent")
    }

    func testOfflineSyncKeepsTheQueue() async {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        store.add(text: "eggs")
        await store.sync()
        XCTAssertEqual(store.pendingCount, 1)
        XCTAssertNotNil(store.lastError)
    }

    func testSyncUsesSinceRevAndStoresTheNewRev() async {
        var asked: [String] = []
        let (store, _) = makeListStore { call in
            if call.path == "/v1/list/changes" {
                asked.append(call.query)
                return (200, JSON.changes(items: [JSON.item(1, "milk", nil)], rev: asked.count == 1 ? 5 : 8, full: asked.count == 1))
            }
            return (200, "[]")
        }
        await store.sync()
        XCTAssertEqual(store.rev, 5)
        await store.sync()
        XCTAssertEqual(store.rev, 8)
        XCTAssertEqual(asked, ["since_rev=0", "since_rev=5"])
    }

    func testFullResyncReplacesTheLocalCopyAndDeltasApply() async {
        var step = 0
        let (store, _) = makeListStore { call in
            guard call.path == "/v1/list/changes" else { return (200, "[]") }
            step += 1
            switch step {
            case 1: return (200, JSON.changes(items: [JSON.item(1, "milk", nil), JSON.item(2, "bread", nil)], groups: [JSON.group(5, "BBQ")], rev: 4, full: true))
            case 2: return (200, JSON.changes(items: [JSON.item(3, "burgers", 5)], deleted: [1], rev: 6))
            default: return (200, JSON.changes(items: [JSON.item(9, "only this", nil)], rev: 20, full: true))
            }
        }
        await store.sync()
        XCTAssertEqual(Set(store.items.map(\.id)), [1, 2])
        XCTAssertEqual(store.groups.map(\.name), ["BBQ"])
        await store.sync()
        XCTAssertEqual(Set(store.items.map(\.id)), [2, 3])
        XCTAssertEqual(store.groupItems(5).map(\.text), ["burgers"])
        await store.sync()
        XCTAssertEqual(store.items.map(\.id), [9], "a full resync replaces everything")
    }

    func testACursorAheadOfTheServerRestartsFromZero() async {
        var asked: [String] = []
        let (store, _) = makeListStore { call in
            guard call.path == "/v1/list/changes" else { return (200, "[]") }
            asked.append(call.query)
            switch asked.count {
            case 1: return (200, JSON.changes(items: [JSON.item(1, "milk", nil)], rev: 5, full: true))
            case 2: return (400, "{\"title\":\"since_rev is ahead of the server\",\"status\":400}")
            default: return (200, JSON.changes(items: [JSON.item(2, "bread", nil)], rev: 2, full: true))
            }
        }
        await store.sync()
        XCTAssertEqual(store.rev, 5)
        await store.sync()
        XCTAssertEqual(asked, ["since_rev=0", "since_rev=5", "since_rev=0"])
        XCTAssertEqual(store.rev, 2)
        XCTAssertEqual(store.items.map(\.id), [2])
    }

    func testCreateGroupOfflineThenSyncMapsTemporaryIds() async {
        let (store, transport) = makeListStore { call in
            if call.method == "POST" && call.path == "/v1/list/groups" {
                let created = "{\"id\":50,\"name\":\"BBQ\",\"kind\":\"checklist\",\"source\":\"user\",\"sort\":1,\"created_at\":\"2026-10-11T10:00:00Z\",\"updated_at\":\"2026-10-11T10:00:00Z\",\"rev\":2,\"item_count\":2,\"checked_count\":0,\"item_ids\":[501,502],\"items\":[\(JSON.item(501, "burgers", 50)),\(JSON.item(502, "buns", 50))]}"
                return (201, created)
            }
            if call.path == "/v1/list/changes" { return (200, JSON.changes(items: [JSON.item(501, "burgers", 50), JSON.item(502, "buns", 50)], groups: [JSON.group(50, "BBQ")], rev: 3, full: true)) }
            return (200, "[]")
        }
        let temp = store.createGroup(name: "BBQ", lines: ["burgers", "buns"])
        XCTAssertLessThan(temp, 0)
        XCTAssertEqual(store.groupItems(temp).count, 2)
        XCTAssertEqual(store.ops.count, 1, "the group and its lines are one atomic op")
        await store.sync()
        XCTAssertEqual(store.groups.map(\.id), [50])
        XCTAssertEqual(Set(store.groupItems(50).map(\.id)), [501, 502])
        XCTAssertTrue(store.ops.isEmpty)
        XCTAssertEqual(transport.paths("POST"), ["POST /v1/list/groups"])
    }

    func testQueuedOpsForATemporaryGroupAreRemappedAfterItIsCreated() async {
        let (store, transport) = makeListStore { call in
            if call.method == "POST" && call.path == "/v1/list/groups" {
                return (201, "{\"id\":50,\"name\":\"BBQ\",\"kind\":\"checklist\",\"source\":\"user\",\"sort\":1,\"created_at\":\"2026-10-11T10:00:00Z\",\"updated_at\":\"2026-10-11T10:00:00Z\",\"rev\":2,\"item_count\":1,\"checked_count\":0,\"item_ids\":[501],\"items\":[\(JSON.item(501, "burgers", 50))]}")
            }
            if call.method == "POST" && call.path == "/v1/list" { return (201, JSON.item(600, "charcoal", 50)) }
            if call.path == "/v1/list/changes" { return (200, JSON.changes(rev: 3, full: true)) }
            return (200, "[]")
        }
        let temp = store.createGroup(name: "BBQ", lines: ["burgers"])
        store.add(text: "charcoal", groupId: temp)
        XCTAssertEqual(store.ops.map(\.kind), [.addGroup, .add])
        await store.sync()
        let itemAdd = transport.calls.first { $0.method == "POST" && $0.path == "/v1/list" }
        XCTAssertNotNil(itemAdd)
        XCTAssertTrue(itemAdd!.body.contains("\"group_id\":50"), "the item goes into the real group id: \(itemAdd!.body)")
    }

    func testDeletingAGroupThatNeverSyncedForgetsItAndItsOps() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        let temp = store.createGroup(name: "BBQ", lines: ["burgers"])
        let snapshot = store.deleteGroup(temp, keepItems: false)
        XCTAssertNotNil(snapshot)
        XCTAssertTrue(store.groups.isEmpty)
        XCTAssertTrue(store.items.isEmpty)
        XCTAssertTrue(store.ops.isEmpty)
    }

    func testKeepingItemsOfAnUnsyncedGroupMovesThemToTheMainList() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        let temp = store.createGroup(name: "BBQ", lines: ["burgers", "buns"])
        _ = store.deleteGroup(temp, keepItems: true)
        XCTAssertEqual(store.mainItems.count, 2)
        XCTAssertEqual(store.ops.map(\.kind), [.add, .add], "each kept line becomes a plain add")
    }

    func testProgressAndDoneComeFromTheLocalCopy() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        let temp = store.createGroup(name: "BBQ", lines: ["a", "b"])
        XCTAssertEqual(store.progress(temp).total, 2)
        XCTAssertFalse(store.isGroupDone(temp))
        for item in store.groupItems(temp) { store.setChecked(item, true) }
        XCTAssertTrue(store.isGroupDone(temp))
    }

    func testClearCheckedOnlyTouchesTheMainList() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        let temp = store.createGroup(name: "BBQ", lines: ["a"])
        store.add(text: "milk")
        for item in store.items { store.setChecked(item, true) }
        store.clearChecked()
        XCTAssertEqual(store.items.map(\.text), ["a"])
        XCTAssertEqual(store.groupItems(temp).count, 1)
    }

    func testAcceptAndDismissSuggestionsQueueOps() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        store.add(text: "oat milk")
        let id = store.items[0].id
        let suggestion = ListSuggestion(id: 4, itemId: id, itemText: "oat milk", receiptId: 9, lineId: nil, lineText: "HAVERMELK", score: 0.8, reason: "match", createdAt: .now)
        store.receiptConfirmed(ReceiptListChecks(checked: [], reduced: [], suggestions: [suggestion]))
        XCTAssertEqual(store.suggestions.count, 1)
        store.accept(suggestion)
        XCTAssertTrue(store.suggestions.isEmpty)
        XCTAssertTrue(store.items[0].isReceiptChecked)
        XCTAssertEqual(store.ops.last?.kind, .acceptSuggestion)
    }

    func testUndoReceiptChecksReopensItsItems() {
        let (store, _) = makeListStore { _ in throw URLError(.notConnectedToInternet) }
        var item = ListItem(id: 1, text: "milk", productId: nil, productName: nil, qty: nil, note: nil, checkedAt: .now, createdAt: .now, cheapest: nil)
        item.checkSource = "receipt"; item.checkedReceiptId = 9
        store.receiptConfirmed(ReceiptListChecks(checked: [item], reduced: [], suggestions: []))
        XCTAssertEqual(store.receiptCheckedItems(9).count, 1)
        store.undoReceiptChecks(9)
        XCTAssertTrue(store.receiptCheckedItems(9).isEmpty)
        XCTAssertFalse(store.items[0].isChecked)
        XCTAssertEqual(store.ops.last?.kind, .undoReceiptChecks)
    }
}
