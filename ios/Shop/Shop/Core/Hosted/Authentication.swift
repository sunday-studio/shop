import CryptoKit
import Foundation

// One seam for "how does this person prove who they are": the pairing password of a self-hosted
// server, or Sign in with Apple on the hosted service. Screens ask the server which method it
// wants (ServerInfo.preferredLogin) and run the matching provider; the rest of the app only ever
// sees a token (stored exactly as today) and, for hosted, the account state that came with it.

nonisolated struct AuthSession: Sendable, Equatable {
    var token: String
    var account: Account?
    var household: Household?
}

enum AuthError: Error, Equatable {
    case cancelled
    case wrongPassword
    case unsupported(String)
    case failed(String)
}

@MainActor
protocol AuthProvider {
    var method: LoginMethod { get }
    func signIn(api: APIClient, deviceName: String) async throws -> AuthSession
}

/// Door 2: the pairing password (today's flow).
@MainActor
struct PairingPasswordAuth: AuthProvider {
    let password: String
    var method: LoginMethod { .pairingPassword }

    func signIn(api: APIClient, deviceName: String) async throws -> AuthSession {
        do {
            return AuthSession(token: try await api.pair(password: password, deviceName: deviceName), account: nil, household: nil)
        } catch APIError.unauthorized {
            throw AuthError.wrongPassword
        }
    }
}

/// What Apple gives back from the system sheet.
nonisolated struct AppleCredential: Sendable, Equatable {
    var identityToken: String
    var authorizationCode: String
}

/// Presents the Sign in with Apple sheet. The nonce is the server's raw nonce; the request carries
/// its SHA-256 so the identity token can only be used once, for this sign-in.
@MainActor
protocol AppleCredentialProvider {
    func credential(hashedNonce: String) async throws -> AppleCredential
}

/// Door 1: Sign in with Apple.
@MainActor
struct AppleAuth: AuthProvider {
    let credentials: AppleCredentialProvider
    var method: LoginMethod { .apple }

    func signIn(api: APIClient, deviceName: String) async throws -> AuthSession {
        let nonce = try await api.appleNonce()
        let credential = try await credentials.credential(hashedNonce: Self.sha256(nonce))
        let response = try await api.signInWithApple(AppleSignInBody(identityToken: credential.identityToken, authorizationCode: credential.authorizationCode,
                                                                    nonce: nonce, deviceName: deviceName))
        return AuthSession(token: response.token, account: response.account, household: response.household)
    }

    nonisolated static func sha256(_ text: String) -> String {
        SHA256.hash(data: Data(text.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

extension ServerInfo {
    /// The provider for what this server wants. Apple needs the system sheet, which the caller provides.
    @MainActor
    func authProvider(password: String, apple: AppleCredentialProvider?) throws -> AuthProvider {
        switch preferredLogin {
        case .pairingPassword?: return PairingPasswordAuth(password: password)
        case .apple?:
            guard let apple else { throw AuthError.unsupported("apple") }
            return AppleAuth(credentials: apple)
        case .other(let raw)?: throw AuthError.unsupported(raw)
        case nil: throw AuthError.unsupported("none")
        }
    }
}

nonisolated struct AppleNonce: Codable, Sendable {
    var nonce: String
    var expiresAt: Date?
}

nonisolated struct AppleSignInBody: Codable, Sendable, Equatable {
    var identityToken: String
    var authorizationCode: String
    var nonce: String
    var deviceName: String
}

nonisolated struct AppleSignInResponse: Codable, Sendable {
    var created: Bool?
    var token: String
    var device: Device?
    var account: Account?
    var household: Household?
}

nonisolated struct SignedTransactionBody: Codable, Sendable { var signedTransaction: String }

nonisolated struct BillingResult: Codable, Sendable {
    /// "bound", "already_bound" or "claimed".
    var result: String
    var account: Account
    var household: Household?
}

nonisolated struct BillingRefresh: Codable, Sendable { var account: Account }
