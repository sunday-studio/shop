package billing_test

import (
	"errors"
	"strings"
	"testing"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
)

func TestSubscribedTrialCreatesEntitlementAndHousehold(t *testing.T) {
	e := newEnv(t)
	a := e.account("ama")
	tx := e.tx(a, "otx1")
	r := e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "n1", t0, tx, ren(1)))
	if r.Outcome != billing.OutcomeApplied || r.AccountID != a.ID {
		t.Fatalf("%+v", r)
	}
	ent := e.ent("otx1")
	if ent.State != control.EntActive || !ent.IsTrial || ent.AccountID != a.ID || ent.Environment != "sandbox" || ent.ProductID != product || ent.AutoRenew == nil || !*ent.AutoRenew {
		t.Fatalf("%+v", ent)
	}
	if _, _, err := e.st.HouseholdOfAccount(e.ctx, a.ID); err != nil {
		t.Fatalf("household not created: %v", err)
	}
	p := e.plan(a)
	if p.State != billing.StateTrial || p.TrialEndsAt == nil || !p.TrialEndsAt.Equal(t0.Add(days(14))) || p.ReadOnly || p.Environment == nil || *p.Environment != "sandbox" {
		t.Fatalf("plan %+v", p)
	}
}

func TestSubscribedWithoutTrialIsActive(t *testing.T) {
	e := newEnv(t)
	a := e.account("ama")
	tx := e.tx(a, "otx1", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(t0.Add(days(30)))
	})
	e.process(e.notif(billing.NtSubscribed, "RESUBSCRIBE", "n1", t0, tx, ren(1)))
	if p := e.plan(a); p.State != billing.StateActive || p.TrialEndsAt != nil || p.RenewsAt == nil {
		t.Fatalf("%+v", p)
	}
}

func TestDuplicateNotificationIsNoop(t *testing.T) {
	e := newEnv(t)
	a := e.account("ama")
	tx := e.tx(a, "otx1")
	n := e.notif(billing.NtSubscribed, "INITIAL_BUY", "n1", t0, tx, ren(1))
	e.process(n)
	before := e.ent("otx1")
	r := e.process(n)
	if r.Outcome != billing.OutcomeDuplicate {
		t.Fatalf("%+v", r)
	}
	if after := e.ent("otx1"); !after.UpdatedAt.Equal(before.UpdatedAt) || after.State != before.State {
		t.Fatal("duplicate changed state")
	}
}

func TestOutOfOrderOlderNotificationIsStale(t *testing.T) {
	e := newEnv(t)
	a := e.account("ama")
	trial := e.tx(a, "otx1")
	renewed := e.tx(a, "otx1", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(t0.Add(days(44)))
		x.TransactionID = "otx1-2"
	})
	// the renewal (later signedDate) arrives first, the SUBSCRIBED (earlier) afterwards
	e.process(e.notif(billing.NtDidRenew, "", "n2", t0.Add(days(14)), renewed, ren(1)))
	r := e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "n1", t0, trial, ren(1)))
	if r.Outcome != billing.OutcomeStale {
		t.Fatalf("%+v", r)
	}
	ent := e.ent("otx1")
	if ent.IsTrial || !ent.ExpiresAt.Equal(t0.Add(days(44))) || ent.LatestTransactionID != "otx1-2" {
		t.Fatalf("stale notification overwrote newer data: %+v", ent)
	}
	// it is recorded, so a redelivery is a duplicate
	if r := e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "n1", t0, trial, ren(1))); r.Outcome != billing.OutcomeDuplicate {
		t.Fatalf("%+v", r)
	}
	rec, _ := e.st.NotificationByUUID(e.ctx, "n1")
	if rec.Result != billing.OutcomeStale {
		t.Fatalf("recorded result %q", rec.Result)
	}
}

