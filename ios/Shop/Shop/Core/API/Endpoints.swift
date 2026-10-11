import Foundation

// One function per openapi.yaml operation.

extension APIClient {
    // MARK: System

    /// 200 healthy, 503 degraded; both carry a Health body.
    func health() async throws -> Health {
        let data = try await raw("GET", "/healthz", acceptStatus: [503])
        return try decode(data)
    }

    func version() async throws -> VersionInfo { try await get("/version") }

    // MARK: Auth

    func authStatus() async throws -> AuthStatus { try await get("/v1/auth/status") }

    /// Exchanges the server's pairing password for this device's token.
    func pair(password: String, deviceName: String) async throws -> String {
        let response: PairResponse = try await send("POST", "/v1/auth/pair", body: PairRequest(password: password, deviceName: deviceName))
        return response.token
    }

    /// Revokes this device's own token (sign out).
    func unpairSelf() async throws {
        let all = try await devices()
        guard let me = all.first(where: \.current) else { return }
        try await revokeDevice(me.id)
    }

    func me() async throws -> Me { try await get("/v1/auth/me") }
    func devices() async throws -> [Device] { try await get("/v1/auth/devices") }
    func revokeDevice(_ id: Int64) async throws { try await perform("DELETE", "/v1/auth/devices/\(id)") }

    // MARK: Shops

    func shops() async throws -> [Shop] { try await get("/v1/shops") }
    func shop(_ id: Int64) async throws -> Shop { try await get("/v1/shops/\(id)") }
    func createShop(_ input: ShopInput) async throws -> Shop { try await send("POST", "/v1/shops", body: input) }
    func updateShop(_ id: Int64, _ input: ShopInput) async throws -> Shop { try await send("PATCH", "/v1/shops/\(id)", body: input) }
    func deleteShop(_ id: Int64) async throws { try await perform("DELETE", "/v1/shops/\(id)") }

    // MARK: Categories

    func categories() async throws -> [Category] { try await get("/v1/categories") }
    func createCategory(_ input: CategoryInput) async throws -> Category { try await send("POST", "/v1/categories", body: input) }
    func updateCategory(_ id: Int64, _ input: CategoryInput) async throws -> Category { try await send("PATCH", "/v1/categories/\(id)", body: input) }
    /// `moveTo`: a category id, or 0 for "no category". Without it a category that still has
/// products answers 409 `category_has_products` and nothing is deleted.
func deleteCategory(_ id: Int64, moveTo: Int64? = nil) async throws {
        try await perform("DELETE", "/v1/categories/\(id)", query: ["move_to": moveTo.map { String($0) }])
    }

    // MARK: Receipts

    func receipts(
        from: Date? = nil,
        to: Date? = nil,
        shopId: Int64? = nil,
        status: ReceiptStatus? = nil,
        limit: Int = 50,
        cursor: String? = nil
    ) async throws -> ReceiptPage {
        try await get("/v1/receipts", query: [
            "from": from.map(Dates.isoDay),
            "to": to.map(Dates.isoDay),
            "shop_id": shopId.map { String($0) },
            "status": status?.rawValue,
            "limit": String(limit),
            "cursor": cursor,
        ])
    }

    func receipt(_ id: Int64) async throws -> Receipt { try await get("/v1/receipts/\(id)") }

    func shopSuggestion(receiptId: Int64) async throws -> ShopSuggestion? {
        let answer: ShopSuggestionAnswer = try await get("/v1/receipts/\(receiptId)/shop-suggestion")
        return answer.suggestion
    }

    func acceptShopSuggestion(receiptId: Int64, _ body: AcceptShopSuggestionBody) async throws -> AcceptedShopSuggestion {
        try await send("POST", "/v1/receipts/\(receiptId)/shop-suggestion/accept", body: body)
    }

    func rejectShopSuggestion(receiptId: Int64) async throws {
        try await perform("POST", "/v1/receipts/\(receiptId)/shop-suggestion/reject")
    }
    func receiptCounts() async throws -> ReceiptCounts { try await get("/v1/receipts/counts") }

    func search(_ query: String, limit: Int = 10) async throws -> SearchResults {
        try await get("/v1/search", query: ["q": query, "limit": String(limit)])
    }
    func updateReceipt(_ id: Int64, _ patch: ReceiptPatch) async throws -> Receipt { try await send("PATCH", "/v1/receipts/\(id)", body: patch) }
    func deleteReceipt(_ id: Int64) async throws { try await perform("DELETE", "/v1/receipts/\(id)") }
    func confirmReceipt(_ id: Int64) async throws -> Receipt { try await send("POST", "/v1/receipts/\(id)/confirm") }

