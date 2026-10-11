import SwiftUI

// MARK: - H.1.3 Edit / add line

struct EditLineTray: View {
    let model: ReceiptModel
    let line: ReceiptLine?

    @Environment(TrayCoordinator.self) private var trays
    @State private var kind: LineKind
    @State private var text: String
    @State private var qty: String
    @State private var unit: LineUnit
    @State private var unitPrice: String
    @State private var total: String
    @State private var vat: String
    @State private var saving = false

    init(model: ReceiptModel, line: ReceiptLine?) {
        self.model = model
        self.line = line
        let isUnread = line?.kind == .other
        _kind = State(initialValue: isUnread ? .item : (line?.kind ?? .item))
        _text = State(initialValue: line?.rawText ?? "")
        _qty = State(initialValue: Quantity.decimal(line?.qty ?? 1))
        _unit = State(initialValue: line?.unit ?? .pc)
        _unitPrice = State(initialValue: Money.editable(line?.unitPriceCents ?? line?.lineTotalCents))
        _total = State(initialValue: Money.editable(line?.lineTotalCents))
        _vat = State(initialValue: line?.vatCode ?? "")
    }

    private var isNew: Bool { line == nil }
    private var isUnread: Bool { line?.kind == .other }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: isNew ? "Add line" : (isUnread ? "Fix row" : "Edit line"))

            if let line {
                RawRowTile(caption: "Row \(line.lineNo) on the receipt", raw: line.rawText, amount: Money.plain(line.lineTotalCents))
            }

            FlowLayout(spacing: 6, lineSpacing: 6) {
                ForEach(LineKind.allCases.filter { $0 != .other }, id: \.self) { k in
                    Chip(title: k.title, isSelected: kind == k, size: .large, idleBackground: Palette.trayField) {
                        kind = k
                        if k == .discount, let cents = Money.parse(total), cents > 0 { total = Money.editable(-cents) }
                    }
                }
            }

            Grid(horizontalSpacing: Space.m, verticalSpacing: Space.m) {
                GridRow {
                    FieldTile(label: "Text", text: $text, placeholder: "As printed", mono: true, emphasised: true, background: Palette.trayField)
                        .textInputAutocapitalization(.characters)
                        .autocorrectionDisabled()
                        .gridCellColumns(2)
                }
                GridRow {
                    ZStack(alignment: .topTrailing) {
                        FieldTile(label: "Quantity · \(unit.rawValue)", text: $qty, background: Palette.trayField)
                            .keyboardType(.decimalPad)
                        Menu {
                            ForEach(LineUnit.allCases, id: \.self) { u in
                                Button(u.title) { unit = u }
                            }
                        } label: {
                            IconView(.down, size: 12, weight: .bold)
                                .foregroundStyle(Palette.ink2)
                                .frame(width: 32, height: 32)
                        }
                        .accessibilityLabel("Unit")
                    }
                    FieldTile(label: unit == .pc ? "Unit price" : "Price per \(unit.rawValue)", text: $unitPrice, background: Palette.trayField)
                        .keyboardType(.decimalPad)
                }
                GridRow {
                    FieldTile(label: "Line total", text: $total, emphasised: true, background: Palette.trayField)
                        .keyboardType(.numbersAndPunctuation)
                    FieldTile(label: "VAT code", text: $vat, placeholder: "A, B…", background: Palette.trayField)
                        .textInputAutocapitalization(.characters)
                }
            }
            .onChange(of: qty) { _, _ in recomputeTotal() }
            .onChange(of: unitPrice) { _, _ in recomputeTotal() }

            sumHint

            HStack(spacing: Space.m) {
                if isUnread {
                    Button("Ignore row") { trays.dismiss() }
                        .buttonStyle(.shop(.secondary))
                } else if let line {
                    Button("Delete") {
                        let id = line.id
                        trays.dismiss()
                        Task { await model.deleteLine(id) }
                    }
                    .buttonStyle(.shop(.destructive))
                } else {
                    Button("Cancel") { trays.dismiss() }
                        .buttonStyle(.shop(.secondary))
                }
                Button {
                    Task { await save() }
                } label: {
                    LoadingLabel(title: isNew || isUnread ? "Add line" : "Save", isLoading: saving)
                }
                .buttonStyle(.shop(haptic: .medium))
                .frame(maxWidth: .infinity)
                .layoutPriority(1.4)
                .disabled(Money.parse(total) == nil || text.trimmingCharacters(in: .whitespaces).isEmpty || saving)
            }
        }
    }

    @ViewBuilder
    private var sumHint: some View {
        if let receiptTotal = model.receipt?.totalCents, let newCents = Money.parse(total) {
            let old = (line.map { $0.kind == .other ? 0 : $0.lineTotalCents }) ?? 0
            let after = model.countedCents - old + newCents
            let diff = after - receiptTotal
            HStack(spacing: Space.m) {
                GlyphBadge(glyph: abs(diff) <= 1 ? .check : .alert, color: abs(diff) <= 1 ? Accent.green : Accent.amber, size: 18)
                TypeStyle.label.sized(13.5).text(abs(diff) <= 1 ? "The receipt adds up after this" : "Still \(Money.format(Int64(diff.magnitude))) \(diff < 0 ? "short" : "over") after this")
                    .foregroundStyle(abs(diff) <= 1 ? Palette.greenText : Palette.amberText)
                    .contentTransition(.numericText())
            }
            .animation(Motion.numberRoll, value: diff)
        }
    }

    private func recomputeTotal() {
        guard let q = Quantity.parse(qty), let up = Money.parse(unitPrice) else { return }
        let computed = Int64((q * Double(up)).rounded())
        let signed = kind == .discount ? -abs(computed) : computed
        total = Money.editable(signed)
    }

    private func save() async {
        guard let cents = Money.parse(total) else { return }
        saving = true
        defer { saving = false }
        let input = LineInput(
            rawText: text.trimmingCharacters(in: .whitespaces).uppercased(),
            kind: kind,
            qty: Quantity.parse(qty) ?? 1,
            unit: unit,
            unitPriceCents: Money.parse(unitPrice),
            lineTotalCents: cents,
            vatCode: vat.isEmpty ? nil : vat,
            parentLineId: line?.parentLineId
        )
        if await model.saveLine(line?.id, input) {
            Haptics.play(.success)
            trays.dismiss()
        }
    }
}

