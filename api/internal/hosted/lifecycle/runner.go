// Package lifecycle holds the periodic jobs of the hosted mode as plain functions of the
// control database and an injected clock: plan-state transitions (lapse), lapse reminders,
// the two-stage household deletion (tombstone, then erase), final removal of accounts whose
// owner asked to delete them (7 day cooling-off), the retry of Apple token revocations, and the
// data erasure plan. Every job is idempotent and has a dry-run mode that changes nothing.
//
// Deletion safeguards (hosted plan 4.7): a kill switch, a cap per run, a fresh Apple check
// for the lapse path (no check, no deletion), no deletion unless the plan computed right now still
// says pending_deletion, a minimum gap between the two stages, and an audit row for every step.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"shop/internal/hosted/billing"
	"shop/internal/hosted/control"
)

// Reconciler re-reads an account's subscriptions from Apple (billing.Service satisfies it).
type Reconciler interface {
	Refresh(ctx context.Context, accountID string) (int, error)
}

// RevocationRetrier retries queued Apple token revocations (appleauth.Service satisfies it).
type RevocationRetrier interface {
	RetryRevocations(ctx context.Context) (done, failed int, err error)
}

// Config of the jobs.
type Config struct {
	KillSwitch      bool          // true: no tombstone, erasure or account removal happens
	MaxTombstones   int           // stage 1 per run (default 5)
	MaxErasures     int           // stage 2 and account removals per run (default 5)
	MinStageGap     time.Duration // minimum time between stage 1 and stage 2 (default 24 h)
	ReminderDays    []int         // days after read-only starts (default 0, 14, 25, 29)
	CoolingOff      time.Duration // account deletion cooling-off (default 7 days)
	PurgeNonceAfter time.Duration // default 24 h
}

// DefaultConfig returns the launch values.
func DefaultConfig() Config {
	return Config{MaxTombstones: 5, MaxErasures: 5, MinStageGap: 24 * time.Hour, ReminderDays: []int{0, 14, 25, 29},
		CoolingOff: 7 * 24 * time.Hour, PurgeNonceAfter: 24 * time.Hour}
}

// Runner runs the jobs.
type Runner struct {
	S       *control.Store
	Ctl     *control.Service
	Calc    *billing.Calculator
	Apple   Reconciler        // nil: the lapse path refuses to delete (no fresh check possible)
	Revoker RevocationRetrier // optional
	Eraser  Eraser
	Layout  Layout
	Sink    ReminderSink
	Cfg     Config
	Log     *slog.Logger
}

// Action kinds in a Report.
const (
	ActPlanCache    = "plan_cache"    // cached plan state/dates updated
	ActReminder     = "reminder"      // reminder emitted
	ActReminderSkip = "reminder_skip" // older reminder marked done without sending
	ActTombstone    = "tombstone"     // stage 1
	ActErase        = "erase"         // stage 2
	ActUntombstone  = "untombstone"   // recovered before stage 2
	ActAccountErase = "account_erase" // account personal data removed
	ActRevocations  = "revocations"   // Apple token revocations retried
	ActPurgeNonces  = "purge_nonces"
	ActSkipped      = "skipped" // a guard said no (Detail tells why)
	ActError        = "error"
)

// Action is one thing the run did (or, in a dry run, would do).
type Action struct {
	Kind    string
	Subject string // household or account id
	Detail  string
}

// Report is the result of Run.
type Report struct {
	DryRun  bool
	Now     time.Time
	Actions []Action
}

// Count returns how many actions have the kind.
func (r Report) Count(kind string) int {
	n := 0
	for _, a := range r.Actions {
		if a.Kind == kind {
			n++
		}
	}
	return n
}

// Has reports whether an action of kind exists for subject.
func (r Report) Has(kind, subject string) bool {
	for _, a := range r.Actions {
		if a.Kind == kind && a.Subject == subject {
			return true
		}
	}
	return false
}

// Options of one run.
type Options struct {
	DryRun bool
}

type run struct {
	*Runner
	ctx    context.Context
	dry    bool
	now    time.Time
	report Report
	tombs  int
	erased int
}

func (r *run) add(kind, subject, detail string) {
	r.report.Actions = append(r.report.Actions, Action{kind, subject, detail})
}