    func reparseReceipt(_ id: Int64, force: Bool = false) async throws -> Receipt {
        try await send("POST", "/v1/receipts/\(id)/reparse", query: ["force": force ? "true" : nil])
    }

    func reocrReceipt(_ id: Int64, force: Bool = false) async throws -> Receipt {
        try await send("POST", "/v1/receipts/\(id)/reocr", query: ["force": force ? "true" : nil])
    }

    func receiptImageURL(_ id: Int64) -> URL? { url("/v1/receipts/\(id)/image") }

    /// Foreground upload (the queue normally uploads in the background).
    func uploadReceipt(image: Data, shopId: Int64?, purchasedAt: Date?, photoTakenAt: Date? = nil) async throws -> Receipt {
        var form = Multipart()
        if let shopId { form.add(field: "shop_id", value: String(shopId)) }
        if let purchasedAt { form.add(field: "purchased_at", value: Dates.rfc3339(purchasedAt)) }
        if let photoTakenAt { form.add(field: "photo_taken_at", value: Dates.rfc3339(photoTakenAt)) }
        form.add(file: "image", filename: "receipt.jpg", mimeType: "image/jpeg", data: image)
        form.finish()
        return try await upload("POST", "/v1/receipts", multipart: form)
    }

    // MARK: Lines

    func createLine(receiptId: Int64, _ input: LineInput) async throws -> ReceiptLine {
        try await send("POST", "/v1/receipts/\(receiptId)/lines", body: input)
    }

    func updateLine(_ id: Int64, _ input: LineInput) async throws -> ReceiptLine { try await send("PATCH", "/v1/lines/\(id)", body: input) }
    func deleteLine(_ id: Int64) async throws { try await perform("DELETE", "/v1/lines/\(id)") }

    func linkLine(_ id: Int64, productId: Int64?, remember: Bool = true) async throws -> LinkResult {
        try await send("PUT", "/v1/lines/\(id)/product", body: LinkInput(productId: productId, remember: remember))
    }

    // MARK: Products

    enum ProductSort: String { case name, recent, purchases }

    func products(query: String? = nil, categoryId: Int64? = nil, sort: ProductSort? = nil, limit: Int = 50, offset: Int = 0) async throws -> [Product] {
        let q = query?.trimmingCharacters(in: .whitespacesAndNewlines)
        let hasQuery = !(q?.isEmpty ?? true)
        return try await get("/v1/products", query: [
            "q": hasQuery ? q : nil,
            "sort": hasQuery ? nil : sort?.rawValue,
            "category_id": categoryId.map { String($0) },
            "limit": String(limit),
            "offset": offset > 0 ? String(offset) : nil,
        ])
    }

    func product(_ id: Int64) async throws -> Product { try await get("/v1/products/\(id)") }
    func createProduct(_ input: ProductInput) async throws -> Product { try await send("POST", "/v1/products", body: input) }
    func updateProduct(_ id: Int64, _ input: ProductInput) async throws -> Product { try await send("PATCH", "/v1/products/\(id)", body: input) }
    func deleteProduct(_ id: Int64) async throws { try await perform("DELETE", "/v1/products/\(id)") }
    func productImageURL(_ product: Product) -> URL? {
        product.hasImage ? url("/v1/products/\(product.id)/image", query: ["v": String(Int(product.updatedAt.timeIntervalSince1970))]) : nil
    }

    func putProductImage(_ id: Int64, jpeg: Data) async throws -> Product {
        var form = Multipart()
        form.add(file: "image", filename: "product.jpg", mimeType: "image/jpeg", data: jpeg)
        form.finish()
        return try await upload("PUT", "/v1/products/\(id)/image", multipart: form)
    }

    func putNutrition(_ id: Int64, _ nutrition: Nutrition) async throws -> Product {
        try await send("PUT", "/v1/products/\(id)/nutrition", body: nutrition)
    }

    func deleteNutrition(_ id: Int64) async throws { try await perform("DELETE", "/v1/products/\(id)/nutrition") }

    /// Local product, Open Food Facts prefill, or nil (404).
    func lookupBarcode(_ code: String) async throws -> BarcodeLookup? {
        do {
            return try await get("/v1/barcodes/\(code)")
        } catch APIError.notFound {
            return nil
        }
    }

    // MARK: Product auto-population

