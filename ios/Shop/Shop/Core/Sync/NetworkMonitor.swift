import Foundation
import Network
import Observation

/// Is there any network path? Drives "Offline · N waiting" and retries on reconnect.
@MainActor
@Observable
final class NetworkMonitor {
    private(set) var isOnline = true
    /// Called on the main actor when the path comes back.
    @ObservationIgnored var onReconnect: (() -> Void)?

    @ObservationIgnored private let monitor = NWPathMonitor()

    /// Simulator aid: `-offline` (with `-mock`) pins the app offline.
    @ObservationIgnored private let pinnedOffline = ProcessInfo.processInfo.arguments.contains("-offline")

    init() {
        if pinnedOffline { isOnline = false }
        monitor.pathUpdateHandler = Self.handler(for: self)
        monitor.start(queue: DispatchQueue(label: "nl.casprine.shop.network"))
    }

    /// Built outside the main actor so the closure isn't main-actor isolated (it runs on the
    /// monitor's queue).
    nonisolated private static func handler(for model: NetworkMonitor) -> @Sendable (NWPath) -> Void {
        { [weak model] path in
            let online = path.status == .satisfied
            guard let monitor = model else { return }
            Task { @MainActor in monitor.update(online) }
        }
    }

    private func update(_ online: Bool) {
        guard !pinnedOffline else { return }
        let wasOnline = isOnline
        isOnline = online
        if online && !wasOnline { onReconnect?() }
    }
}
