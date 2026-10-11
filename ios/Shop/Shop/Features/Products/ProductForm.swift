import SwiftUI
import UIKit

/// Form state shared by the product tray and the nutrition tray (trays replace each other,
/// so the state can't live in either view).
@MainActor
@Observable
final class ProductDraft {
    enum Mode {
        case create(name: String, barcode: String?, prefill: OpenFoodFactsPrefill?)
        case edit(Product)
    }

    let mode: Mode
    var name: String
    var brand: String
    var size: String
    var categoryId: Int64?
    var barcode: String
    var nutrition: Nutrition?
    var nutritionTouched = false
    /// Nutrition came from a barcode scan (saved with its Open Food Facts source, not as manual).
    var nutritionFromScan = false
    var scannedCode: String?
    var photo: UIImage?
    let prefill: OpenFoodFactsPrefill?

    init(mode: Mode) {
        self.mode = mode
        switch mode {
        case .create(let name, let barcode, let prefill):
            self.prefill = prefill
            self.name = prefill?.name ?? name
            brand = prefill?.brand ?? ""
            size = Quantity.netSize(prefill?.netQuantity, unit: prefill?.netUnit) ?? ""
            self.barcode = barcode ?? prefill?.code ?? ""
            nutrition = prefill?.nutrition
        case .edit(let product):
            prefill = nil
            name = product.name
            brand = product.brand ?? ""
            size = product.sizeText ?? ""
            categoryId = product.categoryId
            barcode = product.barcode ?? ""
            nutrition = product.nutrition
        }
    }

    var existing: Product? {
        if case .edit(let p) = mode { return p }
        return nil
    }

    /// What the last barcode scan did, for the line under the Barcode row.
    enum ScanFeedback: Equatable {
        case filled([String])
        case nothingNew
        case notFound
        case unreachable
        case invalid
        case belongsTo(Product)

        static func == (a: ScanFeedback, b: ScanFeedback) -> Bool {
            switch (a, b) {
            case (.filled(let x), .filled(let y)): x == y
            case (.belongsTo(let x), .belongsTo(let y)): x.id == y.id
            case (.nothingNew, .nothingNew), (.notFound, .notFound), (.unreachable, .unreachable), (.invalid, .invalid): true
            default: false
            }
        }
    }

    var scanFeedback: ScanFeedback?

    /// Barcode scanned or typed in the form: sets the barcode and fills what Open Food Facts knows.
    /// Typed values stay; empty ones are filled. Nutrition is replaced unless the user edited it.
    /// A code that belongs to another product is not applied.
    /// EAN-8, UPC-A, EAN-13 or ITF-14 length, digits only.
    static func isValidBarcode(_ code: String) -> Bool {
        code.allSatisfy(\.isNumber) && [8, 12, 13, 14].contains(code.count)
    }

    @discardableResult
    func apply(scan code: String, lookup: BarcodeLookup?) -> ScanFeedback {
        guard Self.isValidBarcode(code) else {
            scanFeedback = .invalid
            return .invalid
        }
        let feedback = Self.feedback(for: code, lookup: lookup, into: self)
        scanFeedback = feedback
        return feedback
    }

    func clearBarcode() {
        barcode = ""
        scannedCode = nil
        scanFeedback = nil
    }

    private static func feedback(for code: String, lookup: BarcodeLookup?, into d: ProductDraft) -> ScanFeedback {
        if let other = lookup?.product, other.id != d.existing?.id { return .belongsTo(other) }
        d.barcode = code
        d.scannedCode = code
        guard let prefill = lookup?.prefill else { return .notFound }
        var filled: [String] = []
        if let n = prefill.nutrition, !n.isEmpty, !d.nutritionTouched {
            d.nutrition = n
            d.nutritionFromScan = true
            filled.append("nutrition")
        }
        if d.name.trimmingCharacters(in: .whitespaces).isEmpty, !prefill.name.isEmpty { d.name = prefill.name; filled.append("name") }
        if d.brand.isEmpty, let b = prefill.brand, !b.isEmpty { d.brand = b; filled.append("brand") }
        if d.size.isEmpty, let q = Quantity.netSize(prefill.netQuantity, unit: prefill.netUnit) { d.size = q; filled.append("size") }
        return filled.isEmpty ? .nothingNew : .filled(filled)
    }

    var isValid: Bool { !name.trimmingCharacters(in: .whitespaces).isEmpty }

