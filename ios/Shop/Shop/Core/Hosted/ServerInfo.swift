import Foundation

// Server discovery and the plan state for the hosted service. Shapes follow notes/hosted-api-contract.md
// (api_level 8). Every field except `server` is optional on decode so an older or newer server still
// works; anything unknown falls back to the safest value (see each type).

/// How the server runs: one household with a pairing password, or the hosted service.
nonisolated enum ServerMode: String, Codable, Sendable, Hashable {
    case single, hosted

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = ServerMode(rawValue: raw) ?? .single
    }
}

nonisolated enum LoginMethod: Sendable, Hashable {
    case pairingPassword
    case apple
    case other(String)

    init(_ raw: String) {
        switch raw {
        case "pairing_password": self = .pairingPassword
        case "apple": self = .apple
        default: self = .other(raw)
        }
    }
}

/// `features` of server-info: booleans, absent means off.
nonisolated struct ServerFeatures: Codable, Sendable, Hashable {
    var households = false
    var billing = false
    var accountDeletion = false
    var asyncExport = false
    var mcp = false

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        households = try c.decodeIfPresent(Bool.self, forKey: .households) ?? false
        billing = try c.decodeIfPresent(Bool.self, forKey: .billing) ?? false
        accountDeletion = try c.decodeIfPresent(Bool.self, forKey: .accountDeletion) ?? false
        asyncExport = try c.decodeIfPresent(Bool.self, forKey: .asyncExport) ?? false
        mcp = try c.decodeIfPresent(Bool.self, forKey: .mcp) ?? false
    }
}

/// Per-household limits. `nil` means no limit.
nonisolated struct PlanLimits: Codable, Sendable, Hashable {
    var householdMembers: Int?
    var maxUploadBytes: Int?
    var receiptsPerMonth: Int?
    var receiptsPerMonthSoft: Int?
    var receiptsPerDay: Int?
    var storageBytes: Int?
    var maxQueuedJobs: Int?
    var exportsPerDay: Int?
    var openInvites: Int?
}

nonisolated struct BillingInfo: Codable, Sendable, Hashable {
    var provider: String?
    /// The app loads StoreKit products by these ids; it never hard-codes one.
    var productIds: [String]
    var subscriptionGroup: String?
    var trialDays: Int?
    var storefronts: [String]?
    var manageSubscriptionUrl: URL?

    init(provider: String?, productIds: [String], subscriptionGroup: String?, trialDays: Int?, storefronts: [String]?, manageSubscriptionUrl: URL?) {
        self.provider = provider; self.productIds = productIds; self.subscriptionGroup = subscriptionGroup
        self.trialDays = trialDays; self.storefronts = storefronts; self.manageSubscriptionUrl = manageSubscriptionUrl
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        provider = try c.decodeIfPresent(String.self, forKey: .provider)
        productIds = try c.decodeIfPresent([String].self, forKey: .productIds) ?? []
        subscriptionGroup = try c.decodeIfPresent(String.self, forKey: .subscriptionGroup)
        trialDays = try c.decodeIfPresent(Int.self, forKey: .trialDays)
        storefronts = try c.decodeIfPresent([String].self, forKey: .storefronts)
        manageSubscriptionUrl = try c.decodeIfPresent(URL.self, forKey: .manageSubscriptionUrl)
    }
}

nonisolated struct HostedLinks: Codable, Sendable, Hashable {
    var termsUrl: URL?
    var privacyUrl: URL?
    var supportUrl: URL?
    var supportEmail: String?
}

