import Foundation

/// Build-time settings for the hosted service. The values come from Config/Shop.xcconfig.
enum HostedConfig {
    /// The product id from the xcconfig (Info.plist `ShopSubscriptionProductID`).
    static var fallbackProductID: String {
        Bundle.main.object(forInfoDictionaryKey: "ShopSubscriptionProductID") as? String ?? "com.sundaystudi.shop.hosted.monthly"
    }

    /// The products to load from StoreKit: what the server lists, else the build's own.
    static func productIDs(_ info: ServerInfo?) -> [String] {
        let listed = info?.billing?.productIds ?? []
        return listed.isEmpty ? [fallbackProductID] : listed
    }
}
