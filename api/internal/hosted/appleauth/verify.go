package appleauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/hosted/hclock"
)

// Issuer is Apple's token issuer.
const Issuer = "https://appleid.apple.com"

// Errors with contract codes. Compare with control.Is.
var (
	ErrTokenInvalid = &control.Error{Code: "apple_token_invalid", Status: http.StatusUnauthorized, Msg: "identity token is invalid"}
	ErrNonceInvalid = &control.Error{Code: "apple_nonce_invalid", Status: http.StatusUnauthorized, Msg: "nonce is unknown, used or expired"}
	ErrUnavailable  = &control.Error{Code: "apple_unavailable", Status: http.StatusBadGateway, Msg: "Apple is unavailable"}
)

// Claims are the identity token claims the server uses.
type Claims struct {
	Iss            string
	Aud            []string
	Sub            string
	Nonce          string
	Email          string
	EmailVerified  bool
	IsPrivateEmail bool
	Exp, Iat       time.Time
	Jti            string
	Events         string // server-to-server tokens: the events JSON
}

// Verifier checks Apple-signed JWTs.
type Verifier struct {
	Keys     *KeySet
	Clock    hclock.Clock
	Audience string        // the app's bundle id
	Leeway   time.Duration // clock skew allowed on exp and iat (default 30 s)
}

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrTokenInvalid, fmt.Sprintf(format, a...))
}

// strBool accepts true, "true", false, "false" (Apple sends both forms).
type strBool bool

func (b *strBool) UnmarshalJSON(d []byte) error {
	s := strings.Trim(string(d), `"`)
	*b = strBool(s == "true")
	return nil
}

// flexNum accepts a JSON number or a numeric string.
type flexAud []string

func (a *flexAud) UnmarshalJSON(d []byte) error {
	var one string
	if json.Unmarshal(d, &one) == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(d, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

type rawClaims struct {
	Iss            string  `json:"iss"`
	Aud            flexAud `json:"aud"`
	Sub            string  `json:"sub"`
	Nonce          string  `json:"nonce"`
	Email          string  `json:"email"`
	EmailVerified  strBool `json:"email_verified"`
	IsPrivateEmail strBool `json:"is_private_email"`
	Exp            int64   `json:"exp"`
	Iat            int64   `json:"iat"`
	Jti            string  `json:"jti"`
	Events         any     `json:"events"`
}

// VerifyIdentityToken verifies signature (key by kid, algorithm by key type), iss, aud and exp.
// The nonce claim is returned for the caller to compare.
func (v *Verifier) VerifyIdentityToken(ctx context.Context, token string) (Claims, error) {
	c, err := v.verify(ctx, token, true)
	if err != nil {
		return c, err
	}
	if c.Sub == "" {
		return c, bad("missing subject")
	}
	return c, nil
}

// VerifyEventToken verifies a server-to-server notification token (exp is not required).
func (v *Verifier) VerifyEventToken(ctx context.Context, token string) (Claims, error) {
	return v.verify(ctx, token, false)
}

func (v *Verifier) verify(ctx context.Context, token string, needExp bool) (Claims, error) {
	var out Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return out, bad("not a JWT")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return out, bad("header encoding")
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(hb, &h) != nil || h.Kid == "" {
		return out, bad("header")
	}
	pub, err := v.Keys.Key(ctx, h.Kid)
	switch {
	case errors.Is(err, ErrUnknownKey):
		return out, bad("unknown key id")
	case err != nil:
		return out, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return out, bad("signature encoding")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	// The algorithm comes from the key; the header must agree (blocks alg confusion and "none").
	switch k := pub.(type) {
	case *rsa.PublicKey:
		if h.Alg != "RS256" {
			return out, bad("algorithm %q does not fit the key", h.Alg)
		}
		if rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) != nil {
			return out, bad("signature")
		}
	case *ecdsa.PublicKey:
		if h.Alg != "ES256" || len(sig) != 64 {
			return out, bad("algorithm %q does not fit the key", h.Alg)
		}
		if !ecdsa.Verify(k, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return out, bad("signature")
		}
	default:
		return out, bad("unsupported key")
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return out, bad("payload encoding")
	}
	var rc rawClaims
	if err := json.Unmarshal(pb, &rc); err != nil {
		return out, bad("payload")
	}
	now := v.Clock.Now()
	leeway := v.Leeway
	if leeway == 0 {
		leeway = 30 * time.Second
	}
	if rc.Iss != Issuer {
		return out, bad("issuer")
	}
	okAud := false
	for _, a := range rc.Aud {
		if a == v.Audience {
			okAud = true
		}
	}
	if !okAud || v.Audience == "" {
		return out, bad("audience")
	}
	if rc.Exp == 0 && needExp {
		return out, bad("missing exp")
	}
	if rc.Exp != 0 && !now.Before(time.Unix(rc.Exp, 0).Add(leeway)) {
		return out, bad("expired")
	}
	if rc.Iat != 0 && time.Unix(rc.Iat, 0).After(now.Add(leeway)) {
		return out, bad("issued in the future")
	}
	out = Claims{Iss: rc.Iss, Aud: rc.Aud, Sub: rc.Sub, Nonce: rc.Nonce, Email: rc.Email, EmailVerified: bool(rc.EmailVerified),
		IsPrivateEmail: bool(rc.IsPrivateEmail), Jti: rc.Jti}
	if rc.Exp != 0 {
		out.Exp = time.Unix(rc.Exp, 0).UTC()
	}
	if rc.Iat != 0 {
		out.Iat = time.Unix(rc.Iat, 0).UTC()
	}
	switch ev := rc.Events.(type) {
	case string:
		out.Events = ev
	case map[string]any:
		b, _ := json.Marshal(ev)
		out.Events = string(b)
	}
	return out, nil
}

// NonceClaim is what the nonce claim must equal for a raw nonce: lowercase hex of sha256 of the
// UTF-8 bytes of the nonce string (the app sends this to ASAuthorizationAppleIDRequest).
func NonceClaim(rawNonce string) string {
	h := sha256.Sum256([]byte(rawNonce))
	return hex.EncodeToString(h[:])
}
