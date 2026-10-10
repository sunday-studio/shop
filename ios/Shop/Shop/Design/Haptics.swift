import UIKit

/// One haptic per user action. Never fire on scroll. See design/MOTION.md.
///
/// Taxonomy (weight matches consequence):
/// - selection:    a choice changes: segmented controls, chips, category/shop picks, tab change, pickers, reorder steps
/// - light:        primary button tap on touch-down, + button
/// - medium:       commit: Confirm receipt, Link product, Save product
/// - soft:         check off a list item, swipe row snaps
/// - trayOpen:     a tray rises (soft, a little weight, lands with the spring)
/// - trayDismiss:  a tray is flicked away by drag (rigid, short, like a latch)
/// - success:      receipt ok, sync finished, pairing succeeded
/// - warning:      total mismatch, duplicate receipt, offline, needs review
/// - error:        wrong password, delete confirmed, receipt failed
/// - scanKnown:    barcode matches a product: one crisp rigid tick
/// - scanNew:      barcode is new: two quick ticks (rising), distinct from known
/// - uploadQueued: receipt saved to the upload queue: low soft thud
/// - uploadFailed: upload gave up: error notification
enum HapticEvent {
    case selection, light, medium, soft, success, warning, error
    case trayOpen, trayDismiss, scanKnown, scanNew, uploadQueued, uploadFailed
}

@MainActor
enum Haptics {
    private static let selectionGen = UISelectionFeedbackGenerator()
    private static let lightGen = UIImpactFeedbackGenerator(style: .light)
    private static let mediumGen = UIImpactFeedbackGenerator(style: .medium)
    private static let softGen = UIImpactFeedbackGenerator(style: .soft)
    private static let rigidGen = UIImpactFeedbackGenerator(style: .rigid)
    private static let notifyGen = UINotificationFeedbackGenerator()

    /// Users can turn haptics off in Settings (persisted).
    static let enabledKey = "settings.haptics"
    static var isEnabled: Bool {
        get { UserDefaults.standard.object(forKey: enabledKey) as? Bool ?? true }
        set { UserDefaults.standard.set(newValue, forKey: enabledKey) }
    }

    /// Call early (e.g. on touch down or screen appear) to cut latency.
    static func prepare() {
        selectionGen.prepare()
        lightGen.prepare()
        mediumGen.prepare()
        softGen.prepare()
        rigidGen.prepare()
        notifyGen.prepare()
    }

    /// Guard against two haptics for one action (a press-style touch-down plus a code path
    /// that also plays one) and against bursts: impacts/selection within 90ms of the previous
    /// one are dropped, notifications always win, and a notification suppresses the next 250ms.
    private static var lastFire = Date.distantPast
    private static var lastWasNotification = false
    private static let impactGap: TimeInterval = 0.09
    private static let notificationGap: TimeInterval = 0.25

    static func play(_ event: HapticEvent) {
        guard isEnabled else { return }
        let isNotification: Bool
        switch event {
        case .success, .warning, .error, .uploadFailed, .scanNew: isNotification = true
        default: isNotification = false
        }
        let now = Date()
        let gap = now.timeIntervalSince(lastFire)
        if lastWasNotification ? gap < notificationGap : (!isNotification && gap < impactGap) { return }
        lastFire = now
        lastWasNotification = isNotification

        switch event {
        case .selection:
            selectionGen.selectionChanged()
            selectionGen.prepare()
        case .light:
            lightGen.impactOccurred()
            lightGen.prepare()
        case .medium:
            mediumGen.impactOccurred()
            mediumGen.prepare()
        case .soft:
            softGen.impactOccurred()
            softGen.prepare()
        case .trayOpen:
            softGen.impactOccurred(intensity: 0.85)
            softGen.prepare()
        case .trayDismiss:
            rigidGen.impactOccurred(intensity: 0.5)
            rigidGen.prepare()
        case .scanKnown:
            rigidGen.impactOccurred(intensity: 0.9)
            rigidGen.prepare()
        case .scanNew:
            lightGen.impactOccurred(intensity: 0.6)
            Task { @MainActor in
                try? await Task.sleep(for: .milliseconds(85))
                rigidGen.impactOccurred(intensity: 1)
                rigidGen.prepare()
            }
            lightGen.prepare()
        case .uploadQueued:
            softGen.impactOccurred(intensity: 0.6)
            softGen.prepare()
        case .success:
            notifyGen.notificationOccurred(.success)
            notifyGen.prepare()
        case .warning:
            notifyGen.notificationOccurred(.warning)
            notifyGen.prepare()
        case .error, .uploadFailed:
            notifyGen.notificationOccurred(.error)
            notifyGen.prepare()
        }
    }
}
