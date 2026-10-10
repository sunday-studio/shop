import AVFoundation
import Foundation
import Observation
import UIKit

/// O.1–O.4: find the server, sign in with its password, copy the catalog.
@MainActor
@Observable
final class OnboardingModel {
    enum Step: Int, Comparable {
        case welcome, server, password, ready
        static func < (a: Step, b: Step) -> Bool { a.rawValue < b.rawValue }
    }

    enum ServerCheck: Equatable {
        case idle
        case testing
        case found(version: String, ms: Int, checks: [CheckRow])
        case failed(String)
    }

    struct CheckRow: Equatable, Identifiable {
        let key: String
        let value: String
        let ok: Bool
        var id: String { key }
    }

    enum SignIn: Equatable {
        case idle, working, wrong, locked(String), failed(String)
    }

    var step: Step = .welcome
    var urlText: String
    var password = ""
    private(set) var serverCheck: ServerCheck = .idle
    private(set) var signIn: SignIn = .idle
    private(set) var wrongAttempts = 0
    private(set) var syncProgress: [String: (Int, Int?)] = [:]
    private(set) var isSyncing = false
    private(set) var cameraStatus: AVAuthorizationStatus = AVCaptureDevice.authorizationStatus(for: .video)

    /// Set after a successful test, so O.3 talks to the same address.
    private(set) var testedURL: URL?

    private let app: AppModel
    private var probe: APIClient?

    init(app: AppModel, presetURL: URL? = nil) {
        self.app = app
        urlText = presetURL?.absoluteString ?? app.serverURL?.absoluteString ?? (app.isMock ? "http://mock.shop.local:8080" : "")
        if let presetURL { testedURL = presetURL }
    }

    var suggestions: [String] { ["shop.local:8080", "192.168.1.20:8080"] }

    var canContinueFromServer: Bool {
        if case .found = serverCheck { return true }
        return false
    }

    // MARK: O.2

    /// "mini.local:8080" → "http://mini.local:8080"
    static func normalize(_ text: String) -> URL? {
        var s = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return nil }
        if !s.contains("://") { s = "http://" + s }
        while s.hasSuffix("/") { s.removeLast() }
        guard let url = URL(string: s), let scheme = url.scheme, ["http", "https"].contains(scheme), url.host() != nil else { return nil }
        return url
    }

    func testConnection() async {
        guard let url = Self.normalize(urlText) else {
            serverCheck = .failed("That doesn't look like a server address. Try something like shop.local:8080.")
            Haptics.play(.error)
            return
        }
        urlText = url.absoluteString
        serverCheck = .testing
        let client = makeClient(url)
        let started = Date()
        do {
            let status = try await client.authStatus()
            guard status.server == "shop" else {
                serverCheck = .failed("Something answered, but it isn't a Shop server.")
                Haptics.play(.error)
                return
            }
            let ms = Int(Date().timeIntervalSince(started) * 1000)
            let health = try? await client.health()
            serverCheck = .found(version: status.version, ms: ms, checks: Self.rows(from: health))
            testedURL = url
            probe = client
            Haptics.play(.success)
        } catch let error as APIError {
            serverCheck = .failed(Self.message(for: error))
            Haptics.play(.error)
        } catch {
            serverCheck = .failed(error.localizedDescription)
            Haptics.play(.error)
        }
    }

    func urlEdited() {
        if serverCheck != .idle && serverCheck != .testing {
            serverCheck = .idle
            testedURL = nil
        }
    }

    static func rows(from health: Health?) -> [CheckRow] {
        guard let health else { return [] }
        let names: [(String, String)] = [("db", "Database"), ("ocr", "OCR engine"), ("backup", "Last backup")]
        var rows: [CheckRow] = names.compactMap { key, title in
            guard let c = health.checks[key] else { return nil }
            return CheckRow(key: title, value: c.detail ?? (c.ok ? "Healthy" : "Problem"), ok: c.ok)
        }
        for (key, c) in health.checks.sorted(by: { $0.key < $1.key }) where !names.contains(where: { $0.0 == key }) && !c.ok {
            rows.append(CheckRow(key: key.capitalized, value: c.detail ?? "Problem", ok: false))
        }
        return rows
    }

    static func message(for error: APIError) -> String {
        switch error {
        case .offline: "This iPhone is offline. Connect to Wi-Fi and try again."
        case .unreachable: "No answer. Check the address, that the Mac mini is on, and that you're on the same network or Tailscale."
        case .invalidURL: "That doesn't look like a server address."
        case .notFound: "Something answered, but it isn't a Shop server."
        default: error.message
        }
    }

    // MARK: O.3

    func submitPassword() async {
        guard let url = testedURL ?? Self.normalize(urlText) else {
            step = .server
            return
        }
        guard !password.isEmpty else { return }
        signIn = .working
        let client = probe ?? makeClient(url)
        do {
            let token = try await client.pair(password: password, deviceName: Self.deviceName)
            password = ""
            app.completePairing(serverURL: url, token: token, deviceName: Self.deviceName)
            signIn = .idle
            Haptics.play(.success)
            step = .ready
            await firstSync()
        } catch APIError.unauthorized {
            wrongAttempts += 1
            signIn = .wrong
            Haptics.play(.error)
        } catch APIError.rateLimited(let detail) {
            signIn = .locked(detail ?? "Too many wrong passwords. Try again in 15 minutes.")
            Haptics.play(.error)
        } catch let error as APIError {
            signIn = .failed(Self.message(for: error))
            Haptics.play(.error)
        } catch {
            signIn = .failed(error.localizedDescription)
            Haptics.play(.error)
        }
    }

    func passwordEdited() {
        if signIn == .wrong { signIn = .idle }
    }

    static var deviceName: String {
        let name = UIDevice.current.name
        return String(name.prefix(60))
    }

    // MARK: O.4

    func firstSync() async {
        isSyncing = true
        defer { isSyncing = false }
        do {
            try await app.catalog.refreshReference()
            syncProgress["Shops"] = (app.catalog.shops.count, app.catalog.shops.count)
            syncProgress["Categories"] = (app.catalog.categories.count, app.catalog.categories.count)
        } catch {}
        await app.list.sync()
        syncProgress["Shopping list"] = (app.list.open.count, app.list.open.count)
        syncProgress["Products"] = (0, nil)
        do {
            try await app.catalog.refreshProducts()
            let n = app.catalog.products.count
            syncProgress["Products"] = (n, n)
        } catch {
            syncProgress["Products"] = (app.catalog.products.count, nil)
        }
    }

    func requestCamera() async {
        _ = await AVCaptureDevice.requestAccess(for: .video)
        cameraStatus = AVCaptureDevice.authorizationStatus(for: .video)
        Haptics.play(cameraStatus == .authorized ? .success : .warning)
    }

    func finish(scanFirst: Bool) {
        app.enterApp()
        if scanFirst { app.startCapture(.receipt) }
    }

    // MARK: Helpers

    private func makeClient(_ url: URL) -> APIClient {
        APIClient(transport: app.isMock ? app.api.transport : URLSessionTransport(), baseURL: url)
    }
}
