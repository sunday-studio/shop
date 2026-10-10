import PhotosUI
import SwiftUI

// MARK: - C · Add tray

struct AddTray: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Add")
            HStack(spacing: Space.m) {
                FeatureCard(icon: .scan, title: "Scan receipt", caption: "Paper receipt, add pages for long ones", color: Accent.blue) {
                    app.startCapture(.receipt)
                }
                FeatureCard(icon: .barcode, title: "Unpack groceries", caption: "Scan barcodes, add new products", color: Accent.green) {
                    app.startCapture(.unpack)
                }
            }
            FeatureCard(icon: .image, title: "Import receipts", caption: "From Photos or Files, old ones too", color: Accent.orange, height: 96) {
                app.openImportSources()
            }
            if let last = lastScan {
                Text(last)
                    .textStyle(.rowSub)
                    .foregroundStyle(Palette.ink2)
                    .frame(maxWidth: .infinity)
                    .padding(.bottom, 2)
            }
        }
    }

    private var lastScan: String? {
        if let item = app.uploads.items.first {
            return "Last scan: \(item.shopName), \(Dates.relativeStamp(item.createdAt))"
        }
        if let r = app.home.receipts.first {
            return "Last scan: \(app.catalog.shopName(r.shopId)), \(Dates.relativeStamp(r.createdAt))"
        }
        return nil
    }
}

// MARK: - Capture cover

struct CaptureCover: View {
    let mode: CaptureMode

    var body: some View {
        switch mode {
        case .receipt: ReceiptCaptureView()
        case .unpack: UnpackView()
        }
    }
}

// MARK: - C.1 + C.2 · Scan, then shop & date

struct ReceiptCaptureView: View {
    private static let maxPages = 5

    @Environment(AppModel.self) private var app
    @State private var pages: [UIImage] = []
    @State private var preview: UIImage?
    @State private var scanning = false
    @State private var appendNext = false
    @State private var shopId: Int64?
    @State private var date: Date?
    @State private var adding = false
    @State private var photoItems: [PhotosPickerItem] = []
    @State private var trays = TrayCoordinator()

