import SwiftUI

// MARK: - A.1 Shop spend

struct ShopSpendView: View {
    let shopId: Int64
    let range: DateRange
    @Environment(AppModel.self) private var app
    @State private var monthly: Spend?
    @State private var allTotal: Int64 = 0
    @State private var receipts: [Receipt] = []
    @State private var loaded = false

    private var shop: Shop? { app.catalog.shop(shopId) }
    private var brand: Color { ShopBrand.chartColor(for: shop?.name ?? "") }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                NavHeader(title: shop?.name ?? "Shop") { rangeLabel(range) }
                card
                    .padding(.horizontal, Space.edge)
                    .padding(.top, Space.xl)
                list
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.ml)
            }
            .padding(.bottom, 40)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await load() }
        .refreshable { await load() }
    }

    private var card: some View {
        let total = monthly?.totalCents ?? 0
        let visits = monthly?.receipts ?? 0
        return VStack(alignment: .leading, spacing: Space.lx) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 2) {
                    TypeStyle.label.text(shop?.location ?? shop?.displayName ?? "").opacity(0.8)
                    HeroAmount(cents: total, size: 46, color: Accent.white, minorColor: Accent.white.opacity(0.7))
                        .padding(.top, 6)
                }
                Spacer()
                Text(ShopBrand.mark(for: shop?.name ?? "").monogram)
                    .font(.custom(ShopFont.bold, size: 14))
                    .frame(width: 40, height: 40)
                    .background(.white.opacity(0.22), in: RoundedRectangle.continuous(Radius.glyph))
            }
            if let monthly {
                MiniBars(values: monthly.buckets.map { (label: AnalyticsModel.barLabel($0, grouping: monthly.groupBy), cents: $0.cents) }, height: 40)
            }
            HStack {
                stat("Visits", "\(visits)")
                stat("Avg. basket", visits > 0 ? Money.format(total / Int64(visits)) : "–")
                stat("Share", allTotal > 0 ? "\(Int((Double(total) / Double(allTotal) * 100).rounded()))%" : "–")
            }
            .padding(.top, Space.l)
            .overlay(alignment: .top) { Color.white.opacity(0.22).frame(height: 1) }
        }
        .foregroundStyle(Accent.white)
        .padding(18)
        .background(brand, in: RoundedRectangle.continuous(Radius.feature))
    }

    private func stat(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            TypeStyle.caption.sized(11.5).text(label).opacity(0.75)
            TypeStyle.input.sized(17).text(value)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private var list: some View {
        let groups = Dictionary(grouping: receipts) { Dates.startOfMonth($0.displayDate) }
        VStack(alignment: .leading, spacing: 0) {
            ForEach(groups.keys.sorted(by: >), id: \.self) { month in
                let items = (groups[month] ?? []).sorted { $0.displayDate > $1.displayDate }
                DaySectionHeader(title: Dates.month(month), trailing: Money.format(items.reduce(0) { $0 + ($1.totalCents ?? 0) }))
                    .padding(.top, Space.xs)
                ForEach(items) { r in
                    Button { app.push(.receipt(r.id)) } label: {
                        HStack(spacing: Space.l) {
                            HStack(alignment: .firstTextBaseline, spacing: Space.m) {
                                TypeStyle.rowTitle.text(Dates.shortDay(r.displayDate).components(separatedBy: " ").prefix(2).joined(separator: " ")).foregroundStyle(Palette.ink)
                                TypeStyle.rowSub.text(sub(r)).foregroundStyle(r.status == .needsReview ? Palette.amberText : Palette.ink2)
                            }
                            Spacer()
                            TypeStyle.rowTitle.text(r.totalCents.map(Money.format) ?? "—").foregroundStyle(Palette.ink)
                        }
                        .frame(height: 46)
                        .hairline()
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: .selection))
                }
            }
            if loaded && receipts.isEmpty {
                TypeStyle.body.text("No receipts from this shop in the range.").foregroundStyle(Palette.ink2).padding(.vertical, Space.xxl)
            }
        }
    }

    private func sub(_ r: Receipt) -> String {
        let lines = r.lineCount.map { "\($0) lines" }
        return [r.status == .needsReview ? "Needs review" : nil, lines].compactMap { $0 }.joined(separator: " · ")
    }

    private func load() async {
        async let m = app.api.spend(from: range.from, to: range.to, groupBy: .month, shopId: shopId)
        async let all = app.api.spend(from: range.from, to: range.to, groupBy: .month)
        async let page = app.api.receipts(from: range.from, to: range.to, shopId: shopId, limit: 200)
        monthly = try? await m
        allTotal = (try? await all)?.totalCents ?? 0
        receipts = (try? await page)?.items ?? receipts
        loaded = true
    }
}

/// Small static range label in pushed analytics headers ("May – Oct").
private func rangeLabel(_ range: DateRange) -> some View {
    TypeStyle.label.sized(12.5).text(range.short)
        .foregroundStyle(Palette.ink)
        .padding(.horizontal, Space.ml)
        .frame(height: 30)
        .background(Palette.sunk, in: Capsule())
        .fixedSize()
}

