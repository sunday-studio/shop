package billing_test

import (
	"strings"
	"testing"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
)

func (e *env) statusFor(tx billing.TransactionInfo, status int) {
	e.api.SetStatus(tx.OriginalTransactionID, billing.SubscriptionStatus{Status: status, Transaction: tx, Renewal: *ren(1), Environment: tx.Environment})
}

func TestBindFirstPurchaseThenRepeat(t *testing.T) {
	e := newEnv(t)
	a := e.account("ama")
	tx := e.tx(a, "otx1")
	e.statusFor(tx, 1)
	res, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(tx))
	if err != nil || res.Result != billing.ResultBound || res.Household.ID == "" || res.Entitlement.State != control.EntActive {
		t.Fatalf("%+v %v", res, err)
	}
	if e.api.CallCount("set_token") != 0 {
		t.Fatal("Set App Account Token must not be needed when the token matches")
	}
	if p := e.plan(a); p.State != billing.StateTrial {
		t.Fatalf("%s", p.State)
	}
	res, err = e.svc.BindTransaction(e.ctx, a.ID, e.sign(tx))
	if err != nil || res.Result != billing.ResultAlreadyBound {
		t.Fatalf("%+v %v", res, err)
	}
	if n, _ := e.st.EntitlementsForAccount(e.ctx, a.ID); len(n) != 1 {
		t.Fatalf("%d entitlements", len(n))
	}
}

func TestBindRejections(t *testing.T) {
	e := newEnv(t)
	a, b := e.account("a"), e.account("b")
	tx := e.tx(a, "otx1")
	e.statusFor(tx, 1)

	// another account's appAccountToken
	_, err := e.svc.BindTransaction(e.ctx, b.ID, e.sign(tx))
	if !control.Is(err, billing.ErrBelongsToOther) {
		t.Fatalf("token of another account: %v", err)
	}
	// bind properly, then a token-less copy from a different account is refused
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(tx)); err != nil {
		t.Fatal(err)
	}
	noTok := e.tx(a, "otx1", func(x *billing.TransactionInfo) { x.AppAccountToken = "" })
	if _, err := e.svc.BindTransaction(e.ctx, b.ID, e.sign(noTok)); !control.Is(err, billing.ErrBelongsToOther) {
		t.Fatalf("bound elsewhere: %v", err)
	}
	// garbage and bad signature
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, "x.y.z"); !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("garbage: %v", err)
	}
	parts := strings.Split(e.sign(tx), ".")
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, parts[0]+"."+parts[1]+"."+parts[2][:len(parts[2])-2]+"AA"); !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("bad signature: %v", err)
	}
	// wrong bundle
	wb := e.tx(a, "otx2", func(x *billing.TransactionInfo) { x.BundleID = "com.evil" })
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(wb)); !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("bundle: %v", err)
	}
	// product not in the allow-list
	wp := e.tx(a, "otx3", func(x *billing.TransactionInfo) { x.ProductID = "com.other" })
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(wp)); !control.Is(err, billing.ErrProductNotAllowed) {
		t.Fatalf("product: %v", err)
	}
	// Xcode environment refused unless allowed
	xc := e.tx(a, "otx4", func(x *billing.TransactionInfo) { x.Environment = "Xcode" })
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(xc)); !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("environment: %v", err)
	}
	// locked account
	l := e.account("l")
	e.st.SetAccountStatus(e.ctx, l.ID, control.AccountLocked)
	if _, err := e.svc.BindTransaction(e.ctx, l.ID, e.sign(e.tx(l, "otx5"))); !control.Is(err, control.ErrAccountLocked) {
		t.Fatalf("locked: %v", err)
	}
	// nothing was created for the failures
	for _, id := range []string{"otx2", "otx3", "otx4", "otx5"} {
		if _, err := e.st.EntitlementByOriginalTx(e.ctx, id); !control.Is(err, control.ErrNotFound) {
			t.Errorf("%s created", id)
		}
	}
}