// Run executes all jobs once at the store's current time. It never returns early for a problem
// with one household: that becomes an "error" action and the run carries on.
func (rn *Runner) Run(ctx context.Context, opts Options) (Report, error) {
	if rn.Log == nil {
		rn.Log = slog.Default()
	}
	cfg := rn.Cfg
	if cfg.MaxTombstones == 0 {
		rn.Cfg = DefaultConfig()
		rn.Cfg.KillSwitch = cfg.KillSwitch
	}
	r := &run{Runner: rn, ctx: ctx, dry: opts.DryRun, now: rn.S.Now()}
	r.report = Report{DryRun: opts.DryRun, Now: r.now}

	r.retryRevocations()
	hs, err := rn.S.ListHouseholds(ctx, control.HouseholdActive, control.HouseholdTombstoned)
	if err != nil {
		return r.report, err
	}
	for _, h := range hs {
		if err := r.household(h); err != nil {
			r.add(ActError, h.ID, err.Error())
			rn.Log.Error("lifecycle: household", "household", h.ID, "err", err)
		}
	}
	if err := r.accounts(); err != nil {
		return r.report, err
	}
	if !r.dry {
		if n, err := rn.S.PurgeNonces(ctx, r.now.Add(-rn.Cfg.PurgeNonceAfter)); err == nil && n > 0 {
			r.add(ActPurgeNonces, "", fmt.Sprintf("%d", n))
		}
	}
	return r.report, nil
}

func (r *run) retryRevocations() {
	pend, err := r.S.PendingRevocations(r.ctx)
	if err != nil || len(pend) == 0 || r.Revoker == nil {
		return
	}
	if r.dry {
		r.add(ActRevocations, "", fmt.Sprintf("%d pending", len(pend)))
		return
	}
	done, failed, err := r.Revoker.RetryRevocations(r.ctx)
	if err != nil {
		r.add(ActError, "", "revocations: "+err.Error())
		return
	}
	r.add(ActRevocations, "", fmt.Sprintf("%d done, %d failed", done, failed))
}

func (r *run) household(h control.Household) error {
	plan, err := r.Calc.ForAccount(r.ctx, h.OwnerAccountID)
	if err != nil {
		return err
	}
	if err := r.updateCache(h, plan); err != nil {
		return err
	}
	if err := r.reminders(h, plan); err != nil {
		return err
	}
	switch h.Status {
	case control.HouseholdActive:
		if plan.State == billing.StatePendingDeletion {
			return r.stage1(h, plan)
		}
	case control.HouseholdTombstoned:
		if plan.State != billing.StatePendingDeletion {
			return r.recover(h, plan)
		}
		return r.stage2(h, plan)
	}
	return nil
}

