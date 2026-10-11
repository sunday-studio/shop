import SwiftUI
import UIKit

/// H.1.1 · Full-screen receipt image, pinch and double-tap to zoom.
struct ReceiptImageView: View {
    let receiptId: Int64
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var image: UIImage?
    @State private var failed = false

    var body: some View {
        ZStack(alignment: .top) {
            Color(hex: 0x0C0C0B).ignoresSafeArea()
            if let image {
                ZoomableImage(image: image)
                    .ignoresSafeArea(edges: .bottom)
            } else if failed {
                EmptyState(title: "Image unavailable", message: "The server couldn't send this image.",
                           action: (title: "Try again", run: { Task { await loadImage() } })) {
                    GlyphIllustration(icon: .image, color: Accent.red)
                }
                .environment(\.colorScheme, .dark)
                .frame(maxHeight: .infinity)
            } else {
                ProgressView().tint(.white).frame(maxHeight: .infinity)
            }
            HStack {
                IconButton(icon: .close, label: "Close", foreground: .white, background: .white.opacity(0.16)) { dismiss() }
                Spacer()
                if let image {
                    ShareLink(item: Image(uiImage: image), preview: SharePreview("Receipt", image: Image(uiImage: image))) {
                        IconView(.upload, size: 18)
                            .foregroundStyle(.white)
                            .frame(width: Size.iconButton, height: Size.iconButton)
                            .background(.white.opacity(0.16), in: Circle())
                    }
                    .accessibilityLabel("Share image")
                }
            }
            .padding(.horizontal, Space.edge)
            .padding(.top, Space.l)
        }
        .toolbar(.hidden, for: .navigationBar)
        .statusBarHidden(false)
        .task { await loadImage() }
    }

    private func loadImage() async {
        failed = false
        guard let url = app.api.receiptImageURL(receiptId) else { failed = true; return }
        let key = "receipt:\(receiptId)"
        if let hit = ImageCache.shared.cached(key) { image = hit; return }
        image = await ImageCache.shared.image(for: url, key: key)
        failed = image == nil
    }
}

/// UIScrollView-backed zoom (SwiftUI's MagnifyGesture lacks momentum and bounce).
struct ZoomableImage: UIViewRepresentable {
    let image: UIImage

    func makeUIView(context: Context) -> LayoutReportingScrollView {
        let scroll = LayoutReportingScrollView()
        scroll.onLayout = { [weak coordinator = context.coordinator, weak scroll] in
            if let scroll { coordinator?.layout(in: scroll) }
        }
        scroll.delegate = context.coordinator
        scroll.minimumZoomScale = 1
        scroll.maximumZoomScale = 5
        scroll.bouncesZoom = true
        scroll.showsVerticalScrollIndicator = false
        scroll.showsHorizontalScrollIndicator = false
        scroll.backgroundColor = .clear
        scroll.contentInsetAdjustmentBehavior = .never

        let imageView = UIImageView(image: image)
        imageView.contentMode = .scaleAspectFit
        imageView.isUserInteractionEnabled = true
        scroll.addSubview(imageView)
        context.coordinator.imageView = imageView

        let doubleTap = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.doubleTapped(_:)))
        doubleTap.numberOfTapsRequired = 2
        scroll.addGestureRecognizer(doubleTap)
        return scroll
    }

    func updateUIView(_ scroll: LayoutReportingScrollView, context: Context) {
        context.coordinator.imageView?.image = image
        context.coordinator.layout(in: scroll)
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    final class Coordinator: NSObject, UIScrollViewDelegate {
        weak var imageView: UIImageView?

        /// Fit width; tall receipts scroll vertically at 1×.
        func layout(in scroll: UIScrollView) {
            guard let imageView, let image = imageView.image, scroll.bounds.width > 0 else { return }
            let width = scroll.bounds.width
            let height = width * image.size.height / max(1, image.size.width)
            if scroll.zoomScale == 1 {
                imageView.frame = CGRect(x: 0, y: 0, width: width, height: height)
                scroll.contentSize = imageView.frame.size
            }
            center(scroll)
        }

        func viewForZooming(in scrollView: UIScrollView) -> UIView? { imageView }

        func scrollViewDidZoom(_ scrollView: UIScrollView) { center(scrollView) }

        private func center(_ scroll: UIScrollView) {
            guard let imageView else { return }
            let dx = max(0, (scroll.bounds.width - imageView.frame.width) / 2)
            let dy = max(0, (scroll.bounds.height - imageView.frame.height) / 2)
            scroll.contentInset = UIEdgeInsets(top: dy + 60, left: dx, bottom: dy + 40, right: dx)
        }

        @objc func doubleTapped(_ gesture: UITapGestureRecognizer) {
            guard let scroll = gesture.view as? UIScrollView else { return }
            if scroll.zoomScale > 1 {
                scroll.setZoomScale(1, animated: true)
            } else {
                let point = gesture.location(in: imageView)
                let size = CGSize(width: scroll.bounds.width / 2.5, height: scroll.bounds.height / 2.5)
                scroll.zoom(to: CGRect(origin: CGPoint(x: point.x - size.width / 2, y: point.y - size.height / 2), size: size), animated: true)
            }
        }
    }
}

/// Calls back on layout so the image can be fitted once the view has a size.
final class LayoutReportingScrollView: UIScrollView {
    var onLayout: (() -> Void)?

    override func layoutSubviews() {
        super.layoutSubviews()
        onLayout?()
    }
}
