import SwiftUI

/// P.1 · Product detail: cheapest-at and usual price cards, price per unit by shop over time,
/// recent purchases, nutrition, receipt names, and Add to list.
@MainActor
@Observable
final class ProductDetailModel {
    let productId: Int64
    private(set) var product: Product?
    private(set) var points: [PricePoint] = []
    private(set) var compare: [ShopPrice] = []
    private(set) var aliases: [Alias] = []
    private(set) var error: APIError?
    private(set) var loaded = false

    @ObservationIgnored let app: AppModel

    init(productId: Int64, app: AppModel) {
        self.productId = productId
        self.app = app
        product = app.catalog.product(productId)
    }

    func load() async {
        do {
            async let p = app.api.product(productId)
            async let h = app.api.priceHistory(productId: productId)
            async let c = app.api.comparePrices(productId: productId)
            async let a = app.api.aliases(productId: productId)
            let (prod, hist, comp, al) = try await (p, h, c, a)
            product = prod
            points = hist
            compare = comp
            aliases = al
            app.catalog.upsert(prod)
            error = nil
        } catch let e as APIError {
            error = e
        } catch {}
        loaded = true
    }

    func replace(_ p: Product) { product = p }

    var baseUnit: BaseUnit? { compare.first?.baseUnit ?? points.last?.baseUnit }

    var cheapest: ShopPrice? { compare.first(where: \.cheapest) ?? compare.first }

    /// Median per-unit price of the last 6 purchases, and the shop bought at most.
    var usual: (cents: Int64, shop: String)? {
        let recent = points.suffix(6)
        guard !recent.isEmpty else { return nil }
        let sorted = recent.map(\.unitCents).sorted()
        let median = sorted[sorted.count / 2]
        let shop = Dictionary(grouping: points, by: \.shopName).max { $0.value.count < $1.value.count }?.key ?? ""
        return (median, shop)
    }

    var series: [PriceSeries] {
        let cheapestShop = cheapest?.shopId
        return Dictionary(grouping: points, by: \.shopId).map { shopId, pts in
            let name = pts.first?.shopName ?? ""
            return PriceSeries(id: shopId, shopName: name, color: ShopBrand.chartColor(for: name),
                               points: pts.sorted { $0.at < $1.at }.map { .init(id: $0.lineId, date: $0.at, unitCents: $0.unitCents) },
                               isCheapest: shopId == cheapestShop)
        }
        .sorted { $0.shopName < $1.shopName }
    }

    /// "1,5 l · Dairy · bought 24× since May"
    var metaLine: String {
        guard let product else { return "" }
        var parts: [String] = []
        if let size = product.sizeText { parts.append(size) }
        if let c = app.catalog.category(product.categoryId)?.name { parts.append(c) }
        let count = product.purchases ?? points.count
        if count > 0, let first = points.first {
            parts.append("bought \(count)× since \(Dates.monthShort(first.at))")
        } else if count > 0 {
            parts.append("bought \(count)×")
        }
        return parts.joined(separator: " · ")
    }

    func addToList() {
        guard let product else { return }
        app.list.add(text: product.name, productId: product.id, qty: 1)
        app.toasts.show("Added to the list", tone: .green)
        Task { await app.list.sync() }
    }

    func delete() async -> Bool {
        do {
            try await app.api.deleteProduct(productId)
            return true
        } catch let e as APIError {
            app.toasts.error(e.message)
            return false
        } catch { return false }
    }
}

struct ProductDetailView: View {
    let productId: Int64
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var model: ProductDetailModel?

