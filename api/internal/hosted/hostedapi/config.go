// Package hostedapi is the HTTP layer of the hosted (multi-household, paid) mode. It implements
// notes/hosted-api-contract.md on top of the packages in internal/hosted (control, billing,
// appleauth, limits, grants, lifecycle, admin) and the tenant seam (tenancy, tenantapp).
//
// Request path of an authenticated household route (the existing API: shops, receipts, ...):
//
//	recover/log -> token (401 token_invalid, 403 account_locked) -> plan gate (403 plan_required /
//	read_only) -> rate limit per token (429 rate_limited) -> requests in flight per household ->
//	idempotency key (24 h replay) -> upload guard (413 file_too_large, 429 limit_reached) ->
//	household Server (tenancy gateway; the household sees the request as already authenticated)
//
// Account, household and billing routes use the same first steps and do their own business rules.
package hostedapi

import (
	"time"

	"shop/internal/hosted/limits"
)

// Config holds what the API shows and enforces that is not a limit (limits are limits.Limits).
type Config struct {
	Environment       string // "sandbox" or "production" (informational; plan.environment comes from Apple data)
	ProductIDs        []string
	SubscriptionGroup string
	TrialDays         int
	Storefronts       []string
	ManageURL         string

	TermsURL, PrivacyURL, SupportURL, SupportEmail string
	// InviteBaseURL is the universal link host path, e.g. https://shop.example.com/join .
	InviteBaseURL string

	MinAppBuild int

	// ExportDir holds finished export archives: ExportDir/<household>/<job>.zip.
	ExportDir      string
	ExportTTL      time.Duration // default 7 days
	IdempotencyTTL time.Duration // default 24 hours

	// TrustProxy reads the client address from the last X-Forwarded-For entry (set by the reverse
	// proxy in front, Caddy in the hosted kit). Off: the socket address.
	TrustProxy bool

	Rates Rates
	// Limits are the launch defaults shown in server-info and enforced per household.
	Limits limits.Limits
}

// Rates are the rate limits of the contract, section 8.1 (starting values).
type Rates struct {
	PublicInfoPerMin     int // server-info, plan-states, per IP
	NoncePerMin          int // per IP
	SignInPerMin         int // per IP
	SignInPerHour        int // per IP, failures count double
	AcceptPerHour        int // per account and per IP (also the invite preview, per IP)
	TransactionsPerHr    int // per account
	RefreshPerMin        int // per account
	WebhookPerMin        int // per IP
	TokenPerMin          int // all other authenticated routes, per token
	UploadsPerMin        int // per household
	InFlightPerHousehold int
	UploadsInFlight      int // receipt uploads being read at once in the whole process (each holds up to 20 MB in memory)
	// Disabled turns every rate limit off (never in production).
	Disabled bool
}

// DefaultRates returns the contract values.
func DefaultRates() Rates {
	return Rates{PublicInfoPerMin: 60, NoncePerMin: 20, SignInPerMin: 10, SignInPerHour: 30, AcceptPerHour: 10,
		TransactionsPerHr: 30, RefreshPerMin: 1, WebhookPerMin: 600, TokenPerMin: 300, UploadsPerMin: 6, InFlightPerHousehold: 16, UploadsInFlight: 6}
}

// DefaultConfig returns the launch values with placeholder links.
func DefaultConfig() Config {
	return Config{
		Environment:       "sandbox",
		ProductIDs:        []string{"com.sundaystudi.shop.hosted.monthly"},
		SubscriptionGroup: "Shop Hosted",
		TrialDays:         14,
		Storefronts:       []string{"NLD"},
		ManageURL:         "https://apps.apple.com/account/subscriptions",
		TermsURL:          "https://shop.example.com/terms",
		PrivacyURL:        "https://shop.example.com/privacy",
		SupportURL:        "https://shop.example.com/support",
		SupportEmail:      "support@shop.example.com",
		InviteBaseURL:     "https://shop.example.com/join",
		MinAppBuild:       1,
		ExportTTL:         7 * 24 * time.Hour,
		IdempotencyTTL:    24 * time.Hour,
		Rates:             DefaultRates(),
		Limits:            limits.Defaults(),
	}
}
