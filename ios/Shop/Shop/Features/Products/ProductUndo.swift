import SwiftUI

extension AppModel {
    /// "Deleted Halfvolle melk · Undo": restores the product (backend keeps it 30 days).
    func undoProductDelete(_ product: Product) {
        toasts.undoable("Deleted \(product.name)") { [weak self] in
            guard let self else { return }
            Task {
                do {
                    let back = try await self.api.restoreProduct(product.id)
                    self.catalog.upsert(back)
                    Haptics.play(.success)
                } catch let error as APIError {
                    if case .conflict = error {
                        Haptics.play(.warning)
                        self.toasts.show("Can't undo this one anymore")
                    } else {
                        Haptics.play(.error)
                        self.toasts.error("Couldn't undo. Try again.")
                    }
                } catch {}
            }
        }
    }
}
