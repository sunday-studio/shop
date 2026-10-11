import SwiftUI
import UIKit

/// Four corners of a document in an image, normalised to 0...1 with the origin at the top left.
/// Vision gives the detected quad; the user nudges it in `CropOverlay`.
struct CropQuad: Equatable, Sendable {
    var topLeft: CGPoint
    var topRight: CGPoint
    var bottomRight: CGPoint
    var bottomLeft: CGPoint

    static let full = CropQuad(
        topLeft: CGPoint(x: 0, y: 0), topRight: CGPoint(x: 1, y: 0),
        bottomRight: CGPoint(x: 1, y: 1), bottomLeft: CGPoint(x: 0, y: 1)
    )

    var points: [CGPoint] { [topLeft, topRight, bottomRight, bottomLeft] }
}

/// "Straighten and crop": the photo with a dimmed outside, the document outline and four draggable
/// corner handles. It only edits the quad; the perspective
/// correction itself happens in the upload queue's preparing step.
struct CropOverlay: View {
    let image: UIImage
    @Binding var quad: CropQuad

    private let handle: CGFloat = 26

    var body: some View {
        GeometryReader { geo in
            let rect = fitted(in: geo.size)
            ZStack {
                Image(uiImage: image)
                    .resizable()
                    .frame(width: rect.width, height: rect.height)
                    .position(x: rect.midX, y: rect.midY)

                outside(rect: rect)
                    .fill(Color.black.opacity(0.5), style: FillStyle(eoFill: true))
                    .allowsHitTesting(false)

                polygon(rect: rect)
                    .stroke(Color.white, style: StrokeStyle(lineWidth: 1.5, lineJoin: .round))
                    .allowsHitTesting(false)

                ForEach(0..<4, id: \.self) { index in
                    let p = point(for: index, in: rect)
                    Circle()
                        .fill(Color.white)
                        .frame(width: handle, height: handle)
                        .overlay(Circle().strokeBorder(Accent.blue, lineWidth: 3))
                        .shadow(color: .black.opacity(0.3), radius: 4, y: 1)
                        .frame(width: 44, height: 44)
                        .contentShape(Circle())
                        .position(p)
                        .gesture(
                            DragGesture(minimumDistance: 0, coordinateSpace: .local)
                                .onChanged { value in
                                    let x = min(1, max(0, (value.location.x - rect.minX) / rect.width))
                                    let y = min(1, max(0, (value.location.y - rect.minY) / rect.height))
                                    set(index, CGPoint(x: x, y: y))
                                }
                        )
                        .accessibilityLabel(["Top left corner", "Top right corner", "Bottom right corner", "Bottom left corner"][index])
                        .accessibilityAdjustableAction { direction in
                            let q = quad.points[index]
                            let step: CGFloat = direction == .increment ? 0.01 : -0.01
                            set(index, CGPoint(x: min(1, max(0, q.x + step)), y: min(1, max(0, q.y + step))))
                        }
                }
            }
        }
    }

    // MARK: Geometry

    private func fitted(in size: CGSize) -> CGRect {
        let ratio = image.size.width / max(1, image.size.height)
        var w = size.width, h = size.width / ratio
        if h > size.height { h = size.height; w = h * ratio }
        return CGRect(x: (size.width - w) / 2, y: (size.height - h) / 2, width: w, height: h)
    }

    private func point(for index: Int, in rect: CGRect) -> CGPoint {
        let p = quad.points[index]
        return CGPoint(x: rect.minX + p.x * rect.width, y: rect.minY + p.y * rect.height)
    }

    private func polygon(rect: CGRect) -> Path {
        Path { path in
            path.move(to: point(for: 0, in: rect))
            for i in 1..<4 { path.addLine(to: point(for: i, in: rect)) }
            path.closeSubpath()
        }
    }

    private func outside(rect: CGRect) -> Path {
        var path = Path(rect)
        path.addPath(polygon(rect: rect))
        return path
    }

    private func set(_ index: Int, _ value: CGPoint) {
        switch index {
        case 0: quad.topLeft = value
        case 1: quad.topRight = value
        case 2: quad.bottomRight = value
        default: quad.bottomLeft = value
        }
    }
}

#Preview("Crop overlay") {
    struct Demo: View {
        @State var quad = CropQuad(
            topLeft: CGPoint(x: 0.12, y: 0.06), topRight: CGPoint(x: 0.86, y: 0.1),
            bottomRight: CGPoint(x: 0.82, y: 0.94), bottomLeft: CGPoint(x: 0.16, y: 0.9)
        )
        let image = UIGraphicsImageRenderer(size: CGSize(width: 300, height: 400)).image { ctx in
            UIColor(white: 0.88, alpha: 1).setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 300, height: 400))
        }
        var body: some View {
            CropOverlay(image: image, quad: $quad).padding(20).background(Color.black)
        }
    }
    return Demo()
}
