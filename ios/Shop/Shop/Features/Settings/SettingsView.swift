import SwiftUI

/// S · Settings (from the avatar): server card, backup warning, management screens, preferences.
struct SettingsView: View {
    @Environment(AppModel.self) private var app
    @State private var hapticsOn = Haptics.isEnabled
    @State private var straightenOn = Straightener.isEnabled

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                NavHeader(title: "Settings")

                serverCard
                    .padding(.horizontal, Space.edge)
                    .padding(.top, 18)

                if app.backupIsStale {
                    NoticeRow(tone: .amber, title: "NAS backup is out of date") { app.push(.server) }
                        .padding(.horizontal, Space.edge)
                        .padding(.top, Space.m)
                }

                VStack(spacing: 0) {
                    Button { app.push(.uploadQueue) } label: {
                        SettingsRow(icon: .upload, color: Accent.blue, title: "Upload queue", value: queueValue)
                    }
                    Button { app.push(.shops) } label: {
                        SettingsRow(icon: .shop, color: Accent.orange, title: "Shops", value: "\(app.catalog.shops.count)")
                    }
                    Button { app.push(.categories) } label: {
                        SettingsRow(icon: .grid, color: Accent.pink, title: "Categories", value: "\(app.catalog.categories.count)")
                    }
                    Button { app.push(.search) } label: {
                        SettingsRow(icon: .search, color: Accent.green, title: "Search")
                    }
                    Menu {
                        ForEach(Appearance.allCases, id: \.self) { a in
                            Button {
                                Haptics.play(.selection)
                                app.setAppearance(a)
                            } label: {
                                if a == app.appearance { Label(a.title, systemImage: "checkmark") } else { Text(a.title) }
                            }
                        }
                    } label: {
                        SettingsRow(icon: .appearance, color: Accent.purple, title: "Appearance", value: app.appearance.title)
                    }
                    HStack(spacing: Space.lx) {
                        IconTile(icon: .haptics, color: Accent.teal)
                        TypeStyle.rowTitle.sized(15.5).text("Haptics").foregroundStyle(Palette.ink)
                        Spacer()
                        Toggle("", isOn: $hapticsOn)
                            .labelsHidden()
                            .tint(Accent.blue)
                            .padding(.trailing, 2) // the switch overhangs the row hairlines by ~2 pt
                            .onChange(of: hapticsOn) { _, on in
                                Haptics.isEnabled = on
                                if on { Haptics.play(.selection) }
                            }
                    }
                    .frame(minHeight: Size.rowProduct)
                    .hairline()
                    HStack(spacing: Space.lx) {
                        IconTile(icon: .scan, color: Accent.blue)
                        TypeStyle.rowTitle.sized(15.5).text("Straighten imported photos").foregroundStyle(Palette.ink).lineLimit(1).minimumScaleFactor(0.85)
                        Spacer()
                        Toggle("", isOn: $straightenOn)
                            .onChange(of: straightenOn) { _, _ in Haptics.play(.selection) }
                            .labelsHidden()
                            .tint(Accent.blue)
                            .padding(.trailing, 2)
                            .onChange(of: straightenOn) { _, on in
                                Straightener.isEnabled = on
                                Haptics.play(.selection)
                            }
                    }
                    .frame(minHeight: Size.rowProduct)
                    .hairline()
                    SettingsRow(icon: .money, color: Accent.yellow, title: "Money format", value: "€1.234,56", showsChevron: false)
                    Button { app.push(.catalog) } label: {
                        SettingsRow(icon: .info, color: Color.dynamic(light: 0x8E8E8A, dark: 0x3A3A37), title: "About Shop", value: appVersion)
                    }
                }
                .buttonStyle(.press(haptic: .selection))
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.lx)
            }
            .padding(.bottom, 40)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await app.refreshHealth() }
    }

    private var serverCard: some View {
        Button { app.push(.server) } label: {
            VStack(alignment: .leading, spacing: 22) {
                HStack(alignment: .top) {
                    IconTile(icon: .server, color: .white.opacity(0.2), size: 44)
                    Spacer()
                    HStack(spacing: 6) {
                        StatusDot(color: connected ? Palette.scanGreen : Palette.scanAmber)
                        Text(connected ? "Connected" : (app.network.isOnline ? "Unreachable" : "Offline"))
                    }
                    .textStyle(TypeStyle.label.sized(12.5))
                    .padding(.horizontal, Space.ml)
                    .frame(height: 26)
                    .background(.white.opacity(0.2), in: Capsule())
                }
                VStack(alignment: .leading, spacing: 3) {
                    TypeStyle.emptyTitle.text(app.isMock ? "Demo server" : "Your server")
                    TypeStyle.mono.sized(13).withFace(.monoRegular).text(app.serverURL?.absoluteString.replacingOccurrences(of: "http://", with: "") ?? "").opacity(0.8).lineLimit(1)
                    TypeStyle.rowSub.text(syncLine).opacity(0.8)
                }
            }
            .foregroundStyle(Accent.white)
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Accent.purple, in: RoundedRectangle.continuous(Radius.feature))
        }
        .buttonStyle(.press(haptic: .light))
    }

    private var connected: Bool { app.api.reachability == .online || (app.api.reachability == .unknown && app.network.isOnline) }

    private var syncLine: String {
        var parts: [String] = []
        if let last = app.api.lastSuccess { parts.append("Synced \(Dates.ago(last))") }
        if let v = app.version?.version { parts.append("server \(v)") }
        return parts.isEmpty ? "Not synced yet" : parts.joined(separator: " · ")
    }

    private var queueValue: String {
        let n = app.uploads.pending.count
        return n == 0 ? "Empty" : "\(n) waiting"
    }

    private var appVersion: String {
        let v = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "1.0"
        let b = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "1"
        return "App \(v) (\(b))"
    }
}

