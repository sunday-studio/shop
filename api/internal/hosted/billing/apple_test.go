package billing_test

import (
	"testing"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
)

func TestPinnedAppleVerifierRejectsNonApple(t *testing.T) {
	v := billing.NewPinnedAppleVerifier(hclock.NewFake(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)))
	p := hostedtest.NewPKI(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	var out map[string]any
	if err := v.Verify(p.Sign(map[string]any{"a": 1}), &out); err == nil {
		t.Fatal("a chain that does not lead to Apple Root CA - G3 was accepted")
	}
	if v.Roots == nil || len(v.LeafOID) == 0 || len(v.IntermediateOID) == 0 {
		t.Fatal("verifier not fully configured")
	}
}
