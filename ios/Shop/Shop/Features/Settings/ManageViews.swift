import SwiftUI

// MARK: - S.3 Shops

struct ShopsView: View {
    @Environment(AppModel.self) private var app
    @State private var counts: [Int64: Int] = [:]

    var body: some View {
        List {
            NavHeader {
                Button { openForm(nil) } label: {
                    HStack(spacing: 5) {
                        IconView(.plus, size: 13, weight: .bold)
                        Text("Add shop").textStyle(.label)
                    }
                    .foregroundStyle(Palette.btnInk)
                    .padding(.horizontal, Space.l)
                    .frame(height: Size.iconButton)
                    .background(Palette.btn, in: Capsule())
                }
                .buttonStyle(.pill)
            }
            .shopListRow()

            VStack(alignment: .leading, spacing: 6) {
                Text("Shops").textStyle(.screenTitle).foregroundStyle(Palette.ink)
                Text("You pick one of these for every receipt.").textStyle(.body).foregroundStyle(Palette.ink2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, Space.gutter)
            .padding(.top, Space.lx)
            .shopListRow()

            HStack {
                Text(app.catalog.shops.count == 1 ? "1 shop" : "\(app.catalog.shops.count) shops")
                Spacer()
                Text("Receipts")
            }
            .textStyle(.label)
            .foregroundStyle(Palette.ink2)
            .padding(.horizontal, Space.gutter)
            .padding(.top, Space.xl)
            .padding(.bottom, Space.xs)
            .shopListRow()

            if app.catalog.shops.isEmpty {
                StatefulContent(phase: app.catalog.isSyncing ? .loading : .empty) {
                    SkeletonRows(count: 5) { SkeletonSimpleRow() }
                } empty: {
                    EmptyState(title: "No shops yet", message: "Add the shops you buy from, or let Shop find them from your receipts.",
                               action: (title: "Add shop", run: { openForm(nil) })) {
                        GlyphIllustration(icon: .products, color: Accent.blue)
                    }
                } content: {
                    EmptyView()
                }
                .padding(.horizontal, Space.gutter)
                .shopListRow()
            }

            ForEach(app.catalog.shops) { shop in
                Button { openForm(shop) } label: {
                    HStack(spacing: Space.l) {
                        ShopMark(name: shop.name, size: Size.shopMarkHeader)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(shop.name).textStyle(.rowTitle).foregroundStyle(Palette.ink)
                            Text([shop.branch, shop.city].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                                .textStyle(.rowSub).foregroundStyle(Palette.ink2)
                        }
                        Spacer()
                        if let n = counts[shop.id] {
                            Text("\(n)").textStyle(.body).foregroundStyle(Palette.ink2)
                        }
                        IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                    }
                    .frame(height: Size.rowReceipt)
                    .hairline()
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: nil))
                .padding(.horizontal, Space.gutter)
                .shopListRow()
                .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                    SwipeButton(title: "Delete", systemImage: "trash", tint: Accent.red) { askDeleteShop(shop) }
                    SwipeButton(title: "Edit", systemImage: "pencil", tint: Accent.blue) { openForm(shop) }
                }
            }
        }
        .shopListStyle()
        .contentMargins(.bottom, 40, for: .scrollContent)
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task {
            try? await app.catalog.refreshReference()
            if let spend = try? await app.api.spend(from: Dates.adding(.year, -5, to: .now), to: .now, groupBy: .shop) {
                counts = Dictionary(spend.buckets.compactMap { b in Int64(b.key).map { ($0, b.receipts) } }, uniquingKeysWith: { a, _ in a })
            }
        }
    }

    private func askDeleteShop(_ shop: Shop) {
        app.trays.present("delete-shop") {
            ConfirmTray(title: "Delete \(shop.displayName)?", message: "Its receipts keep it in their history. It won't be offered for new scans.",
                        confirmTitle: "Delete shop", destructive: true) {
                do {
                    try await app.api.deleteShop(shop.id)
                    app.catalog.removeShop(shop.id)
                    Haptics.play(.error)
                    return true
                } catch let e as APIError {
                    app.toasts.error(e.message)
                    return false
                } catch { return false }
            }
        }
    }

    private func openForm(_ shop: Shop?) {
        app.trays.present("shop-form-\(shop?.id ?? 0)") { ShopFormTray(shop: shop) { _ in } }
    }
}

