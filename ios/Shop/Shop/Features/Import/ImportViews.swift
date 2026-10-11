import SwiftUI

// MARK: - S2 · Source tray

/// "Photo library" or "Files". Replaces the Add tray; the pickers themselves are hosted by the
/// root view so they survive the tray going away.
struct ImportSourceTray: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            TrayHeader(title: "Import receipts")
            VStack(spacing: 6) {
                OptionRow(icon: .image, title: "Photo library", hint: "Pick as many as you like") {
                    app.beginImport(.photos)
                }
                OptionRow(icon: .scan, title: "Files", hint: "JPEG, PNG, HEIC or PDF") {
                    app.beginImport(.files)
                }
            }
            TypeStyle.rowSub.text("Receipts are read on your own server. Nothing leaves your home network.")
                .foregroundStyle(Palette.ink2)
                .padding(.horizontal, Space.xs)
        }
    }
}

// MARK: - S3 · Import screen

struct ImportScreen: View {
    @Bindable var model: ImportModel
    @Environment(AppModel.self) private var app
    @State private var trays = TrayCoordinator()
    @State private var committing = false
    @State private var flashShopRow = false
    @State private var adjusting: AdjustTarget?

    struct AdjustTarget: Identifiable {
        let receiptId: UUID
        let pageId: UUID
        var id: UUID { pageId }
    }

    private var fallbackShopId: Int64? { ImportFlags.letShopFindOut || model.receipts.count != 1 ? nil : app.catalog.lastShop?.id }
    private var displayedBatchShop: Int64? { model.batchShopId ?? fallbackShopId }
    private var missing: Int { ImportFlags.letShopFindOut ? 0 : model.missingShops(fallbackShopId) }

    var body: some View {
        GeometryReader { geo in
            content(topPad: Self.extraInset(\.top, have: geo.safeAreaInsets.top))
        }
        .safeAreaInset(edge: .bottom, spacing: 0) { footer }
        .background(Palette.bg.ignoresSafeArea())
        .trayHost(trays)
        .fullScreenCover(item: $adjusting) { target in
            AdjustScreen(model: model, receiptId: target.receiptId, pageId: target.pageId)
        }
        .onChange(of: model.isEmpty) { _, empty in
            if empty { close() }
        }
    }

    /// Full-screen covers can arrive with no safe-area inset; add what the window has and the
    /// view doesn't.
    static func extraInset(_ edge: KeyPath<UIEdgeInsets, CGFloat>, have: CGFloat) -> CGFloat {
        let window = UIApplication.shared.connectedScenes.compactMap { ($0 as? UIWindowScene)?.keyWindow }.first
        return max(0, (window?.safeAreaInsets[keyPath: edge] ?? 0) - have)
    }

