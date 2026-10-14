package billing_test

import (
	"testing"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
)

// A production server that accepts the sandbox (the default): sandbox notifications and signed
// transactions work, the entitlement is stored as sandbox and flagged in the plan.
func TestAcceptSandboxOnProductionServer(t *testing.T) {
	e := newEnv(t)
	e.svc.Cfg.ProductionOnly, e.svc.Cfg.AcceptSandbox = true, true
	a := e.account("ama")
	sandbox := e.tx(a, "otx-sb")
	if r := e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "n-sb", t0, sandbox, ren(1))); r.Outcome != billing.OutcomeApplied {
		t.Fatalf("sandbox notification: %+v", r)
	}
	if got := e.ent("otx-sb"); got.Environment != "sandbox" || !billing.IsSandbox(got) {
		t.Fatalf("environment %q", got.Environment)
	}
	p := e.plan(a)
	if p.State != billing.StateTrial || p.Environment == nil || *p.Environment != "sandbox" {
		t.Fatalf("plan %+v", p)
	}

	b := e.account("kofi")
	tx := e.tx(b, "otx-bind")
	e.statusFor(tx, 1)
	res, err := e.svc.BindTransaction(e.ctx, b.ID, e.sign(tx))
	if err != nil || res.Entitlement.Environment != "sandbox" {
		t.Fatalf("bind sandbox transaction: %+v %v", res, err)
	}
	// data with no environment at all is still refused (it would be stored as production)
	c := e.account("esi")
	none := e.tx(c, "otx-none", func(x *billing.TransactionInfo) { x.Environment = "" })
	if _, err := e.svc.BindTransaction(e.ctx, c.ID, e.sign(none)); !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("unlabelled transaction: %v", err)
	}
}

// With SHOP_APPLE_ACCEPT_SANDBOX=false the review's strict behaviour applies (see also
// TestProductionOnlyIgnoresSandboxData).
func TestRejectSandboxWhenSwitchedOff(t *testing.T) {
	e := newEnv(t)
	e.svc.Cfg.ProductionOnly, e.svc.Cfg.AcceptSandbox = true, false
	a := e.account("ama")
	sandbox := e.tx(a, "otx-sb")
	if r := e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "n-sb", t0, sandbox, ren(1))); r.Outcome != billing.OutcomeIgnored {
		t.Fatalf("sandbox notification: %+v", r)
	}
	if _, err := e.svc.BindTransaction(e.ctx, a.ID, e.sign(sandbox)); !control.Is(err, billing.ErrTransactionInvalid) {
		t.Fatalf("sandbox transaction: %v", err)
	}
	if p := e.plan(a); p.State != billing.StateNone {
		t.Fatalf("plan %s", p.State)
	}
}

func sandboxEnt(start time.Time, expires time.Time) control.Entitlement {
	t := true
	return control.Entitlement{AccountID: "a", Source: control.SourceApple, State: control.EntActive, Environment: "sandbox",
		StartsAt: start, ExpiresAt: expires, AutoRenew: &t, ProductID: product}
}

// A sandbox subscription renews every few minutes; its access ends 30 days after the first
// purchase whatever Apple says, and it does not keep renewing in the plan.
func TestSandboxLifetimeCap(t *testing.T) {
	now := t0.Add(days(10))
	in := billing.PlanInput{Now: now, IsOwner: true, HasHousehold: true, HouseholdCreatedAt: t0, SandboxMax: billing.DefaultSandboxMax,
		Entitlements: []control.Entitlement{sandboxEnt(t0, now.Add(days(300)))}}
	p := billing.Compute(in)
	if p.State != billing.StateActive || p.EndsAt == nil || !p.EndsAt.Equal(t0.Add(days(30))) || p.RenewsAt != nil {
		t.Fatalf("inside the cap: %+v", p)
	}
	in.Now = t0.Add(days(31))
	if p := billing.Compute(in); p.State != billing.StateLapsedReadOnly {
		t.Fatalf("after the cap: %s", p.State)
	}
	// the cap is configurable; 0 turns it off
	in.SandboxMax = 7 * 24 * time.Hour
	in.Now = t0.Add(days(8))
	if p := billing.Compute(in); p.State != billing.StateLapsedReadOnly {
		t.Fatalf("7 day cap: %s", p.State)
	}
	in.SandboxMax = 0
	if p := billing.Compute(in); p.State != billing.StateActive {
		t.Fatalf("no cap: %s", p.State)
	}
}

// Production wins: with a production subscription on the account a longer sandbox one is
// ignored, also after the production one lapsed; without one the sandbox one counts.
func TestProductionWinsOverSandbox(t *testing.T) {
	now := t0.Add(days(40))
	prod := sandboxEnt(t0, t0.Add(days(35)))
	prod.Environment, prod.State = "production", control.EntExpired
	prod.EndedAt = prod.ExpiresAt
	sb := sandboxEnt(t0.Add(days(38)), now.Add(days(20)))
	in := billing.PlanInput{Now: now, IsOwner: true, HasHousehold: true, HouseholdCreatedAt: t0, SandboxMax: billing.DefaultSandboxMax,
		Entitlements: []control.Entitlement{sb, prod}}
	p := billing.Compute(in)
	if p.State != billing.StateLapsedReadOnly || p.Environment == nil || *p.Environment != "production" {
		t.Fatalf("production lapsed, sandbox active: %+v", p)
	}
	in.Entitlements = []control.Entitlement{sb}
	if p := billing.Compute(in); p.State != billing.StateActive || *p.Environment != "sandbox" {
		t.Fatalf("sandbox alone: %+v", p)
	}
	// the transfer rule (HasFullAccess) follows the same rules: no full access from a lapsed
	// production plus a sandbox
}

func TestSandboxNeverCountsAsRevenue(t *testing.T) {
	now := t0.Add(days(1))
	prod := sandboxEnt(t0, now.Add(days(20)))
	prod.Environment = "production"
	if !billing.CountsAsRevenue(prod, now) {
		t.Fatal("a running production subscription pays")
	}
	trial := prod
	trial.IsTrial = true
	sb := prod
	sb.Environment = "sandbox"
	grant := prod
	grant.Source = control.SourceGrant
	for name, x := range map[string]control.Entitlement{"trial": trial, "sandbox": sb, "grant": grant} {
		if billing.CountsAsRevenue(x, now) {
			t.Fatalf("%s must not count as revenue", name)
		}
	}
}
