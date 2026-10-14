package billing_test

import (
	"encoding/json"
	"testing"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
)

func appleEnt(mut func(*control.Entitlement)) control.Entitlement {
	e := control.Entitlement{Source: control.SourceApple, State: control.EntActive, StartsAt: t0, ExpiresAt: t0.Add(days(30)), ProductID: product, Environment: "production", AutoRenew: ptrBool(true)}
	if mut != nil {
		mut(&e)
	}
	return e
}

func grantEnt(expires time.Time, state string) control.Entitlement {
	return control.Entitlement{Source: control.SourceGrant, State: state, ExpiresAt: expires, GrantID: "g"}
}

func compute(now time.Time, ents ...control.Entitlement) billing.Plan {
	return billing.Compute(billing.PlanInput{Now: now, IsOwner: true, HasHousehold: true, HouseholdCreatedAt: t0, Entitlements: ents})
}

func TestComputeStates(t *testing.T) {
	trial := appleEnt(func(e *control.Entitlement) { e.IsTrial = true; e.ExpiresAt = t0.Add(days(14)) })
	cases := []struct {
		name      string
		now       time.Time
		ents      []control.Entitlement
		state     string
		source    string
		readOnly  bool
		delAfter  time.Time
		mustAllow []string
		mustNot   []string
	}{
		{"trial", t0.Add(days(1)), []control.Entitlement{trial}, billing.StateTrial, "apple", false, time.Time{}, []string{"write", "upload", "invite"}, []string{"subscribe"}},
		{"trial turns active at first charge", t0.Add(days(1)), []control.Entitlement{appleEnt(nil)}, billing.StateActive, "apple", false, time.Time{}, []string{"write"}, nil},
		{"active period over, renewal overdue => lapse starts at period end", t0.Add(days(31)), []control.Entitlement{appleEnt(nil)}, billing.StateLapsedReadOnly, "apple", true, t0.Add(days(60)), []string{"read", "export", "subscribe"}, []string{"write", "upload", "invite"}},
		{"grace", t0.Add(days(31)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntGrace; e.GraceUntil = t0.Add(days(46)) })}, billing.StateGrace, "apple", false, time.Time{}, []string{"write", "upload"}, nil},
		{"grace over, retrying", t0.Add(days(47)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntGrace; e.GraceUntil = t0.Add(days(46)) })}, billing.StateBillingRetry, "apple", true, time.Time{}, []string{"read", "export"}, []string{"write", "subscribe"}},
		{"billing retry has no deletion clock", t0.Add(days(200)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntBillingRetry })}, billing.StateBillingRetry, "apple", true, time.Time{}, []string{"read", "export"}, []string{"write"}},
		{"expired: day 0", t0.Add(days(30)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired })}, billing.StateLapsedReadOnly, "apple", true, t0.Add(days(60)), []string{"read", "export"}, []string{"write"}},
		{"expired: day 29", t0.Add(days(59)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired })}, billing.StateLapsedReadOnly, "apple", true, t0.Add(days(60)), nil, nil},
		{"expired: day 30 => pending deletion, 2 more days", t0.Add(days(60)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired })}, billing.StatePendingDeletion, "apple", true, t0.Add(days(62)), []string{"read", "export"}, []string{"write", "subscribe_not_here"}},
		{"expired: long overdue stays pending until the erasure job ran", t0.Add(days(90)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired })}, billing.StatePendingDeletion, "apple", true, t0.Add(days(62)), nil, nil},
		{"refund starts the clock at the refund", t0.Add(days(10)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntRevoked; e.RevokedAt = t0.Add(days(2)) })}, billing.StateLapsedReadOnly, "apple", true, t0.Add(days(32)), nil, nil},
		{"ended_at wins over the period end (after billing retry)", t0.Add(days(70)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired; e.EndedAt = t0.Add(days(55)) })}, billing.StateLapsedReadOnly, "apple", true, t0.Add(days(85)), nil, nil},
		{"grant", t0.Add(days(10)), []control.Entitlement{grantEnt(t0.Add(days(90)), control.EntActive)}, billing.StateComped, "grant", false, time.Time{}, []string{"write", "subscribe"}, nil},
		{"grant over => lapse", t0.Add(days(95)), []control.Entitlement{grantEnt(t0.Add(days(90)), control.EntActive)}, billing.StateLapsedReadOnly, "grant", true, t0.Add(days(120)), nil, nil},
		{"grant revoked => lapse from the revocation", t0.Add(days(10)), []control.Entitlement{func() control.Entitlement {
			g := grantEnt(t0.Add(days(90)), control.EntRevoked)
			g.RevokedAt = t0.Add(days(5))
			return g
		}()}, billing.StateLapsedReadOnly, "grant", true, t0.Add(days(35)), nil, nil},
		{"grant on top of paying apple: apple shown while it renews", t0.Add(days(10)), []control.Entitlement{appleEnt(nil), grantEnt(t0.Add(days(90)), control.EntActive)}, billing.StateActive, "apple", false, time.Time{}, nil, nil},
		{"grant outlasts a cancelled apple", t0.Add(days(10)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.AutoRenew = ptrBool(false) }), grantEnt(t0.Add(days(90)), control.EntActive)}, billing.StateComped, "grant", false, time.Time{}, nil, nil},
		{"apple outlasts a grant", t0.Add(days(10)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.AutoRenew = ptrBool(false); e.ExpiresAt = t0.Add(days(200)) }), grantEnt(t0.Add(days(90)), control.EntActive)}, billing.StateActive, "apple", false, time.Time{}, nil, nil},
		{"grant rescues a lapsed household", t0.Add(days(40)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired }), grantEnt(t0.Add(days(130)), control.EntActive)}, billing.StateComped, "grant", false, time.Time{}, nil, nil},
		{"billing retry plus a grant: grant is full", t0.Add(days(40)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntBillingRetry }), grantEnt(t0.Add(days(130)), control.EntActive)}, billing.StateComped, "grant", false, time.Time{}, nil, nil},
		{"resubscribe after lapse", t0.Add(days(40)), []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired }), appleEnt(func(e *control.Entitlement) { e.ExpiresAt = t0.Add(days(70)); e.OriginalTransactionID = "o2" })}, billing.StateActive, "apple", false, time.Time{}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := compute(c.now, c.ents...)
			if p.State != c.state {
				t.Fatalf("state %s, want %s (%+v)", p.State, c.state, p)
			}
			if c.source != "" && (p.EntitlementSource == nil || *p.EntitlementSource != c.source) {
				t.Errorf("source %v, want %s", p.EntitlementSource, c.source)
			}
			if p.ReadOnly != c.readOnly {
				t.Errorf("read_only %v", p.ReadOnly)
			}
			if !c.delAfter.IsZero() && (p.DeleteAfter == nil || !p.DeleteAfter.Equal(c.delAfter)) {
				t.Errorf("delete_after %v, want %v", p.DeleteAfter, c.delAfter)
			}
			if c.delAfter.IsZero() && p.DeleteAfter != nil {
				t.Errorf("unexpected delete_after %v", p.DeleteAfter)
			}
			for _, a := range c.mustAllow {
				if !p.Can(a) {
					t.Errorf("missing action %s in %v", a, p.Allowed)
				}
			}
			for _, a := range c.mustNot {
				if p.Can(a) {
					t.Errorf("unexpected action %s in %v", a, p.Allowed)
				}
			}
			// write and upload only ever appear together with a non-read-only plan
			if p.ReadOnly && (p.Can("write") || p.Can("upload") || p.Can("invite")) {
				t.Errorf("read-only plan allows writes: %v", p.Allowed)
			}
		})
	}
}

