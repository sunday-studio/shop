import XCTest
@testable import Shop

@MainActor
private func scripted(_ token: String? = "t", _ handler: @escaping (ScriptedTransport.Call) throws -> (Int, String)) -> (APIClient, ScriptedTransport) {
    let transport = ScriptedTransport(handler)
    return (APIClient(transport: transport, baseURL: URL(string: "http://test.local"), token: token), transport)
}

private let hostedInfoJSON = """
{"server":"shop","version":"0.18.0","api_level":8,"mode":"hosted","login_methods":["apple"],"pairing_enabled":false,"min_app_build":3,
 "features":{"households":true,"billing":true,"account_deletion":true,"async_export":true,"mcp":false},
 "limits":{"household_members":4,"max_upload_bytes":10485760,"receipts_per_month":300,"receipts_per_month_soft":150,"receipts_per_day":25,"storage_bytes":2147483648,"max_queued_jobs":50,"exports_per_day":3,"open_invites":5},
 "billing":{"provider":"apple","product_ids":["com.sundaystudi.shop.hosted.monthly"],"subscription_group":"Shop Hosted","trial_days":14,"storefronts":["NLD"],"price_display":"storekit","manage_subscription_url":"https://apps.apple.com/account/subscriptions"},
 "links":{"terms_url":"https://x.test/t","privacy_url":"https://x.test/p","support_url":"https://x.test/s","support_email":"s@x.test"}}
"""

private let singleInfoJSON = """
{"server":"shop","version":"0.18.0","api_level":8,"mode":"single","login_methods":["pairing_password"],"pairing_enabled":true,"min_app_build":1,
 "features":{"households":false,"billing":false,"account_deletion":false,"async_export":false,"mcp":true},
 "limits":{"household_members":null,"max_upload_bytes":20971520,"receipts_per_month":null,"storage_bytes":null}}
"""

@MainActor
final class ServerInfoTests: XCTestCase {
    func testHostedServerInfoDecodes() async throws {
        let (api, _) = scripted("t") { _ in (200, hostedInfoJSON) }
        let info = try await api.serverInfo()
        XCTAssertEqual(info.mode, .hosted)
        XCTAssertEqual(info.preferredLogin, .apple)
        XCTAssertTrue(info.supportsHostedFeatures)
        XCTAssertTrue(info.features.billing)
        XCTAssertFalse(info.features.mcp)
        XCTAssertEqual(info.limits.receiptsPerMonth, 300)
        XCTAssertEqual(info.limits.receiptsPerMonthSoft, 150)
        XCTAssertEqual(info.billing?.productIds, ["com.sundaystudi.shop.hosted.monthly"])
        XCTAssertEqual(info.billing?.trialDays, 14)
        XCTAssertEqual(info.links?.privacyUrl?.absoluteString, "https://x.test/p")
        XCTAssertTrue(info.requiresUpdate(build: 2))
        XCTAssertFalse(info.requiresUpdate(build: 3))
    }

    func testSingleServerInfoWithNullLimits() async throws {
        let (api, _) = scripted("t") { _ in (200, singleInfoJSON) }
        let info = try await api.serverInfo()
        XCTAssertEqual(info.mode, .single)
        XCTAssertEqual(info.preferredLogin, .pairingPassword)
        XCTAssertNil(info.limits.receiptsPerMonth)
        XCTAssertEqual(info.limits.maxUploadBytes, 20_971_520)
        XCTAssertTrue(info.features.mcp)
        XCTAssertFalse(info.supportsHostedFeatures)
    }

    func testSingleWithoutAPairingPasswordHasNoLogin() async throws {
        let (api, _) = scripted("t") { _ in (200, "{\"server\":\"shop\",\"mode\":\"single\",\"login_methods\":[],\"pairing_enabled\":false}") }
        let info = try await api.serverInfo()
        XCTAssertNil(info.preferredLogin)
    }

    func testOldHostedLevelHidesHostedFeatures() async throws {
        let (api, _) = scripted("t") { _ in (200, hostedInfoJSON.replacingOccurrences(of: "\"api_level\":8", with: "\"api_level\":7")) }
        let info = try await api.serverInfo()
        XCTAssertTrue(info.isHosted)
        XCTAssertFalse(info.supportsHostedFeatures)
    }

