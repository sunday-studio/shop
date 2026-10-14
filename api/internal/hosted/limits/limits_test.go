package limits_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/limits"
)

var t0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

type env struct {
	ctx   context.Context
	clock *hclock.Fake
	st    *control.Store
	ctl   *control.Service
	m     *limits.Meter
	hh    control.Household
}

func newEnv(t *testing.T, l ...limits.Limits) *env {
	t.Helper()
	e := &env{ctx: context.Background(), clock: hclock.NewFake(t0)}
	st, err := control.Open(filepath.Join(t.TempDir(), "c.db"), e.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	e.ctl = control.NewService(st, control.DefaultConfig(), nil)
	e.m = limits.NewMeter(st, l...)
	a, _, _ := e.ctl.UpsertAccount(e.ctx, "owner", "", false, "")
	e.hh, _, _ = e.ctl.EnsureHousehold(e.ctx, a.ID)
	return e
}

func limitOf(t *testing.T, err error) *control.LimitError {
	t.Helper()
	le, ok := control.IsLimit(err)
	if !ok {
		t.Fatalf("not a limit error: %v", err)
	}
	return le
}

func TestDefaultsMatchTheContract(t *testing.T) {
	b, _ := json.Marshal(limits.Defaults().Public())
	want := `{"household_members":4,"max_upload_bytes":10485760,"receipts_per_month":300,"receipts_per_month_soft":150,"receipts_per_day":25,"storage_bytes":2147483648,"max_queued_jobs":50,"exports_per_day":3,"open_invites":5}`
	if string(b) != want {
		t.Fatalf("\n got %s\nwant %s", b, want)
	}
	// zero = unlimited renders null
	b, _ = json.Marshal(limits.Limits{}.Public())
	var m map[string]any
	json.Unmarshal(b, &m)
	for k, v := range m {
		if v != nil {
			t.Errorf("%s = %v, want null", k, v)
		}
	}
}

func TestUploadSize(t *testing.T) {
	e := newEnv(t)
	if err := e.m.CheckUpload(e.ctx, e.hh.ID, 10<<20); err != nil {
		t.Fatal(err)
	}
	err := e.m.CheckUpload(e.ctx, e.hh.ID, 10<<20+1)
	le := limitOf(t, err)
	if le.Limit != "max_upload_bytes" || le.Value != 10<<20 || le.Current != 10<<20+1 || control.Code(err) != "file_too_large" || le.HTTPStatus() != 413 {
		t.Fatalf("%+v code=%s", le, control.Code(err))
	}
}

func TestReceiptsPerDayAndMonthAndReset(t *testing.T) {
	e := newEnv(t, limits.Limits{ReceiptsPerMonth: 5, ReceiptsPerMonthSoft: 3, ReceiptsPerDay: 2, StorageBytes: 1 << 30})
	st, err := e.m.RecordReceipt(e.ctx, e.hh.ID, 100)
	if err != nil || st.ThisMonth != 1 || st.Today != 1 || st.SoftReached || st.StorageUsed != 100 {
		t.Fatalf("%+v %v", st, err)
	}
	e.m.RecordReceipt(e.ctx, e.hh.ID, 100)
	_, err = e.m.RecordReceipt(e.ctx, e.hh.ID, 100)
	le := limitOf(t, err)
	if le.Limit != "receipts_per_day" || le.Value != 2 || le.Current != 2 || !le.ResetsAt.Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)) || control.Code(err) != "limit_reached" {
		t.Fatalf("%+v", le)
	}
	// refused uploads count nothing
	if u, _ := e.m.UsageFor(e.ctx, e.hh.ID, 0); u.ReceiptsToday != 2 || u.ReceiptsThisMonth != 2 || u.StorageBytes != 200 {
		t.Fatalf("%+v", u)
	}
	// next day: two more, the third receipt of the month crosses the soft level
	e.clock.Advance(24 * time.Hour)
	st, _ = e.m.RecordReceipt(e.ctx, e.hh.ID, 1)
	if !st.SoftReached || st.ThisMonth != 3 {
		t.Fatalf("soft: %+v", st)
	}
	e.m.RecordReceipt(e.ctx, e.hh.ID, 1)
	e.clock.Advance(24 * time.Hour)
	e.m.RecordReceipt(e.ctx, e.hh.ID, 1)
	// 5 used this month: the sixth hits the monthly cap even though the day is fresh
	_, err = e.m.RecordReceipt(e.ctx, e.hh.ID, 1)
	le = limitOf(t, err)
	if le.Limit != "receipts_per_month" || le.Value != 5 || le.Current != 5 || !le.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", le)
	}
	// new month: counters start again, storage persists
	e.clock.Set(time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC))
	st, err = e.m.RecordReceipt(e.ctx, e.hh.ID, 1)
	if err != nil || st.ThisMonth != 1 || st.StorageUsed != 204 {
		t.Fatalf("%+v %v", st, err)
	}
	u, _ := e.m.UsageFor(e.ctx, e.hh.ID, 7)
	if u.Period != "2026-11" || u.ReceiptsThisMonth != 1 || u.QueuedJobs != 7 || u.Members != 1 || !u.ResetsAt.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", u)
	}
}

