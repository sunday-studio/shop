import Foundation
import SwiftUI

/// Sample data that mirrors the wireframes. Used by Previews, the design catalog and the
/// `-mock` launch argument (through `MockServer`).
@MainActor
enum MockData {
    static let now = Date()

    static func ago(days: Int, hour: Int = 12, minute: Int = 0) -> Date {
        let day = Dates.adding(.day, -days, to: Dates.startOfDay(now))
        return Dates.calendar.date(bySettingHour: hour, minute: minute, second: 0, of: day) ?? day
    }

    // MARK: Reference data

    static let shops: [Shop] = [
        shop(1, "Albert Heijn", branch: "Oudegracht", address: "Oudegracht 120", city: "Utrecht"),
        shop(2, "Lidl", branch: "Kanaalstraat", address: "Kanaalstraat 52", city: "Utrecht"),
        shop(3, "Jumbo", branch: "Overvecht", address: "Winkelcentrum Overvecht", city: "Utrecht"),
        shop(4, "Dirk", branch: nil, address: "Amsterdamsestraatweg 300", city: "Utrecht"),
        shop(5, "Ekoplaza", branch: nil, address: "Twijnstraat 9", city: "Utrecht"),
    ]

    static func shop(_ id: Int64, _ name: String, branch: String?, address: String?, city: String?) -> Shop {
        Shop(id: id, name: name, chain: name, branch: branch, address: address, city: city, notes: nil, domain: nil, logoUrl: nil, createdAt: ago(days: 200), updatedAt: ago(days: 200))
    }

    static let categories: [Category] = [
        "Dairy", "Produce", "Meat & fish", "Bakery", "Pantry", "Frozen", "Drinks", "Household", "Personal care", "Other",
    ].enumerated().map { Category(id: Int64($0.offset + 1), name: $0.element) }

    static let products: [Product] = [
        product(1, "Halfvolle melk", en: "Semi-skimmed milk", cat: 1, size: 1500, unit: "ml", barcode: "8718907400718"),
        product(2, "Griekse yoghurt 0%", en: "Greek yoghurt 0%", cat: 1, size: 500, unit: "g", barcode: "8718907457521"),
        product(3, "Bananen", en: "Bananas", cat: 2, size: nil, unit: nil),
        product(4, "Kipfilet", en: "Chicken breast", cat: 3, size: 500, unit: "g", barcode: "8718907039802"),
        product(5, "Volkoren brood", en: "Wholemeal bread", cat: 4, size: 800, unit: "g"),
        product(6, "Hummus naturel", en: "Hummus", cat: 5, size: 200, unit: "g", barcode: "4056489123456"),
        product(7, "Tomaten tros", en: "Vine tomatoes", cat: 2, size: nil, unit: nil),
        product(8, "Jonge kaas 48+", en: "Young cheese", cat: 1, size: nil, unit: nil),
        product(9, "Pindakaas", en: "Peanut butter", cat: 5, size: 600, unit: "g", barcode: "8710400311034"),
        product(10, "Eieren vrije uitloop", en: "Free-range eggs", cat: 1, size: 10, unit: "pc", barcode: "8718907112345"),
        product(11, "Koffiebonen", en: "Coffee beans", cat: 5, size: 500, unit: "g", barcode: "8711000530085"),
        product(12, "Spinazie", en: "Spinach", cat: 2, size: 300, unit: "g"),
        product(13, "Afwasmiddel", en: "Dish soap", cat: 8, size: 500, unit: "ml"),
    ]

    static func product(_ id: Int64, _ name: String, en: String, cat: Int64, size: Double?, unit: String?, barcode: String? = nil) -> Product {
        Product(id: id, name: name, nameEn: en, nameEnSource: "dictionary", brand: nil, categoryId: cat, barcode: barcode,
                netQuantity: size, netUnit: unit, openfoodfactsCode: barcode, hasImage: false,
                nutrition: id == 1 ? Nutrition(per: "100ml", energyKcal: 46, fat: 1.5, saturatedFat: 1, carbs: 4.7, sugars: 4.7, fibre: 0, protein: 3.5, salt: 0.1, source: "openfoodfacts") : nil,
                score: nil, createdAt: ago(days: 150), updatedAt: ago(days: 3))
    }

