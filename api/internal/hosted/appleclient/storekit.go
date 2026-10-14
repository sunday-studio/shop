package appleclient

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/hclock"
)

// Hosts of the App Store Server API (the host Apple's current server libraries use; the older
// api.storekit.itunes.apple.com host is still served by Apple and can be injected).
const (
	StoreKitProductionURL = "https://api.storekit.apple.com"
	StoreKitSandboxURL    = "https://api.storekit-sandbox.apple.com"
)

// Apple error code for "transaction id not found" (also what production answers for a
// sandbox transaction).
const codeTransactionNotFound = 4040010

// Mode selects the environment.
type Mode int

const (
	// Auto asks production first and falls back to sandbox on "transaction not found"
	// (Apple's documented rule: App Review and TestFlight purchases are sandbox).
	Auto Mode = iota
	Production
	Sandbox
)

// SignedDecoder verifies a signed value and unmarshals its payload; *billing.Verifier is one.
type SignedDecoder interface {
	Verify(token string, out any) error
}

// StoreKitConfig configures the App Store Server API client.
type StoreKitConfig struct {
	IssuerID string // App Store Connect issuer id (iss)
	KeyID    string // in-app purchase key id (kid)
	BundleID string // bid claim, and every decoded value must match it
	Key      *ecdsa.PrivateKey
	Mode     Mode
	// ProductionURL and SandboxURL default to the Apple hosts.
	ProductionURL, SandboxURL string
	// Decoder verifies every signed value Apple returns (required: nothing unsigned is trusted).
	Decoder SignedDecoder
	Clock   hclock.Clock
	// TokenTTL is the lifetime of the request JWT (default 5 minutes, Apple allows 60).
	TokenTTL time.Duration
	// MaxHistoryPages bounds pagination (default 20 pages of up to 20 transactions).
	MaxHistoryPages int
	Options
}

// StoreKit implements billing.ServerAPI against the App Store Server API.
type StoreKit struct {
	cfg StoreKitConfig
	o   Options
}

var _ billing.ServerAPI = (*StoreKit)(nil)

// NewStoreKit validates the configuration.
func NewStoreKit(cfg StoreKitConfig) (*StoreKit, error) {
	if cfg.IssuerID == "" || cfg.KeyID == "" || cfg.BundleID == "" || cfg.Key == nil || cfg.Decoder == nil {
		return nil, errors.New("appleclient: StoreKit needs issuer id, key id, bundle id, key and a decoder")
	}
	if cfg.ProductionURL == "" {
		cfg.ProductionURL = StoreKitProductionURL
	}
	if cfg.SandboxURL == "" {
		cfg.SandboxURL = StoreKitSandboxURL
	}
	if cfg.Clock == nil {
		cfg.Clock = hclock.System{}
	}
	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = 5 * time.Minute
	}
	if cfg.MaxHistoryPages <= 0 {
		cfg.MaxHistoryPages = 20
	}
	return &StoreKit{cfg: cfg, o: cfg.Options.withDefaults()}, nil
}

// APIError is Apple's error answer (errorCode and errorMessage are Apple's own words).
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("app store server api: HTTP %d code %d: %s", e.Status, e.Code, e.Message)
}

// Is maps Apple's answers onto the billing sentinels: not found, and everything that is
// retryable or our own credentials' problem (401, 403, 429, 5xx) as unavailable.
func (e *APIError) Is(target error) bool {
	switch target {
	case billing.ErrAppleNotFound:
		return e.Code == codeTransactionNotFound
	case billing.ErrAppleUnavailable:
		return e.Status == 401 || e.Status == 403 || e.Status == 429 || e.Status >= 500
	}
	return false
}

func (s *StoreKit) token() (string, error) {
	now := s.cfg.Clock.Now()
	return signES256(s.cfg.Key,
		map[string]any{"kid": s.cfg.KeyID},
		map[string]any{
			"iss": s.cfg.IssuerID,
			"iat": now.Unix(),
			"exp": now.Add(s.cfg.TokenTTL).Unix(),
			"aud": "appstoreconnect-v1",
			"bid": s.cfg.BundleID,
		})
}

