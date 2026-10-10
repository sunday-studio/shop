import SwiftUI

/// Switches between onboarding, the app, and the re-pair screen when a token is rejected.
struct RootView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
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
        .tint(Accent.blue)
        .toastHost(model.toasts, bottomInset: model.phase == .ready && model.paths[model.tab, default: []].isEmpty ? 100 : 24)
        .trayHost(model.trays)
        .preferredColorScheme(model.appearance.colorScheme)
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Haptics.prepare()
                Task { await model.refreshOnForeground() }
            }
        }
        .task { await model.refreshOnForeground() }
    }
}

/// Four tabs kept alive (state and scroll survive switching), a floating pill bar and the Add
/// button. Content slides 12pt toward the tapped tab (200ms). The bar hides on pushed screens.
struct RootTabView: View {
    @Environment(AppModel.self) private var model
    @State private var previous: AppTab = .home
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        @Bindable var model = model
        ZStack(alignment: .bottom) {
            ForEach(AppTab.allCases, id: \.self) { tab in
                TabStack(tab: tab)
                    .opacity(tab == model.tab ? 1 : 0)
                    .offset(x: offset(for: tab))
                    .allowsHitTesting(tab == model.tab)
                    .accessibilityHidden(tab != model.tab)
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
        .fullScreenCover(item: $model.capture) { mode in
            CaptureCover(mode: mode)
                .environment(model)
        }
    }

    private var showsBar: Bool { model.paths[model.tab, default: []].isEmpty }

    private var items: [TabBarItem<AppTab>] {
        AppTab.allCases.map {
            TabBarItem(id: $0, title: $0.title, icon: $0.icon, selectedIcon: $0.selectedIcon, badge: $0 == .home ? model.reviewBadge : 0)
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
            }
        }
        .toolbar(.hidden, for: .navigationBar)
    }
}