extension LineKind {
    var title: String {
        switch self {
        case .item: "Item"
        case .discount: "Discount"
        case .deposit: "Deposit"
        case .bag: "Bag"
        case .other: "Other"
        }
    }
}

extension LineUnit {
    var title: String {
        switch self {
        case .pc: "Pieces"
        case .kg: "Kilograms"
        case .l: "Litres"
        }
    }
}

// MARK: - H.1.4 Link product

struct LinkProductTray: View {
    let model: ReceiptModel
    let line: ReceiptLine

    @Environment(TrayCoordinator.self) private var trays
    @Environment(AppModel.self) private var app
    @State private var query = ""
    @State private var results: [Product] = []
    @State private var selected: Int64?
    @State private var remember = true
    @State private var linking = false
    @State private var searchTask: Task<Void, Never>?

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: line.productId == nil ? "Link product" : "Change product")

            VStack(alignment: .leading, spacing: 3) {
                TypeStyle.mono.sized(18).text(line.rawText).foregroundStyle(Palette.ink)
                TypeStyle.body.sized(13).text("\(model.shop?.name ?? "Shop") · \(Quantity.decimal(line.qty)) × \(Money.format(line.unitPriceCents ?? line.lineTotalCents))")
                    .foregroundStyle(Palette.ink2)
            }

            SearchField(placeholder: "Search products", text: $query, background: Palette.trayField)
                .onChange(of: query) { _, q in search(q) }

            ScrollView {
                VStack(spacing: 0) {
                    ForEach(candidates, id: \.id) { candidate in
                        candidateRow(candidate)
                    }
                    newProductRow
                }
            }
            .frame(maxHeight: 280)
            .scrollBounceBehavior(.basedOnSize)

            HStack(spacing: Space.l) {
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text("Remember at \(model.shop?.name ?? "this shop")").foregroundStyle(Palette.ink)
                    TypeStyle.rowSub.text("Links this text on every next receipt").foregroundStyle(Palette.ink2)
                }
                Spacer()
                Toggle("", isOn: $remember)
                    .labelsHidden()
                    .tint(Accent.blue)
                    .onChange(of: remember) { _, _ in Haptics.play(.selection) }
            }
            .padding(.vertical, Space.l)
            .padding(.horizontal, Space.lx)
            .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.xl))

            Button {
                Task { await link() }
            } label: {
                LoadingLabel(title: "Link", isLoading: linking)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(selected == nil || linking)

            if line.kind == .item && line.productId != nil {
                OptionRow(icon: .unlink, title: "This isn't the same product", hint: "Moves other lines with this text too") {
                    trays.dismiss()
                    Task { await model.splitProduct(of: line) }
                }
            }
        }
        .onAppear {
            if selected == nil { selected = line.suggestions?.first.flatMap { $0.score >= 0.75 ? $0.productId : nil } ?? line.productId }
        }
    }

    private struct Candidate {
        let id: Int64
        let name: String
        let meta: String
        let score: Double?
        let category: String?
    }

    private var candidates: [Candidate] {
        if !query.trimmingCharacters(in: .whitespaces).isEmpty {
            return results.prefix(8).map { p in
                Candidate(id: p.id, name: p.name, meta: meta(for: p), score: p.score, category: app.catalog.category(p.categoryId)?.name)
            }
        }
        return (line.suggestions ?? []).prefix(3).map { s in
            let p = app.catalog.product(s.productId)
            return Candidate(id: s.productId, name: s.name, meta: p.map(meta(for:)) ?? (s.brand ?? ""), score: s.score,
                             category: app.catalog.categoryName(forProduct: s.productId))
        }
    }

    private func meta(for p: Product) -> String {
        [p.sizeText, app.catalog.category(p.categoryId)?.name].compactMap { $0 }.joined(separator: " · ")
    }

    private func candidateRow(_ c: Candidate) -> some View {
        let isOn = selected == c.id
        let isTop = c.id == candidates.first?.id && (c.score ?? 0) >= 0.75
        return Button {
            Haptics.play(.selection)
            selected = c.id
        } label: {
            HStack(spacing: Space.l) {
                ProductTile(name: c.name, category: c.category, size: 40)
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text(c.name).foregroundStyle(Palette.ink).lineLimit(1)
                    if !c.meta.isEmpty { TypeStyle.rowSub.text(c.meta).foregroundStyle(Palette.ink2).lineLimit(1) }
                }
                Spacer(minLength: Space.m)
                if let score = c.score {
                    TypeStyle.label.sized(12.5).text("\(Int((score * 100).rounded()))%")
                        .foregroundStyle(isTop ? Palette.greenText : Palette.ink2)
                }
                ZStack {
                    Circle().strokeBorder(Palette.ink3, lineWidth: 1.5).opacity(isOn ? 0 : 1)
                    Circle().fill(Accent.blue).opacity(isOn ? 1 : 0)
                    Image(icon: .check).font(.system(size: 10, weight: .heavy)).foregroundStyle(Accent.white).opacity(isOn ? 1 : 0)
                }
                .frame(width: 22, height: 22)
                .animation(Motion.pop, value: isOn)
            }
            .frame(minHeight: 58)
            .hairline()
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: nil))
    }

    private var newProductRow: some View {
        Button {
            let lineId = line.id
            let remember = self.remember
            let model = self.model
            trays.present("new-product-from-line") {
                ProductFormTray(mode: .create(name: ReceiptModel.tidy(line.rawText), barcode: nil, prefill: nil)) { product in
                    Task { _ = await model.link(lineId, productId: product.id, remember: remember) }
                }
            }
        } label: {
            HStack(spacing: Space.l) {
                RoundedRectangle.continuous(12)
                    .strokeBorder(Palette.ink3, style: StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
                    .frame(width: 40, height: 40)
                    .overlay(IconView(.plus, size: 16, weight: .semibold).foregroundStyle(Palette.ink2))
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.text("New product").foregroundStyle(Palette.ink)
                    TypeStyle.rowSub.text("Start from “\(ReceiptModel.tidy(line.rawText))”").foregroundStyle(Palette.ink2).lineLimit(1)
                }
                Spacer()
            }
            .frame(minHeight: 58)
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: .light))
    }

    private func search(_ q: String) {
        searchTask?.cancel()
        let text = q.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { results = []; return }
        // Instant local results, then the server's fuzzy ranking.
        results = ProductSearch.local(text, in: app.catalog)
        searchTask = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            if let remote = try? await app.api.products(query: text, limit: 20), !Task.isCancelled {
                results = remote
            }
        }
    }

    private func link() async {
        guard let selected else { return }
        linking = true
        defer { linking = false }
        if await model.link(line.id, productId: selected, remember: remember) {
            trays.dismiss()
        }
    }
}