    var body: some View {
        Group {
            if let model { content(model) } else { Palette.bg }
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .onAppear { if model == nil { model = ProductDetailModel(productId: productId, app: app) } }
        .task(id: model == nil) { await model?.load() }
    }

    @ViewBuilder
    private func content(_ model: ProductDetailModel) -> some View {
        ZStack(alignment: .bottom) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    NavHeader {
                        IconButton(icon: .edit, label: "Edit product") { edit(model) }
                            .disabled(model.product == nil)
                    }
                    if let product = model.product {
                        header(model, product)
                        cards(model)
                        chartSection(model)
                        purchases(model)
                        nutrition(model, product)
                        receiptNames(model)
                        Button("Merge into another product") { startMerge(model) }
                            .textStyle(.label)
                            .foregroundStyle(Palette.ink2)
                            .buttonStyle(.press(haptic: .selection))
                            .padding(.horizontal, Space.gutter)
                            .padding(.top, Space.xxl)
                        Button("Delete product") { confirmDelete(model) }
                            .textStyle(.label)
                            .foregroundStyle(Palette.redText)
                            .buttonStyle(.press(haptic: .selection))
                            .padding(.horizontal, Space.gutter)
                            .padding(.top, Space.l)
                    } else {
                        StatefulContent(phase: model.error.map { .failed($0.message) } ?? .loading, retry: { Task { await model.load() } }) {
                            VStack(alignment: .leading, spacing: Space.xl) {
                                SkeletonCard(height: 96)
                                SkeletonCard(height: 96)
                                SkeletonRows(count: 3) { SkeletonLineRow() }
                            }
                        } empty: {
                            EmptyView()
                        } content: {
                            EmptyView()
                        }
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, Space.xl)
                    }
                }
                .padding(.bottom, 120)
            }
            .scrollIndicators(.hidden)
            .refreshable { await model.load() }

            if model.product != nil {
                Button { model.addToList() } label: {
                    HStack(spacing: Space.m) {
                        IconView(.plus, size: 16, weight: .semibold)
                        Text("Add to list")
                    }
                }
                .buttonStyle(.shop(size: .large))
                .shadow(color: .black.opacity(0.18), radius: 15, y: 12)
                .padding(.horizontal, Space.edge)
                .padding(.bottom, Space.l)
            }
        }
    }

    private func header(_ model: ProductDetailModel, _ product: Product) -> some View {
        HStack(spacing: Space.xl) {
            ProductHero(name: product.name, category: app.catalog.category(product.categoryId)?.name, categoryToken: app.catalog.category(product.categoryId)?.color, imageURL: app.api.productImageURL(product))
            VStack(alignment: .leading, spacing: Space.xs) {
                TypeStyle.emptyTitle.sized(26).text(product.name)
                    .tracking(-0.04 * 26)
                    .foregroundStyle(Palette.ink)
                    .fixedSize(horizontal: false, vertical: true)
                if let en = product.nameEn, en.lowercased() != product.name.lowercased() {
                    TypeStyle.rowSub.text(en).foregroundStyle(Palette.ink3)
                }
                TypeStyle.body.sized(13.5).text(model.metaLine).foregroundStyle(Palette.ink2)
            }
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.xl)
    }

    @ViewBuilder
    private func cards(_ model: ProductDetailModel) -> some View {
        if let cheapest = model.cheapest {
            HStack(spacing: Space.m) {
                StatCard(caption: "Cheapest at \(cheapest.shopName)", value: Money.format(cheapest.latestUnitCents), unit: "/\(cheapest.baseUnit.rawValue)",
                         footnote: "Seen \(Dates.shortDay(cheapest.latestAt))", style: .highlight(Accent.yellow))
                if let usual = model.usual {
                    StatCard(caption: "You usually pay", value: Money.format(usual.cents), unit: "/\(cheapest.baseUnit.rawValue)",
                             footnote: usual.shop.isEmpty ? nil : "Mostly at \(usual.shop)")
                }
            }
            .padding(.horizontal, Space.edge)
            .padding(.top, 18)
        } else if model.loaded {
            Banner(tone: .neutral, glyph: .info, message: "No prices yet. Link this product to receipt lines and prices show up here.")
                .padding(.horizontal, Space.edge)
                .padding(.top, 18)
        }
    }

    @ViewBuilder
    private func chartSection(_ model: ProductDetailModel) -> some View {
        if !model.points.isEmpty, let unit = model.baseUnit {
            VStack(alignment: .leading, spacing: Space.ml) {
                Button { app.push(.priceHistory(model.productId)) } label: {
                    HStack(alignment: .firstTextBaseline) {
                        TypeStyle.rowTitle.text("Price per \(unit.longName)").foregroundStyle(Palette.ink)
                        Spacer()
                        TypeStyle.rowSub.text("After discounts").foregroundStyle(Palette.ink2)
                        IconView(.forward, size: 12, weight: .bold).foregroundStyle(Palette.ink3)
                    }
                }
                .buttonStyle(.press(haptic: .selection))
                PriceChart(series: model.series)
                VStack(spacing: 0) {
                    ForEach(model.compare) { price in
                        HStack(spacing: Space.l) {
                            ShopMark(name: price.shopName, size: 26)
                            (Text(price.shopName).foregroundStyle(Palette.ink)
                                + Text(" · \(Dates.dayLabel(price.latestAt))").font(TypeStyle.rowSub.font).foregroundStyle(Palette.ink2))
                                .textStyle(.rowTitle)
                                .lineLimit(1)
                            Spacer()
                            TypeStyle.rowTitle.text(Money.perUnit(price.latestUnitCents, unit: price.baseUnit))
                                .foregroundStyle(price.cheapest ? Palette.greenText : Palette.ink)
                        }
                        .frame(height: 46)
                        .hairline(.top)
                    }
                }
            }
            .padding(.horizontal, Space.gutter)
            .padding(.top, Space.xxl)
        }
    }

    @ViewBuilder
    private func purchases(_ model: ProductDetailModel) -> some View {
        if !model.points.isEmpty {
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    Text("Recent purchases")
                    Spacer()
                    Text("Paid")
                }
                .textStyle(.label)
                .foregroundStyle(Palette.ink2)
                .padding(.bottom, 2)
                ForEach(model.points.suffix(5).reversed()) { point in
                    HStack(spacing: Space.ml) {
                        Text(Dates.shortDay(point.at)).foregroundStyle(Palette.ink2).frame(width: 84, alignment: .leading)
                        Text("\(point.shopName) · \(Quantity.decimal(point.qty)) \(point.unit == .pc ? "×" : point.unit.rawValue)")
                            .foregroundStyle(Palette.ink)
                            .lineLimit(1)
                        Spacer()
                        TypeStyle.label.sized(14).text(Money.format(point.paidCents)).foregroundStyle(Palette.ink)
                    }
                    .textStyle(.body)
                    .frame(height: 40)
                    .hairline()
                }
            }
            .padding(.horizontal, Space.gutter)
            .padding(.top, Space.lx)
        }
    }

    /// P.1.3 · Nutrition (view; editing goes through the product form).
    private func nutrition(_ model: ProductDetailModel, _ product: Product) -> some View {
        VStack(alignment: .leading, spacing: Space.ml) {
            SectionLabel(title: "Nutrition") {
                if let n = product.nutrition, !n.isEmpty {
                    TypeStyle.rowSub.text("per \(n.per == "100ml" ? "100 ml" : "100 g")").foregroundStyle(Palette.ink2)
                }
            }
            if let n = product.nutrition, !n.isEmpty {
                LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: Space.m), count: 4), spacing: Space.m) {
                    nutrient("kcal", n.energyKcal, Accent.orange, unit: "")
                    nutrient("Fat", n.fat, Accent.yellow)
                    nutrient("Carbs", n.carbs, Accent.blue)
                    nutrient("Sugars", n.sugars, Accent.pink)
                    nutrient("Protein", n.protein, Accent.purple)
                    nutrient("Fibre", n.fibre, Accent.green)
                    nutrient("Salt", n.salt, Palette.ink3)
                    nutrient("Saturates", n.saturatedFat, Accent.yellow)
                }
            } else {
                LinkButton(title: "Add nutrition") { edit(model) }
            }
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.xxl)
    }

    private func nutrient(_ label: String, _ value: Double?, _ color: Color, unit: String = " g") -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 5) {
                RoundedRectangle.continuous(2).fill(color).frame(width: 7, height: 7)
                TypeStyle.caption.text(label).foregroundStyle(Palette.ink2).lineLimit(1)
            }
            TypeStyle.input.sized(16).text(value.map { Quantity.decimal($0, maxFraction: 1) + unit } ?? "–")
                .foregroundStyle(Palette.ink)
        }
        .padding(Space.ml)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.ml))
    }

    @ViewBuilder
    private func receiptNames(_ model: ProductDetailModel) -> some View {
        Button { app.push(.receiptNames(model.productId)) } label: {
            HStack(spacing: Space.ml) {
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text("Receipt names").foregroundStyle(Palette.ink)
                    TypeStyle.mono.sized(12.5).withFace(.monoRegular).text(aliasSummary(model)).foregroundStyle(Palette.ink2).lineLimit(1)
                }
                Spacer()
                IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
            }
            .frame(height: 50)
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: .selection))
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.ml)
    }

    private func aliasSummary(_ model: ProductDetailModel) -> String {
        guard let first = model.aliases.first else { return "None yet" }
        let more = model.aliases.count > 1 ? " +\(model.aliases.count - 1)" : ""
        return first.text.uppercased() + more
    }

    private func edit(_ model: ProductDetailModel) {
        guard let product = model.product else { return }
        app.trays.present("edit-product-\(product.id)") {
            ProductFormTray(mode: .edit(product)) { saved in model.replace(saved) }
        }
    }

    private func startMerge(_ model: ProductDetailModel) {
        guard let source = model.product else { return }
        app.trays.present("merge-pick") {
            MergeProductTray(source: source) { target in
                app.confirmMerge(source, into: target) { dismiss() }
            }
        }
    }

    private func confirmDelete(_ model: ProductDetailModel) {
        app.trays.present("delete-product") {
            ConfirmTray(title: "Delete this product?", message: "Receipt lines keep their history, but new receipts won't link to it. Its barcode is released.",
                        confirmTitle: "Delete product", destructive: true) {
                await model.delete()
            } afterDismiss: {
                let removed = model.product
                app.catalog.removeProduct(model.productId)
                dismiss()
                if let removed { app.undoProductDelete(removed) }
            }
        }
    }
}

