// Package hostedtest holds helpers for the hosted packages' tests: a throw-away certificate
// authority that signs JWS payloads the way Apple does (ES256, x5c chain), and a small
// RSA/EC JWT signer for Sign in with Apple. Nothing here is used in production code.
package hostedtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"time"
)

// Test marker OIDs used instead of Apple's, to prove the extension checks run.
var (
	LeafOID         = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 1}
	IntermediateOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 2}
)

// PKI is a generated root, intermediate and leaf.
type PKI struct {
	Root, Intermediate, Leaf *x509.Certificate
	LeafKey                  *ecdsa.PrivateKey
	Pool                     *x509.CertPool // contains only the root
	NotBefore, NotAfter      time.Time
	serial                   int64
}

// NewPKI creates a chain valid from notBefore for ten years.
func NewPKI(notBefore time.Time) *PKI {
	p := &PKI{NotBefore: notBefore, NotAfter: notBefore.AddDate(10, 0, 0)}
	rootKey := key()
	interKey := key()
	p.LeafKey = key()
	rootTmpl := p.tmpl("Test Root CA", true)
	rootDER := mustCert(rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	p.Root, _ = x509.ParseCertificate(rootDER)
	interTmpl := p.tmpl("Test Intermediate", true)
	interTmpl.ExtraExtensions = []pkix.Extension{{Id: IntermediateOID, Value: []byte{5, 0}}}
	interDER := mustCert(interTmpl, p.Root, &interKey.PublicKey, rootKey)
	p.Intermediate, _ = x509.ParseCertificate(interDER)
	leafTmpl := p.tmpl("Test Leaf", false)
	leafTmpl.ExtraExtensions = []pkix.Extension{{Id: LeafOID, Value: []byte{5, 0}}}
	leafDER := mustCert(leafTmpl, p.Intermediate, &p.LeafKey.PublicKey, interKey)
	p.Leaf, _ = x509.ParseCertificate(leafDER)
	p.Pool = x509.NewCertPool()
	p.Pool.AddCert(p.Root)
	return p
}

func key() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}

func (p *PKI) tmpl(cn string, ca bool) *x509.Certificate {
	p.serial++
	t := &x509.Certificate{
		SerialNumber:          big.NewInt(p.serial + 1000),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             p.NotBefore,
		NotAfter:              p.NotAfter,
		BasicConstraintsValid: true,
		IsCA:                  ca,
	}
	if ca {
		t.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		t.KeyUsage = x509.KeyUsageDigitalSignature
	}
	return t
}

func mustCert(tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) []byte {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		panic(err)
	}
	return der
}

// Chain returns the x5c header value: leaf, intermediate, root (as Apple does).
func (p *PKI) Chain() []string {
	return []string{b64std(p.Leaf.Raw), b64std(p.Intermediate.Raw), b64std(p.Root.Raw)}
}

// Sign builds a compact ES256 JWS over the JSON of payload with the standard chain.
func (p *PKI) Sign(payload any) string { return p.SignWith(p.LeafKey, p.Chain(), "ES256", payload) }

// SignWith allows tampering: another key, chain or alg.
func (p *PKI) SignWith(k *ecdsa.PrivateKey, chain []string, alg string, payload any) string {
	hdr, _ := json.Marshal(map[string]any{"alg": alg, "x5c": chain})
	body, _ := json.Marshal(payload)
	signing := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(body)
	d := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k, d[:])
	if err != nil {
		panic(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// OtherPKI returns an unrelated chain (an attacker's CA) for negative tests.
func OtherPKI(notBefore time.Time) *PKI { return NewPKI(notBefore) }

func b64std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// NewKey returns a fresh P-256 key (for tampering tests).
func NewKey() *ecdsa.PrivateKey { return key() }
