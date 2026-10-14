package main

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"shop/internal/config"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/hostedapi"
	"shop/internal/hosted/limits"
)

// hostedConfig is everything the hosted binary reads from the environment.
//
//	SHOP_MODE                    must be "hosted" (or unset)
//	SHOP_ADDR                    public listener (default :8080)
//	ADMIN_ADDR                   admin listener, loopback only (default 127.0.0.1:8081)
//	ADMIN_TOKEN_HASH             sha256 hex of the admin token (shop-hosted admin token create)
//	SHOP_DATA_DIR                households live in <dir>/households/<id>/
//	SHOP_CONTROL_DB              control database (default <data dir>/control.db)
//	SHOP_EXPORT_DIR              finished exports (default <data dir>/exports)
//	BACKUP_DIR                   per-household backups (default <data dir>/backup)
//	SHOP_SEALER_KEY              32 bytes, base64 or hex: encrypts stored Apple refresh tokens
//	SHOP_TRUST_PROXY=1           take the client address from X-Forwarded-For (behind Caddy)
//	SHOP_SCHEDULER_INTERVAL      lifecycle/cleanup interval (default 15m)
//	SHOP_LIFECYCLE_KILL_SWITCH=1 no tombstoning or erasure happens
//	SHOP_TERMS_URL SHOP_PRIVACY_URL SHOP_SUPPORT_URL SHOP_SUPPORT_EMAIL SHOP_INVITE_BASE_URL
//	SHOP_APPLE_*                 see appleConfig
//	SHOP_REMINDER_WEBHOOK_URL / _SECRET   POST due reminders as JSON
//	SHOP_SMTP_ADDR SHOP_SMTP_FROM SHOP_SMTP_USER SHOP_SMTP_PASS   mail due reminders
type hostedConfig struct {
	App          config.Config
	Addr         string
	AdminAddr    string
	AdminHash    string
	ControlDB    string
	ExportDir    string
	BackupDir    string
	SealerKey    []byte
	TrustProxy   bool
	Interval     time.Duration
	KillSwitch   bool
	API          hostedapi.Config
	Apple        appleConfig
	WebhookURL   string
	WebhookToken string
	SMTPAddr     string
	SMTPFrom     string
	SMTPUser     string
	SMTPPass     string
	// AllowPublicAdmin lets ADMIN_ADDR be a non-loopback address (SHOP_ADMIN_ALLOW_NONLOOPBACK=1).
	AllowPublicAdmin bool
}

