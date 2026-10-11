import SwiftUI

/// Every design-system component in every state (Settings → About → Design catalog).
struct CatalogView: View {
    @State private var scheme: ColorScheme? = nil
    @State private var chip = "Dairy"
    @State private var segment = "By category"
    @State private var text = "http://shop.local:8080"
    @State private var password = ""
    @State private var search = ""
    @State private var checked = false
    @State private var lines: Int64 = 3787
    @State private var attempts = 0
    @State private var tab = 0
    @State private var trays = TrayCoordinator()
    @State private var toasts = ToastCenter()
    @State private var stressRunning = false
    @State private var stressCycle = 0
    @Environment(\.colorScheme) private var systemScheme

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                NavHeader(title: "Design catalog") {
                    Menu {
                        Button("System") { scheme = nil }
                        Button("Light") { scheme = .light }
                        Button("Dark") { scheme = .dark }
                    } label: {
                        IconView(.appearance, size: 18).foregroundStyle(Palette.ink)
                            .frame(width: Size.iconButton, height: Size.iconButton)
                            .background(Palette.sunk, in: Circle())
                    }
                }
                .padding(.horizontal, -Space.edge)

                section("Colour") { colours }
                section("Type") { typeScale }
                section("Amounts") {
                    HeroAmount(cents: 41238)
                    DeltaChip(deltaCents: -4272, caption: "less than this time in September")
                    CategoryBar(shares: MockData.categoryShares)
                }
                section("Buttons") {
                    Button("Connect your server") {}.buttonStyle(.shop(size: .large))
                    HStack(spacing: 8) {
                        Button("Ignore row") {}.buttonStyle(.shop(.secondary))
                        Button("Add line") {}.buttonStyle(.shop())
                    }
                    Button("Delete receipt") {}.buttonStyle(.shop(.destructive))
                    HStack {
                        IconButton.back {}
                        IconButton(icon: .more, label: "More") {}
                        IconButton(icon: .search, label: "Search") {}
                        Avatar {}
                        Spacer()
                        PillMenuButton(title: "Last 6 months") {}
                    }
                    LinkButton(title: "Add line") {}
                }
                section("Marks") {
                    HStack(spacing: 10) {
                        ForEach(["Albert Heijn", "Lidl", "Jumbo", "Dirk", "Ekoplaza", "Vomar"], id: \.self) { ShopMark(name: $0) }
                        ShopMark(name: "Albert Heijn", size: Size.shopMarkHeader)
                    }
                    HStack(spacing: 10) {
                        ProductTile(name: "Halfvolle melk", category: "Dairy")
                        ProductTile(name: "Bananen", category: "Produce")
                        ProductTile(name: "Kipfilet", category: "Meat & fish")
                        ProductTile(name: "Brood", category: "Bakery")
                        CountBadge(count: 2, ring: Palette.tray)
                        GlyphBadge()
                        GlyphBadge(glyph: .check, color: Accent.green)
                    }
                }
                section("Banners") {
                    AttentionCard(count: 2, title: "Receipts to review", hint: "about 2 min") {}
                    Banner(lead: "€0,25 short of the printed total.", message: "One row couldn't be read, it's marked below.")
                    Banner(tone: .red, lead: "No backup for more than 36 hours.", message: "Check the NAS share is mounted.")
                    NoticeRow(title: "NAS backup is 2 days old") {}
                    HStack {
                        Button("Toast") { toasts.show("Receipt confirmed", haptic: .success) }
                            .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                        Button("Error toast") { toasts.error("Upload failed") }
                            .buttonStyle(.shop(.destructive, size: .small, fullWidth: false))
                    }
                }
                section("Chips") {
                    ChipRow(items: ["All", "Dairy", "Produce", "Meat & fish"], title: { $0 }, dot: { $0 == "All" ? nil : CategoryColor.swatch(for: $0).color }, selection: $chip)
                        .padding(.horizontal, -Space.edge)
                    HStack {
                        Chip(title: "All 21", isSelected: true, size: .small) {}
                        ToneChip(title: "To fix 3") {}
                        SuggestionChip(title: "Eieren") {}
                    }
                    SegmentedControl(options: ["By category", "By shop"], title: { $0 }, selection: $segment)
                }
                section("Fields") {
                    FieldTile(label: "Server URL", text: $text, mono: true, large: true)
                    FieldTile(label: "Password", text: $password, large: true, isSecure: true, error: attempts > 0)
                        .shake(attempts)
                    Button("Wrong password") {
                        Haptics.play(.error)
                        attempts += 1
                    }
                    .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    SearchField(placeholder: "Products or receipt names", text: $search)
                    RawRowTile(caption: "Row 14 on the receipt", raw: "ST#TIEG&LD", amount: "0,25", background: Palette.sunk)
                }
                section("Rows") {
                    VStack(spacing: 0) {
                        DaySectionHeader(title: "Today", trailing: "€38,12")
                        ReceiptRow(shopName: "Lidl", subtitle: "Reading receipt…", subtitleColor: Palette.blueText, amount: "—", amountColor: Palette.ink3, showsSpinner: true)
                        ReceiptRow(shopName: "Albert Heijn", subtitle: "Total doesn’t match · 18:42", subtitleColor: Palette.amberText, amount: "€38,12")
                        LineRow(title: "Halfvolle melk", subtitle: "AH HALFV MELK 1,5L · Dairy", amount: "1,79", dot: Accent.blue)
                        LineRow(title: "Bonus", subtitle: "on Halfvolle melk", amount: "−0,45", titleColor: Palette.greenText, amountColor: Palette.greenText, dot: Accent.blue, indented: true)
                        LineRow(title: "HUMMUS NATUREL", isRaw: true, subtitle: "Not linked · tap to pick a product", subtitleColor: Palette.amberText, amount: "1,49", dot: Accent.amber)
                        ProductRow(name: "Halfvolle melk", meta: "1,5 l · Dairy", category: "Dairy", price: "€0,85/l", priceShop: "Lidl")
                        ListItemRow(title: "Halfvolle melk", hint: "Cheapest at Lidl · €0,85/l", hintShop: "Lidl", quantity: "× 2", isChecked: checked) { checked = $0 }
                        SettingsRow(icon: .upload, color: Accent.blue, title: "Upload queue", value: "Empty")
                        KeyValueRow(key: "Database", value: "Healthy", dot: Accent.green)
                    }
                }
                section("Cards") {
                    HStack(spacing: 8) {
                        StatCard(caption: "Cheapest at Lidl", value: "€0,85", unit: "/l", footnote: "Seen Mon 5 Oct", style: .highlight(Accent.yellow))
                        StatCard(caption: "You usually pay", value: "€0,89", unit: "/l", footnote: "Mostly at Albert Heijn")
                    }
                    HStack(spacing: 8) {
                        FeatureCard(icon: .scan, title: "Scan receipt", caption: "Paper receipt, add pages for long ones", color: Accent.blue) {}
                        FeatureCard(icon: .barcode, title: "Unpack groceries", caption: "Scan barcodes, add new products", color: Accent.green) {}
                    }
                }
                section("Import") {
                    let page = UIGraphicsImageRenderer(size: CGSize(width: 90, height: 130)).image { ctx in
                        UIColor(white: 0.93, alpha: 1).setFill()
                        ctx.fill(CGRect(x: 0, y: 0, width: 90, height: 130))
                    }
                    VStack(spacing: 0) {
                        ImportReceiptRow(pages: [page, page], title: "Receipt 1 · 2 pages", shopName: "Albert Heijn", straighten: .straightened, onShop: {}, onDetails: {}, onRemove: {})
                        JoinPill(isJoined: false) {}
                        ImportReceiptRow(pages: [page], title: "Receipt 2", shopName: nil, photoDateHint: "Photo from 3 Oct", straighten: .notFound, onShop: {}, onDetails: {}, onRemove: {})
                    }
                    Banner(lead: "No shop yet.", message: "Pick one so prices compare across shops.", action: (title: "Pick a shop", run: {}))
                    UploadProgressCard(state: .uploading(done: 3, total: 6))
                    UploadProgressCard(state: .offline(waiting: 6))
                    UploadProgressCard(state: .partial(uploaded: 4, failed: 2))
                    UploadProgressCard(state: .done(total: 6))
                    ImportReceiptRow(pages: [page], title: "Receipt 3", shopWillFindOut: true, onShop: {}, onDetails: {}, onRemove: {})
                    ShopSuggestionCard(name: "Aldi", detail: "Utrecht · aldi.nl", waiting: 3, onAccept: {}, onEdit: {}, onNotNow: {})
                    ShopSuggestionCard(kind: .existing, name: "Albert Heijn", detail: "Oudegracht", waiting: 1, onAccept: {}, onEdit: {}, onNotNow: {})
                    ReceiptRow(shopName: "Unknown shop", subtitle: "Looks like Aldi · tap to add", subtitleColor: Palette.blueText, amount: "€27,40", isUnknownShop: true)
                }
                section("Review queue") {
                    ReviewCard(
                        name: "Arla bio halfvolle melk", detail: "1 l", categoryName: "Dairy", reason: .nearMatch, suggestionName: "ARLA MELK",
                        source: .init(shopName: "Albert Heijn", dateText: "Sun 30 Aug", rawText: "ARLA BIO HALFV. MELK", amountText: "1,89")
                    )
                    ReviewCard(name: "Hummus naturel", reason: .noCategory, source: .init(shopName: "Lidl", dateText: "Thu 8 Oct", rawText: "HUMMUS NATUREL", amountText: "1,49"))
                    ReviewCard(name: "Unknown product 2345", reason: .barcodeMinimal, barcode: "8710400012345")
                    ReviewCard(name: "Unknown product 9876", reason: .barcodeOffUnavailable, barcode: "8710400019876")
                    ReviewCard(name: "Griekse yoghurt", reason: .barcodeNameMatch, suggestionName: "AH GRIEKS YOGH", barcode: "8718452390010", isOffline: true)
                    ReviewCardSkeleton()
                    AttentionCard(count: 12, title: "New products to check", hint: "about 2 min") {}
                    ToastView(message: ToastMessage(text: "Merged into AH BIO MELK", action: (title: "Undo", run: {})))
                }
                section("Scanner rows") {
                    VStack(spacing: 0) {
                        ScanTally(known: 9, added: 4, toName: 1).padding(.bottom, 10)
                        ScanEntryRow(code: "8718452390010", outcome: .looking)
                        ScanEntryRow(code: "8710400012345", outcome: .known(name: "Halfvolle melk", meta: "1,5 l"))
                        ScanEntryRow(code: "8718452390027", outcome: .created(name: "Griekse yoghurt 0%", category: "Dairy"), receiptLines: 2)
                        ScanEntryRow(code: "8710400012345", outcome: .createdMinimal(name: "Unknown product 2345"), mergeSuggestion: "AH BIO MELK")
                        ScanEntryRow(code: "8710400019876", outcome: .offUnavailable(name: "Unknown product 9876"))
                        ScanEntryRow(code: "8718452390034", outcome: .queued)
                        ScanEntryRow(code: "8718452390041", outcome: .failed(message: "Server not reachable"))
                    }
                    .padding(Space.l)
                    .background(Color(hex: 0x1C1C1A), in: RoundedRectangle.continuous(Radius.l))
                }
                section("Swipe actions (native)") {
                    List {
                        ForEach(["Albert Heijn", "Jumbo", "Dirk"], id: \.self) { shop in
                            ReceiptRow(shopName: shop, subtitle: "12:08 · 9 items", amount: "€12,40")
                                .listRowInsets(EdgeInsets())
                                .listRowSeparator(.hidden)
                                .listRowBackground(Color.clear)
                                .swipeActions(edge: .leading, allowsFullSwipe: true) {
                                    Button { } label: { Label("Confirm", systemImage: "checkmark").labelStyle(.iconOnly) }
                                        .tint(Accent.green)
                                }
                                .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                                    Button(role: .destructive) { } label: { Label("Delete", systemImage: "trash").labelStyle(.iconOnly) }
                                        .tint(Accent.red)
                                    Button { } label: { Label("Edit", systemImage: "pencil").labelStyle(.iconOnly) }
                                        .tint(Accent.blue)
                                }
                        }
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                    .scrollDisabled(true)
                    .frame(height: 200)
                }
                section("Trays") {
                    Button("Open a tray") { openTray() }.buttonStyle(.shop(.secondary))
                    Button("New category tray") { trays.present("cat-new") { CatalogCategoryTray(duplicate: false) } }
                        .buttonStyle(.shop(.secondary))
                    Button("Duplicate name state") { trays.present("cat-dup") { CatalogCategoryTray(duplicate: true) } }
                        .buttonStyle(.shop(.secondary))
                    Button("Delete category, move products") { trays.present("cat-move") { CatalogMoveTray() } }
                        .buttonStyle(.shop(.secondary))
                    Button(stressRunning ? "Running… \(stressCycle)/10" : "Tray stress: 10 open, swap, close cycles") { runTrayStress() }
                        .buttonStyle(.shop(.secondary))
                        .disabled(stressRunning)
                }
                section("Charts") {
                    SpendBars(bars: MockData.spendBars)
                    PriceChart(series: MockData.priceSeries)
                }
                section("Confirm bar") {
                    ConfirmBar(linesCents: lines, totalCents: 3812) {}
                    HStack {
                        Button("−25") { lines -= 25 }.buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                        Button("+25") { lines += 25 }.buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    }
                }
                section("Empty states") {
                    EmptyState(title: "Scan receipts on the day", message: "Thermal paper fades in weeks. Scan now and review whenever you like.")
                    HStack(spacing: 24) {
                        ReceiptThumb(pages: 2)
                        ProductHero(name: "Melk", category: "Dairy")
                    }
                }
                section("Loading states") {
                    SkeletonRows(count: 3) { SkeletonReceiptRow() }
                    SkeletonRows(count: 3) { SkeletonProductRow() }
                    SkeletonRows(count: 2) { SkeletonSimpleRow() }
                    SkeletonRows(count: 2) { SkeletonListRow() }
                    SkeletonChart(height: 110)
                    StaleBanner(what: "products") {}
                    ErrorState(message: "Saved receipts still work. This needs a connection.", offline: true) {}
                    LoadMoreFooter(state: .failed) {}
                }
                section("Navigation icons") {
                    navIconCompare
                }
                section("Home logo options") {
                    LogoOptions()
                }
                section("Tab bar") {
                    PillTabBar(items: [
                        TabBarItem(id: 0, title: "Home", icon: .home, selectedIcon: .homeFill, badge: 2),
                        TabBarItem(id: 1, title: "Analytics", icon: .analytics, selectedIcon: .analyticsFill),
                        TabBarItem(id: 2, title: "Products", icon: .products, selectedIcon: .productsFill),
                        TabBarItem(id: 3, title: "List", icon: .list, selectedIcon: .listFill),
                    ], selection: $tab) {}
                        .padding(.horizontal, -Space.edge)
                }
            }
            .padding(.horizontal, Space.gutter)
            .padding(.bottom, 60)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .toastHost(toasts, bottomInset: 30)
        .trayHost(trays)
        .environment(\.colorScheme, scheme ?? systemScheme)
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title.uppercased())
                .textStyle(TypeStyle.mono.sized(11))
                .foregroundStyle(Palette.ink3)
            content()
        }
    }

    private var navIconCompare: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            ForEach([NavIconStyle.duotone, .symbol], id: \.self) { style in
                Text(style == .duotone ? "Duotone (Phosphor, MIT)" : "SF Symbols (palette)")
                    .textStyle(.label).foregroundStyle(Palette.ink2)
                ForEach([ColorScheme.light, .dark], id: \.self) { scheme in
                    NavIconPanel(style: style)
                        .environment(\.colorScheme, scheme)
                }
            }
        }
    }

    private var colours: some View {
        let swatches: [(String, Color)] = [
            ("bg", Palette.bg), ("sunk", Palette.sunk), ("line", Palette.line), ("ink", Palette.ink),
            ("ink2", Palette.ink2), ("ink3", Palette.ink3), ("amber", Palette.amberTint), ("green", Palette.greenTint),
            ("red", Palette.redTint), ("blue", Accent.blue), ("pink", Accent.pink), ("purple", Accent.purple),
        ]
        return LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 8), count: 4), spacing: 8) {
            ForEach(swatches.indices, id: \.self) { i in
                let name = swatches[i].0
                let color = swatches[i].1
                VStack(alignment: .leading, spacing: 4) {
                    RoundedRectangle.continuous(Radius.ml)
                        .fill(color)
                        .frame(height: 44)
                        .overlay(RoundedRectangle.continuous(Radius.ml).strokeBorder(Palette.line))
                    Text(name).textStyle(.caption).foregroundStyle(Palette.ink2)
                }
            }
        }
    }

    private var typeScale: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Products").textStyle(.screenTitle)
            Text("Scan receipts on the day").textStyle(.emptyTitle)
            Text("Add line").textStyle(.trayTitle)
            Text("Halfvolle melk  1,79").textStyle(.rowTitle)
            Text("AH HALFV MELK 1,5L · Dairy").textStyle(.rowSub).foregroundStyle(Palette.ink2)
            Text("Today  €38,12").textStyle(.label).foregroundStyle(Palette.ink2)
            Text("AH GRIEKS YOGH 0%").textStyle(.mono)
        }
        .foregroundStyle(Palette.ink)
    }

    /// Repeatable tray workload for hitch recording on a device (docs/DESIGN-STATUS.md, device check pack):
    /// open a light tray, swap to a heavier form tray, close, ten times, hands off the screen.
    private func runTrayStress() {
        stressRunning = true
        stressCycle = 0
        Task {
            for i in 1...10 {
                stressCycle = i
                trays.present("stress-a") {
                    VStack(spacing: 14) {
                        TrayHeader(title: "Options")
                        OptionRow(icon: .edit, title: "Edit details", hint: "Shop, date and printed total") {}
                        OptionRow(icon: .reparse, title: "Reparse", hint: "Apply the latest rules") {}
                        OptionRow(icon: .trash, title: "Delete receipt", hint: "Removes the receipt and its image", destructive: true) {}
                    }
                }
                try? await Task.sleep(for: .milliseconds(900))
                trays.present("stress-b") {
                    VStack(alignment: .leading, spacing: Space.lx) {
                        TrayHeader(title: "New shop")
                        Grid(horizontalSpacing: Space.m, verticalSpacing: Space.m) {
                            GridRow {
                                FieldTile(label: "Name", text: .constant(""), placeholder: "Albert Heijn", background: Palette.trayField)
                                FieldTile(label: "Branch", text: .constant(""), placeholder: "Oudegracht", background: Palette.trayField)
                            }
                            GridRow {
                                FieldTile(label: "Address", text: .constant(""), placeholder: "Optional", background: Palette.trayField)
                                FieldTile(label: "City", text: .constant(""), placeholder: "Utrecht", background: Palette.trayField)
                            }
                        }
                        Button("Save") {}.buttonStyle(.shop(haptic: .medium))
                    }
                }
                try? await Task.sleep(for: .milliseconds(900))
                trays.dismiss()
                try? await Task.sleep(for: .milliseconds(700))
            }
            stressRunning = false
        }
    }

    private func openTray() {
        trays.present("catalog-options") {
            VStack(spacing: 14) {
                TrayHeader(title: "Options")
                VStack(spacing: 6) {
                    OptionRow(icon: .edit, title: "Edit details", hint: "Shop, date and printed total") {}
                    OptionRow(icon: .reparse, title: "Reparse", hint: "Shows a second tray in place") {
                        trays.present("catalog-confirm") {
                            VStack(spacing: 14) {
                                TrayHeader(title: "Reparse receipt?", subtitle: "Keeps your edits")
                                Button("Reparse") { trays.dismiss() }.buttonStyle(.shop())
                            }
                        }
                    }
                    OptionRow(icon: .trash, title: "Delete receipt", hint: "Removes the receipt and its image", destructive: true) {
                        Haptics.play(.error)
                        trays.dismiss()
                    }
                }
            }
        }
    }
}