    // MARK: Receipts

    static func line(_ id: Int64, receipt: Int64, _ no: Int, _ raw: String, kind: LineKind = .item, qty: Double = 1, unit: LineUnit = .pc,
                     unitPrice: Int64? = nil, total: Int64, product: Int64? = nil, parent: Int64? = nil, suggestions: [ProductMatch]? = nil) -> ReceiptLine {
        ReceiptLine(id: id, receiptId: receipt, lineNo: no, rawText: raw, kind: kind, qty: qty, unit: unit, unitPriceCents: unitPrice,
                    lineTotalCents: total, vatCode: kind == .item ? "B" : nil, parentLineId: parent, productId: product,
                    userEdited: false, textEn: nil, suggestions: suggestions)
    }

    /// The "€0,25 short" receipt from H.1.
    static var reviewLines: [ReceiptLine] {
        let r: Int64 = 101
        return [
            line(1001, receipt: r, 1, "AH HALFV MELK 1,5L", total: 179, product: 1),
            line(1002, receipt: r, 2, "BONUS HALFV MELK", kind: .discount, total: -45, parent: 1001),
            line(1003, receipt: r, 3, "BANANEN", qty: 0.842, unit: .kg, unitPrice: 199, total: 168, product: 3),
            line(1004, receipt: r, 4, "AH GRIEKS YOGH 0%", total: 219, suggestions: [
                ProductMatch(productId: 2, name: "Griekse yoghurt 0%", nameEn: "Greek yoghurt 0%", brand: nil, score: 0.92),
                ProductMatch(productId: 6, name: "Hummus naturel", nameEn: nil, brand: nil, score: 0.41),
            ]),
            line(1005, receipt: r, 5, "HUMMUS NATUREL", total: 149, suggestions: [
                ProductMatch(productId: 6, name: "Hummus naturel", nameEn: "Hummus", brand: nil, score: 0.97),
            ]),
            line(1006, receipt: r, 6, "ST#TIEG&LD", kind: .other, total: 25),
            line(1007, receipt: r, 7, "AH KIPFILET 500G", total: 599, product: 4),
            line(1008, receipt: r, 8, "TOMATEN TROS", qty: 0.512, unit: .kg, unitPrice: 349, total: 179, product: 7),
            line(1009, receipt: r, 9, "AH VOLKOREN BRD", total: 249, product: 5),
            line(1010, receipt: r, 10, "STATIEGELD", kind: .deposit, total: 25),
            line(1011, receipt: r, 11, "CALVE PINDAKAAS", total: 329, product: 9),
            line(1012, receipt: r, 12, "JONGE KAAS 48+", qty: 0.412, unit: .kg, unitPrice: 995, total: 410, product: 8),
            line(1013, receipt: r, 13, "AH SPINAZIE", total: 199, product: 12),
            line(1014, receipt: r, 14, "KOFFIEBONEN", total: 799, product: 11),
            line(1015, receipt: r, 15, "AH EIEREN 10ST", total: 329, product: 10),
        ]
    }

    static func receipt(_ id: Int64, shop: Int64, at: Date, total: Int64?, status: ReceiptStatus, reasons: [String] = [], lines: [ReceiptLine]? = nil, lineCount: Int? = nil, unlinked: Int? = nil) -> Receipt {
        let sum = lines?.filter { $0.kind != .other }.reduce(Int64(0)) { $0 + $1.lineTotalCents }
        return Receipt(id: id, shopId: shop, purchasedAt: status == .processing ? nil : at, totalCents: total, parsedSumCents: sum ?? total,
                       paymentMethod: "PIN", status: status, reviewReasons: reasons, ocrEngine: "ppocrv5", ocrMs: 4210,
                       parserVersion: 2, confirmedAt: nil, createdAt: at, updatedAt: at, lines: lines,
                       lineCount: lineCount ?? lines?.filter { $0.kind == .item }.count, unlinkedCount: unlinked)
    }

