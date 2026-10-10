import Foundation

// Codable mirrors of openapi.yaml (v0.5.0). JSON is snake_case; the coders convert.
// Money is integer cents, timestamps are RFC3339 UTC. Optional fields marked
// "backend need #n" are proposed in docs/IOS-BACKEND-NEEDS.md and decode as nil until shipped.

// MARK: - System

nonisolated struct Health: Codable, Sendable, Hashable {
    nonisolated struct Check: Codable, Sendable, Hashable {
        var ok: Bool
        var detail: String?
    }

    var status: String
    var checks: [String: Check]

    var isOK: Bool { status == "ok" }
}

nonisolated struct VersionInfo: Codable, Sendable, Hashable {
    var version: String
    var commit: String
    var schemaVersion: Int64
}

nonisolated struct Problem: Codable, Sendable, Hashable, Error {
    var type: String?
    var title: String
    var status: Int
    var detail: String?
}

// MARK: - Auth

nonisolated struct AuthStatus: Codable, Sendable, Hashable {
    var server: String
    var version: String
    var pairingEnabled: Bool
}

nonisolated struct PairRequest: Codable, Sendable {
    var password: String
    var deviceName: String
}

nonisolated struct PairResponse: Codable, Sendable {
    var token: String
    var device: Device?
}

nonisolated struct Me: Codable, Sendable, Hashable {
    var name: String
    var kind: String
}

nonisolated struct Device: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var name: String
    var createdAt: Date
    var lastUsedAt: Date?
    var current: Bool
}

// MARK: - Shops & categories

nonisolated struct Shop: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var name: String
    var chain: String?
    var branch: String?
    var address: String?
    var city: String?
    var notes: String?
    /// Website domain used for the logo (e.g. "ah.nl").
    var domain: String?
    /// Relative logo path ("/v1/shops/{id}/logo"), when the server has one.
    var logoUrl: String?
    var createdAt: Date
    var updatedAt: Date

    /// "Albert Heijn Oudegracht"
    var displayName: String {
        guard let branch, !branch.isEmpty else { return name }
        return name + " " + branch
    }

    /// "Oudegracht 120, Utrecht"
    var location: String? {
        let parts = [address, city].compactMap { $0 }.filter { !$0.isEmpty }
        return parts.isEmpty ? nil : parts.joined(separator: ", ")
    }

    var input: ShopInput {
        ShopInput(name: name, chain: chain, branch: branch, address: address, city: city, notes: notes, domain: domain)
    }
}

nonisolated struct ShopInput: Codable, Sendable, Hashable {
    var name: String
    var chain: String?
    var branch: String?
    var address: String?
    var city: String?
    var notes: String?
    /// Omit to keep or let the server guess; a domain or URL to set; "" to clear.
    var domain: String?
}

nonisolated struct Category: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var name: String
    var sort: Int
}

nonisolated struct CategoryInput: Codable, Sendable, Hashable {
    var name: String
    var sort: Int?
}

// MARK: - Receipts

nonisolated enum ReceiptStatus: String, Codable, Sendable, CaseIterable, Hashable {
    case pending, processing, needsReview = "needs_review", ok, failed
}

nonisolated enum ReviewReason: String, Sendable, CaseIterable {
    case noTotal = "no_total"
    case totalMismatch = "total_mismatch"
    case lineMath = "line_math"
    case noDate = "no_date"
    case lowConf = "low_conf"
    case unparsedRows = "unparsed_rows"
}

nonisolated enum LineKind: String, Codable, Sendable, CaseIterable, Hashable {
    case item, discount, deposit, bag, other
}

nonisolated enum LineUnit: String, Codable, Sendable, CaseIterable, Hashable {
    case pc, kg, l
}

nonisolated struct ProductMatch: Codable, Sendable, Hashable, Identifiable {
    var productId: Int64
    var name: String
    var nameEn: String?
    var brand: String?
    var score: Double

    var id: Int64 { productId }
}

