package grants_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/hclock"
)

var t0 = time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)

func day(n int) time.Time { return t0.Add(time.Duration(n) * 24 * time.Hour) }

func TestCombineRule(t *testing.T) {
	cases := []struct {
		name         string
		apple, grant grants.Access
		wantSource   string
		wantLevel    grants.Level
	}{
		{"both none", grants.Access{}, grants.Access{}, "", grants.LevelNone},
		{"apple full, grant none", grants.Access{Level: grants.LevelFull, Until: day(5)}, grants.Access{}, "apple", grants.LevelFull},
		{"grant full, apple none", grants.Access{}, grants.Access{Level: grants.LevelFull, Until: day(5)}, "grant", grants.LevelFull},
		{"full beats read-only", grants.Access{Level: grants.LevelReadOnly}, grants.Access{Level: grants.LevelFull, Until: day(1)}, "grant", grants.LevelFull},
		{"read-only apple, grant none", grants.Access{Level: grants.LevelReadOnly}, grants.Access{}, "apple", grants.LevelReadOnly},
		{"both full, grant longer", grants.Access{Level: grants.LevelFull, Until: day(10)}, grants.Access{Level: grants.LevelFull, Until: day(60)}, "grant", grants.LevelFull},
		{"both full, apple longer", grants.Access{Level: grants.LevelFull, Until: day(60)}, grants.Access{Level: grants.LevelFull, Until: day(10)}, "apple", grants.LevelFull},
		{"renewing apple is open-ended", grants.Access{Level: grants.LevelFull, Until: day(10), Renewing: true}, grants.Access{Level: grants.LevelFull, Until: day(90)}, "apple", grants.LevelFull},
		{"tie goes to apple", grants.Access{Level: grants.LevelFull, Until: day(10)}, grants.Access{Level: grants.LevelFull, Until: day(10)}, "apple", grants.LevelFull},
		{"both read-only tie", grants.Access{Level: grants.LevelReadOnly}, grants.Access{Level: grants.LevelReadOnly}, "apple", grants.LevelReadOnly},
	}
	for _, c := range cases {
		got := grants.Combine(c.apple, c.grant)
		if got.Source != c.wantSource || got.Level != c.wantLevel {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
}

type env struct {
	ctx   context.Context
	clock *hclock.Fake
	st    *control.Store
	ctl   *control.Service
	g     *grants.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{ctx: context.Background(), clock: hclock.NewFake(t0)}
	st, err := control.Open(filepath.Join(t.TempDir(), "c.db"), e.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	e.ctl = control.NewService(st, control.DefaultConfig(), nil)
	e.g = grants.New(e.ctl, grants.DefaultConfig())
	return e
}

func (e *env) acct(t *testing.T, sub string) control.Account {
	a, _, err := e.ctl.UpsertAccount(e.ctx, sub, "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if control.Code(err) != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestCreateDefaultsAndEffects(t *testing.T) {
	e := newEnv(t)
	a := e.acct(t, "friend")
	g, replay, err := e.g.Create(e.ctx, grants.CreateInput{SupportCode: a.SupportCode, Reason: "Friend, beta tester", Actor: "casprine"})
	if err != nil || replay {
		t.Fatal(err)
	}
	if !g.ExpiresAt.Equal(day(90)) || g.Key != "hosted" || g.CreatedBy != "casprine" {
		t.Fatalf("%+v", g)
	}
	ents, _ := e.st.EntitlementsForAccount(e.ctx, a.ID)
	if len(ents) != 1 || ents[0].Source != "grant" || ents[0].GrantID != g.ID || !ents[0].ExpiresAt.Equal(day(90)) {
		t.Fatalf("entitlement row: %+v", ents)
	}
	if _, _, err := e.ctl.S.HouseholdOfAccount(e.ctx, a.ID); err != nil {
		t.Fatalf("household must exist after a grant: %v", err)
	}
	au, _ := e.st.ListAudit(e.ctx, control.AuditFilter{Action: "grant.create"})
	if len(au) != 1 || au[0].Actor != "casprine" || au[0].Subject != a.ID {
		t.Fatalf("audit %+v", au)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	a := e.acct(t, "x")
	in := grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op"}
	bad := func(mut func(*grants.CreateInput), name string) {
		t.Helper()
		x := in
		mut(&x)
		_, _, err := e.g.Create(e.ctx, x)
		if control.Code(err) != "invalid_request" {
			t.Errorf("%s: got %v", name, err)
		}
	}
	bad(func(x *grants.CreateInput) { x.Reason = "  " }, "no reason")
	bad(func(x *grants.CreateInput) { x.Actor = "" }, "no actor")
	bad(func(x *grants.CreateInput) { x.AccountID = "" }, "no account")
	bad(func(x *grants.CreateInput) { x.ExpiresAt = t0.Add(-time.Hour) }, "past expiry")
	bad(func(x *grants.CreateInput) { x.Days = 400 }, "too long")
	bad(func(x *grants.CreateInput) { x.Days = -1 }, "negative days")
	bad(func(x *grants.CreateInput) { x.Key = "ai_helper" }, "unknown key")
	_, _, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: "nope", Reason: "r", Actor: "op"})
	wantCode(t, err, "not_found")
	// exactly the maximum is allowed
	if _, _, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op", Days: 366}); err != nil {
		t.Fatal(err)
	}
}

func TestExistsAndIdempotency(t *testing.T) {
	e := newEnv(t)
	a := e.acct(t, "x")
	g1, _, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	g2, replay, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op", IdempotencyKey: "k1"})
	if err != nil || !replay || g2.ID != g1.ID {
		t.Fatalf("replay: %v %v", replay, err)
	}
	_, _, err = e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op", IdempotencyKey: "k2"})
	wantCode(t, err, "grant_exists")
	// after expiry a new one is fine
	e.clock.Advance(91 * 24 * time.Hour)
	if _, _, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "again", Actor: "op", IdempotencyKey: "k3"}); err != nil {
		t.Fatal(err)
	}
}

