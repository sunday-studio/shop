import SwiftUI

/// C.3 · Unpack: continuous barcode scanning over the camera, with the session list in a
/// dark tray. New products open the quick-add form; after saving, C.3.3 offers to link
/// the product to a recent unmatched receipt line.
struct UnpackView: View {
    @Environment(AppModel.self) private var app
    @State private var model: UnpackModel?
    @State private var trays = TrayCoordinator()
    @State private var torch = false
    @State private var manualCode = ""
    @State private var scanLine = false
    @State private var showSummary = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    private let darkTray = Color(hex: 0x1C1C1A)
    private let darkInk = Color(hex: 0xF4F4F1)
    private let darkInk2 = Color(hex: 0x8F8F8A)
    private let darkLine = Color(hex: 0x2A2A27)

    var body: some View {
        ZStack(alignment: .bottom) {
            RadialGradient(colors: [Color(hex: 0x3B3F3C), Color(hex: 0x111211)], center: UnitPoint(x: 0.5, y: 0.3), startRadius: 10, endRadius: 560)
                .ignoresSafeArea()

            if BarcodeScanner.isAvailable {
                BarcodeScanner { code in model?.scanned(code) }
                    .ignoresSafeArea()
            }

            viewfinder
                .frame(maxHeight: .infinity, alignment: .center)
                .padding(.bottom, 220)

            VStack {
                topBar
                Spacer()
            }

            sessionTray
        }
        .trayHost(trays)
        .toastHost(app.toasts, bottomInset: 480)
        .onAppear {
            if model == nil { model = UnpackModel(app: app) }
            Haptics.prepare()
            // Simulator aid (no camera): `-mock-scan` scans one code of each outcome, a second apart.
            if app.isMock, ProcessInfo.processInfo.arguments.contains("-mock-scan") {
                Task {
                    for code in ["8718907400718", "8710000000010", "4000000000001", "9900000000009", "8711111111118", "123"] {
                        try? await Task.sleep(for: .seconds(1))
                        model?.scanned(code)
                    }
                }
            }
        }
        .onDisappear { if torch { Torch.set(false) } }
        .onChange(of: app.network.isOnline) { _, online in
            if online { Task { await model?.flushQueued() } }
        }
    }

    // MARK: Top bar

    private var topBar: some View {
        HStack {
            IconButton(icon: .close, label: "Close", foreground: .white, background: .white.opacity(0.14)) {
                app.capture = nil
            }
            Spacer()
            HStack(spacing: Space.ml) {
                HStack(spacing: 5) {
                    RoundedRectangle.continuous(3).fill(Color(hex: 0x3DDC84)).frame(width: 8, height: 8)
                    Text("\(model?.knownCount ?? 0) known")
                }
                HStack(spacing: 5) {
                    RoundedRectangle.continuous(3).fill(Color(hex: 0xFFB340)).frame(width: 8, height: 8)
                    Text("\((model?.addedCount ?? 0) + (model?.toNameCount ?? 0)) new")
                }
            }
            .textStyle(.label)
            .foregroundStyle(.white)
            .padding(.horizontal, Space.l)
            .frame(height: Size.chip)
            .background(.white.opacity(0.14), in: Capsule())
            .contentTransition(.numericText())
            Spacer()
            if Torch.isAvailable {
                IconButton(icon: torch ? .flash : .flashOff, label: "Torch", foreground: .white, background: .white.opacity(torch ? 0.32 : 0.14), haptic: .selection) {
                    torch.toggle()
                    Torch.set(torch)
                }
            } else {
                HeaderSpacer()
            }
        }
        .padding(.horizontal, Space.edge)
        .padding(.top, Space.l)
    }

    // MARK: Viewfinder