    func testMinimalShapeAndUnknownValuesAreSafe() async throws {
        let (api, _) = scripted("t") { _ in (200, "{\"server\":\"shop\",\"mode\":\"cluster\",\"api_version\":2}") }
        let info = try await api.serverInfo()
        XCTAssertEqual(info.mode, .single, "an unknown mode is treated as a single household")
        XCTAssertEqual(info.preferredLogin, .pairingPassword)
        XCTAssertFalse(info.features.billing)
    }

    func testOlderServerFallsBackToAuthStatus() async throws {
        let (api, transport) = scripted("t") { c in
            c.path == "/v1/server-info" ? (404, "{\"title\":\"Not found\",\"status\":404}") : (200, "{\"server\":\"shop\",\"version\":\"0.3.0\",\"pairing_enabled\":true}")
        }
        let info = try await api.serverInfo()
        XCTAssertEqual(info.mode, .single)
        XCTAssertEqual(info.version, "0.3.0")
        XCTAssertEqual(info.preferredLogin, .pairingPassword)
        XCTAssertEqual(transport.paths(), ["GET /v1/server-info", "GET /v1/auth/status"])
    }

    func testTypingTheHostedAddressInDoorTwoStillPicksApple() {
        var info = ServerInfo.legacy(version: nil, pairingEnabled: true)
        info.mode = .hosted
        info.loginMethodsRaw = ["apple", "pairing_password"]
        XCTAssertEqual(info.preferredLogin, .apple)
        info.mode = .single
        info.loginMethodsRaw = ["passkey"]
        XCTAssertNil(info.preferredLogin)
    }
}

@MainActor
final class EntitlementsTests: XCTestCase {
    private func hosted(_ state: String) -> Entitlements { Entitlements(mode: .hosted, plan: MockHosted.plan(state)) }

    func testSelfHostedAlwaysHasFullAccess() {
        let e = Entitlements.selfHosted
        XCTAssertTrue(e.canWrite && e.canUpload && e.canExport && e.canRead)
        XCTAssertFalse(e.needsPaywall)
        XCTAssertFalse(e.isReadOnly)
        XCTAssertNil(e.state)
    }

    func testEveryStateFollowsTheAllowedList() {
        let writable = ["trial", "trial_cancelled", "active", "active_no_renew", "grace", "comped", "near_limit_soft", "near_limit_hard"]
        for s in writable {
            XCTAssertTrue(hosted(s).canWrite, s)
            XCTAssertTrue(hosted(s).canUpload, s)
            XCTAssertFalse(hosted(s).isReadOnly, s)
        }
        for s in ["billing_retry", "lapsed_read_only", "pending_deletion", "pending_deletion_user", "member_lapsed"] {
            XCTAssertFalse(hosted(s).canWrite, s)
            XCTAssertTrue(hosted(s).canRead, s)
            XCTAssertTrue(hosted(s).canExport, s)
            XCTAssertTrue(hosted(s).isReadOnly, s)
        }
        for s in ["none", "deleted"] {
            XCTAssertTrue(hosted(s).needsPaywall, s)
            XCTAssertFalse(hosted(s).canRead, s)
            XCTAssertTrue(hosted(s).canSubscribe, s)
        }
    }

    func testSignedInWithoutAPlanNeedsThePaywall() {
        XCTAssertTrue(Entitlements(mode: .hosted, plan: nil).needsPaywall)
        XCTAssertFalse(Entitlements(mode: .hosted, plan: nil).canRead)
    }

    func testBannersAndLabels() {
        XCTAssertTrue(hosted("trial").isTrial)
        XCTAssertTrue(hosted("grace").needsPaymentFix)
        XCTAssertTrue(hosted("comped").isComplimentary)
        XCTAssertTrue(hosted("comped").canSubscribe, "a comped person can subscribe to continue")
        XCTAssertFalse(hosted("active").canSubscribe)
        XCTAssertTrue(hosted("pending_deletion_user").isPendingDeletion)
        XCTAssertFalse(hosted("member_lapsed").isOwner)
        XCTAssertNotNil(hosted("active_no_renew").endsWithoutRenewal)
        XCTAssertNotNil(hosted("trial_cancelled").endsWithoutRenewal)
        XCTAssertNil(hosted("active").endsWithoutRenewal)
    }

    func testDaysUntilDeletion() {
        XCTAssertEqual(hosted("lapsed_read_only").daysUntilDeletion(), 27)
        XCTAssertEqual(hosted("pending_deletion").daysUntilDeletion(), 2)
        XCTAssertNil(hosted("active").daysUntilDeletion())
    }

