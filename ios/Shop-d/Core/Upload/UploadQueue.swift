import Foundation
import Observation
import UIKit

/// One scanned receipt waiting for (or going through) upload.
nonisolated struct UploadItem: Codable, Sendable, Identifiable, Hashable {
    enum State: String, Codable, Sendable {
        case waiting, uploading, failed, done
    }

    var id: UUID
    var shopId: Int64
    var shopName: String
    var purchasedAt: Date?
    var pages: Int
    var createdAt: Date
    var state: State = .waiting
    var attempts: Int = 0
    var nextAttemptAt: Date?
    var lastError: String?
    /// Set when the server accepted it (202) or already had it (409).
    var receiptId: Int64?
    var wasDuplicate: Bool = false

    var bodyFile: String { "uploads/\(id.uuidString).body" }
    var imageFile: String { "uploads/\(id.uuidString).jpg" }
}

/// Persistent receipt upload queue (PLAN §9 Sync/UploadQueue).
///
/// Each scan is written to disk as a ready-to-send multipart body and uploaded with a background
/// URLSession, so uploads survive the app being suspended or killed. Network failures back off
/// (5 s → 30 min) and resume on reconnect; a 409 means the server already has the image.
@MainActor
@Observable
final class UploadQueue {
    static let sessionIdentifier = "nl.casprine.shop.uploads"
    private static let storeName = "uploads.json"

    private(set) var items: [UploadItem] = []

    /// Waiting, uploading or failed (not done).
    var pending: [UploadItem] { items.filter { $0.state != .done } }
    var activeCount: Int { items.filter { $0.state == .waiting || $0.state == .uploading }.count }
    var failedCount: Int { items.filter { $0.state == .failed }.count }
    var isUploading: Bool { items.contains { $0.state == .uploading } }

    /// Called after a receipt reaches the server (refresh the inbox).
    @ObservationIgnored var onUploaded: ((UploadItem) -> Void)?
    /// Called when the server says the token is no longer valid.
    @ObservationIgnored var onUnauthorized: (() -> Void)?
    /// Background session completion handler handed over by the app delegate.
    @ObservationIgnored var backgroundCompletion: (() -> Void)?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private var session: URLSession?
    @ObservationIgnored private let delegate = UploadSessionDelegate()
    @ObservationIgnored private var retryTask: Task<Void, Never>?

    init(api: APIClient) {
        self.api = api
        items = DiskStore.load([UploadItem].self, from: Self.storeName) ?? []
        try? FileManager.default.createDirectory(at: DiskStore.url("uploads"), withIntermediateDirectories: true)
        // Anything marked uploading from a previous run is re-checked against live tasks below.
        delegate.onComplete = { [weak self] id, status, data, error in
            guard let queue = self else { return }
            Task { @MainActor in queue.complete(id: id, status: status, data: data, errorDescription: error) }
        }
        delegate.onFinishEvents = { [weak self] in
            guard let queue = self else { return }
            Task { @MainActor in
                queue.backgroundCompletion?()
                queue.backgroundCompletion = nil
            }
        }
        if !api.isMock {
            let config = URLSessionConfiguration.background(withIdentifier: Self.sessionIdentifier)
            config.isDiscretionary = false
            config.sessionSendsLaunchEvents = true
            config.allowsCellularAccess = true
            config.timeoutIntervalForResource = 60 * 60 * 24
            session = URLSession(configuration: config, delegate: delegate, delegateQueue: nil)
        }
        reconcile()
        pruneDone()
    }

    // MARK: Enqueue

    /// Writes the stitched JPEG and its multipart body to disk, then starts the upload.
    func enqueue(jpeg: Data, shop: Shop, purchasedAt: Date?, pages: Int) {
        var item = UploadItem(id: UUID(), shopId: shop.id, shopName: shop.displayName, purchasedAt: purchasedAt, pages: pages, createdAt: .now)
        var form = Multipart()
        form.add(field: "shop_id", value: String(shop.id))
        if let purchasedAt { form.add(field: "purchased_at", value: Dates.rfc3339(purchasedAt)) }
        form.add(file: "image", filename: "receipt.jpg", mimeType: "image/jpeg", data: jpeg)
        form.finish()
        do {
            try jpeg.write(to: DiskStore.url(item.imageFile), options: .atomic)
            try form.data.write(to: DiskStore.url(item.bodyFile), options: .atomic)
            UserDefaults.standard.set(form.contentType, forKey: "upload.ct.\(item.id.uuidString)")
        } catch {
            item.state = .failed
            item.lastError = "Couldn't save the scan on this iPhone."
        }
        items.insert(item, at: 0)
        save()
        if item.state == .waiting { start(item.id) }
    }

