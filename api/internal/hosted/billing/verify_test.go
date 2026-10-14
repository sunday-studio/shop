package billing_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/hostedtest"
)

func TestVerifierAcceptsAndRejects(t *testing.T) {
	e := newEnv(t)
	payload := map[string]any{"hello": "world"}
	good := e.pki.Sign(payload)
	var out map[string]any
	if err := e.svc.V.Verify(good, &out); err != nil || out["hello"] != "world" {
		t.Fatalf("good token refused: %v", err)
	}

	other := hostedtest.OtherPKI(t0.AddDate(-1, 0, 0))
	parts := strings.Split(good, ".")
	tamperedPayload := parts[0] + "." + "eyJoZWxsbyI6ImV2aWwifQ" + "." + parts[2]
	wrongKey := e.pki.SignWith(hostedtest.NewKey(), e.pki.Chain(), "ES256", payload)
	noAlg := e.pki.SignWith(e.pki.LeafKey, e.pki.Chain(), "none", payload)
	rs := e.pki.SignWith(e.pki.LeafKey, e.pki.Chain(), "RS256", payload)
	noChain := e.pki.SignWith(e.pki.LeafKey, nil, "ES256", payload)
	leafOnly := e.pki.SignWith(e.pki.LeafKey, e.pki.Chain()[:1], "ES256", payload) // intermediate missing
	// a self-made chain that does not lead to our root
	foreign := other.Sign(payload)
	// a valid chain presented with the foreign root in x5c only must still fail (root is never taken from x5c)
	cases := map[string]string{
		"tampered payload": tamperedPayload, "signature by a key not in the leaf": wrongKey, "alg none": noAlg,
		"alg RS256": rs, "no x5c": noChain, "missing intermediate": leafOnly, "foreign chain": foreign,
		"not a jws": "abc.def", "empty": "", "garbage parts": "a.b.c",
	}
	for name, tok := range cases {
		if err := e.svc.V.Verify(tok, &out); err == nil || !errors.Is(err, billing.ErrInvalidJWS) {
			t.Errorf("%s: want ErrInvalidJWS, got %v", name, err)
		}
	}

	// expired certificate: move the clock past the chain's life
	e.clock.Set(t0.AddDate(20, 0, 0))
	if err := e.svc.V.Verify(good, &out); err == nil {
		t.Error("expired chain accepted")
	}
	e.clock.Set(t0)
	// marker extension required
	v2 := *e.svc.V
	v2.LeafOID = hostedtest.IntermediateOID // leaf does not carry this one
	if err := v2.Verify(good, &out); err == nil {
		t.Error("leaf marker extension not enforced")
	}
	v3 := *e.svc.V
	v3.IntermediateOID = hostedtest.LeafOID
	if err := v3.Verify(good, &out); err == nil {
		t.Error("intermediate marker extension not enforced")
	}
	// no roots configured
	v4 := *e.svc.V
	v4.Roots = nil
	if err := v4.Verify(good, &out); err == nil {
		t.Error("accepted without roots")
	}
	_ = time.Now
}
