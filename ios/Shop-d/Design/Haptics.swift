import UIKit

/// One haptic per user action. Never fire on scroll. See design/MOTION.md.
///
/// Where each fires (from design/DESIGN-RULES.md):
/// - selection: segmented controls, filter chips, category/shop picks, tab change, pickers, reorder steps
/// - light:     primary button tap, + button, opening a tray
/// - medium:    Confirm receipt, Link product, Save product
/// - soft:      check off a list item, swipe row snaps
/// - success:   receipt ok, barcode known, sync finished, pairing succeeded
/// - warning:   total mismatch, duplicate receipt, offline, sync pill amber, new barcode
/// - error:     wrong password, delete confirmed, upload failed
enum HapticEvent {
    case selection, light, medium, soft, success, warning, error
}

@MainActor
enum Haptics {
    private static let selectionGen = UISelectionFeedbackGenerator()
    private static let lightGen = UIImpactFeedbackGenerator(style: .light)
    private static let mediumGen = UIImpactFeedbackGenerator(style: .medium)
    private static let softGen = UIImpactFeedbackGenerator(style: .soft)
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
        notifyGen.prepare()
    }

    static func play(_ event: HapticEvent) {
        guard isEnabled else { return }
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
        case .success:
            notifyGen.notificationOccurred(.success)
            notifyGen.prepare()
        case .warning:
            notifyGen.notificationOccurred(.warning)
            notifyGen.prepare()
        case .error:
            notifyGen.notificationOccurred(.error)
            notifyGen.prepare()
        }
    }
}
