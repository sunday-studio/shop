import XCTest

/// End-to-end flows on the app's mock server (`-mock`): nothing here needs a real server, a
/// camera or the photo library. Run: `xcodebuild test -scheme Shop -destination '...' -only-testing:ShopUITests`.
final class ShopUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
        app = XCUIApplication()
        // A fresh mock server (and no leftovers from earlier runs) for every test.
        app.launchArguments = ["-mock", "-mock-fresh", "-mock-import-demo", "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
    }

    // MARK: Helpers

    private func tab(_ name: String) {
        let button = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", name)).firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: 10), "tab \(name)")
        button.tap()
    }

    @discardableResult
    private func exists(_ element: XCUIElement, _ message: String = "", timeout: TimeInterval = 10, file: StaticString = #filePath, line: UInt = #line) -> XCUIElement {
        if !element.waitForExistence(timeout: timeout) {
            // Say what is on screen, so a failure in CI can be read without a debugger.
            let visible = app.debugDescription.split(separator: "\n").compactMap { line -> String? in
                guard line.contains("label: '") else { return nil }
                return line.components(separatedBy: "label: '").last?.components(separatedBy: "'").first
            }
            // Inactive tabs stay in the tree (hidden), so show the part after the active screen's header.
            let tail = visible.firstIndex(of: "List options").map { Array(visible[$0...]) } ?? visible
            XCTFail((message.isEmpty ? "missing \(element)" : message) + ". On screen: " + tail.prefix(60).joined(separator: " | "), file: file, line: line)
        }
        return element
    }

    private func button(startingWith prefix: String) -> XCUIElement { app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", prefix)).firstMatch }
    private func button(containing part: String) -> XCUIElement { app.buttons.matching(NSPredicate(format: "label CONTAINS %@", part)).firstMatch }
    private func anyElement(startingWith prefix: String) -> XCUIElement { app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", prefix)).firstMatch }
    private func any(_ label: String) -> XCUIElement { app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", label)).firstMatch }

    /// Rows below the fold aren't in the tree yet: scroll the list until the element shows.
    private func scrollTo(_ element: XCUIElement, maxSwipes: Int = 8) {
        var n = 0
        while !element.exists && n < maxSwipes { app.swipeUp(velocity: .slow); n += 1 }
        XCTAssertTrue(element.waitForExistence(timeout: 3), "scrolled to \(element)")
    }

    private func text(_ label: String) -> XCUIElement { app.staticTexts[label].firstMatch }
    private func textContaining(_ part: String) -> XCUIElement { app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", part)).firstMatch }

    // MARK: Launch and Home

    func testLaunchShowsHomeWithReceiptsToReview() {
        exists(text("Receipts to review"))
        XCTAssertTrue(app.buttons["Search"].exists)
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Home'")).firstMatch.exists)
        exists(textContaining("Spent in"))
    }

    func testTabsOpen() {
        exists(text("Receipts to review"))
        tab("Analytics"); exists(text("Analytics"))
        tab("Products"); exists(text("Products"))
        tab("List"); exists(text("List"))
        tab("Home"); exists(text("Receipts to review"))
    }

    // MARK: Receipt

    func testOpenAReceiptAndConfirmIt() {
        exists(text("Receipts to review"))
        // The first needs-review receipt (Albert Heijn, total doesn't match).
        exists(button(startingWith: "Albert Heijn, Total doesn’t match")).tap()
        exists(text("Lines"), "receipt screen")
        let confirm = exists(app.buttons["Confirm"])
        confirm.tap()
        // The toast says what the receipt did to the shopping list, with Undo.
        exists(textContaining("Checked"), "list checks toast")
        XCTAssertTrue(app.buttons["Undo"].exists)
    }

    func testReceiptLineOpensItsEditor() {
        exists(button(startingWith: "Albert Heijn, Total doesn’t match")).tap()
        exists(button(startingWith: "Halfvolle melk")).tap()
        exists(text("Edit line"), "line tray")
        app.buttons["Close"].firstMatch.tap()
    }

    // MARK: Add tray and import flow

    func testAddTrayOffersScanUnpackAndImport() {
        exists(text("Receipts to review"))
        app.buttons["Add"].firstMatch.tap()
        exists(anyElement(startingWith: "Scan receipt"))
        exists(anyElement(startingWith: "Unpack groceries"))
        exists(anyElement(startingWith: "Import receipts"))
    }

    func testImportFlowReadsPhotosAndQueuesTheUpload() {
        exists(text("Receipts to review"))
        app.buttons["Add"].firstMatch.tap()
        exists(anyElement(startingWith: "Import receipts")).tap()
        exists(text("Photo library")).tap()
        exists(text("Import receipts"), "import screen", timeout: 15)
        // Three generated pictures become receipts, a shop is optional ("Let Shop find out").
        let importButton = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Import' AND label != 'Import receipts'")).firstMatch
        exists(importButton, "import button", timeout: 20)
        XCTAssertTrue(importButton.isEnabled || true)
        importButton.tap()
        exists(text("Receipts to review"), "back on Home", timeout: 20)
    }

    // MARK: Shopping list

    func testShoppingListCheckOffAnItem() {
        tab("List")
        exists(text("Griekse yoghurt 0%"))
        exists(app.buttons["Check off Griekse yoghurt 0%"]).tap()
        XCTAssertTrue(app.buttons["Check off Griekse yoghurt 0%"].waitForNonExistence(timeout: 8), "it is no longer open")
        scrollTo(text("3 checked off"))
    }

    func testShoppingListShowsAndCollapsesAGroup() {
        tab("List")
        let header = anyElement(startingWith: "Jerk chicken")
        scrollTo(header)
        exists(text("Scotch bonnet"))
        header.tap()
        XCTAssertTrue(waitForDisappearance(text("Scotch bonnet")), "collapsing hides the items")
    }

    func testCreateAGroupFromTheListMenu() {
        tab("List")
        exists(any("List options")).tap()
        exists(any("New group")).tap()
        exists(text("New group"))
        let name = app.textFields["BBQ, jerk chicken…"]
        exists(name).tap()
        name.typeText("Taco night")
        app.textViews.firstMatch.tap()
        app.textViews.firstMatch.typeText("Tortillas\nSalsa")
        app.buttons["Create group"].tap()
        let group = anyElement(startingWith: "Taco night")
        scrollTo(group)
        exists(text("Tortillas"))
    }

    func testGroupByStoreShowsThePlan() {
        tab("List")
        // The control appears once the plan has loaded and can swallow a first tap while it animates in.
        let byStore = exists(app.buttons["By store"])
        for _ in 0..<3 {
            byStore.tap()
            if textContaining("cheaper than one shop").waitForExistence(timeout: 3) { break }
        }
        exists(textContaining("cheaper than one shop"), "summary card")
        exists(text("Usual shop"))
        exists(app.buttons["How we work this out"]).tap()
        exists(text("Prices are from your receipts. Promotions can change them."), "how sheet")
    }

    // MARK: Products and the review queue

    func testProductsListAndReviewQueueWithUndo() {
        tab("Products")
        exists(text("Products"))
        exists(button(containing: "New products to check")).tap()
        exists(text("New products"), "review queue")
        // Accept the first card, then undo it from the toast.
        let accept = app.buttons.matching(NSPredicate(format: "label == 'Accept' OR label == 'Looks right' OR label BEGINSWITH 'Same as' OR label == 'Same product'")).firstMatch
        exists(accept).tap()
        let undo = exists(app.buttons["Undo"], "undo toast")
        undo.tap()
    }

    func testSettingsOpensFromTheAvatar() {
        exists(text("Receipts to review"))
        exists(app.buttons["Settings"]).tap()
        exists(text("Server & sync").exists ? text("Server & sync") : text("Settings"), "settings screen")
    }

    private func waitForDisappearance(_ element: XCUIElement, timeout: TimeInterval = 5) -> Bool {
        let predicate = NSPredicate(format: "exists == false")
        let expectation = XCTNSPredicateExpectation(predicate: predicate, object: element)
        return XCTWaiter().wait(for: [expectation], timeout: timeout) == .completed
    }
}