    /// "312 kcal · 9,1 g protein per 100 g"
    var nutritionSummary: String {
        guard let n = nutrition, !n.isEmpty else { return "Optional · per 100 g or 100 ml" }
        var parts: [String] = []
        if let k = n.energyKcal { parts.append("\(Quantity.decimal(k, maxFraction: 0)) kcal") }
        if let p = n.protein { parts.append("\(Quantity.decimal(p, maxFraction: 1)) g protein") }
        return parts.joined(separator: " · ") + " per \(n.per == "100ml" ? "100 ml" : "100 g")"
    }

    /// "200 g" → (200, "g"); "1,5 l" → (1500, "ml"); "10 st" → (10, "pc").
    static func parseSize(_ text: String) -> (Double, String)? {
        let s = text.lowercased().replacingOccurrences(of: ",", with: ".").trimmingCharacters(in: .whitespaces)
        guard !s.isEmpty else { return nil }
        let number = s.prefix { $0.isNumber || $0 == "." }
        guard let value = Double(number) else { return nil }
        let unit = s.dropFirst(number.count).trimmingCharacters(in: .whitespaces)
        switch unit {
        case "g", "gr", "gram": return (value, "g")
        case "kg", "kilo": return (value * 1000, "g")
        case "ml": return (value, "ml")
        case "cl": return (value * 10, "ml")
        case "l", "ltr", "liter", "litre": return (value * 1000, "ml")
        case "", "pc", "st", "stuks", "stuk", "x": return (value, "pc")
        default: return nil
        }
    }

    func input() -> ProductInput {
        let parsed = Self.parseSize(size)
        var input = existing?.input ?? ProductInput(name: name)
        input.name = name.trimmingCharacters(in: .whitespaces)
        input.brand = brand.isEmpty ? nil : brand
        input.categoryId = categoryId
        input.barcode = barcode.isEmpty ? nil : barcode
        input.netQuantity = parsed?.0
        input.netUnit = parsed?.1
        if existing == nil, let prefill {
            input.openfoodfactsCode = prefill.code
            input.imageUrl = photo == nil ? prefill.imageUrl : nil
            if let en = prefill.nameEn, name == prefill.name {
                input.nameEn = en
                input.nameEnSource = "openfoodfacts"
            }
        }
        if existing != nil, let code = scannedCode { input.openfoodfactsCode = code }
        if var n = nutrition, !n.isEmpty {
            if nutritionTouched { n.source = "manual"; nutritionFromScan = false }
            input.nutrition = n
        } else {
            input.nutrition = nil
        }
        return input
    }
}

// MARK: - C.3.1 / P.2 / P.1.2 Product form

struct ProductFormTray: View {
    let onSaved: (Product) -> Void
    @State private var draft: ProductDraft

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var saving = false
    @State private var showCamera = false
    @State private var scanning = false
    @State private var lookingUp = false

    init(mode: ProductDraft.Mode, onSaved: @escaping (Product) -> Void) {
        _draft = State(initialValue: ProductDraft(mode: mode))
        self.onSaved = onSaved
    }

    init(draft: ProductDraft, onSaved: @escaping (Product) -> Void) {
        _draft = State(initialValue: draft)
        self.onSaved = onSaved
    }

    var body: some View {
        @Bindable var draft = draft
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: draft.existing == nil ? "New product" : "Edit product")

            HStack(spacing: Space.lx) {
                photoTile
                VStack(alignment: .leading, spacing: 6) {
                    if draft.prefill != nil {
                        Text("Filled in from Open Food Facts")
                            .textStyle(.captionMedium)
                            .foregroundStyle(Palette.blueText)
                            .padding(.horizontal, 9)
                            .padding(.vertical, 4)
                            .background(Palette.blueTint, in: RoundedRectangle.continuous(Radius.s))
                    }
                    if draft.barcode.isEmpty {
                        Text(draft.existing == nil ? "Add a photo so it's easy to spot in lists." : "Change the photo or details.")
                            .textStyle(TypeStyle.body.sized(13)).foregroundStyle(Palette.ink2)
                    } else {
                        Text("Check the details.")
                            .textStyle(TypeStyle.body.sized(13))
                            .foregroundStyle(Palette.ink2)
                    }
                }
            }
            .padding(.horizontal, Space.xs)

