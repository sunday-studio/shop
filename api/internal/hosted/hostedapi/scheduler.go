package hostedapi

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/hosted/lifecycle"
	"shop/internal/tenancy"
)

// Scheduler runs the periodic jobs: the lifecycle runner (plan cache, reminders, two-stage
// deletion, Apple revocation retries), export cleanup, idempotency purge and the storage gauge
// recount. Only one run happens at a time: a mutex in the process and a lease in the control
// database (so a manual `admin lifecycle run` on the same database waits its turn too).
type Scheduler struct {
	API      *API
	Runner   *lifecycle.Runner
	Interval time.Duration // default 15 minutes
	// RecountEvery runs the storage recount every N runs (default 4).
	RecountEvery int
	Log          *slog.Logger

	mu     sync.Mutex
	holder string
	runs   int
}

// Run loops until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	if s.Interval <= 0 {
		s.Interval = 15 * time.Minute
	}
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		if _, err := s.RunOnce(ctx); err != nil {
			s.log().Error("scheduler run", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Scheduler) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// RunOnce runs every job once. It returns ran=false when another run holds the lock.
func (s *Scheduler) RunOnce(ctx context.Context) (ran bool, err error) {
	if !s.mu.TryLock() {
		return false, nil
	}
	defer s.mu.Unlock()
	if s.holder == "" {
		s.holder = control.NewUUID()
	}
	st := s.API.Store
	ok, err := st.TryLock(ctx, "scheduler", s.holder, 30*time.Minute)
	if err != nil || !ok {
		return false, err
	}
	defer func() { _ = st.Unlock(context.WithoutCancel(ctx), "scheduler", s.holder) }()
	s.runs++
	var first error
	keep := func(e error) {
		if e != nil && first == nil {
			first = e
		}
	}
	if s.Runner != nil {
		rep, err := s.Runner.Run(ctx, lifecycle.Options{})
		keep(err)
		if err == nil && len(rep.Actions) > 0 {
			s.log().Info("lifecycle", "actions", len(rep.Actions))
		}
	}
	_, err = s.API.CleanupExports(ctx)
	keep(err)
	_, err = st.PurgeIdempotent(ctx, st.Now().Add(-s.API.Cfg.IdempotencyTTL))
	keep(err)
	every := s.RecountEvery
	if every <= 0 {
		every = 4
	}
	if s.runs%every == 1 || every == 1 {
		keep(s.API.RecountStorage(ctx))
	}
	return true, first
}

// RecountStorage measures every household folder on disk (database files and images) and stores
// the result as the storage gauge, which corrects drift from deleted receipts.
func (a *API) RecountStorage(ctx context.Context) error {
	hs, err := a.Store.ListHouseholds(ctx, control.HouseholdActive, control.HouseholdTombstoned)
	if err != nil {
		return err
	}
	for _, h := range hs {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := dirSize(a.Provider.Dir(h.ID))
		if err != nil {
			continue
		}
		if err := a.Meter.SetStorage(ctx, h.ID, n); err != nil {
			return err
		}
	}
	return nil
}

func dirSize(root string) (int64, error) {
	if _, err := os.Stat(root); err != nil {
		return 0, err
	}
	var total int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// ---- erasure on the real files ----

// Eraser removes a household through the tenant provider (it closes the open database first and
// refuses new users), then deletes what the provider does not manage (exports) and anything left.
type Eraser struct {
	Provider *tenancy.Multi
	OS       lifecycle.OSEraser
}

func (e Eraser) Erase(ctx context.Context, plan lifecycle.ErasurePlan) error {
	if err := e.Provider.Erase(ctx, plan.HouseholdID); err != nil && err != tenancy.ErrUnknownTenant {
		return fmt.Errorf("erase household %s: %w", plan.HouseholdID, err)
	}
	return e.OS.Erase(ctx, plan)
}

// ---- background work pauses while a household is read-only ----

// SchedulerView is a provider view for the job scheduler: households whose plan is read-only are
// not listed and cannot be acquired, so their OCR and parse jobs stay queued (and resume when the
// household can write again). The HTTP gateway uses the real provider.
type SchedulerView struct {
	tenancy.Provider
	API *API

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	ok bool
	at time.Time
}

func (v *SchedulerView) active(ctx context.Context, id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cache == nil {
		v.cache = map[string]cached{}
	}
	now := time.Now()
	if c, ok := v.cache[id]; ok && now.Sub(c.at) < 20*time.Second {
		return c.ok
	}
	ok := false
	if h, err := v.API.Store.HouseholdByID(ctx, id); err == nil && h.Status == control.HouseholdActive {
		if pl, err := v.API.Calc.ForAccount(ctx, h.OwnerAccountID); err == nil {
			ok = pl.Can("write")
		}
	}
	v.cache[id] = cached{ok, now}
	return ok
}

func (v *SchedulerView) IDs(ctx context.Context) ([]string, error) {
	ids, err := v.Provider.IDs(ctx)
	if err != nil {
		return nil, err
	}
	out := ids[:0]
	for _, id := range ids {
		if v.active(ctx, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (v *SchedulerView) Acquire(ctx context.Context, id string) (*tenancy.Tenant, func(), error) {
	if !v.active(ctx, id) {
		return nil, nil, tenancy.ErrUnknownTenant
	}
	return v.Provider.Acquire(ctx, id)
}