    // MARK: Control

    /// Starts waiting items that are due. `force` ignores backoff (reconnect, pull to refresh).
    func kick(force: Bool = false) {
        for item in items where item.state == .waiting {
            if !force, let next = item.nextAttemptAt, next > .now { continue }
            start(item.id)
        }
    }

    func retry(_ id: UUID) {
        guard let i = index(id) else { return }
        items[i].state = .waiting
        items[i].nextAttemptAt = nil
        items[i].lastError = nil
        save()
        start(id)
    }

    func remove(_ id: UUID) {
        guard let i = index(id) else { return }
        let item = items[i]
        session?.getAllTasks { tasks in
            for t in tasks where t.taskDescription == item.id.uuidString { t.cancel() }
        }
        deleteFiles(item)
        items.remove(at: i)
        save()
    }

    /// Cancels everything and forgets the queue (sign out).
    func reset() {
        session?.getAllTasks { tasks in tasks.forEach { $0.cancel() } }
        retryTask?.cancel()
        items = []
        save()
    }

    func thumbnail(for item: UploadItem) -> UIImage? {
        UIImage(contentsOfFile: DiskStore.url(item.imageFile).path)
    }

    // MARK: Upload

    private func start(_ id: UUID) {
        guard let i = index(id) else { return }
        let item = items[i]
        guard item.state != .uploading else { return }
        items[i].state = .uploading
        items[i].attempts += 1
        save()

        if api.isMock || session == nil {
            Task { await foregroundUpload(item) }
            return
        }
        do {
            let contentType = UserDefaults.standard.string(forKey: "upload.ct.\(item.id.uuidString)") ?? "multipart/form-data"
            let request = try api.request("POST", "/v1/receipts", contentType: contentType, timeout: 120)
            guard let task = session?.uploadTask(with: request, fromFile: DiskStore.url(item.bodyFile)) else { return }
            task.taskDescription = item.id.uuidString
            task.resume()
        } catch {
            fail(id, message: (error as? APIError)?.message ?? error.localizedDescription, retry: false)
        }
    }

    private func foregroundUpload(_ item: UploadItem) async {
        do {
            let jpeg = try Data(contentsOf: DiskStore.url(item.imageFile))
            let receipt = try await api.uploadReceipt(image: jpeg, shopId: item.shopId, purchasedAt: item.purchasedAt)
            succeed(item.id, receiptId: receipt.id, duplicate: false)
        } catch APIError.conflict(let problem) {
            succeed(item.id, receiptId: Self.receiptId(from: problem), duplicate: true)
        } catch APIError.unauthorized {
            fail(item.id, message: APIError.unauthorized.message, retry: false)
            onUnauthorized?()
        } catch let error as APIError {
            fail(item.id, message: error.message, retry: error.isConnectivity || isServerError(error))
        } catch {
            fail(item.id, message: error.localizedDescription, retry: true)
        }
    }

    private func complete(id: String, status: Int, data: Data, errorDescription: String?) {
        guard let uuid = UUID(uuidString: id), index(uuid) != nil else { return }
        if let errorDescription {
            if errorDescription == "cancelled" { return }
            api.markOffline()
            fail(uuid, message: "Waiting for the server · \(errorDescription)", retry: true)
            return
        }
        let decoder = Coders.decoder()
        switch status {
        case 200..<300:
            let receipt = try? decoder.decode(Receipt.self, from: data)
            succeed(uuid, receiptId: receipt?.id, duplicate: false)
        case 409:
            let problem = try? decoder.decode(Problem.self, from: data)
            succeed(uuid, receiptId: problem.flatMap(Self.receiptId(from:)), duplicate: true)
        case 401:
            fail(uuid, message: APIError.unauthorized.message, retry: false)
            onUnauthorized?()
        case 500...:
            fail(uuid, message: "The server had a problem (\(status)). Retrying.", retry: true)
        default:
            let problem = try? decoder.decode(Problem.self, from: data)
            fail(uuid, message: problem?.detail ?? problem?.title ?? "Upload refused (\(status)).", retry: false)
        }
    }