// call performs one request against one base URL and decodes a 2xx JSON body into out.
func (s *StoreKit) call(ctx context.Context, base, method, path string, query url.Values, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	u := strings.TrimRight(base, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	resp, err := send(ctx, s.o, true, func() (*http.Request, error) {
		tok, err := s.token()
		if err != nil {
			return nil, errors.New("appleclient: cannot sign the request token")
		}
		var r *http.Request
		if payload != nil {
			r, err = http.NewRequest(method, u, jsonBody(payload))
		} else {
			r, err = http.NewRequest(method, u, nil)
		}
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Accept", "application/json")
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		return r, nil
	})
	if err != nil {
		if errors.Is(err, errTransport) {
			return fmt.Errorf("%w: %v", billing.ErrAppleUnavailable, err)
		}
		return err
	}
	if resp.Status < 200 || resp.Status > 299 {
		ae := &APIError{Status: resp.Status}
		var eb struct {
			ErrorCode    int    `json:"errorCode"`
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal(resp.Body, &eb) == nil {
			ae.Code = eb.ErrorCode
			ae.Message = truncate(eb.ErrorMessage, 200)
		}
		return ae
	}
	if out == nil || len(resp.Body) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Body, out); err != nil {
		return fmt.Errorf("%w: unreadable answer", billing.ErrAppleUnavailable)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// bases lists the environments to try, in order.
func (s *StoreKit) bases() []envBase {
	switch s.cfg.Mode {
	case Production:
		return []envBase{{s.cfg.ProductionURL, billing.EnvProduction}}
	case Sandbox:
		return []envBase{{s.cfg.SandboxURL, billing.EnvSandbox}}
	}
	return []envBase{{s.cfg.ProductionURL, billing.EnvProduction}, {s.cfg.SandboxURL, billing.EnvSandbox}}
}

type envBase struct{ url, env string }

// try runs fn against each environment: the next one is tried only when the previous answered
// "transaction not found".
func (s *StoreKit) try(fn func(base envBase) error) error {
	var err error
	for _, b := range s.bases() {
		err = fn(b)
		if err == nil || !isNotFound(err) {
			return err
		}
	}
	return billing.ErrAppleNotFound
}

func isNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == codeTransactionNotFound
}

// check enforces bundle id and environment on a decoded transaction.
func (s *StoreKit) checkTx(tx billing.TransactionInfo, env string) error {
	if tx.BundleID != s.cfg.BundleID {
		return fmt.Errorf("%w: transaction belongs to another app", billing.ErrAppleUnavailable)
	}
	if tx.Environment != "" && tx.Environment != env {
		return fmt.Errorf("%w: transaction environment %q, asked %q", billing.ErrAppleUnavailable, tx.Environment, env)
	}
	return nil
}

type statusResponse struct {
	Environment string `json:"environment"`
	BundleID    string `json:"bundleId"`
	Data        []struct {
		LastTransactions []struct {
			Status                int    `json:"status"`
			OriginalTransactionID string `json:"originalTransactionId"`
			SignedTransactionInfo string `json:"signedTransactionInfo"`
			SignedRenewalInfo     string `json:"signedRenewalInfo"`
		} `json:"lastTransactions"`
	} `json:"data"`
}

// GetSubscriptionStatus implements billing.ServerAPI (Get All Subscription Statuses).
func (s *StoreKit) GetSubscriptionStatus(ctx context.Context, originalTransactionID string) (billing.SubscriptionStatus, error) {
	var result billing.SubscriptionStatus
	err := s.try(func(b envBase) error {
		var resp statusResponse
		if err := s.call(ctx, b.url, http.MethodGet, "/inApps/v1/subscriptions/"+url.PathEscape(originalTransactionID), nil, nil, &resp); err != nil {
			return err
		}
		if resp.BundleID != "" && resp.BundleID != s.cfg.BundleID {
			return fmt.Errorf("%w: answer belongs to another app", billing.ErrAppleUnavailable)
		}
		for _, g := range resp.Data {
			for _, lt := range g.LastTransactions {
				if lt.OriginalTransactionID != originalTransactionID {
					continue
				}
				var st billing.SubscriptionStatus
				st.Status = lt.Status
				if err := s.cfg.Decoder.Verify(lt.SignedTransactionInfo, &st.Transaction); err != nil {
					return fmt.Errorf("%w: signed transaction: %v", billing.ErrAppleUnavailable, err)
				}
				if lt.SignedRenewalInfo != "" {
					if err := s.cfg.Decoder.Verify(lt.SignedRenewalInfo, &st.Renewal); err != nil {
						return fmt.Errorf("%w: signed renewal info: %v", billing.ErrAppleUnavailable, err)
					}
				}
				if st.Transaction.OriginalTransactionID != originalTransactionID {
					return fmt.Errorf("%w: signed transaction is for another subscription", billing.ErrAppleUnavailable)
				}
				if err := s.checkTx(st.Transaction, b.env); err != nil {
					return err
				}
				st.Environment = b.env
				result = st
				return nil
			}
		}
		return &APIError{Status: 404, Code: codeTransactionNotFound, Message: "subscription not in the answer"}
	})
	return result, err
}

type historyResponse struct {
	Revision           string   `json:"revision"`
	HasMore            bool     `json:"hasMore"`
	SignedTransactions []string `json:"signedTransactions"`
}

// GetTransactionHistory implements billing.ServerAPI (Get Transaction History v2), oldest first.
func (s *StoreKit) GetTransactionHistory(ctx context.Context, originalTransactionID string) ([]billing.TransactionInfo, error) {
	var all []billing.TransactionInfo
	err := s.try(func(b envBase) error {
		all = nil
		revision := ""
		for page := 0; page < s.cfg.MaxHistoryPages; page++ {
			q := url.Values{"sort": {"ASCENDING"}}
			if revision != "" {
				q.Set("revision", revision)
			}
			var resp historyResponse
			if err := s.call(ctx, b.url, http.MethodGet, "/inApps/v2/history/"+url.PathEscape(originalTransactionID), q, nil, &resp); err != nil {
				return err
			}
			for _, signed := range resp.SignedTransactions {
				var tx billing.TransactionInfo
				if err := s.cfg.Decoder.Verify(signed, &tx); err != nil {
					return fmt.Errorf("%w: signed transaction: %v", billing.ErrAppleUnavailable, err)
				}
				if err := s.checkTx(tx, b.env); err != nil {
					return err
				}
				all = append(all, tx)
			}
			if !resp.HasMore || resp.Revision == "" {
				sort.SliceStable(all, func(i, j int) bool { return all[i].PurchaseDate < all[j].PurchaseDate })
				return nil
			}
			revision = resp.Revision
		}
		return fmt.Errorf("%w: transaction history has too many pages", billing.ErrAppleUnavailable)
	})
	return all, err
}

// SetAppAccountToken implements billing.ServerAPI (Set App Account Token).
func (s *StoreKit) SetAppAccountToken(ctx context.Context, originalTransactionID, appAccountToken string) error {
	return s.try(func(b envBase) error {
		return s.call(ctx, b.url, http.MethodPut, "/inApps/v1/transactions/"+url.PathEscape(originalTransactionID)+"/appAccountToken",
			nil, map[string]string{"appAccountToken": appAccountToken}, nil)
	})
}

// RequestTestNotification asks Apple to send a TEST notification to the server URL configured
// in App Store Connect for env (Production or Sandbox) and returns the token to look it up.
func (s *StoreKit) RequestTestNotification(ctx context.Context, env string) (string, error) {
	base, err := s.baseFor(env)
	if err != nil {
		return "", err
	}
	var resp struct {
		Token string `json:"testNotificationToken"`
	}
	if err := s.call(ctx, base, http.MethodPost, "/inApps/v1/notifications/test", nil, nil, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("%w: no test notification token", billing.ErrAppleUnavailable)
	}
	return resp.Token, nil
}

// TestNotificationResult is Apple's answer to "get test notification status".
type TestNotificationResult struct {
	SignedPayload string
	SendAttempts  []SendAttempt
}

// SendAttempt is one delivery attempt of a notification.
type SendAttempt struct {
	AttemptDate       billing.Ms `json:"attemptDate"`
	SendAttemptResult string     `json:"sendAttemptResult"`
}

// GetTestNotificationStatus looks up a test notification by the token from RequestTestNotification.
func (s *StoreKit) GetTestNotificationStatus(ctx context.Context, env, token string) (TestNotificationResult, error) {
	base, err := s.baseFor(env)
	if err != nil {
		return TestNotificationResult{}, err
	}
	var resp struct {
		SignedPayload string        `json:"signedPayload"`
		SendAttempts  []SendAttempt `json:"sendAttempts"`
	}
	if err := s.call(ctx, base, http.MethodGet, "/inApps/v1/notifications/test/"+url.PathEscape(token), nil, nil, &resp); err != nil {
		return TestNotificationResult{}, err
	}
	return TestNotificationResult{SignedPayload: resp.SignedPayload, SendAttempts: resp.SendAttempts}, nil
}

// HistoryRequest filters the notification history (dates are required by Apple, at most 180
// days apart).
type HistoryRequest struct {
	StartDate        billing.Ms `json:"startDate"`
	EndDate          billing.Ms `json:"endDate"`
	NotificationType string     `json:"notificationType,omitempty"`
	TransactionID    string     `json:"transactionId,omitempty"`
	OnlyFailures     bool       `json:"onlyFailures,omitempty"`
}

// HistoryItem is one past notification; SignedPayload still has to be verified by the caller
// with the billing verifier before it is processed.
type HistoryItem struct {
	SignedPayload string        `json:"signedPayload"`
	SendAttempts  []SendAttempt `json:"sendAttempts"`
}

// GetNotificationHistory returns one page; pass the returned token to get the next one.
func (s *StoreKit) GetNotificationHistory(ctx context.Context, env string, req HistoryRequest, paginationToken string) (items []HistoryItem, next string, hasMore bool, err error) {
	base, err := s.baseFor(env)
	if err != nil {
		return nil, "", false, err
	}
	q := url.Values{}
	if paginationToken != "" {
		q.Set("paginationToken", paginationToken)
	}
	var resp struct {
		History         []HistoryItem `json:"notificationHistory"`
		PaginationToken string        `json:"paginationToken"`
		HasMore         bool          `json:"hasMore"`
	}
	if err := s.call(ctx, base, http.MethodPost, "/inApps/v1/notifications/history", q, req, &resp); err != nil {
		return nil, "", false, err
	}
	return resp.History, resp.PaginationToken, resp.HasMore, nil
}

func (s *StoreKit) baseFor(env string) (string, error) {
	switch env {
	case billing.EnvProduction:
		return s.cfg.ProductionURL, nil
	case billing.EnvSandbox:
		return s.cfg.SandboxURL, nil
	}
	return "", fmt.Errorf("appleclient: unknown environment %q", env)
}