/// `GET /v1/server-info` (public). Falls back to `/v1/auth/status` on servers that predate it.
nonisolated struct ServerInfo: Codable, Sendable, Hashable {
    /// The contract revision that introduced the hosted routes; older servers get no hosted features.
    static let hostedApiLevel = 8

    var server: String
    var version: String?
    var apiLevel: Int?
    var mode: ServerMode
    var loginMethodsRaw: [String]
    var pairingEnabled: Bool
    var minAppBuild: Int?
    var features: ServerFeatures
    var limits: PlanLimits
    var billing: BillingInfo?
    var links: HostedLinks?

    var loginMethods: [LoginMethod] { loginMethodsRaw.map(LoginMethod.init) }
    var isHosted: Bool { mode == .hosted }
    /// A hosted server older than the contract: the app hides the hosted features.
    var supportsHostedFeatures: Bool { isHosted && (apiLevel ?? 0) >= Self.hostedApiLevel }
    /// The app is older than the server allows: show the update screen.
    func requiresUpdate(build: Int) -> Bool { (minAppBuild ?? 0) > build }

    /// What a server without `server-info` looks like.
    static func legacy(version: String?, pairingEnabled: Bool) -> ServerInfo {
        ServerInfo(server: "shop", version: version, apiLevel: nil, mode: .single, loginMethodsRaw: pairingEnabled ? ["pairing_password"] : [], pairingEnabled: pairingEnabled,
                   minAppBuild: nil, features: ServerFeatures(), limits: PlanLimits(), billing: nil, links: nil)
    }

    /// The login this app can do with what the server offers. The hosted service always means Apple,
    /// so a person who types its address under "I have my own server" still gets the Apple button.
    var preferredLogin: LoginMethod? {
        let methods = loginMethods
        if isHosted, methods.contains(.apple) { return .apple }
        if methods.contains(.pairingPassword) { return .pairingPassword }
        if methods.contains(.apple) { return .apple }
        return nil
    }

    enum CodingKeys: String, CodingKey {
        case server, version, apiLevel, mode, loginMethodsRaw = "loginMethods", pairingEnabled, minAppBuild, features, limits, billing, links
    }

    init(server: String, version: String?, apiLevel: Int?, mode: ServerMode, loginMethodsRaw: [String], pairingEnabled: Bool, minAppBuild: Int?,
         features: ServerFeatures, limits: PlanLimits, billing: BillingInfo?, links: HostedLinks?) {
        self.server = server; self.version = version; self.apiLevel = apiLevel; self.mode = mode
        self.loginMethodsRaw = loginMethodsRaw; self.pairingEnabled = pairingEnabled; self.minAppBuild = minAppBuild
        self.features = features; self.limits = limits; self.billing = billing; self.links = links
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        server = try c.decodeIfPresent(String.self, forKey: .server) ?? "shop"
        version = try c.decodeIfPresent(String.self, forKey: .version)
        apiLevel = try c.decodeIfPresent(Int.self, forKey: .apiLevel)
        mode = try c.decodeIfPresent(ServerMode.self, forKey: .mode) ?? .single
        loginMethodsRaw = try c.decodeIfPresent([String].self, forKey: .loginMethodsRaw) ?? (mode == .hosted ? ["apple"] : ["pairing_password"])
        pairingEnabled = try c.decodeIfPresent(Bool.self, forKey: .pairingEnabled) ?? (mode == .single)
        minAppBuild = try c.decodeIfPresent(Int.self, forKey: .minAppBuild)
        features = try c.decodeIfPresent(ServerFeatures.self, forKey: .features) ?? ServerFeatures()
        limits = try c.decodeIfPresent(PlanLimits.self, forKey: .limits) ?? PlanLimits()
        billing = try c.decodeIfPresent(BillingInfo.self, forKey: .billing)
        links = try c.decodeIfPresent(HostedLinks.self, forKey: .links)
    }
}

// MARK: - Plan

/// The nine states of the contract (section 7). The server computes them; the app shows messages from them
/// and enables controls from `PlanInfo.allowed`.
nonisolated enum PlanState: Sendable, Hashable {
    case none, trial, active, grace, billingRetry, comped, lapsedReadOnly, pendingDeletion, deleted
    /// A state from a newer server: treated as access, the server enforces.
    case unknown(String)

    init(_ raw: String) {
        switch raw {
        case "none": self = .none
        case "trial": self = .trial
        case "active": self = .active
        case "grace": self = .grace
        case "billing_retry": self = .billingRetry
        case "comped": self = .comped
        case "lapsed_read_only": self = .lapsedReadOnly
        case "pending_deletion": self = .pendingDeletion
        case "deleted": self = .deleted
        default: self = .unknown(raw)
        }
    }

    var raw: String {
        switch self {
        case .none: "none"
        case .trial: "trial"
        case .active: "active"
        case .grace: "grace"
        case .billingRetry: "billing_retry"
        case .comped: "comped"
        case .lapsedReadOnly: "lapsed_read_only"
        case .pendingDeletion: "pending_deletion"
        case .deleted: "deleted"
        case .unknown(let s): s
        }
    }
}