func TestBindWhenAppleIsDownOrDisagrees(t *testing.T) {
	e := newEnv(t)
	a := e.account("a")
	tx := e.tx(a, "otx1")
	// default (nil) API is the unavailable one; the fake without data says not found
	_, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(tx))
	if !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("unknown to Apple: %v", err)
	}
	e.statusFor(tx, 1)
	e.api.Down = true
	_, err = e.svc.BindTransaction(e.ctx, a.ID, e.sign(tx))
	if !control.Is(err, billing.ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
	if _, err := e.st.EntitlementByOriginalTx(e.ctx, "otx1"); err == nil {
		t.Fatal("entitlement stored while Apple was down")
	}
	// the default service has the Unavailable API and never reaches anything
	svc2 := billing.NewService(e.ctl, e.svc.V, nil, e.svc.Cfg)
	if _, err := svc2.BindTransaction(e.ctx, a.ID, e.sign(tx)); !control.Is(err, billing.ErrUnavailable) {
		t.Fatalf("default api: %v", err)
	}
	// Apple answers for a different subscription
	e.api.Down = false
	other := e.tx(a, "someone-else")
	e.api.SetStatus("otx1", billing.SubscriptionStatus{Status: 1, Transaction: other})
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(tx)); !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("mismatch: %v", err)
	}
}

func TestBindUsesAuthoritativeStatusNotTheAppsBlob(t *testing.T) {
	e := newEnv(t)
	a := e.account("a")
	// the app sends a transaction that looks like a long active subscription ...
	claimed := e.tx(a, "otx1", func(x *billing.TransactionInfo) {
		x.ExpiresDate = billing.MsOf(t0.Add(days(3650)))
		x.OfferType, x.OfferDiscountType = 0, ""
	})
	// ... but Apple says it expired two days ago
	truth := e.tx(a, "otx1", func(x *billing.TransactionInfo) {
		x.ExpiresDate = billing.MsOf(t0.Add(-days(2)))
		x.OfferType, x.OfferDiscountType = 0, ""
	})
	e.statusFor(truth, 2)
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(claimed)); err != nil {
		t.Fatal(err)
	}
	if e.ent("otx1").State != control.EntExpired {
		t.Fatal("app's claim was trusted")
	}
	if p := e.plan(a); p.State == billing.StateActive || p.State == billing.StateTrial {
		t.Fatalf("plan %s", p.State)
	}
}

func TestRefreshReadsEverySubscription(t *testing.T) {
	e := newEnv(t)
	a := e.account("a")
	tx := e.tx(a, "otx1")
	e.statusFor(tx, 1)
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(tx)); err != nil {
		t.Fatal(err)
	}
	// Apple now says the payment failed (grace)
	g := e.tx(a, "otx1", func(x *billing.TransactionInfo) {
		x.SignedDate = billing.MsOf(t0.Add(days(15)))
		x.OfferType, x.OfferDiscountType = 0, ""
	})
	e.api.SetStatus("otx1", billing.SubscriptionStatus{Status: 4, Transaction: g, Renewal: *ren(1, func(r *billing.RenewalInfo) { r.GracePeriodExpiresDate = billing.MsOf(t0.Add(days(30))) }), Environment: "Sandbox"})
	n, err := e.svc.Refresh(e.ctx, a.ID)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if e.ent("otx1").State != control.EntGrace {
		t.Fatal("not refreshed")
	}
	// an older snapshot never overwrites newer data
	old := e.tx(a, "otx1", func(x *billing.TransactionInfo) { x.SignedDate = billing.MsOf(t0) })
	e.api.SetStatus("otx1", billing.SubscriptionStatus{Status: 1, Transaction: old, Renewal: *ren(1)})
	if err := e.svc.ReconcileOriginal(e.ctx, "otx1"); err != nil {
		t.Fatal(err)
	}
	if e.ent("otx1").State != control.EntGrace {
		t.Fatal("stale snapshot applied")
	}
	// Refresh with Apple down surfaces the error
	e.api.Down = true
	if _, err := e.svc.Refresh(e.ctx, a.ID); !control.Is(err, billing.ErrUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestGuardedAPIDefault(t *testing.T) {
	var api billing.ServerAPI = billing.Unavailable{}
	if _, err := api.GetSubscriptionStatus(nil, "x"); err != billing.ErrAppleUnavailable { //nolint:staticcheck
		t.Fatal(err)
	}
	if _, err := api.GetTransactionHistory(nil, "x"); err != billing.ErrAppleUnavailable { //nolint:staticcheck
		t.Fatal(err)
	}
	if err := api.SetAppAccountToken(nil, "x", "y"); err != billing.ErrAppleUnavailable { //nolint:staticcheck
		t.Fatal(err)
	}
}
