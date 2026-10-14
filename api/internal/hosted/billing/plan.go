package billing

import (
	"context"
	"errors"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/hosted/grants"
	"shop/internal/hosted/hclock"
)

// Plan states (contract section 7).
const (
	StateNone            = "none"
	StateTrial           = "trial"
	StateActive          = "active"
	StateGrace           = "grace"
	StateBillingRetry    = "billing_retry"
	StateComped          = "comped"
	StateLapsedReadOnly  = "lapsed_read_only"
	StatePendingDeletion = "pending_deletion"
	StateDeleted         = "deleted"
)

// Allowed action vocabulary (contract section 7.2).
const (
	ActRead            = "read"
	ActWrite           = "write"
	ActUpload          = "upload"
	ActExport          = "export"
	ActInvite          = "invite"
	ActManageHousehold = "manage_household"
	ActLeaveHousehold  = "leave_household"
	ActSubscribe       = "subscribe"
	ActJoinHousehold   = "join_household"
	ActCancelDeletion  = "cancel_deletion"
	ActDeleteAccount   = "delete_account"
)

// Plan is the contract's plan object. Nullable times are pointers (JSON null).
type Plan struct {
	State              string     `json:"state"`
	EntitlementSource  *string    `json:"entitlement_source"`
	Environment        *string    `json:"environment"`
	IsOwner            bool       `json:"is_owner"`
	ReadOnly           bool       `json:"read_only"`
	ProductID          *string    `json:"product_id"`
	TrialEndsAt        *time.Time `json:"trial_ends_at"`
	RenewsAt           *time.Time `json:"renews_at"`
	EndsAt             *time.Time `json:"ends_at"`
	GraceUntil         *time.Time `json:"grace_until"`
	AutoRenew          *bool      `json:"auto_renew"`
	ReadOnlySince      *time.Time `json:"read_only_since"`
	DeleteAfter        *time.Time `json:"delete_after"`
	DeletionRequested  bool       `json:"deletion_requested"`
	ManageSubscription string     `json:"manage_subscription_url"`
	Allowed            []string   `json:"allowed"`
}

// Can reports whether the plan allows an action.
func (p Plan) Can(action string) bool {
	for _, a := range p.Allowed {
		if a == action {
			return true
		}
	}
	return false
}

// Timeline holds the lapse timing. Defaults follow the owner decision: 30 days read-only plus
// export, then 2 days pending deletion; a user's own deletion request waits 7 days.
type Timeline struct {
	ReadOnly   time.Duration
	Pending    time.Duration
	CoolingOff time.Duration
}

// DefaultTimeline returns 30 days, 2 days and 7 days.
func DefaultTimeline() Timeline {
	return Timeline{ReadOnly: 30 * 24 * time.Hour, Pending: 48 * time.Hour, CoolingOff: 7 * 24 * time.Hour}
}

// ManageSubscriptionURL is Apple's page for managing subscriptions.
const ManageSubscriptionURL = "https://apps.apple.com/account/subscriptions"

// PlanInput is everything Compute needs. It is plain data so any moment can be evaluated.
type PlanInput struct {
	Now                          time.Time
	Timeline                     Timeline
	IsOwner                      bool // false for a member of the household; true when there is no household
	HasHousehold                 bool
	HouseholdDeleted             bool                  // the household's data was erased (stage 2 done)
	HouseholdCreatedAt           time.Time             // anchor when an owner has no entitlement at all
	Entitlements                 []control.Entitlement // of the household owner (or of the account without a household)
	ForcedLapseAt                time.Time             // Apple account-deleted, plan-less ownership transfer
	HouseholdDeletionRequestedAt time.Time             // the owner asked to delete the account
	AccountDeletionRequested     bool                  // the caller asked (for plan.deletion_requested)
}

// access describes what one source gives now and when it ended.
type access struct {
	grants.Access
	ent     *control.Entitlement
	endedAt time.Time // when full access stopped (zero = still running or not applicable)
	retry   bool      // billing retry: no deletion clock
}