    private func content(topPad: CGFloat) -> some View {
        VStack(spacing: 0) {
            header
                .padding(.top, topPad)
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    controls
                    list
                }
                .padding(.bottom, Space.xl)
            }
            .scrollIndicators(.hidden)
        }
    }

    // MARK: Pieces

    private var header: some View {
        HStack(spacing: Space.l) {
            IconButton(icon: .close, label: "Close") { requestClose() }
            TypeStyle.navTitle.text("Import receipts")
                .foregroundStyle(Palette.ink)
            Spacer()
            TypeStyle.label.text(model.pageCount == 1 ? "1 photo" : "\(model.pageCount) photos")
                .foregroundStyle(Palette.ink2)
                .contentTransition(.numericText())
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, 8)
        .frame(minHeight: 52)
    }

    @ViewBuilder
    private var controls: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            if model.pageCount >= 2 {
                VStack(alignment: .leading, spacing: 6) {
                    SegmentedControl(options: [ImportModel.Grouping.separate, .oneLong], title: { $0 == .separate ? "Separate receipts" : "One long receipt" },
                                     selection: Binding(get: { model.grouping }, set: { model.setGrouping($0) }))
                        .opacity(1)
                    if !model.canJoinAll {
                        TypeStyle.rowSub.text("Up to 5 pages").foregroundStyle(Palette.ink2)
                    }
                }
            }
            if model.cappedAtLimit {
                Banner(tone: .amber, lead: nil, message: "Only the first 30 pages were added.")
            }
            Button {
                trays.present("import-shop") { ImportShopTray(model: model, target: nil, fallbackShopId: fallbackShopId) }
            } label: {
                HStack(spacing: Space.ml) {
                    TypeStyle.input.sized(15).text("Shop").foregroundStyle(Palette.ink)
                    Spacer()
                    if let shop = app.catalog.shop(displayedBatchShop) {
                        ShopMark(name: shop.name, size: 20)
                        TypeStyle.body.text(shop.displayName).foregroundStyle(Palette.ink2).lineLimit(1)
                    } else if ImportFlags.letShopFindOut {
                        UnknownShopMark(size: 20)
                        TypeStyle.body.text("Let Shop find out").foregroundStyle(Palette.ink2)
                    } else {
                        TypeStyle.body.text("Pick a shop").foregroundStyle(Palette.amberText)
                    }
                    IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                }
                .padding(.horizontal, Space.lx)
                .frame(height: 52)
                .background(flashShopRow ? Palette.amberTint : Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            }
            .buttonStyle(.press(haptic: .selection))
            if model.receipts.count >= 2 {
                Button("Set a date for all") {
                    trays.present("import-date-all") { ImportDateAllTray(model: model) }
                }
                .textStyle(.label)
                .foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: .selection))
            }
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.l)
    }

    private var list: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(model.receipts.enumerated()), id: \.element.id) { index, receipt in
                let isLast = index == model.receipts.count - 1
                ZStack(alignment: .bottom) {
                    VStack(spacing: 0) {
                        ImportReceiptRow(
                            pages: receipt.pages.map(\.thumbnail),
                            title: model.title(at: index),
                            shopName: app.catalog.shop(model.effectiveShopId(receipt, fallback: fallbackShopId))?.name,
                            shopWillFindOut: ImportFlags.letShopFindOut,
                            dateText: model.dateText(receipt),
                            photoDateHint: model.photoHint(for: receipt),
                            onShop: { trays.present("import-shop") { ImportShopTray(model: model, target: receipt.id, fallbackShopId: fallbackShopId) } },
                            onDetails: { openDetails(receipt.id) },
                            onRemove: { Haptics.play(.soft); model.remove(receipt.id) }
                        )
                        .overlay(alignment: .topLeading) { cropBadge(receipt) }
                        .contentShape(Rectangle())
                        .onTapGesture { openDetails(receipt.id) }
                        .accessibilityAction(named: "Pick shop") { trays.present("import-shop") { ImportShopTray(model: model, target: receipt.id, fallbackShopId: fallbackShopId) } }
                        .accessibilityAction(named: "Set date") { openDetails(receipt.id) }
                        .accessibilityAction(named: "Remove") { model.remove(receipt.id) }
                        if let page = receipt.pages.first(where: { $0.straighten == .notFound }) {
                            HStack(spacing: 6) {
                                TypeStyle.caption.text("Edges not found").foregroundStyle(Palette.ink3)
                                Button("Adjust") { adjusting = AdjustTarget(receiptId: receipt.id, pageId: page.id) }
                                    .textStyle(.captionMedium)
                                    .foregroundStyle(Palette.blueText)
                                    .buttonStyle(.press(haptic: .selection))
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.leading, 56 * 0.72 + Space.l)
                            .padding(.top, Space.xs)
                            .padding(.bottom, 20)
                        }
                    }
                    if !isLast {
                        JoinPill(isJoined: false, isEnabled: model.canJoin(after: index)) {
                            model.join(after: index)
                        }
                        .offset(y: 13)
                        .zIndex(1)
                    }
                }
                .padding(.bottom, isLast ? 0 : 13)
                .transition(.opacity)
                .staggeredAppear(index: index)
            }
            ForEach(model.unreadable) { file in
                HStack(spacing: Space.l) {
                    RoundedRectangle.continuous(6).fill(Palette.redTint).frame(width: 40, height: 56)
                    VStack(alignment: .leading, spacing: 2) {
                        TypeStyle.rowTitle.text(file.name).foregroundStyle(Palette.ink).lineLimit(1)
                        TypeStyle.rowSub.text("Can't read this file").foregroundStyle(Palette.redText)
                    }
                    Spacer()
                    Button("Remove") { model.removeUnreadable(file.id) }
                        .textStyle(.label).foregroundStyle(Palette.ink2)
                        .buttonStyle(.press(haptic: .soft))
                }
                .padding(.vertical, Space.l)
                .hairline()
            }
            if model.isLoading {
                HStack(spacing: Space.m) {
                    ProgressView().controlSize(.small).tint(Palette.ink2)
                    TypeStyle.rowSub.text("Reading photos…").foregroundStyle(Palette.ink2)
                }
                .padding(.vertical, Space.xl)
            }
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.l)
    }

    @ViewBuilder
    private func cropBadge(_ receipt: ImportModel.Receipt) -> some View {
        if receipt.pages.contains(where: { $0.straighten == .straightened || $0.straighten == .adjusted }) {
            Image(systemName: "crop")
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(Palette.ink2)
                .frame(width: 16, height: 16)
                .background(Palette.sunk, in: Circle())
                .overlay(Circle().strokeBorder(Palette.bg, lineWidth: 1.5))
                .padding(.leading, 2)
                .padding(.top, Space.l - 4)
                .accessibilityLabel("Straightened")
        }
    }

    // MARK: Footer

    private var footer: some View {
        VStack(spacing: Space.m) {
            Button {
                commit()
            } label: {
                LoadingLabel(title: buttonTitle, isLoading: committing)
                    .contentTransition(.numericText())
            }
            .buttonStyle(.shop(haptic: nil))
            .disabled(committing || model.receipts.isEmpty || missing > 0)
            statusLine
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.l)
        .padding(.bottom, Space.m + Self.extraInset(\.bottom, have: 0))
        .frame(maxWidth: .infinity)
        .background(Palette.bg.opacity(0.94))
    }

    private var buttonTitle: String {
        if model.receipts.isEmpty { return model.isLoading ? "Reading photos…" : "Import receipt" }
        return model.receipts.count == 1 ? "Import receipt" : "Import \(model.receipts.count) receipts"
    }

    @ViewBuilder
    private var statusLine: some View {
        if missing > 0 {
            Button {
                flashShop()
            } label: {
                TypeStyle.rowSub.text(missing == 1 ? "Choose a shop for 1 receipt" : "Choose a shop for \(missing) receipts").foregroundStyle(Palette.amberText)
            }
            .buttonStyle(.press(haptic: nil))
        } else if !model.unreadable.isEmpty {
            TypeStyle.rowSub.text(model.unreadable.count == 1 ? "1 file can't be read · remove it or import the rest" : "\(model.unreadable.count) files can't be read · remove them or import the rest").foregroundStyle(Palette.amberText)
        } else if !app.network.isOnline {
            HStack(spacing: 6) {
                StatusDot(color: Palette.ink3, size: 7)
                TypeStyle.rowSub.text("Offline · uploads when you're back online").foregroundStyle(Palette.ink2)
            }
        } else {
            TypeStyle.rowSub.text("Uploads in the background").foregroundStyle(Palette.ink2)
        }
    }

    // MARK: Actions

    private func flashShop() {
        withAnimation(Motion.ease) { flashShopRow = true }
        Task {
            try? await Task.sleep(for: .milliseconds(500))
            withAnimation(Motion.ease) { flashShopRow = false }
        }
    }

    private func openDetails(_ id: UUID) {
        trays.present("import-details") {
            ImportDetailsTray(model: model, receiptId: id, fallbackShopId: fallbackShopId) { page in
                trays.dismiss()
                adjusting = AdjustTarget(receiptId: id, pageId: page)
            }
        }
    }

    private func requestClose() {
        guard model.touched, !model.receipts.isEmpty else { close(); return }
        let count = model.pageCount
        trays.present("import-discard") {
            ConfirmTray(title: count == 1 ? "Discard 1 photo?" : "Discard \(count) photos?", confirmTitle: "Discard", destructive: true) {
                Haptics.play(.warning)
                close()
                return true
            }
        }
    }

    private func close() {
        app.importFlow = nil
        app.importPicker = nil
    }

    private func commit() {
        guard !committing else { return }
        committing = true
        let drafts = model.drafts(catalog: app.catalog)
        let source = model.source
        let online = app.network.isOnline
        Task {
            await app.uploads.enqueueImport(drafts, source: source)
            if let shop = model.receipts.compactMap({ model.effectiveShopId($0, fallback: fallbackShopId) }).last {
                app.catalog.lastShopId = shop
            }
            Haptics.play(.uploadQueued)
            app.importFlow = nil
            app.tab = .home
            app.toasts.show(online ? "Receipts added · uploading" : "Saved · uploads when you're online")
        }
    }
}

