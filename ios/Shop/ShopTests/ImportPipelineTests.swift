import XCTest
import UIKit
@testable import Shop

@MainActor
final class ImportPipelineTests: XCTestCase {
    private func image(width: Int, height: Int, color: UIColor = .white) -> UIImage {
        let format = UIGraphicsImageRendererFormat(); format.scale = 1
        return UIGraphicsImageRenderer(size: CGSize(width: width, height: height), format: format).image { ctx in
            color.setFill(); ctx.fill(CGRect(x: 0, y: 0, width: width, height: height))
        }
    }

    func testRotatingByQuarterTurnsSwapsTheSides() {
        let cg = image(width: 200, height: 100).cgImage!
        let once = Straightener.rotated(cg, quarterTurns: 1)
        XCTAssertEqual(once.width, 100)
        XCTAssertEqual(once.height, 200)
        let twice = Straightener.rotated(cg, quarterTurns: 2)
        XCTAssertEqual(twice.width, 200)
        let negative = Straightener.rotated(cg, quarterTurns: -1)
        XCTAssertEqual(negative.width, 100)
        XCTAssertEqual(Straightener.rotated(cg, quarterTurns: 4).width, 200, "four turns is no turn")
    }

    func testUprightDownsamplesToTheLongSide() {
        let data = image(width: 4000, height: 2000).jpegData(compressionQuality: 0.8)!
        let small = Straightener.upright(data, maxSide: 1000)!
        XCTAssertEqual(max(small.width, small.height), 1000)
        XCTAssertNil(Straightener.upright(Data([1, 2, 3]), maxSide: 100))
    }

    func testFullFrameQuadKeepsTheSize() {
        let cg = image(width: 600, height: 900).cgImage!
        let quad = StoredQuad(points: [CGPoint(x: 0, y: 0), CGPoint(x: 1, y: 0), CGPoint(x: 1, y: 1), CGPoint(x: 0, y: 1)])
        let out = Straightener.apply(quad, to: cg)
        XCTAssertNotNil(out)
        XCTAssertEqual(Double(out!.width), 600, accuracy: 2)
        XCTAssertEqual(Double(out!.height), 900, accuracy: 2)
    }

    func testCroppingToHalfProducesAHalfSizedImage() {
        let cg = image(width: 600, height: 900).cgImage!
        let quad = StoredQuad(points: [CGPoint(x: 0.25, y: 0.25), CGPoint(x: 0.75, y: 0.25), CGPoint(x: 0.75, y: 0.75), CGPoint(x: 0.25, y: 0.75)])
        let out = Straightener.apply(quad, to: cg)!
        XCTAssertEqual(Double(out.width), 300, accuracy: 3)
        XCTAssertEqual(Double(out.height), 450, accuracy: 3)
    }

    func testOutputIsCappedAtMaxSide() {
        let cg = image(width: 3000, height: 4000).cgImage!
        let quad = StoredQuad(points: [CGPoint(x: 0, y: 0), CGPoint(x: 1, y: 0), CGPoint(x: 1, y: 1), CGPoint(x: 0, y: 1)])
        let out = Straightener.apply(quad, to: cg, maxSide: 1000)!
        XCTAssertLessThanOrEqual(max(out.width, out.height), 1001)
    }

    func testQuadSurvivesCodable() throws {
        let quad = StoredQuad(points: [CGPoint(x: 0.1, y: 0.1), CGPoint(x: 0.9, y: 0.1), CGPoint(x: 0.9, y: 0.9), CGPoint(x: 0.1, y: 0.9)])
        let back = try JSONDecoder().decode(StoredQuad.self, from: try JSONEncoder().encode(quad))
        XCTAssertEqual(back, quad)
        let edit = PageEdit(file: "p1.jpg", quad: quad, detect: false, adjusted: true, rotation: 1)
        XCTAssertEqual(try JSONDecoder().decode(PageEdit.self, from: try JSONEncoder().encode(edit)), edit)
    }

    func testStitchStacksPagesAndEncodesJPEG() throws {
        let data = Stitcher.stitch([image(width: 400, height: 300), image(width: 400, height: 500)])!
        let result = UIImage(data: data)!
        XCTAssertEqual(Double(result.size.width), 400, accuracy: 1)
        XCTAssertEqual(Double(result.size.height), 800, accuracy: 2)
        XCTAssertEqual(data.prefix(2), Data([0xFF, 0xD8]), "a JPEG")
    }

    func testStitchScalesToTheCommonWidthAndCapsIt() {
        let data = Stitcher.stitch([image(width: 4000, height: 1000)], maxWidth: 2400)!
        XCTAssertEqual(Double(UIImage(data: data)!.size.width), 2400, accuracy: 1)
        XCTAssertNil(Stitcher.stitch([]))
    }

    func testCaptureDateIsNilWithoutExif() {
        XCTAssertNil(Straightener.captureDate(image(width: 50, height: 50).jpegData(compressionQuality: 0.8)!))
    }

    func testDetectLeavesAPlainImageAlone() {
        let result = Straightener.detect(in: image(width: 800, height: 1200).cgImage!)
        if case .quad = result { XCTFail("a blank frame is not a document outline") }
    }
}
