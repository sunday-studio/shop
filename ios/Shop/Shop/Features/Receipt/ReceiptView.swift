import SwiftUI

/// H.1 · Receipt: image thumbnail and header on top, lines below, a sticky Σ-vs-total bar
/// with Confirm (always enabled). A receipt that needs attention opens straight into this.
struct ReceiptView: View {
    let receiptId: Int64
    @Environment(AppModel.self) private var app

    var body: some View {
        // The model is built before the first frame so the pushed screen opens with the summary
        // Home already has instead of a blank or a placeholder.
        ReceiptScreen(receiptId: receiptId, app: app)
            .id(receiptId)
    }
}

private struct ReceiptScreen: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var model: ReceiptModel
    /// The screen opened on a summary (no lines): its first fill swaps skeleton rows for real
    /// ones in place, without the stagger.
    @State private var openedOnSummary: Bool
    /// The first-paint stagger only plays for the first second; rows built while scrolling appear at once.
    @State private var staggerOn = true

    init(receiptId: Int64, app: AppModel) {
        let model = ReceiptModel(receiptId: receiptId, app: app)
        _model = State(initialValue: model)
        _openedOnSummary = State(initialValue: model.receipt != nil && model.receipt?.lines == nil)
    }

    var body: some View {
        content(model)
            .background(Palette.bg)
            .task { await model.load() }
            .task {
                try? await Task.sleep(for: .milliseconds(1200))
                staggerOn = false
            }
            .onChange(of: app.network.isOnline) { _, online in
                if online && model.receipt?.lines == nil { Task { await model.load() } }
            }
            .onDisappear { model.stop() }
    }

    @ViewBuilder
    private func content(_ model: ReceiptModel) -> some View {
        ZStack(alignment: .bottom) {
            List {
                VStack(alignment: .leading, spacing: 0) {
                    NavHeader(title: reviewTitle(model)) {
                        IconButton(icon: .more, label: "More actions") { openActions(model) }
                            .disabled(model.receipt == nil)
                    }

                    if let receipt = model.receipt {
                        header(model, receipt)
                            .padding(.horizontal, Space.gutter)
                            .padding(.top, Space.xxl)

                        if let suggestion = model.suggestion {
                            ShopSuggestionCard(
                                kind: suggestion.kind == .existing ? .existing : .new,
                                name: suggestion.displayName(in: app.catalog), detail: suggestion.displayDetail(in: app.catalog), waiting: suggestion.receiptCount,
                                isWorking: model.isAcceptingSuggestion,
                                onAccept: { Task { await model.acceptSuggestion() } },
                                onEdit: { editSuggestion(suggestion, model: model) },
                                onNotNow: { model.dismissSuggestion() }
                            )
                            .disabled(!app.network.isOnline)
                            .padding(.horizontal, Space.edge)
                            .padding(.top, 18)
                            .transition(.opacity)
                            if !app.network.isOnline {
                                TypeStyle.rowSub.text("Needs a connection").foregroundStyle(Palette.ink2)
                                    .padding(.horizontal, Space.edge + Space.xs).padding(.top, Space.s)
                            }
                        }

                        if let duplicate = model.duplicate {
                            DuplicateBannerSection(model: model, duplicate: duplicate) {
                                if !app.advanceReview(after: model.receiptId) { dismiss() }
                            }
                            .padding(.horizontal, Space.edge)
                            .padding(.top, 18)
                        }

                        // One banner per problem, each with its own single action.
                        if receipt.shopId == nil && model.suggestion == nil {
                            Banner(
                                tone: .amber, lead: "No shop yet.",
                                message: "Pick one so prices compare across shops.",
                                action: (title: "Pick a shop ›", run: { pickShop(model) })
                            )
                            .padding(.horizontal, Space.edge)
                            .padding(.top, 18)
                        }
                        if model.linesFailedToLoad {
                            Banner(tone: .amber, lead: "Couldn't load the lines.", message: "Confirm waits for them.",
                                   action: (title: "Try again ›", run: { Task { await model.load() } }))
                            .padding(.horizontal, Space.edge)
                            .padding(.top, 18)
                        }
                        if let banner = model.banner {
                            Banner(
                                tone: banner.tone, lead: banner.lead.isEmpty ? nil : banner.lead, message: banner.message,
                                action: banner.lead == ReceiptModel.noDateLead
                                    ? (title: "Set the date ›", run: { app.trays.present("no-date") { NoDateFixTray(model: model) } })
                                    : nil
                            )
                            .padding(.horizontal, Space.edge)
                            .padding(.top, 18)
                        }

                        if receipt.status == .processing || receipt.status == .pending {
                            processing
                        } else if receipt.lines == nil {
                            // Summary only: placeholder rows until the refresh brings the lines,
                            // swapped in place with no cross-fade.
                            linesSkeleton
                                .padding(.horizontal, Space.gutter)
                                .padding(.top, Space.xl)
                        } else if receipt.status == .failed && model.lines.isEmpty {
                            failedActions(model)
                        }
                    } else {
                        StatefulContent(phase: model.error.map { .failed($0.message) } ?? .loading, retry: { Task { await model.load() } }) {
                            VStack(alignment: .leading, spacing: Space.xl) {
                                SkeletonCard(height: 120)
                                SkeletonRows { SkeletonLineRow() }
                            }
                        } empty: {
                            EmptyView()
                        } content: {
                            EmptyView()
                        }
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, Space.xxl)
                    }
                }
                .shopListRow()

                if showsLines(model) { lineRows(model) }
            }
            .shopListStyle()
            .contentMargins(.bottom, 130, for: .scrollContent)
            .refreshable { await model.load() }

            // Needs review, or accepted but never confirmed (confirming checks list items and adds products).
            if let receipt = model.receipt, receipt.confirmedAt == nil, receipt.status == .needsReview || receipt.status == .ok {
                ConfirmBar(linesCents: model.countedCents, totalCents: receipt.totalCents, isWorking: model.isConfirming, isLoading: receipt.lines == nil) {
                    Task {
                        if await model.confirm() {
                            Haptics.play(.success)
                            if !app.advanceReview(after: model.receiptId) { dismiss() }
                        }
                    }
                }
                .padding(.horizontal, Space.trayInset)
                .padding(.bottom, Space.trayInset)
                .transition(.offset(y: 40).combined(with: .opacity))
            }
        }
        .animation(Motion.ease, value: model.receipt?.status)
        .animation(nil, value: model.receipt?.lines == nil)
        .toolbar(.hidden, for: .navigationBar)
    }

    // MARK: Header

    private func header(_ model: ReceiptModel, _ receipt: Receipt) -> some View {
        HStack(alignment: .bottom, spacing: Space.l) {
            VStack(alignment: .leading, spacing: Space.m) {
                Button { pickShop(model) } label: {
                    HStack(spacing: Space.m) {
                        if receipt.shopId == nil {
                            UnknownShopMark(size: 22)
                            TypeStyle.rowTitle.sized(14).text("Unknown shop").lineLimit(1)
                        } else {
                            ShopMark(name: model.shop?.name ?? "Shop", size: 22)
                            TypeStyle.rowTitle.sized(14).text(model.shop?.displayName ?? "Shop").lineLimit(1)
                        }
                        IconView(.down, size: 10, weight: .bold)
                            .foregroundStyle(receipt.shopId == nil ? Palette.amberText : Palette.ink3)
                    }
                    .foregroundStyle(receipt.shopId == nil ? Palette.amberText : Palette.ink)
                    .padding(.horizontal, Space.m)
                    .frame(height: 32)
                    .background(receipt.shopId == nil ? Palette.amberTint : Palette.sunk, in: RoundedRectangle.continuous(Radius.m))
                }
                .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
                .animation(Motion.fade, value: receipt.shopId)
                if let total = receipt.totalCents {
                    HeroAmount(cents: total, size: 48)
                } else {
                    TypeStyle.emptyTitle.sized(30).text("No total")
                        .foregroundStyle(Palette.ink3)
                }
                TypeStyle.label.withFace(.regular).text(model.metaLine)
                    .foregroundStyle(Palette.ink2)
                listChecksRow(receipt.id)
            }
            Spacer(minLength: 0)
            Button {
                app.push(.receiptImage(receipt.id))
            } label: {
                ReceiptThumb(pages: 1, highlightIndex: model.toFix.isEmpty ? nil : 2)
            }
            .buttonStyle(.press(haptic: .light))
            .accessibilityLabel("Open receipt image")
        }
    }

    /// "Checked 4 items on your list · Undo": stays until the items are reverted or edited by hand.
    @ViewBuilder
    private func listChecksRow(_ receiptId: Int64) -> some View {
        let count = app.list.receiptCheckedItems(receiptId).count
        if count > 0 {
            HStack(spacing: Space.s) {
                TypeStyle.rowSub.text("Checked \(count == 1 ? "1 item" : "\(count) items") on your list")
                    .foregroundStyle(Palette.ink2)
                Button("Undo") {
                    Haptics.play(.light)
                    withAnimation(Motion.collapse) { app.list.undoReceiptChecks(receiptId) }
                    Task { await app.list.sync() }
                }
                .textStyle(TypeStyle.label.sized(13.5))
                .foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: nil))
            }
            .transition(.opacity)
        }
    }

    // MARK: Lines

    /// "Review 2 of 5" while going through Receipts to review, else "Receipt".
    private func reviewTitle(_ model: ReceiptModel) -> String {
        let seq = app.reviewSequence
        guard let i = seq.firstIndex(of: model.receiptId) else { return "Receipt" }
        return "Review \(i + 1) of \(seq.count)"
    }

    private func pickShop(_ model: ReceiptModel) {
        app.trays.present("receipt-shop") { ReceiptShopTray(model: model) }
    }

    private func editSuggestion(_ suggestion: ShopSuggestion, model: ReceiptModel) {
        app.trays.present("suggestion-edit") {
            SuggestionEditTray(suggestion: suggestion) { name, branch, domain in
                Task { await model.acceptSuggestion(name: name, branch: branch, domain: domain) }
            }
        }
    }

    private var linesSkeleton: some View {
        SkeletonRows { SkeletonLineRow() }
    }

    private func showsLines(_ model: ReceiptModel) -> Bool {
        guard let receipt = model.receipt, receipt.lines != nil else { return false }
        if receipt.status == .processing || receipt.status == .pending { return false }
        return !(receipt.status == .failed && model.lines.isEmpty)
    }

    @ViewBuilder
    private func lineRows(_ model: ReceiptModel) -> some View {
        @Bindable var model = model
        SectionLabel(title: "Lines") {
            HStack(spacing: 6) {
                Chip(title: "All \(model.lines.count)", isSelected: model.lineFilter == .all, size: .small) {
                    withAnimation(Motion.ease) { model.lineFilter = .all }
                }
                if !model.toFix.isEmpty || model.lineFilter == .toFix {
                    ToneChip(title: "To fix \(model.toFix.count)", tone: .amber, isSelected: model.lineFilter == .toFix) {
                        withAnimation(Motion.ease) { model.lineFilter = .toFix }
                    }
                }
            }
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.xl)
        .shopListRow()

        if model.visibleLines.isEmpty {
            TypeStyle.body.text(model.lineFilter == .toFix ? "Nothing left to fix." : "No lines were read.")
                .foregroundStyle(Palette.ink2)
                .padding(.vertical, Space.xxl)
                .padding(.horizontal, Space.gutter)
                .shopListRow()
        }

        ForEach(Array(model.visibleLines.enumerated()), id: \.element.id) { index, line in
            let p = model.present(line)
            SwipeActionRow(trailing: SwipeAction(icon: "trash", title: "Delete", tint: Accent.red) { askDeleteLine(line, model: model) }) {
            Button { tap(line, model: model) } label: {
                LineRow(title: p.title, isRaw: p.isRaw, subtitle: p.subtitle, subtitleColor: p.subtitleColor, amount: p.amount,
                        titleColor: p.titleColor, amountColor: p.amountColor, dot: p.dot, indented: p.indented)
            }
            .buttonStyle(.press(haptic: .selection))
            .staggeredAppear(index: openedOnSummary || !staggerOn ? Motion.staggerMax : index)
            .padding(.horizontal, Space.gutter)
            }
            .shopListRow()
            .contextMenu {
                Button("Edit line", systemImage: "pencil") { openEdit(line, model: model) }
                if line.kind == .item {
                    Button(line.productId == nil ? "Link product" : "Change product", systemImage: "link") { openLink(line, model: model) }
                }
                if line.kind == .item && line.productId != nil {
                    Button("This isn't the same product", systemImage: "square.split.2x1") {
                        Task { await model.splitProduct(of: line) }
                    }
                }
                Button("Delete line", systemImage: "trash", role: .destructive) {
                    askDeleteLine(line, model: model)
                }
            }
        }

        LinkButton(title: "Add line") { openEdit(nil, model: model) }
            .padding(.horizontal, Space.gutter)
            .shopListRow()
    }

    private var processing: some View {
        VStack(spacing: Space.l) {
            ProgressView().controlSize(.large).tint(Palette.ink2)
            TypeStyle.emptyTitle.text("Reading receipt…").foregroundStyle(Palette.ink)
            TypeStyle.body.sized(14.5).text("OCR and parsing on your server, usually 4–15 seconds. You can leave this screen.")
                .foregroundStyle(Palette.ink2)
                .multilineTextAlignment(.center)
        }
        .padding(.horizontal, 36)
        .padding(.top, 60)
        .frame(maxWidth: .infinity)
    }

    private func failedActions(_ model: ReceiptModel) -> some View {
        VStack(spacing: Space.m) {
            Button("Re-run OCR") {
                Task { _ = await model.rerun(ocr: true, force: true) }
            }
            .buttonStyle(.shop())
            Button("Delete and scan again") {
                Task {
                    if await model.delete() {
                        dismiss()
                        app.startCapture(.receipt)
                    }
                }
            }
            .buttonStyle(.shop(.destructive))
        }
        .padding(.horizontal, Space.gutter)
        .padding(.top, Space.xxl)
    }

    // MARK: Trays

    private func askDeleteLine(_ line: ReceiptLine, model: ReceiptModel) {
        app.trays.present("delete-line") {
            ConfirmTray(title: "Delete this line?", message: "\(model.present(line).title) · \(model.present(line).amount)", confirmTitle: "Delete line", destructive: true) {
                true
            } afterDismiss: {
                Task { await model.deleteLine(line.id) }
            }
        }
    }

    private func tap(_ line: ReceiptLine, model: ReceiptModel) {
        if line.kind == .item && line.productId == nil {
            openLink(line, model: model)
        } else {
            openEdit(line, model: model)
        }
    }

    private func openEdit(_ line: ReceiptLine?, model: ReceiptModel) {
        app.trays.present("edit-line-\(line?.id ?? 0)") {
            EditLineTray(model: model, line: line)
        }
    }

    private func openLink(_ line: ReceiptLine, model: ReceiptModel) {
        app.trays.present("link-\(line.id)") {
            LinkProductTray(model: model, line: line)
        }
    }

    private func openActions(_ model: ReceiptModel) {
        app.trays.present("receipt-actions") {
            ReceiptActionsTray(model: model, onDeleted: { if !app.advanceReview(after: model.receiptId) { dismiss() } })
        }
    }
}

