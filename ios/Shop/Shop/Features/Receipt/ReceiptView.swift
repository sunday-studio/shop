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
    }
}

private struct ReceiptScreen: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var model: ReceiptModel
    /// The screen opened on a summary (no lines): its first fill swaps skeleton rows for real
    /// ones in place, without the stagger.
    @State private var openedOnSummary: Bool

    init(receiptId: Int64, app: AppModel) {
        let model = ReceiptModel(receiptId: receiptId, app: app)
        _model = State(initialValue: model)
        _openedOnSummary = State(initialValue: model.receipt != nil && model.receipt?.lines == nil)
    }

    var body: some View {
        content(model)
            .background(Palette.bg)
            .task { await model.load() }
            .onDisappear { model.stop() }
    }

    @ViewBuilder
    private func content(_ model: ReceiptModel) -> some View {
        ZStack(alignment: .bottom) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    NavHeader(title: "Receipt") {
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
                                name: suggestion.name, detail: suggestion.detail, waiting: suggestion.receiptCount,
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
                                Text("Needs a connection").textStyle(.rowSub).foregroundStyle(Palette.ink2)
                                    .padding(.horizontal, Space.edge + Space.xs).padding(.top, Space.s)
                            }
                        }

                        if let banner = model.banner {
                            Banner(tone: banner.tone, lead: banner.lead.isEmpty ? nil : banner.lead, message: banner.message)
                                .padding(.horizontal, Space.edge)
                                .padding(.top, 18)
                            if receipt.shopId == nil {
                                Button("Pick a shop") {
                                    app.trays.present("edit-details") { EditDetailsTray(model: model) }
                                }
                                .textStyle(.label)
                                .foregroundStyle(Palette.blueText)
                                .buttonStyle(.press(haptic: .selection))
                                .padding(.horizontal, Space.edge + Space.lx)
                                .padding(.top, Space.m)
                            }
                            if receipt.reasons.contains(.noDate) {
                                Button("Set the date") {
                                    app.trays.present("no-date") { NoDateFixTray(model: model) }
                                }
                                .textStyle(.label)
                                .foregroundStyle(Palette.blueText)
                                .buttonStyle(.press(haptic: .selection))
                                .padding(.horizontal, Space.edge + Space.lx)
                                .padding(.top, Space.m)
                            }
                        } else if receipt.lines == nil && receipt.status == .needsReview {
                            // The banner needs the lines; hold its space so the rows don't jump.
                            Banner(tone: .amber, lead: "Placeholder amount short.", message: "Check the lines against the photo.")
                                .redacted(reason: .placeholder)
                                .accessibilityHidden(true)
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
                        } else {
                            lineList(model)
                                .padding(.horizontal, Space.gutter)
                                .padding(.top, Space.xl)
                        }
                    } else if let error = model.error {
                        ErrorState(message: error.message) { Task { await model.load() } }
                            .padding(.top, 60)
                    } else {
                        LoadingState(title: "Loading receipt")
                            .padding(.top, 60)
                    }
                }
                .padding(.bottom, 130)
            }
            .scrollIndicators(.hidden)
            .refreshable { await model.load() }

            if let receipt = model.receipt, receipt.status == .needsReview {
                ConfirmBar(linesCents: model.countedCents, totalCents: receipt.totalCents, isWorking: model.isConfirming, isLoading: receipt.lines == nil) {
                    Task {
                        if await model.confirm() {
                            Haptics.play(.success)
                            dismiss()
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
                HStack(spacing: Space.m) {
                    ShopMark(name: model.shop?.name ?? "Shop", size: 24)
                    Text(model.shop?.displayName ?? "Unknown shop")
                        .textStyle(TypeStyle.rowTitle.sized(14))
                        .foregroundStyle(Palette.ink)
                        .lineLimit(1)
                }
                if let total = receipt.totalCents {
                    HeroAmount(cents: total, size: 48)
                } else {
                    Text("No total")
                        .textStyle(TypeStyle.emptyTitle.sized(30))
                        .foregroundStyle(Palette.ink3)
                }
                Text(model.metaLine)
                    .textStyle(.label.withFace(.regular))
                    .foregroundStyle(Palette.ink2)
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

    // MARK: Lines

    private func editSuggestion(_ suggestion: ShopSuggestion, model: ReceiptModel) {
        app.trays.present("suggestion-edit") {
            SuggestionEditTray(suggestion: suggestion) { name, branch, domain in
                Task { await model.acceptSuggestion(name: name, branch: branch, domain: domain) }
            }
        }
    }

    private var linesSkeleton: some View {
        VStack(alignment: .leading, spacing: 0) {
            SectionLabel(title: "Lines") {
                Chip(title: "All 00", isSelected: true, size: .small) {}
            }
            ForEach(0..<6, id: \.self) { _ in
                LineRow(title: "Placeholder product name", amount: "€0,00")
                    .hairline()
            }
        }
        .redacted(reason: .placeholder)
        .accessibilityHidden(true)
    }

    private func lineList(_ model: ReceiptModel) -> some View {
        @Bindable var model = model
        return VStack(alignment: .leading, spacing: 0) {
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

            if model.visibleLines.isEmpty {
                Text(model.lineFilter == .toFix ? "Nothing left to fix." : "No lines were read.")
                    .textStyle(.body)
                    .foregroundStyle(Palette.ink2)
                    .padding(.vertical, Space.xxl)
            }

            ForEach(Array(model.visibleLines.enumerated()), id: \.element.id) { index, line in
                let p = model.present(line)
                Button {
                    tap(line, model: model)
                } label: {
                    LineRow(title: p.title, isRaw: p.isRaw, subtitle: p.subtitle, subtitleColor: p.subtitleColor, amount: p.amount,
                            titleColor: p.titleColor, amountColor: p.amountColor, dot: p.dot, indented: p.indented)
                }
                .buttonStyle(.press(haptic: nil))
                .staggeredAppear(index: openedOnSummary ? Motion.staggerMax : index)
                .contextMenu {
                    Button("Edit line", systemImage: "pencil") { openEdit(line, model: model) }
                    if line.kind == .item {
                        Button(line.productId == nil ? "Link product" : "Change product", systemImage: "link") { openLink(line, model: model) }
                    }
                    Button("Delete line", systemImage: "trash", role: .destructive) {
                        Task { await model.deleteLine(line.id) }
                    }
                }
            }

            LinkButton(title: "Add line") { openEdit(nil, model: model) }
        }
    }

    private var processing: some View {
        VStack(spacing: Space.l) {
            ProgressView().controlSize(.large).tint(Palette.ink2)
            Text("Reading receipt…").textStyle(.emptyTitle).foregroundStyle(Palette.ink)
            Text("OCR and parsing on your server, usually 4–15 seconds. You can leave this screen.")
                .textStyle(TypeStyle.body.sized(14.5))
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
            ReceiptActionsTray(model: model, onDeleted: { dismiss() })
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

    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var branch = ""
    @State private var website = ""
    @State private var loaded = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Add shop", subtitle: suggestion.kind == .existing ? "Moves to \(suggestion.name)" : nil)
            HStack(spacing: Space.l) {
                ShopMark(name: name.isEmpty ? "?" : name, size: 32)
                Text("The logo comes from the website once the shop is added.")
                    .textStyle(.rowSub).foregroundStyle(Palette.ink2)
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