nonisolated struct ReceiptLine: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var receiptId: Int64
    var lineNo: Int
    var rawText: String
    var kind: LineKind
    var qty: Double
    var unit: LineUnit
    var unitPriceCents: Int64?
    var lineTotalCents: Int64
    var vatCode: String?
    var parentLineId: Int64?
    var productId: Int64?
    var userEdited: Bool
    var textEn: String?
    var suggestions: [ProductMatch]?
    /// Linked product's name and category (single-receipt GET).
    var productName: String?
    var productNameEn: String?
    var categoryId: Int64?

    var input: LineInput {
        LineInput(rawText: rawText, kind: kind, qty: qty, unit: unit, unitPriceCents: unitPriceCents,
                  lineTotalCents: lineTotalCents, vatCode: vatCode, parentLineId: parentLineId)
    }
}

nonisolated struct LineInput: Codable, Sendable, Hashable {
    var rawText: String
    var kind: LineKind
    var qty: Double?
    var unit: LineUnit?
    var unitPriceCents: Int64?
    var lineTotalCents: Int64
    var vatCode: String?
    var parentLineId: Int64?
}

nonisolated struct ReceiptPatch: Codable, Sendable, Hashable {
    var shopId: Int64?
    var purchasedAt: Date?
    var totalCents: Int64?
}

nonisolated struct Receipt: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var shopId: Int64
    var purchasedAt: Date?
    var totalCents: Int64?
    var parsedSumCents: Int64?
    var paymentMethod: String?
    var status: ReceiptStatus
    var reviewReasons: [String]
    var ocrEngine: String?
    var ocrMs: Int64?
    var parserVersion: Int?
    var confirmedAt: Date?
    var createdAt: Date
    var updatedAt: Date
    var lines: [ReceiptLine]?
    /// Backend need #1.
    var lineCount: Int?
    /// Backend need #1.
    var unlinkedCount: Int?
    /// Single-receipt GET only: the shop this receipt header looks like (openapi 0.10.0).
    var shopSuggestion: ShopSuggestion?

    var reasons: [ReviewReason] { reviewReasons.compactMap(ReviewReason.init(rawValue:)) }
    /// The date to show and group by: purchase time, else upload time.
    var displayDate: Date { purchasedAt ?? createdAt }
}

nonisolated struct ReceiptCounts: Codable, Sendable, Hashable {
    var pending: Int
    var processing: Int
    var needsReview: Int
    var ok: Int
    var failed: Int
    var total: Int
}

nonisolated struct ReceiptPage: Codable, Sendable, Hashable {
    var items: [Receipt]
    var nextCursor: String?
}

// MARK: - Products

nonisolated struct Nutrition: Codable, Sendable, Hashable {
    var per: String?
    var energyKcal: Double?
    var fat: Double?
    var saturatedFat: Double?
    var carbs: Double?
    var sugars: Double?
    var fibre: Double?
    var protein: Double?
    var salt: Double?
    var source: String?

    var isEmpty: Bool {
        [energyKcal, fat, saturatedFat, carbs, sugars, fibre, protein, salt].allSatisfy { $0 == nil }
    }
}

nonisolated struct Product: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var name: String
    var nameEn: String?
    var nameEnSource: String?
    var brand: String?
    var categoryId: Int64?
    var barcode: String?
    var netQuantity: Double?
    var netUnit: String?
    var openfoodfactsCode: String?
    var hasImage: Bool
    var nutrition: Nutrition?
    var score: Double?
    /// Best latest price per base unit (absent if never priced).
    var cheapest: ShopPrice?
    var lastBoughtAt: Date?
    /// Receipts it was bought on.
    var purchases: Int?
    var createdAt: Date
    var updatedAt: Date

    var sizeText: String? { Quantity.netSize(netQuantity, unit: netUnit) }

    var input: ProductInput {
        // A dictionary translation is the server's own; omit it so it keeps translating.
        ProductInput(name: name,
                     nameEn: nameEnSource == "dictionary" ? nil : nameEn,
                     nameEnSource: nameEnSource == "dictionary" ? nil : nameEnSource,
                     brand: brand, categoryId: categoryId, barcode: barcode, netQuantity: netQuantity,
                     netUnit: netUnit, openfoodfactsCode: openfoodfactsCode)
    }
}

