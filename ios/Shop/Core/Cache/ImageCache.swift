import CryptoKit
import SwiftUI
import UIKit

/// Memory + disk image cache. Used for shop logos, product photos and receipt images.
/// Files live in Caches/images/<sha256 of key>; the OS may purge them, which only costs a refetch.
@MainActor
final class ImageCache {
    static let shared = ImageCache()

    /// Adds auth headers for requests to the Shop server. Set by `APIClient`.
    var authorize: (URLRequest) -> URLRequest = { $0 }

    private let memory = NSCache<NSString, UIImage>()
    private var inflight: [String: Task<Data?, Never>] = [:]
    /// Keys that failed this session (e.g. a shop without a logo → 404); not retried until relaunch.
    private var failed: Set<String> = []
    private let directory: URL
    private let session: URLSession

    init() {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        directory = caches.appendingPathComponent("images", isDirectory: true)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        memory.countLimit = 300
        let config = URLSessionConfiguration.default
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        config.timeoutIntervalForRequest = 30
        session = URLSession(configuration: config)
    }

    /// Synchronous lookup: memory, then disk.
    func cached(_ key: String) -> UIImage? {
        if let img = memory.object(forKey: key as NSString) { return img }
        let file = fileURL(for: key)
        guard let data = try? Data(contentsOf: file), let img = UIImage(data: data) else { return nil }
        memory.setObject(img, forKey: key as NSString)
        return img
    }

    /// Cached image, or download, cache and return it. Concurrent requests for one key share a download.
    func image(for url: URL, key: String? = nil) async -> UIImage? {
        let k = key ?? url.absoluteString
        if let img = cached(k) { return img }
        if failed.contains(k) { return nil }
        if let running = inflight[k] {
            return await running.value.flatMap(UIImage.init(data:))
        }
        let request = authorize(URLRequest(url: url))
        let session = self.session
        let task = Task<Data?, Never> {
            guard let result = try? await session.data(for: request),
                  let http = result.1 as? HTTPURLResponse, (200..<300).contains(http.statusCode)
            else { return nil }
            return result.0
        }
        inflight[k] = task
        let data = await task.value
        inflight[k] = nil
        guard let data, let img = UIImage(data: data) else {
            failed.insert(k)
            return nil
        }
        memory.setObject(img, forKey: k as NSString)
        try? data.write(to: fileURL(for: k), options: .atomic)
        return img
    }

    /// Stores an image produced locally (e.g. a photo just taken), keeping PNG alpha.
    func store(_ image: UIImage, key: String) {
        memory.setObject(image, forKey: key as NSString)
        if let data = image.pngData() {
            try? data.write(to: fileURL(for: key), options: .atomic)
        }
    }

    func remove(_ key: String) {
        failed.remove(key)
        memory.removeObject(forKey: key as NSString)
        try? FileManager.default.removeItem(at: fileURL(for: key))
    }

    func removeAll() {
        failed = []
        memory.removeAllObjects()
        try? FileManager.default.removeItem(at: directory)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    private func fileURL(for key: String) -> URL {
        let digest = SHA256.hash(data: Data(key.utf8)).map { String(format: "%02x", $0) }.joined()
        return directory.appendingPathComponent(digest + ".img")
    }
}

/// Shows a cached remote image, with a placeholder while loading or when it fails.
struct CachedImage<Content: View, Placeholder: View>: View {
    let url: URL?
    var key: String? = nil
    @ViewBuilder var content: (Image) -> Content
    @ViewBuilder var placeholder: () -> Placeholder

    @State private var image: UIImage?

    var body: some View {
        Group {
            if let image {
                content(Image(uiImage: image))
            } else {
                placeholder()
            }
        }
        .task(id: url) {
            guard let url else { image = nil; return }
            let k = key ?? url.absoluteString
            if let hit = ImageCache.shared.cached(k) {
                image = hit
                return
            }
            let loaded = await ImageCache.shared.image(for: url, key: k)
            withAnimation(Motion.fade) { image = loaded }
        }
    }
}
