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
                    return true
                } catch let e as APIError {
                    app.toasts.error(e.message)
                    return false
                } catch { return false }
            } afterDismiss: {
                app.catalog.removeShop(shop.id)
                Haptics.play(.warning)
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
                                    return true
                                } catch let e as APIError {
                                    app.toasts.error(e.message)
                                    return false
                                } catch { return false }
                            } afterDismiss: {
                                app.catalog.removeShop(shop.id)
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

extension Category {
    /// The stored colour token as a colour; falls back to the name-based swatch for old caches.
    var dotColor: Color {
        CategoryColor.swatch(for: self).color
    }
}

struct CategoriesView: View {
    @Environment(AppModel.self) private var app
    @State private var loadError: APIError?
    @State private var loading = true

    private var categories: [Category] { app.catalog.categories }

    var body: some View {
        List {
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
            .shopListRow()

            VStack(alignment: .leading, spacing: 6) {
                Text("Categories").textStyle(.screenTitle).foregroundStyle(Palette.ink)
                Text("Listed A to Z. Charts rank them by spend.").textStyle(.body).foregroundStyle(Palette.ink2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, Space.gutter)
            .padding(.top, Space.lx)
            .shopListRow()

            if let error = loadError, !categories.isEmpty {
                StaleBanner(what: "categories", offline: error.isConnectivity) { Task { await load() } }
                    .padding(.horizontal, Space.edge)
                    .padding(.top, Space.l)
                    .shopListRow()
            }

            if categories.isEmpty {
                StatefulContent(phase: loadError.map { .failed($0.message) } ?? (loading ? .loading : .empty), retry: { Task { await load() } }) {
                    SkeletonRows { SkeletonSimpleRow() }
                } empty: {
                    EmptyState(title: "No categories", message: "Categories group your products and your spend.",
                               action: (title: "Add category", run: { openForm(nil) })) {
                        GlyphIllustration(icon: .grid, color: Accent.blue)
                    }
                } content: {
                    EmptyView()
                }
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.xl)
                .shopListRow()
            } else {
                HStack {
                    Text(categories.count == 1 ? "1 category" : "\(categories.count) categories")
                    Spacer()
                    Text("Products")
                }
                .textStyle(.label)
                .foregroundStyle(Palette.ink2)
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.xl)
                .padding(.bottom, Space.xs)
                .shopListRow()

                ForEach(categories) { category in
                    Button { openForm(category) } label: {
                        HStack(spacing: Space.l) {
                            CategoryDot(color: category.dotColor, size: 12)
                            Text(category.name).textStyle(.rowTitle).foregroundStyle(Palette.ink)
                            Spacer()
                            Text("\(category.productCount ?? 0)").textStyle(.body).foregroundStyle(Palette.ink2)
                            IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                        }
                        .frame(height: 46)
                        .hairline()
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: nil))
                    .padding(.horizontal, Space.gutter)
                    .shopListRow()
                    .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                        SwipeButton(title: "Delete", systemImage: "trash", tint: Accent.red) { askDelete(category) }
                        SwipeButton(title: "Rename", systemImage: "pencil", tint: Accent.blue) { openForm(category) }
                    }
                }
            }
        }
        .shopListStyle()
        .contentMargins(.bottom, 40, for: .scrollContent)
        .refreshable { await load() }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await load() }
    }

    private func load() async {
        do {
            try await app.catalog.refreshReference()
            loadError = nil
        } catch let error as APIError {
            loadError = error
        } catch {}
        loading = false
    }

    private func openForm(_ category: Category?) {
        app.trays.present("category-\(category?.id ?? 0)") { CategoryFormTray(category: category) }
    }

    private func askDelete(_ category: Category) {
        let count = category.productCount ?? 0
        if count == 0 {
            app.trays.present("delete-category") {
                ConfirmTray(title: "Delete \(category.name)?", message: "No products use it.", confirmTitle: "Delete category", destructive: true) {
                    do {
                        try await app.api.deleteCategory(category.id)
                        return true
                    } catch let error as APIError {
                        if case .conflict(let problem) = error, problem.code == "category_has_products" {
                            await load()
                            app.toasts.show("Products were added to it. Try again.")
                        } else {
                            app.toasts.error(error.message)
                        }
                        return false
                    } catch { return false }
                } afterDismiss: {
                    app.catalog.removeCategory(category.id)
                    Haptics.play(.warning)
                    app.toasts.show("\(category.name) deleted")
                }
            }
        } else {
            app.trays.present("delete-category") { MoveProductsTray(category: category) }
        }
    }
}

/// Delete a category that still has products: pick where they go, then delete in one step.
struct MoveProductsTray: View {
    let category: Category

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var count: Int
    /// 0 = no category.
    @State private var destination: Int64 = 0
    @State private var working = false

    init(category: Category) {
        self.category = category
        _count = State(initialValue: category.productCount ?? 0)
    }