nonisolated struct ProductInput: Codable, Sendable, Hashable {
    var name: String
    var nameEn: String?
    var nameEnSource: String?
    var brand: String?
    var categoryId: Int64?
    var barcode: String?
    var netQuantity: Double?
    var netUnit: String?
    var openfoodfactsCode: String?
    var imageUrl: String?
    var nutrition: Nutrition?
}

nonisolated struct OpenFoodFactsPrefill: Codable, Sendable, Hashable {
    var code: String
    var name: String
    var nameEn: String?
    var brand: String?
    var netQuantity: Double?
    var netUnit: String?
    var imageUrl: String?
    var nutrition: Nutrition?
}

nonisolated struct BarcodeLookup: Codable, Sendable, Hashable {
    var source: String
    var product: Product?
    var prefill: OpenFoodFactsPrefill?
}

nonisolated struct Alias: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var text: String
    var shopId: Int64?
    var shopName: String?
    var productId: Int64
    var createdAt: Date
}

nonisolated struct UnmatchedLine: Codable, Sendable, Hashable, Identifiable {
    var line: ReceiptLine
    var shopId: Int64
    var shopName: String
    var purchasedAt: Date?
    var score: Double

    var id: Int64 { line.id }
}

nonisolated struct LinkInput: Encodable, Sendable {
    var productId: Int64?
    var remember: Bool = true

    enum CodingKeys: String, CodingKey { case productId, remember }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        // product_id: null clears the link, so always encode it.
        try c.encode(productId, forKey: .productId)
        try c.encode(remember, forKey: .remember)
    }
}

nonisolated struct LinkResult: Codable, Sendable, Hashable {
    var line: ReceiptLine
    var alsoLinked: Int64
    var aliasCreated: Bool
}

// MARK: - Stats

nonisolated enum SpendGrouping: String, Codable, Sendable, CaseIterable {
    case day, week, month, shop, category, product
}

nonisolated enum SpendSplit: String, Codable, Sendable {
    case category, shop
}

nonisolated struct SpendBucket: Codable, Sendable, Hashable, Identifiable {
    var key: String
    var label: String
    var cents: Int64
    var receipts: Int
    /// group_by=product: quantity bought, in `unit` (pc | kg | l).
    var qty: Double?
    var unit: String?
    /// With split_by: parts of this bucket, largest first; absent when the bucket is empty.
    var split: [Part]?

    var id: String { key }

    nonisolated struct Part: Codable, Sendable, Hashable {
        var key: String
        var label: String
        var cents: Int64
    }
}

nonisolated struct Spend: Codable, Sendable, Hashable {
    var from: Date
    var to: Date
    var groupBy: SpendGrouping
    var splitBy: SpendSplit?
    var totalCents: Int64
    var receipts: Int
    var buckets: [SpendBucket]
}

nonisolated struct PricePoint: Codable, Sendable, Hashable, Identifiable {
    var lineId: Int64
    var shopId: Int64
    var shopName: String
    var at: Date
    var qty: Double
    var unit: LineUnit
    var paidCents: Int64
    var unitCents: Int64
    var baseUnit: BaseUnit

    var id: Int64 { lineId }
}

nonisolated struct ShopPrice: Codable, Sendable, Hashable, Identifiable {
    var shopId: Int64
    var shopName: String
    var baseUnit: BaseUnit
    var latestUnitCents: Int64
    var latestAt: Date
    var minUnitCents: Int64
    var maxUnitCents: Int64
    var purchases: Int
    var cheapest: Bool

    var id: Int64 { shopId }
}