            Grid(horizontalSpacing: Space.m, verticalSpacing: Space.m) {
                GridRow {
                    FieldTile(label: "Name", text: $draft.name, placeholder: "As sold, e.g. Hummus naturel", background: Palette.trayField)
                        .gridCellColumns(2)
                }
                GridRow {
                    FieldTile(label: "Brand", text: $draft.brand, placeholder: "Optional", background: Palette.trayField)
                    FieldTile(label: "Size", text: $draft.size, placeholder: "200 g", background: Palette.trayField)
                        .textInputAutocapitalization(.never)
                }
            }

            barcodeRow

            VStack(alignment: .leading, spacing: Space.m) {
                Text("Category").textStyle(.rowSub).foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
                FlowLayout(spacing: 6, lineSpacing: 6) {
                    ForEach(app.catalog.categories) { c in
                        Chip(title: c.name, isSelected: draft.categoryId == c.id, dot: c.dotColor, idleBackground: Palette.trayField) {
                            draft.categoryId = draft.categoryId == c.id ? nil : c.id
                        }
                    }
                    Chip(title: "New…", isSelected: false, idleBackground: Palette.trayField) {
                        let d = draft
                        let saved = onSaved
                        trays.present("category-new") {
                            CategoryFormTray(category: nil) { category in
                                d.categoryId = category.id
                                trays.present("product-form") { ProductFormTray(draft: d, onSaved: saved) }
                            }
                        }
                    }
                }
            }

