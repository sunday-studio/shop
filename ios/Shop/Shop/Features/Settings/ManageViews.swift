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
                        TypeStyle.label.text("Add shop")
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
                TypeStyle.screenTitle.text("Shops").foregroundStyle(Palette.ink)
                TypeStyle.body.text("You pick one of these for every receipt.").foregroundStyle(Palette.ink2)
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
                SwipeActionRow(trailing: SwipeAction(icon: "trash", title: "Delete", tint: Accent.red) { askDeleteShop(shop) }) {
                Button { openForm(shop) } label: {
                    HStack(spacing: Space.l) {
                        ShopMark(name: shop.name, size: Size.shopMarkHeader)
                        VStack(alignment: .leading, spacing: 1) {
                            TypeStyle.rowTitle.text(shop.name).foregroundStyle(Palette.ink)
                            TypeStyle.rowSub.text([shop.branch, shop.city].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")).foregroundStyle(Palette.ink2)
                        }
                        Spacer()
                        if let n = counts[shop.id] {
                            TypeStyle.body.text("\(n)").foregroundStyle(Palette.ink2)
                        }
                        IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                    }
                    .frame(height: Size.rowReceipt)
                    .hairline()
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: .selection))
                .padding(.horizontal, Space.gutter)
                }
                .shopListRow()
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
    @State private var chain = ""
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
                TypeStyle.rowSub.text(name.isEmpty ? "The logo appears once the server knows the website." : "Logo from the shop's website, or its initials.")
                    .foregroundStyle(Palette.ink2)
            }
            .padding(.horizontal, Space.xs)

            InlineFieldGroup {
                InlineField(label: "Name", text: $name, placeholder: "Albert Heijn")
                InlineField(label: "Chain", text: $chain, placeholder: "Same as the name")
                InlineField(label: "Branch", text: $branch, placeholder: "Oudegracht")
                InlineField(label: "Address", text: $address, placeholder: "Optional")
                InlineField(label: "City", text: $city, placeholder: "Utrecht")
                InlineField(label: "Website", text: $website, placeholder: "ah.nl", divider: false, mono: true)
            }

            TypeStyle.rowSub.text("Chain groups branches of one shop. The website gives it a logo; the server guesses it for big chains.")
                .foregroundStyle(Palette.ink2)
                .padding(.horizontal, Space.xs)

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
                    LoadingLabel(title: "Save shop", isLoading: saving)
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
                chain = shop.chain ?? ""
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
        let input = ShopInput(name: name.trimmingCharacters(in: .whitespaces), chain: clean(chain) ?? shop?.chain ?? clean(name), branch: clean(branch),
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
                        TypeStyle.label.text("Add category")
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
                TypeStyle.screenTitle.text("Categories").foregroundStyle(Palette.ink)
                TypeStyle.body.text("Listed A to Z. Charts rank them by spend.").foregroundStyle(Palette.ink2)
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
                .padding(.vertical, 7)
                .padding(.horizontal, 10)
                .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.tile))
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.xl)
                .padding(.bottom, Space.xs)
                .shopListRow()

                ForEach(categories) { category in
                    SwipeActionRow(trailing: SwipeAction(icon: "trash", title: "Delete", tint: Accent.red) { askDelete(category) }) {
                    Button { openForm(category) } label: {
                        HStack(spacing: Space.l) {
                            RoundedRectangle.continuous(Radius.dot).fill(category.dotColor).frame(width: 14, height: 14)
                            TypeStyle.rowTitle.text(category.name).foregroundStyle(Palette.ink)
                            Spacer()
                            TypeStyle.body.text("\(category.productCount ?? 0)").foregroundStyle(Palette.ink2)
                        }
                        .frame(height: 48)
                        .hairline()
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: .selection))
                    .padding(.horizontal, Space.gutter)
                    }
                    .shopListRow()
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
        MoveProductsLayout(
            categoryName: category.name, count: count,
            destinations: others.map { .init(id: $0.id, name: $0.name, colour: $0.dotColor) },
            destination: $destination, working: working,
            onCancel: { trays.dismiss() },
            onDelete: { Task { await delete() } }
        )
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
        CategoryFormLayout(
            title: category == nil ? "Add category" : "Rename category",
            name: $name, colour: $color,
            errorText: isTaken ? "You already have a category called \(trimmed)." : nil,
            canSave: canSave, saving: saving,
            onSubmit: { Task { await save() } }
        )
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
            let input = CategoryInput(name: trimmed, color: color ?? (category == nil ? "blue" : nil))
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
