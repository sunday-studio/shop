import SwiftUI

/// L · Shopping list: works offline (ShoppingListStore), "cheapest at" hints, suggestions
/// from things you usually buy by now, check-off animation, clear checked.
struct ShoppingListView: View {
    @Environment(AppModel.self) private var app
    @State private var frequent: [FrequentItem] = []

    private var list: ShoppingListStore { app.list }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                LargeTitleHeader(title: "List", detail: summary)

                FieldButton(title: "Add an item") { openAdd() }
                    .padding(.horizontal, Space.edge)
                    .padding(.top, Space.lx)

                if !suggestions.isEmpty {
                    VStack(alignment: .leading, spacing: Space.m) {
                        Text("Usually bought by now").textStyle(.label).foregroundStyle(Palette.ink2)
                        FlowLayout(spacing: 6, lineSpacing: 6) {
                            ForEach(suggestions) { s in
                                SuggestionChip(title: s.productName, color: CategoryColor.swatch(for: app.catalog.categoryName(forProduct: s.productId)).color) {
                                    list.add(text: s.productName, productId: s.productId, qty: 1)
                                    Haptics.play(.light)
                                    Task { await list.sync() }
                                }
                            }
                        }
                        Button("See all suggestions") { openSuggestions() }
                            .textStyle(.label)
                            .foregroundStyle(Palette.blueText)
                            .buttonStyle(.press(haptic: nil))
                    }
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.lx)
                }

                items
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.ml)
            }
            .padding(.bottom, Space.tabBarClearance)
        }
        .scrollIndicators(.hidden)
        .refreshable {
            await list.sync()
            frequent = (try? await app.api.frequentItems()) ?? frequent
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task {
            await list.sync()
            frequent = (try? await app.api.frequentItems()) ?? frequent
        }
    }

    private var summary: String? {
        let n = list.open.count
        guard n > 0 else { return nil }
        if let cents = list.estimateCents { return "\(n) to buy · about \(Money.whole(cents))" }
        return "\(n) to buy"
    }

    /// Due frequent items not already on the list.
    private var suggestions: [FrequentItem] {
        let open = Set(list.open.compactMap(\.productId))
        return frequent.filter { $0.due && !open.contains($0.productId) }.prefix(4).map { $0 }
    }

    @ViewBuilder
    private var items: some View {
        VStack(spacing: 0) {
            if list.items.isEmpty {
                EmptyState(title: "Nothing on the list", message: "Add things as you run out. The list works without signal and syncs when you're back online.") {
                    GlyphIllustration(icon: .list, color: Accent.green)
                }
                .padding(.top, 40)
            }

            ForEach(Array(list.open.enumerated()), id: \.element.id) { index, item in
                row(item)
                    .staggeredAppear(index: index)
                    .transition(.opacity)
            }

            if !list.checked.isEmpty {
                HStack {
                    Text("\(list.checked.count) checked off").textStyle(.body).foregroundStyle(Palette.ink2)
                    Spacer()
                    Button("Clear") {
                        Haptics.play(.light)
                        withAnimation(Motion.collapse) { list.clearChecked() }
                        Task { await list.sync() }
                    }
                    .textStyle(TypeStyle.label.sized(14))
                    .foregroundStyle(Palette.blueText)
                    .buttonStyle(.press(haptic: nil))
                }
                .frame(height: 46)
                ForEach(list.checked) { item in
                    row(item)
                }
            }

            if list.pendingCount > 0 {
                HStack(spacing: Space.s) {
                    StatusDot(color: app.network.isOnline ? Accent.blue : Palette.ink3)
                    Text(app.network.isOnline ? "Syncing \(list.pendingCount) change\(list.pendingCount == 1 ? "" : "s")…" : "\(list.pendingCount) change\(list.pendingCount == 1 ? "" : "s") sync when you're online")
                        .textStyle(.rowSub)
                        .foregroundStyle(Palette.ink2)
                }
                .padding(.top, Space.l)
            }
        }
        .animation(Motion.collapse, value: list.items.map(\.id))
    }

    private func row(_ item: ListItem) -> some View {
        ListItemRow(
            title: item.title,
            hint: hint(item),
            hintShop: item.cheapest?.shopName,
            quantity: quantity(item),
            isChecked: item.isChecked,
            onToggle: { checked in toggle(item, checked) },
            onTap: { openEdit(item) }
        )
        .contextMenu {
            Button("Edit", systemImage: "pencil") { openEdit(item) }
            if let id = item.productId { Button("Open product", systemImage: "shippingbox") { app.push(.product(id)) } }
            Button("Delete", systemImage: "trash", role: .destructive) {
                withAnimation(Motion.collapse) { list.delete(item) }
                Task { await list.sync() }
            }
        }
    }

    private func hint(_ item: ListItem) -> String? {
        if let c = item.cheapest {
            return "Cheapest at \(c.shopName) · \(Money.perUnit(c.latestUnitCents, unit: c.baseUnit))"
        }
        if item.productId == nil { return item.note ?? "Not in your catalog" }
        return item.note
    }

    private func quantity(_ item: ListItem) -> String? {
        guard let qty = item.qty else { return nil }
        if let product = app.catalog.product(item.productId), product.netUnit == nil, qty < 10, qty != qty.rounded() {
            return "\(Quantity.decimal(qty)) kg"
        }
        return "× \(Quantity.decimal(qty))"
    }

    /// Circle fills, text strikes after 160ms, then the row collapses (240ms) into "checked off".
    private func toggle(_ item: ListItem, _ checked: Bool) {
        guard checked else {
            list.setChecked(item, false)
            Task { await list.sync() }
            return
        }
        Task {
            try? await Task.sleep(for: .milliseconds(420))
            withAnimation(Motion.collapse) { list.setChecked(item, true) }
            await list.sync()
        }
    }

    private func openAdd() {
        app.trays.present("list-add") { AddItemTray() }
    }

    private func openEdit(_ item: ListItem) {
        app.trays.present("list-edit-\(item.id)") { AddItemTray(editing: item) }
    }

    private func openSuggestions() {
        app.trays.present("list-suggestions") { SuggestionsTray(items: frequent) }
    }
}