func tm(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func (r *run) updateCache(h control.Household, plan billing.Plan) error {
	ro, da := tm(plan.ReadOnlySince), tm(plan.DeleteAfter)
	if h.PlanStateCache == plan.State && h.ReadOnlySince.Equal(ro) && h.DeleteAfter.Equal(da) {
		return nil
	}
	r.add(ActPlanCache, h.ID, h.PlanStateCache+" -> "+plan.State)
	if r.dry {
		return nil
	}
	return r.S.Tx(r.ctx, func(tx *control.Store) error {
		if err := tx.SetHouseholdPlanCache(r.ctx, h.ID, plan.State, ro, da); err != nil {
			return err
		}
		if h.PlanStateCache != plan.State {
			return tx.AppendAudit(r.ctx, "system", "lifecycle.plan_state", h.ID, map[string]any{"from": h.PlanStateCache, "to": plan.State, "delete_after": control.FormatTime(da)})
		}
		return nil
	})
}

func reminderKind(day int) string { return fmt.Sprintf("lapse_day_%d", day) }

func (r *run) reminders(h control.Household, plan billing.Plan) error {
	if plan.State != billing.StateLapsedReadOnly || plan.ReadOnlySince == nil || !h.DeletionRequestedAt.IsZero() {
		return nil
	}
	since := *plan.ReadOnlySince
	anchor := control.FormatTime(since)
	existing, err := r.S.LifecycleEvents(r.ctx, h.ID)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range existing {
		if e.Anchor == anchor {
			have[e.Kind] = true
		}
	}
	// due and not yet recorded, oldest first; only the newest is sent, older ones are marked done
	var due []int
	for _, d := range r.Cfg.ReminderDays {
		if !r.now.Before(since.Add(time.Duration(d)*24*time.Hour)) && !have[reminderKind(d)] {
			due = append(due, d)
		}
	}
	if len(due) == 0 {
		return nil
	}
	owner, err := r.S.AccountByID(r.ctx, h.OwnerAccountID)
	if err != nil {
		return err
	}
	for i, d := range due {
		kind, dueAt := reminderKind(d), since.Add(time.Duration(d)*24*time.Hour)
		send := i == len(due)-1
		if !send {
			r.add(ActReminderSkip, h.ID, kind)
		} else {
			r.add(ActReminder, h.ID, kind)
		}
		if r.dry {
			continue
		}
		if send && r.Sink != nil {
			rem := Reminder{HouseholdID: h.ID, OwnerAccountID: owner.ID, Kind: kind, Anchor: anchor, DueAt: dueAt, DeleteAfter: tm(plan.DeleteAfter),
				Email: owner.Email, EmailDeliverable: owner.EmailDeliverable && owner.Email != ""}
			if err := r.Sink.Emit(r.ctx, rem); err != nil {
				return err // not recorded: tried again next run
			}
		}
		if _, err := r.S.InsertLifecycleEvent(r.ctx, h.ID, kind, anchor, dueAt); err != nil {
			return err
		}
	}
	return nil
}

// guard decides whether a destructive step may go ahead. User-requested deletions need no Apple
// check; the lapse path needs a fresh Apple answer and a plan that still says pending_deletion.
func (r *run) guard(h control.Household, step string) (control.Household, billing.Plan, bool, error) {
	if r.Cfg.KillSwitch {
		r.add(ActSkipped, h.ID, step+": kill switch is on")
		return h, billing.Plan{}, false, nil
	}
	userPath := !h.DeletionRequestedAt.IsZero()
	if !userPath && !r.dry {
		ents, err := r.S.EntitlementsForAccount(r.ctx, h.OwnerAccountID)
		if err != nil {
			return h, billing.Plan{}, false, err
		}
		hasApple := false
		for _, e := range ents {
			if e.Source == control.SourceApple {
				hasApple = true
			}
		}
		if hasApple { // grant-only households have nothing to ask Apple about
			if r.Apple == nil {
				r.add(ActSkipped, h.ID, step+": no way to check Apple, refusing")
				return h, billing.Plan{}, false, nil
			}
			if _, err := r.Apple.Refresh(r.ctx, h.OwnerAccountID); err != nil {
				r.add(ActSkipped, h.ID, step+": Apple check failed: "+err.Error())
				return h, billing.Plan{}, false, nil
			}
		}
	}
	plan, err := r.Calc.ForAccount(r.ctx, h.OwnerAccountID)
	if err != nil {
		return h, plan, false, err
	}
	if plan.State != billing.StatePendingDeletion {
		r.add(ActSkipped, h.ID, step+": plan is now "+plan.State)
		return h, plan, false, nil
	}
	fresh, err := r.S.HouseholdByID(r.ctx, h.ID)
	if err != nil {
		return h, plan, false, err
	}
	return fresh, plan, true, nil
}

func (r *run) stage1(h control.Household, _ billing.Plan) error {
	if r.tombs >= r.Cfg.MaxTombstones {
		r.add(ActSkipped, h.ID, "tombstone: per-run cap reached, deferred")
		return nil
	}
	h, _, ok, err := r.guard(h, "tombstone")
	if err != nil || !ok {
		return err
	}
	r.tombs++
	r.add(ActTombstone, h.ID, "stage 1")
	if r.dry {
		return nil
	}
	return r.S.Tx(r.ctx, func(tx *control.Store) error {
		if err := tx.TombstoneHousehold(r.ctx, h.ID, r.now); err != nil {
			return err
		}
		return tx.AppendAudit(r.ctx, "system", "lifecycle.tombstone", h.ID, map[string]any{"owner": h.OwnerAccountID, "user_requested": !h.DeletionRequestedAt.IsZero()})
	})
}

func (r *run) stage2(h control.Household, plan billing.Plan) error {
	if plan.DeleteAfter == nil || r.now.Before(*plan.DeleteAfter) {
		return nil
	}
	if r.now.Sub(h.TombstonedAt) < r.Cfg.MinStageGap {
		r.add(ActSkipped, h.ID, "erase: stage gap not over yet")
		return nil
	}
	if r.erased >= r.Cfg.MaxErasures {
		r.add(ActSkipped, h.ID, "erase: per-run cap reached, deferred")
		return nil
	}
	h, _, ok, err := r.guard(h, "erase")
	if err != nil || !ok {
		return err
	}
	ep, err := PlanErasure(r.Layout, h.ID)
	if err != nil {
		return err
	}
	r.erased++
	r.add(ActErase, h.ID, fmt.Sprintf("%d paths", len(ep.Items)))
	if r.dry {
		return nil
	}
	if r.Eraser == nil {
		return errors.New("no eraser configured")
	}
	if err := r.Eraser.Erase(r.ctx, ep); err != nil {
		return fmt.Errorf("erase: %w", err) // stays tombstoned, retried next run
	}
	return r.S.Tx(r.ctx, func(tx *control.Store) error {
		if err := tx.RevokeHouseholdInvites(r.ctx, h.ID, r.now); err != nil {
			return err
		}
		if err := tx.MarkHouseholdDeleted(r.ctx, h.ID, r.now); err != nil {
			return err
		}
		if err := tx.DeleteHouseholdCounters(r.ctx, h.ID); err != nil {
			return err
		}
		return tx.AppendAudit(r.ctx, "system", "lifecycle.erase", h.ID, map[string]any{"owner": h.OwnerAccountID, "paths": ep.Paths()})
	})
}

func (r *run) recover(h control.Household, plan billing.Plan) error {
	r.add(ActUntombstone, h.ID, "plan is now "+plan.State)
	if r.dry {
		return nil
	}
	return r.S.Tx(r.ctx, func(tx *control.Store) error {
		if err := tx.UntombstoneHousehold(r.ctx, h.ID); err != nil {
			return err
		}
		return tx.AppendAudit(r.ctx, "system", "lifecycle.untombstone", h.ID, map[string]any{"plan_state": plan.State})
	})
}

// accounts removes the personal data of accounts whose owner asked for deletion once the
// cooling-off is over and their household (if any) is gone.
func (r *run) accounts() error {
	accs, err := r.S.ListAccountsDeletionRequested(r.ctx)
	if err != nil {
		return err
	}
	for _, a := range accs {
		if r.now.Before(a.DeletionRequestedAt.Add(r.Cfg.CoolingOff)) {
			continue
		}
		if r.Cfg.KillSwitch {
			r.add(ActSkipped, a.ID, "account erase: kill switch is on")
			continue
		}
		// a household of this account must be erased first (it is, by household() above, once its own gap is over)
		if h, herr := r.S.LatestOwnedHousehold(r.ctx, a.ID); herr == nil && h.Status != control.HouseholdDeleted {
			if _, _, merr := r.S.HouseholdOfAccount(r.ctx, a.ID); merr == nil {
				r.add(ActSkipped, a.ID, "account erase: household not erased yet")
				continue
			}
		}
		if r.erased >= r.Cfg.MaxErasures {
			r.add(ActSkipped, a.ID, "account erase: per-run cap reached, deferred")
			continue
		}
		r.erased++
		r.add(ActAccountErase, a.ID, "")
		if r.dry {
			continue
		}
		err := r.S.Tx(r.ctx, func(tx *control.Store) error {
			if _, err := tx.RevokeAccountDevices(r.ctx, a.ID, 0, r.now); err != nil {
				return err
			}
			if m, merr := tx.MemberOf(r.ctx, a.ID); merr == nil {
				if err := tx.RemoveMember(r.ctx, m.HouseholdID, a.ID); err != nil {
					return err
				}
			}
			if err := tx.EraseAccount(r.ctx, a.ID, r.now); err != nil {
				return err
			}
			return tx.AppendAudit(r.ctx, "system", "lifecycle.account_erased", a.ID, nil)
		})
		if err != nil {
			r.add(ActError, a.ID, err.Error())
		}
	}
	return nil
}