extension BaseUnit {
    var longName: String {
        switch self {
        case .kg: "kilo"
        case .l: "litre"
        case .pc: "piece"
        }
    }
}

// MARK: - P.1.1 Price history

struct PriceHistoryView: View {
    let productId: Int64
    @Environment(AppModel.self) private var app
    @State private var points: [PricePoint] = []
    @State private var shopFilter: Int64?
    @State private var loaded = false

    private var shops: [(id: Int64, name: String)] {
        var seen = Set<Int64>()
        return points.compactMap { p -> (id: Int64, name: String)? in
            seen.insert(p.shopId).inserted ? (id: p.shopId, name: p.shopName) : nil
        }
    }

    private var filtered: [PricePoint] {
        guard let shopFilter else { return points }
        return points.filter { $0.shopId == shopFilter }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                NavHeader(title: app.catalog.product(productId)?.name ?? "Price history")

                if !points.isEmpty {
                    PriceChart(series: series, height: 180)
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, Space.xl)

                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 6) {
                            Chip(title: "All shops", isSelected: shopFilter == nil) { shopFilter = nil }
                            ForEach(shops.indices, id: \.self) { i in
                                let shop = shops[i]
                                Chip(title: shop.name, isSelected: shopFilter == shop.id, dot: ShopBrand.chartColor(for: shop.name)) { shopFilter = shop.id }
                            }
                        }
                        .padding(.horizontal, Space.edge)
                    }
                    .padding(.top, Space.xl)

                    VStack(spacing: 0) {
                        ForEach(filtered.reversed()) { p in
                            HStack(spacing: Space.l) {
                                ShopMark(name: p.shopName)
                                VStack(alignment: .leading, spacing: 1) {
                                    TypeStyle.rowTitle.text(Dates.dayLabel(p.at)).foregroundStyle(Palette.ink)
                                    TypeStyle.rowSub.text("\(p.shopName) · \(Quantity.decimal(p.qty)) \(p.unit.rawValue) · paid \(Money.format(p.paidCents))").foregroundStyle(Palette.ink2)
                                }
                                Spacer()
                                TypeStyle.rowTitle.text(Money.perUnit(p.unitCents, unit: p.baseUnit)).foregroundStyle(Palette.ink)
                            }
                            .frame(height: Size.rowReceipt)
                            .hairline()
                        }
                    }
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.m)
                } else if loaded {
                    EmptyState(title: "No purchases yet", message: "Prices appear once receipt lines are linked to this product.") {
                        GlyphIllustration(icon: .analytics, color: Accent.blue)
                    }
                    .padding(.top, 60)
                } else {
                    VStack(alignment: .leading, spacing: Space.xl) {
                        SkeletonChart()
                        SkeletonRows(count: 4) { SkeletonLineRow() }
                    }
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.xl)
                }
            }
            .padding(.bottom, 40)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task {
            points = (try? await app.api.priceHistory(productId: productId)) ?? []
            loaded = true
        }
    }

    private var series: [PriceSeries] {
        Dictionary(grouping: filtered, by: \.shopId).map { shopId, pts in
            let name = pts.first?.shopName ?? ""
            return PriceSeries(id: shopId, shopName: name, color: ShopBrand.chartColor(for: name),
                               points: pts.map { .init(id: $0.lineId, date: $0.at, unitCents: $0.unitCents) })
        }
        .sorted { $0.shopName < $1.shopName }
    }
}

