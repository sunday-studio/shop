import AuthenticationServices
import UIKit

/// The real Sign in with Apple sheet. Requests the email scope only (never the name).
/// Needs the Sign in with Apple capability on a real device; see Config/Shop.entitlements.
@MainActor
final class SystemAppleCredentialProvider: NSObject, AppleCredentialProvider, ASAuthorizationControllerDelegate, ASAuthorizationControllerPresentationContextProviding {
    private var continuation: CheckedContinuation<AppleCredential, Error>?

    func credential(hashedNonce: String) async throws -> AppleCredential {
        try await withCheckedThrowingContinuation { continuation in
            self.continuation = continuation
            let request = ASAuthorizationAppleIDProvider().createRequest()
            request.requestedScopes = [.email]
            request.nonce = hashedNonce
            let controller = ASAuthorizationController(authorizationRequests: [request])
            controller.delegate = self
            controller.presentationContextProvider = self
            controller.performRequests()
        }
    }

    func authorizationController(controller: ASAuthorizationController, didCompleteWithAuthorization authorization: ASAuthorization) {
        guard let apple = authorization.credential as? ASAuthorizationAppleIDCredential,
              let tokenData = apple.identityToken, let token = String(data: tokenData, encoding: .utf8),
              let codeData = apple.authorizationCode, let code = String(data: codeData, encoding: .utf8) else {
            continuation?.resume(throwing: AuthError.failed("Apple didn't send a sign-in token."))
            continuation = nil
            return
        }
        continuation?.resume(returning: AppleCredential(identityToken: token, authorizationCode: code))
        continuation = nil
    }

    func authorizationController(controller: ASAuthorizationController, didCompleteWithError error: Error) {
        let cancelled = (error as? ASAuthorizationError)?.code == .canceled
        continuation?.resume(throwing: cancelled ? AuthError.cancelled : AuthError.failed(error.localizedDescription))
        continuation = nil
    }

    func presentationAnchor(for controller: ASAuthorizationController) -> ASPresentationAnchor {
        UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.flatMap(\.windows).first { $0.isKeyWindow } ?? ASPresentationAnchor()
    }

    /// Whether the Apple ID behind this user id still allows the app (call on launch; sign out when revoked).
    static func isStillAuthorized(userID: String) async -> Bool {
        await withCheckedContinuation { continuation in
            ASAuthorizationAppleIDProvider().getCredentialState(forUserID: userID) { state, _ in
                continuation.resume(returning: state == .authorized || state == .transferred)
            }
        }
    }
}