/// Add or edit a shop (S.3, and "Other shop" from C.2).
struct ShopFormTray: View {
    let shop: Shop?
    let onSaved: (Shop) -> Void

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var branch = ""
    @State private var address = ""
    @State private var city = ""
    @State private var website = ""
    @State private var loaded = false
    @State private var saving = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: shop == nil ? "New shop" : "Edit shop")

            HStack(spacing: Space.l) {
                ShopMark(name: name.isEmpty ? "?" : name, size: 32)
                Text(name.isEmpty ? "The logo appears once the server knows the website." : "Logo from the shop's website, or its initials.")
                    .textStyle(.rowSub)
                    .foregroundStyle(Palette.ink2)
            }
            .padding(.horizontal, Space.xs)

            Grid(horizontalSpacing: Space.m, verticalSpacing: Space.m) {
                GridRow {
                    FieldTile(label: "Name", text: $name, placeholder: "Albert Heijn", background: Palette.trayField)
                    FieldTile(label: "Branch", text: $branch, placeholder: "Oudegracht", background: Palette.trayField)
                }
                GridRow {
                    FieldTile(label: "Address", text: $address, placeholder: "Optional", background: Palette.trayField)
                    FieldTile(label: "City", text: $city, placeholder: "Utrecht", background: Palette.trayField)
                }
                GridRow {
                    FieldTile(label: "Website", text: $website, placeholder: "ah.nl · guessed for big chains", mono: true, background: Palette.trayField)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .gridCellColumns(2)
                }
            }

            HStack(spacing: Space.m) {
                if let shop {
                    Button("Delete") {
                        trays.present("delete-shop") {
                            ConfirmTray(title: "Delete \(shop.displayName)?", message: "Its receipts keep it in their history. It won't be offered for new scans.",
                                        confirmTitle: "Delete shop", destructive: true) {
                                do {
                                    try await app.api.deleteShop(shop.id)
                                    app.catalog.removeShop(shop.id)
                                    return true
                                } catch let e as APIError {
                                    app.toasts.error(e.message)
                                    return false
                                } catch { return false }
                            }
                        }
                    }
                    .buttonStyle(.shop(.destructive))
                }
                Button {
                    Task { await save() }
                } label: {
                    LoadingLabel(title: "Save", isLoading: saving)
                }
                .buttonStyle(.shop(haptic: .medium))
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || saving)
            }
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            if let shop {
                name = shop.name
                branch = shop.branch ?? ""
                address = shop.address ?? ""
                city = shop.city ?? ""
                website = shop.domain ?? ""
            }
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        func clean(_ s: String) -> String? {
            let t = s.trimmingCharacters(in: .whitespaces)
            return t.isEmpty ? nil : t
        }
        let site = website.trimmingCharacters(in: .whitespaces)
        // Omit the domain to keep or let the server guess; "" clears a domain that was set.
        let domain: String? = site.isEmpty ? (shop?.domain == nil ? nil : "") : site
        let input = ShopInput(name: name.trimmingCharacters(in: .whitespaces), chain: shop?.chain ?? clean(name), branch: clean(branch),
                              address: clean(address), city: clean(city), notes: shop?.notes, domain: domain)
        do {
            let saved: Shop
            if let shop {
                saved = try await app.api.updateShop(shop.id, input)
            } else {
                saved = try await app.api.createShop(input)
            }
            app.catalog.upsert(saved)
            Haptics.play(.success)
            trays.dismiss()
            onSaved(saved)
        } catch let e as APIError {
            app.toasts.error(e.message)
        } catch {}
    }
}

// MARK: - S.4 Categories

struct CategoriesView: View {
    @Environment(AppModel.self) private var app
    @State private var order: [Category] = []

