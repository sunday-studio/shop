import SwiftUI

/// L · Shopping list: works offline (ShoppingListStore), "cheapest at" hints, suggestions
/// from things you usually buy by now, check-off animation, clear checked.
struct ShoppingListView: View {
    @Environment(AppModel.self) private var app
    @State private var frequent: [FrequentItem] = []
    /// Groups the user opened or closed by hand (done groups start collapsed, others expanded).
    @State private var collapseOverrides: [Int64: Bool] = [:]

    private var list: ShoppingListStore { app.list }

    var body: some View {
        List {
            LargeTitleHeader(title: "List", detail: summary) { listMenu }
                .shopListRow()

            FieldButton(title: "Add an item") { openAdd() }
                .padding(.horizontal, Space.edge)
                .padding(.top, Space.lx)
                .shopListRow()

            if !list.suggestions.isEmpty {
                ListSuggestionsCard()
                    .padding(.horizontal, Space.edge)
                    .padding(.top, Space.lx)
                    .shopListRow()
            }

            if !suggestions.isEmpty {
                VStack(alignment: .leading, spacing: Space.m) {
                    TypeStyle.label.text("Usually bought by now").foregroundStyle(Palette.ink2)
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
                        .buttonStyle(.press(haptic: .selection))
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.lx)
                .shopListRow()
            }

            if let error = list.lastError, !error.isConnectivity, list.isStale, !list.items.isEmpty {
                StaleBanner(what: "list") { Task { await list.sync() } }
                    .padding(.horizontal, Space.edge)
                    .padding(.top, Space.l)
                    .shopListRow()
            }

            items
        }
        .shopListStyle()
        .contentMargins(.bottom, Space.tabBarClearance, for: .scrollContent)
        .animation(Motion.collapse, value: list.items.map(\.id))
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

    private var listMenu: some View {
        Menu {
            Button("New group", systemImage: "plus") { app.trays.present("new-group") { NewGroupTray() } }
            Button("New from template", systemImage: "doc.on.doc") { app.trays.present("new-from-template") { NewFromTemplateTray() } }
            if !list.archivedGroups.isEmpty {
                Button("Show archived", systemImage: "archivebox") { app.trays.present("archived-groups") { ArchivedGroupsTray() } }
            }
            if !list.checked.isEmpty {
                Button("Clear checked", systemImage: "checkmark.circle") {
                    Haptics.play(.light)
                    withAnimation(Motion.collapse) { list.clearChecked() }
                    Task { await list.sync() }
                }
            }
        } label: {
            IconView(.more, size: 16, weight: .bold)
                .foregroundStyle(Palette.ink)
                .frame(width: Size.iconButton, height: Size.iconButton)
                .background(Palette.sunk, in: Circle())
        }
        .accessibilityLabel("List options")
    }

    private func isCollapsed(_ group: ListGroup) -> Bool {
        collapseOverrides[group.id] ?? list.isGroupDone(group.id)
    }

    @ViewBuilder
    private func groupSection(_ group: ListGroup) -> some View {
        let collapsed = isCollapsed(group)
        GroupHeaderRow(
            group: group,
            progress: list.progress(group.id),
            collapsed: collapsed,
            onToggle: { withAnimation(Motion.collapse) { collapseOverrides[group.id] = !collapsed } },
            onAdd: { app.trays.present("group-add-\(group.id)") { AddItemTray(groupId: group.id, groupName: group.name) } },
            onRename: { app.trays.present("group-rename") { RenameGroupTray(group: group) } },
            onSaveTemplate: { app.trays.present("group-template") { SaveTemplateTray(group: group) } },
            onArchive: { archive(group) },
            onDelete: { askDelete(group) }
        )
        .padding(.horizontal, Space.gutter)
        .shopListRow()
        if !collapsed {
            let members = list.groupItems(group.id)
            let open = members.filter { !$0.isChecked }
            let done = members.filter(\.isChecked)
            if members.isEmpty {
                TypeStyle.rowSub.text("No items yet").foregroundStyle(Palette.ink2)
                    .padding(.horizontal, Space.gutter).padding(.vertical, Space.m)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .shopListRow()
            }
            ForEach(open + done) { item in row(item) }
        }
    }

    private func archive(_ group: ListGroup) {
        Haptics.play(.selection)
        withAnimation(Motion.collapse) { list.archiveGroup(group.id, true) }
        Task { await list.sync() }
        app.toasts.undoable("Archived \(group.name)") {
            withAnimation(Motion.collapse) { list.archiveGroup(group.id, false) }
            Task { await list.sync() }
        }
    }

    private func askDelete(_ group: ListGroup) {
        let count = list.groupItems(group.id).count
        app.trays.present("group-delete") {
            DeleteGroupTray(group: group, itemCount: count) { keepItems in
                Task { @MainActor in
                    try? await Task.sleep(for: .milliseconds(350))
                    var snapshot: ShoppingListStore.DeletedGroup?
                    withAnimation(Motion.collapse) { snapshot = list.deleteGroup(group.id, keepItems: keepItems) }
                    Haptics.play(.warning)
                    await list.sync()
                    if let snapshot {
                        app.toasts.show("Deleted “\(group.name)”", duration: .seconds(6), action: (title: "Undo", run: {
                            Haptics.play(.light)
                            Task { await list.restoreGroup(snapshot); await list.sync() }
                        }))
                    }
                }
            }
        }
    }

    /// The last check in a group: one success haptic and an offer to archive it.
    private func checkWatch(_ item: ListItem, _ change: () -> Void) {
        let gid = item.groupId
        let wasDone = gid.map { list.isGroupDone($0) } ?? false
        change()
        guard let gid, !wasDone, list.isGroupDone(gid), let group = list.group(gid) else { return }
        Haptics.play(.success)
        app.toasts.show("\(group.name) is done", tone: .green, duration: .seconds(6), action: (title: "Archive", run: { archive(group) }))
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
        if list.mainItems.isEmpty && list.checklists.isEmpty {
            StatefulContent(phase: list.lastSync == nil && list.lastError == nil ? .loading : .empty) {
                SkeletonRows { SkeletonListRow() }
            } empty: {
                EmptyState(title: "Nothing on the list", message: "Add things as you run out. The list works without signal and syncs when you're back online.") {
                    GlyphIllustration(icon: .list, color: Accent.green)
                }
                .padding(.top, 40)
            } content: {
                EmptyView()
            }
            .padding(.top, Space.ml)
            .padding(.horizontal, Space.gutter)
            .shopListRow()
        }

        ForEach(Array(list.open.enumerated()), id: \.element.id) { index, item in
            row(item)
                .padding(.top, index == 0 ? Space.ml : 0)
        }

        ForEach(list.checklists) { group in
            groupSection(group)
        }

        if !list.checked.isEmpty {
            HStack {
                TypeStyle.body.text("\(list.checked.count) checked off").foregroundStyle(Palette.ink2)
                Spacer()
                Button("Clear") {
                    Haptics.play(.light)
                    withAnimation(Motion.collapse) { list.clearChecked() }
                    Task { await list.sync() }
                }
                .textStyle(TypeStyle.label.sized(14))
                .foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: .selection))
            }
            .frame(height: 46)
            .padding(.horizontal, Space.gutter)
            .shopListRow()
            ForEach(list.checked) { item in
                row(item)
            }
        }