// MARK: - S4 · Shop tray

struct ImportShopTray: View {
    let model: ImportModel
    /// nil = the batch row.
    let target: UUID?
    let fallbackShopId: Int64?
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays

    private var selectedId: Int64? {
        if let target, let r = model.receipt(target) { return r.shopId ?? model.batchShopId ?? fallbackShopId }
        return model.batchShopId ?? fallbackShopId
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Which shop?", subtitle: target == nil ? "All receipts without their own shop" : model.receipt(target!).map { _ in "This receipt only" })
            ScrollView {
                VStack(spacing: 0) {
                    if ImportFlags.letShopFindOut {
                        row(mark: AnyView(UnknownShopMark(size: Size.shopMarkHeader)), title: "Let Shop find out", sub: "Reads the shop name from the receipt", on: selectedId == nil) {
                            choose(nil)
                        }
                    }
                    ForEach(orderedShops) { shop in
                        row(mark: AnyView(ShopMark(name: shop.name, size: Size.shopMarkHeader)), title: shop.name,
                            sub: [shop.branch, shop.id == app.catalog.lastShopId ? "last used" : nil].compactMap { $0 }.joined(separator: " · "),
                            on: selectedId == shop.id) { choose(shop.id) }
                    }
                    Button {
                        trays.present("new-shop") { ShopFormTray(shop: nil) { saved in choose(saved.id) } }
                    } label: {
                        HStack(spacing: Space.l) {
                            RoundedRectangle.continuous(Radius.m)
                                .strokeBorder(Palette.ink3, style: StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
                                .frame(width: 34, height: 34)
                                .overlay(IconView(.plus, size: 15, weight: .semibold))
                            TypeStyle.rowTitle.text("Other shop")
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
            .frame(maxHeight: 340)
            .scrollBounceBehavior(.basedOnSize)
        }
    }

    private var orderedShops: [Shop] {
        let last = app.catalog.lastShopId
        return app.catalog.shops.sorted { a, b in
            if a.id == last { return true }
            if b.id == last { return false }
            return a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
        }
    }

    private func choose(_ shopId: Int64?) {
        if let target { model.setShop(shopId, for: target) } else { model.setBatchShop(shopId) }
        trays.dismiss()
    }

    private func row(mark: AnyView, title: String, sub: String, on: Bool, action: @escaping () -> Void) -> some View {
        Button {
            Haptics.play(.selection)
            action()
        } label: {
            HStack(spacing: Space.l) {
                mark
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text(title).foregroundStyle(Palette.ink)
                    if !sub.isEmpty { TypeStyle.rowSub.text(sub).foregroundStyle(Palette.ink2) }
                }
                Spacer()
                ZStack {
                    Circle().strokeBorder(Palette.ink3, lineWidth: 1.5).opacity(on ? 0 : 1)
                    Circle().fill(Accent.blue).opacity(on ? 1 : 0)
                    Image(icon: .check).font(.system(size: 10, weight: .heavy)).foregroundStyle(.white).opacity(on ? 1 : 0)
                }
                .frame(width: 22, height: 22)
            }
            .frame(height: 52)
            .hairline()
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: nil))
    }
}

// MARK: - Date for all

struct ImportDateAllTray: View {
    let model: ImportModel
    @Environment(TrayCoordinator.self) private var trays
    @State private var picked = Date()

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: "Date for all receipts")
            DatePicker("", selection: $picked, in: ...Date.now)
                .datePickerStyle(.graphical)
                .labelsHidden()
                .tint(Accent.blue)
                .environment(\.timeZone, Dates.timeZone)
            HStack(spacing: Space.m) {
                Button("From receipt") {
                    model.setDateForAll(.fromReceipt)
                    trays.dismiss()
                }
                .buttonStyle(.shop(.secondary))
                Button("Use this date") {
                    model.setDateForAll(.picked(picked))
                    trays.dismiss()
                }
                .buttonStyle(.shop())
            }
        }
    }
}

