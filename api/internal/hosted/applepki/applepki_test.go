package applepki_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/applepki"
	"shop/internal/hosted/hostedtest"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func setup() (*hostedtest.PKI, *applepki.ChainVerifier) {
	p := hostedtest.NewPKI(t0.AddDate(-1, 0, 0))
	return p, applepki.New(p.Root, hostedtest.LeafOID, hostedtest.IntermediateOID)
}

func TestPinnedRoot(t *testing.T) {
	r := applepki.AppleRoot() // panics on a fingerprint mismatch
	sum := sha256.Sum256(r.Raw)
	if hex.EncodeToString(sum[:]) != applepki.AppleRootCAG3SHA256 || r.Subject.CommonName != "Apple Root CA - G3" {
		t.Fatal("wrong root")
	}
	if !r.IsCA || r.NotAfter.Year() != 2039 {
		t.Fatalf("unexpected root: %v", r.NotAfter)
	}
	// Apple's verifier does not accept a test chain
	p, _ := setup()
	if _, err := applepki.NewApple().VerifyJWS(p.Sign(map[string]any{"signedDate": t0.UnixMilli()}), t0); !errors.Is(err, applepki.ErrInvalid) {
		t.Fatalf("test chain accepted by Apple's pinned root: %v", err)
	}
}

func TestVerifyJWS(t *testing.T) {
	p, v := setup()
	payload := map[string]any{"hello": "world", "signedDate": t0.UnixMilli()}
	good := p.Sign(payload)
	if out, err := v.VerifyJWS(good, t0); err != nil || !strings.Contains(string(out), "world") {
		t.Fatalf("valid chain refused: %v", err)
	}

	other := hostedtest.OtherPKI(t0.AddDate(-1, 0, 0))
	parts := strings.Split(good, ".")
	chain := p.Chain()
	// attacker chain whose root is swapped in x5c
	swapped := p.SignWith(p.LeafKey, []string{chain[0], chain[1], other.Chain()[2]}, "ES256", payload)
	cases := map[string]string{
		"wrong root":           other.Sign(payload),
		"root swapped in x5c":  swapped,
		"tampered payload":     parts[0] + ".eyJoZWxsbyI6ImV2aWwifQ." + parts[2],
		"alg none":             p.SignWith(p.LeafKey, chain, "none", payload),
		"alg RS256":            p.SignWith(p.LeafKey, chain, "RS256", payload),
		"alg HS256":            p.SignWith(p.LeafKey, chain, "HS256", payload),
		"wrong signing key":    p.SignWith(hostedtest.NewKey(), chain, "ES256", payload),
		"no root in chain":     p.SignWith(p.LeafKey, chain[:2], "ES256", payload),
		"leaf only":            p.SignWith(p.LeafKey, chain[:1], "ES256", payload),
		"four certificates":    p.SignWith(p.LeafKey, append(append([]string{}, chain...), chain[2]), "ES256", payload),
		"no chain":             p.SignWith(p.LeafKey, nil, "ES256", payload),
		"signedDate in future": p.Sign(map[string]any{"signedDate": t0.Add(time.Hour).UnixMilli()}),
		"garbage":              "a.b.c",
		"not a jws":            "abc",
		"empty":                "",
	}
	for name, tok := range cases {
		if _, err := v.VerifyJWS(tok, t0); !errors.Is(err, applepki.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestMarkerOIDs(t *testing.T) {
	p, _ := setup()
	tok := p.Sign(map[string]any{"signedDate": t0.UnixMilli()})
	// leaf lacks the intermediate's OID and the other way round
	if _, err := applepki.New(p.Root, hostedtest.IntermediateOID, hostedtest.IntermediateOID).VerifyJWS(tok, t0); err == nil {
		t.Error("missing leaf OID accepted")
	}
	if _, err := applepki.New(p.Root, hostedtest.LeafOID, hostedtest.LeafOID).VerifyJWS(tok, t0); err == nil {
		t.Error("missing intermediate OID accepted")
	}
	if _, err := applepki.New(p.Root, nil, nil).VerifyJWS(tok, t0); err == nil {
		t.Error("verifier without markers must fail closed")
	}
}

func TestExpiryAndSignedDate(t *testing.T) {
	p, v := setup() // certificates valid t0-1y .. t0+9y
	old := p.Sign(map[string]any{"signedDate": t0.UnixMilli()})
	// chain expired by the time we look: signed at t0, checked at t0+20y, still valid at signedDate (Apple's rule)
	if _, err := v.VerifyJWS(old, t0.AddDate(20, 0, 0)); err != nil {
		t.Errorf("signed while valid, checked later: %v", err)
	}
	// a payload claiming to be signed after the leaf expired is rejected
	late := p.Sign(map[string]any{"signedDate": t0.AddDate(15, 0, 0).UnixMilli()})
	if _, err := v.VerifyJWS(late, t0.AddDate(15, 0, 0)); err == nil {
		t.Error("expired leaf accepted")
	}
	// signed before the leaf existed
	early := p.Sign(map[string]any{"signedDate": t0.AddDate(-3, 0, 0).UnixMilli()})
	if _, err := v.VerifyJWS(early, t0); err == nil {
		t.Error("not yet valid leaf accepted")
	}
	// no signedDate: validated at now
	nodate := p.Sign(map[string]any{"x": 1})
	if _, err := v.VerifyJWS(nodate, t0); err != nil {
		t.Errorf("no signedDate, valid now: %v", err)
	}
	if _, err := v.VerifyJWS(nodate, t0.AddDate(20, 0, 0)); err == nil {
		t.Error("no signedDate, expired now: accepted")
	}
}
