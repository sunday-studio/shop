import UIKit
import XCTest
@testable import Shop

final class StubImageProtocol: URLProtocol {
    nonisolated(unsafe) static var responder: (URL) -> (Int, Data) = { _ in (500, Data()) }
    nonisolated(unsafe) static var hits: [String] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let url = request.url!
        Self.hits.append(url.absoluteString)
        let (status, data) = Self.responder(url)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@MainActor
final class ImageCacheTests: XCTestCase {
    private func png() -> Data {
        UIGraphicsImageRenderer(size: CGSize(width: 8, height: 8)).pngData { ctx in
            UIColor.red.setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 8, height: 8))
        }
    }

    override func setUp() { StubImageProtocol.hits = [] }

    func testATemporaryFailureIsRetriedNextTime() async {
        let cache = ImageCache(protocolClasses: [StubImageProtocol.self])
        let url = URL(string: "http://test.local/v1/products/1/image?v=a")!
        StubImageProtocol.responder = { _ in (503, Data()) }
        let first = await cache.image(for: url)
        XCTAssertNil(first)
        let data = png()
        StubImageProtocol.responder = { _ in (200, data) }
        let second = await cache.image(for: url)
        XCTAssertNotNil(second, "one bad moment must not blank the image for the whole session")
    }

    func testAnUnauthorisedAnswerIsNotRememberedEither() async {
        let cache = ImageCache(protocolClasses: [StubImageProtocol.self])
        let url = URL(string: "http://test.local/v1/products/2/image?v=a")!
        StubImageProtocol.responder = { _ in (401, Data()) }
        _ = await cache.image(for: url)
        let data = png()
        StubImageProtocol.responder = { _ in (200, data) }
        let image = await cache.image(for: url)
        XCTAssertNotNil(image)
    }

    func testAMissingImageIsNotAskedForAgainStraightAway() async {
        let cache = ImageCache(protocolClasses: [StubImageProtocol.self])
        let url = URL(string: "http://test.local/v1/products/3/image?v=a")!
        StubImageProtocol.responder = { _ in (404, Data()) }
        _ = await cache.image(for: url)
        _ = await cache.image(for: url)
        XCTAssertEqual(StubImageProtocol.hits.count, 1)
    }

    func testANewVersionIsANewImage() async {
        let cache = ImageCache(protocolClasses: [StubImageProtocol.self])
        let data = png()
        StubImageProtocol.responder = { _ in (200, data) }
        let v1 = URL(string: "http://test.local/v1/products/4/image?v=one")!
        let v2 = URL(string: "http://test.local/v1/products/4/image?v=two")!
        _ = await cache.image(for: v1)
        _ = await cache.image(for: v2)
        XCTAssertEqual(StubImageProtocol.hits, [v1.absoluteString, v2.absoluteString])
    }
}

@MainActor
final class ProductImageURLTests: XCTestCase {
    private func product(_ extra: String) -> Product {
        let json = "{\"id\":7,\"name\":\"Melk\",\"has_image\":true,\"created_at\":\"2026-10-01T10:00:00Z\",\"updated_at\":\"2026-10-02T10:00:00Z\"\(extra)}"
        return try! Coders.decoder().decode(Product.self, from: Data(json.utf8))
    }

    private let api = APIClient(transport: ScriptedTransport { _ in (200, "{}") }, baseURL: URL(string: "http://shop.local:8080"), token: "t")

    func testTheServersImageUrlIsUsedAsIs() {
        let p = product(",\"image_url\":\"/v1/products/7/image?v=abc123\",\"image_version\":\"abc123\"")
        XCTAssertEqual(api.productImageURL(p)?.absoluteString, "http://shop.local:8080/v1/products/7/image?v=abc123")
    }

    func testAnImageUrlWinsEvenWhenHasImageIsFalse() {
        let p = product(",\"image_url\":\"/v1/products/7/image?v=x\"").withHasImage(false)
        XCTAssertNotNil(api.productImageURL(p))
    }

    func testOlderServersFallBackToTheUpdatedTimeAsTheCacheKey() {
        let url = api.productImageURL(product(""))?.absoluteString
        XCTAssertTrue(url?.hasPrefix("http://shop.local:8080/v1/products/7/image?v=") == true)
    }

    func testNoImageNoUrl() {
        XCTAssertNil(api.productImageURL(product("").withHasImage(false)))
    }
}

private extension Product {
    func withHasImage(_ value: Bool) -> Product { var p = self; p.hasImage = value; return p }
}