// MARK: - S.1 Server & sync

struct ServerView: View {
    @Environment(AppModel.self) private var app
    @State private var syncing = false
    @State private var latency: Int?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                NavHeader(title: "Server & sync")

                statusCard
                    .padding(.horizontal, Space.edge)
                    .padding(.top, 18)

                VStack(alignment: .leading, spacing: 0) {
                    TypeStyle.label.text("Server health").foregroundStyle(Palette.ink2).padding(.bottom, Space.xs)
                    if let health = app.health {
                        ForEach(health.checks.keys.sorted(), id: \.self) { key in
                            let check = health.checks[key]!
                            KeyValueRow(key: Self.title(for: key), value: check.detail ?? (check.ok ? "OK" : "Problem"),
                                        dot: check.ok ? Accent.green : (key == "backup" ? Accent.amber : Accent.red),
                                        valueColor: check.ok ? Palette.ink : (key == "backup" ? Palette.amberText : Palette.redText))
                        }
                    } else {
                        SkeletonRows(count: 4) {
                            HStack(spacing: Space.l) {
                                SkeletonBlock(width: 8, height: 8, radius: 4)
                                SkeletonBar(width: 110, height: 13)
                                Spacer()
                                SkeletonBar(width: 60, height: 12)
                            }
                            .frame(height: 46)
                            .hairline()
                        }
                    }
                    if app.backupIsStale {
                        Banner(tone: .red, message: "No backup for more than 36 hours. Check the NAS share is mounted on the Mac mini.")
                            .padding(.top, Space.l)
                    }
                }
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.xxl)

                VStack(alignment: .leading, spacing: 0) {
                    TypeStyle.label.text("Connection").foregroundStyle(Palette.ink2).padding(.bottom, Space.xs)
                    connectionRow(title: "Server URL", value: app.serverURL?.absoluteString ?? "—", mono: true, action: "Change") {
                        confirmDisconnect(title: "Change server?", message: "This iPhone signs out of this server and forgets its local copy. Scans still waiting to upload are lost.")
                    }
                    connectionRow(title: "This iPhone", value: "“\(app.deviceName)” · key in Keychain", mono: false, action: nil, run: nil)
                    HStack {
                        Text("Server version").foregroundStyle(Palette.ink2)
                        Spacer()
                        Text(app.version.map { "\($0.version) · schema \($0.schemaVersion)" } ?? "—").foregroundStyle(Palette.ink)
                    }
                    .textStyle(.body)
                    .frame(height: 48)
                    .hairline()
                }
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.xxl)

                Button("Disconnect this iPhone") {
                    confirmDisconnect(title: "Disconnect this iPhone?", message: "Its key is revoked on the server and local data is removed. You can pair again with the server password.")
                }
                .buttonStyle(.shop(.destructive))
                .padding(.horizontal, Space.gutter)
                .padding(.top, Space.xxl)
            }
            .padding(.bottom, 40)
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await sync() }
    }

    private var statusCard: some View {
        let ok = app.api.reachability != .unreachable && app.api.reachability != .offline && app.network.isOnline
        return HStack(spacing: Space.l) {
            IconTile(icon: ok ? .check : .wifiSlash, color: ok ? Accent.green : Accent.red, size: 40)
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.rowTitle.text(ok ? "Connected" : (app.network.isOnline ? "Server unreachable" : "Offline")).foregroundStyle(Palette.ink)
                TypeStyle.rowSub.text(detail).foregroundStyle(Palette.ink2)
            }
            Spacer()
            Button {
                Task { await sync() }
            } label: {
                LoadingLabel(title: "Sync now", isLoading: syncing)
            }
            .buttonStyle(.shop(.secondary, size: .small, fullWidth: false, haptic: .light))
        }
        .padding(Space.lx)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.xl))
    }

    private var detail: String {
        var parts: [String] = []
        if let last = app.api.lastSuccess { parts.append("Synced \(Dates.time(last))") }
        if let latency { parts.append("\(latency) ms") }
        if let host = app.serverURL?.host(), host.hasSuffix(".ts.net") { parts.append("over Tailscale") }
        return parts.isEmpty ? "Not synced yet" : parts.joined(separator: " · ")
    }

    private func connectionRow(title: String, value: String, mono: Bool, action: String?, run: (() -> Void)?) -> some View {
        HStack(spacing: Space.l) {
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.caption.text(title).foregroundStyle(Palette.ink2)
                (mono ? TypeStyle.mono.sized(14) : TypeStyle.rowTitle).text(value)
                    .foregroundStyle(Palette.ink)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer()
            if let action, let run {
                Button(action, action: run)
                    .textStyle(.label)
                    .foregroundStyle(Palette.blueText)
                    .buttonStyle(.press(haptic: .light))
            }
        }
        .frame(minHeight: 56)
        .hairline()
    }

    private func sync() async {
        syncing = true
        defer { syncing = false }
        let started = Date()
        await app.refreshHealth()
        latency = app.health == nil ? nil : Int(Date().timeIntervalSince(started) * 1000)
        app.uploads.kick(force: true)
        await app.list.sync()
        await app.catalog.syncAll()
        await app.home.refresh()
        if app.health != nil { Haptics.play(.success) }
    }

    private func confirmDisconnect(title: String, message: String) {
        app.trays.present("disconnect") {
            ConfirmTray(title: title, message: message, confirmTitle: "Disconnect", destructive: true) {
                Haptics.play(.warning)
                await app.signOut()
                return true
            }
        }
    }

    static func title(for key: String) -> String {
        switch key {
        case "db": "Database"
        case "ocr": "OCR"
        case "ocr_fallback": "Fallback OCR"
        case "queue": "Processing queue"
        case "backup": "NAS backup"
        case "storage": "Storage"
        default: key.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }
}

