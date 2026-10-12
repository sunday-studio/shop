import Foundation
import Observation
import SwiftUI

enum AppTab: Int, CaseIterable, Hashable {
    case home, analytics, products, list

    var title: String {
        switch self {
        case .home: "Home"
        case .analytics: "Analytics"
        case .products: "Products"
        case .list: "List"
        }
    }

    var icon: Icon {
        switch self {
        case .home: .home
        case .analytics: .analytics
        case .products: .products
        case .list: .list
        }
    }

    var selectedIcon: Icon {
        switch self {
        case .home: .homeFill
        case .analytics: .analyticsFill
        case .products: .productsFill
        case .list: .listFill
        }
    }
}

/// Pushed screens. Every tab's NavigationStack resolves these through `RouteView`.
enum Route: Hashable {
    case receipt(Int64)
    case receiptImage(Int64)
    case product(Int64)
    case priceHistory(Int64)
    case receiptNames(Int64)
    case shopSpend(Int64, DateRange)
    case categorySpend(String, Int64?, DateRange)
    case priceChanges
    case settings
    case server
    case uploadQueue
    case shops
    case categories
    case search
    case catalog
    case productReview
}

/// Full-screen camera flows started from the Add tray.
enum CaptureMode: String, Identifiable {
    case receipt, unpack
    var id: String { rawValue }
}

enum Appearance: String, CaseIterable {
    case system, light, dark

    var colorScheme: ColorScheme? {
        switch self {
        case .system: nil
        case .light: .light
        case .dark: .dark
        }
    }

    var title: String { rawValue.capitalized }
}

/// The root of the app's state: connection, services, navigation.
@MainActor
@Observable
final class AppModel {
    enum Phase: Equatable {
        case onboarding
        case ready
        /// The server rejected the token; local data is kept until the user pairs again.
        case tokenRejected
    }

    private(set) var phase: Phase
    private(set) var serverURL: URL?
    private(set) var deviceName: String
    var tab: AppTab = .home
    var paths: [AppTab: [Route]] = [:]
    /// Presented camera flow (document scanner + shop picker, or barcode unpacking).
    var capture: CaptureMode?
    /// Receipts being reviewed one after another (from "Receipts to review"). Confirming one
    /// opens the next; Home clears it when it comes back into view.
    var reviewSequence: [Int64] = []
    /// True while a list row is being swiped (and just after), so the row's tap doesn't fire.
    /// Which system picker the root view should show for "Import receipts".
    var importPicker: ImportSource?
    /// The Import screen (full screen) while receipts are being arranged.
    var importFlow: ImportModel?
    /// True between picking and the Import screen appearing, so a cancelled picker can tell.
    var isStartingImport = false
    private(set) var appearance: Appearance
    private(set) var health: Health?
    private(set) var version: VersionInfo?

    let api: APIClient
    let catalog: Catalog
    let uploads: UploadQueue
    let list: ShoppingListStore
    let network: NetworkMonitor
    let trays = TrayCoordinator()
    let toasts = ToastCenter()
    let home: HomeModel
    let isMock: Bool
    @ObservationIgnored lazy var productReview = ProductReviewModel(app: self)

    private static let serverKey = "session.serverURL"
    private static let deviceKey = "session.deviceName"
    private static let tokenAccount = "token"
    private static let appearanceKey = "settings.appearance"

