import Foundation

/// The hosted service's personality for `-mock` (notes/hosted-api-contract.md sections 7, 9 and 10).
///
/// Launch arguments:
///   `-mock-mode single|hosted`   (default single)
///   `-mock-plan-state <state>`   one of none, trial, trial_cancelled, active, active_no_renew, grace, billing_retry, comped,
///                                lapsed_read_only, pending_deletion, pending_deletion_user, deleted, member_lapsed, member_active,
///                                near_limit_soft, near_limit_hard (default trial)
///   `-mock-server-info old-level|min-build-high|nopassword`   a variant of the server-info answer
/// Switch state while running: `POST /__mock/plan-state {"state": "..."}` (mock only, not part of the API).
/// The fixtures are built here instead of as JSON files; every shape is the contract's.
@MainActor
final class MockHosted {
    private(set) var state: String
    let isHosted: Bool
    private let variant: String?
    private var signedIn = false

    init(arguments: [String] = ProcessInfo.processInfo.arguments) {
        func value(after flag: String) -> String? {
            guard let i = arguments.firstIndex(of: flag), arguments.indices.contains(i + 1) else { return nil }
            return arguments[i + 1]
        }
        isHosted = value(after: "-mock-mode") == "hosted"
        state = value(after: "-mock-plan-state") ?? "trial"
        variant = value(after: "-mock-server-info")
    }

    static let readOnlyStates: Set<String> = ["billing_retry", "lapsed_read_only", "pending_deletion", "pending_deletion_user", "member_lapsed"]
    static let noPlanStates: Set<String> = ["none", "deleted"]

    // MARK: Server info

    private static let hostedLimits = PlanLimits(householdMembers: 4, maxUploadBytes: 10_485_760, receiptsPerMonth: 300, receiptsPerMonthSoft: 150, receiptsPerDay: 25,
                                                 storageBytes: 2_147_483_648, maxQueuedJobs: 50, exportsPerDay: 3, openInvites: 5)

    func serverInfo() -> ServerInfo {
        guard isHosted else {
            var features = ServerFeatures()
            features.mcp = true
            let nopw = variant == "nopassword"
            return ServerInfo(server: "shop", version: "0.18.0-mock", apiLevel: 8, mode: .single, loginMethodsRaw: nopw ? [] : ["pairing_password"], pairingEnabled: !nopw,
                              minAppBuild: 1, features: features, limits: PlanLimits(maxUploadBytes: 20_971_520), billing: nil, links: nil)
        }
        var features = ServerFeatures()
        features.households = true; features.billing = true; features.accountDeletion = true; features.asyncExport = true
        return ServerInfo(
            server: "shop", version: "0.18.0-mock", apiLevel: variant == "old-level" ? 7 : 8, mode: .hosted, loginMethodsRaw: ["apple"], pairingEnabled: false,
            minAppBuild: variant == "min-build-high" ? 9999 : 1, features: features, limits: Self.hostedLimits,
            billing: BillingInfo(provider: "apple", productIds: ["com.sundaystudi.shop.hosted.monthly"], subscriptionGroup: "Shop Hosted", trialDays: 14,
                                 storefronts: ["NLD"], manageSubscriptionUrl: URL(string: "https://apps.apple.com/account/subscriptions")),
            links: HostedLinks(termsUrl: URL(string: "https://shop.example.com/terms"), privacyUrl: URL(string: "https://shop.example.com/privacy"),
                               supportUrl: URL(string: "https://shop.example.com/support"), supportEmail: "support@shop.example.com"))
    }

    // MARK: Fixtures

    private static func days(_ n: Double) -> Date { Date().addingTimeInterval(n * 86_400) }
    private static let manage = URL(string: "https://apps.apple.com/account/subscriptions")
    private static let full = ["read", "write", "upload", "export", "invite", "manage_household", "leave_household", "delete_account"]
    private static let accountId = "6b1f0c52-2d7e-4f59-9c8a-0a3f1e7d4b21"
    private static let householdId = "c3a9d2e8-51b4-4c0e-8f3a-7d1b6e92a0f4"

