import Foundation
import Observation

/// Read-mostly reference data cached on the phone: shops, categories and the product catalog.
/// Lines only carry `product_id`, so names and category colours resolve through here.
@MainActor
@Observable
final class Catalog {
    private(set) var shops: [Shop] = []
    private(set) var categories: [Category] = []
    private(set) var products: [Int64: Product] = [:] {
        didSet { derivedCache = nil }
    }
    private(set) var lastSync: Date?
    private(set) var isSyncing = false
    /// The saved product list is still being read from disk (a few tens of ms after launch).
    private(set) var isLoadingCache = false

    @ObservationIgnored private let api: APIClient

    /// Lists derived from `products`, built once per change instead of once per screen refresh.
    private struct Derived {
        var recentFirst: [Product]
        var haystacks: [Int64: String]
    }
    @ObservationIgnored private var derivedCache: Derived?

    private static let lastShopKey = "catalog.lastShopId"

    init(api: APIClient) {
        self.api = api
        shops = DiskStore.load([Shop].self, from: "shops.json") ?? []
        categories = DiskStore.load([Category].self, from: "categories.json") ?? []
        // The product cache is the biggest file; read it after the first frame instead of before.
        isLoadingCache = true
        Task { [weak self] in
            let list = await Task.detached(priority: .userInitiated) { DiskStore.load([Product].self, from: "products.json") }.value ?? []
            guard let self else { return }
            // A sync that finished first has fresher data; keep it.
            if self.products.isEmpty { self.products = Dictionary(list.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a }) }
            self.isLoadingCache = false
        }
        lastSync = UserDefaults.standard.object(forKey: "catalog.lastSync") as? Date
        ShopLogos.register(shops, api: api)
    }

    // MARK: Sync

    /// Shops and categories (fast; on launch and pull to refresh).
    func refreshReference() async throws {
        async let s = api.shops()
        async let c = api.categories()
        let (shops, categories) = try await (s, c)
        self.shops = shops.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        self.categories = categories.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        DiskStore.save(self.shops, to: "shops.json")
        DiskStore.save(self.categories, to: "categories.json")
        ShopLogos.register(self.shops, api: api)
    }

    /// Whole product catalog, paged by 200.
    func refreshProducts() async throws {
        var all: [Product] = []
        var offset = 0
        while true {
            let page = try await api.products(limit: 200, offset: offset)
            all += page
            if page.count < 200 || offset > 20_000 { break }
            offset += 200
        }
        products = Dictionary(all.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        DiskStore.saveInBackground(all, to: "products.json")
    }

    func syncAll() async {
        guard !isSyncing else { return }
        isSyncing = true
        defer { isSyncing = false }
        do {
            try await refreshReference()
            try await refreshProducts()
            lastSync = .now
            UserDefaults.standard.set(lastSync, forKey: "catalog.lastSync")
        } catch {
            // Keep the cached copy; callers surface connectivity separately.
        }
    }

    // MARK: Lookups

    func shop(_ id: Int64?) -> Shop? {
        guard let id else { return nil }
        return shops.first { $0.id == id }
    }

    func shopName(_ id: Int64?) -> String {
        shop(id)?.name ?? "Unknown shop"
    }

    func category(_ id: Int64?) -> Category? {
        guard let id else { return nil }
        return categories.first { $0.id == id }
    }

    func product(_ id: Int64?) -> Product? {
        guard let id else { return nil }
        return products[id]
    }

    func categoryName(forProduct id: Int64?) -> String? {
        category(product(id)?.categoryId)?.name
    }

    func categoryColor(forProduct id: Int64?) -> CategoryColor.Swatch {
        CategoryColor.swatch(for: category(product(id)?.categoryId))
    }

    var sortedProducts: [Product] {
        products.values.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
    }

    private var derived: Derived {
        _ = products // keeps views that read the cached lists observing `products`
        if let derivedCache { return derivedCache }
        let recent = products.values.sorted { a, b in
            switch (a.lastBoughtAt, b.lastBoughtAt) {
            case let (x?, y?) where x != y: return x > y
            case (.some, nil): return true
            case (nil, .some): return false
            default: return a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
            }
        }
        var hay: [Int64: String] = [:]
        hay.reserveCapacity(products.count)
        for p in products.values { hay[p.id] = ProductSearch.fold(p.name + " " + (p.nameEn ?? "") + " " + (p.brand ?? "")) }
        let built = Derived(recentFirst: recent, haystacks: hay)
        derivedCache = built
        return built
    }

    /// Every product, most recently bought first (then A to Z).
    var productsRecentFirst: [Product] { derived.recentFirst }

    /// Folded "name nameEn brand" text used by local search.
    func searchHaystack(_ id: Int64) -> String? { derived.haystacks[id] }

    // MARK: Last used shop (C.2 preselect)

    var lastShopId: Int64? {
        get { (UserDefaults.standard.object(forKey: Self.lastShopKey) as? NSNumber)?.int64Value }
        set { UserDefaults.standard.set(newValue.map { NSNumber(value: $0) }, forKey: Self.lastShopKey) }
    }

    var lastShop: Shop? { shop(lastShopId) ?? shops.first }

    // MARK: Local updates after writes

    func upsert(_ shop: Shop) {
        if let i = shops.firstIndex(where: { $0.id == shop.id }) { shops[i] = shop } else { shops.append(shop) }
        shops.sort { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        DiskStore.save(shops, to: "shops.json")
        ShopLogos.register(shops, api: api)
        ImageCache.shared.remove(ShopLogos.cacheKey(for: shop.name))
    }

    func removeShop(_ id: Int64) {
        shops.removeAll { $0.id == id }
        DiskStore.save(shops, to: "shops.json")
    }

    func upsert(_ category: Category) {
        if let i = categories.firstIndex(where: { $0.id == category.id }) { categories[i] = category } else { categories.append(category) }
        categories.sort { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        DiskStore.save(categories, to: "categories.json")
    }

    func setCategories(_ list: [Category]) {
        categories = list
        DiskStore.save(categories, to: "categories.json")
    }

    func removeCategory(_ id: Int64) {
        categories.removeAll { $0.id == id }
        DiskStore.save(categories, to: "categories.json")
    }

    func upsert(_ product: Product) {
        var p = product
        p.score = nil
        products[p.id] = p
        DiskStore.saveInBackground(Array(products.values), to: "products.json")
    }

    func removeProduct(_ id: Int64) {
        products[id] = nil
        DiskStore.saveInBackground(Array(products.values), to: "products.json")
    }

    func reset() {
        shops = []
        categories = []
        products = [:]
        lastSync = nil
    }
}
