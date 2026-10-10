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
        let all = app.catalog.products.values.sorted { a, b in
            switch (a.lastBoughtAt, b.lastBoughtAt) {
            case let (x?, y?) where x != y: return x > y
            case (.some, nil): return true
            case (nil, .some): return false
            default: return a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
            }
        }
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
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                LargeTitleHeader(title: "Products", detail: app.catalog.products.isEmpty ? nil : "\(app.catalog.products.count)") {
                    IconButton(icon: .barcode, label: "Scan barcode") { app.startCapture(.unpack) }
                }

                if app.productReview.total > 0 {
                    AttentionCard(count: app.productReview.total, title: "New products to check", hint: "about 2 min") {
                        app.push(.productReview)
                    }
                    .padding(.horizontal, Space.edge)
                    .padding(.top, Space.lx)
                    .transition(.opacity)
                }

                SearchField(placeholder: "Products or receipt names", text: $model.query)
                    .padding(.horizontal, Space.edge)
                    .padding(.top, Space.lx)
                    .onChange(of: model.query) { _, _ in model.queryChanged() }

                categoryChips(model)
                    .padding(.top, Space.l)

                list(model)
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.ml)
            }
            .padding(.bottom, Space.tabBarClearance)
        }
        .scrollDismissesKeyboard(.immediately)
        .scrollIndicators(.hidden)
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
                    Chip(title: c.name, isSelected: model.categoryId == c.id, dot: CategoryColor.swatch(for: c.name).color) {
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
    private func list(_ model: ProductsModel) -> some View {
        let products = model.products
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(model.isSearching ? (model.searching ? "Searching…" : "Results") : "Recently bought")
                Spacer()
                Text("Best per unit")
            }
            .textStyle(.label)
            .foregroundStyle(Palette.ink2)
            .padding(.top, 6)
            .padding(.bottom, 2)

            if products.isEmpty {
                if model.isSearching {
                    newProductRow(name: model.query)
                } else if app.catalog.isSyncing {
                    LoadingState(title: "Copying your catalog")
                } else {
                    EmptyState(title: "No products yet", message: "Unpack your groceries to add products by barcode, or link receipt lines to new products.") {
                        GlyphIllustration(icon: .products, color: Accent.blue)
                    }
                    .padding(.top, 30)
                }
            } else {
                LazyVStack(spacing: 0) {
                    ForEach(Array(products.enumerated()), id: \.element.id) { index, product in
                        SwipeRow(
                            leading: SwipeAction(title: "Add to list", icon: .plus, color: Accent.blue, haptic: .light) { addToList(product) },
                            trailing: SwipeAction(title: "Delete", icon: .trash, color: Accent.red, haptic: nil) { askDelete(product) }
                        ) {
                            Button {
                                guard !app.rowSwipeActive else { return }
                                app.push(.product(product.id))
                            } label: {
                                ProductRow(
                                    name: product.name,
                                    meta: meta(product),
                                    category: app.catalog.category(product.categoryId)?.name,
                                    imageURL: app.api.productImageURL(product),
                                    price: product.cheapest.map { Money.perUnit($0.latestUnitCents, unit: $0.baseUnit) },
                                    priceShop: product.cheapest?.shopName
                                )
                            }
                            .buttonStyle(.press(haptic: nil))
                        }
                        .accessibilityAction(named: "Add to list") { addToList(product) }
                        .accessibilityAction(named: "Delete") { askDelete(product) }
                        .staggeredAppear(index: index)
                    }
                    if model.isSearching {
                        newProductRow(name: model.query)
                    }
                }
            }
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
                    app.catalog.removeProduct(product.id)
                    Haptics.play(.error)
                    return true
                } catch let error as APIError {
                    app.toasts.error(error.message)
                    return false
                } catch { return false }
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
                    Text("New product").textStyle(.rowTitle).foregroundStyle(Palette.ink)
                    Text("“\(name.trimmingCharacters(in: .whitespaces))”").textStyle(.rowSub).foregroundStyle(Palette.ink2)
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