    /// The plan object of section 7.5 for a state name.
    static func plan(_ state: String) -> PlanInfo {
        let product = "com.sundaystudi.shop.hosted.monthly"
        switch state {
        case "none", "deleted":
            return PlanInfo(stateRaw: state, isOwner: false, readOnly: true, allowed: ["join_household", "subscribe", "delete_account"])
        case "trial":
            return PlanInfo(stateRaw: "trial", entitlementSource: "apple", environment: "sandbox", productId: product, trialEndsAt: days(14), renewsAt: days(14), endsAt: days(14),
                            autoRenew: true, manageSubscriptionUrl: manage, allowed: full)
        case "trial_cancelled":
            return PlanInfo(stateRaw: "trial", entitlementSource: "apple", environment: "sandbox", productId: product, trialEndsAt: days(9), endsAt: days(9),
                            autoRenew: false, manageSubscriptionUrl: manage, allowed: full)
        case "active", "near_limit_soft", "near_limit_hard", "member_active":
            return PlanInfo(stateRaw: "active", entitlementSource: "apple", environment: "sandbox", isOwner: state != "member_active", productId: product, renewsAt: days(31), endsAt: days(31),
                            autoRenew: true, manageSubscriptionUrl: manage,
                            allowed: state == "member_active" ? ["read", "write", "upload", "export", "leave_household", "delete_account"] : full)
        case "active_no_renew":
            return PlanInfo(stateRaw: "active", entitlementSource: "apple", environment: "sandbox", productId: product, endsAt: days(31), autoRenew: false, manageSubscriptionUrl: manage, allowed: full)
        case "grace":
            return PlanInfo(stateRaw: "grace", entitlementSource: "apple", environment: "sandbox", productId: product, endsAt: days(-1), graceUntil: days(15), autoRenew: true,
                            manageSubscriptionUrl: manage, allowed: full)
        case "billing_retry":
            return PlanInfo(stateRaw: "billing_retry", entitlementSource: "apple", environment: "sandbox", readOnly: true, productId: product, endsAt: days(-16), autoRenew: true,
                            readOnlySince: days(-1), manageSubscriptionUrl: manage, allowed: ["read", "export", "leave_household", "delete_account"])
        case "comped":
            return PlanInfo(stateRaw: "comped", entitlementSource: "grant", endsAt: days(90), allowed: full + ["subscribe"])
        case "lapsed_read_only":
            return PlanInfo(stateRaw: "lapsed_read_only", entitlementSource: "apple", environment: "sandbox", readOnly: true, productId: product, endsAt: days(-3), autoRenew: false,
                            readOnlySince: days(-3), deleteAfter: days(27), manageSubscriptionUrl: manage, allowed: ["read", "export", "leave_household", "subscribe", "delete_account"])
        case "member_lapsed":
            return PlanInfo(stateRaw: "lapsed_read_only", entitlementSource: "apple", environment: "sandbox", isOwner: false, readOnly: true, productId: product, endsAt: days(-3),
                            readOnlySince: days(-3), deleteAfter: days(27), allowed: ["read", "export", "leave_household", "delete_account"])
        case "pending_deletion":
            return PlanInfo(stateRaw: "pending_deletion", entitlementSource: "apple", environment: "sandbox", readOnly: true, productId: product, endsAt: days(-30),
                            readOnlySince: days(-30), deleteAfter: days(2), manageSubscriptionUrl: manage, allowed: ["read", "export", "leave_household", "subscribe"])
        case "pending_deletion_user":
            return PlanInfo(stateRaw: "pending_deletion", entitlementSource: "apple", environment: "sandbox", readOnly: true, productId: product, endsAt: days(20), autoRenew: true,
                            deleteAfter: days(7), deletionRequested: true, manageSubscriptionUrl: manage, allowed: ["read", "export", "leave_household", "cancel_deletion"])
        default:
            return plan("trial")
        }
    }

    private func hasHousehold(_ state: String) -> Bool { !Self.noPlanStates.contains(state) }

