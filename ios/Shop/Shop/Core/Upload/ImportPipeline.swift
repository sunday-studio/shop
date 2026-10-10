import CoreImage
import ImageIO
import UIKit
import Vision

/// A document outline in normalised image coordinates (0...1, origin top-left), in the order
/// top-left, top-right, bottom-right, bottom-left. Persisted with the upload item.
nonisolated struct StoredQuad: Codable, Sendable, Equatable {
    var points: [CGPoint]

    init(points: [CGPoint]) { self.points = points }

    @MainActor init(_ quad: CropQuad) { points = quad.points }

    @MainActor var cropQuad: CropQuad? {
        guard points.count == 4 else { return nil }
        return CropQuad(topLeft: points[0], topRight: points[1], bottomRight: points[2], bottomLeft: points[3])
    }
}

/// One page of an imported receipt on disk, plus what to do to it in the preparing step.
nonisolated struct PageEdit: Codable, Sendable, Hashable {
    /// File name inside the item's page folder (the original picked bytes or a PDF page render).
    var file: String
    /// Straighten to this outline. nil with `detect` true means "find the edges while preparing".
    var quad: StoredQuad?
    var detect: Bool = false
    /// The user set the outline by hand: applied even when automatic straightening is off.
    var adjusted: Bool = false
    /// Quarter turns clockwise, applied before the outline.
    var rotation: Int = 0
}

extension StoredQuad: Hashable {
    func hash(into hasher: inout Hasher) { for p in points { hasher.combine(p.x); hasher.combine(p.y) } }
}

// MARK: - Image helpers (off the main thread)

nonisolated enum Straightener {
    enum Detection: Sendable, Equatable {
        case quad(StoredQuad)
        /// The photo is already a straight rectangle: leave it alone.
        case square
        case notFound
    }

    /// Spec says 0.35, but a receipt in a typical photo fills only 25 to 38 % of the frame
    /// (measured on spike/data), so 0.15 keeps the real ones and still rejects stray rectangles.
    static let minArea: CGFloat = 0.15

    static let enabledKey = "settings.straightenImports"

    static var isEnabled: Bool {
        get { (UserDefaults.standard.object(forKey: enabledKey) as? Bool) ?? true }
        set { UserDefaults.standard.set(newValue, forKey: enabledKey) }
    }

    /// Upright (EXIF orientation applied) bitmap, downsampled so the long side is at most `maxSide`.
    static func upright(_ data: Data, maxSide: CGFloat) -> CGImage? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: maxSide,
        ]
        return CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary)
    }

    /// The date the photo was taken, read from the original bytes (conversion would drop it).
    static func captureDate(_ data: Data) -> Date? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              let props = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let exif = props[kCGImagePropertyExifDictionary] as? [CFString: Any],
              let text = exif[kCGImagePropertyExifDateTimeOriginal] as? String else { return nil }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = Dates.timeZone
        formatter.dateFormat = "yyyy:MM:dd HH:mm:ss"
        return formatter.date(from: text)
    }

    /// Quarter turns clockwise.
    static func rotated(_ image: CGImage, quarterTurns: Int) -> CGImage {
        let turns = ((quarterTurns % 4) + 4) % 4
        guard turns != 0 else { return image }
        let w = CGFloat(image.width), h = CGFloat(image.height)
        let size = turns % 2 == 0 ? CGSize(width: w, height: h) : CGSize(width: h, height: w)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        let result = UIGraphicsImageRenderer(size: size, format: format).image { ctx in
            let c = ctx.cgContext
            c.translateBy(x: size.width / 2, y: size.height / 2)
            c.rotate(by: CGFloat(turns) * .pi / 2)
            UIImage(cgImage: image).draw(in: CGRect(x: -w / 2, y: -h / 2, width: w, height: h))
        }
        return result.cgImage ?? image
    }

    /// Document outline for a receipt photo (PLAN: docs/DESIGN-UPLOAD-FLOW.md §5a). Runs on a
    /// 1024 px copy; accepts only confident, large, convex quads.
    static func detect(in image: CGImage) -> Detection {
        let long = CGFloat(max(image.width, image.height))
        var small = image
        if long > 1024 {
            let scale = 1024 / long
            let size = CGSize(width: CGFloat(image.width) * scale, height: CGFloat(image.height) * scale)
            let format = UIGraphicsImageRendererFormat()
            format.scale = 1
            let down = UIGraphicsImageRenderer(size: size, format: format).image { _ in
                UIImage(cgImage: image).draw(in: CGRect(origin: .zero, size: size))
            }
            small = down.cgImage ?? image
        }
        let handler = VNImageRequestHandler(cgImage: small, options: [:])
        var candidates: [VNRectangleObservation] = []
        let segmentation = VNDetectDocumentSegmentationRequest()
        if (try? handler.perform([segmentation])) != nil { candidates += segmentation.results ?? [] }
        let rectangles = VNDetectRectanglesRequest()
        rectangles.minimumSize = 0.3
        rectangles.minimumAspectRatio = 0.15
        rectangles.maximumAspectRatio = 1.0
        rectangles.maximumObservations = 3
        if (try? handler.perform([rectangles])) != nil { candidates += rectangles.results ?? [] }
        let size = CGSize(width: small.width, height: small.height)
        // Vision: normalised, origin bottom-left. Flip to top-left.
        func flip(_ p: CGPoint) -> CGPoint { CGPoint(x: min(1, max(0, p.x)), y: min(1, max(0, 1 - p.y))) }
        for o in candidates where o.confidence >= 0.75 {
            let pts = [flip(o.topLeft), flip(o.topRight), flip(o.bottomRight), flip(o.bottomLeft)]
            // A receipt is taller than wide. Vision's segmentation sometimes returns a confident
            // but wrong box (a table edge, a strip of background): those fail here.
            guard area(pts) >= minArea, isConvex(pts), aspect(pts, in: size) <= 1.0 else { continue }
            let tol: CGFloat = 0.015
            let straight = abs(pts[0].x - pts[3].x) < tol && abs(pts[1].x - pts[2].x) < tol
                && abs(pts[0].y - pts[1].y) < tol && abs(pts[3].y - pts[2].y) < tol
            // An axis-aligned outline that fills the frame needs nothing; a straight but small one
            // (a receipt on a table) is still cropped.
            if straight && area(pts) > 0.9 { return .square }
            return .quad(StoredQuad(points: pts))
        }
        return .notFound
    }

    /// Mean width over mean height of the outline, in pixels.
    private static func aspect(_ p: [CGPoint], in size: CGSize) -> CGFloat {
        func dist(_ a: CGPoint, _ b: CGPoint) -> CGFloat { hypot((a.x - b.x) * size.width, (a.y - b.y) * size.height) }
        let width = (dist(p[0], p[1]) + dist(p[3], p[2])) / 2
        let height = (dist(p[0], p[3]) + dist(p[1], p[2])) / 2
        return height > 0 ? width / height : .infinity
    }

    private static func area(_ p: [CGPoint]) -> CGFloat {
        var sum: CGFloat = 0
        for i in 0..<p.count {
            let a = p[i], b = p[(i + 1) % p.count]
            sum += a.x * b.y - b.x * a.y
        }
        return abs(sum) / 2
    }

    private static func isConvex(_ p: [CGPoint]) -> Bool {
        var sign = 0
        for i in 0..<p.count {
            let a = p[i], b = p[(i + 1) % p.count], c = p[(i + 2) % p.count]
            let cross = (b.x - a.x) * (c.y - b.y) - (b.y - a.y) * (c.x - b.x)
            let s = cross > 0 ? 1 : (cross < 0 ? -1 : 0)
            if s == 0 { continue }
            if sign == 0 { sign = s } else if sign != s { return false }
        }
        return true
    }

    private static let context = CIContext(options: [.useSoftwareRenderer: false])

    /// Perspective-corrects `image` to the outline; the long side is capped at `maxSide`.
    static func apply(_ quad: StoredQuad, to image: CGImage, maxSide: CGFloat = 2400) -> CGImage? {
        guard quad.points.count == 4 else { return nil }
        let w = CGFloat(image.width), h = CGFloat(image.height)
        func point(_ p: CGPoint) -> CIVector { CIVector(x: p.x * w, y: (1 - p.y) * h) }
        let filter = CIFilter(name: "CIPerspectiveCorrection")
        filter?.setValue(CIImage(cgImage: image), forKey: kCIInputImageKey)
        filter?.setValue(point(quad.points[0]), forKey: "inputTopLeft")
        filter?.setValue(point(quad.points[1]), forKey: "inputTopRight")
        filter?.setValue(point(quad.points[2]), forKey: "inputBottomRight")
        filter?.setValue(point(quad.points[3]), forKey: "inputBottomLeft")
        guard var output = filter?.outputImage else { return nil }
        let long = max(output.extent.width, output.extent.height)
        if long > maxSide {
            let scale = maxSide / long
            output = output.transformed(by: CGAffineTransform(scaleX: scale, y: scale))
        }
        return context.createCGImage(output, from: output.extent)
    }
}