    init(mock: Bool = ProcessInfo.processInfo.arguments.contains("-mock")) {
        LaunchTrace.mark("AppModel init start")
        // Test aid: `-mock -mock-fresh` forgets everything saved by earlier launches (caches, queue,
        // list, preferences) so each UI test starts from the same state.
        if mock, ProcessInfo.processInfo.arguments.contains("-mock-fresh") {
            DiskStore.removeAll()
            if let id = Bundle.main.bundleIdentifier { UserDefaults.standard.removePersistentDomain(forName: id) }
            ImageCache.shared.removeAll()
        }
        isMock = mock
        let storedURL = mock ? URL(string: "http://mock.shop.local:8080") : UserDefaults.standard.url(forKey: Self.serverKey)
        let token = mock ? "st_mock" : Keychain.get(Self.tokenAccount)
        LaunchTrace.mark("keychain done")
        let api = APIClient(transport: mock ? MockServer() : URLSessionTransport(), baseURL: storedURL, token: token)
        self.api = api
        LaunchTrace.mark("api created")
        serverURL = storedURL
        deviceName = UserDefaults.standard.string(forKey: Self.deviceKey) ?? "iPhone"
        catalog = Catalog(api: api)
        LaunchTrace.mark("catalog loaded")
        uploads = UploadQueue(api: api)
        LaunchTrace.mark("upload queue loaded")
        list = ShoppingListStore(api: api, catalog: catalog)
        LaunchTrace.mark("list loaded")
        network = NetworkMonitor()
        home = HomeModel(api: api, catalog: catalog)
        LaunchTrace.mark("home loaded")
        appearance = Appearance(rawValue: UserDefaults.standard.string(forKey: Self.appearanceKey) ?? "") ?? .system
        phase = (storedURL != nil && token != nil) ? .ready : .onboarding

        api.onUnauthorized = { [weak self] in self?.handleUnauthorized() }
        uploads.onUnauthorized = { [weak self] in self?.handleUnauthorized() }
        uploads.onUploaded = { [weak self] item in self?.uploadFinished(item) }
        network.onReconnect = { [weak self] in self?.reconnected() }
        ShopFont.register()
        LaunchTrace.mark("AppModel init done")
    }

    func setAppearance(_ value: Appearance) {
        appearance = value
        UserDefaults.standard.set(value.rawValue, forKey: Self.appearanceKey)
    }

    // MARK: Lifecycle

    /// First appearance and every return to the foreground.
    func refreshOnForeground() async {
        guard phase == .ready else { return }
        uploads.kick(force: true)
        async let h: Void = refreshHealth()
        async let i: Void = home.refresh()
        async let l: Void = list.sync()
        async let r: Void = productReview.refreshCount()
        _ = await (h, i, l, r)
        if catalog.lastSync == nil || (catalog.lastSync ?? .distantPast) < .now.addingTimeInterval(-60 * 30) {
            await catalog.syncAll()
        } else {
            try? await catalog.refreshReference()
        }
    }

    func refreshHealth() async {
        health = try? await api.health()
        if version == nil { version = try? await api.version() }
    }

    private func reconnected() {
        uploads.kick(force: true)
        Task {
            await list.sync()
            await home.refresh()
        }
        // Barcodes scanned offline become products now (safe to repeat).
        Task {
            for code in PendingScans.codes {
                if let response = try? await api.createBarcodeProduct(code) {
                    catalog.upsert(response.product)
                    PendingScans.remove(code)
                }
            }
            await productReview.refreshCount()
        }
    }

    private func uploadFinished(_ item: UploadItem) {
        if item.wasDuplicate {
            toasts.show("Already uploaded · \(item.shopName)", tone: .amber, haptic: .warning)
        }
        Task { await home.refresh() }
    }

    // MARK: Session

    /// After O.3 succeeds: keep the URL and token, then sync.
    func completePairing(serverURL: URL, token: String, deviceName: String) {
        UserDefaults.standard.set(serverURL, forKey: Self.serverKey)
        UserDefaults.standard.set(deviceName, forKey: Self.deviceKey)
        Keychain.set(token, for: Self.tokenAccount)
        self.serverURL = serverURL
        self.deviceName = deviceName
        api.configure(baseURL: serverURL, token: token)
    }

    /// O.4 "Start" (or after re-pairing from the token-rejected screen).
    func enterApp() {
        withAnimation(Motion.ease) { phase = .ready }
        Task { await refreshOnForeground() }
    }

    func handleUnauthorized() {
        guard phase == .ready, !isMock else { return }
        Keychain.set(nil, for: Self.tokenAccount)
        api.setToken(nil)
        Haptics.play(.warning)
        withAnimation(Motion.ease) { phase = .tokenRejected }
    }