    func testAStateFromANewerServerDoesNotLockAnyoneOut() {
        let e = Entitlements(mode: .hosted, plan: PlanInfo(stateRaw: "something_new"))
        XCTAssertTrue(e.canWrite)
        XCTAssertEqual(PlanState("something_new"), .unknown("something_new"))
    }

    func testAllNineStatesAreKnown() {
        for raw in ["none", "trial", "active", "grace", "billing_retry", "comped", "lapsed_read_only", "pending_deletion", "deleted"] {
            XCTAssertEqual(PlanState(raw).raw, raw)
        }
    }

    func testContractErrorsBecomeTheirOwnCases() async {
        func status(_ code: Int, _ json: String) async -> APIError? {
            let (api, _) = scripted("t") { _ in (code, json) }
            do { try await api.perform("POST", "/v1/shops"); return nil } catch { return error as? APIError }
        }
        if case .readOnly(let p)? = await status(403, "{\"title\":\"Read-only\",\"status\":403,\"code\":\"read_only\",\"plan_state\":\"lapsed_read_only\",\"delete_after\":\"2026-11-13T10:00:00Z\"}") {
            XCTAssertEqual(p.planState, "lapsed_read_only")
            XCTAssertNotNil(p.deleteAfter)
        } else { XCTFail("read_only") }
        if case .planRequired? = await status(403, "{\"title\":\"Plan required\",\"status\":403,\"code\":\"plan_required\",\"plan_state\":\"none\"}") {} else { XCTFail("plan_required") }
        if case .limitReached(let p)? = await status(429, "{\"title\":\"Limit reached\",\"status\":429,\"code\":\"limit_reached\",\"limit\":\"receipts_per_month\",\"limit_value\":300,\"current\":300,\"resets_at\":\"2026-11-01T00:00:00Z\"}") {
            XCTAssertEqual(p.limit, "receipts_per_month")
            XCTAssertEqual(p.limitValue, 300)
        } else { XCTFail("limit_reached") }
        if case .rateLimited? = await status(429, "{\"title\":\"Too many requests\",\"status\":429,\"code\":\"rate_limited\",\"retry_after_seconds\":30}") {} else { XCTFail("rate_limited") }
        if case .problem(let p)? = await status(403, "{\"title\":\"Locked\",\"status\":403,\"code\":\"account_locked\"}") {
            XCTAssertEqual(p.code, "account_locked")
        } else { XCTFail("account_locked") }
    }
}

@MainActor
final class AccountDecodingTests: XCTestCase {
    func testAccountWithUsageAndLimits() async throws {
        let mock = MockHosted(arguments: ["-mock-mode", "hosted", "-mock-plan-state", "near_limit_soft"])
        let data = try Coders.encoder().encode(mock.account())
        let decoded = try Coders.decoder().decode(Account.self, from: data)
        XCTAssertEqual(decoded.usage?.receiptsThisMonth, 148)
        XCTAssertEqual(decoded.limits?.receiptsPerMonthSoft, 150)
        XCTAssertEqual(decoded.role, "owner")
        XCTAssertNotNil(decoded.appAccountToken)
    }

    func testAccountWithoutAHousehold() {
        let mock = MockHosted(arguments: ["-mock-mode", "hosted", "-mock-plan-state", "none"])
        XCTAssertEqual(mock.account().role, "none")
        XCTAssertNil(mock.account().householdId)
        XCTAssertNil(mock.household())
        XCTAssertNil(mock.account().usage)
    }

    func testMemberOfALapsedHousehold() {
        let mock = MockHosted(arguments: ["-mock-mode", "hosted", "-mock-plan-state", "member_lapsed"])
        let account = mock.account()
        XCTAssertEqual(account.role, "member")
        XCTAssertFalse(account.plan.isOwner)
        XCTAssertFalse(Entitlements(mode: .hosted, plan: account.plan).canSubscribe)
    }
}

@MainActor
final class AuthenticationTests: XCTestCase {
    private struct FakeApple: AppleCredentialProvider {
        var seenNonce: (String) -> Void = { _ in }
        func credential(hashedNonce: String) async throws -> AppleCredential {
            seenNonce(hashedNonce)
            return AppleCredential(identityToken: "jwt", authorizationCode: "code")
        }
    }

