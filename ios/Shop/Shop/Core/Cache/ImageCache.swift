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
    /// Pixel sizes that may be cached for a key (so `remove` can drop every variant).
    private static let variantSizes = [96, 160, 300, 720]

    init() {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        directory = caches.appendingPathComponent("images", isDirectory: true)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        // Bounded by decoded size (about 48 MB), not by count: a few full-size photos used to
        // be able to fill the memory budget on their own.
        memory.totalCostLimit = 48 * 1024 * 1024
        let config = URLSessionConfiguration.default
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        config.timeoutIntervalForRequest = 30
        session = URLSession(configuration: config)
    }

    private func memoryKey(_ key: String, _ maxPixel: Int?) -> NSString {
        (maxPixel.map { "\(key)#\($0)" } ?? key) as NSString
    }

    /// Synchronous lookup in memory only; never touches the disk, so it is safe while scrolling.
    func cached(_ key: String, maxPixel: Int? = nil) -> UIImage? {
        memory.object(forKey: memoryKey(key, maxPixel))
    }

    /// Cached image, or read it from disk / download it, decode it off the main thread, cache and
    /// return it. `maxPixel` decodes a smaller copy (long side in pixels) for thumbnails and rows.
    /// Concurrent requests for one key share a download.
    func image(for url: URL, key: String? = nil, maxPixel: Int? = nil) async -> UIImage? {
        let k = key ?? url.absoluteString
        if let img = cached(k, maxPixel: maxPixel) { return img }
        if failed.contains(k) { return nil }
        let file = fileURL(for: k)
        var data = await Task.detached(priority: .userInitiated) { try? Data(contentsOf: file) }.value
        if data == nil {
            if let running = inflight[k] {
                data = await running.value
            } else {
                let request = authorize(URLRequest(url: url))
                let session = self.session
                let task = Task<Data?, Never> {
                    guard let result = try? await session.data(for: request),
                          let http = result.1 as? HTTPURLResponse, (200..<300).contains(http.statusCode)
                    else { return nil }
                    return result.0
                }
                inflight[k] = task
                data = await task.value
                inflight[k] = nil
                if let data {
                    Task.detached(priority: .utility) { try? data.write(to: file, options: .atomic) }
                }
            }
        }
        guard let data else { failed.insert(k); return nil }
        let img = await Task.detached(priority: .userInitiated) { Self.decode(data, maxPixel: maxPixel) }.value
        guard let img else { failed.insert(k); return nil }
        memory.setObject(img, forKey: memoryKey(k, maxPixel), cost: Self.cost(img))
        return img
    }

    private nonisolated static func cost(_ image: UIImage) -> Int {
        Int(image.size.width * image.scale * image.size.height * image.scale * 4)
    }

    /// Decodes fully (so the first draw doesn't), optionally down to a thumbnail.
    private nonisolated static func decode(_ data: Data, maxPixel: Int?) -> UIImage? {
        if let maxPixel {
            guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
            let options: [CFString: Any] = [
                kCGImageSourceCreateThumbnailFromImageAlways: true,
                kCGImageSourceCreateThumbnailWithTransform: true,
                kCGImageSourceShouldCacheImmediately: true,
                kCGImageSourceThumbnailMaxPixelSize: maxPixel,
            ]
            guard let cg = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
            return UIImage(cgImage: cg)
        }
        guard let img = UIImage(data: data) else { return nil }
        return img.preparingForDisplay() ?? img
    }

    /// Stores an image produced locally (e.g. a photo just taken), keeping PNG alpha.
    func store(_ image: UIImage, key: String) {
        dropVariants(of: key)
        memory.setObject(image, forKey: key as NSString, cost: Self.cost(image))
        let file = fileURL(for: key)
        Task.detached(priority: .utility) {
            if let data = image.pngData() { try? data.write(to: file, options: .atomic) }
        }
    }

    private func dropVariants(of key: String) {
        failed.remove(key)
        memory.removeObject(forKey: key as NSString)
        for size in Self.variantSizes { memory.removeObject(forKey: memoryKey(key, size)) }
    }

    func remove(_ key: String) {
        dropVariants(of: key)
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
    /// Long side in pixels to decode at: rows and logos are small, heroes larger. nil = full size.
    var maxPixel: Int? = 300
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
            if let hit = ImageCache.shared.cached(k, maxPixel: maxPixel) {
                image = hit
                return
            }
            let loaded = await ImageCache.shared.image(for: url, key: k, maxPixel: maxPixel)
            withAnimation(Motion.fade) { image = loaded }
        }
    }
}