func TestNotificationMappingTable(t *testing.T) {
	type step struct {
		typ, sub string
		at       int // days after t0 (signedDate)
		mut      func(*billing.TransactionInfo)
		ren      *billing.RenewalInfo
		wantEnt  string
	}
	renewed := func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(t0.Add(days(44)))
	}
	graceUntil := billing.MsOf(t0.Add(days(14 + 16)))
	cases := []struct {
		name  string
		steps []step
		want  string
		check func(t *testing.T, ent control.Entitlement)
	}{
		{"renew", []step{{billing.NtDidRenew, "", 14, renewed, ren(1), control.EntActive}}, control.EntActive,
			func(t *testing.T, ent control.Entitlement) {
				if ent.IsTrial {
					t.Error("renewal still trial")
				}
			}},
		{"fail to renew in grace", []step{{billing.NtDidFailToRenew, billing.SubGracePeriod, 15, nil, ren(1, func(r *billing.RenewalInfo) { r.GracePeriodExpiresDate = graceUntil }), control.EntGrace}}, control.EntGrace,
			func(t *testing.T, ent control.Entitlement) {
				if !ent.GraceUntil.Equal(t0.Add(days(30))) {
					t.Errorf("grace until %v", ent.GraceUntil)
				}
			}},
		{"fail to renew without grace", []step{{billing.NtDidFailToRenew, "", 15, nil, ren(1), control.EntBillingRetry}}, control.EntBillingRetry, nil},
		{"grace then expired grace", []step{
			{billing.NtDidFailToRenew, billing.SubGracePeriod, 15, nil, ren(1, func(r *billing.RenewalInfo) { r.GracePeriodExpiresDate = graceUntil }), control.EntGrace},
			{billing.NtGracePeriodExpired, "", 31, nil, ren(1), control.EntBillingRetry}}, control.EntBillingRetry, nil},
		{"grace then recovered", []step{
			{billing.NtDidFailToRenew, billing.SubGracePeriod, 15, nil, ren(1, func(r *billing.RenewalInfo) { r.GracePeriodExpiresDate = graceUntil }), control.EntGrace},
			{billing.NtDidRenew, "BILLING_RECOVERY", 17, renewed, ren(1), control.EntActive}}, control.EntActive,
			func(t *testing.T, ent control.Entitlement) {
				if !ent.GraceUntil.IsZero() {
					t.Error("grace not cleared")
				}
			}},
		{"expired", []step{{billing.NtExpired, "VOLUNTARY", 14, nil, ren(0), control.EntExpired}}, control.EntExpired,
			func(t *testing.T, ent control.Entitlement) {
				if ent.EndedAt.IsZero() {
					t.Error("ended_at not set")
				}
			}},
		{"refund", []step{{billing.NtRefund, "", 3, func(x *billing.TransactionInfo) { x.RevocationDate = billing.MsOf(t0.Add(days(3))) }, ren(0), control.EntRevoked}}, control.EntRevoked,
			func(t *testing.T, ent control.Entitlement) {
				if !ent.RevokedAt.Equal(t0.Add(days(3))) {
					t.Errorf("revoked at %v", ent.RevokedAt)
				}
			}},
		{"refund reversed", []step{
			{billing.NtRefund, "", 3, func(x *billing.TransactionInfo) { x.RevocationDate = billing.MsOf(t0.Add(days(3))) }, ren(0), control.EntRevoked},
			{billing.NtRefundReversed, "", 4, nil, ren(1), control.EntActive}}, control.EntActive,
			func(t *testing.T, ent control.Entitlement) {
				if !ent.RevokedAt.IsZero() {
					t.Error("revocation not cleared")
				}
			}},
		{"revoke", []step{{billing.NtRevoke, "", 5, nil, ren(0), control.EntRevoked}}, control.EntRevoked, nil},
		{"auto renew off", []step{{billing.NtDidChangeRenewalStatus, billing.SubAutoRenewDisabled, 2, nil, nil, control.EntActive}}, control.EntActive,
			func(t *testing.T, ent control.Entitlement) {
				if ent.AutoRenew == nil || *ent.AutoRenew {
					t.Error("auto_renew not off")
				}
			}},
		{"price increase no-op", []step{{billing.NtPriceIncrease, "ACCEPTED", 2, nil, ren(1), control.EntActive}}, control.EntActive, nil},
		{"renewal pref no-op", []step{{billing.NtDidChangeRenewalPref, "UPGRADE", 2, nil, ren(1), control.EntActive}}, control.EntActive, nil},
		{"offer redeemed", []step{{billing.NtOfferRedeemed, "", 2, func(x *billing.TransactionInfo) { x.OfferType = 3; x.OfferIdentifier = "friends" }, ren(1), control.EntActive}}, control.EntActive,
			func(t *testing.T, ent control.Entitlement) {
				if ent.OfferID != "friends" || ent.OfferType != 3 {
					t.Errorf("offer fields %+v", ent)
				}
			}},
		{"renewal extended", []step{{billing.NtRenewalExtended, "", 2, func(x *billing.TransactionInfo) { x.ExpiresDate = billing.MsOf(t0.Add(days(20))) }, ren(1), control.EntActive}}, control.EntActive,
			func(t *testing.T, ent control.Entitlement) {
				if !ent.ExpiresAt.Equal(t0.Add(days(20))) {
					t.Errorf("expires %v", ent.ExpiresAt)
				}
			}},
		{"refund declined no-op", []step{{billing.NtRefundDeclined, "", 2, nil, nil, control.EntActive}}, control.EntActive, nil},
		{"consumption request no-op", []step{{billing.NtConsumptionRequest, "", 2, nil, nil, control.EntActive}}, control.EntActive, nil},
		{"unknown type no-op", []step{{"SOMETHING_NEW", "", 2, nil, nil, control.EntActive}}, control.EntActive, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			a := e.account("ama")
			e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "sub", t0, e.tx(a, "otx"), ren(1)))
			for i, s := range c.steps {
				tx := e.tx(a, "otx", s.mut)
				tx.SignedDate = billing.MsOf(t0.Add(days(s.at)))
				e.process(e.notif(s.typ, s.sub, "u"+string(rune('a'+i)), t0.Add(days(s.at)), tx, s.ren))
			}
			ent := e.ent("otx")
			if ent.State != c.want {
				t.Fatalf("state %s, want %s", ent.State, c.want)
			}
			if c.check != nil {
				c.check(t, ent)
			}
		})
	}
}

