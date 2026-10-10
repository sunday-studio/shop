import SwiftUI

/// What the sync pill says (IA "Sync states"). Nil = connected and idle: no pill on screen.
struct SyncStatus: Equatable {
    let title: String
    let dot: Color
    let busy: Bool
    let route: Route
}

extension AppModel {
    var syncStatus: SyncStatus? {
        let waiting = uploads.activeCount
        if !network.isOnline {
            return SyncStatus(title: waiting > 0 ? "Offline · \(waiting) waiting" : "Offline", dot: Palette.ink3, busy: false, route: .uploadQueue)
        }
        if api.reachability == .unreachable {
            return SyncStatus(title: "Server unreachable", dot: Accent.red, busy: false, route: .server)
        }
        if uploads.isUploading {
            return SyncStatus(title: "Uploading \(max(1, waiting))", dot: Accent.blue, busy: true, route: .uploadQueue)
        }
        if waiting > 0 {
            return SyncStatus(title: "\(waiting) waiting", dot: Palette.ink3, busy: false, route: .uploadQueue)
        }
        if uploads.failedCount > 0 {
            let n = uploads.failedCount
            return SyncStatus(title: n == 1 ? "1 upload failed" : "\(n) uploads failed", dot: Accent.red, busy: false, route: .uploadQueue)
        }
        if let ocr = health?.checks["ocr"], !ocr.ok {
            return SyncStatus(title: "OCR down", dot: Accent.amber, busy: false, route: .server)
        }
        return nil
    }

    /// Last NAS backup older than 36 h (Settings only).
    var backupIsStale: Bool {
        guard let backup = health?.checks["backup"] else { return false }
        return !backup.ok
    }
}