nonisolated struct PriceChange: Codable, Sendable, Hashable, Identifiable {
    var productId: Int64
    var productName: String
    var shopId: Int64
    var shopName: String
    var baseUnit: BaseUnit
    var oldUnitCents: Int64
    var newUnitCents: Int64
    var pct: Double
    var latestAt: Date

    var id: String { "\(productId)-\(shopId)" }
}

nonisolated struct FrequentItem: Codable, Sendable, Hashable, Identifiable {
    var productId: Int64
    var productName: String
    var purchases: Int
    var lastBought: Date
    var medianIntervalDays: Double
    var daysSince: Double
    var due: Bool

    var id: Int64 { productId }
}

// MARK: - Search

nonisolated struct SearchResults: Codable, Sendable, Hashable {
    nonisolated struct AliasHit: Codable, Sendable, Hashable, Identifiable {
        var text: String
        var productId: Int64
        var productName: String
        var shopId: Int64?
        var shopName: String?
        var score: Double
        var id: String { "\(productId)-\(shopId ?? 0)-\(text)" }
    }

    nonisolated struct ReceiptHit: Codable, Sendable, Hashable, Identifiable {
        var receipt: Receipt
        var matchedLines: [String]
        var id: Int64 { receipt.id }
    }

    var products: [Product]
    var aliases: [AliasHit]
    var receipts: [ReceiptHit]
}

// MARK: - Shopping list

nonisolated struct ListItem: Codable, Sendable, Hashable, Identifiable {
    var id: Int64
    var text: String
    var productId: Int64?
    var productName: String?
    var qty: Double?
    var note: String?
    var checkedAt: Date?
    var createdAt: Date
    var cheapest: ShopPrice?
    /// Backend need #6.
    var updatedAt: Date?

    var isChecked: Bool { checkedAt != nil }
    var title: String { productName ?? text }
}

nonisolated struct ListItemInput: Codable, Sendable, Hashable {
    var text: String?
    var productId: Int64?
    var qty: Double?
    var note: String?
    /// Backend need #5 (idempotent offline adds). Ignored by servers that don't know it.
    var clientId: String?
}

nonisolated struct RemovedCount: Codable, Sendable {
    var removed: Int64
}


/// "This looks like Aldi": read from the receipt header by the server.
nonisolated struct ShopSuggestion: Codable, Sendable, Hashable {
    enum Kind: String, Codable, Sendable { case existing, new }

    var kind: Kind
    /// Set when `kind` is `existing`.
    var shopId: Int64?
    var name: String
    var chain: String?
    /// "Hoofdstraat 12, Teststad"
    var branch: String?
    /// Guessed web domain for the logo, e.g. "ah.nl".
    var domain: String?
    var confidence: Double
    var evidence: [String]
    /// Other unconfirmed receipts from the same branch that accepting would move too.
    var waitingReceiptIds: [Int64]

    /// This receipt plus the ones waiting.
    var receiptCount: Int { waitingReceiptIds.count + 1 }

    /// "Utrecht · aldi.nl" style detail line.
    var detail: String? {
        let parts = [branch, domain].compactMap { $0 }.filter { !$0.isEmpty }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    /// Same suggestion for several receipts → one Home card.
    var groupKey: String { "\(kind.rawValue)|\(shopId ?? 0)|\(name)|\(branch ?? "")" }
}

nonisolated struct ShopSuggestionAnswer: Codable, Sendable {
    var receiptId: Int64
    var suggestion: ShopSuggestion?
}

nonisolated struct AcceptShopSuggestionBody: Codable, Sendable {
    var shopId: Int64?
    var name: String?
    var branch: String?
    var domain: String?
    var applyToWaiting: Bool = true
}

nonisolated struct AcceptedShopSuggestion: Codable, Sendable {
    var shop: Shop
    var linkedReceiptIds: [Int64]
}