            Button {
                let d = draft
                let saved = onSaved
                trays.present("nutrition") {
                    NutritionTray(draft: d) {
                        trays.present("product-form") { ProductFormTray(draft: d, onSaved: saved) }
                    }
                }
            } label: {
                HStack(spacing: Space.l) {
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Nutrition").textStyle(.rowTitle).foregroundStyle(Palette.ink)
                        Text(draft.nutritionSummary).textStyle(.rowSub).foregroundStyle(Palette.ink2)
                    }
                    Spacer()
                    IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                }
                .padding(.horizontal, Space.lx)
                .frame(height: 52)
                .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            }
            .buttonStyle(.press(haptic: .light))

            Button {
                Task { await save() }
            } label: {
                LoadingLabel(title: "Save product", isLoading: saving)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(!draft.isValid || saving)
        }
        .fullScreenCover(isPresented: $scanning) {
            BarcodeScanCover(onCode: scanned, onCancel: { scanning = false })
        }
        .fullScreenCover(isPresented: $showCamera) {
            CameraPicker { image in
                draft.photo = image
                showCamera = false
            } onCancel: {
                showCamera = false
            }
            .ignoresSafeArea()
        }
    }

    private var barcodeRow: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: Space.s) {
                Button { scanning = true } label: {
                    HStack(spacing: Space.m) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Barcode").textStyle(.caption).foregroundStyle(Palette.ink2)
                            Text(draft.barcode.isEmpty ? "Not set" : draft.barcode)
                                .textStyle(TypeStyle.mono.sized(16))
                                .foregroundStyle(draft.barcode.isEmpty ? Palette.ink3 : Palette.ink)
                        }
                        Spacer()
                        if lookingUp {
                            ProgressView().controlSize(.small).tint(Palette.ink2)
                        } else {
                            Text(draft.barcode.isEmpty ? "Scan" : "Rescan")
                                .textStyle(.label).foregroundStyle(Palette.blueText)
                                .frame(minHeight: 44)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: .light))
                .disabled(lookingUp)
                if !draft.barcode.isEmpty && !lookingUp {
                    Button { draft.clearBarcode() } label: {
                        IconView(.close, size: 11, weight: .bold)
                            .foregroundStyle(Palette.ink2)
                            .frame(width: 28, height: 28)
                            .background(Palette.sunk, in: Circle())
                            .frame(width: 44, height: 44)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: .selection))
                    .accessibilityLabel("Remove barcode")
                }
            }
            .padding(.leading, Space.lx)
            .padding(.trailing, Space.xs)
            .frame(minHeight: 56)
            .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            scanFeedbackLine
        }
    }

    private func openItButton(_ other: Product) -> some View {
        Button("Open it ›") {
            trays.dismiss()
            app.push(.product(other.id))
        }
        .textStyle(.captionMedium).foregroundStyle(Palette.blueText)
        .frame(minHeight: 44)
        .buttonStyle(.press(haptic: .selection))
    }

    @ViewBuilder private var scanFeedbackLine: some View {
        switch draft.scanFeedback {
        case .filled(let parts):
            Text("Filled in from Open Food Facts: \(parts.joined(separator: ", ")).")
                .textStyle(.rowSub).foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
        case .nothingNew:
            Text("Open Food Facts has nothing new for this product.")
                .textStyle(.rowSub).foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
        case .notFound:
            Text("Not on Open Food Facts. Fill in the details yourself.")
                .textStyle(.rowSub).foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
        case .invalid:
            Text("Not a valid barcode. Barcodes have 8, 12, 13 or 14 digits.")
                .textStyle(.rowSub).foregroundStyle(Palette.amberText).padding(.horizontal, Space.xs)
        case .unreachable:
            Text("Couldn't reach Open Food Facts. The code is saved.")
                .textStyle(.rowSub).foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
        case .belongsTo(let other):
            ViewThatFits(in: .horizontal) {
                HStack(spacing: Space.m) {
                    Text("This barcode belongs to \(other.name)").textStyle(.rowSub).foregroundStyle(Palette.amberText).lineLimit(1)
                    Spacer(minLength: 0)
                    openItButton(other)
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text("This barcode belongs to \(other.name)").textStyle(.rowSub).foregroundStyle(Palette.amberText)
                    openItButton(other)
                }
            }
            .padding(.horizontal, Space.xs)
        case nil:
            EmptyView()
        }
    }

    /// Looks the scanned code up (the product catalogue first, then Open Food Facts) and fills the draft.
    private func scanned(_ code: String) {
        scanning = false
        lookingUp = true
        Task {
            defer { lookingUp = false }
            guard ProductDraft.isValidBarcode(code) else {
                draft.apply(scan: code, lookup: nil)
                Haptics.play(.warning)
                return
            }
            do {
                let lookup = try await app.api.lookupBarcode(code)
                switch draft.apply(scan: code, lookup: lookup) {
                case .filled: Haptics.play(.scanNew)
                case .belongsTo, .invalid: Haptics.play(.warning)
                case .nothingNew, .notFound, .unreachable: Haptics.play(.selection)
                }
            } catch let error as APIError {
                if error.isConnectivity {
                    // Keep the code; nutrition can be filled by scanning again later.
                    draft.apply(scan: code, lookup: nil)
                    draft.scanFeedback = .unreachable
                    Haptics.play(.selection)
                } else {
                    Haptics.play(.error)
                    app.toasts.error(error.message)
                }
            } catch {}
        }
    }

    private var photoTile: some View {
        let swatch = CategoryColor.swatch(for: app.catalog.category(draft.categoryId)?.name)
        return ZStack(alignment: .bottomTrailing) {
            Group {
                if let photo = draft.photo {
                    Image(uiImage: photo).resizable().scaledToFill()
                } else if let existing = draft.existing {
                    ProductHero(name: existing.name, category: app.catalog.category(existing.categoryId)?.name, imageURL: app.api.productImageURL(existing))
                } else if let url = draft.prefill?.imageUrl.flatMap(URL.init(string:)) {
                    CachedImage(url: url) { $0.resizable().scaledToFill() } placeholder: { swatch.tile }
                } else {
                    ProductHero(name: draft.name, category: app.catalog.category(draft.categoryId)?.name)
                }
            }
            .frame(width: 84, height: 84)
            .clipShape(RoundedRectangle.continuous(22))

            Button { showCamera = true } label: {
                IconView(.camera, size: 15, weight: .semibold)
                    .foregroundStyle(Palette.btnInk)
                    .frame(width: 32, height: 32)
                    .background(Palette.btn, in: Circle())
                    .overlay(Circle().strokeBorder(Palette.tray, lineWidth: 3))
            }
            .buttonStyle(.press(scale: Motion.pillScale, haptic: .light))
            .offset(x: 6, y: 6)
            .accessibilityLabel("Take photo")
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        do {
            var product: Product
            if let existing = draft.existing {
                product = try await app.api.updateProduct(existing.id, draft.input())
                if draft.nutritionTouched || draft.nutritionFromScan {
                    if let n = draft.nutrition, !n.isEmpty {
                        product = try await app.api.putNutrition(existing.id, n)
                    } else if existing.nutrition != nil {
                        try await app.api.deleteNutrition(existing.id)
                    }
                }
            } else {
                product = try await app.api.createProduct(draft.input())
            }
            if let photo = draft.photo, let jpeg = Self.jpeg(photo) {
                product = try await app.api.putProductImage(product.id, jpeg: jpeg)
            }
            app.catalog.upsert(product)
            trays.dismiss()
            app.toasts.show("\(product.name) saved", tone: .green)
            onSaved(product)
        } catch APIError.conflict(let problem) {
            app.toasts.error(problem.detail ?? "That barcode belongs to another product.")
        } catch let error as APIError {
            app.toasts.error(error.message)
        } catch {
            app.toasts.error(error.localizedDescription)
        }
    }

    /// Long side ≤ 1024 px (the server resizes too).
    static func jpeg(_ image: UIImage) -> Data? {
        let maxSide: CGFloat = 1024
        let scale = min(1, maxSide / max(image.size.width, image.size.height))
        let size = CGSize(width: image.size.width * scale, height: image.size.height * scale)
        let resized = UIGraphicsImageRenderer(size: size).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }
        return resized.jpegData(compressionQuality: 0.8)
    }
}