nonisolated struct PlanInfo: Codable, Sendable, Hashable {
    var stateRaw: String
    var entitlementSource: String?
    var environment: String?
    var isOwner: Bool
    var readOnly: Bool
    var productId: String?
    var trialEndsAt: Date?
    var renewsAt: Date?
    var endsAt: Date?
    var graceUntil: Date?
    var autoRenew: Bool?
    var readOnlySince: Date?
    var deleteAfter: Date?
    var deletionRequested: Bool
    var manageSubscriptionUrl: URL?
    var allowed: [String]

    var state: PlanState { PlanState(stateRaw) }

    enum CodingKeys: String, CodingKey {
        case stateRaw = "state", entitlementSource, environment, isOwner, readOnly, productId, trialEndsAt, renewsAt, endsAt, graceUntil
        case autoRenew, readOnlySince, deleteAfter, deletionRequested, manageSubscriptionUrl, allowed
    }

    init(stateRaw: String, entitlementSource: String? = nil, environment: String? = nil, isOwner: Bool = true, readOnly: Bool = false, productId: String? = nil,
         trialEndsAt: Date? = nil, renewsAt: Date? = nil, endsAt: Date? = nil, graceUntil: Date? = nil, autoRenew: Bool? = nil, readOnlySince: Date? = nil,
         deleteAfter: Date? = nil, deletionRequested: Bool = false, manageSubscriptionUrl: URL? = nil, allowed: [String] = []) {
        self.stateRaw = stateRaw; self.entitlementSource = entitlementSource; self.environment = environment; self.isOwner = isOwner; self.readOnly = readOnly
        self.productId = productId; self.trialEndsAt = trialEndsAt; self.renewsAt = renewsAt; self.endsAt = endsAt; self.graceUntil = graceUntil
        self.autoRenew = autoRenew; self.readOnlySince = readOnlySince; self.deleteAfter = deleteAfter; self.deletionRequested = deletionRequested
        self.manageSubscriptionUrl = manageSubscriptionUrl; self.allowed = allowed
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        stateRaw = try c.decodeIfPresent(String.self, forKey: .stateRaw) ?? "none"
        entitlementSource = try c.decodeIfPresent(String.self, forKey: .entitlementSource)
        environment = try c.decodeIfPresent(String.self, forKey: .environment)
        isOwner = try c.decodeIfPresent(Bool.self, forKey: .isOwner) ?? false
        readOnly = try c.decodeIfPresent(Bool.self, forKey: .readOnly) ?? false
        productId = try c.decodeIfPresent(String.self, forKey: .productId)
        trialEndsAt = try c.decodeIfPresent(Date.self, forKey: .trialEndsAt)
        renewsAt = try c.decodeIfPresent(Date.self, forKey: .renewsAt)
        endsAt = try c.decodeIfPresent(Date.self, forKey: .endsAt)
        graceUntil = try c.decodeIfPresent(Date.self, forKey: .graceUntil)
        autoRenew = try c.decodeIfPresent(Bool.self, forKey: .autoRenew)
        readOnlySince = try c.decodeIfPresent(Date.self, forKey: .readOnlySince)
        deleteAfter = try c.decodeIfPresent(Date.self, forKey: .deleteAfter)
        deletionRequested = try c.decodeIfPresent(Bool.self, forKey: .deletionRequested) ?? false
        manageSubscriptionUrl = try c.decodeIfPresent(URL.self, forKey: .manageSubscriptionUrl)
        allowed = try c.decodeIfPresent([String].self, forKey: .allowed) ?? []
    }
}

