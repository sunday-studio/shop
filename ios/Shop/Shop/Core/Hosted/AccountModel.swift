import Foundation
import Observation

/// What the app knows about the server it is connected to and, on the hosted service, the plan.
/// Screens read `entitlements`; nothing else needs to know which door the person came through.
@MainActor
@Observable
final class AccountModel {
    private(set) var serverInfo: ServerInfo?
    private(set) var account: Account?
    private(set) var isRefreshing = false

    @ObservationIgnored private let api: APIClient
    private static let infoFile = "server-info.json"
    private static let accountFile = "account.json"

    init(api: APIClient) {
        self.api = api
        serverInfo = DiskStore.load(ServerInfo.self, from: Self.infoFile)
        account = DiskStore.load(Account.self, from: Self.accountFile)
    }

    /// Hosted or not; before the first answer it is the single-household behaviour of today.
    var mode: ServerMode { serverInfo?.mode ?? .single }
    var entitlements: Entitlements { Entitlements(server: serverInfo, account: account) }

    /// Called when the person connects (onboarding) with what the probe found.
    func adopt(_ info: ServerInfo) {
        serverInfo = info
        DiskStore.save(info, to: Self.infoFile)
        if info.mode == .single { setAccount(nil) }
    }

    func adopt(_ state: Account?) { setAccount(state) }

    /// Foreground and after purchases: refresh the server info and, when hosted, the plan.
    /// Offline or failing, the saved copy stays (so a trip without signal doesn't lock anyone out).
    func refresh() async {
        guard !isRefreshing else { return }
        isRefreshing = true
        defer { isRefreshing = false }
        if let info = try? await api.serverInfo() { serverInfo = info; DiskStore.save(info, to: Self.infoFile) }
        guard mode == .hosted else { setAccount(nil); return }
        do {
            setAccount(try await api.account())
        } catch APIError.notFound {
            setAccount(nil)
        } catch {}
    }

    func reset() {
        serverInfo = nil
        setAccount(nil)
        DiskStore.remove(Self.infoFile)
    }

    private func setAccount(_ state: Account?) {
        account = state
        if let state { DiskStore.save(state, to: Self.accountFile) } else { DiskStore.remove(Self.accountFile) }
    }
}