    func account(withUsage: Bool = true) -> Account {
        let s = state
        let member = s == "member_lapsed" || s == "member_active"
        let near = s == "near_limit_soft" ? 148 : (s == "near_limit_hard" ? 296 : 12)
        return Account(
            id: Self.accountId, displayName: "Ama B.", email: "k3j2h1@privaterelay.appleid.com", emailIsPrivate: true,
            appAccountToken: UUID(uuidString: "0b8f0f6e-4f3c-4c8e-9a5b-6d1c2e3f4a5b"), supportCode: "K7PQ-4MXD",
            role: hasHousehold(s) ? (member ? "member" : "owner") : "none", householdId: hasHousehold(s) ? Self.householdId : nil, createdAt: Self.days(-3),
            plan: Self.plan(s),
            usage: withUsage && hasHousehold(s) ? AccountUsage(period: "2026-10", resetsAt: Self.days(18), receiptsThisMonth: near, receiptsToday: 2,
                                                              storageBytes: s == "near_limit_hard" ? 1_932_735_283 : 18_350_080, queuedJobs: 0, exportsToday: 0, openInvites: 0, members: 1) : nil,
            limits: withUsage && hasHousehold(s) ? Self.hostedLimits : nil)
    }

    func household() -> Household? {
        guard hasHousehold(state) else { return nil }
        return Household(id: Self.householdId, name: "Home", ownerAccountId: Self.accountId, memberCap: 4, yourRole: state.hasPrefix("member") ? "member" : "owner",
                         createdAt: Self.days(-3), members: [HouseholdMember(accountId: Self.accountId, displayName: "Ama B.", role: "owner", joinedAt: Self.days(-3), isYou: true)])
    }

    // MARK: Errors

    private func problem(_ status: Int, _ title: String, _ detail: String, code: String, planState: String? = nil, deleteAfter: Date? = nil, encoder: JSONEncoder) -> (Int, Data) {
        var p = Problem(type: nil, title: title, status: status, detail: detail, code: code)
        p.planState = planState
        p.deleteAfter = deleteAfter
        return (status, (try? encoder.encode(p)) ?? Data())
    }

    // MARK: Routing

