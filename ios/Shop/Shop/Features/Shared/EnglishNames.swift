import SwiftUI

/// One place that decides how a product or receipt line shows its English (docs/PLAN.md §6a):
/// title, subline, badge, source hint and the unknown-word spans to highlight.
struct EnglishDisplay {
    var title: String
    var subline: String?
    var needsEnglish: Bool
    var source: EnglishSourceHint.Source?
    /// Spans into `title` when the status is `partial`.
    var unknown: [UnknownWord]

    init(product: Product) {
        let status = product.nameEnStatus ?? (product.nameEn == nil ? "missing" : "dictionary")
        let dutch = product.name
        switch status {
        case "partial":
            title = product.nameEn ?? dutch
            subline = dutch
            needsEnglish = true
            source = nil
            unknown = (product.nameEnUnknown ?? []).map { UnknownWord(word: $0.word, key: $0.key, start: $0.start, end: $0.end) }
        case "missing":
            title = dutch
            subline = "No English yet"
            needsEnglish = true
            source = nil
            unknown = []
        default:
            let en = product.nameEn.flatMap { $0.isEmpty ? nil : $0 }
            title = en ?? dutch
            subline = en != nil && en?.lowercased() != dutch.lowercased() ? dutch : nil
            needsEnglish = false
            unknown = []
            switch status {
            case "manual": source = .you
            case "openfoodfacts": source = .openFoodFacts
            default: source = .auto
            }
        }
    }
}

// MARK: - Queue

@MainActor
@Observable
final class NeedsEnglishModel {
    private(set) var todo: TranslationTodo?
    private(set) var loadError: APIError?
    private(set) var isLoading = false
    @ObservationIgnored private let app: AppModel
    private static let cacheName = "translation-todo.json"

    init(app: AppModel) {
        self.app = app
        todo = DiskStore.load(TranslationTodo.self, from: Self.cacheName)
    }

    var wordsLeft: Int { todo?.totals.wordsLeft ?? 0 }
    var productsLeft: Int { todo?.totals.productsLeft ?? 0 }

    func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let fresh = try await app.api.translationTodo()
            withAnimation(Motion.collapse) { todo = fresh }
            loadError = nil
            DiskStore.saveInBackground(fresh, to: Self.cacheName)
        } catch let error as APIError {
            loadError = error
        } catch {}
    }
}

struct NeedsEnglishView: View {
    @Environment(AppModel.self) private var app
    @State private var tab = Tab.words
    @State private var highlighted: String?

    enum Tab: String, CaseIterable { case words = "Words", products = "Products" }

    private var model: NeedsEnglishModel { app.needsEnglish }

    var body: some View {
        VStack(spacing: 0) {
            NavHeader {
                Button { app.push(.glossary) } label: {
                    TypeStyle.label.text("Your glossary").foregroundStyle(Palette.blueText)
                }
                .buttonStyle(.press(haptic: .selection))
            }
            List {
                VStack(alignment: .leading, spacing: Space.xs) {
                    TypeStyle.screenTitle.text("Needs English").foregroundStyle(Palette.ink)
                    TypeStyle.body.text("\(model.wordsLeft) word\(model.wordsLeft == 1 ? "" : "s") left · \(model.productsLeft) product\(model.productsLeft == 1 ? "" : "s")")
                        .foregroundStyle(Palette.ink2)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, Space.gutter).padding(.top, Space.l)
                .shopListRow()

                SegmentedControl(options: Tab.allCases, title: { "\($0.rawValue) \($0 == .words ? model.wordsLeft : model.productsLeft)" }, selection: $tab)
                    .padding(.horizontal, Space.edge).padding(.top, Space.lx)
                    .shopListRow()

                content
            }
            .shopListStyle()
            .contentMargins(.bottom, 60, for: .scrollContent)
            .refreshable { await model.load() }
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await model.load() }
    }

