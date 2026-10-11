import Foundation
import UIKit

/// In-memory fake of the Shop API for previews and the `-mock` launch argument.
/// Speaks HTTP shapes through `HTTPTransport`, so the real client code paths run unchanged.
@MainActor
final class MockServer: HTTPTransport {
    private var shops = MockData.shops
    private var categories = MockData.categories
    private var products = MockData.products
    private var deletedProducts: [Int64: Product] = [:]
    private var receipts = MockData.receipts
    private var aliases: [Alias] = [
        Alias(id: 1, text: "ah halfv melk 1,5l", shopId: 1, shopName: "Albert Heijn", productId: 1, createdAt: MockData.ago(days: 90)),
        Alias(id: 2, text: "halfvolle melk", shopId: 2, shopName: "Lidl", productId: 1, createdAt: MockData.ago(days: 60)),
        Alias(id: 3, text: "jumbo halfvolle mlk", shopId: 3, shopName: "Jumbo", productId: 1, createdAt: MockData.ago(days: 40)),
        Alias(id: 4, text: "melk halfvol 1.5l", shopId: nil, shopName: nil, productId: 1, createdAt: MockData.ago(days: 20)),
    ]
    private var list = MockData.listItems
    private var nextId: Int64 = 5000
    private var latency: Duration = .milliseconds(250)

    private let encoder = Coders.encoder()
    private let decoder = Coders.decoder()

    /// `-mock-public`: invented shop names and receipt text instead of real chains, for screenshots
    /// that are shown publicly. Applied to every response body, so no screen can leak a real name.
    static let isPublic = ProcessInfo.processInfo.arguments.contains("-mock-public")

    private static let publicNames: [(String, String)] = [
        ("Albert Heijn", "Corner Market"), ("ALBERT HEIJN", "CORNER MARKET"), ("Ekoplaza", "Northside"),
        ("Jumbo", "Greenfield"), ("JUMBO", "GREENFIELD"), ("jumbo", "greenfield"),
        ("Lidl", "Harbour Fresh"), ("LIDL", "HARBOUR FRESH"), ("Dirk", "Basil & Co"),
        ("Aldi", "Riverside"), ("ALDI", "RIVERSIDE"), ("aldi.nl", "riverside.example"), ("aldi", "riverside"),
        ("AH GRIEKS", "CM GRIEKS"), ("AH HALFV", "CM HALFV"), ("AH KIPFILET", "CM KIPFILET"), ("AH VOLKOREN", "CM VOLKOREN"),
        ("AH SPINAZIE", "CM SPINAZIE"), ("AH EIEREN", "CM EIEREN"), ("AH VOLLE", "CM VOLLE"),
        ("ah halfv", "cm halfv"), ("\"AH\"", "\"CM\""), ("ARLA BIO", "DAIRY BIO"),
    ]

    static func anonymised(_ text: String) -> String {
        var out = text
        for (real, invented) in publicNames { out = out.replacingOccurrences(of: real, with: invented) }
        return out
    }

    init() {
        // Simulator aid for profiling: a catalog and inbox the size of a few years of use.
        if ProcessInfo.processInfo.arguments.contains("-mock-large") {
            let words = ["Halfvolle", "Volle", "Biologische", "Verse", "Gerookte", "Gesneden", "Oude", "Jonge", "Roomboter", "Grove"]
            let things = ["melk", "kaas", "yoghurt", "brood", "pasta", "rijst", "appel", "tomaten", "kipfilet", "koffie", "thee", "chips", "pindakaas", "hummus", "worst", "soep"]
            for i in 0..<1500 {
                let name = "\(words[i % words.count]) \(things[(i / 7) % things.count]) \(i)"
                products.append(MockData.product(Int64(10_000 + i), name, en: name, cat: Int64(i % 10) + 1, size: Double(100 + (i % 9) * 100), unit: i % 2 == 0 ? "g" : "ml", barcode: nil))
            }
            for i in 0..<400 {
                receipts.append(MockData.receipt(Int64(500 + i), shop: Int64(i % 5) + 1, at: MockData.ago(days: 20 + i / 2, hour: 9 + i % 10, minute: i % 60),
                                                 total: Int64(1500 + (i * 137) % 9000), status: .ok, lineCount: 5 + i % 25))
            }
        }
    }

    // MARK: Transport

