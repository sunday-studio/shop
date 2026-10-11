import PhotosUI
import SwiftUI
import UIKit

/// "New products": products Shop created that it isn't sure about (docs/DESIGN-PRODUCT-AUTOPOPULATE.md §2).
struct ProductReviewView: View {
    @Environment(AppModel.self) private var app

    private var model: ProductReviewModel { app.productReview }

    var body: some View {
        VStack(spacing: 0) {
            NavHeader(title: "New products") {
                if model.total > 0 {
                    TypeStyle.label.text("\(model.total)")
                        .foregroundStyle(Palette.ink3)
                        .contentTransition(.numericText(value: Double(model.total)))
                        .frame(minWidth: Size.iconButton, alignment: .trailing)
                } else {
                    Color.clear.frame(width: Size.iconButton, height: Size.iconButton)
                }
            }
            ScrollView {
                content
                    .padding(.horizontal, Space.edge)
                    .padding(.bottom, 60)
            }
            .scrollIndicators(.hidden)
            .refreshable { await model.load() }
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await model.load() }
    }

    @ViewBuilder
    private var content: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            if model.isOffline {
                Banner(tone: .neutral, lead: nil, message: "Offline. You can review again when you're back online.")
            }
            if let error = model.loadError {
                Banner(tone: .red, lead: "Couldn't load new products.", message: error.message)
                Button("Retry") { Task { await model.load() } }
                    .textStyle(.label).foregroundStyle(Palette.blueText)
                    .buttonStyle(.press(haptic: .selection))
            }
            if !model.hasLoaded && model.items.isEmpty {
                ForEach(0..<3, id: \.self) { _ in ReviewCardSkeleton() }
            } else if model.items.isEmpty && model.loadError == nil {
                EmptyState(title: "All caught up", message: "New products show up here when Shop isn't sure about one. Scan or confirm a receipt and they'll appear.") {
                    GlyphIllustration(icon: .check, color: Accent.green)
                }
                .padding(.top, 60)
                .transition(.opacity)
            } else {
                LazyVStack(alignment: .leading, spacing: Space.l, pinnedViews: [.sectionHeaders]) {
                    ForEach(model.sections, id: \.reason) { section in
                        Section {
                            ForEach(section.items) { item in
                                card(item)
                                    .transition(.opacity.combined(with: .scale(scale: 0.98)))
                                    .task { await model.loadMoreIfNeeded(current: item) }
                            }
                        } header: {
                            sectionHeader(section.reason, count: section.count)
                        }
                    }
                    LoadMoreFooter(state: model.loadMoreFailed ? .failed : (model.isLoadingMore ? .loading : .idle)) { Task { await model.loadMore() } }
                }
            }
        }
        .padding(.top, Space.l)
        .animation(Motion.collapse, value: model.items.map(\.id))
    }

    private func sectionHeader(_ reason: ReviewReasonKind, count: Int) -> some View {
        HStack(spacing: 6) {
            IconView(reason.icon, size: 12, weight: .semibold)
            TypeStyle.captionMedium.text(reason.headline)
            TypeStyle.caption.text("· \(count)").contentTransition(.numericText(value: Double(count)))
            Spacer()
        }
        .foregroundStyle(reason.tone.text)
        .padding(.vertical, Space.m)
        .background(Palette.bg)
        .accessibilityAddTraits(.isHeader)
    }

    // MARK: Card

    @ViewBuilder
    private func card(_ item: ReviewItem) -> some View {
        if let reason = item.kind {
            let source = item.sourceLines.first
            VStack(alignment: .leading, spacing: Space.s) {
                ReviewCard(
                    name: model.displayName(for: item),
                    detail: item.product.sizeText,
                    categoryName: model.categoryName(for: item),
                    categoryToken: app.catalog.category(model.pickedCategory[item.id] ?? item.product.categoryId)?.color,
                    imageURL: app.api.productImageURL(item.product),
                    reason: reason,
                    suggestionName: item.suggestedMerge?.name,
                    barcode: source == nil ? item.product.barcode : nil,
                    source: source.map {
                        .init(shopName: $0.shopName ?? "Shop", dateText: $0.purchasedAt.map(Dates.shortDay) ?? "", rawText: $0.rawText, amountText: Money.format($0.lineTotalCents))
                    },
                    isWorking: model.workingIds.contains(item.id),
                    isOffline: model.isOffline,
                    onPrimary: { primary(item, reason) },
                    onSecondary: { secondary(item, reason) },
                    onCategory: { pickCategory(item) },
                    onMore: { options(item) }
                )
                if reason == .barcodeOffUnavailable {
                    Button("Try now") { Task { await model.tryNow(item) } }
                        .textStyle(.label).foregroundStyle(Palette.blueText)
                        .buttonStyle(.press(haptic: .selection))
                        .disabled(model.workingIds.contains(item.id) || model.isOffline)
                        .padding(.horizontal, Space.lx)
                }
                if item.sourceLineCount > 1 {
                    TypeStyle.caption.text("+\(item.sourceLineCount - 1) more receipts").foregroundStyle(Palette.ink3).padding(.horizontal, Space.lx)
                }
            }
        }
    }

    private func primary(_ item: ReviewItem, _ reason: ReviewReasonKind) {
        if reason == .barcodeMinimal { nameTray(item, accept: true) } else { Task { await model.primary(item) } }
    }

    private func secondary(_ item: ReviewItem, _ reason: ReviewReasonKind) {
        switch reason {
        case .nearMatch, .barcodeNameMatch: Task { await model.keepSeparate(item) }
        case .oddName, .barcodeOffUnavailable: nameTray(item, accept: true)
        case .noCategory: pickCategory(item)
        case .barcodeMinimal: Task { await model.accept(item) }
        }
    }

    private func nameTray(_ item: ReviewItem, accept: Bool) {
        app.trays.present("review-name") {
            ReviewNameTray(item: item, startEmpty: item.kind == .barcodeMinimal, onPhoto: { data in
                Task { await model.setPhoto(data, for: item) }
            }) { name in
                if accept { Task { await model.accept(item, name: name) } } else { model.set(name: name, for: item) }
            }
        }
    }

    private func pickCategory(_ item: ReviewItem) {
        app.trays.present("review-category") { ReviewCategoryTray(item: item, model: model) }
    }

    private func options(_ item: ReviewItem) {
        app.trays.present("review-options") {
            ReviewOptionsTray(
                item: item,
                onName: { nameTray(item, accept: false) },
                onCategory: { pickCategory(item) },
                onDiscard: { Task { await model.discard(item) } }
            )
        }
    }
}

