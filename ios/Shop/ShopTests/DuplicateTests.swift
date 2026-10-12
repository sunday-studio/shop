import XCTest
@testable import Shop

@MainActor
final class DuplicateReceiptTests: XCTestCase {
    private func line(_ no: Int, _ text: String, _ cents: Int64 = 100, kind: LineKind = .item) -> ReceiptLine {
        ReceiptLine(id: Int64(no), receiptId: 1, lineNo: no, rawText: text, kind: kind, qty: 1, unit: .pc, unitPriceCents: cents, lineTotalCents: cents,
                    vatCode: nil, parentLineId: nil, productId: nil, userEdited: false, textEn: nil, suggestions: nil)
    }

    private func api(_ handler: @escaping (ScriptedTransport.Call) throws -> (Int, String)) -> (APIClient, ScriptedTransport) {
        let transport = ScriptedTransport(handler)
        return (APIClient(transport: transport, baseURL: URL(string: "http://test.local"), token: "t"), transport)
    }

    func testReceiptDecodesPossibleDuplicate() async throws {
        let body = """
        {"id":111,"shop_id":1,"purchased_at":"2026-10-10T16:12:00Z","total_cents":2245,"status":"ok","review_reasons":[],
         "created_at":"2026-10-10T16:13:00Z","updated_at":"2026-10-10T16:13:00Z",
         "possible_duplicate_of":{"receipt_id":104,"purchased_at":"2026-10-10T16:10:00Z","total_cents":2245,"shop":{"id":1,"name":"Albert Heijn"},
           "shop_name":"Albert Heijn","match":"total+date+lines","shared_lines":10,"item_count":11,"other_item_count":11,"time_matches":true}}
        """
        let (api, _) = api { _ in (200, body) }
        let r = try await api.receipt(111)
        let d = try XCTUnwrap(r.possibleDuplicateOf)
        XCTAssertEqual(d.receiptId, 104)
        XCTAssertTrue(d.isStrong)
        XCTAssertTrue(d.timeMatches)
        XCTAssertEqual(d.shop?.name, "Albert Heijn")
    }

    func testUnassignedShopAndAbsentField() async throws {
        let withDup = """
        {"id":5,"status":"ok","review_reasons":[],"created_at":"2026-10-10T16:13:00Z","updated_at":"2026-10-10T16:13:00Z",
         "possible_duplicate_of":{"receipt_id":4,"total_cents":500,"match":"total+date","shared_lines":0,"item_count":3,"other_item_count":4,"time_matches":false}}
        """
        let without = "{\"id\":6,\"status\":\"ok\",\"review_reasons\":[],\"created_at\":\"2026-10-10T16:13:00Z\",\"updated_at\":\"2026-10-10T16:13:00Z\"}"
        let (api, _) = api { c in c.path.hasSuffix("/5") ? (200, withDup) : (200, without) }
        let a = try await api.receipt(5)
        XCTAssertNil(a.possibleDuplicateOf?.shop)
        XCTAssertNil(a.possibleDuplicateOf?.purchasedAt)
        XCTAssertFalse(try XCTUnwrap(a.possibleDuplicateOf).isStrong)
        let b = try await api.receipt(6)
        XCTAssertNil(b.possibleDuplicateOf)
    }

    func testDateLabelAddsTheYearOnlyWhenNotThisYear() {
        let now = Dates.calendar.date(from: DateComponents(year: 2026, month: 10, day: 12, hour: 12))!
        let aug = Dates.calendar.date(from: DateComponents(year: 2026, month: 8, day: 2, hour: 12))!
        let old = Dates.calendar.date(from: DateComponents(year: 2025, month: 8, day: 2, hour: 12))!
        XCTAssertEqual(DuplicateText.date(aug, now: now), "2 Aug")
        XCTAssertEqual(DuplicateText.date(old, now: now), "2 Aug 2025")
    }

    func testRowsMatchByNameAndSeparateTheRest() {
        let a = [line(1, "MELK"), line(2, "BANANEN"), line(3, "Kaas  oud"), line(4, "DEPOSIT", kind: .deposit)]
        let b = [line(1, "melk"), line(2, "KAAS OUD"), line(3, "EIEREN")]
        let rows = DuplicateCompare.rows(this: a, other: b)
        XCTAssertEqual(rows.filter { $0.side == .both }.map(\.text), ["MELK", "Kaas  oud"])
        XCTAssertEqual(rows.filter { $0.side == .thisOnly }.map(\.text), ["BANANEN"])
        XCTAssertEqual(rows.filter { $0.side == .otherOnly }.map(\.text), ["EIEREN"])
        XCTAssertEqual(DuplicateCompare.differing(rows), 2)
    }

    func testRepeatedNamesMatchOneForOne() {
        let rows = DuplicateCompare.rows(this: [line(1, "MELK"), line(2, "MELK")], other: [line(1, "MELK")])
        XCTAssertEqual(rows.filter { $0.side == .both }.count, 1)
        XCTAssertEqual(rows.filter { $0.side == .thisOnly }.count, 1)
    }

    func testKeepBothWaitsOfflineAndIsSentWhenBackOnline() async {
        DiskStore.remove("duplicate-decisions.json")
        var online = false
        let (api, transport) = api { _ in
            if !online { throw URLError(.notConnectedToInternet) }
            return (204, "")
        }
        let decisions = DuplicateDecisions()
        decisions.set(7, keep: true, api: api)
        try? await Task.sleep(for: .milliseconds(200))
        XCTAssertTrue(decisions.isKept(7), "stays applied while offline")
        online = true
        await decisions.flush(api: api)
        XCTAssertFalse(decisions.isKept(7))
        XCTAssertEqual(transport.paths("POST").last, "POST /v1/receipts/7/duplicate/keep")
    }

    func testUndoReplacesAPendingKeep() async {
        DiskStore.remove("duplicate-decisions.json")
        let (api, transport) = api { _ in throw URLError(.notConnectedToInternet) }
        let decisions = DuplicateDecisions()
        decisions.set(8, keep: true, api: api)
        decisions.set(8, keep: false, api: api)
        XCTAssertFalse(decisions.isKept(8))
        XCTAssertEqual(decisions.pending["8"], false)
        _ = transport
    }
}