    func send(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        try? await Task.sleep(for: latency)
        // Simulator aid: `-offline` makes every request fail like a dead connection.
        if ProcessInfo.processInfo.arguments.contains("-offline") { throw URLError(.notConnectedToInternet) }
        guard let url = request.url else { throw APIError.invalidURL }
        let method = request.httpMethod ?? "GET"
        let parts = url.path.split(separator: "/").map(String.init)
        let query = Dictionary(
            (URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") },
            uniquingKeysWith: { a, _ in a }
        )
        var (status, body) = route(method, parts, query, request.httpBody ?? Data(), contentType: request.value(forHTTPHeaderField: "Content-Type"))
        if Self.isPublic, !body.isEmpty, let text = String(data: body, encoding: .utf8) { body = Data(Self.anonymised(text).utf8) }
        let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        return (body, response)
    }

    // MARK: Router

    private func route(_ method: String, _ p: [String], _ q: [String: String], _ body: Data, contentType: String?) -> (Int, Data) {
        func id(_ i: Int) -> Int64? { p.count > i ? Int64(p[i]) : nil }

        // Numeric segments become ":id" so routes match as plain strings.
        let shape = p.map { Int64($0) != nil ? ":id" : $0 }.joined(separator: "/")

        switch (method, shape) {
        case ("GET", "healthz"):
            return ok(Health(status: "ok", checks: [
                "db": .init(ok: true, detail: "Healthy"),
                "ocr": .init(ok: true, detail: "ppocrv5 · ready"),
                "backup": .init(ok: true, detail: "Today, 03:30"),
                "queue": .init(ok: true, detail: "0 waiting"),
            ]))
        case ("GET", "version"):
            return ok(VersionInfo(version: "0.5.0-mock", commit: "mock", schemaVersion: 6))

        // Auth
        case ("GET", "v1/auth/status"):
            return ok(AuthStatus(server: "shop", version: "0.5.0-mock", pairingEnabled: true))
        case ("POST", "v1/auth/pair"):
            let req = try? decoder.decode(PairRequest.self, from: body)
            if req?.password == "wrong" { return problem(401, "Wrong password", "Check it and try again.") }
            return ok(PairResponse(token: "st_mock", device: Device(id: 1, name: req?.deviceName ?? "iPhone", createdAt: .now, lastUsedAt: .now, current: true)), status: 201)
        case ("DELETE", "v1/auth/devices/:id"):
            return (204, Data())
        case ("GET", "v1/auth/me"):
            return ok(Me(name: "iPhone", kind: "device"))
        case ("GET", "v1/auth/devices"):
            return ok([Device(id: 1, name: "iPhone", createdAt: MockData.ago(days: 30), lastUsedAt: .now, current: true)])

        // Shops
        case ("GET", "v1/shops"):
            return ok(shops)
        case ("POST", "v1/shops"):
            guard let input = try? decoder.decode(ShopInput.self, from: body) else { return bad() }
            let shop = Shop(id: newId(), name: input.name, chain: input.chain, branch: input.branch, address: input.address, city: input.city, notes: input.notes, domain: input.domain, logoUrl: nil, createdAt: .now, updatedAt: .now)
            shops.append(shop)
            return ok(shop, status: 201)
        case ("PATCH", "v1/shops/:id"):
            guard let i = shops.firstIndex(where: { $0.id == id(2) }), let input = try? decoder.decode(ShopInput.self, from: body) else { return notFound() }
            shops[i].name = input.name; shops[i].branch = input.branch; shops[i].address = input.address
            shops[i].city = input.city; shops[i].chain = input.chain; shops[i].notes = input.notes; shops[i].updatedAt = .now
            return ok(shops[i])
        case ("DELETE", "v1/shops/:id"):
            shops.removeAll { $0.id == id(2) }
            return (204, Data())

        // Categories
        case ("GET", "v1/categories"):
            let sorted = categories.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
            return ok(sorted.map { c in
                var c = c
                c.productCount = products.filter { $0.categoryId == c.id }.count
                c.color = c.color ?? Self.tone(for: c.name)
                return c
            })
        case ("POST", "v1/categories"):
            guard let input = try? decoder.decode(CategoryInput.self, from: body) else { return bad() }
            let name = input.name.split(separator: " ").joined(separator: " ")
            if categories.contains(where: { $0.name.caseInsensitiveCompare(name) == .orderedSame }) { return categoryNameTaken() }
            let c = Category(id: newId(), name: name, productCount: 0, color: input.color ?? CategoryTone.tokens[categories.count % CategoryTone.tokens.count])
            categories.append(c)
            return ok(c, status: 201)
        case ("PATCH", "v1/categories/:id"):
            guard let i = categories.firstIndex(where: { $0.id == id(2) }), let input = try? decoder.decode(CategoryInput.self, from: body) else { return notFound() }
            let name = input.name.split(separator: " ").joined(separator: " ")
            if categories.contains(where: { $0.id != categories[i].id && $0.name.caseInsensitiveCompare(name) == .orderedSame }) { return categoryNameTaken() }
            categories[i].name = name
            if let color = input.color { categories[i].color = color }
            return ok(categories[i])
        case ("DELETE", "v1/categories/:id"):
            guard let target = id(2), categories.contains(where: { $0.id == target }) else { return notFound() }
            let inside = products.indices.filter { products[$0].categoryId == target }
            if !inside.isEmpty {
                guard let raw = q["move_to"], let moveTo = Int64(raw), moveTo != target else {
                    return ok(Problem(type: nil, title: "category has products", status: 409, detail: nil, code: "category_has_products", productCount: inside.count), status: 409)
                }
                for i in inside { products[i].categoryId = moveTo == 0 ? nil : moveTo }
            }
            categories.removeAll { $0.id == target }
            return (204, Data())

        // Receipts
        case ("GET", "v1/receipts/counts"):
            func n(_ s: ReceiptStatus) -> Int { receipts.filter { $0.status == s }.count }
            return ok(ReceiptCounts(pending: n(.pending), processing: n(.processing), needsReview: n(.needsReview), ok: n(.ok), failed: n(.failed), total: receipts.count))
        case ("GET", "v1/search"):
            return ok(search(q["q"] ?? ""))
        case ("GET", "v1/receipts"):
            var items = receipts.sorted { $0.displayDate > $1.displayDate }
            if let s = q["status"], let st = ReceiptStatus(rawValue: s) { items = items.filter { $0.status == st } }
            if let s = q["shop_id"], let sid = Int64(s) { items = items.filter { $0.shopId == sid } }
            if let f = q["from"].flatMap(Dates.parseIsoDay) { items = items.filter { $0.displayDate >= f } }
            if let t = q["to"].flatMap(Dates.parseIsoDay) { items = items.filter { $0.displayDate < Dates.adding(.day, 1, to: t) } }
            return ok(ReceiptPage(items: items.map { var r = $0; r.lines = nil; return r }, nextCursor: nil))
        case ("POST", "v1/receipts"):
            return upload(body, contentType: contentType)
        case ("GET", "v1/receipts/:id"):
            guard var r = receipts.first(where: { $0.id == id(2) }) else { return notFound() }
            r.shopSuggestion = mockSuggestion(for: r.id)
            return ok(r)
        case ("GET", "v1/receipts/:id/shop-suggestion"):
            guard let rid = id(2) else { return notFound() }
            return ok(ShopSuggestionAnswer(receiptId: rid, suggestion: mockSuggestion(for: rid)))
        case ("POST", "v1/receipts/:id/shop-suggestion/accept"):
            guard let rid = id(2), let suggestion = mockSuggestion(for: rid) else { return notFound() }
            let input = (try? decoder.decode(AcceptShopSuggestionBody.self, from: body)) ?? AcceptShopSuggestionBody()
            let shop: Shop
            if let existing = input.shopId.flatMap({ sid in shops.first { $0.id == sid } }) {
                shop = existing
            } else {
                shop = Shop(id: newId(), name: input.name ?? suggestion.name, chain: suggestion.chain, branch: input.branch ?? suggestion.branch, address: nil, city: nil,
                            notes: nil, domain: input.domain ?? suggestion.domain, logoUrl: nil, createdAt: .now, updatedAt: .now)
                shops.append(shop)
            }
            let linked = [rid] + (input.applyToWaiting ? suggestion.waitingReceiptIds : [])
            for i in receipts.indices where linked.contains(receipts[i].id) { receipts[i].shopId = shop.id }
            acceptedSuggestions.formUnion(linked)
            return ok(AcceptedShopSuggestion(shop: shop, linkedReceiptIds: linked))
        case ("POST", "v1/receipts/:id/shop-suggestion/reject"):
            if let rid = id(2) { acceptedSuggestions.insert(rid) }
            return (204, Data())
        case ("PATCH", "v1/receipts/:id"):
            guard let i = receipts.firstIndex(where: { $0.id == id(2) }), let patch = try? decoder.decode(ReceiptPatch.self, from: body) else { return notFound() }
            if let s = patch.shopId { receipts[i].shopId = s }
            if let d = patch.purchasedAt { receipts[i].purchasedAt = d }
            if let t = patch.totalCents { receipts[i].totalCents = t }
            revalidate(i)
            return ok(receipts[i])
        case ("DELETE", "v1/receipts/:id"):
            receipts.removeAll { $0.id == id(2) }
            return (204, Data())
        case ("POST", "v1/receipts/:id/confirm"):
            guard let i = receipts.firstIndex(where: { $0.id == id(2) }) else { return notFound() }
            receipts[i].status = .ok
            receipts[i].confirmedAt = .now
            var confirmed = receipts[i]
            // Simulator aid: pretend AUTO_PRODUCTS is on so the confirm toast can be tried.
            if ProcessInfo.processInfo.arguments.contains("-mock-auto") {
                confirmed.autoProducts = AutoProductsSummary(linked: 2, created: 3, needsReview: 2, error: nil)
            }
            return ok(confirmed)
        case ("POST", "v1/receipts/:id/reparse"), ("POST", "v1/receipts/:id/reocr"):
            guard let i = receipts.firstIndex(where: { $0.id == id(2) }) else { return notFound() }
            if receipts[i].confirmedAt != nil && q["force"] != "true" {
                return problem(409, "Receipt was confirmed", "Reparse would discard your edits. Use force to continue.")
            }
            let rid = receipts[i].id
            receipts[i].status = .processing
            finishProcessing(rid, after: .seconds(2))
            return ok(receipts[i], status: 202)
        case ("GET", "v1/receipts/:id/image"):
            return (200, Self.receiptImage())
        case ("POST", "v1/receipts/:id/lines"):
            guard let i = receipts.firstIndex(where: { $0.id == id(2) }), let input = try? decoder.decode(LineInput.self, from: body) else { return notFound() }
            let line = ReceiptLine(id: newId(), receiptId: receipts[i].id, lineNo: (receipts[i].lines?.count ?? 0) + 1, rawText: input.rawText, kind: input.kind,
                                   qty: input.qty ?? 1, unit: input.unit ?? .pc, unitPriceCents: input.unitPriceCents, lineTotalCents: input.lineTotalCents,
                                   vatCode: input.vatCode, parentLineId: input.parentLineId, productId: nil, userEdited: true, textEn: nil, suggestions: nil)
            receipts[i].lines = (receipts[i].lines ?? []) + [line]
            revalidate(i)
            return ok(line, status: 201)

        // Lines
        case ("PATCH", "v1/lines/:id"):
            guard let idx = lineIndex(id(2)), let input = try? decoder.decode(LineInput.self, from: body) else { return notFound() }
            let (ri, li) = idx
            var line = receipts[ri].lines![li]
            line.rawText = input.rawText; line.kind = input.kind; line.qty = input.qty ?? line.qty; line.unit = input.unit ?? line.unit
            line.unitPriceCents = input.unitPriceCents; line.lineTotalCents = input.lineTotalCents; line.vatCode = input.vatCode
            line.parentLineId = input.parentLineId; line.userEdited = true
            receipts[ri].lines![li] = line
            revalidate(ri)
            return ok(line)
        case ("DELETE", "v1/lines/:id"):
            guard let idx = lineIndex(id(2)) else { return notFound() }
            let (ri, li) = idx
            receipts[ri].lines!.remove(at: li)
            revalidate(ri)
            return (204, Data())
        case ("PUT", "v1/lines/:id/product"):
            guard let idx = lineIndex(id(2)) else { return notFound() }
            let (ri, li) = idx
            let input = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
            let pid = (input?["product_id"] as? NSNumber)?.int64Value
            receipts[ri].lines![li].productId = pid
            receipts[ri].lines![li].suggestions = nil
            var also: Int64 = 0
            var created = false
            if let pid, (input?["remember"] as? Bool) ?? true {
                let text = receipts[ri].lines![li].rawText.lowercased()
                aliases.append(Alias(id: newId(), text: text, shopId: receipts[ri].shopId, shopName: shopName(receipts[ri].shopId), productId: pid, createdAt: .now))
                created = true
                for r in receipts.indices {
                    for l in (receipts[r].lines ?? []).indices where receipts[r].lines![l].productId == nil && receipts[r].lines![l].rawText.lowercased() == text && !(r == ri && l == li) {
                        receipts[r].lines![l].productId = pid
                        also += 1
                    }
                }
            }
            revalidate(ri)
            return ok(LinkResult(line: receipts[ri].lines![li], alsoLinked: also, aliasCreated: created))

        // Products
        case ("GET", "v1/products"):
            var items = products.sorted { $0.name < $1.name }
            if let c = q["category_id"], let cid = Int64(c) { items = items.filter { $0.categoryId == cid } }
            if let text = q["q"]?.lowercased(), !text.isEmpty {
                items = items.compactMap { p in
                    let hay = (p.name + " " + (p.nameEn ?? "")).lowercased()
                    let aliasHit = aliases.contains { $0.productId == p.id && $0.text.contains(text) }
                    guard hay.contains(text) || aliasHit else { return nil }
                    var m = p
                    m.score = hay.hasPrefix(text) ? 0.95 : 0.8
                    return m
                }
                .sorted { ($0.score ?? 0) > ($1.score ?? 0) }
            }
            items = items.map(enriched)
            if q["q"] == nil, q["sort"] == "recent" {
                items.sort { ($0.lastBoughtAt ?? .distantPast) > ($1.lastBoughtAt ?? .distantPast) }
            }
            let offset = Int(q["offset"] ?? "0") ?? 0
            let limit = Int(q["limit"] ?? "50") ?? 50
            return ok(Array(items.dropFirst(offset).prefix(limit)))
        case ("POST", "v1/products"):
            guard let input = try? decoder.decode(ProductInput.self, from: body) else { return bad() }
            if let b = input.barcode, products.contains(where: { $0.barcode == b }) {
                return problem(409, "Barcode already used", "Another product has this barcode.")
            }
            let p = Product(id: newId(), name: input.name, nameEn: input.nameEn, nameEnSource: input.nameEnSource ?? "manual", brand: input.brand,
                            categoryId: input.categoryId, barcode: input.barcode, netQuantity: input.netQuantity, netUnit: input.netUnit,
                            openfoodfactsCode: input.openfoodfactsCode, hasImage: false, nutrition: input.nutrition, score: nil, createdAt: .now, updatedAt: .now)
            products.append(p)
            return ok(p, status: 201)
        case ("GET", "v1/products/:id"):
            guard let pr = products.first(where: { $0.id == id(2) }) else { return notFound() }
            return ok(enriched(pr))
        case ("PATCH", "v1/products/:id"):
            guard let i = products.firstIndex(where: { $0.id == id(2) }), let input = try? decoder.decode(ProductInput.self, from: body) else { return notFound() }
            products[i].name = input.name; products[i].brand = input.brand; products[i].categoryId = input.categoryId
            products[i].barcode = input.barcode; products[i].netQuantity = input.netQuantity; products[i].netUnit = input.netUnit
            if let en = input.nameEn { products[i].nameEn = en; products[i].nameEnSource = input.nameEnSource ?? "manual" }
            if let n = input.nutrition { products[i].nutrition = n }
            products[i].updatedAt = .now
            return ok(products[i])
        case ("DELETE", "v1/products/:id"):
            if let gone = products.first(where: { $0.id == id(2) }) { deletedProducts[gone.id] = gone }
            products.removeAll { $0.id == id(2) }
            return (204, Data())
        case ("POST", "v1/products/:id/restore"):
            guard let pid = id(2), let back = deletedProducts.removeValue(forKey: pid) else { return problem(409, "product is not deleted", nil) }
            products.append(back)
            return ok(back)
        case ("GET", "v1/products/:id/image"):
            guard let pid = id(2), let pr = (products.first { $0.id == pid }) ?? queue().first(where: { $0.id == pid })?.product, pr.hasImage else { return notFound() }
            return (200, Self.productPhoto)
        case ("PUT", "v1/products/:id/image"):
            guard let pid = id(2) else { return notFound() }
            var photo: Product?
            if let i = products.firstIndex(where: { $0.id == pid }) {
                products[i].updatedAt = .now; products[i].hasImage = true; products[i].imageUrl = "/v1/products/\(pid)/image"
                photo = products[i]
            } else if var items = reviewItems, let i = items.firstIndex(where: { $0.id == pid }) {
                items[i].product.updatedAt = .now; items[i].product.hasImage = true; items[i].product.imageUrl = "/v1/products/\(pid)/image"
                photo = items[i].product
                reviewItems = items
            }
            guard let photo else { return notFound() }
            return ok(photo)
        case ("PUT", "v1/products/:id/nutrition"):
            guard let i = products.firstIndex(where: { $0.id == id(2) }), let n = try? decoder.decode(Nutrition.self, from: body) else { return notFound() }
            products[i].nutrition = n
            return ok(products[i])
        case ("DELETE", "v1/products/:id/nutrition"):
            if let i = products.firstIndex(where: { $0.id == id(2) }) { products[i].nutrition = nil }
            return (204, Data())
        case ("GET", "v1/products/:id/aliases"):
            return ok(aliases.filter { $0.productId == id(2) })
        case ("DELETE", "v1/aliases/:id"):
            aliases.removeAll { $0.id == id(2) }
            return (204, Data())
        case ("GET", "v1/products/:id/unmatched-lines"):
            var out: [UnmatchedLine] = []
            for r in receipts {
                for l in r.lines ?? [] where l.productId == nil && l.kind == .item {
                    out.append(UnmatchedLine(line: l, shopId: r.shopId, shopName: shopName(r.shopId), purchasedAt: r.purchasedAt, score: 0.8))
                }
            }
            return ok(out)
        // Product auto-population
        case ("GET", "v1/products/review-queue"):
            return reviewQueueResponse(q)
        case ("POST", "v1/products/:id/review"):
            return review(id(2), body)
        case ("POST", "v1/products/:id/merge"):
            return mergePlain(id(2), body)
        case ("POST", "v1/product-merges/:id/undo"):
            return undo(id(2))
        case ("POST", "v1/products/:id/refresh"):
            guard let pid = id(2) else { return notFound() }
            var all = queue()
            var product: Product
            let queueIndex = all.firstIndex { $0.id == pid }
            if let queueIndex { product = all[queueIndex].product }
            else if let known = products.first(where: { $0.id == pid }) { product = known }
            else { return notFound() }
            if product.barcode == nil { return problem(422, "No barcode", "Only barcode products can be refreshed.") }
            // Simulator aid: outcomes cycle not_found → unavailable → found.
            refreshCount += 1
            let outcome = ["not_found", "unavailable", "found"][(refreshCount - 1) % 3]
            if outcome == "found" {
                product.name = "Chocomel Vol 1 l"
                product.categoryId = 7
                product.needsReview = false
                product.reviewReason = nil
                if let queueIndex { all.remove(at: queueIndex); reviewItems = all }
            }
            if let i = products.firstIndex(where: { $0.id == pid }) { products[i] = product }
            return ok(ProductRefreshResult(product: product, offStatus: outcome))
        case ("POST", "v1/lines/:id/split-product"):
            let new = MockData.product(newId(), "AH GRIEKS YOGH", en: "Greek yoghurt", cat: 1, size: nil, unit: nil)
            products.append(new)
            let mid = newId()
            undoRecords[mid] = UndoRecord(kind: "split", removed: new, survivor: products.first { $0.id == 4 }, item: nil)
            return ok(SplitResult(mergeId: mid, kind: "split", product: new, linesMoved: 2), status: 201)
        case ("POST", "v1/barcodes/:id/product"):
            return createBarcodeProduct(p[2])
        case ("GET", "v1/barcodes/:id"):
            let code = p[2]
            if let pr = products.first(where: { $0.barcode == code }) {
                return ok(BarcodeLookup(source: "local", product: pr, prefill: nil))
            }
            if code.hasPrefix("87") {
                return ok(BarcodeLookup(source: "openfoodfacts", product: nil, prefill: OpenFoodFactsPrefill(
                    code: code, name: "AH Volle yoghurt", nameEn: "Whole yoghurt", brand: "AH", netQuantity: 1000, netUnit: "g", imageUrl: nil,
                    nutrition: Nutrition(per: "100g", energyKcal: 63, fat: 3.4, saturatedFat: 2.2, carbs: 4.4, sugars: 4.4, fibre: 0, protein: 3.8, salt: 0.13, source: "openfoodfacts"))))
            }
            return notFound()

        // Stats
        case ("GET", "v1/stats/spend"):
            return ok(spend(q))
        case ("GET", "v1/stats/products/:id/prices"):
            return ok(MockData.pricePoints(product: id(3) ?? 1))
        case ("GET", "v1/stats/products/:id/compare"):
            return ok(MockData.compare(product: id(3) ?? 1))
        case ("GET", "v1/stats/price-changes"):
            return ok(MockData.priceChanges)
        case ("GET", "v1/stats/frequent-items"):
            return ok(MockData.frequentItems)

        // List
        case ("GET", "v1/list"):
            let items = q["include_checked"] == "true" ? list : list.filter { !$0.isChecked }
            return ok(items.sorted { ($0.isChecked ? 1 : 0, $1.createdAt) < ($1.isChecked ? 1 : 0, $0.createdAt) })
        case ("POST", "v1/list"):
            guard let input = try? decoder.decode(ListItemInput.self, from: body) else { return bad() }
            if let pid = input.productId, let i = list.firstIndex(where: { $0.productId == pid && !$0.isChecked }) {
                list[i].qty = (list[i].qty ?? 1) + (input.qty ?? 1)
                return ok(list[i])
            }
            let product = products.first { $0.id == input.productId }
            let item = ListItem(id: newId(), text: input.text ?? product?.name ?? "", productId: input.productId, productName: product?.name,
                                qty: input.qty, note: input.note, checkedAt: nil, createdAt: .now,
                                cheapest: input.productId.flatMap { MockData.compare(product: $0).first })
            list.insert(item, at: 0)
            return ok(item, status: 201)
        case ("DELETE", "v1/list/checked"):
            let n = list.filter(\.isChecked).count
            list.removeAll(where: \.isChecked)
            return ok(RemovedCount(removed: Int64(n)))
        case ("PATCH", "v1/list/:id"):
            guard let i = list.firstIndex(where: { $0.id == id(2) }), let input = try? decoder.decode(ListItemInput.self, from: body) else { return notFound() }
            if let t = input.text { list[i].text = t }
            list[i].qty = input.qty
            list[i].note = input.note
            return ok(list[i])
        case ("DELETE", "v1/list/:id"):
            list.removeAll { $0.id == id(2) }
            return (204, Data())
        case ("POST", "v1/list/:id/check"):
            guard let i = list.firstIndex(where: { $0.id == id(2) }) else { return notFound() }
            list[i].checkedAt = .now
            return ok(list[i])
        case ("POST", "v1/list/:id/uncheck"):
            guard let i = list.firstIndex(where: { $0.id == id(2) }) else { return notFound() }
            list[i].checkedAt = nil
            return ok(list[i])

        default:
            return notFound()
        }
    }

    // MARK: Behaviours

    private var uploadCount = 0
    private var refreshCount = 0

    /// A plain drawn picture standing in for a product photo.
    private static let productPhoto: Data = {
        let renderer = UIGraphicsImageRenderer(size: CGSize(width: 240, height: 240))
        return renderer.jpegData(withCompressionQuality: 0.8) { ctx in
            UIColor(red: 0.98, green: 0.78, blue: 0.2, alpha: 1).setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 240, height: 240))
            UIColor.white.setFill()
            ctx.fill(CGRect(x: 70, y: 40, width: 100, height: 160))
        }
    }()

    private func upload(_ body: Data, contentType: String?) -> (Int, Data) {
        // Simulator aid: `-mock-fail` makes every 2nd upload a 415 and every 3rd a 409 duplicate.
        uploadCount += 1
        if ProcessInfo.processInfo.arguments.contains("-mock-fail") {
            if uploadCount % 3 == 2 { return problem(415, "Unsupported image", "This file isn't a readable image.") }
            if uploadCount % 3 == 0 { return problem(409, "Duplicate receipt", "Already uploaded as receipt 101") }
        }
        let text = String(decoding: body.prefix(2000), as: UTF8.self)
        let shopId = text.range(of: "name=\"shop_id\"\r\n\r\n").flatMap { r -> Int64? in
            let rest = text[r.upperBound...]
            return Int64(rest.prefix { $0.isNumber })
        }
        let rid = newId()
        let r = Receipt(id: rid, shopId: shopId, purchasedAt: nil, totalCents: nil, parsedSumCents: nil, paymentMethod: nil, status: .processing,
                        reviewReasons: [], ocrEngine: nil, ocrMs: nil, parserVersion: nil, confirmedAt: nil, createdAt: .now, updatedAt: .now, lines: [])
        receipts.insert(r, at: 0)
        finishProcessing(rid, after: .seconds(4))
        return ok(r, status: 202)
    }

    /// Turns a processing receipt into a needs-review one with the sample lines.
    private func finishProcessing(_ id: Int64, after delay: Duration) {
        Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard let self, let i = self.receipts.firstIndex(where: { $0.id == id }) else { return }
            var lines = self.receipts[i].lines ?? []
            if lines.isEmpty {
                lines = MockData.reviewLines.map { l in
                    var c = l
                    c.id = self.newId()
                    c.receiptId = id
                    return c
                }
                self.receipts[i].totalCents = lines.filter { $0.kind != .other }.reduce(0) { $0 + $1.lineTotalCents } + 25
            }
            self.receipts[i].lines = lines
            self.receipts[i].purchasedAt = self.receipts[i].purchasedAt ?? .now
            self.receipts[i].paymentMethod = "PIN"
            self.revalidate(i)
        }
    }

    /// Recomputes Σ, reasons and status after edits (mirrors the server's validator loosely).
    private func revalidate(_ i: Int) {
        let lines = receipts[i].lines ?? []
        let counted = lines.filter { $0.kind != .other }
        let sum = counted.reduce(Int64(0)) { $0 + $1.lineTotalCents }
        receipts[i].parsedSumCents = sum
        receipts[i].lineCount = lines.filter { $0.kind == .item }.count
        receipts[i].unlinkedCount = lines.filter { $0.kind == .item && $0.productId == nil }.count
        var reasons: [String] = []
        if receipts[i].totalCents == nil { reasons.append("no_total") }
        else if abs(sum - (receipts[i].totalCents ?? 0)) > 1 { reasons.append("total_mismatch") }
        if receipts[i].purchasedAt == nil { reasons.append("no_date") }
        receipts[i].reviewReasons = reasons
        if receipts[i].confirmedAt == nil {
            receipts[i].status = reasons.isEmpty ? .ok : .needsReview
        }
        receipts[i].updatedAt = .now
    }

    /// Adds the 0.9.0 list fields (cheapest, last bought, purchases) from the sample prices.
    private func enriched(_ p: Product) -> Product {
        var out = p
        let points = MockData.pricePoints(product: p.id)
        out.cheapest = MockData.compare(product: p.id).first
        out.lastBoughtAt = points.last?.at
        out.purchases = points.count
        return out
    }

    private func search(_ query: String) -> SearchResults {
        let text = query.lowercased()
        guard !text.isEmpty else { return SearchResults(products: [], aliases: [], receipts: []) }
        let hits = products.filter { ($0.name + " " + ($0.nameEn ?? "")).lowercased().contains(text) }
        let hitIds = Set(hits.map(\.id))
        let aliasHits = aliases.filter { $0.text.contains(text) || hitIds.contains($0.productId) }.map { a in
            SearchResults.AliasHit(text: a.text, productId: a.productId, productName: products.first { $0.id == a.productId }?.name ?? "",
                                   shopId: a.shopId, shopName: a.shopName, score: 0.9)
        }
        let receiptHits: [SearchResults.ReceiptHit] = receipts.compactMap { r in
            let matched = (r.lines ?? []).filter { $0.rawText.lowercased().contains(text) || ($0.productId.map(hitIds.contains) ?? false) }
            guard !matched.isEmpty else { return nil }
            var bare = r
            bare.lines = nil
            return SearchResults.ReceiptHit(receipt: bare, matchedLines: matched.prefix(3).map(\.rawText))
        }
        return SearchResults(products: hits.map { var p = $0; p.score = 0.9; return p }, aliases: aliasHits, receipts: receiptHits)
    }

    private func spend(_ q: [String: String]) -> Spend {
        let group = SpendGrouping(rawValue: q["group_by"] ?? "month") ?? .month
        let to = q["to"].flatMap(Dates.parseIsoDay) ?? Dates.startOfDay(.now)
        let from = q["from"].flatMap(Dates.parseIsoDay) ?? Dates.adding(.day, -29, to: to)
        let shopFilter = q["shop_id"].flatMap { Int64($0) }
        let days = max(1, (Dates.calendar.dateComponents([.day], from: from, to: to).day ?? 0) + 1)
        // About €14 a day, varying by weekday.
        func daily(_ d: Date) -> Int64 {
            let wd = Dates.calendar.component(.weekday, from: d)
            let dom = Dates.calendar.component(.day, from: d)
            return Int64(900 + (wd * 311 + dom * 97) % 1100)
        }
        var dates: [Date] = []
        for i in 0..<days { dates.append(Dates.adding(.day, i, to: from)) }
        let shopShare: [Int64: Double] = [1: 0.42, 2: 0.27, 3: 0.17, 4: 0.09, 5: 0.05]
        let factor = shopFilter.flatMap { shopShare[$0] } ?? 1
        let total = Int64(Double(dates.reduce(Int64(0)) { $0 + daily($1) }) * factor)
        var buckets: [SpendBucket] = []
        switch group {
        case .day, .week, .month:
            var map: [String: (label: String, cents: Int64, start: Date)] = [:]
            for d in dates {
                let key: String
                let label: String
                switch group {
                case .day:
                    key = Dates.isoDay(d); label = "\(Dates.calendar.component(.day, from: d))"
                case .week:
                    let start = Dates.calendar.dateInterval(of: .weekOfYear, for: d)?.start ?? d
                    key = Dates.isoDay(start); label = "\(Dates.calendar.component(.day, from: start)) \(Dates.monthShort(start))"
                default:
                    let start = Dates.startOfMonth(d)
                    key = Dates.isoDay(start); label = Dates.monthShort(start)
                }
                let cents = Int64(Double(daily(d)) * factor)
                let start = Dates.parseIsoDay(key) ?? d
                map[key] = (label, (map[key]?.cents ?? 0) + cents, start)
            }
            buckets = map.sorted { $0.value.start < $1.value.start }.map {
                SpendBucket(key: $0.key, label: $0.value.label, cents: $0.value.cents, receipts: max(1, Int($0.value.cents / 3500)))
            }
        case .product:
            let cat = q["category_id"].flatMap { Int64($0) }
            let inCat = products.filter { cat == nil || $0.categoryId == cat }
            buckets = inCat.enumerated().map { i, p in
                let cents = Int64(Double(total) * 0.04 / Double(i + 1))
                let unit = p.netUnit == nil ? "kg" : "pc"
                return SpendBucket(key: String(p.id), label: p.name, cents: cents, receipts: max(1, 12 - i), qty: unit == "kg" ? 3.2 : Double(12 - i), unit: unit)
            }
            .sorted { $0.cents > $1.cents }
        case .shop:
            buckets = shops.map { s in
                let c = Int64(Double(total) * (shopShare[s.id] ?? 0.03))
                return SpendBucket(key: String(s.id), label: s.name, cents: c, receipts: max(1, Int(c / 3500)))
            }
            .sorted { $0.cents > $1.cents }
        case .category:
            let shares: [Double] = [0.19, 0.17, 0.16, 0.10, 0.14, 0.04, 0.06, 0.05, 0.03]
            buckets = categories.prefix(shares.count).enumerated().map { i, c in
                let cents = Int64(Double(total) * shares[i])
                return SpendBucket(key: String(c.id), label: c.name, cents: cents, receipts: max(1, Int(cents / 3500)))
            }
            let rest = total - buckets.reduce(0) { $0 + $1.cents }
            buckets.append(SpendBucket(key: "none", label: "Not linked", cents: rest, receipts: 3))
            buckets.sort { $0.cents > $1.cents }
        }
        // split_by=category on time groupings: fixed shares per category, parts sum to the bucket.
        let split = SpendSplit(rawValue: q["split_by"] ?? "")
        if split == .category, [.day, .week, .month].contains(group) {
            let shares: [Double] = [0.19, 0.17, 0.16, 0.10, 0.14, 0.04, 0.06, 0.05, 0.03]
            for i in buckets.indices where buckets[i].cents > 0 {
                var parts: [SpendBucket.Part] = categories.prefix(shares.count).enumerated().map { j, c in
                    SpendBucket.Part(key: String(c.id), label: c.name, cents: Int64(Double(buckets[i].cents) * shares[j]))
                }
                let rest = buckets[i].cents - parts.reduce(0) { $0 + $1.cents }
                parts.append(SpendBucket.Part(key: "none", label: "Uncategorised", cents: rest))
                buckets[i].split = parts.sorted { $0.cents > $1.cents }
            }
        }
        return Spend(from: from, to: Dates.adding(.day, 1, to: to), groupBy: group, splitBy: split, totalCents: total, receipts: max(1, Int(total / 3500)), buckets: buckets)
    }

    // MARK: Helpers

    private func lineIndex(_ id: Int64?) -> (Int, Int)? {
        guard let id else { return nil }
        for r in receipts.indices {
            if let l = receipts[r].lines?.firstIndex(where: { $0.id == id }) { return (r, l) }
        }
        return nil
    }

    private func shopName(_ id: Int64?) -> String { id == nil ? "Unassigned" : (shops.first { $0.id == id }?.name ?? "Shop") }

    // MARK: Product auto-population state

    private var reviewItems: [ReviewItem]?
    private struct UndoRecord { var kind: String; var removed: Product; var survivor: Product?; var item: ReviewItem? }
    private var undoRecords: [Int64: UndoRecord] = [:]

    private func reviewProduct(_ id: Int64, _ name: String, cat: Int64?, barcode: String? = nil, via: String, reason: String) -> Product {
        var pr = MockData.product(id, name, en: name, cat: cat ?? 0, size: nil, unit: nil, barcode: barcode)
        pr.categoryId = cat
        pr.nameEn = nil
        pr.createdVia = via
        pr.needsReview = true
        pr.reviewReason = reason
        pr.createdAt = .now.addingTimeInterval(-3600)
        return pr
    }

    private func seedReviewItems() -> [ReviewItem] {
        func line(_ id: Int64, _ raw: String, _ shop: String, cents: Int64) -> ReviewSourceLine {
            ReviewSourceLine(lineId: id, receiptId: 104, rawText: raw, shopName: shop, purchasedAt: .now.addingTimeInterval(-86400 * 3), lineTotalCents: cents)
        }
        let melk = SuggestedMerge(productId: 1, name: "Halfvolle melk", nameEn: nil, brand: nil, score: 0.88)
        let yog = SuggestedMerge(productId: 2, name: "Griekse yoghurt 0%", nameEn: nil, brand: nil, score: 0.82)
        return [
            ReviewItem(product: reviewProduct(201, "Arla bio halfvolle melk", cat: 1, via: "receipt", reason: "near_match"), reason: "near_match", suggestedMerge: melk,
                       sourceLineCount: 3, sourceLines: [line(901, "ARLA BIO HALFV. MELK", "Albert Heijn", cents: 189)]),
            ReviewItem(product: reviewProduct(202, "Griekse yoghurt", cat: 1, barcode: "8718452390010", via: "barcode", reason: "barcode_name_match"), reason: "barcode_name_match", suggestedMerge: yog,
                       sourceLineCount: 1, sourceLines: [line(902, "AH GRIEKS YOGH", "Albert Heijn", cents: 219)]),
            ReviewItem(product: reviewProduct(203, "ST#TIEG&LD", cat: nil, via: "receipt", reason: "odd_name"), reason: "odd_name", suggestedMerge: nil,
                       sourceLineCount: 1, sourceLines: [line(903, "ST#TIEG&LD", "Lidl", cents: 25)]),
            ReviewItem(product: reviewProduct(204, "Hummus naturel", cat: nil, via: "receipt", reason: "no_category"), reason: "no_category", suggestedMerge: nil,
                       sourceLineCount: 1, sourceLines: [line(904, "HUMMUS NATUREL", "Lidl", cents: 149)]),
            ReviewItem(product: reviewProduct(205, "Unknown product 2345", cat: nil, barcode: "8710400012345", via: "barcode", reason: "barcode_minimal"), reason: "barcode_minimal", suggestedMerge: nil,
                       sourceLineCount: 0, sourceLines: []),
            ReviewItem(product: reviewProduct(206, "Unknown product 9876", cat: nil, barcode: "8710400019876", via: "barcode", reason: "barcode_off_unavailable"), reason: "barcode_off_unavailable", suggestedMerge: nil,
                       sourceLineCount: 0, sourceLines: []),
        ]
    }

    private func queue() -> [ReviewItem] {
        if reviewItems == nil {
            if ProcessInfo.processInfo.arguments.contains("-mock-empty-queue") { reviewItems = [] } else { reviewItems = seedReviewItems() }
        }
        return reviewItems ?? []
    }

    private func reviewQueueResponse(_ q: [String: String]) -> (Int, Data) {
        var items = queue()
        var counts: [String: Int] = [:]
        for r in ["near_match", "barcode_name_match", "odd_name", "no_category", "barcode_minimal", "barcode_off_unavailable"] { counts[r] = items.filter { $0.reason == r }.count }
        let total = items.count
        if let reason = q["reason"], !reason.isEmpty { items = items.filter { $0.reason == reason } }
        let limit = Int(q["limit"] ?? "") ?? 50
        let offset = Int(q["offset"] ?? "") ?? 0
        let page = limit == 0 ? [] : Array(items.dropFirst(offset).prefix(limit))
        return ok(ReviewQueueResponse(total: total, counts: counts, items: page))
    }

    private func review(_ pid: Int64?, _ body: Data) -> (Int, Data) {
        guard let pid, let req = try? decoder.decode(ReviewBody.self, from: body) else { return bad() }
        var items = queue()
        guard let i = items.firstIndex(where: { $0.id == pid }) else { return problem(409, "Not in the queue", "This product was already handled.") }
        let item = items[i]
        var product = item.product
        switch req.action {
        case "accept", "keep_separate":
            if let name = req.name { product.name = name }
            if let c = req.categoryId { product.categoryId = c; product.categorySource = "manual" }
            product.needsReview = false; product.reviewReason = nil
            items.remove(at: i); reviewItems = items
            return ok(ReviewResult(product: product))
        case "merge":
            guard let target = req.intoProductId ?? item.suggestedMerge?.productId else { return bad() }
            let targetProduct = products.first { $0.id == target } ?? product
            // near_match: the card disappears into the suggestion. barcode_name_match: the card survives.
            let survivor = item.reason == "barcode_name_match" ? product : targetProduct
            let away = item.reason == "barcode_name_match" ? targetProduct : product
            let mid = newId()
            undoRecords[mid] = UndoRecord(kind: "merge", removed: away, survivor: survivor, item: item)
            items.remove(at: i); reviewItems = items
            return ok(ReviewResult(product: survivor, mergeId: mid, kind: "merge", mergedAway: MergedAway(id: away.id, name: away.name), linesMoved: 3, aliasesMoved: 1))
        case "discard":
            if product.createdVia == "manual" { return problem(422, "Can't discard", "Manual products can't be discarded.") }
            let did = newId()
            undoRecords[did] = UndoRecord(kind: "discard", removed: product, survivor: nil, item: item)
            items.remove(at: i); reviewItems = items
            return ok(ReviewResult(product: product, discardId: did, kind: "discard", linesUnlinked: item.sourceLineCount))
        default:
            return bad()
        }
    }

    private func mergePlain(_ pid: Int64?, _ body: Data) -> (Int, Data) {
        guard let pid, let into = (try? decoder.decode(MergeIntoBody.self, from: body))?.intoProductId, pid != into else { return problem(422, "Invalid merge", nil) }
        let away = products.first { $0.id == pid } ?? MockData.product(pid, "Merged product", en: "Merged product", cat: 1, size: nil, unit: nil)
        let survivor = products.first { $0.id == into } ?? away
        let mid = newId()
        undoRecords[mid] = UndoRecord(kind: "merge", removed: away, survivor: survivor, item: nil)
        return ok(ReviewResult(product: survivor, mergeId: mid, kind: "merge", mergedAway: MergedAway(id: away.id, name: away.name), linesMoved: 1, aliasesMoved: 0))
    }

    private struct MergeIntoBody: Codable { var intoProductId: Int64 }

    private func undo(_ mid: Int64?) -> (Int, Data) {
        guard let mid, let record = undoRecords.removeValue(forKey: mid) else { return problem(409, "Can't undo", "Already undone or too old.") }
        if let item = record.item, var items = reviewItems {
            var restored = item
            restored.product.needsReview = true
            items.insert(restored, at: min(items.count, 0))
            reviewItems = items
            // keep the original reason order: near_match first
            reviewItems = items.sorted { Self.reasonRank($0.reason) < Self.reasonRank($1.reason) }
        }
        var restoredProduct = record.item?.product ?? record.removed
        restoredProduct.needsReview = record.item != nil
        return ok(UndoResult(kind: record.kind, product: restoredProduct, into: record.survivor, linesRestored: 3, linesSkipped: 0))
    }

    private static func reasonRank(_ r: String) -> Int {
        ["near_match", "barcode_name_match", "odd_name", "no_category", "barcode_minimal", "barcode_off_unavailable"].firstIndex(of: r) ?? 99
    }

    /// Codes: a known barcode → 200; 87… → OFF data; 40… → not on OFF; 99… → OFF down;
    /// …0010 also suggests a merge with "Griekse yoghurt 0%"; short codes → 400.
    private func createBarcodeProduct(_ code: String) -> (Int, Data) {
        guard code.count >= 8, code.allSatisfy(\.isNumber) else { return problem(400, "Not a valid barcode", nil) }
        if let known = products.first(where: { $0.barcode == code }) {
            return ok(BarcodeCreateResponse(status: "known", createdNow: false, offStatus: nil, product: known, mergeSuggestion: nil, unmatchedLines: []))
        }
        let id = newId()
        var product: Product
        let status: String
        let off: String
        var merge: SuggestedMerge?
        if code.hasPrefix("99") {
            product = reviewProduct(id, "Unknown product \(code.suffix(4))", cat: nil, barcode: code, via: "barcode", reason: "barcode_off_unavailable")
            status = "created_minimal"; off = "unavailable"
        } else if code.hasPrefix("40") || !code.hasPrefix("87") {
            product = reviewProduct(id, "Unknown product \(code.suffix(4))", cat: nil, barcode: code, via: "barcode", reason: "barcode_minimal")
            status = "created_minimal"; off = "not_found"
        } else {
            product = reviewProduct(id, "AH Volle yoghurt", cat: 1, barcode: code, via: "barcode", reason: "barcode_name_match")
            product.needsReview = false; product.reviewReason = nil
            status = "created"; off = "found"
            if code.hasSuffix("0010") {
                merge = SuggestedMerge(productId: 2, name: "Griekse yoghurt 0%", nameEn: nil, brand: nil, score: 0.84)
                product.needsReview = true; product.reviewReason = "barcode_name_match"; product.suggestedMerge = merge
            }
        }
        products.append(product)
        if product.needsReview == true, var items = reviewItems ?? Optional(queue()) {
            items.append(ReviewItem(product: product, reason: product.reviewReason ?? "", suggestedMerge: merge, sourceLineCount: 0, sourceLines: []))
            reviewItems = items.sorted { Self.reasonRank($0.reason) < Self.reasonRank($1.reason) }
        }
        let lines: [UnmatchedLine] = status == "created" ? [UnmatchedLine(line: MockData.line(950 + id, receipt: 104, 1, "AH VOLLE YOGH", total: 119), shopId: 1, shopName: "Albert Heijn", purchasedAt: .now.addingTimeInterval(-86400 * 2), score: 0.8)] : []
        return ok(BarcodeCreateResponse(status: status, createdNow: true, offStatus: off, product: product, mergeSuggestion: merge, unmatchedLines: lines), status: 201)
    }

    private var acceptedSuggestions: Set<Int64> = []

    /// One receipt that "looks like Aldi", for trying the suggestion card in -mock.
    private func mockSuggestion(for id: Int64) -> ShopSuggestion? {
        let unassigned = receipts.first { $0.id == id }.map { $0.shopId == nil && $0.status != .processing } ?? false
        guard id == 102 || unassigned, !acceptedSuggestions.contains(id) else { return nil }
        return ShopSuggestion(kind: .new, key: "new:aldi|lange kerkstraat 34", shopId: nil, name: "Aldi", chain: "Aldi", branch: "Hoofdstraat 12, Teststad", domain: "aldi.nl",
                              confidence: 0.92, evidence: ["ALDI", "Hoofdstraat 12", "Teststad"], waitingReceiptIds: acceptedSuggestions.contains(104) ? [] : [104])
    }

    private func newId() -> Int64 {
        nextId += 1
        return nextId
    }

    private func ok<T: Encodable>(_ value: T, status: Int = 200) -> (Int, Data) {
        (status, (try? encoder.encode(value)) ?? Data())
    }

    private func problem(_ status: Int, _ title: String, _ detail: String?) -> (Int, Data) {
        ok(Problem(type: nil, title: title, status: status, detail: detail), status: status)
    }

    /// Same mapping the real server backfilled from the old name-based colours.
    private static func tone(for name: String) -> String {
        switch name {
        case "Dairy": "blue"
        case "Produce": "green"
        case "Meat & fish": "red"
        case "Bakery": "orange"
        case "Pantry": "yellow"
        case "Frozen": "cyan"
        case "Drinks": "purple"
        case "Household": "teal"
        case "Personal care": "pink"
        default: "grey"
        }
    }

    private func categoryNameTaken() -> (Int, Data) {
        ok(Problem(type: nil, title: "category name taken", status: 409, detail: nil, code: "category_name_taken", productCount: nil), status: 409)
    }

    private func notFound() -> (Int, Data) { problem(404, "Not found", nil) }
    private func bad() -> (Int, Data) { problem(400, "Bad request", nil) }

    /// A drawn receipt for the image viewer.
    static func receiptImage() -> Data {
        let size = CGSize(width: 600, height: 1500)
        let renderer = UIGraphicsImageRenderer(size: size)
        let image = renderer.image { ctx in
            UIColor(hex: 0xF7F6F1).setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
            let font = UIFont.monospacedSystemFont(ofSize: 22, weight: .medium)
            let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: UIColor(hex: 0x1D1D1B)]
            var y: CGFloat = 60
            let rows = ["ALBERT HEIJN", "OUDEGRACHT 120", ""] + MockData.reviewLines.map {
                let amount = Money.plain($0.lineTotalCents)
                return $0.rawText.padding(toLength: 30, withPad: " ", startingAt: 0) + amount
            } + ["", "TOTAAL                        38,12", "PIN                           38,12"]
            for row in rows {
                (row as NSString).draw(at: CGPoint(x: 50, y: y), withAttributes: attrs)
                y += 40
            }
        }
        return image.jpegData(compressionQuality: 0.8) ?? Data()
    }
}
