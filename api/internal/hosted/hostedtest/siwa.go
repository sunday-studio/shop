package hostedtest

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
)

// FakeIDP is a fake "appleid.apple.com": it publishes a JWKS (one RSA and one EC key) and signs
// identity tokens and server-to-server event tokens. It implements appleauth.KeyFetcher.
type FakeIDP struct {
	mu      sync.Mutex
	RSAKey  *rsa.PrivateKey
	ECKey   *ecdsa.PrivateKey
	RSAKid  string
	ECKid   string
	Hidden  map[string]bool // kids left out of the published JWKS
	Fetches int
	Down    bool
}

// NewFakeIDP generates keys.
func NewFakeIDP() *FakeIDP {
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return &FakeIDP{RSAKey: rk, ECKey: NewKey(), RSAKid: "rsa-1", ECKid: "ec-1", Hidden: map[string]bool{}}
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// FetchJWKS returns the published key set.
func (f *FakeIDP) FetchJWKS(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Fetches++
	if f.Down {
		return nil, errors.New("connection refused")
	}
	var keys []map[string]string
	if !f.Hidden[f.RSAKid] {
		keys = append(keys, map[string]string{"kty": "RSA", "kid": f.RSAKid, "use": "sig", "alg": "RS256",
			"n": b64u(f.RSAKey.N.Bytes()), "e": b64u(big.NewInt(int64(f.RSAKey.E)).Bytes())})
	}
	if !f.Hidden[f.ECKid] {
		keys = append(keys, map[string]string{"kty": "EC", "kid": f.ECKid, "use": "sig", "alg": "ES256", "crv": "P-256",
			"x": b64u(f.ECKey.X.Bytes()), "y": b64u(f.ECKey.Y.Bytes())})
	}
	return json.Marshal(map[string]any{"keys": keys})
}

// Sign signs claims with the key named by kid (RS256 for the RSA key, ES256 for the EC key).
func (f *FakeIDP) Sign(kid string, claims map[string]any) string {
	if kid == f.RSAKid {
		return f.SignWith("RS256", kid, f.RSAKey, claims)
	}
	return f.SignWith("ES256", kid, f.ECKey, claims)
}

// SignWith allows any header algorithm and key (for tampering tests).
func (f *FakeIDP) SignWith(alg, kid string, key crypto.Signer, claims map[string]any) string {
	hdr, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signing := b64u(hdr) + "." + b64u(body)
	d := sha256.Sum256([]byte(signing))
	var sig []byte
	switch k := key.(type) {
	case *rsa.PrivateKey:
		s, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, d[:])
		if err != nil {
			panic(err)
		}
		sig = s
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, k, d[:])
		if err != nil {
			panic(err)
		}
		sig = make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
	}
	return signing + "." + b64u(sig)
}