func appleAccess(e control.Entitlement, now time.Time) access {
	a := access{ent: &e}
	a.Source = "apple"
	renewing := e.AutoRenew != nil && *e.AutoRenew
	signedAt := time.Time{}
	if e.LastSignedMs > 0 {
		signedAt = time.UnixMilli(e.LastSignedMs).UTC()
	}
	switch e.State {
	case control.EntActive:
		if now.Before(e.ExpiresAt) {
			a.Level, a.Until, a.Renewing = grants.LevelFull, e.ExpiresAt, renewing
		} else {
			a.endedAt = e.ExpiresAt // renewal overdue: ends at the period end, comes back with DID_RENEW
		}
	case control.EntGrace:
		until := e.GraceUntil
		if until.IsZero() || until.Before(e.ExpiresAt) {
			until = e.ExpiresAt
		}
		if now.Before(until) {
			a.Level, a.Until = grants.LevelFull, until
		} else {
			a.Level, a.retry = grants.LevelReadOnly, true // grace over, Apple still retrying
			a.endedAt = until
		}
	case control.EntBillingRetry:
		a.Level, a.retry = grants.LevelReadOnly, true
		a.endedAt = e.ExpiresAt
		if !e.GraceUntil.IsZero() && e.GraceUntil.After(e.ExpiresAt) {
			a.endedAt = e.GraceUntil
		}
	case control.EntExpired:
		a.endedAt = e.EndedAt
		if a.endedAt.IsZero() {
			a.endedAt = e.ExpiresAt
		}
	case control.EntRevoked:
		a.endedAt = e.RevokedAt
		if a.endedAt.IsZero() {
			a.endedAt = signedAt
		}
	}
	return a
}

func grantAccess(e control.Entitlement, now time.Time) access {
	a := access{ent: &e}
	a.Source = "grant"
	end := e.ExpiresAt
	if e.State == control.EntRevoked && !e.RevokedAt.IsZero() && e.RevokedAt.Before(end) {
		end = e.RevokedAt
	}
	if e.State != control.EntRevoked && now.Before(end) {
		a.Level, a.Until = grants.LevelFull, end
		return a
	}
	a.endedAt = end
	return a
}

// better picks the stronger of two same-source accesses (level, then later end).
func better(a, b access) access {
	if a.ent == nil {
		return b
	}
	if b.ent == nil {
		return a
	}
	if a.Level != b.Level {
		if a.Level > b.Level {
			return a
		}
		return b
	}
	if a.Level == grants.LevelNone {
		if b.endedAt.After(a.endedAt) {
			return b
		}
		return a
	}
	if b.Renewing && !a.Renewing || b.Until.After(a.Until) && !a.Renewing {
		return b
	}
	return a
}