    private var viewfinder: some View {
        ZStack {
            ForEach(0..<4, id: \.self) { corner in
                CornerShape()
                    .stroke(.white, style: StrokeStyle(lineWidth: 4, lineCap: .round))
                    .frame(width: 30, height: 30)
                    .rotationEffect(.degrees(Double(corner) * 90))
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: [.topLeading, .topTrailing, .bottomTrailing, .bottomLeading][corner])
            }
            Capsule()
                .fill(Color(hex: 0x3DDC84))
                .frame(height: 2)
                .shadow(color: Color(hex: 0x3DDC84), radius: 7)
                .padding(.horizontal, 18)
                .offset(y: scanLine ? 30 : -30)
                .animation(reduce ? nil : .easeInOut(duration: 1.4).repeatForever(autoreverses: true), value: scanLine)
                .onAppear { scanLine = true }
        }
        .frame(width: 250, height: 130)
        .accessibilityHidden(true)
    }

    // MARK: Session tray

    private var sessionTray: some View {
        VStack(alignment: .leading, spacing: 6) {
            if showSummary, let model {
                summary(model)
            } else {
                HStack {
                    Text("This session").textStyle(TypeStyle.trayTitle.sized(17)).foregroundStyle(darkInk)
                    Spacer()
                    Text("\(model?.entries.count ?? 0) scanned").textStyle(.label.withFace(.regular)).foregroundStyle(darkInk2)
                }
                .padding(.horizontal, Space.xs)
                .padding(.bottom, 2)

                if let model, !model.entries.isEmpty {
                    ScanTally(known: model.knownCount, added: model.addedCount, toName: model.toNameCount)
                        .padding(.horizontal, Space.xs)
                        .padding(.bottom, 6)
                }

                if !BarcodeScanner.isAvailable {
                    manualEntry
                }

                if let model, !model.entries.isEmpty {
                    ScrollView {
                        VStack(spacing: 0) {
                            ForEach(model.entries) { entry in
                                ScanEntryRow(
                                    code: entry.code,
                                    outcome: entry.outcome,
                                    mergeSuggestion: entry.mergeSuggestion?.name,
                                    receiptLines: entry.receiptLines.count,
                                    onName: { nameTray(entry, model: model) },
                                    onRetry: { model.retry(entry) },
                                    onLinkLines: { linkLines(entry) },
                                    onMerge: { Task { await model.merge(entry) } },
                                    onDismissMerge: { model.dismissMerge(entry) }
                                )
                                .transition(.opacity.combined(with: .offset(y: -8)))
                            }
                        }
                        .animation(Motion.spring, value: model.entries.map(\.id))
                    }
                    .frame(maxHeight: 210)
                    .scrollBounceBehavior(.basedOnSize)
                } else {
                    Text(BarcodeScanner.isAvailable ? "Point the camera at a barcode." : "Type a barcode to look it up.")
                        .textStyle(.body)
                        .foregroundStyle(darkInk2)
                        .padding(.horizontal, Space.xs)
                        .padding(.vertical, Space.l)
                }

                Button("Done") { done() }
                    .buttonStyle(.shop(.tinted(text: darkInk, tint: darkLine)))
                    .padding(.top, Space.ml)
            }
        }
        .padding(.top, 18)
        .padding(.horizontal, Space.xl)
        .padding(.bottom, Space.xl)
        .background(darkTray, in: RoundedRectangle.continuous(Radius.tray))
        .padding(.horizontal, Space.trayInset)
        .padding(.bottom, Space.trayInset)
        .ignoresSafeArea(.container, edges: .bottom)
        .animation(Motion.heightSpring, value: showSummary)
    }

    // MARK: Done and summary (§5.5)

    private func done() {
        guard let model, model.createdAny else { close(); return }
        Haptics.play(.light)
        showSummary = true
    }

    private func close() {
        app.capture = nil
        Task { await app.productReview.refreshCount() }
    }

    private func summary(_ model: UnpackModel) -> some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            Text("Session done").textStyle(TypeStyle.trayTitle.sized(17)).foregroundStyle(darkInk)
                .padding(.horizontal, Space.xs)
            HStack(spacing: Space.m) {
                summaryCell(model.knownCount, "known", Color(hex: 0x3DDC84))
                summaryCell(model.addedCount, "added", Color(hex: 0x4DA3FF))
                summaryCell(model.toNameCount, "to name", Color(hex: 0xFFB340))
            }
            if model.reviewCount > 0 {
                Button(model.reviewCount == 1 ? "Review 1 new product" : "Review \(model.reviewCount) new products") {
                    app.capture = nil
                    app.tab = .products
                    app.push(.productReview)
                }
                .buttonStyle(.shop(.tinted(text: Color(hex: 0x0C0C0B), tint: darkInk)))
            }
            if model.linkableCount > 0 {
                Button("Link receipt lines (\(model.linkableCount))") {
                    if let entry = model.entries.first(where: { !$0.receiptLines.isEmpty }) { linkLines(entry) }
                }
                .textStyle(.label)
                .foregroundStyle(Color(hex: 0x4DA3FF))
                .buttonStyle(.press(haptic: .selection))
                .frame(maxWidth: .infinity)
            }
            Button("Done") { close() }
                .buttonStyle(.shop(.tinted(text: darkInk, tint: darkLine)))
        }
    }

    private func summaryCell(_ value: Int, _ label: String, _ color: Color) -> some View {
        VStack(spacing: 2) {
            Text("\(value)").textStyle(TypeStyle.trayTitle.sized(22)).foregroundStyle(color)
            Text(label).textStyle(.rowSub).foregroundStyle(darkInk2)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, Space.l)
        .background(darkLine, in: RoundedRectangle.continuous(Radius.l))
    }

    private var manualEntry: some View {
        HStack(spacing: Space.m) {
            TextField("", text: $manualCode, prompt: Text("Barcode").foregroundStyle(darkInk2))
                .keyboardType(.numberPad)
                .textStyle(TypeStyle.mono.sized(16))
                .foregroundStyle(darkInk)
                .padding(.horizontal, Space.lx)
                .frame(height: 44)
                .background(darkLine, in: Capsule())
            Button("Look up") {
                let code = manualCode.filter(\.isNumber)
                guard !code.isEmpty else { return }
                model?.scanned(code)
                manualCode = ""
            }
            .buttonStyle(.shop(.tinted(text: Color(hex: 0x0C0C0B), tint: darkInk), size: .small, fullWidth: false))
        }
    }

    private func nameTray(_ entry: UnpackModel.Entry, model: UnpackModel) {
        trays.present("unpack-name-\(entry.code)") {
            UnpackNameTray(code: entry.code) { text in
                Task { await model.name(entry, text) }
            }
        }
    }

    /// C.3.3: receipt lines that could belong to the product (opened from the "N on receipts" link).
    private func linkLines(_ entry: UnpackModel.Entry) {
        guard let product = entry.product, !entry.receiptLines.isEmpty else { return }
        trays.present("link-line-\(product.id)") {
            LinkLineTray(product: product, lines: Array(entry.receiptLines.prefix(4)))
        }
    }
}