func TestTestNotificationAndOtherBundleAreIgnored(t *testing.T) {
	e := newEnv(t)
	// TEST: no transaction info
	testN := e.sign(billing.NotificationPayload{NotificationType: billing.NtTest, NotificationUUID: "t1", SignedDate: billing.MsOf(t0), Data: billing.NotificationData{BundleID: bundle, Environment: "Sandbox"}})
	r := e.process(testN)
	if r.Outcome != billing.OutcomeRecorded {
		t.Fatalf("%+v", r)
	}
	// other bundle id: nothing recorded at all
	a := e.account("ama")
	tx := e.tx(a, "otx", func(x *billing.TransactionInfo) { x.BundleID = "com.other.app" })
	n := e.sign(billing.NotificationPayload{NotificationType: billing.NtSubscribed, NotificationUUID: "o1", SignedDate: billing.MsOf(t0),
		Data: billing.NotificationData{BundleID: "com.other.app", Environment: "Sandbox", SignedTransactionInfo: e.sign(tx)}})
	r = e.process(n)
	if r.Outcome != billing.OutcomeIgnored {
		t.Fatalf("%+v", r)
	}
	if _, err := e.st.EntitlementByOriginalTx(e.ctx, "otx"); !control.Is(err, control.ErrNotFound) {
		t.Fatal("foreign bundle created an entitlement")
	}
	// unknown product id: ignored, recorded
	tx2 := e.tx(a, "otx2", func(x *billing.TransactionInfo) { x.ProductID = "com.sundaystudi.other" })
	r = e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "p1", t0, tx2, nil))
	if r.Outcome != billing.OutcomeIgnored {
		t.Fatalf("%+v", r)
	}
}