/// Local fuzzy-ish search over the cached catalog (instant, offline).
enum ProductSearch {
    @MainActor
    static func local(_ query: String, in catalog: Catalog, limit: Int = 20) -> [Product] {
        let q = fold(query)
        guard !q.isEmpty else { return [] }
        let tokens = q.split(separator: " ").map(String.init)
        return catalog.products.values
            .compactMap { p -> (Product, Double)? in
                let hay = fold(p.name + " " + (p.nameEn ?? "") + " " + (p.brand ?? ""))
                guard tokens.allSatisfy({ hay.contains($0) }) else { return nil }
                let score = hay.hasPrefix(q) ? 1.0 : 0.8
                return (p, score)
            }
            .sorted { $0.1 == $1.1 ? $0.0.name < $1.0.name : $0.1 > $1.1 }
            .prefix(limit)
            .map { var p = $0.0; p.score = $0.1; return p }
    }

    static func fold(_ s: String) -> String {
        s.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: .current)
            .lowercased()
            .trimmingCharacters(in: .whitespaces)
    }
}

// MARK: - H.1.5 Actions

struct ReceiptActionsTray: View {
    let model: ReceiptModel
    let onDeleted: () -> Void

    @Environment(TrayCoordinator.self) private var trays
    @Environment(AppModel.self) private var app

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Options")
            VStack(spacing: 6) {
                OptionRow(icon: .edit, title: "Edit details", hint: "Shop, date and printed total") {
                    trays.present("edit-details") { EditDetailsTray(model: model) }
                }
                OptionRow(icon: .image, title: "View image", hint: "Full size, pinch to zoom") {
                    trays.dismiss()
                    app.push(.receiptImage(model.receiptId))
                }
                OptionRow(icon: .reparse, title: "Reparse", hint: "Apply the latest rules · keeps your edits") {
                    rerun(ocr: false)
                }
                OptionRow(icon: .reocr, title: "Re-run OCR", hint: "Read the image again from scratch") {
                    rerun(ocr: true)
                }
                OptionRow(icon: .trash, title: "Delete receipt", hint: "Removes the receipt and its image", destructive: true) {
                    trays.present("delete-receipt") {
                        ConfirmTray(
                            title: "Delete this receipt?",
                            message: "The receipt, its lines and its image are removed from the server. This can't be undone.",
                            confirmTitle: "Delete receipt",
                            destructive: true
                        ) {
                            if await model.delete() {
                                onDeleted()
                                return true
                            }
                            return false
                        }
                    }
                }
            }
        }
    }

    private func rerun(ocr: Bool) {
        Task {
            switch await model.rerun(ocr: ocr) {
            case .started:
                trays.dismiss()
                app.toasts.show(ocr ? "Reading the image again…" : "Reparsing…")
            case .needsForce(let detail):
                trays.present("force-rerun") {
                    ConfirmTray(
                        title: ocr ? "Re-run OCR and drop your edits?" : "Reparse and drop your edits?",
                        message: detail,
                        confirmTitle: ocr ? "Re-run anyway" : "Reparse anyway",
                        destructive: true
                    ) {
                        if case .started = await model.rerun(ocr: ocr, force: true) { return true }
                        return false
                    }
                }
            case .failed:
                break
            }
        }
    }
}