    func testPairingPasswordSignsIn() async throws {
        let (api, transport) = scripted(nil) { _ in (201, "{\"token\":\"st_abc\",\"device\":{\"id\":1,\"name\":\"iPhone\",\"created_at\":\"2026-10-12T10:00:00Z\",\"current\":true}}") }
        let session = try await PairingPasswordAuth(password: "pw").signIn(api: api, deviceName: "iPhone")
        XCTAssertEqual(session.token, "st_abc")
        XCTAssertNil(session.account)
        XCTAssertEqual(transport.paths("POST"), ["POST /v1/auth/pair"])
    }

    func testWrongPasswordIsItsOwnError() async {
        let (api, _) = scripted(nil) { _ in (401, "{\"title\":\"Wrong password\",\"status\":401}") }
        do {
            _ = try await PairingPasswordAuth(password: "nope").signIn(api: api, deviceName: "iPhone")
            XCTFail("should throw")
        } catch { XCTAssertEqual(error as? AuthError, .wrongPassword) }
    }

    func testAppleSendsTheHashedNonceToAppleAndTheRawNonceToTheServer() async throws {
        let (api, transport) = scripted(nil) { c in
            c.path == "/v1/auth/apple/nonce" ? (201, "{\"nonce\":\"raw-nonce\",\"expires_at\":\"2026-10-14T09:40:00Z\"}")
                : (201, "{\"created\":true,\"token\":\"st_apple\",\"account\":{\"id\":\"a1\",\"support_code\":\"K7PQ-4MXD\",\"role\":\"none\",\"plan\":{\"state\":\"none\",\"allowed\":[\"subscribe\"]}},\"household\":null}")
        }
        var hashed = ""
        let session = try await AppleAuth(credentials: FakeApple(seenNonce: { hashed = $0 })).signIn(api: api, deviceName: "iPhone")
        XCTAssertEqual(hashed, AppleAuth.sha256("raw-nonce"))
        XCTAssertEqual(hashed.count, 64)
        XCTAssertEqual(transport.paths("POST"), ["POST /v1/auth/apple/nonce", "POST /v1/auth/apple"])
        let post = try XCTUnwrap(transport.calls.last)
        XCTAssertTrue(post.body.contains("\"nonce\":\"raw-nonce\""))
        XCTAssertTrue(post.body.contains("identity_token"))
        XCTAssertEqual(session.token, "st_apple")
        XCTAssertEqual(session.account?.plan.state, PlanState.none)
        XCTAssertNil(session.household)
    }

    func testAppleErrorsKeepTheirCodes() async {
        let (api, _) = scripted(nil) { c in
            c.path.hasSuffix("/nonce") ? (201, "{\"nonce\":\"n\"}") : (401, "{\"title\":\"Apple sign-in failed\",\"status\":401,\"code\":\"apple_nonce_invalid\"}")
        }
        do {
            _ = try await AppleAuth(credentials: FakeApple()).signIn(api: api, deviceName: "iPhone")
            XCTFail("should throw")
        } catch let APIError.problem(p) {
            XCTAssertEqual(p.code, "apple_nonce_invalid")
        } catch APIError.unauthorized {
            // A 401 on the sign-in route is the same signal; the screen reads the code from the problem in the next step.
        } catch { XCTFail("\(error)") }
    }

    func testProviderFollowsTheServer() throws {
        let single = ServerInfo.legacy(version: nil, pairingEnabled: true)
        XCTAssertEqual(try single.authProvider(password: "x", apple: nil).method, .pairingPassword)
        var hosted = single
        hosted.mode = .hosted
        hosted.loginMethodsRaw = ["apple"]
        XCTAssertEqual(try hosted.authProvider(password: "", apple: FakeApple()).method, .apple)
        XCTAssertThrowsError(try hosted.authProvider(password: "", apple: nil))
    }
}

@MainActor
final class AccountModelTests: XCTestCase {
    private func make(_ handler: @escaping (ScriptedTransport.Call) throws -> (Int, String)) -> AccountModel {
        DiskStore.remove("server-info.json")
        DiskStore.remove("account.json")
        return AccountModel(api: scripted("t", handler).0)
    }

    private func accountJSON(_ state: String) -> String {
        let data = try! Coders.encoder().encode(MockHosted(arguments: ["-mock-mode", "hosted", "-mock-plan-state", state]).account())
        return String(data: data, encoding: .utf8)!
    }