// MARK: - A.2 Category spend

struct CategorySpendView: View {
    let name: String
    let categoryId: Int64?
    let range: DateRange
    @Environment(AppModel.self) private var app
    @State private var total: Int64 = 0
    @State private var allTotal: Int64 = 0
    @State private var shopSplit: [(shop: Shop, cents: Int64)] = []
    @State private var rows: [Row] = []
    @State private var loaded = false

    struct Row: Identifiable {
        let productId: Int64
        let name: String
        let qty: String
        let spent: Int64
        let best: String?
        var id: Int64 { productId }
    }

    /// "24", "3,2 kg", "1,5 l"
    static func qtyText(_ bucket: SpendBucket) -> String {
        let q = bucket.qty ?? Double(bucket.receipts)
        switch bucket.unit {
        case "kg", "l": return "\(Quantity.decimal(q, maxFraction: 1)) \(bucket.unit!)"
        default: return Quantity.decimal(q, maxFraction: 0)
        }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                NavHeader(title: name) { rangeLabel(range) }

                VStack(alignment: .leading, spacing: Space.xs) {
                    HeroAmount(cents: total, size: 50)
                    TypeStyle.body.sized(13.5).text(caption).foregroundStyle(Palette.ink2)
                }
                .padding(.horizontal, Space.gutter)
                .padding(.top, 18)

                if !shopSplit.isEmpty {
                    splitBar
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, 18)
                }

                table
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.xxl)
            }
            .padding(.bottom, 40)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await load() }
    }

    private var caption: String {
        guard allTotal > 0 else { return " " }
        let pct = Int((Double(total) / Double(allTotal) * 100).rounded())
        return "\(pct)% of all spending · \(Money.whole(total / Int64(max(1, range.monthCount)))) a month"
    }

    private var splitBar: some View {
        let sum = max(1, shopSplit.reduce(Int64(0)) { $0 + $1.cents })
        return GeometryReader { geo in
            HStack(spacing: 2) {
                ForEach(shopSplit.indices, id: \.self) { i in
                    let part = shopSplit[i]
                    let fraction = Double(part.cents) / Double(sum)
                    let mark = ShopBrand.mark(for: part.shop.name)
                    TypeStyle.captionMedium.text(fraction > 0.18 ? "\(part.shop.name.count > 6 ? mark.monogram : part.shop.name) \(Int((fraction * 100).rounded()))%" : "")
                        .foregroundStyle(Accent.white)
                        .lineLimit(1)
                        .padding(.leading, 8)
                        .frame(width: max(4, (geo.size.width - CGFloat(shopSplit.count - 1) * 2) * fraction), alignment: .leading)
                        .frame(maxHeight: .infinity)
                        .background(ShopBrand.chartColor(for: part.shop.name))
                }
            }
        }
        .frame(height: 28)
        .clipShape(RoundedRectangle.continuous(Radius.tile))
        .accessibilityLabel(shopSplit.map { "\($0.shop.name) \(Money.format($0.cents))" }.joined(separator: ", "))
    }

    @ViewBuilder
    private var table: some View {
        VStack(spacing: 0) {
            HStack(spacing: Space.m) {
                Text("Product").frame(maxWidth: .infinity, alignment: .leading)
                Text("Qty").frame(width: 56, alignment: .trailing)
                Text("Spent").frame(width: 72, alignment: .trailing)
            }
            .textStyle(.captionMedium)
            .foregroundStyle(Palette.ink2)
            .padding(.bottom, 6)
            .hairline()

            ForEach(rows) { row in
                Button { app.push(.product(row.productId)) } label: {
                    HStack(spacing: Space.m) {
                        VStack(alignment: .leading, spacing: 1) {
                            TypeStyle.rowTitle.sized(14.5).text(row.name).foregroundStyle(Palette.ink).lineLimit(1)
                            if let best = row.best { TypeStyle.caption.text(best).foregroundStyle(Palette.greenText).lineLimit(1) }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        TypeStyle.body.text(row.qty).foregroundStyle(Palette.ink2).lineLimit(1).frame(width: 56, alignment: .trailing)
                        TypeStyle.rowTitle.sized(14.5).text(Money.format(row.spent)).foregroundStyle(Palette.ink).frame(width: 72, alignment: .trailing)
                    }
                    .frame(height: 48)
                    .hairline()
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: .selection))
            }
            if loaded && rows.isEmpty {
                TypeStyle.body.text("No linked products in this category for the range.").foregroundStyle(Palette.ink2).padding(.vertical, Space.xxl)
            } else if !loaded {
                SkeletonRows(count: 5) { SkeletonProductRow() }
            }
        }
    }

    private func load() async {
        let key = categoryId.map { String($0) } ?? "none"
        if let all = try? await app.api.spend(from: range.from, to: range.to, groupBy: .category) {
            allTotal = all.totalCents
            total = all.buckets.first { $0.key == key }?.cents ?? 0
        }
        // Per-shop split: one category breakdown per shop.
        var split: [(shop: Shop, cents: Int64)] = []
        for shop in app.catalog.shops {
            if let s = try? await app.api.spend(from: range.from, to: range.to, groupBy: .category, shopId: shop.id),
               let cents = s.buckets.first(where: { $0.key == key })?.cents, cents > 0 {
                split.append((shop, cents))
            }
        }
        shopSplit = split.sorted { $0.cents > $1.cents }

        // Products table: spend per product in this category.
        guard let categoryId else { loaded = true; return }
        let byProduct = try? await app.api.spend(from: range.from, to: range.to, groupBy: .product, categoryId: categoryId)
        var result: [Row] = []
        for bucket in byProduct?.buckets ?? [] {
            guard let id = Int64(bucket.key) else { continue }
            let product = app.catalog.product(id)
            let best = product?.cheapest.map { "\(Money.perUnit($0.latestUnitCents, unit: $0.baseUnit)) at \($0.shopName)" }
            result.append(Row(productId: id, name: product?.name ?? bucket.label, qty: Self.qtyText(bucket), spent: bucket.cents, best: best))
        }
        rows = result.sorted { $0.spent > $1.spent }
        loaded = true
    }
}