    private var others: [Category] { app.catalog.categories.filter { $0.id != category.id } }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            TrayHeader(title: "Delete \(category.name)?", subtitle: "\(count) product\(count == 1 ? "" : "s") use it. Pick where they go.")
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(others) { other in row(id: other.id, name: other.name, color: other.dotColor) }
                    row(id: 0, name: "No category", color: Palette.neutral)
                }
                .padding(.horizontal, Space.xs)
            }
            .frame(maxHeight: 6 * 46)
            .scrollBounceBehavior(.basedOnSize)
            HStack(spacing: Space.m) {
                Button("Cancel") { trays.dismiss() }.buttonStyle(.shop(.secondary))
                Button { Task { await delete() } } label: {
                    LoadingLabel(title: "Delete and move \(count)", isLoading: working)
                }
                .buttonStyle(.shop(.destructive, haptic: .medium))
                .disabled(working)
            }
        }
    }

    private func row(id: Int64, name: String, color: Color) -> some View {
        Button {
            Haptics.play(.selection)
            destination = id
        } label: {
            HStack(spacing: Space.l) {
                CategoryDot(color: color, size: 12)
                Text(name).textStyle(.rowTitle).foregroundStyle(Palette.ink)
                Spacer()
                if destination == id {
                    Image(icon: .check).font(.system(size: 13, weight: .bold)).foregroundStyle(Accent.blue)
                }
            }
            .frame(height: 46)
            .hairline()
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: nil))
    }

    private func delete() async {
        working = true
        defer { working = false }
        do {
            try await app.api.deleteCategory(category.id, moveTo: destination)
            trays.dismiss()
            // Let the tray finish sliding away before the list, toast and refreshes change.
            try? await Task.sleep(for: .milliseconds(350))
            app.catalog.removeCategory(category.id)
            Haptics.play(.warning)
            let target = destination == 0 ? "no category" : app.catalog.category(destination)?.name ?? "another category"
            app.toasts.show("\(category.name) deleted · \(count) product\(count == 1 ? "" : "s") moved to \(target)")
            try? await app.catalog.refreshReference()
            try? await app.catalog.refreshProducts()
        } catch let error as APIError {
            if case .conflict(let problem) = error, let n = problem.productCount {
                count = n
                Haptics.play(.warning)
            } else {
                Haptics.play(.error)
                app.toasts.error(error.message)
            }
        } catch {}
    }
}

struct CategoryFormTray: View {
    let category: Category?
    /// Called with the saved category (used by the product form's "New…" chip).
    var onSaved: ((Category) -> Void)? = nil

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var color: String?
    @State private var saving = false
    @State private var takenName: String?
    @State private var loaded = false

    private var trimmed: String { name.split(separator: " ").joined(separator: " ") }
    private var isTaken: Bool { takenName?.caseInsensitiveCompare(trimmed) == .orderedSame }
    private var canSave: Bool { !trimmed.isEmpty && !saving && !isTaken && (category.map { $0.name != trimmed || $0.color != color } ?? true) }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: category == nil ? "New category" : "Rename category")
            VStack(alignment: .leading, spacing: Space.s) {
                FieldTile(label: "Name", text: $name, placeholder: "Snacks", error: isTaken, background: Palette.trayField) { Task { await save() } }
                    .submitLabel(.done)
                if isTaken {
                    Text("You already have a category called \(trimmed).")
                        .textStyle(.caption).foregroundStyle(Palette.redText)
                        .padding(.horizontal, Space.xs)
                }
            }
            HStack(spacing: Space.m) {
                ForEach(CategoryTone.tokens, id: \.self) { token in
                    Button {
                        Haptics.play(.selection)
                        color = token
                    } label: {
                        Circle()
                            .fill(Category(id: 0, name: "", color: token).dotColor)
                            .frame(width: 22, height: 22)
                            .overlay { if color == token { Circle().strokeBorder(Palette.ink, lineWidth: 2).padding(-4) } }
                            .frame(maxWidth: .infinity, minHeight: 32)
                    }
                    .buttonStyle(.press(haptic: nil))
                    .accessibilityLabel(token.capitalized)
                }
            }
            Button { Task { await save() } } label: {
                LoadingLabel(title: "Save", isLoading: saving)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(!canSave)
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            name = category?.name ?? ""
            color = category?.color
        }
    }

    private func save() async {
        guard canSave else { return }
        saving = true
        defer { saving = false }
        do {
            let input = CategoryInput(name: trimmed, color: color)
            let saved = category == nil
                ? try await app.api.createCategory(input)
                : try await app.api.updateCategory(category!.id, input)
            app.catalog.upsert(saved)
            Haptics.play(.success)
            if let onSaved { onSaved(saved) } else { trays.dismiss() }
        } catch let error as APIError {
            if case .conflict(let problem) = error, problem.code == "category_name_taken" {
                withAnimation(Motion.sheetSwap) { takenName = trimmed }
                Haptics.play(.warning)
            } else {
                Haptics.play(.error)
                app.toasts.error(error.message)
            }
        } catch {}
    }
}