    var body: some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Categories").textStyle(.screenTitle).foregroundStyle(Palette.ink)
                    Text("Drag to change the order in Analytics and Products.").textStyle(.body).foregroundStyle(Palette.ink2)
                }
                .padding(.top, Space.lx)
                .listRowInsets(EdgeInsets(top: 0, leading: Space.gutter, bottom: Space.m, trailing: Space.gutter))
                .listRowBackground(Palette.bg)
                .listRowSeparator(.hidden)
                .moveDisabled(true)
            }
            Section {
                ForEach(order) { c in
                    Button { openForm(c) } label: {
                        HStack(spacing: Space.l) {
                            CategoryDot(color: CategoryColor.swatch(for: c.name).color, size: 12)
                            Text(c.name).textStyle(.rowTitle).foregroundStyle(Palette.ink)
                            Spacer()
                            Text("\(productCount(c))").textStyle(.body).foregroundStyle(Palette.ink2)
                        }
                        .frame(height: 46)
                    }
                    .buttonStyle(.plain)
                    .listRowInsets(EdgeInsets(top: 0, leading: Space.gutter, bottom: 0, trailing: Space.edge))
                    .listRowBackground(Palette.bg)
                    .listRowSeparatorTint(Palette.line)
                }
                .onMove(perform: move)
            } header: {
                HStack {
                    Text("\(order.count) categories")
                    Spacer()
                    Text("Products")
                }
                .textStyle(.label)
                .foregroundStyle(Palette.ink2)
                .textCase(nil)
            }
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .environment(\.editMode, .constant(.active))
        .background(Palette.bg)
        .safeAreaInset(edge: .top, spacing: 0) {
            NavHeader {
                Button { openForm(nil) } label: {
                    HStack(spacing: 5) {
                        IconView(.plus, size: 13, weight: .bold)
                        Text("Add").textStyle(.label)
                    }
                    .foregroundStyle(Palette.btnInk)
                    .padding(.horizontal, Space.l)
                    .frame(height: Size.iconButton)
                    .background(Palette.btn, in: Capsule())
                }
                .buttonStyle(.pill)
            }
            .padding(.bottom, Space.xs)
            .background(Palette.bg)
        }
        .toolbar(.hidden, for: .navigationBar)
        .onAppear { order = app.catalog.categories }
        .onChange(of: app.catalog.categories) { _, new in order = new }
    }

    private func productCount(_ c: Category) -> Int {
        app.catalog.products.values.filter { $0.categoryId == c.id }.count
    }

    private func move(from source: IndexSet, to destination: Int) {
        Haptics.play(.selection)
        order.move(fromOffsets: source, toOffset: destination)
        let changed = order.enumerated().compactMap { i, c -> Category? in c.sort == i ? nil : Category(id: c.id, name: c.name, sort: i) }
        order = order.enumerated().map { i, c in Category(id: c.id, name: c.name, sort: i) }
        app.catalog.setCategories(order)
        Task {
            for c in changed {
                _ = try? await app.api.updateCategory(c.id, CategoryInput(name: c.name, sort: c.sort))
            }
        }
    }

    private func openForm(_ category: Category?) {
        app.trays.present("category-\(category?.id ?? 0)") { CategoryFormTray(category: category, nextSort: order.count) }
    }
}

struct CategoryFormTray: View {
    let category: Category?
    let nextSort: Int

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var loaded = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: category == nil ? "New category" : "Rename category")
            FieldTile(label: "Name", text: $name, placeholder: "Snacks", background: Palette.trayField) { Task { await save() } }
                .submitLabel(.done)
            HStack(spacing: Space.m) {
                if let category {
                    Button("Delete") {
                        Task {
                            do {
                                try await app.api.deleteCategory(category.id)
                                app.catalog.removeCategory(category.id)
                                Haptics.play(.error)
                                trays.dismiss()
                            } catch let e as APIError { app.toasts.error(e.message) } catch {}
                        }
                    }
                    .buttonStyle(.shop(.destructive))
                }
                Button("Save") { Task { await save() } }
                    .buttonStyle(.shop(haptic: .medium))
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            name = category?.name ?? ""
        }
    }

    private func save() async {
        let n = name.trimmingCharacters(in: .whitespaces)
        guard !n.isEmpty else { return }
        do {
            let saved: Category
            if let category {
                saved = try await app.api.updateCategory(category.id, CategoryInput(name: n, sort: category.sort))
            } else {
                saved = try await app.api.createCategory(CategoryInput(name: n, sort: nextSort))
            }
            app.catalog.upsert(saved)
            Haptics.play(.success)
            trays.dismiss()
        } catch let e as APIError {
            app.toasts.error(e.message)
        } catch {}
    }
}
