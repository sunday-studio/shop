import AVFoundation
import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

/// Switches between onboarding, the app, and the re-pair screen when a token is rejected.
struct RootView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.scenePhase) private var scenePhase

    /// Toasts sit above the tab bar, and above the floating Confirm bar on a receipt screen.
    private var toastInset: CGFloat {
        guard model.phase == .ready else { return 24 }
        let path = model.paths[model.tab, default: []]
        if path.isEmpty { return 100 }
        if case .receipt = path.last { return 150 }
        return 24
    }

    var body: some View {
        let _ = LaunchTrace.mark("RootView body")
        ZStack {
            Palette.bg.ignoresSafeArea()
            switch model.phase {
            case .onboarding:
                OnboardingFlow()
                    .transition(.opacity)
            case .ready:
                RootTabView()
                    .transition(.opacity)
            case .tokenRejected:
                TokenRejectedView()
                    .transition(.opacity)
            }
        }
        .shopTypography()
        // Any Button without its own style still gets the press feel and a haptic tick.
        .buttonStyle(.press(haptic: .selection))
        .tint(Accent.blue)
        .toastHost(model.toasts, bottomInset: toastInset)
        .trayHost(model.trays)
        .preferredColorScheme(model.appearance.colorScheme)
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Haptics.prepare()
                Task { await model.refreshOnForeground() }
            }
        }
        .task {
            // Let the first frames finish before the first round of network work lands.
            try? await Task.sleep(for: .milliseconds(350))
            await model.refreshOnForeground()
        }
    }
}

/// Four tabs kept alive (state and scroll survive switching), a floating pill bar and the Add
/// button. Content slides 12pt toward the tapped tab (200ms). The bar hides on pushed screens.
struct RootTabView: View {
    @Environment(AppModel.self) private var model
    @State private var previous: AppTab = .home
    /// Tabs are built the first time they are opened and then kept (their scroll position and
    /// state stay). Building all four at launch made every launch pay for screens nobody opened.
    @State private var visited: Set<AppTab> = [.home]
    @State private var pickedPhotos: [PhotosPickerItem] = []
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        @Bindable var model = model
        ZStack(alignment: .bottom) {
            ForEach(AppTab.allCases, id: \.self) { tab in
                if visited.contains(tab) || tab == model.tab {
                    TabStack(tab: tab)
                        .opacity(tab == model.tab ? 1 : 0)
                        .offset(x: offset(for: tab))
                        .allowsHitTesting(tab == model.tab)
                        .accessibilityHidden(tab != model.tab)
                }
            }

            if showsBar {
                BottomFade()
                    .ignoresSafeArea(edges: .bottom)
                    .transition(.opacity)
                PillTabBar(items: items, selection: tabBinding) {
                    model.trays.present("add") { AddTray() }
                }
                .padding(.bottom, 2)
                .transition(.offset(y: 30).combined(with: .opacity))
            }
        }
        .animation(Motion.ease, value: showsBar)
        .onChange(of: model.tab, initial: true) { _, tab in visited.insert(tab) }
        .task {
            // Ask up front so the first scan opens straight into the camera. Onboarding has its
            // own camera step; this covers users who skipped it.
            if !model.isMock, AVCaptureDevice.authorizationStatus(for: .video) == .notDetermined {
                _ = await AVCaptureDevice.requestAccess(for: .video)
            }
        }
        .fullScreenCover(item: $model.capture) { mode in
            CaptureCover(mode: mode)
                .environment(model)
        }
        .fullScreenCover(item: $model.importFlow) { flow in
            ImportScreen(model: flow)
                .environment(model)
        }
        .photosPicker(
            isPresented: Binding(get: { model.importPicker == .photos }, set: { if !$0 { pickerClosed() } }),
            selection: $pickedPhotos, maxSelectionCount: ImportModel.maxPages, selectionBehavior: .ordered, matching: .images
        )
        .fileImporter(
            isPresented: Binding(get: { model.importPicker == .files }, set: { if !$0 { pickerClosed() } }),
            allowedContentTypes: [.jpeg, .png, .heic, .heif, .pdf], allowsMultipleSelection: true
        ) { result in
            guard case .success(let urls) = result, !urls.isEmpty else { return }
            startImport(source: .files) { await $0.load(files: urls) }
        }
        .onChange(of: pickedPhotos) { _, items in
            guard !items.isEmpty else { return }
            pickedPhotos = []
            startImport(source: .photos) { await $0.load(photos: items) }
        }
    }

    /// A cancelled picker returns to the Add tray; a pick opens the Import screen.
    private func pickerClosed() {
        model.importPicker = nil
        Task {
            try? await Task.sleep(for: .milliseconds(450))
            if model.importFlow == nil && !model.isStartingImport { model.trays.present("add") { AddTray() } }
        }
    }

    private func startImport(source: UploadItem.Source, load: @escaping (ImportModel) async -> Void) {
        model.isStartingImport = true
        let flow = ImportModel()
        flow.source = source
        Task {
            // The system picker is still dismissing; present the screen once it is gone.
            try? await Task.sleep(for: .milliseconds(350))
            model.importFlow = flow
            model.isStartingImport = false
            await load(flow)
        }
    }

    private var showsBar: Bool { model.paths[model.tab, default: []].isEmpty }

    private var items: [TabBarItem<AppTab>] {
        AppTab.allCases.map {
            TabBarItem(id: $0, title: $0.title, icon: $0.icon, selectedIcon: $0.selectedIcon,
                       badge: $0 == .home ? model.reviewBadge : ($0 == .products ? model.productReview.total : 0),
                       dot: $0 == .list && model.tab != .list && !model.list.suggestions.isEmpty)
        }
    }

    private var tabBinding: Binding<AppTab> {
        Binding(
            get: { model.tab },
            set: { new in
                if new == model.tab {
                    model.popToRoot(new)
                    return
                }
                previous = model.tab
                withAnimation(reduce ? Motion.reducedFade : Motion.tabSlide) { model.tab = new }
            }
        )
    }

    private func offset(for tab: AppTab) -> CGFloat {
        guard !reduce, tab != model.tab else { return 0 }
        return tab.rawValue < model.tab.rawValue ? -Motion.slideDistance : Motion.slideDistance
    }
}