// MARK: - P.1.4 Receipt names

struct ReceiptNamesView: View {
    let productId: Int64
    @Environment(AppModel.self) private var app
    @State private var aliases: [Alias] = []
    @State private var loaded = false

    private var groups: [(name: String, items: [Alias])] {
        let grouped = Dictionary(grouping: aliases) { $0.shopName ?? "Any shop" }
        return grouped.keys.sorted().map { ($0, grouped[$0] ?? []) }
    }

    var body: some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: 6) {
                    TypeStyle.screenTitle.text("Receipt names").foregroundStyle(Palette.ink)
                    TypeStyle.body.text("How each shop prints \(app.catalog.product(productId)?.name ?? "this product"). New receipts with these texts link automatically.").foregroundStyle(Palette.ink2)
                }
                .padding(.top, Space.lx)
                .listRowInsets(EdgeInsets(top: 0, leading: Space.gutter, bottom: Space.m, trailing: Space.gutter))
                .listRowBackground(Palette.bg)
                .listRowSeparator(.hidden)
            }

            ForEach(groups.indices, id: \.self) { gi in
                let group = groups[gi]
                Section {
                    ForEach(group.items) { alias in
                        SwipeActionRow(trailing: SwipeAction(icon: "trash", title: "Delete", tint: Accent.red, removesRow: true, haptic: .warning) { delete(alias) }) {
                            HStack(spacing: Space.l) {
                                TypeStyle.mono.sized(14).text(alias.text.uppercased()).foregroundStyle(Palette.ink)
                                Spacer()
                                TypeStyle.rowSub.text("since \(Dates.dayLabel(alias.createdAt))").foregroundStyle(Palette.ink2)
                            }
                            .frame(height: 46)
                            .hairline()
                            .padding(.horizontal, Space.gutter)
                        }
                        .shopListRow()
                    }
                } header: {
                    HStack(spacing: Space.m) {
                        ShopMark(name: group.name, size: 18)
                        TypeStyle.label.text(group.name).foregroundStyle(Palette.ink2)
                    }
                    .textCase(nil)
                    .padding(.top, Space.xs)
                }
            }

            Section {
                TypeStyle.rowSub.text(aliases.isEmpty && loaded
                     ? "No receipt names yet. Link a receipt line to this product and its text is remembered here."
                     : "Swipe to delete a name linked by mistake. Receipts you already confirmed keep their link.")
                    .foregroundStyle(Palette.ink2)
                    .listRowBackground(Palette.bg)
                    .listRowSeparator(.hidden)
                    .listRowInsets(EdgeInsets(top: Space.lx, leading: Space.gutter, bottom: 0, trailing: Space.gutter))
            }
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .background(Palette.bg)
        .safeAreaInset(edge: .top, spacing: 0) {
            NavHeader().background(Palette.bg)
        }
        .toolbar(.hidden, for: .navigationBar)
        .task {
            aliases = (try? await app.api.aliases(productId: productId)) ?? []
            loaded = true
        }
    }

    private func delete(_ alias: Alias) {
        withAnimation(Motion.collapse) { aliases.removeAll { $0.id == alias.id } }
        Haptics.play(.warning)
        Task {
            do { try await app.api.deleteAlias(alias.id) } catch {
                app.toasts.error("Couldn't delete \(alias.text.uppercased())")
                aliases = (try? await app.api.aliases(productId: productId)) ?? aliases
            }
        }
    }
}
