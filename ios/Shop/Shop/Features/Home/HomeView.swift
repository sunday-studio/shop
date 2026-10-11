import SwiftUI

/// H · Home: spent this month vs last month, category bar, receipts that need attention,
/// then every receipt by day. H-Empty is the same screen with nothing read yet.
struct HomeView: View {
    @Environment(AppModel.self) private var app

    private var home: HomeModel { app.home }

    /// A typical bar: six categories, two legend rows.
    private static let placeholderShares = ["Dairy", "Produce", "Meat", "Pantry", "Bakery", "Other"].map {
        CategoryShare(name: $0, cents: 2_000, color: Palette.neutral)
    }

    var body: some View {
        List {
            VStack(alignment: .leading, spacing: 0) {
                HomeHeader(showsStatus: app.syncStatus != nil, onSettings: { app.push(.settings) }, onSearch: { app.push(.search) }) {
                    if let status = app.syncStatus {
                        SyncPill(title: status.title, dot: status.dot, isBusy: status.busy) { app.push(status.route) }
                    }
                }
                .animation(Motion.ease, value: app.syncStatus)

                spendBlock
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, 22)

                if !home.categoryShares.isEmpty {
                    CategoryBar(shares: home.categoryShares)
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, Space.xxl)
                } else if !home.spendLoaded {
                    // Holds the bar and legend's height until the first stats land, so the
                    // review card and list don't drop when they arrive.
                    CategoryBar(shares: Self.placeholderShares)
                        .hidden()
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, Space.xxl)
                }

                progressCards
                    .padding(.horizontal, Space.edge)

                suggestionCards
                    .padding(.horizontal, Space.edge)

