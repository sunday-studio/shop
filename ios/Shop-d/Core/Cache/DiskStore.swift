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

    static func remove(_ name: String) {
        try? FileManager.default.removeItem(at: url(name))
    }

    /// Wipes everything (sign out of a server).
    static func removeAll() {
        try? FileManager.default.removeItem(at: directory)
    }
}
