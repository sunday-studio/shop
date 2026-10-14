package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/admin"
	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/lifecycle"
)

var t0 = time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	ctx   context.Context
	clock *hclock.Fake
	st    *control.Store
	ctl   *control.Service
	e     admin.Env
	out   *bytes.Buffer
	errb  *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	x := &env{t: t, ctx: context.Background(), clock: hclock.NewFake(t0), out: &bytes.Buffer{}, errb: &bytes.Buffer{}}
	st, err := control.Open(filepath.Join(t.TempDir(), "c.db"), x.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	x.st = st
	calc := billing.NewCalculator(st)
	x.ctl = control.NewService(st, control.DefaultConfig(), calc.AccessChecker())
	x.e = admin.Env{Ctl: x.ctl, Grants: grants.New(x.ctl, grants.DefaultConfig()), Calc: calc, Operator: "casprine", Out: x.out, Err: x.errb,
		Lifecycle: &lifecycle.Runner{S: st, Ctl: x.ctl, Calc: calc, Eraser: &lifecycle.FakeEraser{}, Sink: &lifecycle.MemorySink{}, Cfg: lifecycle.DefaultConfig(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	return x
}

func (x *env) run(args ...string) (int, string, string) {
	x.out.Reset()
	x.errb.Reset()
	code := admin.Run(x.ctx, args, x.e)
	return code, x.out.String(), x.errb.String()
}

func (x *env) account(sub string) control.Account {
	a, _, err := x.ctl.UpsertAccount(x.ctx, sub, sub+"@privaterelay.appleid.com", true, "Friend")
	if err != nil {
		x.t.Fatal(err)
	}
	return a
}

func TestGrantLifecycleThroughTheCLI(t *testing.T) {
	x := newEnv(t)
	a := x.account("friend")
	code, out, errs := x.run("grant", "create", "--support-code", strings.ToLower(a.SupportCode), "--reason", "Friend, beta tester", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var g admin.GrantJSON
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}
	if g.Status != "active" || g.SupportCode != a.SupportCode || g.AccountID != a.ID || g.CreatedBy != "casprine" || g.Key != "hosted" ||
		!g.ExpiresAt.Equal(t0.Add(90*24*time.Hour)) || g.Reason != "Friend, beta tester" || g.RevokedAt != nil {
		t.Fatalf("%+v", g)
	}
	// the account now shows comped
	p, _ := x.e.Calc.ForAccount(x.ctx, a.ID)
	if p.State != billing.StateComped {
		t.Fatalf("plan %s", p.State)
	}
	// list
	code, out, _ = x.run("grant", "list", "--status", "active")
	if code != 0 || !strings.Contains(out, g.ID) || !strings.Contains(out, a.SupportCode) {
		t.Fatalf("%d %s", code, out)
	}
	// extend by days and by date
	code, out, errs = x.run("grant", "extend", g.ID, "--days", "30", "--reason", "still testing")
	if code != 0 || !strings.Contains(out, "2027-02-11T11:00:00Z") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	code, _, errs = x.run("grant", "extend", "--expires-at", "2027-03-01", g.ID, "--reason", "more")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	// cannot shorten
	code, _, errs = x.run("grant", "extend", g.ID, "--expires-at", "2026-12-01", "--reason", "x")
	if code != 1 || !strings.Contains(errs, "extended") {
		t.Fatalf("%d %s", code, errs)
	}
	// revoke
	code, _, errs = x.run("grant", "revoke", g.ID, "--reason", "no longer needed")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	_, out, _ = x.run("grant", "list", "--status", "revoked", "--json")
	var l struct{ Grants []admin.GrantJSON }
	json.Unmarshal([]byte(out), &l)
	if len(l.Grants) != 1 || l.Grants[0].Status != "revoked" || l.Grants[0].RevokedBy == nil || *l.Grants[0].RevokedBy != "casprine" || *l.Grants[0].RevokedReason != "no longer needed" {
		t.Fatalf("%s", out)
	}
	// audit tail shows all three, newest first
	_, out, _ = x.run("audit", "tail", "--subject", a.ID, "--json")
	var au struct {
		Entries []struct{ Action, Actor string }
	}
	json.Unmarshal([]byte(out), &au)
	var actions []string
	for _, e := range au.Entries {
		actions = append(actions, e.Action)
		if e.Actor != "casprine" && e.Action != "account.create" {
			t.Errorf("actor %q for %s", e.Actor, e.Action)
		}
	}
	if strings.Join(actions, ",") != "grant.revoke,grant.extend,grant.extend,grant.create,account.create" {
		t.Fatalf("audit: %v", actions)
	}
	code, out, _ = x.run("audit", "tail", "--action", "grant.create")
	if code != 0 || strings.Count(out, "grant.create") != 1 {
		t.Fatalf("%s", out)
	}
}

func TestGrantCreateValidationAndErrors(t *testing.T) {
	x := newEnv(t)
	a := x.account("friend")
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"grant", "create", "--account-id", a.ID}, 2, "--reason"},
		{[]string{"grant", "create", "--reason", "r"}, 2, "--support-code or --account-id"},
		{[]string{"grant", "create", "--account-id", a.ID, "--reason", "r", "--expires-at", "next tuesday"}, 2, "cannot read"},
		{[]string{"grant", "create", "--account-id", a.ID, "--reason", "r", "--days", "999"}, 1, "maximum"},
		{[]string{"grant", "create", "--account-id", "nope", "--reason", "r"}, 1, "not found"},
		{[]string{"grant", "list", "--status", "weird"}, 2, "--status"},
		{[]string{"grant", "extend", "x", "--reason", "r"}, 2, "exactly one"},
		{[]string{"grant", "revoke", "--reason", "r"}, 2, "argument"},
		{[]string{"grant", "frobnicate"}, 2, "usage"},
		{[]string{"nonsense"}, 2, "usage"},
		{[]string{}, 2, "usage"},
		{[]string{"grant", "create", "--bogus"}, 2, "flag provided but not defined"},
	}
	for _, c := range cases {
		code, _, errs := x.run(c.args...)
		if code != c.code || !strings.Contains(errs, c.msg) {
			t.Errorf("%v: exit %d %q, want %d containing %q", c.args, code, errs, c.code, c.msg)
		}
	}
	// no operator => refused
	x.e.Operator = ""
	code, _, errs := x.run("grant", "create", "--account-id", a.ID, "--reason", "r")
	if code != 2 || !strings.Contains(errs, "operator") {
		t.Fatalf("%d %s", code, errs)
	}
	code, _, _ = x.run("grant", "create", "--account-id", a.ID, "--reason", "r", "--operator", "someone")
	if code != 0 {
		t.Fatal("explicit operator flag should work")
	}
	// the second grant for the same account: grant_exists
	code, _, errs = x.run("grant", "create", "--account-id", a.ID, "--reason", "again", "--operator", "someone")
	if code != 1 || !strings.Contains(errs, "extend it") {
		t.Fatalf("%d %s", code, errs)
	}
	// nothing was written for refused commands
	if gs, _ := x.e.Grants.List(x.ctx, grants.ListFilter{}); len(gs) != 1 {
		t.Fatalf("%d grants", len(gs))
	}
}

