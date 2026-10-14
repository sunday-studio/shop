package appleclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"shop/internal/hosted/hclock"
)

// DefaultJWKSURL is Sign in with Apple's key set.
const DefaultJWKSURL = "https://appleid.apple.com/auth/keys"

// JWKSFetcher implements appleauth.KeyFetcher over HTTP. It is a rate limiter in front of
// Apple, not a second key cache (appleauth.KeySet owns the TTL and the refresh on an unknown
// kid): a call within MinInterval of the last successful fetch is answered from memory, so a
// flood of tokens with unknown kids reaches Apple at most once per MinInterval. If Apple is
// down, the last good document is served for up to MaxStale.
type JWKSFetcher struct {
	URL         string        // default DefaultJWKSURL
	Options                   // HTTP client, retries, logger
	Clock       hclock.Clock  // default system
	MinInterval time.Duration // default 10 s
	MaxStale    time.Duration // default 1 hour

	mu        sync.Mutex
	doc       []byte
	fetchedAt time.Time
}

// FetchJWKS returns the raw JWKS document.
func (f *JWKSFetcher) FetchJWKS(ctx context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	clk := f.Clock
	if clk == nil {
		clk = hclock.System{}
	}
	minInt, maxStale := f.MinInterval, f.MaxStale
	if minInt <= 0 {
		minInt = 10 * time.Second
	}
	if maxStale <= 0 {
		maxStale = time.Hour
	}
	now := clk.Now()
	if f.doc != nil && now.Sub(f.fetchedAt) < minInt {
		return f.doc, nil
	}
	doc, err := f.fetch(ctx)
	if err != nil {
		if f.doc != nil && now.Sub(f.fetchedAt) < maxStale {
			f.opts().Log.Warn("apple JWKS fetch failed, serving the last good key set")
			return f.doc, nil
		}
		return nil, err
	}
	f.doc, f.fetchedAt = doc, now
	return doc, nil
}

func (f *JWKSFetcher) opts() Options { return f.Options.withDefaults() }

func (f *JWKSFetcher) fetch(ctx context.Context) ([]byte, error) {
	o := f.opts()
	u := f.URL
	if u == "" {
		u = DefaultJWKSURL
	}
	resp, err := send(ctx, o, true, func() (*http.Request, error) {
		r, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Accept", "application/json")
		return r, nil
	})
	if err != nil {
		return nil, err
	}
	if resp.Status != http.StatusOK {
		return nil, fmt.Errorf("apple JWKS: HTTP %d", resp.Status)
	}
	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if json.Unmarshal(resp.Body, &doc) != nil || len(doc.Keys) == 0 {
		return nil, errors.New("apple JWKS: unexpected document")
	}
	if !strings.HasPrefix(strings.TrimSpace(string(resp.Body)), "{") {
		return nil, errors.New("apple JWKS: unexpected document")
	}
	return resp.Body, nil
}
