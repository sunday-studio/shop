import Foundation

/// Shop logos come from the server (`GET /v1/shops/{id}/logo`, fetched once from logo.dev and
/// cached there). `Catalog` registers each shop's logo URL here; `ShopMark` loads it through
/// `ImageCache` (disk + memory, bearer token attached) and shows the monogram until it loads
/// or when there is no logo (404).
@MainActor
enum ShopLogos {
    private static var urls: [String: URL] = [:]

    static func register(_ shops: [Shop], api: APIClient) {
        var map: [String: URL] = [:]
        for shop in shops {
            guard let path = shop.logoUrl, let url = api.url(path) else { continue }
            map[shop.name.lowercased()] = url
        }
        urls = map
    }

    static func url(for shopName: String) -> URL? {
        urls[shopName.lowercased()]
    }

    /// Cache key per shop name.
    static func cacheKey(for shopName: String) -> String { "logo:" + shopName.lowercased() }
}