func TestCap(t *testing.T) {
	e := newEnv(t)
	e.g.Cfg.MaxActive = 2
	for i := 0; i < 2; i++ {
		a := e.acct(t, "u"+string(rune('a'+i)))
		if _, _, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op"}); err != nil {
			t.Fatal(err)
		}
	}
	a := e.acct(t, "uz")
	_, _, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op"})
	wantCode(t, err, "grant_cap_reached")
	// revoking frees a slot
	list, _ := e.g.List(e.ctx, grants.ListFilter{Status: grants.StatusActive})
	if _, err := e.g.Revoke(e.ctx, list[0].ID, "done", "op"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op"}); err != nil {
		t.Fatal(err)
	}
}

func TestExtendAndRevoke(t *testing.T) {
	e := newEnv(t)
	a := e.acct(t, "x")
	g, _, _ := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op", Days: 30})
	// cannot shorten or keep
	_, err := e.g.Extend(e.ctx, g.ID, day(10), "x", "op")
	wantCode(t, err, "invalid_request")
	_, err = e.g.Extend(e.ctx, g.ID, g.ExpiresAt, "x", "op")
	wantCode(t, err, "invalid_request")
	_, err = e.g.Extend(e.ctx, g.ID, day(60), "", "op")
	wantCode(t, err, "invalid_request")
	got, err := e.g.Extend(e.ctx, g.ID, day(60), "more beta", "op")
	if err != nil || !got.ExpiresAt.Equal(day(60)) {
		t.Fatal(err)
	}
	ents, _ := e.st.EntitlementsForAccount(e.ctx, a.ID)
	if !ents[0].ExpiresAt.Equal(day(60)) {
		t.Fatal("entitlement not extended")
	}
	_, err = e.g.Extend(e.ctx, "missing", day(70), "x", "op")
	wantCode(t, err, "not_found")

	r, err := e.g.Revoke(e.ctx, g.ID, "abuse", "op")
	if err != nil || grants.StatusAt(r, e.clock.Now()) != grants.StatusRevoked || r.RevokedBy != "op" || r.RevokedReason != "abuse" {
		t.Fatalf("%+v %v", r, err)
	}
	ents, _ = e.st.EntitlementsForAccount(e.ctx, a.ID)
	if ents[0].State != control.EntRevoked || ents[0].RevokedAt.IsZero() {
		t.Fatalf("entitlement not revoked: %+v", ents[0])
	}
	_, err = e.g.Revoke(e.ctx, g.ID, "again", "op")
	wantCode(t, err, "invalid_request")
	_, err = e.g.Extend(e.ctx, g.ID, day(80), "x", "op")
	wantCode(t, err, "invalid_request")
	for _, act := range []string{"grant.create", "grant.extend", "grant.revoke"} {
		au, _ := e.st.ListAudit(e.ctx, control.AuditFilter{Action: act})
		if len(au) != 1 {
			t.Errorf("audit %s: %d rows", act, len(au))
		}
	}
}

func TestListFilterAndStatus(t *testing.T) {
	e := newEnv(t)
	a, b := e.acct(t, "a"), e.acct(t, "b")
	ga, _, _ := e.g.Create(e.ctx, grants.CreateInput{AccountID: a.ID, Reason: "r", Actor: "op", Days: 10})
	gb, _, _ := e.g.Create(e.ctx, grants.CreateInput{AccountID: b.ID, Reason: "r", Actor: "op", Days: 100})
	e.g.Revoke(e.ctx, gb.ID, "x", "op")
	e.clock.Advance(11 * 24 * time.Hour)
	if l, _ := e.g.List(e.ctx, grants.ListFilter{Status: grants.StatusExpired}); len(l) != 1 || l[0].ID != ga.ID {
		t.Fatalf("expired: %+v", l)
	}
	if l, _ := e.g.List(e.ctx, grants.ListFilter{Status: grants.StatusRevoked}); len(l) != 1 || l[0].ID != gb.ID {
		t.Fatalf("revoked: %+v", l)
	}
	if l, _ := e.g.List(e.ctx, grants.ListFilter{AccountID: a.ID}); len(l) != 1 {
		t.Fatalf("by account: %+v", l)
	}
	if l, _ := e.g.List(e.ctx, grants.ListFilter{}); len(l) != 2 {
		t.Fatal("all")
	}
}