    static var receipts: [Receipt] {
        let lines = reviewLines
        let reviewTotal = lines.filter { $0.kind != .other }.reduce(Int64(0)) { $0 + $1.lineTotalCents } + 25
        return [
            receipt(100, shop: 2, at: ago(days: 0, hour: 19, minute: 5), total: nil, status: .processing),
            receipt(101, shop: 1, at: ago(days: 0, hour: 18, minute: 42), total: reviewTotal, status: .needsReview, reasons: ["total_mismatch", "unparsed_rows"], lines: lines, unlinked: 2),
            receipt(102, shop: 3, at: ago(days: 1, hour: 17, minute: 20), total: 6105, status: .needsReview, reasons: ["no_date"], lineCount: 14, unlinked: 3),
            receipt(103, shop: 4, at: ago(days: 1, hour: 12, minute: 8), total: 1240, status: .ok, lineCount: 9),
            receipt(104, shop: 1, at: ago(days: 2, hour: 18, minute: 10), total: 2245, status: .ok, lineCount: 11),
            receipt(105, shop: 5, at: ago(days: 2, hour: 9, minute: 32), total: 1895, status: .ok, lineCount: 6, unlinked: 2),
            receipt(106, shop: 1, at: ago(days: 5, hour: 17, minute: 55), total: 4180, status: .ok, lineCount: 19),
            receipt(107, shop: 2, at: ago(days: 5, hour: 12, minute: 40), total: 2740, status: .ok, lineCount: 14),
            receipt(108, shop: 3, at: ago(days: 8, hour: 16, minute: 0), total: 3390, status: .ok, lineCount: 12),
            receipt(109, shop: 1, at: ago(days: 12, hour: 11, minute: 15), total: 5273, status: .ok, lineCount: 26),
            receipt(110, shop: 2, at: ago(days: 16, hour: 18, minute: 30), total: 3115, status: .failed, reasons: []),
            // Duplicate pairs (the newer one carries the warning): same photo scanned twice, and a maybe-second-trip.
            receipt(111, shop: 1, at: ago(days: 2, hour: 18, minute: 12), total: 2245, status: .ok, lineCount: 11),
            receipt(112, shop: 2, at: ago(days: 5, hour: 17, minute: 30), total: 2740, status: .ok, lineCount: 14),
        ]
    }

    /// Possible-duplicate fixtures, keyed by the NEWER receipt (openapi 0.17.0 PossibleDuplicate).
    static let duplicatePairs: [Int64: PossibleDuplicate] = [
        // Almost certainly the same receipt: 10 of 11 lines shared, same minute.
        111: PossibleDuplicate(receiptId: 104, purchasedAt: ago(days: 2, hour: 18, minute: 10), totalCents: 2245, shop: ShopRefLite(id: 1, name: "Albert Heijn"),
                               shopName: "Albert Heijn", match: "total+date+lines", sharedLines: 10, itemCount: 11, otherItemCount: 11, timeMatches: true),
        // Same shop, day and total, different items and times: could be a second trip.
        112: PossibleDuplicate(receiptId: 107, purchasedAt: ago(days: 5, hour: 12, minute: 40), totalCents: 2740, shop: ShopRefLite(id: 2, name: "Lidl"),
                               shopName: "Lidl", match: "total+date", sharedLines: 3, itemCount: 14, otherItemCount: 14, timeMatches: false),
    ]

    /// Item lines for the compare view (the other receipts in the mock are summaries).
    static func comparisonLines(receipt id: Int64) -> [ReceiptLine]? {
        let common = ["AH HALFV MELK 1,5L", "BANANEN", "AH KIPFILET 500G", "TOMATEN TROS", "AH VOLKOREN BRD", "CALVE PINDAKAAS", "JONGE KAAS 48+", "AH SPINAZIE", "KOFFIEBONEN", "AH EIEREN 10ST"]
        func make(_ r: Int64, _ names: [String]) -> [ReceiptLine] {
            names.enumerated().map { i, n in line(r * 100 + Int64(i), receipt: r, i + 1, n, total: Int64(150 + i * 37)) }
        }
        switch id {
        case 104: return make(104, common + ["AH ZUIVELSPREAD"])
        case 111: return make(111, common + ["AH ZUIVELSPREAD "])
        case 107: return make(107, (1...14).map { "LIDL ARTIKEL \($0)" })
        case 112: return make(112, ["LIDL ARTIKEL 1", "LIDL ARTIKEL 2", "LIDL ARTIKEL 3"] + (1...11).map { "LIDL ANDERS \($0)" })
        default: return nil
        }
    }