                if home.attentionCount > 0 && !home.filter.isActive {
                    AttentionCard(count: home.attentionCount, title: "Receipts to review", hint: home.attentionHint) {
                        app.startReview(home.attention.map(\.id))
                    }
                    .padding(.horizontal, Space.edge)
                    .padding(.top, 18)
                }

            }
            .shopListRow()

            inbox
        }
        .shopListStyle()
        .contentMargins(.bottom, Space.tabBarClearance, for: .scrollContent)
        .onAppear { app.reviewSequence = [] }
        .transaction(value: home.spendLoaded) { $0.animation = nil }
        .refreshable {
            app.uploads.kick(force: true)
            await home.refresh()
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
    }

    // MARK: Spend

    private var spendBlock: some View {
        Button {
            app.tab = .analytics
        } label: {
            VStack(alignment: .leading, spacing: 6) {
                TypeStyle.rowTitle.text("Spent in \(Dates.month(.now))")
                    .foregroundStyle(Palette.ink2)
                if home.spendLoaded && home.spendUnavailable {
                    // No figure yet and the request failed (offline): say so instead of €0,00.
                    HeroAmount(cents: 0).hidden()
                        .overlay(alignment: .leading) {
                            TypeStyle.body.sized(15).text("Totals appear once you're online.")
                                .foregroundStyle(Palette.ink2)
                        }
                    TypeStyle.body.sized(13.5).text(" ").padding(.top, Space.xs)
                } else if home.spendLoaded {
                    HeroAmount(cents: home.monthSpend?.totalCents ?? 0, color: hasSpend ? Palette.ink : Palette.ink3)
                    Group {
                        if home.spendIsStale, let updated = home.spendUpdatedAt {
                            TypeStyle.body.sized(13.5).text("Last updated \(Dates.relativeStamp(updated))")
                                .foregroundStyle(Palette.ink3)
                        } else if let delta = home.monthDeltaCents, hasSpend {
                            DeltaChip(deltaCents: delta, caption: "\(delta <= 0 ? "less" : "more") than this time in \(Dates.month(Dates.adding(.month, -1, to: .now)))")
                        } else {
                            TypeStyle.body.sized(13.5).text("Totals show up once a receipt is read.")
                                .foregroundStyle(Palette.ink2)
                        }
                    }
                    .padding(.top, Space.xs)
                } else {
                    // Keeps the layout while the first stats load, so the real total appears
                    // without a €0,00 flash or a number roll.
                    HeroAmount(cents: 0).hidden()
                    TypeStyle.body.sized(13.5).text("Totals show up once a receipt is read.")
                        .hidden()
                        .padding(.top, Space.xs)
                }
            }
            .animation(nil, value: home.spendLoaded)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: nil))
        .disabled(!hasSpend)
        .accessibilityHint("Opens Analytics")
    }

    private var hasSpend: Bool { (home.monthSpend?.totalCents ?? 0) > 0 }

    // MARK: Shop suggestions

    @ViewBuilder
    private var suggestionCards: some View {
        let groups = home.suggestionGroups
        VStack(spacing: Space.m) {
            ForEach(groups) { group in
                AttentionCard(
                    count: group.count,
                    title: group.suggestion.kind == .new ? "New shop: \(group.suggestion.name)" : "Looks like \(group.suggestion.displayName(in: app.catalog))",
                    hint: group.suggestion.kind == .new ? "Add" : "Move",
                    tone: .blue
                ) { app.push(.receipt(group.firstReceiptId)) }
                .transition(.opacity)
            }
        }
        .padding(.top, groups.isEmpty ? 0 : 18)
        .animation(Motion.ease, value: groups.map(\.id))
    }

    // MARK: Upload progress

    private var onlineNow: Bool { app.network.isOnline }

    @ViewBuilder
    private var progressCards: some View {
        let batches = app.uploads.summaries(online: onlineNow)
        VStack(spacing: Space.m) {
            ForEach(batches) { batch in
                UploadProgressCard(state: cardState(batch), onOpen: { open(batch) }, onRetry: { app.uploads.retryFailed(in: batch.id) })
                    .transition(.opacity.combined(with: .offset(y: -8)))
            }
        }
        .padding(.top, batches.isEmpty ? 0 : 18)
        .animation(Motion.ease, value: batches.map(\.id))
    }

    private func cardState(_ batch: UploadQueue.BatchSummary) -> UploadProgressCard.State {
        switch batch.phase {
        case .preparing: .preparing(done: batch.prepared, total: batch.total)
        case .uploading: .uploading(done: batch.uploaded, total: batch.total)
        case .offline: .offline(waiting: batch.total - batch.uploaded)
        case .partial: .partial(uploaded: batch.uploaded, failed: batch.failed)
        case .done: .done(total: batch.total)
        }
    }

    private func open(_ batch: UploadQueue.BatchSummary) {
        if batch.phase == .done {
            home.setFilter(.init(status: .processing))
        } else {
            app.push(.uploadQueue)
        }
    }

    // MARK: Inbox

    @ViewBuilder
    private var inbox: some View {
        let carded = app.uploads.cardBatchIds(online: onlineNow)
        let pendingRows = app.uploads.pending.filter { $0.batchId == nil || !carded.contains($0.batchId!) }
        if !pendingRows.isEmpty {
            DaySectionHeader(title: "Today", trailing: "On this iPhone")
                .padding(.horizontal, Space.gutter)
                .padding(.top, 18)
                .shopListRow()
            ForEach(pendingRows) { item in
                Button { app.push(.uploadQueue) } label: {
                    ReceiptRow(
                        shopName: item.shopName,
                        subtitle: uploadSubtitle(item),
                        subtitleColor: item.state == .failed ? Palette.redText : Palette.ink2,
                        amount: "",
                        showsSpinner: item.state == .uploading,
                        trailingIcon: item.state == .failed ? .retry : .upload
                    )
                }
                .buttonStyle(.press(haptic: nil))
                .padding(.horizontal, Space.gutter)
                .shopListRow()
            }
        }

        filterBar
            .padding(.horizontal, Space.gutter)
            .padding(.top, pendingRows.isEmpty ? 18 : 0)
            .shopListRow()

        if let error = home.error, !home.receipts.isEmpty {
            StaleBanner(what: "receipts", offline: error.isConnectivity) { Task { await home.refresh() } }
                .padding(.horizontal, Space.edge)
                .padding(.top, Space.l)
                .shopListRow()
        }

        if homePhase == .content {
            ForEach(Array(home.dayGroups.enumerated()), id: \.element.id) { index, group in
                DaySectionHeader(title: group.title, trailing: group.totalCents > 0 ? Money.format(group.totalCents) : nil)
                    .padding(.horizontal, Space.gutter)
                    .shopListRow()
                ForEach(group.receipts) { receipt in
                    receiptRow(receipt)
                        .staggeredAppear(index: index)
                }
            }
            if home.nextCursor != nil {
                LoadMoreFooter(state: .loading, retry: {})
                    .onAppear { Task { await home.loadMore() } }
                    .shopListRow()
            }
        } else {
            StatefulContent(phase: homePhase, retry: { Task { await home.refresh() } }) {
                SkeletonRows { SkeletonReceiptRow() }
            } empty: {
                emptyState
            } content: {
                EmptyView()
            }
            .padding(.horizontal, Space.gutter)
            .padding(.top, Space.l)
            .shopListRow()
        }
    }

    /// Cached receipts count as content; the skeleton only shows when there's nothing to show yet.
    private var homePhase: ScreenPhase {
        if !home.receipts.isEmpty { return .content }
        if let error = home.error { return app.uploads.pending.isEmpty ? .failed(error.message) : .empty }
        if home.hasLoaded || !app.uploads.pending.isEmpty { return .empty }
        return .loading
    }

    @ViewBuilder
    private var filterBar: some View {
        if home.hasLoaded && (!home.receipts.isEmpty || home.filter.isActive) {
            HStack(spacing: Space.s) {
                if home.filter.isActive {
                    TypeStyle.label.text(filterTitle).foregroundStyle(Palette.ink2)
                    Spacer()
                    Button("Clear") { home.setFilter(.init()) }
                        .textStyle(.label)
                        .foregroundStyle(Palette.blueText)
                        .buttonStyle(.press(haptic: .selection))
                } else {
                    Spacer()
                }
                Button {
                    app.trays.present("home-filter") { HomeFilterTray() }
                } label: {
                    HStack(spacing: 5) {
                        IconView(.filter, size: 14, weight: .semibold)
                        TypeStyle.label.text("Filter")
                    }
                    .foregroundStyle(home.filter.isActive ? Palette.btnInk : Palette.ink2)
                    .padding(.horizontal, Space.ml)
                    .frame(height: 28)
                    .background(home.filter.isActive ? Palette.btn : Palette.sunk, in: Capsule())
                }
                .buttonStyle(.pill)
            }
            .padding(.top, Space.xs)
        }
    }

    private var filterTitle: String {
        var parts: [String] = []
        if let s = home.filter.status { parts.append(s.title) }
        if let id = home.filter.shopId { parts.append(app.catalog.shopName(id)) }
        return parts.joined(separator: " · ")
    }

    private func receiptRow(_ receipt: Receipt) -> some View {
        let canConfirm = receipt.confirmedAt == nil && (receipt.status == .ok || receipt.status == .needsReview)
        let busy = receipt.status == .processing || receipt.status == .pending
        return SwipeActionRow(
            leading: canConfirm ? SwipeAction(icon: "checkmark", title: "Confirm", tint: Accent.green, removesRow: true, haptic: .medium) { confirm(receipt) } : nil,
            trailing: busy ? nil : SwipeAction(icon: "trash", title: "Delete", tint: Accent.red) { askDelete(receipt) }
        ) {
            receiptRowContent(receipt)
                .padding(.horizontal, Space.gutter)
        }
        .shopListRow()
    }

    private func confirm(_ receipt: Receipt) {
        Task {
            do {
                let confirmed = try await app.api.confirmReceipt(receipt.id)
                app.home.apply(confirmed)
                Haptics.play(.success)
                app.toasts.show("Receipt confirmed", tone: .green)
                await app.home.refresh()
            } catch let error as APIError {
                Haptics.play(.error)
                app.toasts.error(error.message)
            } catch {}
        }
    }

    private func askDelete(_ receipt: Receipt) {
        let name = receipt.shopId == nil ? "this receipt" : "the \(app.catalog.shopName(receipt.shopId)) receipt"
        app.trays.present("delete-receipt") {
            ConfirmTray(title: "Delete \(name)?", message: "The photo and its lines are removed. This can't be undone.", confirmTitle: "Delete", destructive: true) {
                do {
                    try await app.api.deleteReceipt(receipt.id)
                    return true
                } catch let error as APIError {
                    app.toasts.error(error.message)
                    return false
                } catch { return false }
            } afterDismiss: {
                app.home.remove(receipt.id)
                Haptics.play(.warning)
                Task { await app.home.refresh() }
            }
        }
    }

    private func receiptRowContent(_ receipt: Receipt) -> some View {
        var p = receipt.rowPresentation()
        let unknownShop = receipt.shopId == nil
        if unknownShop && (receipt.status == .ok || receipt.status == .needsReview) {
            if let suggestion = home.suggestions[receipt.id] {
                p.subtitle = "Looks like \(suggestion.displayName(in: app.catalog)) · tap to add"
                p.subtitleColor = Palette.blueText
            } else {
                p.subtitle = "Pick a shop"
                p.subtitleColor = Palette.amberText
            }
        }
        return Button {
            app.push(.receipt(receipt.id))
        } label: {
            ReceiptRow(
                shopName: app.catalog.shop(receipt.shopId)?.name ?? (unknownShop ? "Unknown shop" : "Shop"),
                subtitle: p.subtitle,
                subtitleColor: p.subtitleColor,
                amount: p.amount,
                amountColor: p.amountColor,
                showsSpinner: p.spinner,
                isUnknownShop: unknownShop
            )
        }
        .buttonStyle(.press(haptic: nil))
        .contextMenu {
            if receipt.status == .needsReview {
                Button("Review", systemImage: "checkmark.circle") { app.push(.receipt(receipt.id)) }
            }
            if receipt.confirmedAt == nil && (receipt.status == .ok || receipt.status == .needsReview) {
                Button("Confirm", systemImage: "checkmark") { confirm(receipt) }
            }
            if receipt.status != .processing && receipt.status != .pending {
                Button("Delete", systemImage: "trash", role: .destructive) { askDelete(receipt) }
            }
        }
    }

    private func uploadSubtitle(_ item: UploadItem) -> String {
        let pages = item.pages == 1 ? "1 page" : "\(item.pages) pages"
        switch item.state {
        case .preparing: return "\(pages) · preparing"
        case .waiting:
            return app.network.isOnline ? "\(pages) · waiting to upload" : "\(pages) · uploads when online"
        case .uploading: return "\(pages) · uploading"
        case .failed: return item.lastError ?? "Upload failed · tap to retry"
        case .done: return "Uploaded"
        }
    }

    @ViewBuilder
    private var emptyState: some View {
        if home.filter.isActive {
            EmptyState(title: "Nothing matches", message: "No receipts with this filter.", action: (title: "Clear filter", run: { home.setFilter(.init()) })) {
                GlyphIllustration(icon: .filter, color: Accent.purple)
            }
            .padding(.top, 40)
        } else {
            EmptyState(
                title: "Scan receipts on the day",
                message: "Thermal paper fades in weeks. Scan now and review whenever you like. Scans upload as soon as you're back online."
            )
            .padding(.top, 40)
            Button("Import receipts from Photos") { app.openImportSources() }
                .textStyle(.label)
                .foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: .selection))
                .frame(maxWidth: .infinity)
                .padding(.top, Space.m)
        }
    }
}