    func testRefreshLoadsThePlan() async {
        let lapsed = accountJSON("lapsed_read_only")
        let model = make { c in c.path == "/v1/server-info" ? (200, hostedInfoJSON) : (200, lapsed) }
        await model.refresh()
        XCTAssertEqual(model.mode, .hosted)
        XCTAssertTrue(model.entitlements.isReadOnly)
    }

    func testOfflineKeepsTheSavedCopy() async {
        var online = true
        let lapsed = accountJSON("lapsed_read_only")
        let model = make { c in
            if !online { throw URLError(.notConnectedToInternet) }
            return c.path == "/v1/server-info" ? (200, hostedInfoJSON) : (200, lapsed)
        }
        await model.refresh()
        online = false
        await model.refresh()
        XCTAssertTrue(model.entitlements.isReadOnly, "no signal doesn't change what the person may do")
    }

    func testSingleHouseholdServerHasNoAccount() async {
        let model = make { c in c.path == "/v1/server-info" ? (200, singleInfoJSON) : (404, "{\"title\":\"Not found\",\"status\":404}") }
        await model.refresh()
        XCTAssertEqual(model.mode, .single)
        XCTAssertNil(model.account)
        XCTAssertTrue(model.entitlements.canWrite)
    }

    func testResetForgetsEverything() async {
        let active = accountJSON("active")
        let model = make { c in c.path == "/v1/server-info" ? (200, hostedInfoJSON) : (200, active) }
        await model.refresh()
        model.reset()
        XCTAssertNil(model.serverInfo)
        XCTAssertNil(model.account)
    }
}

@MainActor
final class MockHostedTests: XCTestCase {
    private func run(_ mock: MockHosted, _ method: String, _ path: String, _ body: String = "") -> (Int, String)? {
        let parts = path.split(separator: "/").map(String.init)
        let shape = parts.map { Int64($0) != nil ? ":id" : $0 }.joined(separator: "/")
        guard let (status, data) = mock.handle(method, parts, shape, Data(body.utf8), encoder: Coders.encoder(), decoder: Coders.decoder()) else { return nil }
        return (status, String(data: data, encoding: .utf8) ?? "")
    }

    func testSingleModeKnowsNoHostedRoutes() {
        let mock = MockHosted(arguments: [])
        XCTAssertEqual(run(mock, "GET", "/v1/account")?.0, 404)
        XCTAssertNil(run(mock, "POST", "/v1/shops"), "normal routes are left to the normal mock")
        XCTAssertEqual(run(mock, "GET", "/v1/server-info")?.0, 200)
    }

    func testWritesAreRefusedWhenReadOnlyButReadsWork() {
        let mock = MockHosted(arguments: ["-mock-mode", "hosted", "-mock-plan-state", "lapsed_read_only"])
        let refused = run(mock, "POST", "/v1/shops")
        XCTAssertEqual(refused?.0, 403)
        XCTAssertTrue(refused?.1.contains("read_only") == true)
        XCTAssertTrue(refused?.1.contains("delete_after") == true)
        XCTAssertNil(run(mock, "GET", "/v1/shops"))
        XCTAssertNil(run(mock, "POST", "/v1/glossary/preview"), "pure computation still works")
    }

    func testEverythingNeedsAPlanWhenThereIsNone() {
        let mock = MockHosted(arguments: ["-mock-mode", "hosted", "-mock-plan-state", "none"])
        XCTAssertEqual(run(mock, "GET", "/v1/shops")?.0, 403)
        XCTAssertTrue(run(mock, "GET", "/v1/shops")?.1.contains("plan_required") == true)
        XCTAssertEqual(run(mock, "GET", "/v1/account")?.0, 200, "account routes always work")
    }