    var body: some View {
        ZStack(alignment: .bottom) {
            RadialGradient(colors: [Color(hex: 0x3A3D3B), Color(hex: 0x121312)], center: UnitPoint(x: 0.5, y: 0.2), startRadius: 10, endRadius: 520)
                .ignoresSafeArea()

            if let preview {
                Image(uiImage: preview)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: 200, maxHeight: 280)
                    .clipShape(RoundedRectangle.continuous(6))
                    .rotationEffect(.degrees(-3))
                    .shadow(color: .black.opacity(0.5), radius: 25, y: 24)
                    .frame(maxHeight: .infinity, alignment: .top)
                    .padding(.top, 60)
                    .transition(.opacity)
            } else {
                noPagesPlaceholder
            }

            if !pages.isEmpty {
                shopTray
                    .transition(.tray)
            }
        }
        .trayHost(trays)
        .fullScreenCover(isPresented: $scanning) {
            DocumentScanner { newPages in
                scanning = false
                receive(newPages)
            } onCancel: {
                scanning = false
                appendNext = false
                if pages.isEmpty { app.capture = nil }
            }
            .ignoresSafeArea()
        }
        .onChange(of: photoItems) { _, items in
            Task { await loadPhotos(items) }
        }
        .onAppear {
            if shopId == nil { shopId = app.catalog.lastShop?.id }
            guard pages.isEmpty else { return }
            if DocumentScanner.isAvailable { scanning = true }
        }
        .animation(Motion.spring, value: pages.isEmpty)
    }

    // MARK: Pieces

    private var noPagesPlaceholder: some View {
        VStack(spacing: Space.xl) {
            Spacer()
            PaperReceipt(lines: 7)
                .frame(width: 150, height: 200)
                .rotationEffect(.degrees(-3))
                .opacity(0.9)
            Text(DocumentScanner.isAvailable ? "Opening the scanner…" : "No camera here")
                .textStyle(.emptyTitle)
                .foregroundStyle(Color(hex: 0xF4F4F1))
            Text(DocumentScanner.isAvailable ? "Hold the receipt flat on a dark surface." : "Pick a photo of a receipt instead.")
                .textStyle(.body)
                .foregroundStyle(Color(hex: 0x8F8F8A))
            Spacer()
            VStack(spacing: Space.m) {
                if DocumentScanner.isAvailable {
                    Button("Open scanner") { scanning = true }
                        .buttonStyle(.shop(.solid(Accent.blue)))
                }
                PhotosPicker(selection: $photoItems, maxSelectionCount: Self.maxPages, matching: .images) {
                    Text("Choose a photo")
                }
                .buttonStyle(.shop(.tinted(text: Color(hex: 0xF4F4F1), tint: Color(hex: 0x2A2A27))))
                Button("Cancel") { app.capture = nil }
                    .buttonStyle(.press(haptic: nil))
                    .textStyle(.rowTitle)
                    .foregroundStyle(Color(hex: 0x8F8F8A))
                    .frame(height: 44)
            }
            .padding(.horizontal, Space.gutter)
            .padding(.bottom, Space.xxl)
        }
    }

    private var shopTray: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            HStack {
                Text("Which shop?").textStyle(.trayTitle).foregroundStyle(Palette.ink)
                Spacer()
                IconButton(icon: .close, label: "Discard scan", size: Size.iconButtonSmall, glyph: 13, foreground: Palette.ink2, background: Palette.trayField) {
                    app.capture = nil
                }
            }
            .padding(.horizontal, Space.xs)

            HStack(spacing: Space.m) {
                HStack(spacing: 6) {
                    IconView(.scan, size: 14)
                    Text(pages.count == 1 ? "1 page" : "\(pages.count) pages")
                }
                .textStyle(.label)
                .foregroundStyle(Palette.ink)
                .padding(.leading, 8)
                .padding(.trailing, 12)
                .frame(height: 30)
                .background(Palette.trayField, in: Capsule())
                if DocumentScanner.isAvailable && pages.count < Self.maxPages {
                    Button("+ Page") {
                        appendNext = true
                        scanning = true
                    }
                    .textStyle(.label)
                    .foregroundStyle(Palette.ink)
                    .padding(.horizontal, 12)
                    .frame(height: 30)
                    .background(Palette.trayField, in: Capsule())
                    .buttonStyle(.pill)
                }
                Spacer()
                Button("Retake") {
                    appendNext = false
                    if DocumentScanner.isAvailable { scanning = true } else { pages = []; preview = nil }
                }
                .textStyle(.label)
                .foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: .selection))
            }
            .padding(.horizontal, Space.xs)

            ScrollView {
                VStack(spacing: 0) {
                    ForEach(orderedShops) { shop in
                        shopRow(shop)
                    }
                    Button {
                        trays.present("new-shop") {
                            ShopFormTray(shop: nil) { saved in shopId = saved.id }
                        }
                    } label: {
                        HStack(spacing: Space.l) {
                            RoundedRectangle.continuous(Radius.m)
                                .strokeBorder(Palette.ink3, style: StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
                                .frame(width: 34, height: 34)
                                .overlay(IconView(.plus, size: 15, weight: .semibold))
                            Text("Other shop").textStyle(.rowTitle)
                            Spacer()
                        }
                        .foregroundStyle(Palette.blueText)
                        .frame(height: 50)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: nil))
                }
                .padding(.horizontal, Space.xs)
            }
            .frame(maxHeight: 290)
            .scrollBounceBehavior(.basedOnSize)

            Button {
                trays.present("receipt-date") {
                    ReceiptDateTray(date: $date)
                }
            } label: {
                HStack(spacing: Space.ml) {
                    Text("Date").textStyle(TypeStyle.input.sized(15)).foregroundStyle(Palette.ink)
                    Spacer()
                    Text(date.map { "\(Dates.shortDay($0)) \(Dates.time($0))" } ?? "Read from receipt")
                        .textStyle(.body)
                        .foregroundStyle(Palette.ink2)
                    IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                }
                .padding(.horizontal, Space.lx)
                .frame(height: 48)
                .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            }
            .buttonStyle(.press(haptic: nil))

            Button {
                add()
            } label: {
                LoadingLabel(title: "Add receipt", isLoading: adding)
            }
            .buttonStyle(.shop(haptic: nil))
            .disabled(shopId == nil || adding)
        }
        .padding(.top, 22)
        .padding(.horizontal, Space.xl)
        .padding(.bottom, Space.xl)
        .background(Palette.tray, in: RoundedRectangle.continuous(Radius.tray))
        .padding(.horizontal, Space.trayInset)
        .padding(.bottom, Space.trayInset)
        .ignoresSafeArea(.container, edges: .bottom)
    }

    private var orderedShops: [Shop] {
        let last = app.catalog.lastShopId
        return app.catalog.shops.sorted { a, b in
            if a.id == last { return true }
            if b.id == last { return false }
            return a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
        }
    }

    private func shopRow(_ shop: Shop) -> some View {
        let on = shopId == shop.id
        let sub = [shop.branch, shop.id == app.catalog.lastShopId ? "last used" : nil].compactMap { $0 }.joined(separator: " · ")
        return Button {
            Haptics.play(.selection)
            shopId = shop.id
        } label: {
            HStack(spacing: Space.l) {
                ShopMark(name: shop.name, size: Size.shopMarkHeader)
                VStack(alignment: .leading, spacing: 1) {
                    Text(shop.name).textStyle(.rowTitle).foregroundStyle(Palette.ink)
                    if !sub.isEmpty { Text(sub).textStyle(.rowSub).foregroundStyle(Palette.ink2) }
                }
                Spacer()
                ZStack {
                    Circle().strokeBorder(Palette.ink3, lineWidth: 1.5).opacity(on ? 0 : 1)
                    Circle().fill(Accent.blue).opacity(on ? 1 : 0)
                    Image(icon: .check).font(.system(size: 10, weight: .heavy)).foregroundStyle(.white).opacity(on ? 1 : 0)
                }
                .frame(width: 22, height: 22)
                .animation(Motion.pop, value: on)
            }
            .frame(height: 52)
            .hairline()
            .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: nil))
    }

    // MARK: Actions

    private func receive(_ newPages: [UIImage]) {
        guard !newPages.isEmpty else { return }
        if appendNext { pages = Array((pages + newPages).prefix(Self.maxPages)) } else { pages = Array(newPages.prefix(Self.maxPages)) }
        appendNext = false
        preview = pages.first.map { Stitcher.thumbnail($0) }
        Haptics.play(.light)
    }

    private func loadPhotos(_ items: [PhotosPickerItem]) async {
        var images: [UIImage] = []
        for item in items {
            if let data = try? await item.loadTransferable(type: Data.self), let image = UIImage(data: data) {
                images.append(image)
            }
        }
        photoItems = []
        if images.isEmpty && !items.isEmpty { app.toasts.error("Couldn't read that photo.") }
        receive(images)
    }

    private func add() {
        guard let shop = app.catalog.shop(shopId) else { return }
        adding = true
        guard let jpeg = Stitcher.stitch(pages) else {
            adding = false
            app.toasts.error("Couldn't prepare the scan.")
            return
        }
        app.uploads.enqueue(jpeg: jpeg, shop: shop, purchasedAt: date, pages: pages.count)
        app.catalog.lastShopId = shop.id
        Haptics.play(.uploadQueued)
        app.capture = nil
        app.tab = .home
        app.toasts.show(app.network.isOnline ? "Receipt added · uploading" : "Saved · uploads when you're online")
    }
}

/// Date override for a scan: "Read from receipt" or a chosen date and time.
struct ReceiptDateTray: View {
    @Binding var date: Date?
    @Environment(TrayCoordinator.self) private var trays
    @State private var picked = Date()

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: "Receipt date")
            DatePicker("", selection: $picked, in: ...Date.now)
                .datePickerStyle(.graphical)
                .labelsHidden()
                .tint(Accent.blue)
                .environment(\.timeZone, Dates.timeZone)
            HStack(spacing: Space.m) {
                Button("Read from receipt") {
                    date = nil
                    trays.dismiss()
                }
                .buttonStyle(.shop(.secondary))
                Button("Use this date") {
                    date = picked
                    trays.dismiss()
                }
                .buttonStyle(.shop())
            }
        }
        .onAppear { if let date { picked = date } }
    }
}