/// "Name it": a one-field tray over the dark scanner (the scanner keeps running behind it).
private struct UnpackNameTray: View {
    let code: String
    let onSave: (String) -> Void
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: "Name this product", subtitle: code)
            FieldTile(label: "Name", text: $name, placeholder: "What is it?", background: Palette.trayField)
            Button("Save") {
                let text = name.trimmingCharacters(in: .whitespaces)
                trays.dismiss()
                onSave(text)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            Button("Cancel") { trays.dismiss() }
                .textStyle(.label).foregroundStyle(Palette.ink2)
                .frame(maxWidth: .infinity)
                .buttonStyle(.press(haptic: nil))
        }
    }
}

/// One rounded viewfinder corner (top-left orientation; rotated for the others).
private nonisolated struct CornerShape: Shape {
    func path(in rect: CGRect) -> Path {
        var p = Path()
        p.move(to: CGPoint(x: rect.minX, y: rect.maxY))
        p.addLine(to: CGPoint(x: rect.minX, y: rect.minY + 18))
        p.addQuadCurve(to: CGPoint(x: rect.minX + 18, y: rect.minY), control: CGPoint(x: rect.minX, y: rect.minY))
        p.addLine(to: CGPoint(x: rect.maxX, y: rect.minY))
        return p
    }
}