// MARK: - L.1 Add item

struct AddItemTray: View {
    var editing: ListItem? = nil

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var text = ""
    @State private var qty: Double = 1
    @State private var note = ""
    @State private var results: [Product] = []
    @State private var loaded = false
    @State private var searchTask: Task<Void, Never>?

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            TrayHeader(title: editing == nil ? "Add to list" : "Edit item")

            SearchField(placeholder: "Product or anything", text: $text, background: Palette.trayField, autofocus: editing == nil) {
                if editing == nil, !text.trimmingCharacters(in: .whitespaces).isEmpty {
                    addText()
                }
            }
            .onChange(of: text) { _, q in search(q) }

            if editing == nil && !text.trimmingCharacters(in: .whitespaces).isEmpty {
                VStack(spacing: 0) {
                    ForEach(results.prefix(3)) { p in
                        Button { addProduct(p) } label: {
                            HStack(spacing: Space.l) {
                                ProductTile(name: p.name, category: app.catalog.category(p.categoryId)?.name, size: 34)
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(p.name).textStyle(.rowTitle).foregroundStyle(Palette.ink).lineLimit(1)
                                    Text([p.sizeText, app.catalog.category(p.categoryId)?.name].compactMap { $0 }.joined(separator: " · "))
                                        .textStyle(.rowSub).foregroundStyle(Palette.ink2).lineLimit(1)
                                }
                                Spacer()
                                IconView(.plus, size: 14, weight: .bold)
                                    .foregroundStyle(Palette.ink)
                                    .frame(width: 28, height: 28)
                                    .background(Palette.trayField, in: Circle())
                            }
                            .frame(height: 54)
                            .hairline()
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.press(haptic: .light))
                    }
                    Button { addText() } label: {
                        HStack(spacing: Space.l) {
                            RoundedRectangle.continuous(Radius.m)
                                .strokeBorder(Palette.ink3, style: StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
                                .frame(width: 34, height: 34)
                                .overlay(IconView(.plus, size: 14, weight: .semibold))
                            Text("Add “\(text.trimmingCharacters(in: .whitespaces))” as text").textStyle(.rowTitle).lineLimit(1)
                            Spacer()
                        }
                        .foregroundStyle(Palette.blueText)
                        .frame(height: 50)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: .light))
                }
                .padding(.horizontal, Space.xs)
            }

            HStack(spacing: Space.m) {
                HStack {
                    VStack(alignment: .leading, spacing: 0) {
                        Text("Quantity").textStyle(.caption).foregroundStyle(Palette.ink2)
                        Text(Quantity.decimal(qty)).textStyle(TypeStyle.input.sized(17)).foregroundStyle(Palette.ink)
                            .contentTransition(.numericText(value: qty))
                    }
                    Spacer()
                    HStack(spacing: 4) {
                        stepper("−", label: "Fewer") { qty = max(1, qty - 1) }
                        stepper("+", label: "More") { qty += 1 }
                    }
                }
                .padding(.vertical, Space.m)
                .padding(.leading, Space.lx)
                .padding(.trailing, Space.m)
                .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
                .animation(Motion.numberRoll, value: qty)

                FieldTile(label: "Note", text: $note, placeholder: "Optional", background: Palette.trayField)
            }

            if let editing {
                HStack(spacing: Space.m) {
                    Button("Delete") {
                        app.list.delete(editing)
                        trays.dismiss()
                        Task { await app.list.sync() }
                    }
                    .buttonStyle(.shop(.destructive))
                    Button("Save") {
                        let name = text.trimmingCharacters(in: .whitespaces)
                        app.list.update(editing, text: name.isEmpty ? editing.text : name, qty: qty, note: note.isEmpty ? nil : note)
                        trays.dismiss()
                        Task { await app.list.sync() }
                    }
                    .buttonStyle(.shop(haptic: .medium))
                }
            }
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            if let editing {
                text = editing.title
                qty = editing.qty ?? 1
                note = editing.note ?? ""
            }
        }
    }

    private func stepper(_ symbol: String, label: String, action: @escaping () -> Void) -> some View {
        Button {
            Haptics.play(.selection)
            action()
        } label: {
            Text(symbol)
                .textStyle(TypeStyle.input)
                .foregroundStyle(Palette.ink)
                .frame(width: 30, height: 30)
                .background(Palette.tray, in: Circle())
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
        .accessibilityLabel(label)
    }

    private func search(_ q: String) {
        searchTask?.cancel()
        let t = q.trimmingCharacters(in: .whitespaces)
        guard editing == nil, !t.isEmpty else { results = []; return }
        results = ProductSearch.local(t, in: app.catalog, limit: 3)
        searchTask = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled, app.network.isOnline else { return }
            if let remote = try? await app.api.products(query: t, limit: 3), !Task.isCancelled, !remote.isEmpty {
                results = remote
            }
        }
    }

    private func addProduct(_ p: Product) {
        app.list.add(text: p.name, productId: p.id, qty: qty, note: note.isEmpty ? nil : note)
        finish()
    }

    private func addText() {
        let t = text.trimmingCharacters(in: .whitespaces)
        guard !t.isEmpty else { return }
        app.list.add(text: t, qty: qty == 1 ? nil : qty, note: note.isEmpty ? nil : note)
        finish()
    }

    /// Stays open for the next item (clears the field), like a quick-entry sheet.
    private func finish() {
        Haptics.play(.light)
        text = ""
        note = ""
        qty = 1
        results = []
        Task { await app.list.sync() }
    }
}

