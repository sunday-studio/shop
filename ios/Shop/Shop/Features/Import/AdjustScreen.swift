import SwiftUI

extension CropQuad {
    /// The outline after the picture is turned 90° clockwise.
    var rotatedClockwise: CropQuad {
        func turn(_ p: CGPoint) -> CGPoint { CGPoint(x: 1 - p.y, y: p.x) }
        return CropQuad(topLeft: turn(bottomLeft), topRight: turn(topLeft), bottomRight: turn(topRight), bottomLeft: turn(bottomRight))
    }
}

/// S8 · Straighten & crop: drag the four corners to the receipt's edges.
struct AdjustScreen: View {
    let model: ImportModel
    let receiptId: UUID
    let pageId: UUID

    @Environment(\.dismiss) private var dismiss
    @State private var image: CGImage?
    @State private var quad: CropQuad = .full
    @State private var detected: CropQuad = .full
    @State private var rotation = 0

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                IconButton(icon: .back, label: "Back") { dismiss() }
                Spacer()
                Text("Straighten & crop").textStyle(.navTitle).foregroundStyle(Palette.ink)
                Spacer()
                Button {
                    rotate()
                } label: {
                    Image(systemName: "rotate.right")
                        .font(.system(size: 17, weight: .semibold))
                        .foregroundStyle(Palette.ink)
                        .frame(width: Size.iconButton, height: Size.iconButton)
                        .background(Palette.sunk, in: Circle())
                }
                .buttonStyle(.press(haptic: .selection))
                .accessibilityLabel("Rotate")
            }
            .padding(.horizontal, Space.gutter)
            .padding(.top, 8)

            ZStack {
                Color.black
                if let image {
                    CropOverlay(image: UIImage(cgImage: image), quad: $quad)
                        .padding(Space.l)
                } else {
                    ProgressView().tint(.white)
                }
            }
            .clipShape(RoundedRectangle.continuous(Radius.l))
            .padding(.horizontal, Space.edge)
            .padding(.top, Space.l)

            Text("Drag the corners to the receipt's edges.")
                .textStyle(.rowSub)
                .foregroundStyle(Palette.ink2)
                .padding(.top, Space.m)

            HStack(spacing: Space.m) {
                Button("Use original") { useOriginal() }
                    .buttonStyle(.shop(.secondary, size: .small, fullWidth: false, haptic: .selection))
                Button("Reset") {
                    withAnimation(Motion.spring) { quad = detected }
                }
                .textStyle(.label)
                .foregroundStyle(Palette.blueText)
                .buttonStyle(.press(haptic: .selection))
                .padding(.horizontal, Space.m)
                Spacer()
                Button("Done") { done() }
                    .buttonStyle(.shop(fullWidth: false))
            }
            .padding(.horizontal, Space.gutter)
            .padding(.vertical, Space.l)
        }
        .background(Palette.bg)
        .task { await load() }
    }

    private var page: ImportModel.Page? { model.receipt(receiptId)?.pages.first { $0.id == pageId } }

    private func load() async {
        guard let page else { return }
        let data = page.data
        let turns = page.rotation
        let storedQuad = page.quad
        let result: (CGImage, StoredQuad?)? = await Task.detached(priority: .userInitiated) {
            guard let base = Straightener.upright(data, maxSide: 1800) else { return nil }
            let rotated = Straightener.rotated(base, quarterTurns: turns)
            if let stored = storedQuad { return (rotated, stored) }
            if case .quad(let found) = Straightener.detect(in: rotated) { return (rotated, found) }
            return (rotated, nil)
        }.value
        guard let (cg, found) = result else { return }
        rotation = turns
        image = cg
        detected = found?.cropQuad ?? .full
        quad = found?.cropQuad ?? .full
    }

    private func rotate() {
        guard let current = image else { return }
        image = Straightener.rotated(current, quarterTurns: 1)
        withAnimation(Motion.spring) {
            quad = quad.rotatedClockwise
            detected = detected.rotatedClockwise
        }
        rotation = (rotation + 1) % 4
    }

    private func useOriginal() {
        let turns = rotation
        model.updatePage(pageId, in: receiptId) { page in
            page.straighten = .original
            page.quad = nil
            page.rotation = turns
            if let cg = image { page.thumbnail = Self.thumbnail(cg, quad: nil) }
        }
        dismiss()
    }

    private func done() {
        let turns = rotation
        let stored = StoredQuad(quad)
        model.updatePage(pageId, in: receiptId) { page in
            page.straighten = .adjusted
            page.quad = stored
            page.rotation = turns
            if let cg = image { page.thumbnail = Self.thumbnail(cg, quad: stored) }
        }
        Haptics.play(.light)
        dismiss()
    }

    private static func thumbnail(_ image: CGImage, quad: StoredQuad?) -> UIImage {
        var cg = image
        if let quad, let corrected = Straightener.apply(quad, to: image, maxSide: 420) { cg = corrected }
        let long = CGFloat(max(cg.width, cg.height))
        let scale = min(1, 420 / long)
        let size = CGSize(width: CGFloat(cg.width) * scale, height: CGFloat(cg.height) * scale)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        return UIGraphicsImageRenderer(size: size, format: format).image { _ in
            UIImage(cgImage: cg).draw(in: CGRect(origin: .zero, size: size))
        }
    }
}