// MARK: - S5 · Receipt details tray

struct ImportDetailsTray: View {
    let model: ImportModel
    let receiptId: UUID
    let fallbackShopId: Int64?
    let onAdjust: (UUID) -> Void

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var mode: DateMode = .fromReceipt
    @State private var picked = Date()

    enum DateMode: Hashable { case fromReceipt, photo, pick }

    private var receipt: ImportModel.Receipt? { model.receipt(receiptId) }
    private var photoDate: Date? { receipt?.pages.first?.takenAt }

    var body: some View {
        if let receipt {
            VStack(alignment: .leading, spacing: Space.lx) {
                TrayHeader(title: "Receipt \((model.receipts.firstIndex { $0.id == receiptId } ?? 0) + 1)",
                           subtitle: receipt.pages.count == 1 ? "1 page" : "\(receipt.pages.count) pages")
                pages(receipt)
                shopRow(receipt)
                VStack(alignment: .leading, spacing: 8) {
                    SegmentedControl(options: DateMode.allCases(hasPhoto: photoDate != nil), title: title(for:), selection: $mode)
                    caption
                    if mode == .pick {
                        DatePicker("", selection: $picked, in: ...Date.now)
                            .datePickerStyle(.graphical)
                            .labelsHidden()
                            .tint(Accent.blue)
                            .environment(\.timeZone, Dates.timeZone)
                    }
                }
                if receipt.pages.contains(where: { !$0.isPDFPage }) {
                    Button {
                        if let page = receipt.pages.first(where: { !$0.isPDFPage }) { onAdjust(page.id) }
                    } label: {
                        HStack {
                            TypeStyle.input.sized(15).text("Straighten & crop").foregroundStyle(Palette.ink)
                            Spacer()
                            TypeStyle.body.text(straightenValue(receipt)).foregroundStyle(Palette.ink2)
                            IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                        }
                        .padding(.horizontal, Space.lx)
                        .frame(height: 48)
                        .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
                    }
                    .buttonStyle(.press(haptic: .selection))
                }
                Button("Done") { trays.dismiss() }
                    .buttonStyle(.shop())
                Button("Remove this receipt") {
                    Haptics.play(.soft)
                    trays.dismiss()
                    model.remove(receiptId)
                }
                .textStyle(.label)
                .foregroundStyle(Palette.redText)
                .frame(maxWidth: .infinity)
                .buttonStyle(.press(haptic: nil))
            }
            .onAppear { syncFromModel(receipt) }
            .onChange(of: mode) { _, new in apply(new) }
            .onChange(of: picked) { _, _ in if mode == .pick { apply(.pick) } }
        }
    }