#Preview("Receipt review") {
    let app = AppModel(mock: true)
    return NavigationStack {
        ReceiptView(receiptId: 101)
    }
    .environment(app)
    .trayHost(app.trays)
    .task { try? await app.catalog.refreshReference(); try? await app.catalog.refreshProducts() }
}

#Preview("Receipt review · dark") {
    let app = AppModel(mock: true)
    return NavigationStack {
        ReceiptView(receiptId: 101)
    }
    .environment(app)
    .trayHost(app.trays)
    .task { try? await app.catalog.refreshReference(); try? await app.catalog.refreshProducts() }
    .preferredColorScheme(.dark)
}


/// "Edit" on the suggestion card: fix the name, branch or website, then add the shop.
struct SuggestionEditTray: View {
    let suggestion: ShopSuggestion
    let onAdd: (String, String?, String?) -> Void

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var branch = ""
    @State private var website = ""
    @State private var loaded = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Add shop", subtitle: suggestion.kind == .existing ? "Moves to \(suggestion.displayName(in: app.catalog))" : nil)
            HStack(spacing: Space.l) {
                ShopMark(name: name.isEmpty ? "?" : name, size: 32)
                TypeStyle.rowSub.text("The logo comes from the website once the shop is added.").foregroundStyle(Palette.ink2)
            }
            .padding(.horizontal, Space.xs)
            Grid(horizontalSpacing: Space.m, verticalSpacing: Space.m) {
                GridRow {
                    FieldTile(label: "Name", text: $name, placeholder: "Aldi", background: Palette.trayField)
                    FieldTile(label: "Branch", text: $branch, placeholder: "Hoofdstraat 12", background: Palette.trayField)
                }
                GridRow {
                    FieldTile(label: "Website", text: $website, placeholder: "aldi.nl", mono: true, background: Palette.trayField)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .gridCellColumns(2)
                }
            }
            Button("Add \(name.isEmpty ? "shop" : name)") {
                func clean(_ s: String) -> String? {
                    let t = s.trimmingCharacters(in: .whitespaces)
                    return t.isEmpty ? nil : t
                }
                trays.dismiss()
                onAdd(name.trimmingCharacters(in: .whitespaces), clean(branch), clean(website))
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            name = suggestion.name
            branch = suggestion.branch ?? ""
            website = suggestion.domain ?? ""
        }
    }
}


