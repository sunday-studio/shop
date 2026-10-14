// Package billing implements the StoreKit 2 / App Store Server side of the hosted plan:
// verification of signed transactions and App Store Server Notifications V2 (JWS with an x5c
// certificate chain checked against an injected root), the idempotent notification processor,
// the link between a purchase and an account (appAccountToken), a guarded interface for the
// App Store Server API (never called in tests), and the Plan calculator that turns stored
// dates into the plan state and the allowed actions at any moment.
//
// No HTTP here and no network: a thin HTTP layer and a real Server API client come later.
package billing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"shop/internal/hosted/hclock"
)

// ErrInvalidJWS is returned for any malformed, badly signed or untrusted JWS.
var ErrInvalidJWS = errors.New("invalid signed payload")

// Apple's marker extensions on the certificates of a signing chain (from the App Store
// Server Library; not verified against Apple's page here, see decisions-log). Set them on
// the Verifier to require them; tests use their own OIDs to prove the check runs.
var (
	AppleLeafOID         = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 11, 1}
	AppleIntermediateOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 1}
)

// Verifier checks JWS strings signed with ES256 whose header carries an x5c chain.
// The chain must lead to one of Roots; the root inside x5c is never trusted by itself.
type Verifier struct {
	Roots           *x509.CertPool
	Clock           hclock.Clock
	LeafOID         asn1.ObjectIdentifier // optional: leaf must carry this extension
	IntermediateOID asn1.ObjectIdentifier // optional: first intermediate must carry it
}

// NewAppleVerifier is the production setup: Apple's roots (pinned by the owner from Apple's
// certificate authority page) plus the marker extensions.
func NewAppleVerifier(roots *x509.CertPool, clock hclock.Clock) *Verifier {
	return &Verifier{Roots: roots, Clock: clock, LeafOID: AppleLeafOID, IntermediateOID: AppleIntermediateOID}
}

type jwsHeader struct {
	Alg string   `json:"alg"`
	X5C []string `json:"x5c"`
}

// Verify checks the JWS and unmarshals its payload into out.
func (v *Verifier) Verify(token string, out any) error {
	payload, err := v.VerifyRaw(token)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("%w: payload: %v", ErrInvalidJWS, err)
	}
	return nil
}

// VerifyRaw checks the JWS and returns the raw payload bytes.
func (v *Verifier) VerifyRaw(token string) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: not a compact JWS", ErrInvalidJWS)
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header encoding", ErrInvalidJWS)
	}
	var h jwsHeader
	if err := json.Unmarshal(hb, &h); err != nil {
		return nil, fmt.Errorf("%w: header", ErrInvalidJWS)
	}
	if h.Alg != "ES256" { // never "none", never RS256 or HS256 confusion
		return nil, fmt.Errorf("%w: algorithm %q", ErrInvalidJWS, h.Alg)
	}
	if len(h.X5C) == 0 || len(h.X5C) > 5 {
		return nil, fmt.Errorf("%w: certificate chain", ErrInvalidJWS)
	}
	var certs []*x509.Certificate
	for _, s := range h.X5C {
		der, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("%w: certificate encoding", ErrInvalidJWS)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w: certificate", ErrInvalidJWS)
		}
		certs = append(certs, c)
	}
	leaf := certs[0]
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	if v.Roots == nil {
		return nil, fmt.Errorf("%w: no trusted roots configured", ErrInvalidJWS)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{Roots: v.Roots, Intermediates: inter, CurrentTime: v.Clock.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	if err != nil {
		return nil, fmt.Errorf("%w: chain: %v", ErrInvalidJWS, err)
	}
	if len(v.LeafOID) > 0 && !hasExt(leaf, v.LeafOID) {
		return nil, fmt.Errorf("%w: leaf marker extension missing", ErrInvalidJWS)
	}
	if len(v.IntermediateOID) > 0 {
		ok := false
		for _, ch := range chains {
			if len(ch) >= 3 && hasExt(ch[1], v.IntermediateOID) {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%w: intermediate marker extension missing", ErrInvalidJWS)
		}
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: leaf key must be ECDSA P-256", ErrInvalidJWS)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, fmt.Errorf("%w: signature", ErrInvalidJWS)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return nil, fmt.Errorf("%w: signature mismatch", ErrInvalidJWS)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload encoding", ErrInvalidJWS)
	}
	return payload, nil
}

func hasExt(c *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	for _, e := range append(append([]pkix.Extension{}, c.Extensions...), c.ExtraExtensions...) {
		if e.Id.Equal(oid) {
			return true
		}
	}
	return false
}