// Compute is the pure plan calculator: the state and the allowed actions for any account at
// any time, derived from stored dates (never from "the last notification").
//
// Lapse timeline: when full access stops (Apple period end, grace end, refund time, grant end,
// forced lapse) at E, the household is lapsed_read_only until E+30d, pending_deletion until
// E+32d (and stays pending_deletion until the erasure job has run), then deleted once the
// household's data is gone. Billing retry is read-only without a deletion clock.
// A user's own deletion request overrides everything: pending_deletion for 7 days.
func Compute(in PlanInput) Plan {
	tl := in.Timeline
	if tl.ReadOnly == 0 {
		tl = DefaultTimeline()
	}
	now := in.Now
	p := Plan{IsOwner: in.IsOwner, ManageSubscription: ManageSubscriptionURL, DeletionRequested: in.AccountDeletionRequested}
	finish := func() Plan {
		p.ReadOnly = !(p.State == StateTrial || p.State == StateActive || p.State == StateGrace || p.State == StateComped)
		p.Allowed = allowedFor(p.State, in.IsOwner, in.HasHousehold, !in.HouseholdDeletionRequestedAt.IsZero())
		return p
	}

	var apple, grant access
	for _, e := range in.Entitlements {
		switch e.Source {
		case control.SourceApple:
			apple = better(apple, appleAccess(e, now))
		case control.SourceGrant:
			grant = better(grant, grantAccess(e, now))
		}
	}
	var lastEnded time.Time
	for _, a := range []access{apple, grant} {
		if a.endedAt.After(lastEnded) {
			lastEnded = a.endedAt
		}
	}
	if in.ForcedLapseAt.After(lastEnded) {
		lastEnded = in.ForcedLapseAt
	}

	if in.HouseholdDeleted {
		p.State = StateDeleted
		return finish()
	}
	if !in.HouseholdDeletionRequestedAt.IsZero() && in.HasHousehold {
		p.State = StatePendingDeletion
		t := in.HouseholdDeletionRequestedAt
		p.ReadOnlySince = &t
		d := t.Add(tl.CoolingOff)
		p.DeleteAfter = &d
		return finish()
	}

	best := grants.Combine(apple.Access, grant.Access)
	switch {
	case best.Level == grants.LevelFull && best.Source == "grant":
		p.State = StateComped
		src := "grant"
		p.EntitlementSource = &src
		end := best.Until
		p.EndsAt = &end
		return finish()
	case best.Level == grants.LevelFull:
		e := apple.ent
		src := "apple"
		p.EntitlementSource = &src
		setAppleFields(&p, e)
		switch {
		case e.State == control.EntGrace && !now.Before(e.ExpiresAt):
			p.State = StateGrace
			g := best.Until
			p.GraceUntil = &g
			p.EndsAt = &g
			p.RenewsAt = nil
		case e.IsTrial && now.Before(e.ExpiresAt):
			p.State = StateTrial
			t := e.ExpiresAt
			p.TrialEndsAt = &t
		default:
			p.State = StateActive
		}
		if p.State != StateGrace {
			end := e.ExpiresAt
			p.EndsAt = &end
			if e.AutoRenew != nil && *e.AutoRenew {
				r := e.ExpiresAt
				p.RenewsAt = &r
			}
		}
		return finish()
	case best.Level == grants.LevelReadOnly:
		e := apple.ent
		src := "apple"
		p.EntitlementSource = &src
		setAppleFields(&p, e)
		p.State = StateBillingRetry
		if !apple.endedAt.IsZero() {
			t := apple.endedAt
			p.ReadOnlySince = &t
		}
		return finish()
	}

	// No full access and no billing retry: nothing, or a lapse.
	if lastEnded.IsZero() && in.HasHousehold && !in.HouseholdCreatedAt.IsZero() {
		lastEnded = in.HouseholdCreatedAt // a household with no entitlement at all lapses from its creation
	}
	if lastEnded.IsZero() || !in.HasHousehold {
		p.State = StateNone
		return finish()
	}
	// Remember where the last access came from for display.
	if apple.ent != nil || grant.ent != nil {
		src := "apple"
		if grant.endedAt.After(apple.endedAt) {
			src = "grant"
		}
		p.EntitlementSource = &src
		if src == "apple" && apple.ent != nil {
			setAppleFields(&p, apple.ent)
		}
	}
	since := lastEnded
	p.ReadOnlySince = &since
	readOnlyEnd := since.Add(tl.ReadOnly)
	if now.Before(readOnlyEnd) {
		p.State = StateLapsedReadOnly
		d := readOnlyEnd
		p.DeleteAfter = &d
	} else {
		p.State = StatePendingDeletion
		d := readOnlyEnd.Add(tl.Pending)
		p.DeleteAfter = &d
	}
	e := since
	p.EndsAt = &e
	return finish()
}

func setAppleFields(p *Plan, e *control.Entitlement) {
	if e == nil {
		return
	}
	if e.ProductID != "" {
		pid := e.ProductID
		p.ProductID = &pid
	}
	if e.Environment != "" {
		env := e.Environment
		p.Environment = &env
	}
	p.AutoRenew = e.AutoRenew
}

func allowedFor(state string, owner, hasHousehold, userDeletion bool) []string {
	member := hasHousehold && !owner
	var a []string
	full := []string{ActRead, ActWrite, ActUpload, ActExport}
	switch state {
	case StateTrial, StateActive, StateGrace, StateComped:
		a = append(a, full...)
		if owner {
			a = append(a, ActInvite, ActManageHousehold)
		} else {
			a = append(a, ActLeaveHousehold)
		}
		if state == StateComped && owner {
			a = append(a, ActSubscribe)
		}
		a = append(a, ActDeleteAccount)
	case StateBillingRetry:
		a = append(a, ActRead, ActExport)
		if member {
			a = append(a, ActLeaveHousehold)
		}
		a = append(a, ActDeleteAccount)
	case StateLapsedReadOnly:
		a = append(a, ActRead, ActExport)
		if member {
			a = append(a, ActLeaveHousehold)
		} else {
			a = append(a, ActSubscribe)
		}
		a = append(a, ActDeleteAccount)
	case StatePendingDeletion:
		a = append(a, ActRead, ActExport)
		if member {
			a = append(a, ActLeaveHousehold)
		} else {
			a = append(a, ActSubscribe)
			if userDeletion {
				a = append(a, ActCancelDeletion)
			}
		}
	default: // none, deleted
		a = append(a, ActJoinHousehold, ActSubscribe, ActDeleteAccount)
	}
	return a
}