// MARK: - Preparing step

/// Turns the picked originals of one receipt into the single JPEG that uploads: decode (HEIC,
/// PNG, JPEG), rotate, straighten, stitch, then write the multipart body.
nonisolated enum ImportPreparer {
    enum Outcome: Sendable {
        case ready(contentType: String)
        case failed(String)
    }

    static func prepare(_ item: UploadItem, straighten: Bool) -> Outcome {
        guard let edits = item.pageEdits, !edits.isEmpty else { return .failed("Nothing to upload.") }
        var pages: [UIImage] = []
        for edit in edits {
            guard let data = try? Data(contentsOf: DiskStore.url("\(item.pageDir)/\(edit.file)")) else {
                return .failed("Couldn't read a picked file.")
            }
            guard var image = Straightener.upright(data, maxSide: 3600) else {
                return .failed("This file isn't a readable image.")
            }
            image = Straightener.rotated(image, quarterTurns: edit.rotation)
            if edit.adjusted || straighten {
                var quad = edit.quad
                if quad == nil, edit.detect, straighten, case .quad(let found) = Straightener.detect(in: image) { quad = found }
                if let quad, let corrected = Straightener.apply(quad, to: image) { image = corrected }
            }
            pages.append(UIImage(cgImage: image))
        }
        guard let jpeg = Stitcher.stitch(pages) else { return .failed("Couldn't prepare this receipt.") }
        var form = Multipart()
        if let shopId = item.shopId { form.add(field: "shop_id", value: String(shopId)) }
        if let purchasedAt = item.purchasedAt { form.add(field: "purchased_at", value: Dates.rfc3339(purchasedAt)) }
        form.add(file: "image", filename: "receipt.jpg", mimeType: "image/jpeg", data: jpeg)
        form.finish()
        do {
            try jpeg.write(to: DiskStore.url(item.imageFile), options: .atomic)
            try form.data.write(to: DiskStore.url(item.bodyFile), options: .atomic)
        } catch {
            return .failed("Not enough space to prepare this.")
        }
        return .ready(contentType: form.contentType)
    }
}
