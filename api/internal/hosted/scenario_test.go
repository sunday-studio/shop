package hosted_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/admin"
	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
	"shop/internal/hosted/lifecycle"
	"shop/internal/hosted/limits"
)

const (
	bundle  = "com.sundaystudi.Shop"
	product = "com.sundaystudi.shop.hosted.monthly"
)

var t0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

func day(n int) time.Time { return t0.Add(time.Duration(n) * 24 * time.Hour) }

// world wires packages 1 to 6 together with a fake clock, a fake Apple and a fake eraser.
type world struct {
	t      *testing.T
	ctx    context.Context
	clock  *hclock.Fake
	st     *control.Store
	ctl    *control.Service
	calc   *billing.Calculator
	bill   *billing.Service
	auth   *appleauth.Service
	grants *grants.Service
	meter  *limits.Meter
	life   *lifecycle.Runner
	idp    *hostedtest.FakeIDP
	apple  *appleauth.FakeApple
	api    *billing.FakeAPI
	pki    *hostedtest.PKI
	eraser *lifecycle.FakeEraser
	sink   *lifecycle.MemorySink
	n      int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, ctx: context.Background(), clock: hclock.NewFake(t0), idp: hostedtest.NewFakeIDP(), apple: appleauth.NewFakeApple(),
		api: billing.NewFakeAPI(), eraser: &lifecycle.FakeEraser{}, sink: &lifecycle.MemorySink{}}
	w.pki = hostedtest.NewPKI(t0.AddDate(-1, 0, 0))
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := control.Open(filepath.Join(t.TempDir(), "control.db"), w.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w.st = st
	w.calc = billing.NewCalculator(st)
	w.ctl = control.NewService(st, control.DefaultConfig(), w.calc.AccessChecker())
	v := &billing.Verifier{Roots: w.pki.Pool, Clock: w.clock, LeafOID: hostedtest.LeafOID, IntermediateOID: hostedtest.IntermediateOID}
	w.bill = billing.NewService(w.ctl, v, w.api, billing.Config{BundleID: bundle, ProductIDs: []string{product}})
	w.bill.Log = quiet
	sealer, _ := appleauth.NewAESGCMSealer([]byte("0123456789abcdef0123456789abcdef"))
	keys := &appleauth.KeySet{Fetcher: w.idp, Clock: w.clock}
	w.auth = appleauth.New(w.ctl, &appleauth.Verifier{Keys: keys, Clock: w.clock, Audience: bundle}, w.apple, w.apple, sealer)
	w.auth.Log = quiet
	w.grants = grants.New(w.ctl, grants.DefaultConfig())
	w.meter = limits.NewMeter(st)
	w.life = &lifecycle.Runner{S: st, Ctl: w.ctl, Calc: w.calc, Apple: w.bill, Revoker: w.auth, Eraser: w.eraser,
		Layout: lifecycle.Layout{DataDir: "/data", BackupDir: "/backup", ExportDir: "/exports"}, Sink: w.sink, Cfg: lifecycle.DefaultConfig(), Log: quiet}
	return w
}

type user struct {
	acc   control.Account
	token string
	dev   control.Device
}