func TestInvalidSignatureIsRejectedAndNothingRecorded(t *testing.T) {
	e := newEnv(t)
	a := e.account("ama")
	n := e.notif(billing.NtSubscribed, "INITIAL_BUY", "bad1", t0, e.tx(a, "otx"), nil)
	parts := strings.Split(n, ".")
	bad := parts[0] + "." + parts[1] + "A." + parts[2]
	_, err := e.svc.ProcessNotification(e.ctx, bad)
	if !errors.Is(err, billing.ErrNotificationInvalid) || !control.Is(err, billing.ErrNotificationInvalid) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.st.NotificationByUUID(e.ctx, "bad1"); !control.Is(err, control.ErrNotFound) {
		t.Fatal("bad notification recorded")
	}
	// a notification whose embedded transaction is signed by a stranger is refused as well
	foreignTx := billing.TransactionInfo{OriginalTransactionID: "x", BundleID: bundle, ProductID: product}
	other := newEnv(t)
	n2 := e.sign(billing.NotificationPayload{NotificationType: billing.NtSubscribed, NotificationUUID: "bad2", SignedDate: billing.MsOf(t0),
		Data: billing.NotificationData{BundleID: bundle, SignedTransactionInfo: other.sign(foreignTx)}})
	if _, err := e.svc.ProcessNotification(e.ctx, n2); !control.Is(err, billing.ErrNotificationInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestUnclaimedThenClaimedOfferCode(t *testing.T) {
	e := newEnv(t)
	a := e.account("friend")
	// offer code redeemed in the App Store: no appAccountToken in the transaction
	tx := e.tx(a, "otx-oc", func(x *billing.TransactionInfo) {
		x.AppAccountToken = ""
		x.OfferType = 3
		x.OfferDiscountType = "FREE_TRIAL"
		x.ExpiresDate = billing.MsOf(t0.Add(days(30)))
	})
	r := e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "oc1", t0, tx, ren(1)))
	if r.Outcome != billing.OutcomeApplied || r.AccountID != "" {
		t.Fatalf("%+v", r)
	}
	ent := e.ent("otx-oc")
	if ent.AccountID != "" {
		t.Fatal("must stay unclaimed")
	}
	if p := e.plan(a); p.State != billing.StateNone {
		t.Fatalf("plan before claim: %s", p.State)
	}
	// the signed-in app posts the transaction: claimed, bound through Apple
	e.api.SetStatus("otx-oc", billing.SubscriptionStatus{Status: 1, Transaction: tx, Renewal: *ren(1), Environment: "Sandbox"})
	signed := e.sign(tx)
	res, err := e.svc.BindTransaction(e.ctx, a.ID, signed)
	if err != nil || res.Result != billing.ResultClaimed {
		t.Fatalf("%+v %v", res, err)
	}
	if e.api.Tokens["otx-oc"] != a.AppAccountToken {
		t.Fatal("Set App Account Token not called with the account's token")
	}
	if p := e.plan(a); p.State != billing.StateTrial {
		t.Fatalf("plan after claim: %s", p.State)
	}
	// later notifications now carry the token and keep the binding
	tx2 := e.tx(a, "otx-oc", func(x *billing.TransactionInfo) {
		x.OfferType = 0
		x.OfferDiscountType = ""
		x.ExpiresDate = billing.MsOf(t0.Add(days(60)))
	})
	e.process(e.notif(billing.NtDidRenew, "", "oc2", t0.Add(days(30)), tx2, ren(1)))
	if e.ent("otx-oc").AccountID != a.ID {
		t.Fatal("binding lost")
	}
}

func TestNotificationBindsByTokenWhenEntitlementIsUnclaimed(t *testing.T) {
	e := newEnv(t)
	a := e.account("late")
	noToken := e.tx(a, "otx", func(x *billing.TransactionInfo) { x.AppAccountToken = "" })
	e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "u1", t0, noToken, ren(1)))
	withToken := e.tx(a, "otx", func(x *billing.TransactionInfo) {
		x.OfferType = 0
		x.OfferDiscountType = ""
		x.ExpiresDate = billing.MsOf(t0.Add(days(44)))
	})
	e.process(e.notif(billing.NtDidRenew, "", "u2", t0.Add(days(14)), withToken, ren(1)))
	if e.ent("otx").AccountID != a.ID {
		t.Fatal("not bound by token")
	}
	if p := e.plan(a); p.State != billing.StateActive {
		t.Fatalf("%s", p.State)
	}
}