// MARK: - H.2 Filter

struct HomeFilterTray: View {
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var status: ReceiptStatus?
    @State private var shopId: Int64?
    @State private var loaded = false

    private let statuses: [ReceiptStatus?] = [nil, .needsReview, .processing, .failed, .ok]

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            TrayHeader(title: "Filter receipts")
            VStack(alignment: .leading, spacing: Space.m) {
                TypeStyle.label.text("Status").foregroundStyle(Palette.ink2)
                FlowLayout(spacing: 6, lineSpacing: 6) {
                    ForEach(statuses, id: \.self) { s in
                        Chip(title: s?.title ?? "All", isSelected: status == s, size: .large, idleBackground: Palette.trayField) { status = s }
                    }
                }
            }
            VStack(alignment: .leading, spacing: Space.m) {
                TypeStyle.label.text("Shop").foregroundStyle(Palette.ink2)
                FlowLayout(spacing: 6, lineSpacing: 6) {
                    Chip(title: "Any shop", isSelected: shopId == nil, size: .large, idleBackground: Palette.trayField) { shopId = nil }
                    ForEach(app.catalog.shops) { shop in
                        Chip(title: shop.name, isSelected: shopId == shop.id, size: .large, idleBackground: Palette.trayField) { shopId = shop.id }
                    }
                }
            }
            Button("Show receipts") {
                app.home.setFilter(.init(status: status, shopId: shopId))
                trays.dismiss()
            }
            .buttonStyle(.shop())
        }
        .onAppear {
            guard !loaded else { return }
            status = app.home.filter.status
            shopId = app.home.filter.shopId
            loaded = true
        }
    }
}

extension ReceiptStatus {
    var title: String {
        switch self {
        case .pending: "Waiting"
        case .processing: "Processing"
        case .needsReview: "Needs review"
        case .ok: "Done"
        case .failed: "Failed"
        }
    }

    var tone: Tone {
        switch self {
        case .pending, .processing: .blue
        case .needsReview: .amber
        case .ok: .green
        case .failed: .red
        }
    }
}

#Preview("Home") {
    let app = AppModel(mock: true)
    return HomeView()
        .environment(app)
        .task { await app.home.refresh() }
}

#Preview("Home · dark") {
    let app = AppModel(mock: true)
    return HomeView()
        .environment(app)
        .task { await app.home.refresh() }
        .preferredColorScheme(.dark)
}