// MARK: - C.3.2 Nutrition

struct NutritionTray: View {
    let draft: ProductDraft
    let onDone: () -> Void

    @State private var per = "100g"
    @State private var kcal = ""
    @State private var values: [String: String] = [:]
    @State private var loaded = false

    private struct Field: Identifiable {
        let key: String
        let label: String
        let color: Color
        var id: String { key }
    }

    private let fields: [Field] = [
        Field(key: "fat", label: "Fat", color: Accent.yellow), Field(key: "saturatedFat", label: "Saturates", color: Accent.yellow),
        Field(key: "carbs", label: "Carbohydrates", color: Accent.blue), Field(key: "sugars", label: "Sugars", color: Accent.pink),
        Field(key: "fibre", label: "Fibre", color: Accent.green), Field(key: "protein", label: "Protein", color: Accent.purple),
        Field(key: "salt", label: "Salt", color: Palette.ink3),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Nutrition", subtitle: "\(draft.name.isEmpty ? "Product" : draft.name) · per \(per == "100ml" ? "100 ml" : "100 g")", onClose: onDone)

            HStack(alignment: .bottom) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Energy").textStyle(.label).opacity(0.85)
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        TextField("0", text: $kcal)
                            .keyboardType(.decimalPad)
                            .textStyle(TypeStyle.hero.sized(44))
                            .fixedSize()
                            .frame(minWidth: 60, alignment: .leading)
                        Text("kcal").textStyle(TypeStyle.input).opacity(0.85)
                    }
                }
                Spacer()
                Text(kilojoules).textStyle(.label.withFace(.regular)).opacity(0.85).padding(.bottom, 4)
            }
            .foregroundStyle(Accent.white)
            .tint(.white)
            .padding(.vertical, Space.xl)
            .padding(.horizontal, 18)
            .background(Accent.orange, in: RoundedRectangle.continuous(22))

            SegmentedControl(options: ["100g", "100ml"], title: { $0 == "100g" ? "Per 100 g" : "Per 100 ml" }, selection: $per, track: Palette.trayField)

            LazyVGrid(columns: [GridItem(.flexible(), spacing: Space.m), GridItem(.flexible(), spacing: Space.m)], spacing: Space.m) {
                ForEach(fields) { field in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack(spacing: 6) {
                            RoundedRectangle.continuous(2).fill(field.color).frame(width: 7, height: 7)
                            Text(field.label).textStyle(.caption).foregroundStyle(Palette.ink2)
                        }
                        HStack(alignment: .firstTextBaseline, spacing: 3) {
                            TextField("–", text: binding(field.key))
                                .keyboardType(.decimalPad)
                                .textStyle(TypeStyle.input.sized(18))
                                .foregroundStyle(Palette.ink)
                            Text("g").textStyle(.label.withFace(.regular)).foregroundStyle(Palette.ink2)
                        }
                    }
                    .padding(.vertical, Space.ml)
                    .padding(.horizontal, Space.lx)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
                }
            }

            Text(draft.nutrition?.source == "openfoodfacts" ? "From Open Food Facts. Changing a value marks it as entered by you." : "Values per 100 g or 100 ml, from the label.")
                .textStyle(.rowSub)
                .foregroundStyle(Palette.ink2)
                .padding(.horizontal, Space.xs)

            Button("Done") {
                commit()
                onDone()
            }
            .buttonStyle(.shop())
        }
        .onAppear(perform: load)
    }

    private var kilojoules: String {
        guard let k = Quantity.parse(kcal) else { return "– kJ" }
        return "\(Quantity.decimal((k * 4.184).rounded(), maxFraction: 0)) kJ"
    }

    private func binding(_ key: String) -> Binding<String> {
        Binding(get: { values[key] ?? "" }, set: { values[key] = $0 })
    }

    private func load() {
        guard !loaded else { return }
        loaded = true
        let n = draft.nutrition
        per = n?.per ?? "100g"
        kcal = n?.energyKcal.map { Quantity.decimal($0, maxFraction: 1) } ?? ""
        let pairs: [(String, Double?)] = [("fat", n?.fat), ("saturatedFat", n?.saturatedFat), ("carbs", n?.carbs), ("sugars", n?.sugars),
                                          ("fibre", n?.fibre), ("protein", n?.protein), ("salt", n?.salt)]
        for (k, v) in pairs { values[k] = v.map { Quantity.decimal($0, maxFraction: 2) } ?? "" }
    }

    private func commit() {
        func v(_ key: String) -> Double? { Quantity.parse(values[key] ?? "") }
        let new = Nutrition(per: per, energyKcal: Quantity.parse(kcal), fat: v("fat"), saturatedFat: v("saturatedFat"),
                            carbs: v("carbs"), sugars: v("sugars"), fibre: v("fibre"), protein: v("protein"), salt: v("salt"),
                            source: draft.nutrition?.source ?? "manual")
        if new != draft.nutrition {
            var changed = new
            changed.source = "manual"
            draft.nutrition = changed
            draft.nutritionTouched = true
        }
    }
}