func TestLockedAccountDoesNotFailNotification(t *testing.T) {
	e := newEnv(t)
	a := e.account("locked")
	e.st.SetAccountStatus(e.ctx, a.ID, control.AccountLocked)
	r, err := e.svc.ProcessNotification(e.ctx, e.notif(billing.NtSubscribed, "INITIAL_BUY", "l1", t0, e.tx(a, "otx"), ren(1)))
	if err != nil || r.Outcome != billing.OutcomeApplied {
		t.Fatalf("%+v %v", r, err)
	}
	if _, _, err := e.st.HouseholdOfAccount(e.ctx, a.ID); err == nil {
		t.Fatal("household created for a locked account")
	}
}

func TestEndedAtDoesNotSlideOnLaterEvents(t *testing.T) {
	e := newEnv(t)
	a := e.account("ama")
	e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "s", t0, e.tx(a, "otx"), ren(1)))
	e.process(e.notif(billing.NtExpired, "VOLUNTARY", "x1", t0.Add(days(14)), e.tx(a, "otx"), ren(0)))
	first := e.ent("otx").EndedAt
	// a reconcile a week later says expired again (status 2)
	e.api.SetStatus("otx", billing.SubscriptionStatus{Status: 2, Transaction: func() billing.TransactionInfo {
		x := e.tx(a, "otx")
		x.SignedDate = billing.MsOf(t0.Add(days(21)))
		return x
	}(), Renewal: *ren(0), Environment: "Sandbox"})
	if err := e.svc.ReconcileOriginal(e.ctx, "otx"); err != nil {
		t.Fatal(err)
	}
	if got := e.ent("otx").EndedAt; !got.Equal(first) {
		t.Fatalf("lapse clock slid from %v to %v", first, got)
	}
}

// A production server must not give paid access for sandbox purchases (TestFlight, App Review and
// sandbox testers buy for free; Apple signs their notifications like any other).
func TestProductionOnlyIgnoresSandboxData(t *testing.T) {
	e := newEnv(t)
	e.svc.Cfg.ProductionOnly = true
	a := e.account("ama")

	sandbox := e.tx(a, "otx-sb") // helper builds Environment "Sandbox"
	r := e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "n-sb", t0, sandbox, ren(1)))
	if r.Outcome != billing.OutcomeIgnored {
		t.Fatalf("sandbox notification: %+v", r)
	}
	if _, err := e.st.EntitlementByOriginalTx(e.ctx, "otx-sb"); !errors.Is(err, control.ErrNotFound) {
		t.Fatalf("a sandbox notification created an entitlement: %v", err)
	}
	if p := e.plan(a); p.State != billing.StateNone {
		t.Fatalf("plan %s, want none", p.State)
	}

	// a signed sandbox transaction handed in by the app is refused too
	_, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(sandbox))
	if !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("bind sandbox transaction: %v", err)
	}

	// production data still works
	prod := e.tx(a, "otx-prod", func(x *billing.TransactionInfo) { x.Environment = "Production" })
	signed := e.sign(billing.NotificationPayload{NotificationType: billing.NtSubscribed, Subtype: "INITIAL_BUY", NotificationUUID: "n-prod", Version: "2.0",
		SignedDate: billing.MsOf(t0), Data: billing.NotificationData{BundleID: bundle, Environment: "Production", SignedTransactionInfo: e.sign(prod), SignedRenewalInfo: e.sign(ren(1))}})
	if r := e.process(signed); r.Outcome != billing.OutcomeApplied {
		t.Fatalf("production notification: %+v", r)
	}
	if p := e.plan(a); p.State != billing.StateTrial {
		t.Fatalf("plan %s, want trial", p.State)
	}
}
