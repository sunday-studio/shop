import SwiftUI
import UIKit

@main
struct ShopApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(appDelegate.model)
                .task { if ProcessInfo.processInfo.arguments.contains("-bench") { await Bench.run(appDelegate.model) } }
        }
    }
}

/// Owns the app model so it exists even when iOS relaunches the app in the background to
/// deliver upload events (no window then), and hands those events to the upload queue.
final class AppDelegate: NSObject, UIApplicationDelegate {
    let model = AppModel()

    func application(
        _ application: UIApplication,
        handleEventsForBackgroundURLSession identifier: String,
        completionHandler: @escaping () -> Void
    ) {
        guard identifier == UploadQueue.sessionIdentifier else {
            completionHandler()
            return
        }
        // The queue created the session with this identifier at launch, which reconnects its
        // delegate; the handler runs from urlSessionDidFinishEvents.
        model.uploads.backgroundCompletion = completionHandler
    }
}

/// Keeps the edge-swipe back gesture when the navigation bar is hidden (every screen draws
/// its own header).
extension UINavigationController: @retroactive UIGestureRecognizerDelegate {
    override open func viewDidLoad() {
        super.viewDidLoad()
        interactivePopGestureRecognizer?.delegate = self
    }

    public func gestureRecognizerShouldBegin(_ gestureRecognizer: UIGestureRecognizer) -> Bool {
        viewControllers.count > 1
    }
}