// MARK: - L.2 Suggestions

struct SuggestionsTray: View {
    let items: [FrequentItem]
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            TrayHeader(title: "Suggestions", subtitle: "Bought often, not lately")
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(items.sorted { ($0.due ? 0 : 1, -$0.daysSince) < ($1.due ? 0 : 1, -$1.daysSince) }) { item in
                        let onList = app.list.open.contains { $0.productId == item.productId }
                        HStack(spacing: Space.l) {
                            ProductTile(name: item.productName, category: app.catalog.categoryName(forProduct: item.productId), size: 34)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(item.productName).textStyle(.rowTitle).foregroundStyle(Palette.ink).lineLimit(1)
                                Text("Every \(Int(item.medianIntervalDays.rounded())) days · last \(Int(item.daysSince.rounded())) days ago")
                                    .textStyle(.rowSub)
                                    .foregroundStyle(item.due ? Palette.amberText : Palette.ink2)
                            }
                            Spacer()
                            Button {
                                guard !onList else { return }
                                app.list.add(text: item.productName, productId: item.productId, qty: 1)
                                Haptics.play(.light)
                                Task { await app.list.sync() }
                            } label: {
                                IconView(onList ? .check : .plus, size: 14, weight: .bold)
                                    .foregroundStyle(onList ? Accent.white : Palette.ink)
                                    .frame(width: 30, height: 30)
                                    .background(onList ? Accent.green : Palette.trayField, in: Circle())
                            }
                            .buttonStyle(.press(scale: Motion.pillScale, haptic: nil))
                            .accessibilityLabel(onList ? "On the list" : "Add \(item.productName)")
                        }
                        .frame(height: 54)
                        .hairline()
                    }
                    if items.isEmpty {
                        Text("Suggestions appear once you've bought something a few times.")
                            .textStyle(.body).foregroundStyle(Palette.ink2).padding(.vertical, Space.xxl)
                    }
                }
            }
            .frame(maxHeight: 380)
            .scrollBounceBehavior(.basedOnSize)
            Button("Done") { trays.dismiss() }.buttonStyle(.shop(.secondary))
        }
    }
}

#Preview("List") {
    let app = AppModel(mock: true)
    return NavigationStack { ShoppingListView() }
        .environment(app)
        .trayHost(app.trays)
}