/// Generic confirmation tray (destructive ones use the red button, no bounce).
struct ConfirmTray: View {
    let title: String
    var message: String? = nil
    let confirmTitle: String
    var destructive: Bool = false
    let action: () async -> Bool
    /// Runs about 0.35 s after the tray has closed. Put list changes, pops and toasts here:
    /// mutating them in the same turn as the dismiss makes the tray's slide stutter.
    var afterDismiss: (@MainActor () -> Void)? = nil

    @Environment(TrayCoordinator.self) private var trays
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: title)
            if let message {
                TypeStyle.body.sized(14.5).text(message).foregroundStyle(Palette.ink2)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, Space.xs)
            }
            HStack(spacing: Space.m) {
                Button("Cancel") { trays.dismiss() }
                    .buttonStyle(.shop(.secondary))
                Button {
                    Task {
                        working = true
                        let ok = await action()
                        working = false
                        if ok {
                            trays.dismiss()
                            if let afterDismiss {
                                Task { @MainActor in
                                    try? await Task.sleep(for: .milliseconds(350))
                                    afterDismiss()
                                }
                            }
                        }
                    }
                } label: {
                    LoadingLabel(title: confirmTitle, isLoading: working)
                }
                .buttonStyle(.shop(destructive ? .solid(Accent.red) : .primary, haptic: destructive ? nil : .medium))
                .disabled(working)
            }
        }
    }
}