// MARK: - A.3 Price changes

struct PriceChangesView: View {
    @Environment(AppModel.self) private var app
    @State private var changes: [PriceChange] = []
    @State private var sinceDays = 90
    @State private var minPct: Double = 5
    @State private var shopId: Int64?
    @State private var loaded = false

    private var filtered: [PriceChange] {
        changes.filter { shopId == nil || $0.shopId == shopId }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                NavHeader()
                VStack(alignment: .leading, spacing: 6) {
                    TypeStyle.screenTitle.text("Price changes").foregroundStyle(Palette.ink)
                    TypeStyle.body.text("Your last three buys against the three before, per kg, litre or piece.").foregroundStyle(Palette.ink2)
                }
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.lx)

                HStack(spacing: 6) {
                    Chip(title: "Since \(Dates.dayLabel(Dates.adding(.day, -sinceDays, to: .now)))", isSelected: true) {
                        sinceDays = sinceDays == 90 ? 180 : (sinceDays == 180 ? 30 : 90)
                        Task { await load() }
                    }
                    Chip(title: "At least \(Int(minPct))%", isSelected: false) {
                        minPct = minPct == 5 ? 10 : (minPct == 10 ? 20 : 5)
                        Task { await load() }
                    }
                    Chip(title: shopId.map { app.catalog.shopName($0) } ?? "All shops", isSelected: false) {
                        let ids: [Int64?] = [nil] + app.catalog.shops.map { Optional($0.id) }
                        let i = ids.firstIndex(of: shopId) ?? 0
                        shopId = ids[(i + 1) % ids.count]
                    }
                }
                .padding(.horizontal, Space.edge)
                .padding(.top, Space.lx)

                VStack(alignment: .leading, spacing: 0) {
                    group("More expensive", filtered.filter { $0.pct > 0 }.sorted { $0.pct > $1.pct })
                    group("Cheaper", filtered.filter { $0.pct < 0 }.sorted { $0.pct < $1.pct })
                    if loaded && filtered.isEmpty {
                        EmptyState(title: "No big changes", message: "Nothing moved by \(Int(minPct))% or more in this period.") {
                            GlyphIllustration(icon: .analytics, color: Accent.green)
                        }
                        .padding(.top, 40)
                    } else if !loaded {
                        SkeletonRows(count: 5) { SkeletonProductRow() }
                    }
                }
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.ml)
            }
            .padding(.bottom, 40)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await load() }
    }

    @ViewBuilder
    private func group(_ title: String, _ items: [PriceChange]) -> some View {
        if !items.isEmpty {
            DaySectionHeader(title: title, trailing: "\(items.count)")
                .padding(.top, Space.xs)
            ForEach(items) { c in
                Button { app.push(.product(c.productId)) } label: {
                    HStack(spacing: Space.l) {
                        ShopMark(name: c.shopName, size: Size.shopMarkHeader)
                        VStack(alignment: .leading, spacing: 1) {
                            TypeStyle.rowTitle.text(c.productName).foregroundStyle(Palette.ink).lineLimit(1)
                            (Text("\(Money.perUnit(c.oldUnitCents, unit: c.baseUnit)) → ").foregroundStyle(Palette.ink2)
                                + Text(Money.perUnit(c.newUnitCents, unit: c.baseUnit)).foregroundStyle(Palette.ink))
                                .textStyle(.rowSub)
                        }
                        Spacer()
                        TypeStyle.label.text("\(c.pct > 0 ? "+" : Money.minus)\(Int(abs(c.pct).rounded()))%")
                            .foregroundStyle(c.pct > 0 ? Palette.redText : Palette.greenText)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(c.pct > 0 ? Palette.redTint : Palette.greenTint, in: RoundedRectangle.continuous(Radius.s))
                    }
                    .frame(height: Size.rowProduct)
                    .hairline()
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: .selection))
            }
        }
    }

    private func load() async {
        changes = (try? await app.api.priceChanges(since: Dates.adding(.day, -sinceDays, to: .now), minPct: minPct)) ?? changes
        loaded = true
    }
}