    func testMagicInputs() {
        let mock = MockHosted(arguments: ["-mock-mode", "hosted"])
        func apple(_ token: String, nonce: String = "n") -> (Int, String)? {
            run(mock, "POST", "/v1/auth/apple", "{\"identity_token\":\"\(token)\",\"authorization_code\":\"c\",\"nonce\":\"\(nonce)\",\"device_name\":\"iPhone\"}")
        }
        XCTAssertEqual(apple("mock.valid.new")?.0, 201)
        XCTAssertEqual(mock.state, "none")
        XCTAssertEqual(apple("mock.valid.existing")?.0, 200)
        XCTAssertEqual(mock.state, "trial")
        XCTAssertEqual(apple("mock.valid.lapsed")?.0, 200)
        XCTAssertEqual(mock.state, "lapsed_read_only")
        XCTAssertEqual(apple("mock.invalid")?.0, 401)
        XCTAssertEqual(apple("mock.locked")?.0, 403)
        XCTAssertEqual(apple("mock.valid.existing", nonce: "mock-used-nonce")?.0, 401)
        func jws(_ v: String) -> Int? { run(mock, "POST", "/v1/billing/apple/transactions", "{\"signed_transaction\":\"\(v)\"}")?.0 }
        XCTAssertEqual(jws("mock.jws.invalid"), 400)
        XCTAssertEqual(jws("mock.jws.other-account"), 409)
        XCTAssertEqual(jws("mock.jws.apple-down"), 502)
        _ = run(mock, "POST", "/__mock/plan-state", "{\"state\":\"none\"}")
        XCTAssertEqual(jws("mock.jws.ok"), 200)
        XCTAssertEqual(mock.state, "trial", "a purchase starts the trial")
    }

    func testDeleteAccountNeedsTheConfirmationText() {
        let mock = MockHosted(arguments: ["-mock-mode", "hosted"])
        XCTAssertEqual(run(mock, "DELETE", "/v1/account", "{\"confirm\":\"nope\"}")?.0, 400)
        XCTAssertEqual(run(mock, "DELETE", "/v1/account", "{\"confirm\":\"delete my account\",\"delete_household\":true}")?.0, 202)
        XCTAssertEqual(mock.state, "pending_deletion_user")
    }

    func testServerInfoVariants() {
        XCTAssertEqual(MockHosted(arguments: ["-mock-mode", "hosted", "-mock-server-info", "old-level"]).serverInfo().apiLevel, 7)
        XCTAssertTrue(MockHosted(arguments: ["-mock-mode", "hosted", "-mock-server-info", "min-build-high"]).serverInfo().requiresUpdate(build: 1))
        XCTAssertNil(MockHosted(arguments: ["-mock-server-info", "nopassword"]).serverInfo().preferredLogin)
    }
}

/// The local StoreKit file the Debug scheme uses: one monthly subscription, 14 days free.
/// Checked as data (StoreKitTest cannot serve products to a unit-test host reliably); the purchase
/// itself is tried in the simulator through the scheme.
@MainActor
final class StoreKitConfigTests: XCTestCase {
    func testMonthlySubscriptionWithAFourteenDayTrial() throws {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("Config/Shop.storekit")
        let json = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any]
        let groups = try XCTUnwrap(json?["subscriptionGroups"] as? [[String: Any]])
        XCTAssertEqual(groups.count, 1)
        XCTAssertEqual(groups[0]["name"] as? String, "Shop Hosted")
        let subs = try XCTUnwrap(groups[0]["subscriptions"] as? [[String: Any]])
        XCTAssertEqual(subs.count, 1)
        let sub = subs[0]
        XCTAssertEqual(sub["productID"] as? String, "com.sundaystudi.shop.hosted.monthly")
        XCTAssertEqual(sub["displayPrice"] as? String, "4.99")
        XCTAssertEqual(sub["recurringSubscriptionPeriod"] as? String, "P1M")
        XCTAssertEqual(sub["familyShareable"] as? Bool, false)
        let intro = try XCTUnwrap(sub["introductoryOffer"] as? [String: Any])
        XCTAssertEqual(intro["paymentMode"] as? String, "free")
        XCTAssertEqual(intro["subscriptionPeriod"] as? String, "P2W")
    }

    func testProductIdsComeFromTheServerFirst() {
        var info = ServerInfo.legacy(version: nil, pairingEnabled: false)
        XCTAssertEqual(HostedConfig.productIDs(info), [HostedConfig.fallbackProductID])
        info.billing = BillingInfo(provider: "apple", productIds: ["a.b.c"], subscriptionGroup: nil, trialDays: 14, storefronts: nil, manageSubscriptionUrl: nil)
        XCTAssertEqual(HostedConfig.productIDs(info), ["a.b.c"])
    }

    func testTheBuildProductIdMatchesTheStoreKitFile() {
        XCTAssertEqual(HostedConfig.fallbackProductID, "com.sundaystudi.shop.hosted.monthly")
    }
}
