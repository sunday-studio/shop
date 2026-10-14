package hostedapi_test

import (
	"testing"
	"time"

	"shop/internal/hosted/billing"
	w "shop/internal/hosted/hostedworld"
)

// A sandbox purchase shows as environment "sandbox" in GET /v1/account and in the admin account
// lookup, never counts as paying in the admin summary, and ends at the lifetime cap.
func TestSandboxEntitlementIsFlaggedCappedAndNotRevenue(t *testing.T) {
	world := w.New(t, w.NoRates)
	world.Calc.SandboxMax = 5 * 24 * time.Hour
	ama := world.SignIn("sub-ama", "Ama")
	kofi := world.SignIn("sub-kofi", "Kofi")
	world.Subscribe(ama, "otx-sb") // helper transactions are Sandbox

	paid := func(x *billing.TransactionInfo) {
		x.Environment, x.OfferType, x.OfferDiscountType = "Production", 0, ""
	}
	tx := world.Tx(kofi, "otx-prod", paid)
	world.SetStatus("otx-prod", 1, tx)
	want(t, world.Do("POST", "/v1/billing/apple/transactions", kofi.Token, map[string]any{"signed_transaction": world.PKI.Sign(tx)}), 200, "")

	plan := func(u w.User) map[string]any {
		r := world.Do("GET", "/v1/account", u.Token, nil)
		want(t, r, 200, "")
		return r.JSON()["plan"].(map[string]any)
	}
	if plan(ama)["environment"] != "sandbox" || plan(kofi)["environment"] != "production" {
		t.Fatalf("environments: %v / %v", plan(ama)["environment"], plan(kofi)["environment"])
	}
	f := world.DoAdmin("GET", "/admin/v1/accounts?support_code="+ama.Account["support_code"].(string), nil)
	want(t, f, 200, "")
	if f.JSON()["accounts"].([]any)[0].(map[string]any)["plan_environment"] != "sandbox" {
		t.Fatalf("%s", f.Body)
	}
	s := world.DoAdmin("GET", "/admin/v1/subscriptions/summary", nil)
	want(t, s, 200, "")
	if j := s.JSON(); j["paying"] != float64(1) || j["sandbox"] != float64(1) {
		t.Fatalf("summary %s", s.Body)
	}

	// day 6: past the 5 day cap the sandbox plan is read-only although Apple's period runs to day 14
	world.At(0)
	if got := world.PlanState(ama); got != billing.StateTrial {
		t.Fatalf("day 0: %s", got)
	}
	world.At(6)
	if got := world.PlanState(ama); got != billing.StateLapsedReadOnly {
		t.Fatalf("day 6 sandbox: %s", got)
	}
	if got := world.PlanState(kofi); got != billing.StateActive {
		t.Fatalf("day 6 production: %s", got)
	}
}
