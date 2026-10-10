import SwiftUI

/// H · Home: spent this month vs last month, category bar, receipts that need attention,
/// then every receipt by day. H-Empty is the same screen with nothing read yet.
struct HomeView: View {
    @Environment(AppModel.self) private var app

    private var home: HomeModel { app.home }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                HomeHeader(initial: String(app.deviceName.prefix(1)), onSettings: { app.push(.settings) }, onSearch: { app.push(.search) }) {
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
                }

                if home.attentionCount > 0 && !home.filter.isActive {
                    AttentionCard(count: home.attentionCount, title: "Receipts to review", hint: home.attentionHint) {
                        if let first = home.attention.first { app.push(.receipt(first.id)) }
                    }
                    .padding(.horizontal, Space.edge)
                    .padding(.top, 18)
                }

                inbox
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, 18)
            }
            .padding(.bottom, Space.tabBarClearance)
        }
        .scrollIndicators(.hidden)
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
                Text("Spent in \(Dates.month(.now))")
                    .textStyle(TypeStyle.rowTitle)
                    .foregroundStyle(Palette.ink2)
                HeroAmount(cents: home.monthSpend?.totalCents ?? 0, color: hasSpend ? Palette.ink : Palette.ink3)
                Group {
                    if let delta = home.monthDeltaCents, hasSpend {
                        DeltaChip(deltaCents: delta, caption: "\(delta <= 0 ? "less" : "more") than this time in \(Dates.month(Dates.adding(.month, -1, to: .now)))")
                    } else {
                        Text("Totals show up once a receipt is read.")
                            .textStyle(TypeStyle.body.sized(13.5))
                            .foregroundStyle(Palette.ink2)
                    }
                }
                .padding(.top, Space.xs)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: nil))
        .disabled(!hasSpend)
        .accessibilityHint("Opens Analytics")
    }

    private var hasSpend: Bool { (home.monthSpend?.totalCents ?? 0) > 0 }

    // MARK: Inbox

    @ViewBuilder
    private var inbox: some View {
        VStack(alignment: .leading, spacing: 0) {
            if !app.uploads.pending.isEmpty {
                DaySectionHeader(title: "Today", trailing: "On this iPhone")
                ForEach(app.uploads.pending) { item in
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
                }
            }

            filterBar

            if home.receipts.isEmpty {
                if home.hasLoaded || home.error != nil || !app.uploads.pending.isEmpty {
                    emptyState
                } else {
                    LoadingState()
                }
            } else {
                ForEach(Array(home.dayGroups.enumerated()), id: \.element.id) { index, group in
                    DaySectionHeader(title: group.title, trailing: group.totalCents > 0 ? Money.format(group.totalCents) : nil)
                    ForEach(group.receipts) { receipt in
                        receiptRow(receipt)
                            .staggeredAppear(index: index)
                    }
                }
                if home.nextCursor != nil {
                    LoadingState()
                        .onAppear { Task { await home.loadMore() } }
                }
            }
        }
    }

    @ViewBuilder
    private var filterBar: some View {
        if home.hasLoaded && (!home.receipts.isEmpty || home.filter.isActive) {
            HStack(spacing: Space.s) {
                if home.filter.isActive {
                    Text(filterTitle).textStyle(.label).foregroundStyle(Palette.ink2)
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
                        Text("Filter").textStyle(TypeStyle.label)
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
        let p = receipt.rowPresentation()
        return Button {
            app.push(.receipt(receipt.id))
        } label: {
            ReceiptRow(
                shopName: app.catalog.shop(receipt.shopId)?.name ?? "Shop",
                subtitle: p.subtitle,
                subtitleColor: p.subtitleColor,
                amount: p.amount,
                amountColor: p.amountColor,
                showsSpinner: p.spinner
            )
        }
        .buttonStyle(.press(haptic: nil))
        .contextMenu {
            if receipt.status == .needsReview {
                Button("Review", systemImage: "checkmark.circle") { app.push(.receipt(receipt.id)) }
            }
        }
    }

    private func uploadSubtitle(_ item: UploadItem) -> String {
        let pages = item.pages == 1 ? "1 page" : "\(item.pages) pages"
        switch item.state {
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
        } else if let error = home.error, !error.isConnectivity {
            ErrorState(message: error.message) { Task { await home.refresh() } }
        } else {
            EmptyState(
                title: "Scan receipts on the day",
                message: "Thermal paper fades in weeks. Scan now and review whenever you like. Scans upload as soon as you're back online."
            )
            .padding(.top, 40)
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
                Text("Status").textStyle(.label).foregroundStyle(Palette.ink2)
                FlowLayout(spacing: 6, lineSpacing: 6) {
                    ForEach(statuses, id: \.self) { s in
                        Chip(title: s?.title ?? "All", isSelected: status == s, size: .large, idleBackground: Palette.trayField) { status = s }
                    }
                }
            }
            VStack(alignment: .leading, spacing: Space.m) {
                Text("Shop").textStyle(.label).foregroundStyle(Palette.ink2)
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
