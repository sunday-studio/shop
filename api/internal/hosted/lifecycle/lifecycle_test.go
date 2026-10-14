package lifecycle_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
	"shop/internal/hosted/lifecycle"
)

var t0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

type retrier struct {
	calls int
	st    *control.Store
}

// RetryRevocations pretends Apple accepted every queued revocation.
func (r *retrier) RetryRevocations(ctx context.Context) (int, int, error) {
	r.calls++
	pend, _ := r.st.PendingRevocations(ctx)
	for _, p := range pend {
		r.st.FinishRevocation(ctx, p.AccountID, "")
	}
	return len(pend), 0, nil
}

type env struct {
	t      *testing.T
	ctx    context.Context
	clock  *hclock.Fake
	st     *control.Store
	ctl    *control.Service
	calc   *billing.Calculator
	bill   *billing.Service
	api    *billing.FakeAPI
	eraser *lifecycle.FakeEraser
	sink   *lifecycle.MemorySink
	rt     *retrier
	run    *lifecycle.Runner
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, ctx: context.Background(), clock: hclock.NewFake(t0), api: billing.NewFakeAPI(), eraser: &lifecycle.FakeEraser{}, sink: &lifecycle.MemorySink{}, rt: &retrier{}}
	st, err := control.Open(filepath.Join(t.TempDir(), "c.db"), e.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	e.rt.st = st
	e.calc = billing.NewCalculator(st)
	e.ctl = control.NewService(st, control.DefaultConfig(), e.calc.AccessChecker())
	pki := hostedtest.NewPKI(t0.AddDate(-1, 0, 0))
	e.bill = billing.NewService(e.ctl, &billing.Verifier{Roots: pki.Pool, Clock: e.clock}, e.api, billing.Config{BundleID: "b", ProductIDs: []string{"p"}})
	e.bill.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	e.run = &lifecycle.Runner{S: st, Ctl: e.ctl, Calc: e.calc, Apple: e.bill, Revoker: e.rt, Eraser: e.eraser,
		Layout: lifecycle.Layout{DataDir: "/data", BackupDir: "/backup", ExportDir: "/exports"}, Sink: e.sink, Cfg: lifecycle.DefaultConfig(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return e
}

func (e *env) account(sub string) control.Account {
	a, _, err := e.ctl.UpsertAccount(e.ctx, sub, sub+"@example.com", false, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

// appleSub stores an Apple entitlement and teaches the fake Apple the same answer.
func (e *env) appleSub(a control.Account, otx, state string, expires time.Time, mut ...func(*control.Entitlement)) control.Entitlement {
	ent := control.Entitlement{AccountID: a.ID, Source: control.SourceApple, State: state, StartsAt: t0, ExpiresAt: expires, OriginalTransactionID: otx, ProductID: "p", Environment: "sandbox", LastSignedMs: t0.UnixMilli()}
	if state == control.EntExpired {
		ent.EndedAt = expires
	}
	for _, m := range mut {
		m(&ent)
	}
	ent, err := e.st.UpsertEntitlement(e.ctx, ent)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, _, err := e.ctl.EnsureHousehold(e.ctx, a.ID); err != nil {
		e.t.Fatal(err)
	}
	e.setApple(otx, state, expires)
	return ent
}

func (e *env) setApple(otx, state string, expires time.Time) {
	code := map[string]int{control.EntActive: 1, control.EntExpired: 2, control.EntBillingRetry: 3, control.EntGrace: 4, control.EntRevoked: 5}[state]
	e.api.SetStatus(otx, billing.SubscriptionStatus{Status: code, Environment: "Sandbox", Transaction: billing.TransactionInfo{
		OriginalTransactionID: otx, ProductID: "p", ExpiresDate: billing.MsOf(expires), SignedDate: billing.MsOf(e.clock.Now().Add(time.Hour))}})
}

func (e *env) runNow(dry bool) lifecycle.Report {
	e.t.Helper()
	r, err := e.run.Run(e.ctx, lifecycle.Options{DryRun: dry})
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) household(a control.Account) control.Household {
	h, _, err := e.st.HouseholdOfAccount(e.ctx, a.ID)
	if err != nil {
		hh, err2 := e.st.LatestOwnedHousehold(e.ctx, a.ID)
		if err2 != nil {
			e.t.Fatal(err)
		}
		return hh
	}
	return h
}

func (e *env) state(a control.Account) string {
	p, err := e.calc.ForAccount(e.ctx, a.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return p.State
}

// lapsedOwner returns an owner whose Apple subscription expired at t0 (the lapse starts at t0).
func (e *env) lapsedOwner() (control.Account, control.Household) {
	a := e.account("owner")
	e.appleSub(a, "otx", control.EntExpired, t0)
	return a, e.household(a)
}

func TestPlanCacheAndTransitionsAreRecorded(t *testing.T) {
	e := newEnv(t)
	a := e.account("owner")
	e.appleSub(a, "otx", control.EntActive, t0.Add(days(30)), func(x *control.Entitlement) { x.IsTrial = true })
	h := e.household(a)
	r := e.runNow(false)
	if r.Count(lifecycle.ActPlanCache) != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	if got := e.household(a); got.PlanStateCache != billing.StateTrial {
		t.Fatalf("cache %q", got.PlanStateCache)
	}
	// nothing changed => nothing to do
	if r := e.runNow(false); len(r.Actions) != 0 {
		t.Fatalf("second run not idempotent: %+v", r.Actions)
	}
	// time passes: the subscription period ends without a renewal
	e.clock.Advance(days(31))
	r = e.runNow(false)
	if r.Count(lifecycle.ActPlanCache) != 1 || e.household(a).PlanStateCache != billing.StateLapsedReadOnly {
		t.Fatalf("%+v cache=%s", r.Actions, e.household(a).PlanStateCache)
	}
	au, _ := e.st.ListAudit(e.ctx, control.AuditFilter{Subject: h.ID, Action: "lifecycle.plan_state"})
	if len(au) != 2 {
		t.Fatalf("audit rows %d", len(au))
	}
	if got := e.household(a); got.ReadOnlySince.IsZero() || !got.DeleteAfter.Equal(t0.Add(days(30)).Add(days(30))) {
		t.Fatalf("dates %v %v", got.ReadOnlySince, got.DeleteAfter)
	}
	// revocation retries are part of every run
	if e.rt.calls != 0 {
		t.Fatal("retrier called without pending revocations")
	}
}

func TestRemindersDay0_14_25_29OnceEach(t *testing.T) {
	e := newEnv(t)
	a, h := e.lapsedOwner()
	e.runNow(false)
	if got := e.sink.Kinds(); len(got) != 1 || got[0] != "lapse_day_0" {
		t.Fatalf("day 0: %v", got)
	}
	rem := e.sink.List[0]
	if rem.HouseholdID != h.ID || rem.OwnerAccountID != a.ID || rem.Email != "owner@example.com" || !rem.EmailDeliverable || !rem.DeleteAfter.Equal(t0.Add(days(30))) {
		t.Fatalf("%+v", rem)
	}
	e.runNow(false) // same day again: nothing new
	e.clock.Advance(days(13))
	e.runNow(false)
	if len(e.sink.List) != 1 {
		t.Fatalf("day 13: %v", e.sink.Kinds())
	}
	for _, step := range []struct {
		day  int
		want string
	}{{14, "lapse_day_14"}, {25, "lapse_day_25"}, {29, "lapse_day_29"}} {
		e.clock.Set(t0.Add(days(step.day)))
		e.runNow(false)
		e.runNow(false)
		if got := e.sink.Kinds(); got[len(got)-1] != step.want {
			t.Fatalf("day %d: %v", step.day, got)
		}
	}
	if got := e.sink.Kinds(); len(got) != 4 {
		t.Fatalf("%v", got)
	}
}

func TestMissedRemindersSendOnlyTheLatest(t *testing.T) {
	e := newEnv(t)
	e.lapsedOwner()
	e.clock.Set(t0.Add(days(26))) // the job was down for 26 days
	r := e.runNow(false)
	if got := e.sink.Kinds(); len(got) != 1 || got[0] != "lapse_day_25" {
		t.Fatalf("%v", got)
	}
	if r.Count(lifecycle.ActReminderSkip) != 2 {
		t.Fatalf("%+v", r.Actions)
	}
	e.runNow(false)
	if len(e.sink.List) != 1 {
		t.Fatal("older reminders came back")
	}
}

func TestReminderFailureIsRetried(t *testing.T) {
	e := newEnv(t)
	e.lapsedOwner()
	e.sink.Fail = errors.New("mailer down")
	r := e.runNow(false)
	if r.Count(lifecycle.ActError) != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	e.sink.Fail = nil
	e.runNow(false)
	if got := e.sink.Kinds(); len(got) != 1 {
		t.Fatalf("%v", got)
	}
}

func TestUndeliverableEmailIsFlagged(t *testing.T) {
	e := newEnv(t)
	a, _ := e.lapsedOwner()
	e.st.SetEmailDeliverable(e.ctx, a.ID, false)
	e.runNow(false)
	if e.sink.List[0].EmailDeliverable {
		t.Fatal("reminder claims a deliverable address")
	}
}

func TestLapseTimelineTwoStageDeletion(t *testing.T) {
	e := newEnv(t)
	a, h := e.lapsedOwner()
	// day 29: nothing destructive
	e.clock.Set(t0.Add(days(29)))
	r := e.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 0 || e.state(a) != billing.StateLapsedReadOnly {
		t.Fatalf("%+v", r.Actions)
	}
	// day 30: pending deletion; stage 1 tombstones (Apple is asked first and agrees: expired)
	e.clock.Set(t0.Add(days(30)))
	r = e.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 1 || e.household(a).Status != control.HouseholdTombstoned || e.state(a) != billing.StatePendingDeletion {
		t.Fatalf("%+v status=%s", r.Actions, e.household(a).Status)
	}
	if e.api.CallCount("status:otx") == 0 {
		t.Fatal("no fresh Apple check before stage 1")
	}
	if e.eraser.Count() != 0 {
		t.Fatal("erased at stage 1")
	}
	// the owner can still read and export while tombstoned
	if p, _ := e.calc.ForAccount(e.ctx, a.ID); !p.Can("read") || !p.Can("export") || p.Can("write") {
		t.Fatalf("%v", p.Allowed)
	}
	// day 31: 2 days not over
	e.clock.Set(t0.Add(days(31)))
	e.runNow(false)
	if e.eraser.Count() != 0 || e.household(a).Status != control.HouseholdTombstoned {
		t.Fatal("erased too early")
	}
	// day 32: stage 2
	e.clock.Set(t0.Add(days(32)))
	r = e.runNow(false)
	if r.Count(lifecycle.ActErase) != 1 || e.eraser.Count() != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	got := e.household(a)
	if got.Status != control.HouseholdDeleted || got.DeletedAt.IsZero() || e.state(a) != billing.StateDeleted {
		t.Fatalf("%+v state=%s", got, e.state(a))
	}
	if _, _, err := e.st.HouseholdOfAccount(e.ctx, a.ID); !control.Is(err, control.ErrNoHousehold) {
		t.Fatal("membership remains after erasure")
	}
	// the plan lists the files of exactly this household
	ep := e.eraser.Erased[0]
	if ep.HouseholdID != h.ID || len(ep.Paths()) == 0 {
		t.Fatalf("%+v", ep)
	}
	for _, p := range ep.Paths() {
		if !strings.Contains(p, h.ID) {
			t.Errorf("path %s does not belong to the household", p)
		}
	}
	// the account row survives; idempotent from here on
	acc, _ := e.st.AccountByID(e.ctx, a.ID)
	if acc.Status != control.AccountActive {
		t.Fatal("account must be kept until the user deletes it")
	}
	if r := e.runNow(false); r.Count(lifecycle.ActErase) != 0 || r.Count(lifecycle.ActTombstone) != 0 || e.eraser.Count() != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	for _, act := range []string{"lifecycle.tombstone", "lifecycle.erase"} {
		if au, _ := e.st.ListAudit(e.ctx, control.AuditFilter{Subject: h.ID, Action: act}); len(au) != 1 {
			t.Errorf("%s audit rows: %d", act, len(au))
		}
	}
	// after deletion a new subscription starts an empty household
	e.appleSub(a, "otx2", control.EntActive, t0.Add(days(70)))
	nh := e.household(a)
	if nh.ID == h.ID || nh.Status != control.HouseholdActive || e.state(a) != billing.StateActive {
		t.Fatalf("new household %+v state=%s", nh, e.state(a))
	}
}

func TestStageGapAndLateJob(t *testing.T) {
	e := newEnv(t)
	a, _ := e.lapsedOwner()
	// the job did not run until day 40: stage 1 now, stage 2 only after the gap
	e.clock.Set(t0.Add(days(40)))
	e.runNow(false)
	if e.household(a).Status != control.HouseholdTombstoned || e.eraser.Count() != 0 {
		t.Fatal("stage 1 only")
	}
	r := e.runNow(false)
	if r.Count(lifecycle.ActSkipped) != 1 || e.eraser.Count() != 0 {
		t.Fatalf("%+v", r.Actions)
	}
	e.clock.Advance(25 * time.Hour)
	e.runNow(false)
	if e.eraser.Count() != 1 {
		t.Fatal("not erased after the gap")
	}
}

func TestGuardsApple(t *testing.T) {
	// Apple says the subscription is alive again: nothing is deleted, the plan recovers
	e := newEnv(t)
	a, _ := e.lapsedOwner()
	e.clock.Set(t0.Add(days(30)))
	e.setApple("otx", control.EntActive, t0.Add(days(60)))
	r := e.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 0 || e.household(a).Status != control.HouseholdActive {
		t.Fatalf("%+v", r.Actions)
	}
	if e.state(a) != billing.StateActive {
		t.Fatalf("plan did not recover: %s", e.state(a))
	}
	// Apple unreachable: refuse
	e2 := newEnv(t)
	a2, _ := e2.lapsedOwner()
	e2.clock.Set(t0.Add(days(30)))
	e2.api.Down = true
	r = e2.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 0 || r.Count(lifecycle.ActSkipped) != 1 || e2.household(a2).Status != control.HouseholdActive {
		t.Fatalf("%+v", r.Actions)
	}
	e2.api.Down = false
	if r = e2.runNow(false); r.Count(lifecycle.ActTombstone) != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	// no Apple client at all: refuse
	e3 := newEnv(t)
	a3, _ := e3.lapsedOwner()
	e3.run.Apple = nil
	e3.clock.Set(t0.Add(days(30)))
	if r = e3.runNow(false); r.Count(lifecycle.ActTombstone) != 0 || e3.household(a3).Status != control.HouseholdActive {
		t.Fatalf("%+v", r.Actions)
	}
}

func TestResubscribeOrGrantAfterTombstoneUntombstones(t *testing.T) {
	e := newEnv(t)
	a, _ := e.lapsedOwner()
	e.clock.Set(t0.Add(days(30)))
	e.runNow(false)
	if e.household(a).Status != control.HouseholdTombstoned {
		t.Fatal("setup")
	}
	e.appleSub(a, "otx3", control.EntActive, t0.Add(days(60)))
	r := e.runNow(false)
	if r.Count(lifecycle.ActUntombstone) != 1 || e.household(a).Status != control.HouseholdActive || e.state(a) != billing.StateActive {
		t.Fatalf("%+v", r.Actions)
	}
	if e.eraser.Count() != 0 {
		t.Fatal("erased a recovered household")
	}
}

func TestKillSwitchAndCaps(t *testing.T) {
	e := newEnv(t)
	var owners []control.Account
	for i := 0; i < 7; i++ {
		a := e.account("o" + string(rune('a'+i)))
		e.appleSub(a, "otx"+string(rune('a'+i)), control.EntExpired, t0)
		owners = append(owners, a)
	}
	e.clock.Set(t0.Add(days(30)))
	e.run.Cfg.KillSwitch = true
	r := e.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 0 || r.Count(lifecycle.ActSkipped) != 7 {
		t.Fatalf("%+v", r.Actions)
	}
	e.run.Cfg.KillSwitch = false
	r = e.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 5 {
		t.Fatalf("cap: %d tombstones", r.Count(lifecycle.ActTombstone))
	}
	r = e.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 2 {
		t.Fatalf("rest: %d", r.Count(lifecycle.ActTombstone))
	}
	e.clock.Set(t0.Add(days(33)))
	e.runNow(false)
	if e.eraser.Count() != 5 {
		t.Fatalf("erased %d, want the cap of 5", e.eraser.Count())
	}
	e.runNow(false)
	if e.eraser.Count() != 7 {
		t.Fatalf("erased %d", e.eraser.Count())
	}
	_ = owners
}

func TestEraserFailureKeepsTombstoneAndRetries(t *testing.T) {
	e := newEnv(t)
	a, _ := e.lapsedOwner()
	e.clock.Set(t0.Add(days(30)))
	e.runNow(false)
	e.clock.Set(t0.Add(days(32)))
	e.eraser.Fail = errors.New("disk busy")
	r := e.runNow(false)
	if r.Count(lifecycle.ActError) != 1 || e.household(a).Status != control.HouseholdTombstoned {
		t.Fatalf("%+v", r.Actions)
	}
	e.eraser.Fail = nil
	e.runNow(false)
	if e.household(a).Status != control.HouseholdDeleted {
		t.Fatal("not retried")
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	a, h := e.lapsedOwner()
	e.clock.Set(t0.Add(days(30)))
	e.st.AddPendingRevocation(e.ctx, a.ID, []byte("x"))
	before := snapshot(e, a)
	r := e.runNow(true)
	if !r.DryRun || r.Count(lifecycle.ActTombstone) != 1 || r.Count(lifecycle.ActPlanCache) != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	if after := snapshot(e, a); after != before {
		t.Fatalf("dry run changed state:\n%s\n%s", before, after)
	}
	if len(e.sink.List) != 0 || e.eraser.Count() != 0 || e.rt.calls != 0 || e.api.CallCount("status") != 0 {
		t.Fatal("dry run had side effects")
	}
	if ev, _ := e.st.LifecycleEvents(e.ctx, h.ID); len(ev) != 0 {
		t.Fatal("dry run recorded events")
	}
	// a dry run at stage 2 reports the erase but deletes nothing
	e.runNow(false)
	e.clock.Set(t0.Add(days(32)))
	r = e.runNow(true)
	if r.Count(lifecycle.ActErase) != 1 || e.eraser.Count() != 0 || e.household(a).Status != control.HouseholdTombstoned {
		t.Fatalf("%+v", r.Actions)
	}
	// the real run then does what the dry run announced
	r2 := e.runNow(false)
	if r2.Count(lifecycle.ActErase) != 1 || e.eraser.Count() != 1 {
		t.Fatalf("%+v", r2.Actions)
	}
}

func snapshot(e *env, a control.Account) string {
	h := e.household(a)
	au, _ := e.st.ListAudit(e.ctx, control.AuditFilter{Limit: 1000})
	ev, _ := e.st.LifecycleEvents(e.ctx, h.ID)
	pend, _ := e.st.PendingRevocations(e.ctx)
	return strings.Join([]string{h.Status, h.PlanStateCache, control.FormatTime(h.ReadOnlySince), control.FormatTime(h.DeleteAfter),
		string(rune('0' + len(au)%10)), string(rune('0' + len(ev))), string(rune('0' + len(pend)))}, "|")
}

func TestUserAccountDeletionEndToEnd(t *testing.T) {
	e := newEnv(t)
	a := e.account("leaving")
	e.appleSub(a, "otx", control.EntActive, t0.Add(days(300)))
	keep, _, _ := e.ctl.CreateDevice(e.ctx, a.ID, "phone")
	h := e.household(a)
	e.st.SetRefreshToken(e.ctx, a.ID, []byte("sealed"))
	if _, err := e.ctl.RequestAccountDeletion(e.ctx, a.ID, control.DeleteConfirmText, true, keep.ID); err != nil {
		t.Fatal(err)
	}
	if e.state(a) != billing.StatePendingDeletion {
		t.Fatal(e.state(a))
	}
	// day 0: tombstone (no Apple check needed for a user request)
	r := e.runNow(false)
	if r.Count(lifecycle.ActTombstone) != 1 || e.api.CallCount("status") != 0 {
		t.Fatalf("%+v", r.Actions)
	}
	if e.rt.calls != 1 {
		t.Fatal("revocations not retried")
	}
	// day 3: still waiting
	e.clock.Set(t0.Add(days(3)))
	e.runNow(false)
	if e.eraser.Count() != 0 {
		t.Fatal("erased inside the cooling-off")
	}
	// day 7: household erased, then the account's personal data
	e.clock.Set(t0.Add(days(7)))
	r = e.runNow(false)
	if r.Count(lifecycle.ActErase) != 1 || r.Count(lifecycle.ActAccountErase) != 1 {
		t.Fatalf("%+v", r.Actions)
	}
	acc, _ := e.st.AccountByID(e.ctx, a.ID)
	if acc.Status != control.AccountDeleted || acc.Email != "" || acc.AppleSub == "leaving" || acc.DeletedAt.IsZero() {
		t.Fatalf("%+v", acc)
	}
	if devs, _ := e.st.ListDevices(e.ctx, a.ID); len(devs) != 0 {
		t.Fatal("devices remain")
	}
	if ent, err := e.st.EntitlementByOriginalTx(e.ctx, "otx"); err != nil || ent.OriginalTransactionID != "otx" {
		t.Fatal("billing ledger must survive")
	}
	if got, _ := e.st.HouseholdByID(e.ctx, h.ID); got.Status != control.HouseholdDeleted {
		t.Fatal("household not deleted")
	}
	// the Apple id may sign up again as a new account
	b, created, _ := e.ctl.UpsertAccount(e.ctx, "leaving", "", false, "")
	if !created || b.ID == a.ID {
		t.Fatal("re-signup")
	}
	if r := e.runNow(false); len(r.Actions) != 0 {
		t.Fatalf("not idempotent: %+v", r.Actions)
	}
}

func TestCancelledUserDeletionNeverErases(t *testing.T) {
	e := newEnv(t)
	a := e.account("changed-mind")
	e.appleSub(a, "otx", control.EntActive, t0.Add(days(300)))
	keep, _, _ := e.ctl.CreateDevice(e.ctx, a.ID, "phone")
	e.ctl.RequestAccountDeletion(e.ctx, a.ID, control.DeleteConfirmText, true, keep.ID)
	e.runNow(false) // tombstoned
	if e.household(a).Status != control.HouseholdTombstoned {
		t.Fatal("setup")
	}
	e.clock.Set(t0.Add(days(5)))
	if err := e.ctl.CancelAccountDeletion(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(t0.Add(days(8)))
	r := e.runNow(false)
	if e.eraser.Count() != 0 || r.Count(lifecycle.ActErase) != 0 || r.Count(lifecycle.ActAccountErase) != 0 {
		t.Fatalf("%+v", r.Actions)
	}
	if e.household(a).Status != control.HouseholdActive || e.state(a) != billing.StateActive {
		t.Fatalf("household %s plan %s", e.household(a).Status, e.state(a))
	}
}

func TestMemberAccountDeletion(t *testing.T) {
	e := newEnv(t)
	o := e.account("owner")
	e.appleSub(o, "otx", control.EntActive, t0.Add(days(300)))
	_, code, _ := e.ctl.CreateInvite(e.ctx, o.ID)
	m := e.account("member")
	if _, err := e.ctl.AcceptInvite(e.ctx, m.ID, code); err != nil {
		t.Fatal(err)
	}
	e.ctl.RequestAccountDeletion(e.ctx, m.ID, control.DeleteConfirmText, false, 0)
	e.runNow(false)
	e.clock.Set(t0.Add(days(7)))
	r := e.runNow(false)
	if r.Count(lifecycle.ActAccountErase) != 1 || r.Count(lifecycle.ActErase) != 0 {
		t.Fatalf("%+v", r.Actions)
	}
	if got, _ := e.st.AccountByID(e.ctx, m.ID); got.Status != control.AccountDeleted {
		t.Fatal("member not erased")
	}
	if e.household(o).Status != control.HouseholdActive || e.state(o) != billing.StateActive {
		t.Fatal("owner's household touched")
	}
}

func TestGrantOnlyHouseholdNeedsNoAppleCheck(t *testing.T) {
	e := newEnv(t)
	a := e.account("friend")
	g, _ := e.st.InsertGrant(e.ctx, control.Grant{AccountID: a.ID, ExpiresAt: t0.Add(days(10)), Reason: "r", CreatedBy: "op"})
	e.st.UpsertEntitlement(e.ctx, control.Entitlement{AccountID: a.ID, Source: control.SourceGrant, State: control.EntActive, ExpiresAt: t0.Add(days(10)), GrantID: g.ID})
	e.ctl.EnsureHousehold(e.ctx, a.ID)
	e.run.Apple = nil
	e.clock.Set(t0.Add(days(10)))
	e.runNow(false)
	if e.household(a).PlanStateCache != billing.StateLapsedReadOnly {
		t.Fatal(e.household(a).PlanStateCache)
	}
	e.clock.Set(t0.Add(days(40)))
	e.runNow(false)
	if e.household(a).Status != control.HouseholdTombstoned {
		t.Fatal("grant-only lapse should proceed without an Apple check")
	}
	e.clock.Set(t0.Add(days(42)))
	e.runNow(false)
	if e.household(a).Status != control.HouseholdDeleted {
		t.Fatal("not erased")
	}
}

// Grant-only households have no Apple answer to contradict the local clock, so a clock that jumped
// (or is behind the newest audit row) stops every destructive step until it is trustworthy again.
func TestGrantOnlyErasureRefusesAnUntrustedClock(t *testing.T) {
	e := newEnv(t)
	e.run.Cfg.ClockJumpGuard = 24 * time.Hour
	a := e.account("friend")
	g, _ := e.st.InsertGrant(e.ctx, control.Grant{AccountID: a.ID, ExpiresAt: t0.Add(days(10)), Reason: "r", CreatedBy: "op"})
	e.st.UpsertEntitlement(e.ctx, control.Entitlement{AccountID: a.ID, Source: control.SourceGrant, State: control.EntActive, ExpiresAt: t0.Add(days(10)), GrantID: g.ID})
	e.ctl.EnsureHousehold(e.ctx, a.ID)
	e.run.Apple = nil
	e.runNow(false) // records the first heartbeat at t0

	// the clock jumps 40 days in one step: the run does nothing destructive, the next one may
	e.clock.Set(t0.Add(days(40)))
	r := e.runNow(false)
	if e.household(a).Status != control.HouseholdActive || !r.Has(lifecycle.ActSkipped, e.household(a).ID) {
		t.Fatalf("a clock jump must stop the first run: %s %+v", e.household(a).Status, r.Actions)
	}
	e.runNow(false)
	if e.household(a).Status != control.HouseholdTombstoned {
		t.Fatalf("the second run should proceed: %s", e.household(a).Status)
	}

	// a clock that falls behind the newest audit row never deletes
	e.clock.Set(t0.Add(days(42)))
	e.runNow(false) // audit rows now exist at day 42
	e.clock.Set(t0.Add(days(41)))
	e.runNow(false)
	if e.household(a).Status != control.HouseholdTombstoned {
		t.Fatalf("erased with a clock behind the audit log: %s", e.household(a).Status)
	}
	e.clock.Set(t0.Add(days(43)))
	e.runNow(false)
	if e.household(a).Status != control.HouseholdDeleted {
		t.Fatalf("a sane clock should finish the erasure: %s", e.household(a).Status)
	}
}

// ---- erasure planning and the OS eraser ----

func TestPlanErasure(t *testing.T) {
	id := control.NewUUID()
	p, err := lifecycle.PlanErasure(lifecycle.Layout{DataDir: "/data", BackupDir: "/backup", ExportDir: "/exports"}, id)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, it := range p.Items {
		kinds[it.Kind]++
		if !strings.Contains(it.Path, id) {
			t.Errorf("%s lacks the household id", it.Path)
		}
	}
	if kinds["database"] < 3 || kinds["images"] != 1 || kinds["backups"] != 1 || kinds["exports"] != 1 {
		t.Fatalf("%v", kinds)
	}
	if _, err := lifecycle.PlanErasure(lifecycle.Layout{DataDir: "/data"}, "../../etc"); err == nil {
		t.Fatal("malformed id accepted")
	}
	if _, err := lifecycle.PlanErasure(lifecycle.Layout{DataDir: "/data"}, ""); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestOSEraserDeletesOnlyTheHousehold(t *testing.T) {
	root := t.TempDir()
	l := lifecycle.Layout{DataDir: filepath.Join(root, "data"), BackupDir: filepath.Join(root, "backup"), ExportDir: filepath.Join(root, "exports")}
	a, b := control.NewUUID(), control.NewUUID()
	mk := func(parts ...string) string {
		p := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	files := []string{
		mk(l.DataDir, "households", a, "shop.db"), mk(l.DataDir, "households", a, "shop.db-wal"), mk(l.DataDir, "households", a, "images", "1.jpg"),
		mk(l.BackupDir, "households", a, "db", "s.db"), mk(l.ExportDir, a, "e.zip"),
	}
	others := []string{mk(l.DataDir, "households", b, "shop.db"), mk(l.DataDir, "control.db"), mk(l.BackupDir, "households", b, "db", "s.db"), mk(l.ExportDir, b, "e.zip")}
	plan, _ := lifecycle.PlanErasure(l, a)
	er := lifecycle.OSEraser{Layout: l}
	if err := er.Erase(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s still exists", f)
		}
	}
	for _, f := range others {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s was deleted: %v", f, err)
		}
	}
	if err := er.Erase(context.Background(), plan); err != nil { // idempotent
		t.Fatal(err)
	}
	// a doctored plan is refused before anything is deleted
	evil := lifecycle.ErasurePlan{HouseholdID: a, Items: []lifecycle.ErasureItem{{Kind: "database", Path: others[0]}, {Kind: "database", Path: "/etc/" + a}}}
	if err := er.Erase(context.Background(), evil); err == nil {
		t.Fatal("plan outside the roots accepted")
	}
	if _, err := os.Stat(others[0]); err != nil {
		t.Fatal("deleted before validating")
	}
	root2 := lifecycle.ErasurePlan{HouseholdID: a, Items: []lifecycle.ErasureItem{{Kind: "database", Path: l.DataDir}}}
	if err := er.Erase(context.Background(), root2); err == nil {
		t.Fatal("deleting a root accepted")
	}
}