    @ViewBuilder
    private var content: some View {
        if let todo = model.todo {
            if todo.totals.wordsLeft == 0 && todo.products.isEmpty {
                EmptyState(title: "All names have English", message: "New products with unknown words will show up here.",
                           action: (title: "Your glossary", run: { app.push(.glossary) })) {
                    GlyphIllustration(icon: .check, color: Accent.green)
                }
                .padding(.top, 60).padding(.horizontal, Space.gutter).shopListRow()
            } else if tab == .words {
                ForEach(todo.words) { word in
                    Button { teach(word.key, word.word) } label: {
                        HStack(spacing: Space.l) {
                            VStack(alignment: .leading, spacing: 2) {
                                TypeStyle.rowTitle.sized(17).text(word.word).foregroundStyle(Palette.ink)
                                TypeStyle.rowSub.text("used in \(word.products) product\(word.products == 1 ? "" : "s")").foregroundStyle(Palette.ink2)
                                if !word.examples.isEmpty {
                                    TypeStyle.caption.text(word.examples.prefix(3).joined(separator: " · ")).foregroundStyle(Palette.ink3).lineLimit(1)
                                }
                            }
                            Spacer()
                            TypeStyle.body.text("\(word.products)").foregroundStyle(Palette.ink2)
                            IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                        }
                        .padding(.vertical, Space.m)
                        .frame(minHeight: 64)
                        .hairline()
                        .background(highlighted == word.key ? Palette.blueTint : Color.clear)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: .selection))
                    .padding(.horizontal, Space.gutter)
                    .shopListRow()
                }
            } else {
                ForEach(todo.products) { product in
                    Button { app.trays.present("english-edit") { EditEnglishTray(productId: product.id) } } label: {
                        HStack(spacing: Space.l) {
                            ShopMark(name: product.shopName ?? "?", size: Size.shopMarkHeader)
                            VStack(alignment: .leading, spacing: 2) {
                                EnglishNameText(text: product.nameEn ?? product.name, unknown: product.nameEnUnknown.map { UnknownWord(word: $0.word, key: $0.key, start: $0.start, end: $0.end) })
                                TypeStyle.rowSub.text(product.shopName ?? product.name).foregroundStyle(Palette.ink2)
                            }
                            Spacer()
                            NeedsEnglishBadge()
                        }
                        .padding(.vertical, Space.m)
                        .frame(minHeight: 60)
                        .hairline()
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.press(haptic: .selection))
                    .padding(.horizontal, Space.gutter)
                    .shopListRow()
                }
            }
        } else if model.loadError != nil {
            ErrorState(message: model.loadError?.message ?? "", offline: model.loadError?.isConnectivity ?? false) { Task { await model.load() } }
                .padding(.horizontal, Space.gutter).shopListRow()
        } else {
            SkeletonRows { SkeletonSimpleRow() }.padding(.horizontal, Space.gutter).shopListRow()
        }
    }

    private func teach(_ key: String, _ word: String) {
        app.trays.present("teach-word") {
            TeachWordTray(key: key, word: word, fromQueue: true) { taught in
                highlighted = nil
                Task { await model.load() }
                _ = taught
            }
        }
    }
}

extension AppModel {
    /// Teaching a word rewrites many English names on the server; pull the catalog again.
    func refreshCatalogNames() async { try? await catalog.refreshProducts() }
}

extension Catalog {
    /// English-first display for a product id (nil when the product isn't in the catalog).
    func englishDisplay(_ id: Int64?) -> EnglishDisplay? {
        product(id).map(EnglishDisplay.init(product:))
    }
}

import AVFoundation

/// Camera permission as the scan flows need it: denied (or restricted) means show "Camera is off".
enum CameraAccess {
    static var isDenied: Bool {
        let args = ProcessInfo.processInfo.arguments
        if args.contains("-mock-camera-denied") { return true }
        guard !args.contains("-mock") else { return false }
        let status = AVCaptureDevice.authorizationStatus(for: .video)
        return status == .denied || status == .restricted
    }

    @MainActor static func openSettings() {
        if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
    }
}