// MARK: - Camera

/// Takes a product photo with the system camera.
struct CameraPicker: UIViewControllerRepresentable {
    let onImage: (UIImage) -> Void
    let onCancel: () -> Void

    func makeUIViewController(context: Context) -> UIImagePickerController {
        let vc = UIImagePickerController()
        vc.sourceType = UIImagePickerController.isSourceTypeAvailable(.camera) ? .camera : .photoLibrary
        vc.delegate = context.coordinator
        return vc
    }

    func updateUIViewController(_ vc: UIImagePickerController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(parent: self) }

    final class Coordinator: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate {
        let parent: CameraPicker

        init(parent: CameraPicker) { self.parent = parent }

        func imagePickerController(_ picker: UIImagePickerController, didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]) {
            if let image = info[.originalImage] as? UIImage {
                parent.onImage(image)
            } else {
                parent.onCancel()
            }
        }

        func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
            parent.onCancel()
        }
    }
}


/// Full-screen barcode scanner for the product form: scan once, or type the code when the
/// device has no camera.
struct BarcodeScanCover: View {
    let onCode: (String) -> Void
    let onCancel: () -> Void
    @State private var manual = ""
    @State private var found = false

    var body: some View {
        ZStack {
            Color(hex: 0x111211).ignoresSafeArea()
            if BarcodeScanner.isAvailable {
                BarcodeScanner { code in
                    guard !found else { return }
                    found = true
                    onCode(code)
                }
                .ignoresSafeArea()
            }
            VStack {
                HStack {
                    IconButton(icon: .close, label: "Close", foreground: .white, background: .white.opacity(0.14), action: onCancel)
                    Spacer()
                }
                .padding(.horizontal, Space.edge)
                .padding(.top, Space.l)
                Spacer()
                VStack(spacing: Space.l) {
                    Text(BarcodeScanner.isAvailable ? "Point the camera at the barcode." : "Type the barcode.")
                        .textStyle(.body).foregroundStyle(Color(hex: 0xF4F4F1))
                    if !BarcodeScanner.isAvailable {
                        HStack(spacing: Space.m) {
                            TextField("", text: $manual, prompt: Text("Barcode").foregroundStyle(Color(hex: 0x8F8F8A)))
                                .keyboardType(.numberPad)
                                .textStyle(TypeStyle.mono.sized(16))
                                .foregroundStyle(Color(hex: 0xF4F4F1))
                                .padding(.horizontal, Space.lx)
                                .frame(height: 44)
                                .background(Color(hex: 0x2A2A27), in: Capsule())
                            Button("Use") {
                                let code = manual.filter(\.isNumber)
                                if !code.isEmpty { onCode(code) }
                            }
                            .buttonStyle(.shop(.tinted(text: Color(hex: 0x0C0C0B), tint: Color(hex: 0xF4F4F1)), size: .small, fullWidth: false))
                        }
                        .padding(.horizontal, Space.edge)
                    }
                }
                .padding(.bottom, 60)
            }
        }
    }
}