// MARK: - C.3.3 Link to receipt line

struct LinkLineTray: View {
    let product: Product
    let lines: [UnmatchedLine]

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var selected: Int64?
    @State private var linking = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            HStack(alignment: .top) {
                HStack(spacing: Space.ml) {
                    IconTile(icon: .check, color: Accent.green, size: 34)
                    VStack(alignment: .leading, spacing: 0) {
                        Text("\(product.name) saved").textStyle(TypeStyle.trayTitle.sized(17)).foregroundStyle(Palette.ink)
                        Text("Is it on one of these receipts?").textStyle(.rowSub).foregroundStyle(Palette.ink2)
                    }
                }
                Spacer()
                IconButton.close { trays.dismiss() }
            }
            .padding(.horizontal, Space.xs)

            VStack(spacing: 0) {
                ForEach(Array(lines.enumerated()), id: \.element.id) { index, item in
                    row(item, best: index == 0)
                }
            }
            .padding(.horizontal, Space.xs)

            HStack(spacing: Space.m) {
                Button("Skip") { trays.dismiss() }
                    .buttonStyle(.shop(.secondary))
                Button {
                    Task { await link() }
                } label: {
                    LoadingLabel(title: "Link and remember", isLoading: linking)
                }
                .buttonStyle(.shop(haptic: .medium))
                .layoutPriority(1.6)
                .disabled(selected == nil || linking)
            }
        }
        .onAppear { if selected == nil { selected = lines.first?.line.id } }
    }

    private func row(_ item: UnmatchedLine, best: Bool) -> some View {
        let on = selected == item.line.id
        let when = item.purchasedAt.map { Dates.dayLabel($0) } ?? ""
        let meta = [item.shopName, when, Money.format(item.line.lineTotalCents), best ? "best match" : nil]
            .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
        return Button {
            Haptics.play(.selection)
            selected = item.line.id
        } label: {
            HStack(spacing: Space.l) {
                ShopMark(name: item.shopName, size: Size.shopMarkHeader)
                VStack(alignment: .leading, spacing: 1) {
                    Text(item.line.rawText).textStyle(TypeStyle.mono.sized(14)).foregroundStyle(Palette.ink).lineLimit(1)
                    Text(meta).textStyle(.rowSub).foregroundStyle(Palette.ink2).lineLimit(1)
                }
                Spacer()
                ZStack {
                    Circle().strokeBorder(Palette.ink3, lineWidth: 1.5).opacity(on ? 0 : 1)
                    Circle().fill(Accent.blue).opacity(on ? 1 : 0)
                    Image(icon: .check).font(.system(size: 10, weight: .heavy)).foregroundStyle(.white).opacity(on ? 1 : 0)
                }
                .frame(width: 22, height: 22)
            }
            .frame(height: 56)
            .hairline()
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: nil))
    }

    private func link() async {
        guard let selected else { return }
        linking = true
        defer { linking = false }
        do {
            let result = try await app.api.linkLine(selected, productId: product.id, remember: true)
            let extra = result.alsoLinked > 0 ? " and \(result.alsoLinked) more" : ""
            app.toasts.show("Linked\(extra)", tone: .green)
            trays.dismiss()
            Task { await app.home.refresh() }
        } catch let error as APIError {
            app.toasts.error(error.message)
        } catch {}
    }
}