#Preview("Catalog") {
    CatalogView()
}

#Preview("Catalog · dark") {
    CatalogView().preferredColorScheme(.dark)
}


/// One tab bar on a page-coloured panel, tappable, for the icon comparison.
private struct NavIconPanel: View {
    let style: NavIconStyle
    @State private var tab = 1

    var body: some View {
        PillTabBar(items: [
            TabBarItem(id: 0, title: "Home", icon: .home, selectedIcon: .homeFill, badge: 2),
            TabBarItem(id: 1, title: "Analytics", icon: .analytics, selectedIcon: .analyticsFill),
            TabBarItem(id: 2, title: "Products", icon: .products, selectedIcon: .productsFill),
            TabBarItem(id: 3, title: "List", icon: .list, selectedIcon: .listFill),
        ], selection: $tab) {}
            .environment(\.navIconStyle, style)
            .padding(.vertical, Space.l)
            .frame(maxWidth: .infinity)
            .background(Palette.bg)
            .clipShape(RoundedRectangle.continuous(Radius.l))
            .overlay(RoundedRectangle.continuous(Radius.l).strokeBorder(Palette.line))
    }
}


private struct CatalogCategoryTray: View {
    let duplicate: Bool
    @State private var name = ""
    @State private var colour: String?

    var body: some View {
        CategoryFormLayout(title: "Add category", name: $name, colour: $colour,
                           errorText: duplicate && !name.isEmpty ? "You already have a category called \(name)." : nil,
                           canSave: !name.isEmpty && !duplicate, saving: false) {}
            .onAppear { if duplicate { name = "Dairy" } }
    }
}

