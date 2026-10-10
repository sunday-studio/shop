import Foundation
import Observation

/// C.3: one unpacking session. Every scanned barcode becomes an entry: known (success
/// haptic, "✓ name"), new with an Open Food Facts prefill, or new and unknown (warning).
@MainActor
@Observable
final class UnpackModel {
    struct Entry: Identifiable, Equatable {
        enum State: Equatable {
            case looking
            case known(Product)
            case new(OpenFoodFactsPrefill?)
            case failed(String)
        }

        let id = UUID()
        let code: String
        var state: State
        let at = Date()
    }

    private(set) var entries: [Entry] = []
    @ObservationIgnored private let app: AppModel

    init(app: AppModel) {
        self.app = app
    }

    var knownCount: Int { entries.filter { if case .known = $0.state { return true }; return false }.count }
    var newCount: Int { entries.filter { if case .new = $0.state { return true }; return false }.count }

    func scanned(_ code: String) {
        // A product scanned twice in one session moves to the top instead of duplicating.
        if let i = entries.firstIndex(where: { $0.code == code }) {
            let entry = entries.remove(at: i)
            entries.insert(entry, at: 0)
            if case .known = entry.state { Haptics.play(.scanKnown) } else { Haptics.play(.scanNew) }
            return
        }
        // Instant answer from the local catalog when we have it.
        if let local = app.catalog.products.values.first(where: { $0.barcode == code }) {
            entries.insert(Entry(code: code, state: .known(local)), at: 0)
            Haptics.play(.scanKnown)
            return
        }
        entries.insert(Entry(code: code, state: .looking), at: 0)
        Task { await lookup(code) }
    }

    private func lookup(_ code: String) async {
        let state: Entry.State
        do {
            if let result = try await app.api.lookupBarcode(code) {
                if let product = result.product {
                    app.catalog.upsert(product)
                    state = .known(product)
                } else {
                    state = .new(result.prefill)
                }
            } else {
                state = .new(nil)
            }
        } catch let error as APIError {
            state = .failed(error.message)
        } catch {
            state = .failed(error.localizedDescription)
        }
        guard let i = entries.firstIndex(where: { $0.code == code }) else { return }
        entries[i].state = state
        switch state {
        case .known: Haptics.play(.scanKnown)
        case .new: Haptics.play(.scanNew)
        case .failed: Haptics.play(.error)
        case .looking: break
        }
    }

    func retry(_ entry: Entry) {
        guard let i = entries.firstIndex(where: { $0.id == entry.id }) else { return }
        entries[i].state = .looking
        Task { await lookup(entry.code) }
    }

    func saved(_ product: Product, for code: String) {
        guard let i = entries.firstIndex(where: { $0.code == code }) else { return }
        entries[i].state = .known(product)
    }
}