    private func pages(_ receipt: ImportModel.Receipt) -> some View {
        ScrollView(.horizontal) {
            HStack(spacing: Space.m) {
                ForEach(Array(receipt.pages.enumerated()), id: \.element.id) { index, page in
                    ZStack(alignment: .topTrailing) {
                        Image(uiImage: page.thumbnail)
                            .resizable().scaledToFill()
                            .frame(width: 58, height: 80)
                            .clipShape(RoundedRectangle.continuous(8))
                            .overlay(RoundedRectangle.continuous(8).strokeBorder(Palette.line))
                        if receipt.pages.count > 1 {
                            Button {
                                model.removePage(page.id, from: receiptId)
                            } label: {
                                IconView(.close, size: 9, weight: .bold)
                                    .foregroundStyle(.white)
                                    .frame(width: 18, height: 18)
                                    .background(Color.black.opacity(0.65), in: Circle())
                            }
                            .buttonStyle(.press(haptic: .soft))
                            .padding(3)
                            .accessibilityLabel("Remove page \(index + 1)")
                        }
                    }
                    .contextMenu {
                        if index > 0 { Button("Move earlier", systemImage: "arrow.left") { model.movePage(in: receiptId, from: IndexSet(integer: index), to: index - 1) } }
                        if index < receipt.pages.count - 1 { Button("Move later", systemImage: "arrow.right") { model.movePage(in: receiptId, from: IndexSet(integer: index), to: index + 2) } }
                    }
                }
            }
            .padding(.vertical, 2)
        }
        .scrollIndicators(.hidden)
    }

