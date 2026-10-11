import SwiftUI

/// P · Products: fuzzy search, category chips, best per-unit price per product.
@MainActor
@Observable
final class ProductsModel {
    var query = ""
    var categoryId: Int64?
    private(set) var results: [Product]?
    private(set) var searching = false

    @ObservationIgnored private let app: AppModel
    @ObservationIgnored private var searchTask: Task<Void, Never>?

    init(app: AppModel) {
        self.app = app
    }

    /// Search results, or the cached catalog (recently bought first) filtered by category.
    var products: [Product] {
        if let results { return results }
        let all = app.catalog.productsRecentFirst
        guard let categoryId else { return all }
        return all.filter { $0.categoryId == categoryId }
    }

    var isSearching: Bool { !query.trimmingCharacters(in: .whitespaces).isEmpty }

    func queryChanged() {
        searchTask?.cancel()
        let text = query.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else {
            results = nil
            searching = false
            return
        }
        var local = ProductSearch.local(text, in: app.catalog, limit: 50)
        if let categoryId { local = local.filter { $0.categoryId == categoryId } }
        results = local
        searching = true
        let category = categoryId
        searchTask = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            if let remote = try? await app.api.products(query: text, categoryId: category, limit: 50), !Task.isCancelled {
                results = remote
            }
            searching = false
        }
    }

    func categoryChanged() {
        if isSearching { queryChanged() }
    }

    func refresh() async {
        await app.catalog.syncAll()
        if isSearching { queryChanged() }
    }
}

struct ProductsView: View {
    @Environment(AppModel.self) private var app
    @State private var model: ProductsModel?