/// "No date found" fix: use the upload day, or pick one. (A photo date joins them once the
/// server keeps `photo_taken_at`.)
struct NoDateFixTray: View {
    let model: ReceiptModel
    @Environment(TrayCoordinator.self) private var trays
    @State private var picked = Date()
    @State private var picking = false
    @State private var saving = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Receipt date", subtitle: "Pick a date so it counts in your totals.")
            if let uploaded = model.receipt?.createdAt {
                OptionRow(icon: .upload, title: "Use upload day", hint: "\(Dates.shortDay(uploaded)) · \(Dates.time(uploaded))") {
                    Task { await save(uploaded) }
                }
            }
            OptionRow(icon: .calendar, title: "Pick…", hint: "Choose the day you shopped") {
                withAnimation(Motion.spring) { picking.toggle() }
            }
            if picking {
                DatePicker("", selection: $picked, in: ...Date.now)
                    .datePickerStyle(.graphical)
                    .labelsHidden()
                    .tint(Accent.blue)
                    .environment(\.timeZone, Dates.timeZone)
                Button {
                    Task { await save(picked) }
                } label: {
                    LoadingLabel(title: "Use this date", isLoading: saving)
                }
                .buttonStyle(.shop())
                .disabled(saving)
            }
        }
    }

    private func save(_ date: Date) async {
        saving = true
        defer { saving = false }
        var patch = ReceiptPatch()
        patch.purchasedAt = date
        if await model.updateDetails(patch) {
            Haptics.play(.success)
            trays.dismiss()
        }
    }
}