// MARK: - Trays

private struct ReviewOptionsTray: View {
    let item: ReviewItem
    let onName: () -> Void
    let onCategory: () -> Void
    let onDiscard: () -> Void
    @Environment(TrayCoordinator.self) private var trays

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            TrayHeader(title: item.product.name)
            VStack(spacing: 6) {
                OptionRow(icon: .edit, title: "Edit name") { onName() }
                OptionRow(icon: .grid, title: "Pick category") { onCategory() }
                if item.product.createdVia != "manual" {
                    OptionRow(icon: .trash, title: "Discard", hint: "Its receipt lines become unlinked. You can undo.", destructive: true, haptic: .light) {
                        trays.dismiss()
                        onDiscard()
                    }
                }
            }
        }
    }
}

private struct ReviewNameTray: View {
    let item: ReviewItem
    let startEmpty: Bool
    let onPhoto: (Data) -> Void
    let onSave: (String) -> Void
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var loaded = false
    @State private var photoItem: PhotosPickerItem?
    @State private var camera = false
    @State private var photoTaken = false

    /// Barcode-born products without a picture can get one here.
    private var canAddPhoto: Bool { item.product.createdVia == "barcode" && item.product.imageUrl == nil && !photoTaken }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: "Name this product", subtitle: item.product.barcode)
            FieldTile(label: "Name", text: $name, placeholder: startEmpty ? "What is it?" : "Name", background: Palette.trayField)
            if canAddPhoto {
                HStack(spacing: Space.xl) {
                    if UIImagePickerController.isSourceTypeAvailable(.camera) {
                        Button("Take a photo") { camera = true }
                            .buttonStyle(.press(haptic: .selection))
                    }
                    PhotosPicker(selection: $photoItem, matching: .images) { Text("Choose a photo") }
                }
                .textStyle(.label).foregroundStyle(Palette.blueText)
                .padding(.horizontal, Space.xs)
            }
            Button("Save") {
                let trimmed = name.trimmingCharacters(in: .whitespaces)
                trays.dismiss()
                onSave(trimmed)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            Button("Cancel") { trays.dismiss() }
                .textStyle(.label).foregroundStyle(Palette.ink2)
                .frame(maxWidth: .infinity)
                .buttonStyle(.press(haptic: .selection))
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            if !startEmpty { name = item.product.name }
        }
        .onChange(of: photoItem) { _, picked in
            guard let picked else { return }
            Task {
                if let data = try? await picked.loadTransferable(type: Data.self), let jpeg = UIImage(data: data)?.jpegData(compressionQuality: 0.85) {
                    photoTaken = true
                    onPhoto(jpeg)
                }
            }
        }
        .fullScreenCover(isPresented: $camera) {
            CameraPicker(onImage: { image in
                camera = false
                if let jpeg = image.jpegData(compressionQuality: 0.85) { photoTaken = true; onPhoto(jpeg) }
            }, onCancel: { camera = false })
            .ignoresSafeArea()
        }
    }
}

private struct ReviewCategoryTray: View {
    let item: ReviewItem
    let model: ProductReviewModel
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            TrayHeader(title: "Pick a category")
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(app.catalog.categories) { category in
                        Button {
                            Haptics.play(.selection)
                            model.pick(category: category.id, for: item)
                            trays.dismiss()
                        } label: {
                            HStack(spacing: Space.l) {
                                CategoryDot(color: CategoryColor.swatch(for: category).color, size: 12)
                                TypeStyle.rowTitle.text(category.name).foregroundStyle(Palette.ink)
                                Spacer()
                                if (model.pickedCategory[item.id] ?? item.product.categoryId) == category.id {
                                    IconView(.check, size: 13, weight: .bold).foregroundStyle(Accent.blue)
                                }
                            }
                            .frame(height: 46)
                            .hairline()
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.press(haptic: .selection))
                    }
                }
                .padding(.horizontal, Space.xs)
            }
            .frame(maxHeight: 8 * 46)
            .scrollBounceBehavior(.basedOnSize)
        }
    }
}