    /// nil = not mine, the normal mock answers.
    func handle(_ method: String, _ p: [String], _ shape: String, _ body: Data, encoder: JSONEncoder, decoder: JSONDecoder) -> (Int, Data)? {
        func ok<T: Encodable>(_ value: T, _ status: Int = 200) -> (Int, Data) { (status, (try? encoder.encode(value)) ?? Data()) }

        if shape == "__mock/plan-state", method == "POST" {
            struct Body: Decodable { var state: String }
            if let b = try? decoder.decode(Body.self, from: body) { state = b.state }
            return (204, Data())
        }
        if shape == "v1/server-info", method == "GET" { return ok(serverInfo()) }
        guard isHosted else {
            // A single-household server has none of the hosted routes.
            if ["v1/account", "v1/household"].contains(shape) || shape.hasPrefix("v1/billing") || shape == "v1/auth/apple" || shape == "v1/auth/apple/nonce" {
                return problem(404, "Not found", "Only on the hosted service.", code: "hosted_only", encoder: encoder)
            }
            return nil
        }

        switch (method, shape) {
        case ("POST", "v1/auth/pair"):
            return problem(404, "Not found", "Use Sign in with Apple.", code: "login_method_unavailable", encoder: encoder)
        case ("POST", "v1/auth/apple/nonce"):
            return ok(AppleNonce(nonce: "kP3cVn0o5q8f2z1Yw7oQ0M3b9sT4uX6aD1eR8hJ2L5s", expiresAt: Self.days(0.007)), 201)
        case ("POST", "v1/auth/apple"):
            guard let b = try? decoder.decode(AppleSignInBody.self, from: body) else { return problem(400, "Bad request", "Invalid body.", code: "invalid_request", encoder: encoder) }
            if b.nonce == "mock-used-nonce" { return problem(401, "Apple sign-in failed", "The nonce is not valid.", code: "apple_nonce_invalid", encoder: encoder) }
            switch b.identityToken {
            case "mock.invalid": return problem(401, "Apple sign-in failed", "The Apple identity token could not be verified.", code: "apple_token_invalid", encoder: encoder)
            case "mock.locked": return problem(403, "Account locked", "Contact support.", code: "account_locked", encoder: encoder)
            case "mock.valid.new": state = "none"
            case "mock.valid.lapsed": state = "lapsed_read_only"
            case "mock.valid.existing": state = "trial"
            default: break
            }
            signedIn = true
            let created = b.identityToken == "mock.valid.new"
            return ok(AppleSignInResponse(created: created, token: "st_mock", device: Device(id: 41, name: b.deviceName, createdAt: .now, lastUsedAt: .now, current: true),
                                          account: account(withUsage: false), household: household()), created ? 201 : 200)
        case ("GET", "v1/account"):
            return ok(account())
        case ("GET", "v1/household"):
            guard let h = household() else { return problem(404, "Not found", "No household.", code: "no_household", encoder: encoder) }
            return ok(h)
        case ("GET", "v1/billing/plan-states"):
            return ok(PlanStatesTable(states: ["none", "trial", "active", "grace", "billing_retry", "comped", "lapsed_read_only", "pending_deletion", "deleted"].map {
                let plan = Self.plan($0)
                return PlanStatesTable.Row(state: $0, readOnly: plan.readOnly, allowed: plan.allowed, hasHouseholdData: hasHousehold($0))
            }))
        case ("POST", "v1/billing/apple/transactions"):
            guard let b = try? decoder.decode(SignedTransactionBody.self, from: body) else { return problem(400, "Bad request", "Invalid body.", code: "invalid_request", encoder: encoder) }
            switch b.signedTransaction {
            case "mock.jws.invalid": return problem(400, "Invalid transaction", "The transaction could not be verified.", code: "apple_transaction_invalid", encoder: encoder)
            case "mock.jws.other-account": return problem(409, "Subscription belongs to another account", "This subscription is linked to a different Shop sign-in.", code: "subscription_belongs_to_other_account", encoder: encoder)
            case "mock.jws.apple-down": return problem(502, "Apple unavailable", "Try again later.", code: "apple_unavailable", encoder: encoder)
            default:
                let already = !Self.noPlanStates.contains(state)
                if !already { state = "trial" }
                return ok(BillingResult(result: already ? "already_bound" : "bound", account: account(withUsage: false), household: household()))
            }
        case ("POST", "v1/billing/apple/refresh"):
            return ok(BillingRefresh(account: account(withUsage: false)))
        case ("DELETE", "v1/account"):
            struct Body: Decodable { var confirm: String? }
            guard (try? decoder.decode(Body.self, from: body))?.confirm == "delete my account" else {
                return problem(400, "Bad request", "Type the confirmation text.", code: "invalid_request", encoder: encoder)
            }
            state = "pending_deletion_user"
            return ok(DeletionScheduled(status: "scheduled", requestedAt: .now, deleteAfter: Self.days(7), subscriptionStillActive: true, manageSubscriptionUrl: Self.manage, canCancel: true), 202)
        default: break
        }

        // The plan gate in front of every household route (section 10.1).
        let top = p.count > 1 ? p[1] : ""
        let accountLevel = ["auth", "account", "household", "billing", "server-info"].contains(top) || p.first != "v1"
        guard !accountLevel else { return nil }
        if Self.noPlanStates.contains(state) {
            return problem(403, "Plan required", "Start the free trial to use Shop.", code: "plan_required", planState: state, encoder: encoder)
        }
        let isWrite = !["GET", "HEAD"].contains(method)
        let pure = shape == "v1/translate" || shape == "v1/glossary/preview"
        if isWrite, Self.readOnlyStates.contains(state), !pure {
            let plan = Self.plan(state)
            return problem(403, "Read-only", "This plan is read-only. Subscribe to make changes.", code: "read_only", planState: plan.stateRaw, deleteAfter: plan.deleteAfter, encoder: encoder)
        }
        return nil
    }
}

nonisolated struct PlanStatesTable: Codable, Sendable {
    struct Row: Codable, Sendable {
        var state: String
        var readOnly: Bool
        var allowed: [String]
        var hasHouseholdData: Bool
    }
    var states: [Row]
}

nonisolated struct DeletionScheduled: Codable, Sendable {
    var status: String
    var requestedAt: Date
    var deleteAfter: Date
    var subscriptionStillActive: Bool
    var manageSubscriptionUrl: URL?
    var canCancel: Bool
}