/// "Pick a shop" for a receipt: the shop list, then "Other shop" to create a new one.
struct ReceiptShopTray: View {
    let model: ReceiptModel
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Which shop?")
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(orderedShops) { shop in
                        Button {
                            Haptics.play(.selection)
                            Task { await assign(shop.id) }
                        } label: {
                            HStack(spacing: Space.l) {
                                ShopMark(name: shop.name, size: Size.shopMarkHeader)
                                VStack(alignment: .leading, spacing: 1) {
                                    TypeStyle.rowTitle.text(shop.name).foregroundStyle(Palette.ink)
                                    if let branch = shop.branch, !branch.isEmpty {
                                        TypeStyle.rowSub.text(branch).foregroundStyle(Palette.ink2)
                                    }
                                }
                                Spacer()
                                if model.receipt?.shopId == shop.id {
                                    IconView(.check, size: 13, weight: .bold).foregroundStyle(Accent.blue)
                                }
                            }
                            .frame(height: 52)
                            .hairline()
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.press(haptic: .selection))
                        .disabled(working)
                    }
                    Button {
                        trays.present("new-shop") { ShopFormTray(shop: nil) { saved in Task { await assign(saved.id) } } }
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

    private func assign(_ shopId: Int64) async {
        working = true
        defer { working = false }
        var patch = ReceiptPatch()
        patch.shopId = shopId
        if await model.updateDetails(patch) {
            app.catalog.lastShopId = shopId
            trays.dismiss()
            app.toasts.show("Moved to \(app.catalog.shopName(shopId))")
        } else {
            Haptics.play(.error)
        }
    }
}