    // MARK: Prices

    static func pricePoints(product id: Int64) -> [PricePoint] {
        let base: BaseUnit = id == 10 ? .pc : (id == 1 ? .l : .kg)
        let series: [(Int64, String, [Int64])] = [
            (3, "Jumbo", [95, 95, 93, 95, 93, 93]),
            (1, "Albert Heijn", [92, 92, 89, 92, 89, 89]),
            (2, "Lidl", [87, 85, 85, 87, 85, 85]),
        ]
        let scale = id == 1 ? 1.0 : Double(id) * 1.7
        var out: [PricePoint] = []
        var lineId: Int64 = Int64(id) * 1000
        for (shopId, name, cents) in series {
            for (i, c) in cents.enumerated() {
                lineId += 1
                let unitCents = Int64(Double(c) * scale)
                out.append(PricePoint(lineId: lineId, shopId: shopId, shopName: name, at: ago(days: (5 - i) * 30 + Int(shopId) * 2),
                                      qty: 1, unit: .pc, paidCents: Int64(Double(unitCents) * 1.5), unitCents: unitCents, baseUnit: base))
            }
        }
        return out.sorted { $0.at < $1.at }
    }

    static func compare(product id: Int64) -> [ShopPrice] {
        let points = pricePoints(product: id)
        let byShop = Dictionary(grouping: points, by: \.shopId)
        var prices = byShop.compactMap { shopId, pts -> ShopPrice? in
            guard let last = pts.max(by: { $0.at < $1.at }) else { return nil }
            return ShopPrice(shopId: shopId, shopName: last.shopName, baseUnit: last.baseUnit, latestUnitCents: last.unitCents,
                             latestAt: last.at, minUnitCents: pts.map(\.unitCents).min() ?? 0, maxUnitCents: pts.map(\.unitCents).max() ?? 0,
                             purchases: pts.count, cheapest: false)
        }
        prices.sort { $0.latestUnitCents < $1.latestUnitCents }
        if !prices.isEmpty { prices[0].cheapest = true }
        return prices
    }

    static var priceChanges: [PriceChange] {
        [
            PriceChange(productId: 4, productName: "Kipfilet", shopId: 3, shopName: "Jumbo", baseUnit: .kg, oldUnitCents: 1008, newUnitCents: 1149, pct: 14, latestAt: ago(days: 2)),
            PriceChange(productId: 11, productName: "Koffiebonen", shopId: 2, shopName: "Lidl", baseUnit: .kg, oldUnitCents: 1398, newUnitCents: 1516, pct: 8.4, latestAt: ago(days: 5)),
            PriceChange(productId: 9, productName: "Pindakaas", shopId: 1, shopName: "Albert Heijn", baseUnit: .kg, oldUnitCents: 515, newUnitCents: 548, pct: 6.4, latestAt: ago(days: 9)),
            PriceChange(productId: 3, productName: "Bananen", shopId: 2, shopName: "Lidl", baseUnit: .kg, oldUnitCents: 189, newUnitCents: 169, pct: -10.6, latestAt: ago(days: 4)),
            PriceChange(productId: 2, productName: "Griekse yoghurt 0%", shopId: 1, shopName: "Albert Heijn", baseUnit: .kg, oldUnitCents: 478, newUnitCents: 438, pct: -8.4, latestAt: ago(days: 12)),
        ]
    }

    static var frequentItems: [FrequentItem] {
        [
            FrequentItem(productId: 10, productName: "Eieren vrije uitloop", purchases: 9, lastBought: ago(days: 9), medianIntervalDays: 7, daysSince: 9, due: true),
            FrequentItem(productId: 11, productName: "Koffiebonen", purchases: 6, lastBought: ago(days: 16), medianIntervalDays: 14, daysSince: 16, due: true),
            FrequentItem(productId: 3, productName: "Bananen", purchases: 12, lastBought: ago(days: 6), medianIntervalDays: 5, daysSince: 6, due: true),
            FrequentItem(productId: 1, productName: "Halfvolle melk", purchases: 24, lastBought: ago(days: 0), medianIntervalDays: 4, daysSince: 0, due: false),
        ]
    }

    // MARK: List