private struct CatalogMoveTray: View {
    @State private var destination: Int64 = 0

    var body: some View {
        MoveProductsLayout(
            categoryName: "Dairy", count: 4,
            destinations: [("Bakery", Accent.orange), ("Drinks", Accent.purple), ("Frozen", Accent.cyan), ("Household", Accent.teal),
                           ("Meat & fish", Accent.red), ("Other", Palette.neutral), ("Pantry", Accent.yellow)]
                .enumerated().map { .init(id: Int64($0.offset + 1), name: $0.element.0, colour: $0.element.1) },
            destination: $destination, working: false, onCancel: {}, onDelete: {})
    }
}


/// A, B, C on a Home-header mock, light and dark; tap one to replay the tilt.
private struct LogoOptions: View {
    @State private var wiggle = 0

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            ForEach(Array(ShopLogoStyle.allCases.enumerated()), id: \.offset) { i, style in
                Text(["A  Badge + wordmark (recommended)", "B  Torn-receipt wordmark", "C  Total-bar pill"][i])
                    .textStyle(.label).foregroundStyle(Palette.ink2)
                ForEach([ColorScheme.light, .dark], id: \.self) { scheme in
                    HStack {
                        Avatar(initial: "I") {}
                        Spacer()
                        ShopLogo(style: style, height: 30, wiggle: wiggle)
                        Spacer()
                        IconView(.search, size: 18).foregroundStyle(Palette.ink)
                            .frame(width: Size.iconButton, height: Size.iconButton)
                            .background(Palette.sunk, in: Circle())
                    }
                    .padding(Space.l)
                    .background(Palette.bg, in: RoundedRectangle.continuous(Radius.l))
                    .overlay(RoundedRectangle.continuous(Radius.l).strokeBorder(Palette.line))
                    .environment(\.colorScheme, scheme)
                    .onTapGesture { wiggle += 1 }
                }
            }
        }
    }
}