type appleConfig struct {
	Mode        string // live or fake
	Environment string // auto, production or sandbox (live); fake is always sandbox
	BundleID    string
	TeamID      string
	ProductIDs  []string
	AppAppleID  string
	SIWAKeyID   string
	IAPKeyID    string
	IAPIssuer   string
	// Base URLs (tests and the e2e point these at a fake; empty = Apple).
	JWKSURL, SIWAURL, StoreKitURL, StoreKitSandboxURL string
	// RootCAFile replaces the pinned Apple root (a test chain). Refused in the production environment.
	RootCAFile string
	// AcceptSandbox (SHOP_APPLE_ACCEPT_SANDBOX, default true): a production server also accepts
	// sandbox-signed transactions and notifications (App Review and TestFlight buy in the sandbox
	// against the production backend). They are flagged environment=sandbox, capped in lifetime
	// (SandboxMax), never counted as revenue, and lose against a production entitlement.
	AcceptSandbox bool
	// SandboxMax (SHOP_APPLE_SANDBOX_MAX_DAYS, default 30, 1 to 366) is that cap; 0 = none (fake mode only).
	SandboxMax time.Duration
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// isFalse is true for an explicit "no" (so that an unset or garbled value keeps a default of true).
func isFalse(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func loadConfig() (hostedConfig, error) {
	var c hostedConfig
	if m := env("SHOP_MODE", "hosted"); m != "hosted" {
		return c, fmt.Errorf("SHOP_MODE=%q: shop-hosted only runs the hosted mode (use the shop binary for single mode)", m)
	}
	app, err := config.Load()
	if err != nil {
		return c, err
	}
	c.App = app
	c.Addr = app.Addr
	c.AdminAddr = env("ADMIN_ADDR", "127.0.0.1:8081")
	c.AdminHash = strings.ToLower(env("ADMIN_TOKEN_HASH", ""))
	c.AllowPublicAdmin = truthy(env("SHOP_ADMIN_ALLOW_NONLOOPBACK", ""))
	if !c.AllowPublicAdmin && !loopbackAddr(c.AdminAddr) {
		return c, fmt.Errorf("ADMIN_ADDR=%q is not a loopback address; the admin API must not be reachable from outside (SHOP_ADMIN_ALLOW_NONLOOPBACK=1 overrides, not recommended)", c.AdminAddr)
	}
	c.ControlDB = env("SHOP_CONTROL_DB", filepath.Join(app.DataDir, "control.db"))
	c.ExportDir = env("SHOP_EXPORT_DIR", filepath.Join(app.DataDir, "exports"))
	c.BackupDir = app.BackupDir
	if os.Getenv("BACKUP_DIR") == "" {
		c.BackupDir = filepath.Join(app.DataDir, "backup")
	}
	c.TrustProxy = truthy(env("SHOP_TRUST_PROXY", ""))
	c.Interval, err = time.ParseDuration(env("SHOP_SCHEDULER_INTERVAL", "15m"))
	if err != nil || c.Interval < time.Second {
		return c, errors.New("SHOP_SCHEDULER_INTERVAL must be a duration of at least 1s")
	}
	c.KillSwitch = truthy(env("SHOP_LIFECYCLE_KILL_SWITCH", ""))
	if k := env("SHOP_SEALER_KEY", ""); k != "" {
		if c.SealerKey, err = decodeKey(k); err != nil {
			return c, fmt.Errorf("SHOP_SEALER_KEY: %w", err)
		}
	}

	a := hostedapi.DefaultConfig()
	a.ExportDir = c.ExportDir
	a.TrustProxy = c.TrustProxy
	a.TermsURL = env("SHOP_TERMS_URL", a.TermsURL)
	a.PrivacyURL = env("SHOP_PRIVACY_URL", a.PrivacyURL)
	a.SupportURL = env("SHOP_SUPPORT_URL", a.SupportURL)
	a.SupportEmail = env("SHOP_SUPPORT_EMAIL", a.SupportEmail)
	a.InviteBaseURL = env("SHOP_INVITE_BASE_URL", a.InviteBaseURL)
	a.ManageURL = env("SHOP_MANAGE_SUBSCRIPTION_URL", a.ManageURL)
	a.SubscriptionGroup = env("SHOP_SUBSCRIPTION_GROUP", a.SubscriptionGroup)
	if v := env("SHOP_MIN_APP_BUILD", ""); v != "" {
		if a.MinAppBuild, err = strconv.Atoi(v); err != nil || a.MinAppBuild < 1 {
			return c, errors.New("SHOP_MIN_APP_BUILD must be a positive integer")
		}
	}
	if v := env("SHOP_STOREFRONTS", ""); v != "" {
		a.Storefronts = splitList(v)
	}
	if a.Limits, err = limitsFromEnv(a.Limits); err != nil {
		return c, err
	}
	c.API = a

	ap := appleConfig{
		Mode:               strings.ToLower(env("SHOP_APPLE_MODE", "live")),
		Environment:        strings.ToLower(env("SHOP_APPLE_ENVIRONMENT", "auto")),
		BundleID:           env("SHOP_APPLE_BUNDLE_ID", "com.sundaystudi.Shop"),
		TeamID:             env("SHOP_APPLE_TEAM_ID", ""),
		AppAppleID:         env("SHOP_APPLE_APP_ID", ""),
		SIWAKeyID:          env("SHOP_APPLE_SIWA_KEY_ID", ""),
		IAPKeyID:           env("SHOP_APPLE_IAP_KEY_ID", ""),
		IAPIssuer:          env("SHOP_APPLE_IAP_ISSUER_ID", ""),
		JWKSURL:            env("SHOP_APPLE_JWKS_URL", ""),
		SIWAURL:            env("SHOP_APPLE_SIWA_URL", ""),
		StoreKitURL:        env("SHOP_APPLE_STOREKIT_URL", ""),
		StoreKitSandboxURL: env("SHOP_APPLE_STOREKIT_SANDBOX_URL", ""),
		RootCAFile:         env("SHOP_APPLE_ROOT_CA_FILE", ""),
	}
	ap.AcceptSandbox = !isFalse(env("SHOP_APPLE_ACCEPT_SANDBOX", "true"))
	ap.SandboxMax = billing.DefaultSandboxMax
	if ap.Mode == "fake" {
		ap.SandboxMax = 0 // the simulated Apple signs everything as Sandbox and the e2e moves time by months
	}
	if v := env("SHOP_APPLE_SANDBOX_MAX_DAYS", ""); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 366 {
			return c, errors.New("SHOP_APPLE_SANDBOX_MAX_DAYS must be a whole number of days from 1 to 366")
		}
		ap.SandboxMax = time.Duration(n) * 24 * time.Hour
	}
	ap.ProductIDs = splitList(env("SHOP_APPLE_PRODUCT_IDS", strings.Join(a.ProductIDs, ",")))
	switch ap.Mode {
	case "live", "fake":
	default:
		return c, errors.New("SHOP_APPLE_MODE must be live or fake")
	}
	switch ap.Environment {
	case "auto", "production", "sandbox":
	default:
		return c, errors.New("SHOP_APPLE_ENVIRONMENT must be auto, production or sandbox")
	}
	if ap.Mode == "fake" {
		if err := checkFakeAllowed(ap); err != nil {
			return c, err
		}
	}
	if ap.RootCAFile != "" && ap.Environment == "production" {
		return c, errors.New("SHOP_APPLE_ROOT_CA_FILE cannot be combined with SHOP_APPLE_ENVIRONMENT=production")
	}
	if ap.Environment == "production" {
		// Where the keys, the sign-in tokens and the money come from must not be redirectable by an
		// environment variable on the production server (a stray override would make us trust a
		// stranger's key set or send our signed requests to a stranger).
		for name, v := range map[string]string{"SHOP_APPLE_JWKS_URL": ap.JWKSURL, "SHOP_APPLE_SIWA_URL": ap.SIWAURL,
			"SHOP_APPLE_STOREKIT_URL": ap.StoreKitURL, "SHOP_APPLE_STOREKIT_SANDBOX_URL": ap.StoreKitSandboxURL} {
			if v != "" {
				return c, fmt.Errorf("%s cannot be combined with SHOP_APPLE_ENVIRONMENT=production", name)
			}
		}
	}
	c.API.ProductIDs = ap.ProductIDs
	switch ap.Environment {
	case "production":
		c.API.Environment = "production"
	default:
		c.API.Environment = "sandbox"
	}
	c.Apple = ap

	c.WebhookURL, c.WebhookToken = env("SHOP_REMINDER_WEBHOOK_URL", ""), env("SHOP_REMINDER_WEBHOOK_SECRET", "")
	c.SMTPAddr, c.SMTPFrom = env("SHOP_SMTP_ADDR", ""), env("SHOP_SMTP_FROM", "")
	c.SMTPUser, c.SMTPPass = env("SHOP_SMTP_USER", ""), env("SHOP_SMTP_PASS", "")
	if c.SMTPAddr != "" && c.SMTPFrom == "" {
		return c, errors.New("SHOP_SMTP_ADDR needs SHOP_SMTP_FROM")
	}
	return c, nil
}

// fakeAck is the second switch that turns on the simulated Apple. A single variable (a copied
// .env, a stray export) must not be enough to run a server whose "Apple" says yes to everything.
const fakeAck = "i-know-this-is-a-test"

// checkFakeAllowed refuses SHOP_APPLE_MODE=fake unless SHOP_APPLE_FAKE_ACK=i-know-this-is-a-test is
// set too, and never when a production indicator is present: the production environment or real
// Apple credentials (sign-in or in-app purchase keys, key ids, issuer id, app id).
func checkFakeAllowed(ap appleConfig) error {
	if ap.Environment == "production" {
		return errors.New("SHOP_APPLE_MODE=fake cannot be combined with SHOP_APPLE_ENVIRONMENT=production")
	}
	for _, name := range []string{"SHOP_APPLE_SIWA_KEY", "SHOP_APPLE_SIWA_KEY_FILE", "SHOP_APPLE_IAP_KEY", "SHOP_APPLE_IAP_KEY_FILE",
		"SHOP_APPLE_SIWA_KEY_ID", "SHOP_APPLE_IAP_KEY_ID", "SHOP_APPLE_IAP_ISSUER_ID", "SHOP_APPLE_APP_ID", "SHOP_APPLE_TEAM_ID"} {
		if os.Getenv(name) != "" {
			return fmt.Errorf("SHOP_APPLE_MODE=fake cannot be combined with %s (real Apple credentials are configured)", name)
		}
	}
	if os.Getenv("SHOP_APPLE_FAKE_ACK") != fakeAck {
		return errors.New("SHOP_APPLE_MODE=fake also needs SHOP_APPLE_FAKE_ACK=" + fakeAck + " (it simulates Apple and is for tests and local stacks only)")
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("must be 32 bytes as 64 hex characters or base64")
}

func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// limitsFromEnv lets the operator tune the launch limits: SHOP_LIMIT_RECEIPTS_PER_MONTH=500 etc.
func limitsFromEnv(l limits.Limits) (limits.Limits, error) {
	set := func(name string, dst *int64) error {
		v := env("SHOP_LIMIT_"+name, "")
		if v == "" {
			return nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("SHOP_LIMIT_%s must be a non-negative integer (0 = unlimited)", name)
		}
		*dst = n
		return nil
	}
	for name, dst := range map[string]*int64{
		"HOUSEHOLD_MEMBERS": &l.HouseholdMembers, "MAX_UPLOAD_BYTES": &l.MaxUploadBytes, "RECEIPTS_PER_MONTH": &l.ReceiptsPerMonth,
		"RECEIPTS_PER_MONTH_SOFT": &l.ReceiptsPerMonthSoft, "RECEIPTS_PER_DAY": &l.ReceiptsPerDay, "STORAGE_BYTES": &l.StorageBytes,
		"MAX_QUEUED_JOBS": &l.MaxQueuedJobs, "EXPORTS_PER_DAY": &l.ExportsPerDay, "OPEN_INVITES": &l.OpenInvites,
	} {
		if err := set(name, dst); err != nil {
			return l, err
		}
	}
	return l, nil
}