// signIn performs the full Sign in with Apple flow for a new or returning user.
func (w *world) signIn(sub, name string) user {
	w.t.Helper()
	nonce, _, err := w.auth.IssueNonce(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	code := "code-" + sub + nonce[:6]
	w.apple.AddCode(code, "refresh-"+sub)
	tok := w.idp.Sign("rsa-1", map[string]any{"iss": appleauth.Issuer, "aud": bundle, "sub": sub, "nonce": appleauth.NonceClaim(nonce),
		"exp": w.clock.Now().Add(10 * time.Minute).Unix(), "iat": w.clock.Now().Unix(), "email": sub + "@privaterelay.appleid.com", "is_private_email": "true"})
	res, err := w.auth.SignIn(w.ctx, appleauth.SignInRequest{IdentityToken: tok, AuthorizationCode: code, Nonce: nonce, DeviceName: name + "'s iPhone", GivenName: name, FamilyName: "Test"})
	if err != nil {
		w.t.Fatalf("sign in %s: %v", sub, err)
	}
	return user{acc: res.Account, token: res.Token, dev: res.Device}
}

func (w *world) tx(u user, otx string, mut ...func(*billing.TransactionInfo)) billing.TransactionInfo {
	t := billing.TransactionInfo{TransactionID: otx + "-x", OriginalTransactionID: otx, BundleID: bundle, ProductID: product,
		OriginalPurchaseDate: billing.MsOf(t0), PurchaseDate: billing.MsOf(w.clock.Now()), ExpiresDate: billing.MsOf(day(14)), Type: "Auto-Renewable Subscription",
		AppAccountToken: u.acc.AppAccountToken, Environment: "Sandbox", OfferType: 1, OfferDiscountType: "FREE_TRIAL", SignedDate: billing.MsOf(w.clock.Now())}
	for _, m := range mut {
		m(&t)
	}
	return t
}

func (w *world) notif(typ, sub, uuid string, signed time.Time, tx billing.TransactionInfo, ren *billing.RenewalInfo) string {
	data := billing.NotificationData{BundleID: bundle, Environment: "Sandbox", SignedTransactionInfo: w.pki.Sign(tx)}
	if ren != nil {
		data.SignedRenewalInfo = w.pki.Sign(ren)
	}
	return w.pki.Sign(billing.NotificationPayload{NotificationType: typ, Subtype: sub, NotificationUUID: uuid, Version: "2.0", SignedDate: billing.MsOf(signed), Data: data})
}

func (w *world) deliver(signed string) billing.NotificationResult {
	w.t.Helper()
	r, err := w.bill.ProcessNotification(w.ctx, signed)
	if err != nil {
		w.t.Fatalf("notification: %v", err)
	}
	return r
}

func (w *world) plan(u user) billing.Plan {
	w.t.Helper()
	p, err := w.calc.ForAccount(w.ctx, u.acc.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) expect(who string, u user, state string, allow, deny []string) billing.Plan {
	w.t.Helper()
	p := w.plan(u)
	if p.State != state {
		w.t.Fatalf("%s at %s: state %s, want %s (%+v)", who, w.clock.Now().Format("2006-01-02"), p.State, state, p)
	}
	for _, a := range allow {
		if !p.Can(a) {
			w.t.Fatalf("%s in %s must allow %s, has %v", who, state, a, p.Allowed)
		}
	}
	for _, a := range deny {
		if p.Can(a) {
			w.t.Fatalf("%s in %s must not allow %s, has %v", who, state, a, p.Allowed)
		}
	}
	return p
}

func (w *world) at(n int) { w.clock.Set(day(n)) }

func (w *world) runLifecycle() lifecycle.Report {
	w.t.Helper()
	r, err := w.life.Run(w.ctx, lifecycle.Options{})
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func ren(autoRenew int, mut ...func(*billing.RenewalInfo)) *billing.RenewalInfo {
	r := &billing.RenewalInfo{AutoRenewStatus: autoRenew, AutoRenewProductID: product}
	for _, m := range mut {
		m(r)
	}
	return r
}

func (w *world) status(otx string, code int, tx billing.TransactionInfo) {
	w.api.SetStatus(otx, billing.SubscriptionStatus{Status: code, Transaction: tx, Renewal: *ren(1), Environment: "Sandbox"})
}

var (
	fullAccess  = []string{"read", "write", "upload", "export"}
	writeAccess = []string{"write", "upload", "invite", "manage_household"}
)

func TestHostedLifecycleScenario(t *testing.T) {
	w := newWorld(t)

	// ---- day 0: Ama signs in, sees the paywall, starts the trial -----------------------------
	ama := w.signIn("sub-ama", "Ama")
	if ama.acc.DisplayName != "Ama T." {
		t.Fatalf("display name %q", ama.acc.DisplayName)
	}
	w.expect("ama before buying", ama, billing.StateNone, []string{"subscribe", "join_household", "delete_account"}, []string{"read", "write", "export"})
	if _, _, err := w.st.HouseholdOfAccount(w.ctx, ama.acc.ID); !control.Is(err, control.ErrNoHousehold) {
		t.Fatal("sign-in must not create a household")
	}

	trialTx := w.tx(ama, "otx-ama")
	w.status("otx-ama", 1, trialTx)
	res, err := w.bill.BindTransaction(w.ctx, ama.acc.ID, w.pki.Sign(trialTx))
	if err != nil || res.Result != billing.ResultBound || res.Household.ID == "" {
		t.Fatalf("bind: %+v %v", res, err)
	}
	hh := res.Household
	p := w.expect("ama in trial", ama, billing.StateTrial, append(fullAccess, "invite", "manage_household", "delete_account"), []string{"subscribe", "leave_household"})
	if p.TrialEndsAt == nil || !p.TrialEndsAt.Equal(day(14)) || !p.IsOwner || p.ReadOnly || *p.EntitlementSource != "apple" || *p.Environment != "sandbox" {
		t.Fatalf("%+v", p)
	}
	// the app can now upload receipts, metered
	if _, err := w.meter.RecordReceipt(w.ctx, hh.ID, 400_000); err != nil {
		t.Fatal(err)
	}

	// ---- an invite fills the household to the cap of 4 ------------------------------------------
	var members []user
	for _, name := range []string{"Kofi", "Esi", "Yaw"} {
		_, code, err := w.ctl.CreateInvite(w.ctx, ama.acc.ID)
		if err != nil {
			t.Fatalf("invite for %s: %v", name, err)
		}
		m := w.signIn("sub-"+strings.ToLower(name), name)
		if _, err := w.ctl.AcceptInvite(w.ctx, m.acc.ID, code); err != nil {
			t.Fatalf("accept %s: %v", name, err)
		}
		members = append(members, m)
	}
	if n, _ := w.st.CountMembers(w.ctx, hh.ID); n != 4 {
		t.Fatalf("members %d", n)
	}
	if _, _, err := w.ctl.CreateInvite(w.ctx, ama.acc.ID); !control.Is(err, control.ErrHouseholdFull) {
		t.Fatalf("5th person: %v", err)
	}
	kofi := members[0]
	mp := w.expect("kofi (member)", kofi, billing.StateTrial, append(fullAccess, "leave_household"), []string{"invite", "manage_household", "subscribe", "cancel_deletion"})
	if mp.IsOwner {
		t.Fatal("member reported as owner")
	}

	// ---- complimentary access for a friend ------------------------------------------------------
	nana := w.signIn("sub-nana", "Nana")
	w.expect("nana before the grant", nana, billing.StateNone, []string{"subscribe"}, []string{"read"})
	g, _, err := w.grants.Create(w.ctx, grants.CreateInput{SupportCode: nana.acc.SupportCode, Reason: "Friend, beta tester", Actor: "casprine"})
	if err != nil || !g.ExpiresAt.Equal(day(90)) {
		t.Fatalf("grant %+v %v", g, err)
	}
	np := w.expect("nana comped", nana, billing.StateComped, append(fullAccess, "invite", "subscribe"), nil)
	if *np.EntitlementSource != "grant" || np.EndsAt == nil || !np.EndsAt.Equal(day(90)) {
		t.Fatalf("%+v", np)
	}

	// ---- a refund (Efua) and its reversal --------------------------------------------------------
	w.at(1)
	efua := w.signIn("sub-efua", "Efua")
	paid := w.tx(efua, "otx-efua", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(day(400))
	})
	w.deliver(w.notif(billing.NtSubscribed, "INITIAL_BUY", "efua-1", day(1), paid, ren(1)))
	w.expect("efua paid", efua, billing.StateActive, writeAccess, nil)
	w.at(5)
	refunded := w.tx(efua, "otx-efua", func(x *billing.TransactionInfo) {
		*x = paid
		x.RevocationDate = billing.MsOf(day(5))
		x.SignedDate = billing.MsOf(day(5))
	})
	w.deliver(w.notif(billing.NtRefund, "", "efua-2", day(5), refunded, ren(0)))
	ep := w.expect("efua refunded", efua, billing.StateLapsedReadOnly, []string{"read", "export", "subscribe"}, []string{"write", "upload", "invite"})
	if ep.DeleteAfter == nil || !ep.DeleteAfter.Equal(day(35)) || ep.ReadOnlySince == nil || !ep.ReadOnlySince.Equal(day(5)) {
		t.Fatalf("%+v", ep)
	}
	w.at(6)
	w.deliver(w.notif(billing.NtRefundReversed, "", "efua-3", day(6), paid, ren(1)))
	w.expect("efua refund reversed", efua, billing.StateActive, writeAccess, nil)

	// ---- day 14: first charge after the trial, with duplicates and out-of-order delivery -----------
	w.at(14)
	renewed := w.tx(ama, "otx-ama", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(day(44))
		x.TransactionID = "otx-ama-2"
		x.SignedDate = billing.MsOf(day(14))
	})
	renewal := w.notif(billing.NtDidRenew, "", "ama-renew-1", day(14), renewed, ren(1))
	if r := w.deliver(renewal); r.Outcome != billing.OutcomeApplied {
		t.Fatalf("%+v", r)
	}
	p = w.expect("ama after first charge", ama, billing.StateActive, writeAccess, nil)
	if p.TrialEndsAt != nil || p.RenewsAt == nil || !p.RenewsAt.Equal(day(44)) {
		t.Fatalf("%+v", p)
	}
	if r := w.deliver(renewal); r.Outcome != billing.OutcomeDuplicate { // Apple redelivers the same notification
		t.Fatalf("%+v", r)
	}
	late := w.notif(billing.NtSubscribed, "INITIAL_BUY", "ama-sub-late", day(0), trialTx, ren(1)) // older one arrives last
	if r := w.deliver(late); r.Outcome != billing.OutcomeStale {
		t.Fatalf("%+v", r)
	}
	if r := w.deliver(late); r.Outcome != billing.OutcomeDuplicate {
		t.Fatalf("%+v", r)
	}
	w.expect("ama still active after stale delivery", ama, billing.StateActive, writeAccess, nil)
	w.expect("kofi follows the owner", kofi, billing.StateActive, fullAccess, []string{"invite"})

	// ---- day 44: the renewal fails: grace, then billing retry ----------------------------------------
	w.at(44)
	failTx := w.tx(ama, "otx-ama", func(x *billing.TransactionInfo) { *x = renewed; x.SignedDate = billing.MsOf(day(44)) })
	w.deliver(w.notif(billing.NtDidFailToRenew, billing.SubGracePeriod, "ama-fail-1", day(44), failTx,
		ren(1, func(r *billing.RenewalInfo) {
			r.GracePeriodExpiresDate = billing.MsOf(day(60))
			r.IsInBillingRetryPeriod = true
		})))
	p = w.expect("ama in grace", ama, billing.StateGrace, append(fullAccess, "invite"), nil)
	if p.GraceUntil == nil || !p.GraceUntil.Equal(day(60)) || p.ReadOnly {
		t.Fatalf("%+v", p)
	}
	w.expect("kofi in grace", kofi, billing.StateGrace, fullAccess, nil)
	if r := w.runLifecycle(); r.Count(lifecycle.ActTombstone)+r.Count(lifecycle.ActReminder) != 0 {
		t.Fatalf("lifecycle acted during grace: %+v", r.Actions)
	}
	w.at(61)
	w.deliver(w.notif(billing.NtGracePeriodExpired, "", "ama-fail-2", day(61), failTx, ren(1)))
	p = w.expect("ama in billing retry", ama, billing.StateBillingRetry, []string{"read", "export", "delete_account"}, []string{"write", "upload", "invite", "subscribe"})
	if !p.ReadOnly || p.DeleteAfter != nil {
		t.Fatalf("billing retry must be read-only with no deletion clock: %+v", p)
	}
	if _, err := w.meter.RecordReceipt(w.ctx, hh.ID, 1); err != nil { // metering is independent of the gate; the plan is what refuses writes
		t.Fatal(err)
	}
	w.at(70)
	if r := w.runLifecycle(); r.Count(lifecycle.ActTombstone) != 0 {
		t.Fatal("billing retry must never start deletion")
	}

	// ---- day 75: Apple gives up: expired => lapsed, read-only plus export ----------------------------------
	w.at(75)
	w.deliver(w.notif(billing.NtExpired, "BILLING_RETRY", "ama-exp", day(75), failTx, ren(0)))
	p = w.expect("ama lapsed", ama, billing.StateLapsedReadOnly, []string{"read", "export", "subscribe"}, []string{"write", "upload", "invite"})
	if p.ReadOnlySince == nil || !p.ReadOnlySince.Equal(day(75)) || p.DeleteAfter == nil || !p.DeleteAfter.Equal(day(105)) {
		t.Fatalf("lapse dates: %+v", p)
	}
	lp := w.expect("kofi lapsed with the household", kofi, billing.StateLapsedReadOnly, []string{"read", "export", "leave_household"}, []string{"write", "subscribe"})
	if lp.IsOwner {
		t.Fatal("member must be told to ask the owner")
	}
	w.runLifecycle()
	if got := w.sink.Kinds(); len(got) != 1 || got[0] != "lapse_day_0" || w.sink.List[0].DeleteAfter != day(105) {
		t.Fatalf("reminders %v", got)
	}
	if h, _ := w.st.HouseholdByID(w.ctx, hh.ID); h.PlanStateCache != billing.StateLapsedReadOnly {
		t.Fatalf("cache %s", h.PlanStateCache)
	}

	// ---- reminders at day 14, 25, 29; the friend's grant ends on day 90 -------------------------------------
	w.at(89)
	w.runLifecycle()
	w.at(90)
	w.expect("nana after the grant", nana, billing.StateLapsedReadOnly, []string{"read", "export"}, []string{"write"})
	w.runLifecycle()
	w.at(100)
	w.runLifecycle()
	w.at(104)
	w.runLifecycle()
	var amaKinds []string
	for _, rem := range w.sink.List {
		if rem.HouseholdID == hh.ID {
			amaKinds = append(amaKinds, rem.Kind)
		}
	}
	if strings.Join(amaKinds, ",") != "lapse_day_0,lapse_day_14,lapse_day_25,lapse_day_29" {
		t.Fatalf("Ama's reminders: %v", amaKinds)
	}
	if w.eraser.Count() != 0 {
		t.Fatal("something was erased before day 30")
	}

	// ---- day 105: pending deletion, stage 1 ------------------------------------------------------------------------
	w.at(105)
	w.status("otx-ama", 2, w.tx(ama, "otx-ama", func(x *billing.TransactionInfo) { *x = failTx; x.SignedDate = billing.MsOf(day(105)) })) // Apple agrees: expired
	r := w.runLifecycle()
	if r.Count(lifecycle.ActTombstone) != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	p = w.expect("ama pending deletion", ama, billing.StatePendingDeletion, []string{"read", "export", "subscribe"}, []string{"write", "upload", "invite"})
	if p.DeleteAfter == nil || !p.DeleteAfter.Equal(day(107)) {
		t.Fatalf("%+v", p)
	}
	if h, _ := w.st.HouseholdByID(w.ctx, hh.ID); h.Status != control.HouseholdTombstoned {
		t.Fatalf("status %s", h.Status)
	}
	w.expect("kofi pending deletion", kofi, billing.StatePendingDeletion, []string{"read", "export"}, []string{"write"})

	// ---- day 107: stage 2, erased -------------------------------------------------------------------------------------
	w.at(107)
	r = w.runLifecycle()
	if r.Count(lifecycle.ActErase) != 1 || w.eraser.Count() != 1 || w.eraser.Erased[0].HouseholdID != hh.ID {
		t.Fatalf("%+v", r.Actions)
	}
	w.expect("ama deleted", ama, billing.StateDeleted, []string{"subscribe", "join_household", "delete_account"}, []string{"read", "export", "write"})
	w.expect("kofi without a household", kofi, billing.StateNone, []string{"join_household"}, []string{"read"})
	if a, _ := w.st.AccountByID(w.ctx, ama.acc.ID); a.Status != control.AccountActive {
		t.Fatal("the account row stays until the user deletes it")
	}
	// the other households are untouched
	w.expect("efua unaffected", efua, billing.StateActive, writeAccess, nil)
	w.expect("nana unaffected (lapsed since day 90, not yet due)", nana, billing.StateLapsedReadOnly, []string{"read", "export"}, nil)
	if r := w.runLifecycle(); len(r.Actions) != 0 && (r.Count(lifecycle.ActErase) != 0 || r.Count(lifecycle.ActTombstone) != 0) {
		t.Fatalf("not idempotent: %+v", r.Actions)
	}

	// ---- resubscribing after deletion starts an empty household ------------------------------------------------------------
	w.at(110)
	again := w.tx(ama, "otx-ama-new", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(day(140))
	})
	w.status("otx-ama-new", 1, again)
	res, err = w.bill.BindTransaction(w.ctx, ama.acc.ID, w.pki.Sign(again))
	if err != nil || res.Household.ID == hh.ID || res.Household.ID == "" {
		t.Fatalf("%+v %v", res, err)
	}
	w.expect("ama subscribed again", ama, billing.StateActive, writeAccess, nil)
	if n, _ := w.st.CountMembers(w.ctx, res.Household.ID); n != 1 {
		t.Fatalf("new household has %d members", n)
	}

	// ---- audit trail ----------------------------------------------------------------------------------------------------------
	for _, action := range []string{"grant.create", "lifecycle.tombstone", "lifecycle.erase", "household.create", "invite.accept"} {
		if au, _ := w.st.ListAudit(w.ctx, control.AuditFilter{Action: action}); len(au) == 0 {
			t.Errorf("no audit row for %s", action)
		}
	}
}

