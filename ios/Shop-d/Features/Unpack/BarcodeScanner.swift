import AVFoundation
import SwiftUI
import VisionKit

/// Continuous EAN/UPC scanning with VisionKit's DataScannerViewController.
struct BarcodeScanner: UIViewControllerRepresentable {
    let onCode: (String) -> Void

    static var isAvailable: Bool {
        DataScannerViewController.isSupported && DataScannerViewController.isAvailable
    }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.ean13, .ean8, .upce])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isPinchToZoomEnabled: true,
            isGuidanceEnabled: false,
            isHighlightingEnabled: false
        )
        vc.delegate = context.coordinator
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {
        if !vc.isScanning { try? vc.startScanning() }
    }

    static func dismantleUIViewController(_ vc: DataScannerViewController, coordinator: Coordinator) {
        vc.stopScanning()
    }

    func makeCoordinator() -> Coordinator { Coordinator(onCode: onCode) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let onCode: (String) -> Void
        private var lastCode: String?
        private var lastAt = Date.distantPast

        init(onCode: @escaping (String) -> Void) { self.onCode = onCode }

        func dataScanner(_ dataScanner: DataScannerViewController, didAdd addedItems: [RecognizedItem], allItems: [RecognizedItem]) {
            for item in addedItems {
                guard case .barcode(let barcode) = item, let code = barcode.payloadStringValue else { continue }
                let digits = code.filter(\.isNumber)
                guard [8, 12, 13, 14].contains(digits.count) else { continue }
                // Ignore the same code for 3 s so holding a product doesn't re-fire.
                if digits == lastCode, Date().timeIntervalSince(lastAt) < 3 { continue }
                lastCode = digits
                lastAt = Date()
                onCode(digits)
            }
        }
    }
}

/// Torch toggle for the back camera (DataScanner doesn't expose one).
enum Torch {
    @MainActor
    static func set(_ on: Bool) {
        guard let device = AVCaptureDevice.default(for: .video), device.hasTorch else { return }
        do {
            try device.lockForConfiguration()
            device.torchMode = on ? .on : .off
            device.unlockForConfiguration()
        } catch {}
    }

    static var isAvailable: Bool {
        AVCaptureDevice.default(for: .video)?.hasTorch ?? false
    }
}