/// One tab's navigation stack.
private struct TabStack: View {
    let tab: AppTab
    @Environment(AppModel.self) private var model

    var body: some View {
        NavigationStack(path: model.binding(for: tab)) {
            root
                .navigationDestination(for: Route.self) { route in
                    RouteView(route: route)
                }
        }
    }

    @ViewBuilder
    private var root: some View {
        rootScreen
            // SwiftUI's accessibilityHidden doesn't reach the UIKit navigation container, so a tab
            // that is not showing was still readable by VoiceOver: hide its container directly.
            .background(NavigationAccessibilityHider(hidden: tab != model.tab).allowsHitTesting(false))
    }

    @ViewBuilder
    private var rootScreen: some View {
        switch tab {
        case .home: HomeView()
        case .analytics: AnalyticsView()
        case .products: ProductsView()
        case .list: ShoppingListView()
        }
    }
}

/// Resolves a pushed route to its screen.
struct RouteView: View {
    let route: Route

    var body: some View {
        Group {
            switch route {
            case .receipt(let id): ReceiptView(receiptId: id)
            case .receiptImage(let id): ReceiptImageView(receiptId: id)
            case .product(let id): ProductDetailView(productId: id)
            case .priceHistory(let id): PriceHistoryView(productId: id)
            case .receiptNames(let id): ReceiptNamesView(productId: id)
            case .shopSpend(let id, let range): ShopSpendView(shopId: id, range: range)
            case .categorySpend(let name, let id, let range): CategorySpendView(name: name, categoryId: id, range: range)
            case .priceChanges: PriceChangesView()
            case .settings: SettingsView()
            case .server: ServerView()
            case .uploadQueue: UploadQueueView()
            case .shops: ShopsView()
            case .categories: CategoriesView()
            case .search: SearchView()
            case .catalog: CatalogView()
            case .productReview: ProductReviewView()
            case .needsEnglish: NeedsEnglishView()
            case .glossary: GlossaryView()
            }
        }
        .toolbar(.hidden, for: .navigationBar)
    }
}

/// Hides the surrounding `UINavigationController`'s view from assistive technologies.
private struct NavigationAccessibilityHider: UIViewRepresentable {
    let hidden: Bool

    func makeUIView(context: Context) -> UIView { UIView() }

    func updateUIView(_ view: UIView, context: Context) {
        let hidden = hidden
        DispatchQueue.main.async {
            var responder: UIResponder? = view
            while let current = responder {
                if let navigation = current as? UINavigationController {
                    navigation.view.accessibilityElementsHidden = hidden
                    return
                }
                responder = current.next
            }
        }
    }
}
