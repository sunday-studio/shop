package hostedapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"shop/internal/auth"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/hostedapi"
	w "shop/internal/hosted/hostedworld"
	"shop/internal/hosted/lifecycle"
	"shop/internal/tenancy"
)

func TestSignInRateLimitFailuresCountDouble(t *testing.T) {
	world := w.New(t)
	try := func() w.Resp {
		nonce := world.Nonce()
		return world.Do("POST", "/v1/auth/apple", "", map[string]any{"identity_token": "bad", "authorization_code": "c", "nonce": nonce, "device_name": "x"})
	}
	// 10 per minute, a failure counts twice: the 5th failure uses up the minute
	for i := 0; i < 5; i++ {
		want(t, try(), 401, "apple_token_invalid")
	}
	r := try()
	want(t, r, 429, "rate_limited")
	if r.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	world.Clock.Advance(61 * time.Second)
	want(t, try(), 401, "apple_token_invalid")
}

func TestOtherRateLimits(t *testing.T) {
	world := w.New(t)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	// invite accept: 10 per hour per account
	for i := 0; i < 10; i++ {
		want(t, world.Do("POST", "/v1/household/invites/AAAA-AAAA-AAAA/accept", ama.Token, nil), 404, "invite_invalid")
	}
	want(t, world.Do("POST", "/v1/household/invites/AAAA-AAAA-AAAA/accept", ama.Token, nil), 429, "rate_limited")
	// the public preview: 10 per hour per IP
	for i := 0; i < 10; i++ {
		want(t, world.Do("GET", "/v1/household/invites/AAAA-AAAA-AAAA", "", nil), 200, "")
	}
	want(t, world.Do("GET", "/v1/household/invites/AAAA-AAAA-AAAA", "", nil), 429, "rate_limited")
	// webhooks: 600 per minute per IP is far above the others; a flood of bad ones is still limited
	for i := 0; i < 600; i++ {
		world.Do("POST", "/v1/billing/apple/notifications", "", map[string]any{"signedPayload": "x"})
	}
	want(t, world.Do("POST", "/v1/billing/apple/notifications", "", map[string]any{"signedPayload": "x"}), 429, "rate_limited")
	// 300 requests per minute per token
	world.Clock.Advance(2 * time.Minute)
	n := 0
	for ; n < 400; n++ {
		if world.Do("GET", "/v1/auth/me", ama.Token, nil).Status() == 429 {
			break
		}
	}
	if n < 290 || n > 301 {
		t.Fatalf("token limit hit after %d requests", n)
	}
}

func TestInFlightLimitPerHousehold(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	hh := world.HouseholdID(ama)
	var rels []func()
	for i := 0; i < 16; i++ {
		rel, err := world.Hosted.Gate.Acquire(hh, 16)
		if err != nil {
			t.Fatal(err)
		}
		rels = append(rels, rel)
	}
	want(t, world.Do("GET", "/v1/shops", ama.Token, nil), 429, "rate_limited")
	rels[0]()
	want(t, world.Do("GET", "/v1/shops", ama.Token, nil), 200, "")
}

func TestIdempotencyInProgress(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	world.Subscribe(ama, "otx-ama")
	// another request holds the key
	if res, _, err := world.Store.BeginIdempotent(world.Ctx, ama.AccountID, "k-busy", "POST", "/v1/shops", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", 24*time.Hour); err != nil || res != control.IdemNew {
		t.Fatal(res, err)
	}
	r := world.Do("POST", "/v1/shops", ama.Token, nil, "Idempotency-Key", "k-busy")
	want(t, r, 409, "request_in_progress")
	if r.JSON()["retry_after_seconds"].(float64) != 1 {
		t.Fatalf("%s", r.Body)
	}
}

func TestMemberOfLapsedHousehold(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	tx := world.Subscribe(ama, "otx-ama")
	kofi := world.SignIn("sub-kofi", "Kofi")
	inv := world.Do("POST", "/v1/household/invites", ama.Token, nil)
	want(t, world.Do("POST", "/v1/household/invites/"+inv.JSON()["code"].(string)+"/accept", kofi.Token, nil), 200, "")
	world.At(20)
	exp := tx
	exp.ExpiresDate, exp.SignedDate = billing.MsOf(w.Day(14)), billing.MsOf(w.Day(20))
	want(t, world.Deliver(world.Notification(billing.NtExpired, "VOLUNTARY", "e1", w.Day(20), exp, w.Ren(0))), 200, "")
	acc := world.Do("GET", "/v1/account", kofi.Token, nil).JSON()
	pl := acc["plan"].(map[string]any)
	if pl["state"] != "lapsed_read_only" || pl["is_owner"] != false || pl["read_only"] != true {
		t.Fatalf("%v", pl)
	}
	want(t, world.Do("POST", "/v1/shops", kofi.Token, map[string]any{"name": "x"}), 403, "read_only")
	want(t, world.Do("GET", "/v1/shops", kofi.Token, nil), 200, "")
	want(t, world.Do("POST", "/v1/account/export", kofi.Token, nil), 202, "") // any member may export
	want(t, world.Do("POST", "/v1/household/leave", kofi.Token, nil), 204, "")
	// day ~51: pending deletion still reads; day 53 the lifecycle job removes the household
	world.At(51)
	if world.PlanState(ama) != "pending_deletion" {
		t.Fatal(world.PlanState(ama))
	}
	want(t, world.Do("GET", "/v1/shops", ama.Token, nil), 200, "")
	want(t, world.Do("POST", "/v1/shops", ama.Token, map[string]any{"name": "x"}), 403, "read_only")
}

