import Foundation

/// Small JSON files in Application Support/Shop. Used for the offline cache (shops,
/// categories, products, list), the upload queue and the list operation log.
nonisolated enum DiskStore {
    static var directory: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("Shop", isDirectory: true)
        if !FileManager.default.fileExists(atPath: dir.path) {
            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            var url = dir
            try? url.setResourceValues(values)
        }
        return dir
    }

    static func url(_ name: String) -> URL {
        directory.appendingPathComponent(name)
    }

    static func load<T: Decodable>(_ type: T.Type, from name: String) -> T? {
        guard let data = try? Data(contentsOf: url(name)) else { return nil }
        return try? Coders.decoder().decode(T.self, from: data)
    }

    static func save<T: Encodable>(_ value: T, to name: String) {
        guard let data = try? Coders.encoder().encode(value) else { return }
        try? data.write(to: url(name), options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    private static let writer = DispatchQueue(label: "shop.diskstore.writer", qos: .utility)
    private static let generation = Generation()

    private final class Generation: @unchecked Sendable {
        private let lock = NSLock()
        private var latest: [String: Int] = [:]
        func next(_ name: String) -> Int {
            lock.lock(); defer { lock.unlock() }
            let n = (latest[name] ?? 0) + 1
            latest[name] = n
            return n
        }
        func isLatest(_ name: String, _ n: Int) -> Bool {
            lock.lock(); defer { lock.unlock() }
            return latest[name] == n
        }
    }

    private struct Unchecked<T>: @unchecked Sendable { let value: T }

    /// Like `save`, but encodes and writes off the main thread (big files: the product catalog,
    /// the inbox). Only the newest pending write for a file is kept. Don't read the file back
    /// straight away; use `save` for anything that is read again within the same run.
    static func saveInBackground<T: Encodable>(_ value: T, to name: String) {
        let box = Unchecked(value: value)
        let n = generation.next(name)
        writer.async {
            guard generation.isLatest(name, n) else { return }
            save(box.value, to: name)
        }
    }

    static func remove(_ name: String) {
        try? FileManager.default.removeItem(at: url(name))
    }

    /// Wipes everything (sign out of a server).
    static func removeAll() {
        try? FileManager.default.removeItem(at: directory)
    }
}