// MARK: - S.2 Upload queue

struct UploadQueueView: View {
    @Environment(AppModel.self) private var app

    private var waiting: [UploadItem] { app.uploads.items.filter { $0.state == .preparing || $0.state == .waiting || $0.state == .uploading } }

    /// Waiting items in queue order; imports of two or more get a batch header.
    private var waitingGroups: [(batch: UUID?, items: [UploadItem])] {
        var groups: [(batch: UUID?, items: [UploadItem])] = []
        for item in waiting {
            if let batch = item.batchId, let i = groups.firstIndex(where: { $0.batch == batch }) {
                groups[i].items.append(item)
            } else {
                groups.append((item.batchId, [item]))
            }
        }
        return groups
    }

    private func batchTitle(_ items: [UploadItem]) -> String {
        let first = items.map(\.createdAt).min() ?? .now
        let from = items.first?.source == .files ? "Files" : "Photos"
        return "Imported from \(from) · \(Dates.shortDay(first)), \(Dates.time(first))"
    }
    private var failed: [UploadItem] { app.uploads.items.filter { $0.state == .failed } }
    private var duplicates: [UploadItem] { app.uploads.items.filter { $0.state == .done && $0.wasDuplicate } }

    var body: some View {
        List {
            NavHeader(title: "Upload queue") {
                if !waiting.isEmpty || !failed.isEmpty {
                    IconButton(icon: .plus, label: "Import receipts") { app.openImportSources() }
                }
            }
            .shopListRow()

            if !app.network.isOnline {
                HStack(spacing: Space.ml) {
                    StatusDot(color: Palette.ink3, size: 8)
                    TypeStyle.body.text("Offline. Scans upload when you reconnect.").foregroundStyle(Palette.ink)
                }
                .padding(Space.lx)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.l))
                .padding(.horizontal, Space.edge)
                .padding(.top, 18)
                .shopListRow()
            }

