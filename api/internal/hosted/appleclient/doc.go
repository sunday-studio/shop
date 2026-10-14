// Package appleclient holds the real HTTP clients for Apple: the Sign in with Apple JWKS
// fetcher (appleauth.KeyFetcher), the token exchange and revocation
// (appleauth.CodeExchanger, appleauth.TokenRevoker) and the App Store Server API
// (billing.ServerAPI). Every base URL is injectable so tests talk to an httptest server;
// nothing here is ever pointed at Apple by a test.
//
// Secrets: private keys are loaded from PEM (a file or an environment variable), live only in
// unexported fields, are never logged and never appear in an error. Logs carry method, host,
// status and attempt number, not paths, headers or bodies.
package appleclient