// MARK: - H.1.2 Edit details

struct EditDetailsTray: View {
    let model: ReceiptModel

    @Environment(TrayCoordinator.self) private var trays
    @Environment(AppModel.self) private var app
    @State private var shopId: Int64 = 0
    @State private var date = Date()
    @State private var hasDate = true
    @State private var total = ""
    @State private var loaded = false
    @State private var saving = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: "Edit details")

            VStack(alignment: .leading, spacing: Space.m) {
                TypeStyle.label.text("Shop").foregroundStyle(Palette.ink2)
                FlowLayout(spacing: 6, lineSpacing: 6) {
                    ForEach(app.catalog.shops) { shop in
                        Chip(title: shop.displayName, isSelected: shopId == shop.id, size: .large, idleBackground: Palette.trayField) { shopId = shop.id }
                    }
                }
            }

            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    TypeStyle.caption.text("Date and time").foregroundStyle(Palette.ink2)
                    if hasDate {
                        DatePicker("", selection: $date, in: ...Date.now)
                            .labelsHidden()
                            .datePickerStyle(.compact)
                            .environment(\.timeZone, Dates.timeZone)
                    } else {
                        Button("Set date") { hasDate = true }
                            .textStyle(.rowTitle)
                            .foregroundStyle(Palette.blueText)
                    }
                }
                Spacer()
            }
            .padding(.vertical, Space.ml)
            .padding(.horizontal, Space.lx)
            .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))

            FieldTile(label: "Printed total", text: $total, placeholder: "0,00", emphasised: true, background: Palette.trayField)
                .keyboardType(.decimalPad)

            Button {
                Task { await save() }
            } label: {
                LoadingLabel(title: "Save", isLoading: saving)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(saving || shopId == 0)
        }
        .onAppear {
            guard !loaded, let r = model.receipt else { return }
            shopId = r.shopId ?? 0
            if let d = r.purchasedAt { date = d } else { hasDate = false }
            total = Money.editable(r.totalCents)
            loaded = true
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        var patch = ReceiptPatch()
        if shopId != 0 && shopId != model.receipt?.shopId { patch.shopId = shopId }
        if hasDate && date != model.receipt?.purchasedAt { patch.purchasedAt = date }
        if let cents = Money.parse(total), cents != model.receipt?.totalCents { patch.totalCents = cents }
        if patch.shopId == nil && patch.purchasedAt == nil && patch.totalCents == nil {
            trays.dismiss()
            return
        }
        if await model.updateDetails(patch) {
            Haptics.play(.success)
            trays.dismiss()
        }
    }
}