            if !waiting.isEmpty {
                DaySectionHeader(title: "Waiting · \(waiting.count)", trailing: "On this iPhone")
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.l)
                    .shopListRow()
                ForEach(Array(waitingGroups.enumerated()), id: \.offset) { _, group in
                    if group.batch != nil, group.items.count >= 2 {
                        DaySectionHeader(title: batchTitle(group.items), trailing: "\(group.items.count) receipts")
                            .padding(.horizontal, Space.gutter)
                            .padding(.top, Space.m)
                            .shopListRow()
                    }
                    ForEach(group.items) { item in itemRow(item) }
                }
            }
            if !failed.isEmpty || !duplicates.isEmpty {
                DaySectionHeader(title: "Didn't upload · \(failed.count + duplicates.count)")
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, Space.l + (waiting.isEmpty ? Space.l : 0))
                    .shopListRow()
                ForEach(failed) { item in problemRow(item, duplicate: false).padding(.horizontal, Space.gutter).shopListRow() }
                ForEach(duplicates) { item in problemRow(item, duplicate: true).padding(.horizontal, Space.gutter).shopListRow() }
            }
            if waiting.isEmpty && failed.isEmpty && duplicates.isEmpty {
                EmptyState(title: "All uploaded", message: "Scans wait here when you're offline or the server can't be reached, and upload on their own.",
                           action: (title: "Import receipts", run: { app.openImportSources() })) {
                    GlyphIllustration(icon: .upload, color: Accent.blue)
                }
                .padding(.top, 60)
                .padding(.horizontal, Space.gutter)
                .shopListRow()
            }
        }
        .shopListStyle()
        .contentMargins(.bottom, 120, for: .scrollContent)
        .safeAreaInset(edge: .bottom) {
            if !waiting.isEmpty || !failed.isEmpty {
                Button(app.network.isOnline ? "Retry now" : "Retry when online") {
                    for item in failed { app.uploads.retry(item.id) }
                    app.uploads.kick(force: true)
                }
                .buttonStyle(.shop())
                .disabled(!app.network.isOnline)
                .padding(.horizontal, Space.gutter)
                .padding(.bottom, Space.m)
            }
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
    }

    private func itemRow(_ item: UploadItem) -> some View {
        SwipeActionRow(trailing: SwipeAction(icon: "xmark", title: "Remove", tint: Accent.red, removesRow: true, haptic: .soft) { removeWithUndo(item) }) {
            itemRowContent(item)
                .padding(.horizontal, Space.gutter)
        }
        .shopListRow()
    }

    /// Takes the item off the queue but keeps its files for the 8 s the Undo toast is up.
    private func removeWithUndo(_ item: UploadItem) {
        app.uploads.softRemove(item.id)
        app.toasts.undoable("Removed from the queue") { app.uploads.undoRemove(item.id) }
    }

    private func itemRowContent(_ item: UploadItem) -> some View {
        HStack(spacing: Space.l) {
            thumbnail(item)
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.rowTitle.text(item.shopId == nil ? "Shop to be found out" : item.shopName).foregroundStyle(Palette.ink)
                TypeStyle.rowSub.text(itemSubtitle(item)).foregroundStyle(Palette.ink2).lineLimit(1)
            }
            Spacer()
            if item.state == .uploading || item.state == .preparing {
                ProgressView().controlSize(.small).tint(Palette.ink2)
            } else {
                IconView(.upload, size: 18).foregroundStyle(Palette.ink3)
            }
        }
        .frame(height: 60)
        .hairline()
        .contextMenu {
            Button("Remove", systemImage: "trash", role: .destructive) { removeWithUndo(item) }
        }
    }

    private func itemSubtitle(_ item: UploadItem) -> String {
        let pages = item.pages == 1 ? "1 page" : "\(item.pages) pages"
        switch item.state {
        case .preparing: return "\(pages) · Preparing…"
        case .waiting: return "\(pages) · \(item.lastError ?? "Waiting")"
        default: return "\(pages) · \(item.source == .scanner || item.source == nil ? "scanned" : "imported") \(Dates.time(item.createdAt))"
        }
    }

    private func problemRow(_ item: UploadItem, duplicate: Bool) -> some View {
        HStack(alignment: .top, spacing: Space.l) {
            thumbnail(item)
            VStack(alignment: .leading, spacing: Space.m) {
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text(item.shopId == nil ? "Shop to be found out" : item.shopName).foregroundStyle(Palette.ink)
                    TypeStyle.rowSub.text(duplicate ? "Same receipt was already uploaded" : (item.lastError ?? "Upload failed"))
                        .foregroundStyle(duplicate ? Palette.amberText : Palette.redText)
                        .fixedSize(horizontal: false, vertical: true)
                }
                HStack(spacing: Space.m) {
                    Button("Remove") { app.uploads.remove(item.id) }
                        .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    if duplicate, let id = item.receiptId {
                        Button("Open receipt") { app.push(.receipt(id)) }
                            .buttonStyle(.shop(size: .small, fullWidth: false))
                    } else if !duplicate {
                        Button("Retry") { app.uploads.retry(item.id) }
                            .buttonStyle(.shop(size: .small, fullWidth: false))
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, Space.l)
        .hairline()
    }

    private func thumbnail(_ item: UploadItem) -> some View {
        Group {
            if let image = app.uploads.thumbnail(for: item) {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                PaperReceipt(lines: 3)
            }
        }
        .frame(width: 32, height: 40)
        .clipShape(RoundedRectangle.continuous(Radius.badge))
        .overlay(RoundedRectangle.continuous(Radius.badge).strokeBorder(Palette.line))
    }
}