    /// Creates the product for a scanned barcode (or returns the known one). Safe to repeat.
    func createBarcodeProduct(_ code: String) async throws -> BarcodeCreateResponse {
        try await send("POST", "/v1/barcodes/\(code)/product")
    }

    func reviewQueue(limit: Int = 50, offset: Int = 0, reason: String? = nil) async throws -> ReviewQueueResponse {
        try await get("/v1/products/review-queue", query: ["limit": String(limit), "offset": String(offset), "reason": reason])
    }

    func reviewProduct(_ id: Int64, _ body: ReviewBody) async throws -> ReviewResult {
        try await send("POST", "/v1/products/\(id)/review", body: body)
    }

    struct MergeBody: Codable, Sendable { var intoProductId: Int64 }

    func mergeProduct(_ id: Int64, into target: Int64) async throws -> ReviewResult {
        try await send("POST", "/v1/products/\(id)/merge", body: MergeBody(intoProductId: target))
    }

    func undoProductChange(_ id: Int64) async throws -> UndoResult {
        try await send("POST", "/v1/product-merges/\(id)/undo")
    }

    struct SplitBody: Codable, Sendable { var name: String? }

    func splitLineProduct(_ lineId: Int64, name: String? = nil) async throws -> SplitResult {
        try await send("POST", "/v1/lines/\(lineId)/split-product", body: SplitBody(name: name))
    }

    func refreshProduct(_ id: Int64) async throws -> ProductRefreshResult {
        try await send("POST", "/v1/products/\(id)/refresh")
    }

    func aliases(productId: Int64) async throws -> [Alias] { try await get("/v1/products/\(productId)/aliases") }
    func deleteAlias(_ id: Int64) async throws { try await perform("DELETE", "/v1/aliases/\(id)") }

    func unmatchedLines(productId: Int64, days: Int = 14) async throws -> [UnmatchedLine] {
        try await get("/v1/products/\(productId)/unmatched-lines", query: ["days": String(days)])
    }

    // MARK: Stats

    /// `splitBy` only with a time grouping; `categoryId` only with `.product` (else 400).
    func spend(from: Date? = nil, to: Date? = nil, groupBy: SpendGrouping = .month, splitBy: SpendSplit? = nil,
               shopId: Int64? = nil, categoryId: Int64? = nil) async throws -> Spend {
        try await get("/v1/stats/spend", query: [
            "from": from.map(Dates.isoDay),
            "to": to.map(Dates.isoDay),
            "group_by": groupBy.rawValue,
            "split_by": splitBy?.rawValue,
            "shop_id": shopId.map { String($0) },
            "category_id": categoryId.map { String($0) },
        ])
    }

    func priceHistory(productId: Int64, shopId: Int64? = nil) async throws -> [PricePoint] {
        try await get("/v1/stats/products/\(productId)/prices", query: ["shop_id": shopId.map { String($0) }])
    }

    func comparePrices(productId: Int64) async throws -> [ShopPrice] {
        try await get("/v1/stats/products/\(productId)/compare")
    }

    func priceChanges(since: Date? = nil, minPct: Double? = nil) async throws -> [PriceChange] {
        try await get("/v1/stats/price-changes", query: [
            "since": since.map(Dates.isoDay),
            "min_pct": minPct.map { String($0) },
        ])
    }

    func frequentItems(lookbackDays: Int? = nil, minCount: Int? = nil) async throws -> [FrequentItem] {
        try await get("/v1/stats/frequent-items", query: [
            "lookback_days": lookbackDays.map { String($0) },
            "min_count": minCount.map { String($0) },
        ])
    }

    // MARK: Shopping list

    func shoppingList(includeChecked: Bool = true) async throws -> [ListItem] {
        try await get("/v1/list", query: ["include_checked": includeChecked ? "true" : nil])
    }

    func addListItem(_ input: ListItemInput) async throws -> ListItem { try await send("POST", "/v1/list", body: input) }
    func updateListItem(_ id: Int64, _ input: ListItemInput) async throws -> ListItem { try await send("PATCH", "/v1/list/\(id)", body: input) }
    func deleteListItem(_ id: Int64) async throws { try await perform("DELETE", "/v1/list/\(id)") }
    func checkListItem(_ id: Int64) async throws -> ListItem { try await send("POST", "/v1/list/\(id)/check") }
    func uncheckListItem(_ id: Int64) async throws -> ListItem { try await send("POST", "/v1/list/\(id)/uncheck") }
    func clearCheckedItems() async throws -> RemovedCount { try await send("DELETE", "/v1/list/checked") }
}