    /// Sign out: revoke this device's token, forget the server and local data.
    func signOut() async {
        if !isMock { try? await api.unpairSelf() }
        Keychain.set(nil, for: Self.tokenAccount)
        UserDefaults.standard.removeObject(forKey: Self.serverKey)
        DiskStore.removeAll()
        ImageCache.shared.removeAll()
        uploads.reset()
        catalog.reset()
        list.reset()
        home.reset()
        productReview.reset()
        api.configure(baseURL: nil, token: nil)
        serverURL = nil
        paths = [:]
        tab = .home
        capture = nil
        trays.dismiss()
        withAnimation(Motion.ease) { phase = .onboarding }
    }

    /// Change server from Settings: keeps nothing tied to the old server.
    func changeServer() async {
        await signOut()
    }

    // MARK: Navigation

    func push(_ route: Route, on tab: AppTab? = nil) {
        let t = tab ?? self.tab
        paths[t, default: []].append(route)
    }

    /// Opens the first receipt that needs review and remembers the rest, so the review keeps going.
    func startReview(_ ids: [Int64]) {
        guard let first = ids.first else { return }
        reviewSequence = ids
        push(.receipt(first))
    }

    /// After a receipt is confirmed or deleted inside a review run: show the next one still
    /// waiting, or report there is none. Replaces the current screen so Back still goes Home.
    @discardableResult
    func advanceReview(after id: Int64) -> Bool {
        guard let i = reviewSequence.firstIndex(of: id) else { return false }
        let waiting = Set(home.attention.map(\.id))
        guard let next = reviewSequence.dropFirst(i + 1).first(where: { waiting.contains($0) }) else {
            reviewSequence = []
            return false
        }
        var path = paths[tab, default: []]
        if !path.isEmpty { path.removeLast() }
        path.append(.receipt(next))
        paths[tab] = path
        return true
    }

    func popToRoot(_ tab: AppTab? = nil) {
        paths[tab ?? self.tab] = []
    }

    /// Add tray / empty state / queue: dismiss the tray and show the chosen system picker.
    func beginImport(_ source: ImportSource) {
        trays.dismiss()
        // Simulator and UI-test aid: `-mock -mock-import-demo` skips the system picker and imports
        // three generated receipt pictures, so the whole import flow can run unattended.
        if isMock, ProcessInfo.processInfo.arguments.contains("-mock-import-demo") {
            let flow = ImportModel()
            flow.source = source == .files ? .files : .photos
            Task {
                try? await Task.sleep(for: .milliseconds(350))
                importFlow = flow
                await flow.load(images: Self.demoReceiptImages())
            }
            return
        }
        importPicker = source
    }

    private static func demoReceiptImages() -> [UIImage] {
        (0..<3).map { n in
            let format = UIGraphicsImageRendererFormat(); format.scale = 1
            return UIGraphicsImageRenderer(size: CGSize(width: 900, height: 1400), format: format).image { ctx in
                UIColor(white: 0.18, alpha: 1).setFill(); ctx.fill(CGRect(x: 0, y: 0, width: 900, height: 1400))
                UIColor(white: 0.97, alpha: 1).setFill(); ctx.fill(CGRect(x: 120, y: 80, width: 660, height: 1240))
                UIColor.black.setFill()
                for row in 0..<22 { ctx.fill(CGRect(x: 170, y: 160 + row * 48 + n * 3, width: 280 + (row * 37 + n * 61) % 300, height: 14)) }
            }
        }
    }

    func openImportSources() {
        trays.present("import-source") { ImportSourceTray() }
    }

    func startCapture(_ mode: CaptureMode) {
        trays.dismiss()
        capture = mode
    }

    func open(receipt id: Int64) {
        trays.dismiss()
        tab = .home
        paths[.home] = [.receipt(id)]
    }

    func binding(for tab: AppTab) -> Binding<[Route]> {
        Binding(
            get: { self.paths[tab] ?? [] },
            set: { self.paths[tab] = $0 }
        )
    }

    var reviewBadge: Int { home.attentionCount }
}
