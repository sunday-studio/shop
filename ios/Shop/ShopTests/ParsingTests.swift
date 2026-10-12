import XCTest
@testable import Shop

@MainActor
final class ParseSizeTests: XCTestCase {
    func testWeightsAndVolumes() {
        XCTAssertTrue(ProductDraft.parseSize("200 g")! == (200, "g"))
        XCTAssertTrue(ProductDraft.parseSize("1,5 l")! == (1500, "ml"))
        XCTAssertTrue(ProductDraft.parseSize("1.5kg")! == (1500, "g"))
        XCTAssertTrue(ProductDraft.parseSize("33 cl")! == (330, "ml"))
        XCTAssertTrue(ProductDraft.parseSize("500ml")! == (500, "ml"))
    }

    func testPieces() {
        XCTAssertTrue(ProductDraft.parseSize("10 st")! == (10, "pc"))
        XCTAssertTrue(ProductDraft.parseSize("6")! == (6, "pc"))
        XCTAssertTrue(ProductDraft.parseSize("12 stuks")! == (12, "pc"))
    }

    func testRejectsNonsense() {
        XCTAssertNil(ProductDraft.parseSize(""))
        XCTAssertNil(ProductDraft.parseSize("lots"))
        XCTAssertNil(ProductDraft.parseSize("5 parsecs"))
    }
}

@MainActor
final class BarcodeTests: XCTestCase {
    func testValidLengths() {
        for code in ["12345678", "123456789012", "8718907400718", "12345678901234"] { XCTAssertTrue(ProductDraft.isValidBarcode(code), code) }
    }

    func testInvalid() {
        for code in ["", "1234567", "123456789012345678901", "87189074007x8", "8718 907400718"] { XCTAssertFalse(ProductDraft.isValidBarcode(code), code) }
    }
}

@MainActor
final class ReceiptIdFromProblemTests: XCTestCase {
    func testTakesTheLastNumberInTheDetail() {
        let problem = Problem(type: nil, title: "duplicate receipt", status: 409, detail: "already uploaded as receipt 42")
        XCTAssertEqual(UploadQueue.receiptId(from: problem), 42)
    }

    func testNilWithoutNumber() {
        XCTAssertNil(UploadQueue.receiptId(from: Problem(type: nil, title: "x", status: 409, detail: "no digits here")))
        XCTAssertNil(UploadQueue.receiptId(from: Problem(type: nil, title: "x", status: 409, detail: nil)))
    }
}

@MainActor
final class PlanDecodingTests: XCTestCase {
    func testDecodesAPlanWithPairConfirmation() throws {
        let json = """
        {"computed_at":"2026-10-11T10:00:00Z","list_rev":7,"status":"split","usual_shop_id":1,
         "stores":[{"shop_id":1,"name":"Corner Market","item_count":2,"estimate_cents":900,"unpriced_count":1,"is_usual":true,"receipts_90d":5}],
         "items":[{"item_id":3,"text":"oat milk","qty":1,"shop_id":1,"status":"compared","unit_cents":129,"base_unit":"l","pack_size_known":false,
                   "estimate_cents":129,"saving_cents":40,"other_prices":[{"shop_id":2,"unit_cents":169}],"prices":[],"confidence":["seen_once","different_pack"],
                   "explanation":"x","pair_confirmation":{"product_a":1,"product_b":2,"name_a":"A","name_b":"B","shop_a":"S1","shop_b":"S2","shop_a_id":1,"shop_b_id":2}}],
         "summary":{"item_count":2,"priced_count":1,"compared_count":1,"no_price_count":1,"per_piece_count":1,"saving_cents":40,"gross_saving_cents":290,"extra_stops":1,
                    "extra_stop_cents":250,"extra_stop_total_cents":250,"max_stops":2,"one_shop_fine":false,"split_saving_cents":290,"confidence":"low","coverage":0.5},
         "basis":{"window_days":90}}
        """
        let plan = try Coders.decoder().decode(ListPlan.self, from: Data(json.utf8))
        XCTAssertEqual(plan.status, "split")
        XCTAssertEqual(plan.stores.first?.name, "Corner Market")
        XCTAssertEqual(plan.items.first?.confidence, ["seen_once", "different_pack"])
        XCTAssertEqual(plan.items.first?.packSizeKnown, false)
        XCTAssertEqual(plan.items.first?.pairConfirmation?.shopBId, 2)
        XCTAssertEqual(plan.summary.savingCents, 40)
    }

    func testDecodesListChangesWithGroups() throws {
        let json = JSON.changes(items: [JSON.item(1, "milk", 5)], groups: [JSON.group(5, "BBQ")], deleted: [9], deletedGroups: [4], rev: 12, full: true)
        let changes = try Coders.decoder().decode(ListChanges.self, from: Data(json.utf8))
        XCTAssertEqual(changes.rev, 12)
        XCTAssertTrue(changes.fullResync)
        XCTAssertEqual(changes.items.first?.groupId, 5)
        XCTAssertEqual(changes.groups.first?.name, "BBQ")
        XCTAssertEqual(changes.deletedGroupIds, [4])
    }

    func testDecodesSuggestionsAndReceiptChecks() throws {
        let json = """
        {"checked":[{"id":1,"text":"milk","created_at":"2026-10-11T10:00:00Z","checked_at":"2026-10-11T11:00:00Z","check_source":"receipt","checked_receipt_id":9}],
         "reduced":[],"suggestions":[{"id":4,"item_id":2,"item_text":"oat milk","receipt_id":9,"line_text":"ALPRO HAVERMELK","score":0.8,"reason":"match","created_at":"2026-10-11T11:00:00Z"}]}
        """
        let result = try Coders.decoder().decode(ReceiptListChecks.self, from: Data(json.utf8))
        XCTAssertTrue(result.checked[0].isReceiptChecked)
        XCTAssertEqual(result.suggestions[0].lineText, "ALPRO HAVERMELK")
    }
}