func TestComputeNoneDeletedAndUserDeletion(t *testing.T) {
	p := billing.Compute(billing.PlanInput{Now: t0, IsOwner: true})
	if p.State != billing.StateNone || !p.ReadOnly || p.Can("read") || !p.Can("subscribe") || !p.Can("join_household") || p.EntitlementSource != nil {
		t.Fatalf("%+v", p)
	}
	d := billing.Compute(billing.PlanInput{Now: t0, IsOwner: true, HouseholdDeleted: true, Entitlements: []control.Entitlement{appleEnt(func(e *control.Entitlement) { e.State = control.EntExpired })}})
	if d.State != billing.StateDeleted || d.Can("read") || !d.Can("subscribe") {
		t.Fatalf("%+v", d)
	}
	// user deletion request beats an active subscription
	req := t0.Add(days(1))
	u := billing.Compute(billing.PlanInput{Now: t0.Add(days(2)), IsOwner: true, HasHousehold: true, Entitlements: []control.Entitlement{appleEnt(nil)}, HouseholdDeletionRequestedAt: req, AccountDeletionRequested: true})
	if u.State != billing.StatePendingDeletion || !u.DeletionRequested || u.DeleteAfter == nil || !u.DeleteAfter.Equal(req.Add(days(7))) || !u.Can("cancel_deletion") || u.Can("write") || !u.Can("export") {
		t.Fatalf("%+v", u)
	}
	// a member of that household sees pending deletion but cannot cancel it
	m := billing.Compute(billing.PlanInput{Now: t0.Add(days(2)), IsOwner: false, HasHousehold: true, Entitlements: []control.Entitlement{appleEnt(nil)}, HouseholdDeletionRequestedAt: req})
	if m.State != billing.StatePendingDeletion || m.DeletionRequested || m.Can("cancel_deletion") || !m.Can("leave_household") {
		t.Fatalf("%+v", m)
	}
	// forced lapse (Apple account-deleted, plan-less transfer) starts the clock
	f := billing.Compute(billing.PlanInput{Now: t0.Add(days(5)), IsOwner: true, HasHousehold: true, ForcedLapseAt: t0.Add(days(2)), HouseholdCreatedAt: t0})
	if f.State != billing.StateLapsedReadOnly || !f.DeleteAfter.Equal(t0.Add(days(32))) {
		t.Fatalf("%+v", f)
	}
	// household without any entitlement lapses from its creation
	h := billing.Compute(billing.PlanInput{Now: t0.Add(days(5)), IsOwner: true, HasHousehold: true, HouseholdCreatedAt: t0})
	if h.State != billing.StateLapsedReadOnly {
		t.Fatalf("%+v", h)
	}
}

