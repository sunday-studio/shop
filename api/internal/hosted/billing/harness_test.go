package billing_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
)

var t0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

const (
	bundle  = "com.sundaystudi.Shop"
	product = "com.sundaystudi.shop.hosted.monthly"
)

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

type env struct {
	t     *testing.T
	ctx   context.Context
	clock *hclock.Fake
	st    *control.Store
	ctl   *control.Service
	calc  *billing.Calculator
	svc   *billing.Service
	api   *billing.FakeAPI
	pki   *hostedtest.PKI
	seq   int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, ctx: context.Background(), clock: hclock.NewFake(t0), api: billing.NewFakeAPI()}
	e.pki = hostedtest.NewPKI(t0.AddDate(-1, 0, 0))
	st, err := control.Open(filepath.Join(t.TempDir(), "c.db"), e.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	e.calc = billing.NewCalculator(st)
	e.ctl = control.NewService(st, control.DefaultConfig(), e.calc.AccessChecker())
	v := &billing.Verifier{Roots: e.pki.Pool, Clock: e.clock, LeafOID: hostedtest.LeafOID, IntermediateOID: hostedtest.IntermediateOID}
	e.svc = billing.NewService(e.ctl, v, e.api, billing.Config{BundleID: bundle, ProductIDs: []string{product}})
	e.svc.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return e
}

func (e *env) account(sub string) control.Account {
	a, _, err := e.ctl.UpsertAccount(e.ctx, sub, "", false, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

// tx builds a trial transaction for the account.
func (e *env) tx(acc control.Account, otx string, mut ...func(*billing.TransactionInfo)) billing.TransactionInfo {
	t := billing.TransactionInfo{
		TransactionID: otx + "-1", OriginalTransactionID: otx, BundleID: bundle, ProductID: product,
		PurchaseDate: billing.MsOf(t0), OriginalPurchaseDate: billing.MsOf(t0), ExpiresDate: billing.MsOf(t0.Add(days(14))),
		Type: "Auto-Renewable Subscription", AppAccountToken: acc.AppAccountToken, Environment: "Sandbox",
		OfferType: 1, OfferDiscountType: "FREE_TRIAL", SignedDate: billing.MsOf(t0),
	}
	for _, m := range mut {
		if m != nil {
			m(&t)
		}
	}
	return t
}

func (e *env) sign(v any) string { return e.pki.Sign(v) }

// notif builds a signed notification for a transaction. signed is the notification's signedDate.
func (e *env) notif(typ, sub, uuid string, signed time.Time, tx billing.TransactionInfo, ren *billing.RenewalInfo) string {
	e.seq++
	data := billing.NotificationData{BundleID: bundle, Environment: "Sandbox", SignedTransactionInfo: e.sign(tx)}
	if ren != nil {
		data.SignedRenewalInfo = e.sign(ren)
	}
	return e.sign(billing.NotificationPayload{NotificationType: typ, Subtype: sub, NotificationUUID: uuid, Version: "2.0", SignedDate: billing.MsOf(signed), Data: data})
}

func (e *env) process(signed string) billing.NotificationResult {
	e.t.Helper()
	r, err := e.svc.ProcessNotification(e.ctx, signed)
	if err != nil {
		e.t.Fatalf("process: %v", err)
	}
	return r
}

func (e *env) plan(acc control.Account) billing.Plan {
	e.t.Helper()
	p, err := e.calc.ForAccount(e.ctx, acc.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) ent(otx string) control.Entitlement {
	e.t.Helper()
	x, err := e.st.EntitlementByOriginalTx(e.ctx, otx)
	if err != nil {
		e.t.Fatal(err)
	}
	return x
}

func ren(autoRenew int, mut ...func(*billing.RenewalInfo)) *billing.RenewalInfo {
	r := &billing.RenewalInfo{AutoRenewStatus: autoRenew, AutoRenewProductID: product}
	for _, m := range mut {
		m(r)
	}
	return r
}

func ptrBool(b bool) *bool { return &b }