    static var listItems: [ListItem] {
        func item(_ id: Int64, _ productId: Int64?, _ text: String, qty: Double?, shop: Int64?, cents: Int64?, base: BaseUnit = .kg, checked: Bool = false) -> ListItem {
            let cheapest = shop.flatMap { s in cents.map { c in
                ShopPrice(shopId: s, shopName: shops.first { $0.id == s }?.name ?? "", baseUnit: base, latestUnitCents: c, latestAt: ago(days: 5), minUnitCents: c, maxUnitCents: c, purchases: 4, cheapest: true)
            } }
            return ListItem(id: id, text: text, productId: productId, productName: productId.flatMap { p in products.first { $0.id == p }?.name },
                            qty: qty, note: nil, checkedAt: checked ? ago(days: 0) : nil, createdAt: ago(days: 1), cheapest: cheapest)
        }
        return [
            item(1, 1, "Halfvolle melk", qty: 2, shop: 2, cents: 85, base: .l),
            item(2, 2, "Griekse yoghurt 0%", qty: 1, shop: 1, cents: 438),
            item(3, 4, "Kipfilet", qty: 1, shop: 3, cents: 1149),
            item(4, 5, "Volkoren brood", qty: 1, shop: 1, cents: 311),
            item(5, 7, "Tomaten tros", qty: 1, shop: 1, cents: 349),
            item(6, 9, "Pindakaas", qty: 1, shop: 4, cents: 548),
            item(7, 6, "Hummus naturel", qty: 2, shop: 2, cents: 695),
            item(8, nil, "Afwasmiddel", qty: nil, shop: nil, cents: nil),
            item(9, 12, "Spinazie", qty: 1, shop: 1, cents: 663, checked: true),
            item(10, 3, "Bananen", qty: 1, shop: 2, cents: 169, checked: true),
        ]
    }

    // MARK: Design-catalog helpers

    static var categoryShares: [CategoryShare] {
        [
            CategoryShare(name: "Dairy", cents: 7840, color: Accent.blue),
            CategoryShare(name: "Produce", cents: 7120, color: Accent.green),
            CategoryShare(name: "Meat", cents: 6690, color: Accent.red),
            CategoryShare(name: "Pantry", cents: 5830, color: Accent.yellow),
            CategoryShare(name: "Bakery", cents: 4160, color: Accent.orange),
            CategoryShare(name: "Other", cents: 9600, color: Palette.neutral),
        ]
    }

    static var spendBars: [SpendBar] {
        let months: [(String, Int64)] = [("May", 44820), ("Jun", 47105), ("Jul", 40260), ("Aug", 42532), ("Sep", 45535), ("Oct", 41238)]
        let shares: [(Double, Color)] = [(0.19, Accent.blue), (0.17, Accent.green), (0.16, Accent.red), (0.14, Accent.yellow), (0.10, Accent.orange), (0.24, Palette.neutral)]
        return months.enumerated().map { i, m in
            SpendBar(id: m.0, label: m.0, segments: shares.enumerated().map { j, s in
                SpendBar.Segment(id: "\(j)", cents: Int64(Double(m.1) * s.0), color: s.1)
            }, isCurrent: i == months.count - 1)
        }
    }

    static var priceSeries: [PriceSeries] {
        let points = pricePoints(product: 1)
        let cheapest = compare(product: 1).first?.shopId
        return Dictionary(grouping: points, by: \.shopId).map { shopId, pts in
            let name = pts.first?.shopName ?? ""
            return PriceSeries(id: shopId, shopName: name, color: ShopBrand.chartColor(for: name),
                               points: pts.map { PriceSeries.Point(id: $0.lineId, date: $0.at, unitCents: $0.unitCents) },
                               isCheapest: shopId == cheapest)
        }
        .sorted { $0.id < $1.id }
    }
}

extension ShopBrand {
    /// Line colour per shop in charts (Lidl uses a darker yellow so it reads on white).
    static func chartColor(for name: String) -> Color {
        let key = name.lowercased()
        if key.contains("lidl") { return Color(hex: 0xE5B000) }
        if key.contains("jumbo") { return Accent.orange }
        if key.contains("albert heijn") || key == "ah" { return Accent.blue }
        if key.contains("dirk") { return Accent.red }
        if key.contains("ekoplaza") { return Accent.green }
        return Accent.hashed(key)
    }
}