    var body: some View {
        Group {
            if let model { content(model) } else { Palette.bg }
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .onAppear { if model == nil { model = ProductsModel(app: app) } }
    }

    @ViewBuilder
    private func content(_ model: ProductsModel) -> some View {
        @Bindable var model = model
        List {
            LargeTitleHeader(title: "Products", detail: app.catalog.products.isEmpty ? nil : "\(app.catalog.products.count)") {
                HStack(spacing: Space.s) {
                    IconButton(icon: .plus, label: "Add product") { addProduct() }
                    IconButton(icon: .barcode, label: "Scan barcode") { app.startCapture(.unpack) }
                }
            }
            .shopListRow()

            if app.productReview.total > 0 {
                AttentionCard(count: app.productReview.total, title: "New products to check", hint: "about 2 min") {
                    app.push(.productReview)
                }
                .padding(.horizontal, Space.edge)
                .padding(.top, Space.lx)
                .shopListRow()
            }

            SearchField(placeholder: "Products or receipt names", text: $model.query)
                .padding(.horizontal, Space.edge)
                .padding(.top, Space.lx)
                .onChange(of: model.query) { _, _ in model.queryChanged() }
                .shopListRow()

            categoryChips(model)
                .padding(.top, Space.l)
                .shopListRow()

            productRows(model)
        }
        .shopListStyle()
        .contentMargins(.bottom, Space.tabBarClearance, for: .scrollContent)
        .scrollDismissesKeyboard(.immediately)
        .refreshable { await model.refresh() }
    }

    private func categoryChips(_ model: ProductsModel) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                Chip(title: "All", isSelected: model.categoryId == nil) {
                    model.categoryId = nil
                    model.categoryChanged()
                }
                ForEach(app.catalog.categories) { c in
                    Chip(title: c.name, isSelected: model.categoryId == c.id, dot: CategoryColor.swatch(for: c).color) {
                        model.categoryId = c.id
                        model.categoryChanged()
                    }
                }
            }
            .padding(.horizontal, Space.edge)
        }
        .scrollClipDisabled()
    }

    @ViewBuilder
    private func productRows(_ model: ProductsModel) -> some View {
        let products = model.products
        HStack {
            Text(model.isSearching ? (model.searching ? "Searching…" : "Results") : "Recently bought")
            Spacer()
            Text("Best per unit")
        }
        .textStyle(.label)
        .foregroundStyle(Palette.ink2)
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.ml + 6)
        .padding(.bottom, 2)
        .shopListRow()

        if products.isEmpty {
            Group {
                if model.isSearching {
                    newProductRow(name: model.query)
                } else if app.catalog.isSyncing || app.catalog.isLoadingCache {
                    SkeletonRows(count: 8) { SkeletonProductRow() }
                } else if let id = model.categoryId {
                    let name = app.catalog.category(id)?.name ?? "this category"
                    EmptyState(title: "Nothing in \(name) yet", message: "Products show up here once they're in this category.",
                               action: (title: "Show all", run: { model.categoryId = nil; model.categoryChanged() })) {
                        GlyphIllustration(icon: .products, color: Accent.blue)
                    }
                    .padding(.top, 30)
                } else {
                    VStack(spacing: Space.m) {
                        EmptyState(title: "No products yet", message: "Unpack your groceries to add products by barcode, or link receipt lines to new products.",
                                   action: (title: "Scan a barcode", run: { app.startCapture(.unpack) })) {
                            GlyphIllustration(icon: .products, color: Accent.blue)
                        }
                        Button("Add by hand") { addProduct() }
                            .textStyle(.label)
                            .foregroundStyle(Palette.blueText)
                            .buttonStyle(.press(haptic: .selection))
                    }
                    .padding(.top, 30)
                }
            }
            .padding(.horizontal, Space.gutter)
            .shopListRow()
        } else {
            ForEach(products) { product in
                SwipeActionRow(
                    leading: SwipeAction(icon: "cart.badge.plus", title: "Add to list", tint: Accent.green, haptic: .light) { addToList(product) },
                    trailing: SwipeAction(icon: "trash", title: "Delete", tint: Accent.red) { askDelete(product) }
                ) {
                Button { app.push(.product(product.id)) } label: {
                    ProductRow(
                        name: product.name,
                        meta: meta(product),
                        category: app.catalog.category(product.categoryId)?.name,
                        categoryToken: app.catalog.category(product.categoryId)?.color,
                        imageURL: app.api.productImageURL(product),
                        price: product.cheapest.map { Money.perUnit($0.latestUnitCents, unit: $0.baseUnit) },
                        priceShop: product.cheapest?.shopName
                    )
                }
                .buttonStyle(.press(haptic: nil))
                .padding(.horizontal, Space.gutter)
                }
                .shopListRow()
            }
            if model.isSearching {
                newProductRow(name: model.query)
                    .padding(.horizontal, Space.gutter)
                    .shopListRow()
            }
        }
    }

    private func addProduct() {
        app.trays.present("new-product") {
            ProductFormTray(mode: .create(name: "", barcode: nil, prefill: nil)) { product in app.catalog.upsert(product) }
        }
    }

    private func edit(_ product: Product) {
        app.trays.present("edit-product-\(product.id)") {
            ProductFormTray(mode: .edit(product)) { saved in app.catalog.upsert(saved) }
        }
    }

    private func addToList(_ product: Product) {
        app.list.add(text: product.name, productId: product.id, qty: 1)
        app.toasts.show("Added to the list", tone: .green)
        Task { await app.list.sync() }
    }

    private func askDelete(_ product: Product) {
        app.trays.present("delete-product") {
            ConfirmTray(title: "Delete \(product.name)?", message: "Receipt lines keep their history, but new receipts won't link to it. Its barcode is released.",
                        confirmTitle: "Delete product", destructive: true) {
                do {
                    try await app.api.deleteProduct(product.id)
                    return true
                } catch let error as APIError {
                    app.toasts.error(error.message)
                    return false
                } catch { return false }
            } afterDismiss: {
                app.catalog.removeProduct(product.id)
                Haptics.play(.warning)
                app.undoProductDelete(product)
            }
        }
    }

    private func meta(_ p: Product) -> String {
        let size = p.sizeText ?? (p.netUnit == nil ? "Loose" : nil)
        return [size, app.catalog.category(p.categoryId)?.name].compactMap { $0 }.joined(separator: " · ")
    }

    private func newProductRow(name: String) -> some View {
        Button {
            app.trays.present("new-product") {
                ProductFormTray(mode: .create(name: name.trimmingCharacters(in: .whitespaces), barcode: nil, prefill: nil)) { product in
                    app.push(.product(product.id))
                }
            }
        } label: {
            HStack(spacing: Space.l) {
                RoundedRectangle.continuous(Radius.m)
                    .strokeBorder(Palette.ink3, style: StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
                    .frame(width: Size.tile, height: Size.tile)
                    .overlay(IconView(.plus, size: 15, weight: .semibold).foregroundStyle(Palette.ink2))
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text("New product").foregroundStyle(Palette.ink)
                    TypeStyle.rowSub.text("“\(name.trimmingCharacters(in: .whitespaces))”").foregroundStyle(Palette.ink2)
                }
                Spacer()
            }
            .frame(height: Size.rowProduct)
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: .light))
    }
}

#Preview("Products") {
    let app = AppModel(mock: true)
    return NavigationStack { ProductsView() }
        .environment(app)
        .trayHost(app.trays)
        .task { await app.catalog.syncAll() }
}
