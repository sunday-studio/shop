import Foundation
import SwiftUI

/// Launch with `-bench` (plus `-mock -mock-large`) to log how long the hot paths take.
@MainActor
enum Bench {
    static func run(_ app: AppModel) async {
        func time(_ name: String, _ n: Int, _ body: () -> Void) {
            let start = ContinuousClock.now
            for _ in 0..<n { body() }
            let d = ContinuousClock.now - start
            let us = Double(d.components.seconds) * 1e6 + Double(d.components.attoseconds) / 1e12
            NSLog("BENCH %@: %.1f us per call (%d calls)", name, us / Double(n), n)
        }
        try? await Task.sleep(for: .seconds(4))
        try? await app.catalog.syncAll()
        await app.home.refresh()
        NSLog("BENCH products=%d receipts=%d", app.catalog.products.count, app.home.receipts.count)

        time("TypeStyle.text amount", 5000) { _ = TypeStyle.rowTitle.text("€1.234,56") }
        time("TypeStyle.text sub", 5000) { _ = TypeStyle.rowSub.text("500 g · Dairy · 12:08 · 9 items") }
        time("Text plain", 5000) { _ = Text("€1.234,56") }

        let model = ProductsModel(app: app)
        time("ProductsModel.products (sort)", 20) { _ = model.products }
        time("HomeModel.dayGroups", 50) { _ = app.home.dayGroups }
        time("Dates.dayLabel", 2000) { _ = Dates.dayLabel(Date()) }
        time("Money.format", 5000) { _ = Money.format(123456) }
        time("ProductSearch.local", 50) { _ = ProductSearch.local("mel", in: app.catalog, limit: 50) }
        time("DiskStore.load products", 5) { _ = DiskStore.load([Product].self, from: "products.json") }
        for sample in ["2026-10-11T03:12:09Z", "2026-10-11T03:12:09.123Z", "2026-10-11T05:12:09+02:00", "2024-02-29T23:59:59.5-05:30", "1999-12-31T00:00:00Z"] {
            let f = Dates.fastRFC3339(sample), g = Dates.parseRFC3339(sample)
            let iso = ISO8601DateFormatter(); iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            let iso2 = ISO8601DateFormatter(); iso2.formatOptions = [.withInternetDateTime]
            let ref = iso.date(from: sample) ?? iso2.date(from: sample)
            NSLog("BENCH date %@ fast=%@ ref=%@ match=%d", sample, String(describing: f?.timeIntervalSince1970), String(describing: ref?.timeIntervalSince1970), f == ref ? 1 : 0)
            _ = g
        }
        let probe = Product(id: 999_999, name: "Zzz bench", nameEn: nil, nameEnSource: nil, brand: nil, categoryId: 1, barcode: nil, netQuantity: nil, netUnit: nil, openfoodfactsCode: nil, hasImage: false, nutrition: nil, score: nil, createdAt: .now, updatedAt: .now)
        let before = app.catalog.productsRecentFirst.count
        app.catalog.upsert(probe)
        NSLog("BENCH cache invalidation: before=%d after=%d found=%d search=%d", before, app.catalog.productsRecentFirst.count, app.catalog.productsRecentFirst.contains { $0.id == 999_999 } ? 1 : 0, ProductSearch.local("zzz bench", in: app.catalog).count)
        app.catalog.removeProduct(999_999)
        NSLog("BENCH done")
    }
}