nonisolated struct AccountUsage: Codable, Sendable, Hashable {
    var period: String?
    var resetsAt: Date?
    var receiptsThisMonth: Int?
    var receiptsToday: Int?
    var storageBytes: Int?
    var queuedJobs: Int?
    var exportsToday: Int?
    var openInvites: Int?
    var members: Int?
}

/// `GET /v1/account`, and the `account` of sign-in and billing responses (usage and limits only on the GET).
nonisolated struct Account: Codable, Sendable, Hashable {
    var id: String
    var displayName: String?
    var email: String?
    var emailIsPrivate: Bool?
    /// Given to StoreKit as the app account token, so a purchase is tied to this account.
    var appAccountToken: UUID?
    var supportCode: String?
    /// "owner", "member" or "none".
    var role: String?
    var householdId: String?
    var createdAt: Date?
    var plan: PlanInfo
    var usage: AccountUsage?
    var limits: PlanLimits?
}

nonisolated struct HouseholdMember: Codable, Sendable, Hashable {
    var accountId: String
    var displayName: String?
    var role: String
    var joinedAt: Date?
    var isYou: Bool?
}

nonisolated struct Household: Codable, Sendable, Hashable {
    var id: String
    var name: String?
    var ownerAccountId: String?
    var memberCap: Int?
    var yourRole: String?
    var createdAt: Date?
    var members: [HouseholdMember]?
}

// MARK: - What the person may do

/// One answer to "may I?" for every screen. A self-hosted server always allows everything; the hosted
/// service decides with `plan.allowed` (the app never re-derives it from the state).
nonisolated struct Entitlements: Sendable, Hashable {
    var mode: ServerMode
    var plan: PlanInfo?

    static let selfHosted = Entitlements(mode: .single, plan: nil)

    init(mode: ServerMode, plan: PlanInfo?) { self.mode = mode; self.plan = plan }

    init(server: ServerInfo?, account: Account?) {
        mode = server?.mode ?? .single
        plan = account?.plan
    }

    /// Hosted and signed in, but no plan (or its household is gone): show the paywall.
    var state: PlanState? { mode == .hosted ? (plan?.state ?? PlanState.none) : nil }

    func allows(_ action: String) -> Bool {
        guard mode == .hosted else { return true }
        guard let plan else { return false }
        if case .unknown = plan.state, plan.allowed.isEmpty { return true }
        return plan.allowed.contains(action)
    }

    var canRead: Bool { allows("read") }
    var canWrite: Bool { allows("write") }
    var canUpload: Bool { allows("upload") }
    var canExport: Bool { allows("export") }
    var canSubscribe: Bool { mode == .hosted && allows("subscribe") }

    var needsPaywall: Bool {
        guard let state else { return false }
        return state == .none || state == .deleted
    }
    var isReadOnly: Bool { mode == .hosted && (plan?.readOnly ?? false) && !needsPaywall }
    var isTrial: Bool { state == .trial }
    var needsPaymentFix: Bool { state == .grace }
    var isComplimentary: Bool { state == .comped }
    var isOwner: Bool { mode != .hosted || (plan?.isOwner ?? false) }
    var isPendingDeletion: Bool { state == .pendingDeletion }

    /// Active (or trial) but will not renew: "Ends on 12 Nov".
    var endsWithoutRenewal: Date? {
        guard let plan, plan.autoRenew == false, state == .active || state == .trial else { return nil }
        return plan.endsAt
    }

    /// Whole days left to export before the data is deleted (lapsed or pending deletion).
    func daysUntilDeletion(now: Date = .now) -> Int? {
        guard let due = plan?.deleteAfter, state == .lapsedReadOnly || state == .pendingDeletion else { return nil }
        let cal = Dates.calendar
        return max(0, cal.dateComponents([.day], from: cal.startOfDay(for: now), to: cal.startOfDay(for: due)).day ?? 0)
    }
}
