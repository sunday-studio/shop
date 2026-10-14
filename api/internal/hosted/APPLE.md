# Apple setup for the hosted mode: what the owner creates

Status marks: **[V]** verified on 2026-10-14 against Apple's own open-source server library
(apple/app-store-server-library-java and -python on GitHub) or Apple's certificate page;
**[M]** from memory or from the plan, not verifiable here (Apple's developer documentation
needs JavaScript, no login was available). Check every [M] item in the portal before relying on it.

## 1. Apple Developer portal (company account, Account Holder or Admin)

| # | Create | Notes | Env var |
|---|---|---|---|
| 1 | **Team ID** (Membership page) | 10 characters | `SHOP_APPLE_TEAM_ID` |
| 2 | **App ID** (Identifiers > App IDs) with bundle id, capability **Sign in with Apple** enabled (also In-App Purchase, on by default) | The bundle id is the `bid` of every App Store request and the `client_id`/`sub` of Sign in with Apple for the native app [M] | `SHOP_APPLE_BUNDLE_ID` |
| 3 | **Services ID** | Not needed: it is only for Sign in with Apple on the web or Android. The native app uses the bundle id as client id [M]. Create one only if a web login is added later | none |
| 4 | **Key** (Keys > +) with **Sign in with Apple** enabled, configured for the App ID from 2. Download the `.p8` once (Apple shows it only once) | Gives the key id. Used to sign the client secret for `POST /auth/token` and `/auth/revoke` [M] | `SHOP_APPLE_SIWA_KEY_ID`, key PEM in `SHOP_APPLE_SIWA_KEY` or file path in `SHOP_APPLE_SIWA_KEY_FILE` |

## 2. App Store Connect

| # | Create | Notes | Env var |
|---|---|---|---|
| 5 | **App** record with the same bundle id | Its numeric **Apple ID** (App Information) is `appAppleId`; Apple's library requires it for production verification [V] | `SHOP_APPLE_APP_ID` |
| 6 | **In-App Purchase key** (Users and Access > Integrations > In-App Purchase > Generate). Download the `.p8` once | Shows the **Issuer ID** at the top of that page and the **Key ID**. Signs the JWT for the App Store Server API (claims iss, bid, aud=`appstoreconnect-v1`, exp, alg ES256, header kid) [V] | `SHOP_APPLE_IAP_ISSUER_ID`, `SHOP_APPLE_IAP_KEY_ID`, `SHOP_APPLE_IAP_KEY` or `SHOP_APPLE_IAP_KEY_FILE` |
| 7 | **Subscription group** `Shop Hosted` and one **auto-renewable subscription**, 1 month | Product id is free text, e.g. `studio.sunday.shop.hosted.monthly`; the server only accepts ids on its allow-list | `SHOP_APPLE_PRODUCT_IDS` (comma separated) |
| 8 | **Price** 4.99 EUR | Set the base territory (Netherlands first), choose the 4.99 EUR price point; Apple derives the other storefronts [M]. The server does not read the price | none |
| 9 | **Introductory offer**: Free, 14 days, all new subscribers | One per Apple ID per group [M]. Shows up in transactions as `offerDiscountType = FREE_TRIAL` (the server reads this) | none |
| 10 | **Billing grace period** on, 16 days, all renewals | Plan section 4.1 | none |
| 11 | **App Store Server Notifications**: App Information > App Store Server Notifications: **Production Server URL** `https://<host>/hooks/apple/asn-v2` and **Sandbox Server URL** `https://<host>/hooks/apple/asn-v2` (same path is fine, or a separate sandbox path), **Version 2** | TLS 1.2 or newer on a publicly trusted certificate [M]. Then use "Request a Test Notification" (or `StoreKit.RequestTestNotification`) for each environment and confirm a `TEST` notification arrives | none |
| 12 | **Sandbox testers** (Users and Access > Sandbox) | For TestFlight and App Review purchases | none |

**App-specific shared secret**: not needed. It belongs to the old receipt validation
(`verifyReceipt`); this server uses StoreKit 2 signed transactions and the App Store Server API only [M].

## 3. Other server settings

| Value | Env var |
|---|---|
| `auto` (production first, sandbox on "transaction not found", Apple's rule), `production` or `sandbox` | `SHOP_APPLE_ENVIRONMENT` (default `auto`) |
| Overrides for the API hosts (tests only) | none; `appleclient.StoreKitConfig.ProductionURL/SandboxURL` |

Env var names are a proposal for the wiring code; the loaders are
`appleclient.LoadKeyFromEnv("SHOP_APPLE_SIWA_KEY")` (reads `$NAME`, with `\n` escapes allowed, or
the file named by `$NAME_FILE`) and the same for `SHOP_APPLE_IAP_KEY`. Keys are PKCS#8 `.p8`
files with a P-256 key; nothing else is accepted.

## 4. What is pinned and checked in the code

* Root: **Apple Root CA - G3**, `applepki/AppleRootCA-G3.cer`, SHA-256
  `63:34:3A:BF:B8:9A:6A:03:EB:B5:7E:9B:3F:5F:A7:BE:7C:4F:5C:75:6F:30:17:B3:A8:C4:88:C3:65:3E:91:79`,
  valid until 2039-04-30. Downloaded from `https://www.apple.com/certificateauthority/AppleRootCA-G3.cer`
  and compared with the copy in macOS's system trust store [V]. **Apple's page does not print a
  fingerprint**, so there is no second source from Apple's website itself. Apple's Java library
  takes the roots from the caller and pins nothing; the Python library repo only holds a test CA.
* Chain rules [V, ChainVerifier.java and AppleExtensionCertPathChecker.java]: exactly three
  certificates (leaf, WWDR intermediate, root), the leaf carries the extension
  `1.2.840.113635.100.6.11.1`, the intermediate `1.2.840.113635.100.6.2.1`, the chain is valid at
  the payload's `signedDate`, the JWS algorithm is ES256. Revocation (OCSP) is **off** in Apple's
  library unless "online checks" are enabled; it is off here too.
* **Not verified**: whether Apple will move to a new intermediate or a different root (G3
  intermediates "Worldwide Developer Relations - G3" expire 2030 per Apple's page). If real
  notifications are rejected with `chain`, check the x5c of a real notification first.

## 5. Not verified against Apple's current documentation

* Token endpoint and revoke endpoint request shapes (form fields `client_id`, `client_secret`,
  `code`, `grant_type`, `token`, `token_type_hint`), the claims of the client secret (`iss`, `iat`,
  `exp` at most 6 months, `aud` `https://appleid.apple.com`, `sub`), error codes `invalid_grant`,
  `invalid_client`: [M]. The App Store Server API paths, JWT claims, sandbox host and error code
  `4040010` are [V] from the Python library; the production host `api.storekit.apple.com` is the
  one Apple's current library uses (the older `api.storekit.itunes.apple.com` still exists).
* That a successful revoke answers HTTP 200 with an empty body, and that revoking an already
  revoked token answers `400 invalid_grant` (treated as success): [M].
* Rate limits of the App Store Server API (answer 429, code 4290000, `Retry-After`) are retried with backoff [V for the code].
* Price tier names and the exact menu names above change with App Store Connect releases.
