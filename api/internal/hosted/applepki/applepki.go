// Package applepki pins Apple's root certificate in the binary and verifies the x5c
// certificate chain of the JWS values Apple signs (App Store Server Notifications V2, signed
// transactions and renewal info) the way Apple's own server libraries do.
//
// What was checked against apple/app-store-server-library-java (ChainVerifier.java,
// AppleExtensionCertPathChecker.java, SignedDataVerifier.java), 2026-10-14:
//   - the chain has exactly three certificates (leaf, intermediate, root); the root is a trust
//     anchor supplied by the caller, never trusted because it is inside x5c;
//   - the leaf carries the non-critical extension 1.2.840.113635.100.6.11.1 and the
//     intermediate 1.2.840.113635.100.6.2.1;
//   - the chain is validated at the payload's signedDate (or "now" when online checks are on);
//   - revocation checking (OCSP) is off by default, and is off here;
//   - the signature algorithm is ES256.
//
// The pinned root is "Apple Root CA - G3", downloaded from
// https://www.apple.com/certificateauthority/AppleRootCA-G3.cer. Its SHA-256 fingerprint
// (63343abf...3e9179) matches the copy in macOS's system trust store. Apple's page does not
// print fingerprints, so that comparison is the independent check.
package applepki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	_ "embed"
)

//go:embed AppleRootCA-G3.cer
var appleRootCAG3DER []byte

// AppleRootCAG3SHA256 is the expected SHA-256 of the DER certificate (hex, lower case).
const AppleRootCAG3SHA256 = "63343abfb89a6a03ebb57e9b3f5fa7be7c4f5c756f3017b3a8c488c3653e9179"

// Apple's marker extensions: the leaf (receipt signer) and the WWDR intermediate.
var (
	LeafOID         = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 11, 1}
	IntermediateOID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 2, 1}
)

// ErrInvalid is wrapped by every verification failure.
var ErrInvalid = errors.New("apple chain invalid")

// maxClockSkew is how far a signedDate may lie in the future.
const maxClockSkew = 5 * time.Minute

// AppleRoot returns the pinned Apple Root CA - G3. It panics if the embedded bytes do not
// match the expected fingerprint (a corrupted binary must not trust anything).
func AppleRoot() *x509.Certificate {
	sum := sha256.Sum256(appleRootCAG3DER)
	if hex.EncodeToString(sum[:]) != AppleRootCAG3SHA256 {
		panic("applepki: embedded Apple root certificate does not match its pinned fingerprint")
	}
	c, err := x509.ParseCertificate(appleRootCAG3DER)
	if err != nil {
		panic("applepki: embedded Apple root certificate: " + err.Error())
	}
	return c
}

// ChainVerifier validates Apple-style signing chains against pinned roots.
type ChainVerifier struct {
	roots           []*x509.Certificate
	pool            *x509.CertPool
	LeafOID         asn1.ObjectIdentifier
	IntermediateOID asn1.ObjectIdentifier
}

// NewApple returns the production verifier: Apple Root CA - G3 and Apple's marker OIDs.
func NewApple() *ChainVerifier { return New(AppleRoot(), LeafOID, IntermediateOID) }

// New builds a verifier with the given root and marker OIDs (tests use their own).
func New(root *x509.Certificate, leafOID, intermediateOID asn1.ObjectIdentifier) *ChainVerifier {
	p := x509.NewCertPool()
	p.AddCert(root)
	return &ChainVerifier{roots: []*x509.Certificate{root}, pool: p, LeafOID: leafOID, IntermediateOID: intermediateOID}
}

// Pool returns the trust anchors as a pool (for billing.Verifier.Roots).
func (c *ChainVerifier) Pool() *x509.CertPool { return c.pool }

// VerifyX5C validates a base64 (standard encoding) DER chain at time at and returns the
// leaf's public key.
func (c *ChainVerifier) VerifyX5C(x5c []string, at time.Time) (*ecdsa.PublicKey, error) {
	if len(x5c) != 3 {
		return nil, fmt.Errorf("%w: chain length %d, want 3", ErrInvalid, len(x5c))
	}
	certs := make([]*x509.Certificate, 3)
	for i, s := range x5c {
		der, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("%w: certificate encoding", ErrInvalid)
		}
		if certs[i], err = x509.ParseCertificate(der); err != nil {
			return nil, fmt.Errorf("%w: certificate", ErrInvalid)
		}
	}
	// The third certificate must be one of our roots, byte for byte.
	pinned := false
	for _, r := range c.roots {
		if string(r.Raw) == string(certs[2].Raw) {
			pinned = true
		}
	}
	if !pinned {
		return nil, fmt.Errorf("%w: root is not a pinned root", ErrInvalid)
	}
	inter := x509.NewCertPool()
	inter.AddCert(certs[1])
	chains, err := certs[0].Verify(x509.VerifyOptions{Roots: c.pool, Intermediates: inter, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	okPath := false
	for _, ch := range chains {
		if len(ch) == 3 && ch[1].Equal(certs[1]) {
			okPath = true
		}
	}
	if !okPath {
		return nil, fmt.Errorf("%w: unexpected path", ErrInvalid)
	}
	if !hasExt(certs[0], c.LeafOID) {
		return nil, fmt.Errorf("%w: leaf marker extension missing", ErrInvalid)
	}
	if !hasExt(certs[1], c.IntermediateOID) {
		return nil, fmt.Errorf("%w: intermediate marker extension missing", ErrInvalid)
	}
	pub, ok := certs[0].PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: leaf key must be ECDSA P-256", ErrInvalid)
	}
	return pub, nil
}

func hasExt(c *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	if len(oid) == 0 {
		return false // a verifier without markers fails closed
	}
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return true
		}
	}
	return false
}

// VerifyJWS verifies a compact JWS (ES256, x5c header) and returns its payload. The chain is
// validated at the payload's signedDate (Apple's rule), as long as that date is not in the
// future; without a signedDate it is validated at now. now is the caller's clock.
func (c *ChainVerifier) VerifyJWS(token string, now time.Time) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: not a compact JWS", ErrInvalid)
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header encoding", ErrInvalid)
	}
	var h struct {
		Alg string   `json:"alg"`
		X5C []string `json:"x5c"`
	}
	if json.Unmarshal(hb, &h) != nil {
		return nil, fmt.Errorf("%w: header", ErrInvalid)
	}
	if h.Alg != "ES256" {
		return nil, fmt.Errorf("%w: algorithm %q", ErrInvalid, h.Alg)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload encoding", ErrInvalid)
	}
	at := now
	var sd struct {
		SignedDate int64 `json:"signedDate"`
	}
	if json.Unmarshal(payload, &sd) == nil && sd.SignedDate > 0 {
		t := time.UnixMilli(sd.SignedDate).UTC()
		if t.After(now.Add(maxClockSkew)) {
			return nil, fmt.Errorf("%w: signedDate in the future", ErrInvalid)
		}
		if t.Before(now) {
			at = t
		}
	}
	pub, err := c.VerifyX5C(h.X5C, at)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, fmt.Errorf("%w: signature", ErrInvalid)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, fmt.Errorf("%w: signature mismatch", ErrInvalid)
	}
	return payload, nil
}