func TestComputeMemberVersusOwnerActions(t *testing.T) {
	ents := []control.Entitlement{appleEnt(nil)}
	own := billing.Compute(billing.PlanInput{Now: t0.Add(days(1)), IsOwner: true, HasHousehold: true, Entitlements: ents})
	mem := billing.Compute(billing.PlanInput{Now: t0.Add(days(1)), IsOwner: false, HasHousehold: true, Entitlements: ents})
	for _, a := range []string{"invite", "manage_household", "subscribe", "cancel_deletion"} {
		if mem.Can(a) {
			t.Errorf("member can %s", a)
		}
	}
	if own.Can("leave_household") || !mem.Can("leave_household") || !mem.Can("write") || mem.IsOwner || !own.IsOwner {
		t.Fatalf("own %v mem %v", own.Allowed, mem.Allowed)
	}
	// member of a lapsed household: read-only, "ask the owner to renew" => is_owner false, no subscribe
	lap := billing.Compute(billing.PlanInput{Now: t0.Add(days(40)), IsOwner: false, HasHousehold: true, Entitlements: ents})
	if lap.State != billing.StateLapsedReadOnly || lap.IsOwner || lap.Can("subscribe") || !lap.Can("export") {
		t.Fatalf("%+v", lap)
	}
}

func TestPlanJSONShape(t *testing.T) {
	p := compute(t0.Add(days(1)), appleEnt(func(e *control.Entitlement) { e.IsTrial = true; e.ExpiresAt = t0.Add(days(14)) }))
	b, _ := json.Marshal(p)
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"state", "entitlement_source", "environment", "is_owner", "read_only", "product_id", "trial_ends_at", "renews_at", "ends_at", "grace_until", "auto_renew", "read_only_since", "delete_after", "deletion_requested", "manage_subscription_url", "allowed"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
	if m["trial_ends_at"] != "2026-10-28T09:30:00Z" || m["grace_until"] != nil || m["state"] != "trial" {
		t.Fatalf("%s", b)
	}
	if got := len(billing.StateTable()); got != 9 {
		t.Fatalf("%d states", got)
	}
}

func TestCalculatorMemberSeesOwnersPlan(t *testing.T) {
	e := newEnv(t)
	owner, member := e.account("o"), e.account("m")
	e.process(e.notif(billing.NtSubscribed, "INITIAL_BUY", "n1", t0, e.tx(owner, "otx"), ren(1)))
	code := func() string { _, c, err := e.ctl.CreateInvite(e.ctx, owner.ID); must(t, err); return c }()
	if _, err := e.ctl.AcceptInvite(e.ctx, member.ID, code); err != nil {
		t.Fatal(err)
	}
	mp := e.plan(member)
	if mp.State != billing.StateTrial || mp.IsOwner || mp.Can("invite") || !mp.Can("write") {
		t.Fatalf("%+v", mp)
	}
	// HasFullAccess uses the account's own entitlements
	ok, _ := e.calc.HasFullAccess(e.ctx, owner.ID, t0.Add(days(1)))
	no, _ := e.calc.HasFullAccess(e.ctx, member.ID, t0.Add(days(1)))
	if !ok || no {
		t.Fatalf("has access: owner=%v member=%v", ok, no)
	}
	// owner's trial ends without renewal
	e.process(e.notif(billing.NtExpired, "VOLUNTARY", "n2", t0.Add(days(14)), e.tx(owner, "otx"), ren(0)))
	e.clock.Set(t0.Add(days(15)))
	mp = e.plan(member)
	if mp.State != billing.StateLapsedReadOnly || mp.IsOwner || mp.Can("write") || !mp.Can("export") {
		t.Fatalf("%+v", mp)
	}
	// an account with no household and no entitlements is "none"
	if p := e.plan(e.account("nobody")); p.State != billing.StateNone {
		t.Fatalf("%s", p.State)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