    private func succeed(_ id: UUID, receiptId: Int64?, duplicate: Bool) {
        guard let i = index(id) else { return }
        items[i].state = .done
        items[i].receiptId = receiptId
        items[i].wasDuplicate = duplicate
        items[i].lastError = nil
        let item = items[i]
        deleteFiles(item, keepImage: false)
        save()
        onUploaded?(item)
    }

    private func fail(_ id: UUID, message: String, retry: Bool) {
        guard let i = index(id) else { return }
        items[i].lastError = message
        if retry && items[i].attempts < 8 {
            let delay = Self.backoff(attempt: items[i].attempts)
            items[i].state = .waiting
            items[i].nextAttemptAt = .now.addingTimeInterval(delay)
            scheduleRetry(after: delay)
        } else {
            items[i].state = .failed
        }
        save()
    }

    private func scheduleRetry(after delay: TimeInterval) {
        retryTask?.cancel()
        retryTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled else { return }
            self?.kick()
        }
    }

    /// Items stuck in `uploading` without a live task (app was killed) go back to waiting.
    private func reconcile() {
        guard let session else {
            for i in items.indices where items[i].state == .uploading { items[i].state = .waiting }
            save()
            return
        }
        session.getAllTasks { [weak self] tasks in
            let live = Set(tasks.compactMap(\.taskDescription))
            guard let queue = self else { return }
            Task { @MainActor in queue.resumeAfterLaunch(live: live) }
        }
    }

    private func resumeAfterLaunch(live: Set<String>) {
        for i in items.indices where items[i].state == .uploading && !live.contains(items[i].id.uuidString) {
            items[i].state = .waiting
        }
        save()
        kick()
    }

    private func pruneDone() {
        let cutoff = Date.now.addingTimeInterval(-60 * 60 * 24)
        items.removeAll { $0.state == .done && $0.createdAt < cutoff }
        save()
    }

    // MARK: Helpers

    private func isServerError(_ error: APIError) -> Bool {
        if case .http(let code) = error { return code >= 500 }
        return false
    }

    private func index(_ id: UUID) -> Int? { items.firstIndex { $0.id == id } }

    private func save() { DiskStore.save(items, to: Self.storeName) }

    private func deleteFiles(_ item: UploadItem, keepImage: Bool = false) {
        try? FileManager.default.removeItem(at: DiskStore.url(item.bodyFile))
        if !keepImage { try? FileManager.default.removeItem(at: DiskStore.url(item.imageFile)) }
        UserDefaults.standard.removeObject(forKey: "upload.ct.\(item.id.uuidString)")
    }

    static func backoff(attempt: Int) -> TimeInterval {
        let steps: [TimeInterval] = [5, 15, 30, 60, 120, 300, 900, 1800]
        return steps[min(max(attempt - 1, 0), steps.count - 1)]
    }

    /// The 409 body holds the existing receipt id in `detail` (openapi.yaml).
    nonisolated static func receiptId(from problem: Problem) -> Int64? {
        guard let detail = problem.detail else { return nil }
        let digits = detail.split(whereSeparator: { !$0.isNumber }).last.map { String($0) }
        return digits.flatMap { Int64($0) }
    }
}

/// URLSession delegate for the background session. Lives off the main actor; forwards results.
nonisolated final class UploadSessionDelegate: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    var onComplete: (@Sendable (String, Int, Data, String?) -> Void)?
    var onFinishEvents: (@Sendable () -> Void)?

    private let lock = NSLock()
    private var buffers: [Int: Data] = [:]

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        buffers[dataTask.taskIdentifier, default: Data()].append(data)
        lock.unlock()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        lock.lock()
        let data = buffers.removeValue(forKey: task.taskIdentifier) ?? Data()
        lock.unlock()
        let status = (task.response as? HTTPURLResponse)?.statusCode ?? 0
        var message: String? = nil
        if let error = error as? URLError {
            message = error.code == .cancelled ? "cancelled" : error.localizedDescription
        } else if let error {
            message = error.localizedDescription
        }
        onComplete?(task.taskDescription ?? "", status, data, message)
    }

    func urlSessionDidFinishEvents(forBackgroundURLSession session: URLSession) {
        onFinishEvents?()
    }
}
