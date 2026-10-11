import SwiftUI

/// P.1.5 · "Merge into another product" (docs/DESIGN-CATEGORIES-PRODUCTS.md §3.4): pick the
/// product to keep, confirm, and the server moves receipt names and purchases across.
struct MergeProductTray: View {
    let source: Product
    let onPick: (Product) -> Void

    @Environment(AppModel.self) private var app
    @State private var query = ""

    private var candidates: [Product] {
        let text = query.trimmingCharacters(in: .whitespaces)
        let pool: [Product] = text.isEmpty
            ? app.catalog.products.values.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
            : ProductSearch.local(text, in: app.catalog, limit: 12)
        return pool.filter { $0.id != source.id }.prefix(12).map { $0 }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            TrayHeader(title: "Merge into…", subtitle: "\(source.name) moves into the product you pick.")
            SearchField(placeholder: "Find the product to keep", text: $query, background: Palette.trayField, autofocus: true)
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(candidates) { product in
                        Button { onPick(product) } label: {
                            ProductRow(
                                name: product.name,
                                meta: [product.sizeText, app.catalog.category(product.categoryId)?.name].compactMap { $0 }.joined(separator: " · "),
                                category: app.catalog.category(product.categoryId)?.name,
                                categoryToken: app.catalog.category(product.categoryId)?.color,
                                imageURL: app.api.productImageURL(product)
                            )
                        }
                        .buttonStyle(.press(haptic: .selection))
                    }
                    if candidates.isEmpty {
                        TypeStyle.body.text("No product matches “\(query)”.").foregroundStyle(Palette.ink2)
                            .frame(maxWidth: .infinity).padding(.vertical, Space.xl)
                    }
                }
                .padding(.horizontal, Space.xs)
            }
            .frame(height: 5 * 56)
            .scrollBounceBehavior(.basedOnSize)
        }
    }
}

extension AppModel {
    /// Confirm tray for merging `source` into `target`; on success the source disappears, the
    /// detail screen closes (`afterMerge`) and an Undo toast shows.
    func confirmMerge(_ source: Product, into target: Product, afterMerge: @escaping () -> Void) {
        final class Box { var result: ReviewResult? }
        let box = Box()
        trays.present("merge-confirm") {
            ConfirmTray(
                title: "Merge into \(target.name)?",
                message: "Receipt names and purchases of \(source.name) move to \(target.name). Its barcode and nutrition are kept only where \(target.name) has none.",
                confirmTitle: "Merge"
            ) { [weak self] in
                guard let self else { return false }
                do {
                    box.result = try await self.api.mergeProduct(source.id, into: target.id)
                    return true
                } catch let error as APIError {
                    Haptics.play(.error)
                    self.toasts.error(error.message)
                    return false
                } catch { return false }
            } afterDismiss: { [weak self] in
                guard let self, let result = box.result else { return }
                self.catalog.removeProduct(source.id)
                self.catalog.upsert(result.product)
                Haptics.play(.success)
                afterMerge()
                if let mergeId = result.mergeId {
                    self.toasts.undoable("Merged \(source.name) into \(result.product.name)") { [weak self] in
                        Task { await self?.undoMerge(mergeId) }
                    }
                }
            }
        }
    }

    private func undoMerge(_ id: Int64) async {
        do {
            let result = try await api.undoProductChange(id)
            catalog.upsert(result.product)
            try? await catalog.refreshProducts()
            toasts.show("Restored \(result.product.name)")
        } catch APIError.conflict(_) {
            Haptics.play(.warning)
            toasts.show("Can't undo this one anymore")
        } catch {
            Haptics.play(.error)
            toasts.error("Couldn't undo. Try again.")
        }
    }
}