// StateInfo is one row of the static table behind GET /v1/billing/plan-states.
type StateInfo struct {
	State            string   `json:"state"`
	ReadOnly         bool     `json:"read_only"`
	Allowed          []string `json:"allowed"`
	HasHouseholdData bool     `json:"has_household_data"`
}

// StateTable returns the contract's master list (owner view).
func StateTable() []StateInfo {
	std := []string{ActRead, ActWrite, ActUpload, ActExport, ActInvite, ActManageHousehold, ActLeaveHousehold, ActDeleteAccount}
	return []StateInfo{
		{StateNone, true, []string{ActJoinHousehold, ActSubscribe, ActDeleteAccount}, false},
		{StateTrial, false, std, true},
		{StateActive, false, std, true},
		{StateGrace, false, std, true},
		{StateBillingRetry, true, []string{ActRead, ActExport, ActLeaveHousehold, ActDeleteAccount}, true},
		{StateComped, false, []string{ActRead, ActWrite, ActUpload, ActExport, ActInvite, ActManageHousehold, ActLeaveHousehold, ActSubscribe, ActDeleteAccount}, true},
		{StateLapsedReadOnly, true, []string{ActRead, ActExport, ActLeaveHousehold, ActSubscribe, ActDeleteAccount}, true},
		{StatePendingDeletion, true, []string{ActRead, ActExport, ActLeaveHousehold, ActSubscribe, ActCancelDeletion}, true},
		{StateDeleted, true, []string{ActJoinHousehold, ActSubscribe, ActDeleteAccount}, false},
	}
}

// Calculator loads the inputs for an account from the control database and runs Compute.
type Calculator struct {
	S        *control.Store
	Clock    hclock.Clock
	Timeline Timeline
}

// NewCalculator builds a Calculator with the default timeline.
func NewCalculator(s *control.Store) *Calculator {
	return &Calculator{S: s, Clock: s.Clock(), Timeline: DefaultTimeline()}
}

// Input gathers the PlanInput for an account at the calculator's current time.
func (c *Calculator) Input(ctx context.Context, accountID string) (PlanInput, error) {
	acc, err := c.S.AccountByID(ctx, accountID)
	if err != nil {
		return PlanInput{}, err
	}
	in := PlanInput{Now: c.Clock.Now().UTC(), Timeline: c.Timeline, IsOwner: true, AccountDeletionRequested: !acc.DeletionRequestedAt.IsZero()}
	h, m, err := c.S.HouseholdOfAccount(ctx, accountID)
	switch {
	case err == nil:
		in.HasHousehold = true
		in.IsOwner = m.Role == control.RoleOwner
		in.HouseholdCreatedAt = h.CreatedAt
		in.ForcedLapseAt = h.ForcedLapseAt
		in.HouseholdDeletionRequestedAt = h.DeletionRequestedAt
		in.HouseholdDeleted = h.Status == control.HouseholdDeleted
		in.Entitlements, err = c.S.EntitlementsForAccount(ctx, h.OwnerAccountID)
		if err != nil {
			return in, err
		}
	case errors.Is(err, control.ErrNoHousehold):
		in.Entitlements, err = c.S.EntitlementsForAccount(ctx, accountID)
		if err != nil {
			return in, err
		}
		// An owner whose household was erased: no membership remains, the household row says deleted.
		if last, lerr := c.S.LatestOwnedHousehold(ctx, accountID); lerr == nil && last.Status == control.HouseholdDeleted {
			in.HouseholdDeleted = true
		}
	default:
		return in, err
	}
	return in, nil
}

// ForAccount computes the plan of an account (a member gets the household's plan).
func (c *Calculator) ForAccount(ctx context.Context, accountID string) (Plan, error) {
	in, err := c.Input(ctx, accountID)
	if err != nil {
		return Plan{}, err
	}
	return Compute(in), nil
}

// HasFullAccess reports whether the account's own entitlements give full access at now
// (the transfer rule: does the new owner have a plan?).
func (c *Calculator) HasFullAccess(ctx context.Context, accountID string, now time.Time) (bool, error) {
	ents, err := c.S.EntitlementsForAccount(ctx, accountID)
	if err != nil {
		return false, err
	}
	for _, e := range ents {
		var a access
		if e.Source == control.SourceApple {
			a = appleAccess(e, now)
		} else {
			a = grantAccess(e, now)
		}
		if a.Level == grants.LevelFull {
			return true, nil
		}
	}
	return false, nil
}

// AccessChecker adapts HasFullAccess for control.NewService.
func (c *Calculator) AccessChecker() control.AccessChecker {
	return c.HasFullAccess
}