func TestStorageLimitAndRelease(t *testing.T) {
	e := newEnv(t, limits.Limits{StorageBytes: 1000})
	if _, err := e.m.RecordReceipt(e.ctx, e.hh.ID, 600); err != nil {
		t.Fatal(err)
	}
	_, err := e.m.RecordReceipt(e.ctx, e.hh.ID, 401)
	le := limitOf(t, err)
	if le.Limit != "storage_bytes" || le.Value != 1000 || le.Current != 600 || !le.ResetsAt.IsZero() {
		t.Fatalf("%+v", le)
	}
	if _, err := e.m.RecordReceipt(e.ctx, e.hh.ID, 400); err != nil { // exactly at the limit is fine
		t.Fatal(err)
	}
	// the second upload failed downstream: release gives the room back
	if err := e.m.Release(e.ctx, e.hh.ID, 400); err != nil {
		t.Fatal(err)
	}
	u, _ := e.m.UsageFor(e.ctx, e.hh.ID, 0)
	if u.StorageBytes != 600 || u.ReceiptsThisMonth != 1 || u.ReceiptsToday != 1 {
		t.Fatalf("%+v", u)
	}
	// deleting images lowers the gauge, never below zero
	if v, _ := e.m.AddStorage(e.ctx, e.hh.ID, -10_000); v != 0 {
		t.Fatalf("%d", v)
	}
	e.m.SetStorage(e.ctx, e.hh.ID, 123)
	if u, _ := e.m.UsageFor(e.ctx, e.hh.ID, 0); u.StorageBytes != 123 {
		t.Fatal(u.StorageBytes)
	}
}

func TestConcurrentUploadsNeverExceedTheLimit(t *testing.T) {
	e := newEnv(t, limits.Limits{ReceiptsPerDay: 10})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.m.RecordReceipt(e.ctx, e.hh.ID, 1); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 10 {
		t.Fatalf("%d uploads accepted, want 10", ok)
	}
}

func TestQueueExportsAndOverrides(t *testing.T) {
	e := newEnv(t)
	if err := e.m.CheckQueue(e.ctx, e.hh.ID, 49); err != nil {
		t.Fatal(err)
	}
	le := limitOf(t, e.m.CheckQueue(e.ctx, e.hh.ID, 50))
	if le.Limit != "max_queued_jobs" || le.Value != 50 || le.Current != 50 {
		t.Fatalf("%+v", le)
	}
	for i := 0; i < 3; i++ {
		if err := e.m.StartExport(e.ctx, e.hh.ID); err != nil {
			t.Fatal(err)
		}
	}
	le = limitOf(t, e.m.StartExport(e.ctx, e.hh.ID))
	if le.Limit != "exports_per_day" || le.Value != 3 || le.ResetsAt.IsZero() {
		t.Fatalf("%+v", le)
	}
	e.clock.Advance(24 * time.Hour)
	if err := e.m.StartExport(e.ctx, e.hh.ID); err != nil {
		t.Fatal(err)
	}
	// per-household override: more exports, no queue limit, storage unchanged
	e.st.SetHouseholdLimitsOverride(e.ctx, e.hh.ID, `{"exports_per_day": 10, "max_queued_jobs": null}`)
	l, err := e.m.LimitsFor(e.ctx, e.hh.ID)
	if err != nil || l.ExportsPerDay != 10 || l.MaxQueuedJobs != 0 || l.StorageBytes != 2<<30 {
		t.Fatalf("%+v %v", l, err)
	}
	if err := e.m.CheckQueue(e.ctx, e.hh.ID, 5000); err != nil {
		t.Fatalf("unlimited queue refused: %v", err)
	}
	e.st.SetHouseholdLimitsOverride(e.ctx, e.hh.ID, `{not json`)
	if _, err := e.m.LimitsFor(e.ctx, e.hh.ID); err == nil {
		t.Fatal("bad override accepted")
	}
	if _, err := e.m.LimitsFor(e.ctx, "missing"); !control.Is(err, control.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestUnlimitedHousehold(t *testing.T) {
	e := newEnv(t, limits.Limits{})
	for i := 0; i < 50; i++ {
		if _, err := e.m.RecordReceipt(e.ctx, e.hh.ID, 1<<30); err != nil {
			t.Fatal(err)
		}
	}
}

func TestControlConfigFromLimits(t *testing.T) {
	cfg := limits.Limits{HouseholdMembers: 2, OpenInvites: 1}.ControlConfig(control.DefaultConfig())
	if cfg.MemberCap != 2 || cfg.MaxOpenInvites != 1 || cfg.InviteTTL != 7*24*time.Hour {
		t.Fatalf("%+v", cfg)
	}
	if got := (limits.Limits{}).ControlConfig(control.DefaultConfig()); got.MemberCap != 4 {
		t.Fatal("zero must keep the base")
	}
}

func TestGate(t *testing.T) {
	g := limits.NewGate(3)
	var rel []func()
	for i := 0; i < 2; i++ {
		r, err := g.Acquire("h1", 2)
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, r)
	}
	_, err := g.Acquire("h1", 2)
	if !control.Is(err, limits.ErrBusy) || err.(*control.Error).RetryAfterSeconds != 1 || control.Code(err) != "rate_limited" {
		t.Fatalf("per-household cap: %v", err)
	}
	r3, err := g.Acquire("h2", 2) // other household still fine, total now 3
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Acquire("h3", 2); err == nil {
		t.Fatal("global cap not enforced")
	}
	rel[0]()
	rel[0]() // releasing twice must not double-free
	if g.InFlight("h1") != 1 {
		t.Fatalf("in flight %d", g.InFlight("h1"))
	}
	if _, err := g.Acquire("h1", 2); err != nil {
		t.Fatal(err)
	}
	r3()
	rel[1]()
}
