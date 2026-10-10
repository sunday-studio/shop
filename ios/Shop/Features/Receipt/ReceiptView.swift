import SwiftUI

/// H.1 · Receipt: image thumbnail and header on top, lines below, a sticky Σ-vs-total bar
/// with Confirm (always enabled). A receipt that needs attention opens straight into this.
struct ReceiptView: View {
    let receiptId: Int64
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var model: ReceiptModel?

    var body: some View {
        Group {
            if let model {
                content(model)
            } else {
                Palette.bg
            }
        }
        .background(Palette.bg)
        .onAppear {
            if model == nil { model = ReceiptModel(receiptId: receiptId, app: app) }
        }
        .task(id: model == nil) {
            await model?.load()
        }
        .onDisappear { model?.stop() }
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

                        if let banner = model.banner {
                            Banner(tone: banner.tone, lead: banner.lead.isEmpty ? nil : banner.lead, message: banner.message)
                                .padding(.horizontal, Space.edge)
                                .padding(.top, 18)
                        }

                        if receipt.status == .processing || receipt.status == .pending {
                            processing
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
                ConfirmBar(linesCents: model.countedCents, totalCents: receipt.totalCents, isWorking: model.isConfirming) {
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
                .staggeredAppear(index: index)
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