// The admin CLI drives the same grants and shows the same plan.
func TestScenarioGrantThroughAdminCLI(t *testing.T) {
	w := newWorld(t)
	friend := w.signIn("sub-friend", "Friend")
	var out, errb strings.Builder
	env := admin.Env{Ctl: w.ctl, Grants: w.grants, Calc: w.calc, Lifecycle: w.life, Operator: "casprine", Out: &out, Err: &errb}
	if code := admin.Run(w.ctx, []string{"grant", "create", "--support-code", friend.acc.SupportCode, "--reason", "tester", "--days", "30"}, env); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	w.expect("friend comped", friend, billing.StateComped, writeAccess, nil)
	w.at(31)
	w.expect("friend after 30 days", friend, billing.StateLapsedReadOnly, []string{"read", "export"}, []string{"write"})
	// a new grant while lapsed cancels the clock
	if code := admin.Run(w.ctx, []string{"grant", "create", "--account-id", friend.acc.ID, "--reason", "more testing"}, env); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	w.expect("friend comped again", friend, billing.StateComped, writeAccess, nil)
}

// Sandbox traffic is accepted and flagged; a household that stays unpaid is never deleted by a
// stale local view: Apple's fresh answer wins.
func TestScenarioFreshAppleCheckSavesAHousehold(t *testing.T) {
	w := newWorld(t)
	u := w.signIn("sub-u", "Una")
	tx := w.tx(u, "otx-u", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(day(30))
	})
	w.status("otx-u", 1, tx)
	if _, err := w.bill.BindTransaction(w.ctx, u.acc.ID, w.pki.Sign(tx)); err != nil {
		t.Fatal(err)
	}
	// the EXPIRED notification arrived, but the user had in fact renewed and the renewal notification was lost
	w.at(31)
	w.deliver(w.notif(billing.NtExpired, "VOLUNTARY", "u-exp", day(31), tx, ren(0)))
	w.expect("lapsed", u, billing.StateLapsedReadOnly, []string{"read"}, []string{"write"})
	w.at(61)
	renewed := w.tx(u, "otx-u", func(x *billing.TransactionInfo) {
		x.OfferType, x.OfferDiscountType = 0, ""
		x.ExpiresDate = billing.MsOf(day(91))
		x.SignedDate = billing.MsOf(day(61))
	})
	w.status("otx-u", 1, renewed)
	r := w.runLifecycle()
	if r.Count(lifecycle.ActTombstone) != 0 || w.eraser.Count() != 0 {
		t.Fatalf("deleted a paying customer: %+v", r.Actions)
	}
	w.expect("recovered by the fresh check", u, billing.StateActive, writeAccess, nil)
}