func TestDirectory(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	dir := world.Hosted.Directory()
	if _, err := dir.HouseholdForToken(world.Ctx, auth.Hash(ama.Token)); err != tenancy.ErrUnknownToken {
		t.Fatalf("no household yet: %v", err)
	}
	world.Subscribe(ama, "otx-ama")
	id, err := dir.HouseholdForToken(world.Ctx, auth.Hash(ama.Token))
	if err != nil || id != world.HouseholdID(ama) {
		t.Fatal(id, err)
	}
	if _, err := dir.HouseholdForToken(world.Ctx, auth.Hash("st_unknown")); err != tenancy.ErrUnknownToken {
		t.Fatal(err)
	}
	want(t, world.Do("DELETE", "/v1/auth/devices/"+itoa(ama.DeviceID), ama.Token, nil), 204, "")
	if _, err := dir.HouseholdForToken(world.Ctx, auth.Hash(ama.Token)); err != tenancy.ErrUnknownToken {
		t.Fatalf("revoked device: %v", err)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestSchedulerLockPausedJobsAndStorageRecount(t *testing.T) {
	world := w.New(t, w.NoRates)
	ama := world.SignIn("sub-ama", "Ama")
	tx := world.Subscribe(ama, "otx-ama")
	want(t, upload(world, ama.Token, png1(3)), 202, "")
	hh := world.HouseholdID(ama)
	s := &hostedapi.Scheduler{API: world.Hosted, Runner: world.Life, Interval: time.Hour, RecountEvery: 1}
	// a held lease stops a run
	if ok, err := world.Store.TryLock(world.Ctx, "scheduler", "someone-else", time.Hour); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ran, err := s.RunOnce(world.Ctx); err != nil || ran {
		t.Fatalf("ran=%v err=%v while another holder has the lock", ran, err)
	}
	_ = world.Store.Unlock(world.Ctx, "scheduler", "someone-else")
	if ran, err := s.RunOnce(world.Ctx); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	// the lease is released afterwards
	if ok, _ := world.Store.TryLock(world.Ctx, "scheduler", "x", time.Minute); !ok {
		t.Fatal("lock not released")
	}
	_ = world.Store.Unlock(world.Ctx, "scheduler", "x")
	// the recount measured the household folder (database + image)
	if n, _ := world.Store.Counter(world.Ctx, hh, "gauge", "storage_bytes"); n < 1000 {
		t.Fatalf("storage gauge %d", n)
	}
	// background work pauses while the household is read-only
	view := &hostedapi.SchedulerView{Provider: world.Prov, API: world.Hosted}
	if ids, _ := view.IDs(world.Ctx); len(ids) != 1 || ids[0] != hh {
		t.Fatalf("active household not listed: %v", ids)
	}
	world.At(20)
	exp := tx
	exp.ExpiresDate, exp.SignedDate = billing.MsOf(w.Day(14)), billing.MsOf(w.Day(20))
	want(t, world.Deliver(world.Notification(billing.NtExpired, "VOLUNTARY", "e1", w.Day(20), exp, w.Ren(0))), 200, "")
	view2 := &hostedapi.SchedulerView{Provider: world.Prov, API: world.Hosted}
	if ids, _ := view2.IDs(world.Ctx); len(ids) != 0 {
		t.Fatalf("read-only household still scheduled: %v", ids)
	}
	if _, _, err := view2.Acquire(world.Ctx, hh); err == nil {
		t.Fatal("read-only household can be acquired by the job scheduler")
	}
}

func TestReminderSinks(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.Store(string(b) + "|" + r.Header.Get("X-Shop-Secret"))
	}))
	defer srv.Close()
	rem := lifecycle.Reminder{HouseholdID: "h1", OwnerAccountID: "a1", Kind: lifecycle.ReminderLapseDay25, DeleteAfter: w.Day(30), Email: "ama@example.com", EmailDeliverable: true}
	if err := (hostedapi.WebhookSink{URL: srv.URL, Secret: "s3"}).Emit(context.Background(), rem); err != nil {
		t.Fatal(err)
	}
	if v, _ := got.Load().(string); !strings.Contains(v, `"kind":"lapse_day_25"`) || !strings.HasSuffix(v, "|s3") {
		t.Fatal(v)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(500) }))
	defer bad.Close()
	if err := (hostedapi.WebhookSink{URL: bad.URL}).Emit(context.Background(), rem); err == nil {
		t.Fatal("a failing webhook must fail the emit (the job retries)")
	}
	var msg string
	var to []string
	sm := hostedapi.SMTPSink{Addr: "smtp.example:587", From: "shop@example.com", Support: "support@example.com",
		Send: func(addr string, a smtp.Auth, from string, t []string, m []byte) error {
			to, msg = t, string(m)
			return nil
		}}
	if err := sm.Emit(context.Background(), rem); err != nil || len(to) != 1 || !strings.Contains(msg, "Subject: Export your Shop data soon") || !strings.Contains(msg, "13 November 2026") {
		t.Fatal(err, to, msg)
	}
	to = nil
	rem.EmailDeliverable = false
	if err := sm.Emit(context.Background(), rem); err != nil || to != nil {
		t.Fatal("mailed an undeliverable address")
	}
	rem.EmailDeliverable, rem.Email = true, "a@b.c\r\nBcc: x@y.z"
	if err := sm.Emit(context.Background(), rem); err != nil || to != nil {
		t.Fatal("header injection reached the mailer")
	}
	ms := hostedapi.MultiSink{hostedapi.LogSink{}, hostedapi.WebhookSink{URL: bad.URL}}
	if err := ms.Emit(context.Background(), rem); err == nil {
		t.Fatal("multi sink swallowed a failure")
	}
}
