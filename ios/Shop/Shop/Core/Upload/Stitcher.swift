import UIKit

/// Multi-page scans become one long receipt, stitched vertically on the phone (PLAN §9).
@MainActor
enum Stitcher {
    /// Scales pages to a common width (≤ maxWidth), stacks them, and encodes a JPEG under
    /// the server's 20 MB limit (lowering quality, then width, if needed).
    static func stitch(_ pages: [UIImage], maxWidth: CGFloat = 1600, maxBytes: Int = 19_000_000) -> Data? {
        guard !pages.isEmpty else { return nil }
        var width = min(maxWidth, pages.map { $0.size.width * $0.scale }.max() ?? maxWidth)
        for _ in 0..<3 {
            let image = render(pages, width: width)
            for quality in [0.82, 0.7, 0.55] {
                if let data = image.jpegData(compressionQuality: quality), data.count <= maxBytes {
                    return data
                }
            }
            width *= 0.75
        }
        return render(pages, width: width).jpegData(compressionQuality: 0.5)
    }

    private static func render(_ pages: [UIImage], width: CGFloat) -> UIImage {
        let heights = pages.map { width * $0.size.height / max(1, $0.size.width) }
        let size = CGSize(width: width, height: heights.reduce(0, +))
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        return UIGraphicsImageRenderer(size: size, format: format).image { ctx in
            UIColor.white.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
            var y: CGFloat = 0
            for (page, h) in zip(pages, heights) {
                page.draw(in: CGRect(x: 0, y: y, width: width, height: h))
                y += h
            }
        }
    }

    /// Small preview for the capture screen.
    static func thumbnail(_ image: UIImage, maxSide: CGFloat = 600) -> UIImage {
        let scale = min(1, maxSide / max(image.size.width, image.size.height))
        let size = CGSize(width: image.size.width * scale, height: image.size.height * scale)
        return UIGraphicsImageRenderer(size: size).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }
    }
}