func TestIdempotentCreate(t *testing.T) {
	x := newEnv(t)
	a := x.account("friend")
	_, o1, _ := x.run("grant", "create", "--account-id", a.ID, "--reason", "r", "--idempotency-key", "k1", "--json")
	code, o2, _ := x.run("grant", "create", "--account-id", a.ID, "--reason", "r", "--idempotency-key", "k1", "--json")
	var g1, g2 admin.GrantJSON
	json.Unmarshal([]byte(o1), &g1)
	json.Unmarshal([]byte(o2), &g2)
	if code != 0 || g1.ID == "" || g1.ID != g2.ID {
		t.Fatalf("%s | %s", o1, o2)
	}
}

func TestAccountFind(t *testing.T) {
	x := newEnv(t)
	a := x.account("friend")
	x.run("grant", "create", "--account-id", a.ID, "--reason", "r")
	for _, args := range [][]string{{"--support-code", a.SupportCode}, {"--account-id", a.ID}, {"--email", strings.ToUpper(a.Email)}} {
		code, out, errs := x.run(append([]string{"account", "find", "--json"}, args...)...)
		var r struct{ Accounts []admin.AccountJSON }
		json.Unmarshal([]byte(out), &r)
		if code != 0 || len(r.Accounts) != 1 || r.Accounts[0].ID != a.ID || r.Accounts[0].PlanState != "comped" || r.Accounts[0].HouseholdID == nil {
			t.Fatalf("%v: %d %s %s", args, code, out, errs)
		}
	}
	code, out, _ := x.run("account", "find", "--support-code", "ZZZZ-ZZZZ")
	if code != 0 || !strings.Contains(out, "no account found") {
		t.Fatalf("%d %s", code, out)
	}
	if code, _, _ := x.run("account", "find"); code != 2 {
		t.Fatal(code)
	}
}

func TestLifecycleRunCommand(t *testing.T) {
	x := newEnv(t)
	a := x.account("friend")
	x.run("grant", "create", "--account-id", a.ID, "--reason", "r", "--days", "10")
	x.clock.Advance(11 * 24 * time.Hour)
	code, out, errs := x.run("lifecycle", "run", "--dry-run")
	if code != 0 || !strings.Contains(out, "dry run") || !strings.Contains(out, "plan_cache") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	if h, _, _ := x.st.HouseholdOfAccount(x.ctx, a.ID); h.PlanStateCache != "" {
		t.Fatal("dry run wrote")
	}
	code, out, _ = x.run("lifecycle", "run")
	if code != 0 || strings.Contains(out, "dry run") {
		t.Fatalf("%d %s", code, out)
	}
	if h, _, _ := x.st.HouseholdOfAccount(x.ctx, a.ID); h.PlanStateCache != billing.StateLapsedReadOnly {
		t.Fatalf("cache %q", h.PlanStateCache)
	}
	x.e.Lifecycle = nil
	if code, _, _ := x.run("lifecycle", "run"); code != 1 {
		t.Fatal(code)
	}
}
