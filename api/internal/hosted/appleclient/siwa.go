package appleclient

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/hclock"
)

// SIWABaseURL is Apple's ID server.
const SIWABaseURL = "https://appleid.apple.com"

// SIWAAudience is the audience of the client secret.
const SIWAAudience = "https://appleid.apple.com"

// SIWAConfig configures the token exchange and revocation client.
type SIWAConfig struct {
	TeamID   string // Apple developer team id (iss of the client secret)
	KeyID    string // id of the Sign in with Apple key (kid)
	ClientID string // the app's bundle id for native sign in (sub, client_id)
	Key      *ecdsa.PrivateKey
	BaseURL  string // default SIWABaseURL
	Clock    hclock.Clock
	// SecretTTL is the lifetime of each generated client secret (default 5 minutes; Apple
	// allows up to 6 months, short is safer).
	SecretTTL time.Duration
	Options
}

// SIWAClient implements appleauth.CodeExchanger and appleauth.TokenRevoker.
type SIWAClient struct {
	cfg SIWAConfig
	o   Options
}

var (
	_ appleauth.CodeExchanger = (*SIWAClient)(nil)
	_ appleauth.TokenRevoker  = (*SIWAClient)(nil)
)

// NewSIWAClient validates the configuration.
func NewSIWAClient(cfg SIWAConfig) (*SIWAClient, error) {
	if cfg.TeamID == "" || cfg.KeyID == "" || cfg.ClientID == "" || cfg.Key == nil {
		return nil, errors.New("appleclient: SIWA needs team id, key id, client id and key")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = SIWABaseURL
	}
	if cfg.Clock == nil {
		cfg.Clock = hclock.System{}
	}
	if cfg.SecretTTL <= 0 {
		cfg.SecretTTL = 5 * time.Minute
	}
	return &SIWAClient{cfg: cfg, o: cfg.Options.withDefaults()}, nil
}

// clientSecret signs the client secret JWT: header alg ES256 + kid, claims iss=team id,
// iat, exp, aud=https://appleid.apple.com, sub=client id.
func (c *SIWAClient) clientSecret() (string, error) {
	now := c.cfg.Clock.Now()
	return signES256(c.cfg.Key,
		map[string]any{"kid": c.cfg.KeyID},
		map[string]any{
			"iss": c.cfg.TeamID,
			"iat": now.Unix(),
			"exp": now.Add(c.cfg.SecretTTL).Unix(),
			"aud": SIWAAudience,
			"sub": c.cfg.ClientID,
		})
}

type tokenResp struct {
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func (c *SIWAClient) post(ctx context.Context, path string, form func(secret string) url.Values, retry bool) (*response, error) {
	resp, err := send(ctx, c.o, retry, func() (*http.Request, error) {
		secret, err := c.clientSecret()
		if err != nil {
			return nil, errors.New("appleclient: cannot sign the client secret")
		}
		r, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.cfg.BaseURL, "/")+path, strings.NewReader(form(secret).Encode()))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", "application/json")
		return r, nil
	})
	if err != nil {
		if errors.Is(err, errTransport) {
			return nil, fmt.Errorf("%w: %v", appleauth.ErrAppleUnavailable, err)
		}
		return nil, err
	}
	return resp, nil
}

// Exchange trades an authorization code for a refresh token. The code is single use, so a
// 5xx is not retried here (the caller can ask the user to try again).
func (c *SIWAClient) Exchange(ctx context.Context, authorizationCode string) (string, error) {
	resp, err := c.post(ctx, "/auth/token", func(secret string) url.Values {
		return url.Values{"client_id": {c.cfg.ClientID}, "client_secret": {secret}, "code": {authorizationCode}, "grant_type": {"authorization_code"}}
	}, false)
	if err != nil {
		return "", err
	}
	var tr tokenResp
	_ = json.Unmarshal(resp.Body, &tr)
	switch {
	case resp.Status == http.StatusOK && tr.RefreshToken != "":
		return tr.RefreshToken, nil
	case resp.Status == http.StatusOK:
		return "", fmt.Errorf("%w: no refresh token in the answer", appleauth.ErrAppleUnavailable)
	case resp.Status == http.StatusBadRequest && (tr.Error == "invalid_grant" || tr.Error == "invalid_request"):
		return "", appleauth.ErrCodeInvalid
	case resp.Status == http.StatusBadRequest || resp.Status == http.StatusUnauthorized:
		// invalid_client, unauthorized_client...: our configuration, not the user's code
		return "", fmt.Errorf("%w: token endpoint refused our client (%s)", appleauth.ErrAppleUnavailable, safeErrCode(tr.Error))
	default:
		return "", fmt.Errorf("%w: token endpoint HTTP %d", appleauth.ErrAppleUnavailable, resp.Status)
	}
}

// Revoke revokes a refresh token. An invalid_grant answer (already revoked or unknown token)
// counts as success: the token is not usable either way.
func (c *SIWAClient) Revoke(ctx context.Context, refreshToken string) error {
	resp, err := c.post(ctx, "/auth/revoke", func(secret string) url.Values {
		return url.Values{"client_id": {c.cfg.ClientID}, "client_secret": {secret}, "token": {refreshToken}, "token_type_hint": {"refresh_token"}}
	}, true)
	if err != nil {
		return err
	}
	var tr tokenResp
	_ = json.Unmarshal(resp.Body, &tr)
	switch {
	case resp.Status == http.StatusOK:
		return nil
	case resp.Status == http.StatusBadRequest && tr.Error == "invalid_grant":
		return nil
	default:
		return fmt.Errorf("%w: revoke HTTP %d (%s)", appleauth.ErrAppleUnavailable, resp.Status, safeErrCode(tr.Error))
	}
}

// safeErrCode keeps only a short lower-case OAuth error code (never free text).
func safeErrCode(s string) string {
	if len(s) > 40 {
		return "unknown"
	}
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z')) {
			return "unknown"
		}
	}
	if s == "" {
		return "none"
	}
	return s
}
