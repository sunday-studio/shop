import SwiftUI

/// X.1 · Search: products, receipt names (aliases) and receipts in one box (`GET /v1/search`).
/// Offline it falls back to the cached catalog.
struct SearchView: View {
    enum Section: Hashable { case all, products, names, receipts }

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @State private var results: SearchResults?
    @State private var section: Section = .all
    @State private var searching = false
    @State private var task: Task<Void, Never>?

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: Space.l) {
                SearchField(placeholder: "Products, shops, receipts", text: $query, autofocus: true)
                    .onChange(of: query) { _, q in search(q) }
                Button("Cancel") { dismiss() }
                    .textStyle(TypeStyle.rowTitle)
                    .foregroundStyle(Palette.blueText)
                    .buttonStyle(.press(haptic: nil))
            }
            .padding(.horizontal, Space.edge)
            .padding(.top, Space.l)

            if let results {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        Chip(title: "All", isSelected: section == .all) { section = .all }
                        Chip(title: "Products \(results.products.count)", isSelected: section == .products) { section = .products }
                        Chip(title: "Receipt names \(results.aliases.count)", isSelected: section == .names) { section = .names }
                        Chip(title: "Receipts \(results.receipts.count)", isSelected: section == .receipts) { section = .receipts }
                    }
                    .padding(.horizontal, Space.edge)
                }
                .padding(.top, Space.l)
            }

            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    if let results {
                        content(results)
                    } else if searching {
                        SearchLoadingRows()
                    } else {
                        EmptyState(title: "Search everything", message: "Products by Dutch or English name, the way shops print them, and receipts that contain them.") {
                            GlyphIllustration(icon: .search, color: Accent.green)
                        }
                        .padding(.top, 60)
                    }
                }
                .padding(.horizontal, Space.gutter)
                .padding(.bottom, 40)
            }
            .scrollDismissesKeyboard(.immediately)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
    }

    @ViewBuilder
    private func content(_ r: SearchResults) -> some View {
        let empty = r.products.isEmpty && r.aliases.isEmpty && r.receipts.isEmpty
        if empty {
            EmptyState(title: "Nothing found", message: "No products, receipt names or receipts match “\(query)”.") {
                GlyphIllustration(icon: .search, color: Palette.ink3)
            }
            .padding(.top, 50)
        }
        if (section == .all || section == .products) && !r.products.isEmpty {
            DaySectionHeader(title: "Products").padding(.top, Space.ml)
            ForEach(r.products) { p in
                Button { app.push(.product(p.id)) } label: {
                    ProductRow(name: p.name, meta: [p.sizeText, app.catalog.category(p.categoryId)?.name].compactMap { $0 }.joined(separator: " · "),
                               category: app.catalog.category(p.categoryId)?.name, imageURL: app.api.productImageURL(p), showsChevron: true)
                }
                .buttonStyle(.press(haptic: nil))
            }
        }
        if (section == .all || section == .names) && !r.aliases.isEmpty {
            DaySectionHeader(title: "Receipt names").padding(.top, Space.ml)
            ForEach(r.aliases) { a in
                Button { app.push(.receiptNames(a.productId)) } label: {
                    HStack(spacing: Space.l) {
                        ShopMark(name: a.shopName ?? "Any")
                        Text(a.text.uppercased()).textStyle(TypeStyle.mono.sized(14)).foregroundStyle(Palette.ink).lineLimit(1)
                        Spacer(minLength: Space.m)
                        Text(a.productName).textStyle(.rowSub).foregroundStyle(Palette.ink2).lineLimit(1)
                    }
                    .frame(height: 48)
                    .hairline()
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: nil))
            }
        }
        if (section == .all || section == .receipts) && !r.receipts.isEmpty {
            DaySectionHeader(title: "Receipts with “\(query.trimmingCharacters(in: .whitespaces))”").padding(.top, Space.ml)
            ForEach(r.receipts) { hit in
                Button { app.push(.receipt(hit.receipt.id)) } label: {
                    ReceiptRow(
                        shopName: app.catalog.shopName(hit.receipt.shopId),
                        subtitle: ([Dates.shortDay(hit.receipt.displayDate)] + hit.matchedLines.prefix(2)).joined(separator: " · "),
                        amount: hit.receipt.totalCents.map(Money.format) ?? "—"
                    )
                }
                .buttonStyle(.press(haptic: nil))
            }
        }
    }

    private func search(_ q: String) {
        task?.cancel()
        let text = q.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else {
            results = nil
            searching = false
            return
        }
        searching = true
        task = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            do {
                let r = try await app.api.search(text, limit: 10)
                guard !Task.isCancelled else { return }
                results = r
            } catch {
                guard !Task.isCancelled else { return }
                // Offline or older server: products from the local catalog.
                results = SearchResults(products: ProductSearch.local(text, in: app.catalog, limit: 10), aliases: [], receipts: [])
            }
            searching = false
        }
    }
}

#Preview("Search") {
    let app = AppModel(mock: true)
    return NavigationStack { SearchView() }.environment(app)
}

/// Four product-row skeletons, only after a short wait so quick searches don't flash.
private struct SearchLoadingRows: View {
    @State private var visible = false

    var body: some View {
        Group {
            if visible { SkeletonRows(count: 4) { SkeletonProductRow() }.transition(.opacity) } else { Color.clear.frame(height: 1) }
        }
        .task {
            try? await Task.sleep(for: Motion.skeletonDelay)
            guard !Task.isCancelled else { return }
            withAnimation(Motion.stateSwap) { visible = true }
        }
    }
}
