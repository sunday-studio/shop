// Package appleauth implements Sign in with Apple for the hosted mode: server-issued
// single-use nonces, identity-token (JWT) verification against Apple's JWKS with an injectable
// key fetcher and clock, account upsert and device-token minting, the revocation call
// abstraction, and the server-to-server event handler. No HTTP and no network: Apple's key
// endpoint, token endpoint and revoke endpoint are interfaces with fakes.
package appleauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"shop/internal/hosted/hclock"
)

// KeyFetcher fetches the raw JWKS document (https://appleid.apple.com/auth/keys in production).
type KeyFetcher interface {
	FetchJWKS(ctx context.Context) ([]byte, error)
}

// ErrKeysUnavailable means the key set could not be fetched (HTTP 502 apple_unavailable).
var ErrKeysUnavailable = errors.New("apple keys unavailable")

// ErrUnknownKey means no key with that kid exists even after a refresh.
var ErrUnknownKey = errors.New("unknown signing key")

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// KeySet caches Apple's keys. It refetches after TTL and on an unknown kid (at most once per
// MinRefetch, so a flood of bad tokens cannot hammer Apple).
type KeySet struct {
	Fetcher    KeyFetcher
	Clock      hclock.Clock
	TTL        time.Duration // default 1 hour
	MinRefetch time.Duration // default 1 minute

	mu        sync.Mutex
	keys      map[string]crypto.PublicKey
	fetchedAt time.Time
	lastTry   time.Time
}

func (k *KeySet) refresh(ctx context.Context) error {
	now := k.Clock.Now()
	k.lastTry = now
	raw, err := k.Fetcher.FetchJWKS(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrKeysUnavailable, err)
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: bad JWKS: %v", ErrKeysUnavailable, err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, j := range doc.Keys {
		if pub, err := j.public(); err == nil && j.Kid != "" {
			keys[j.Kid] = pub
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: JWKS holds no usable key", ErrKeysUnavailable)
	}
	k.keys, k.fetchedAt = keys, now
	return nil
}

// Key returns the public key for kid.
func (k *KeySet) Key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	ttl, minRe := k.TTL, k.MinRefetch
	if ttl == 0 {
		ttl = time.Hour
	}
	if minRe == 0 {
		minRe = time.Minute
	}
	now := k.Clock.Now()
	if k.keys == nil || now.Sub(k.fetchedAt) >= ttl {
		if err := k.refresh(ctx); err != nil {
			if k.keys == nil {
				return nil, err
			}
			// keep serving the stale set when Apple is down; only unknown kids fail below
		}
	}
	if pub, ok := k.keys[kid]; ok {
		return pub, nil
	}
	if now.Sub(k.lastTry) >= minRe {
		if err := k.refresh(ctx); err != nil {
			return nil, err
		}
		if pub, ok := k.keys[kid]; ok {
			return pub, nil
		}
	}
	return nil, ErrUnknownKey
}

func (j jwk) public() (crypto.PublicKey, error) {
	switch j.Kty {
	case "RSA":
		n, err1 := base64.RawURLEncoding.DecodeString(j.N)
		e, err2 := base64.RawURLEncoding.DecodeString(j.E)
		if err1 != nil || err2 != nil || len(n) < 256 {
			return nil, errors.New("bad RSA key")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
	case "EC":
		if j.Crv != "P-256" {
			return nil, errors.New("unsupported curve")
		}
		x, err1 := base64.RawURLEncoding.DecodeString(j.X)
		y, err2 := base64.RawURLEncoding.DecodeString(j.Y)
		if err1 != nil || err2 != nil {
			return nil, errors.New("bad EC key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return nil, errors.New("point not on curve")
		}
		return pub, nil
	}
	return nil, errors.New("unsupported key type")
}