    private func shopRow(_ receipt: ImportModel.Receipt) -> some View {
        Button {
            trays.present("import-shop") { ImportShopTray(model: model, target: receiptId, fallbackShopId: fallbackShopId) }
        } label: {
            HStack(spacing: Space.ml) {
                TypeStyle.input.sized(15).text("Shop").foregroundStyle(Palette.ink)
                Spacer()
                if let shop = app.catalog.shop(model.effectiveShopId(receipt, fallback: fallbackShopId)) {
                    ShopMark(name: shop.name, size: 20)
                    TypeStyle.body.text(shop.displayName).foregroundStyle(Palette.ink2).lineLimit(1)
                } else {
                    TypeStyle.body.text("Pick a shop").foregroundStyle(Palette.amberText)
                }
                IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
            }
            .padding(.horizontal, Space.lx)
            .frame(height: 48)
            .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
        }
        .buttonStyle(.press(haptic: .selection))
    }

    @ViewBuilder private var caption: some View {
        switch mode {
        case .fromReceipt:
            TypeStyle.rowSub.text("Shop reads the date printed on it. If there isn't one, you'll be asked later.").foregroundStyle(Palette.ink2)
        case .photo:
            TypeStyle.rowSub.text("The day you took this photo. It can differ from the day you shopped.").foregroundStyle(Palette.ink2)
        case .pick:
            EmptyView()
        }
    }

    private func title(for mode: DateMode) -> String {
        switch mode {
        case .fromReceipt: "From receipt"
        case .photo: photoDate.map { "Photo · \(Dates.shortDay($0))" } ?? "Photo date"
        case .pick: "Pick…"
        }
    }

    private func straightenValue(_ receipt: ImportModel.Receipt) -> String {
        let states = receipt.pages.filter { !$0.isPDFPage }.map(\.straighten)
        if states.contains(.adjusted) { return "Adjusted" }
        if states.allSatisfy({ $0 == .original }) { return "Original" }
        return "Automatic"
    }

    private func syncFromModel(_ receipt: ImportModel.Receipt) {
        switch receipt.date {
        case .fromReceipt: mode = .fromReceipt
        case .photo: mode = .photo
        case .picked(let d): mode = .pick; picked = d
        }
    }

    private func apply(_ new: DateMode) {
        switch new {
        case .fromReceipt: model.setDate(.fromReceipt, for: receiptId)
        case .photo: model.setDate(.photo, for: receiptId)
        case .pick: model.setDate(.picked(picked), for: receiptId)
        }
    }
}

extension ImportDetailsTray.DateMode {
    static func allCases(hasPhoto: Bool) -> [ImportDetailsTray.DateMode] {
        hasPhoto ? [.fromReceipt, .photo, .pick] : [.fromReceipt, .pick]
    }
}