        if list.pendingCount > 0 {
            HStack(spacing: Space.s) {
                StatusDot(color: app.network.isOnline ? Accent.blue : Palette.ink3)
                TypeStyle.rowSub.text(app.network.isOnline ? "Syncing \(list.pendingCount) change\(list.pendingCount == 1 ? "" : "s")…" : "\(list.pendingCount) change\(list.pendingCount == 1 ? "" : "s") sync when you're online")
                    .foregroundStyle(Palette.ink2)
            }
            .padding(.top, Space.l)
            .padding(.horizontal, Space.gutter)
            .shopListRow()
        }
    }

    private func row(_ item: ListItem) -> some View {
        SwipeActionRow(
            leading: SwipeAction(icon: "checkmark", title: item.isChecked ? "Uncheck" : "Check off", tint: Accent.green, haptic: .soft) { toggleBySwipe(item) },
            trailing: SwipeAction(icon: "trash", title: "Delete", tint: Accent.red, removesRow: true, haptic: .soft) { remove(item) }
        ) {
        ListItemRow(
            title: item.title,
            hint: hint(item),
            hintShop: receiptNote(item)?.shop ?? item.cheapest?.shopName,
            quantity: quantity(item),
            isChecked: item.isChecked,
            onToggle: { checked in toggle(item, checked) },
            onTap: { openEdit(item) }
        )
        .padding(.horizontal, Space.gutter)
        }
        .shopListRow()
        .contextMenu {
            Button("Edit", systemImage: "pencil") { openEdit(item) }
            if let id = item.productId { Button("Open product", systemImage: "shippingbox") { app.push(.product(id)) } }
            if !list.checklists.isEmpty || item.groupId != nil {
                Button("Move to…", systemImage: "arrow.right.circle") { app.trays.present("move-item") { MoveItemTray(item: item) } }
            }
            if item.isReceiptChecked, let receiptId = item.checkedReceiptId {
                Button("Open receipt", systemImage: "doc.text") { app.push(.receipt(receiptId)) }
            }
            Button("Delete", systemImage: "trash", role: .destructive) { remove(item) }
        }
    }

    /// Check or uncheck straight away (the swipe is the confirmation, no 420 ms tick animation).
    private func toggleBySwipe(_ item: ListItem) {
        checkWatch(item) { withAnimation(Motion.collapse) { list.setChecked(item, !item.isChecked) } }
        Task { await list.sync() }
    }

    /// Removes the item with an Undo toast (the item is added back).
    private func remove(_ item: ListItem) {
        withAnimation(Motion.collapse) { list.delete(item) }
        Task { await list.sync() }
        app.toasts.undoable("Removed \(item.title)") {
            list.add(text: item.title, productId: item.productId, qty: item.qty, note: item.note)
            Task { await list.sync() }
        }
    }

    /// "Bought at Corner Market · Tue" for items a receipt checked.
    private func receiptNote(_ item: ListItem) -> (text: String, shop: String?)? {
        guard item.isReceiptChecked, let id = item.checkedReceiptId else { return nil }
        guard let receipt = app.home.receipts.first(where: { $0.id == id }) else { return ("Bought on a receipt", nil) }
        let shop = receipt.shopId.map { app.catalog.shopName($0) }
        let when = Dates.shortDay(receipt.displayDate)
        return (["Bought" + (shop.map { " at \($0)" } ?? ""), when].joined(separator: " · "), shop)
    }

    private func hint(_ item: ListItem) -> String? {
        if let note = receiptNote(item) { return note.text }
        let also = list.alsoIn(item)
        if !also.isEmpty, item.cheapest == nil { return "Also in " + also.joined(separator: ", ") }
        if let c = item.cheapest {
            let price = "Cheapest at \(c.shopName) · \(Money.perUnit(c.latestUnitCents, unit: c.baseUnit))"
            // "1 of 2 bought" (a partly bought item) comes first, so the reason for the lower quantity is visible.
            if let note = item.note, !note.isEmpty, note.contains(" bought") { return note + " · " + price }
            return price
        }
        // Plain checklist lines (a recipe, a packing list) don't need the catalog nag.
        if item.productId == nil { return item.groupId != nil ? item.note : (item.note ?? "Not in your catalog") }
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
            checkWatch(item) { withAnimation(Motion.collapse) { list.setChecked(item, true) } }
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
    /// Add into this checklist instead of the main list.
    var groupId: Int64? = nil
    var groupName: String? = nil

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
            TrayHeader(title: editing == nil ? (groupName.map { "Add to \($0)" } ?? "Add to list") : "Edit item")

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
                                ProductTile(name: p.name, category: app.catalog.category(p.categoryId)?.name, categoryToken: app.catalog.category(p.categoryId)?.color, size: 34)
                                VStack(alignment: .leading, spacing: 1) {
                                    TypeStyle.rowTitle.text(p.name).foregroundStyle(Palette.ink).lineLimit(1)
                                    TypeStyle.rowSub.text([p.sizeText, app.catalog.category(p.categoryId)?.name].compactMap { $0 }.joined(separator: " · ")).foregroundStyle(Palette.ink2).lineLimit(1)
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
                            TypeStyle.rowTitle.text("Add “\(text.trimmingCharacters(in: .whitespaces))” as text").lineLimit(1)
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
                        TypeStyle.caption.text("Quantity").foregroundStyle(Palette.ink2)
                        TypeStyle.input.sized(17).text(Quantity.decimal(qty)).foregroundStyle(Palette.ink)
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
                        let item = editing
                        trays.dismiss()
                        Task { @MainActor in
                            try? await Task.sleep(for: .milliseconds(350))
                            app.list.delete(item)
                            Haptics.play(.soft)
                            await app.list.sync()
                            app.toasts.undoable("Removed \(item.title)") {
                                app.list.add(text: item.title, productId: item.productId, qty: item.qty, note: item.note)
                                Task { await app.list.sync() }
                            }
                        }
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
        // The suggestion block growing or shrinking while you type eases the tray's height.
        .animation(Motion.sheetSwap, value: resultsHeightKey)
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

    private var resultsHeightKey: Int {
        guard editing == nil, !text.trimmingCharacters(in: .whitespaces).isEmpty else { return 0 }
        return 1 + min(results.count, 3)
    }

    private func stepper(_ symbol: String, label: String, action: @escaping () -> Void) -> some View {
        Button {
            Haptics.play(.selection)
            action()
        } label: {
            TypeStyle.input.text(symbol)
                .foregroundStyle(Palette.ink)
                .frame(width: 30, height: 30)
                .background(Palette.tray, in: Circle())
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
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
        app.list.add(text: p.name, productId: p.id, qty: qty, note: note.isEmpty ? nil : note, groupId: groupId)
        finish()
    }

    private func addText() {
        let t = text.trimmingCharacters(in: .whitespaces)
        guard !t.isEmpty else { return }
        app.list.add(text: t, qty: qty == 1 ? nil : qty, note: note.isEmpty ? nil : note, groupId: groupId)
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
                            ProductTile(name: item.productName, category: app.catalog.categoryName(forProduct: item.productId), categoryToken: app.catalog.category(app.catalog.product(item.productId)?.categoryId)?.color, size: 34)
                            VStack(alignment: .leading, spacing: 1) {
                                TypeStyle.rowTitle.text(item.productName).foregroundStyle(Palette.ink).lineLimit(1)
                                TypeStyle.rowSub.text("Every \(Int(item.medianIntervalDays.rounded())) days · last \(Int(item.daysSince.rounded())) days ago")
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
                            .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
                            .accessibilityLabel(onList ? "On the list" : "Add \(item.productName)")
                        }
                        .frame(height: 54)
                        .hairline()
                    }
                    if items.isEmpty {
                        TypeStyle.body.text("Suggestions appear once you've bought something a few times.").foregroundStyle(Palette.ink2).padding(.vertical, Space.xxl)
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
