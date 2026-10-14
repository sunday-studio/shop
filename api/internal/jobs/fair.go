package jobs

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Source is where the fair scheduler finds households and their queues.
type Source interface {
	// IDs lists every household.
	IDs(ctx context.Context) ([]string, error)
	// Queue returns the household's queue and a release function (the household stays open until
	// released). The same open household returns the same *Queue.
	Queue(ctx context.Context, id string) (*Queue, func(), error)
}

// Fair runs the queues of many households with a shared pool of workers:
//
//   - at most one job runs per household at a time (one household's backlog never uses two workers
//     and, with a single OCR sidecar, never queues the sidecar up for itself);
//   - households take turns round robin, so a household with 50 queued receipts does not starve one
//     with a single receipt.
//
// A household is looked at when it enqueues something (its queue's Notify hook calls Hint) and
// during a sweep of every household (for jobs that become due later, such as retries).
type Fair struct {
	Source  Source
	Workers int           // size of the shared pool (default 1)
	Sweep   time.Duration // how often every household is probed (default 30 s)
	Log     *slog.Logger

	mu        sync.Mutex
	order     []string // all households, sorted; the round-robin ring
	cursor    int
	hot       map[string]bool // households that may have a ready job
	busy      map[string]bool // households with a job running
	recovered map[*Queue]bool
	wake      chan struct{}
}

func (f *Fair) init() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hot == nil {
		f.hot, f.busy, f.recovered = map[string]bool{}, map[string]bool{}, map[*Queue]bool{}
		f.wake = make(chan struct{}, 1)
	}
	if f.Workers < 1 {
		f.Workers = 1
	}
	if f.Sweep <= 0 {
		f.Sweep = 30 * time.Second
	}
	if f.Log == nil {
		f.Log = slog.New(slog.DiscardHandler)
	}
}

// Hint says household id may have work now.
func (f *Fair) Hint(id string) {
	f.init()
	f.mu.Lock()
	f.hot[id] = true
	f.mu.Unlock()
	f.signal()
}

func (f *Fair) signal() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled, running Workers workers.
func (f *Fair) Run(ctx context.Context) {
	f.init()
	f.refresh(ctx, true)
	var wg sync.WaitGroup
	for range f.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.worker(ctx)
		}()
	}
	wg.Wait()
}

// refresh reloads the household list; with markAll every household is probed once.
func (f *Fair) refresh(ctx context.Context, markAll bool) {
	ids, err := f.Source.IDs(ctx)
	if err != nil {
		f.Log.Error("list households", "err", err)
		return
	}
	sort.Strings(ids)
	f.mu.Lock()
	f.order = ids
	if markAll {
		for _, id := range ids {
			f.hot[id] = true
		}
	}
	f.mu.Unlock()
}

func (f *Fair) worker(ctx context.Context) {
	sweep := time.NewTicker(f.Sweep)
	defer sweep.Stop()
	for {
		for f.runNext(ctx) {
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		case <-sweep.C:
			f.refresh(ctx, true)
		}
	}
}

// claim picks the next hot household that has no job running, in round-robin order, and marks it busy.
func (f *Fair) claim() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.order)
	// A hinted household that is not in the ring yet (created after the last refresh) joins it.
	for id := range f.hot {
		if !contains(f.order, id) {
			f.order = append(f.order, id)
			sort.Strings(f.order)
			n = len(f.order)
		}
	}
	for i := range n {
		idx := (f.cursor + i) % n
		id := f.order[idx]
		if f.hot[id] && !f.busy[id] {
			f.busy[id] = true
			f.cursor = (idx + 1) % n // the next claim starts after this household
			return id, true
		}
	}
	return "", false
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// runNext runs one job of the next household in turn. It reports whether a job ran.
func (f *Fair) runNext(ctx context.Context) bool {
	for {
		id, ok := f.claim()
		if !ok {
			return false
		}
		ran := f.runHousehold(ctx, id)
		f.mu.Lock()
		delete(f.busy, id)
		if !ran {
			delete(f.hot, id) // nothing ready: look again on the next hint or sweep
		}
		f.mu.Unlock()
		if ran {
			f.signal() // other workers may take other households while this one has more
			return true
		}
		if ctx.Err() != nil {
			return false
		}
	}
}

func (f *Fair) runHousehold(ctx context.Context, id string) bool {
	q, release, err := f.Source.Queue(ctx, id)
	if err != nil {
		f.Log.Warn("household queue unavailable", "household", id, "err", err)
		return false
	}
	defer release()
	f.mu.Lock()
	first := !f.recovered[q]
	f.recovered[q] = true
	f.mu.Unlock()
	if first {
		if n, err := q.RequeueStale(ctx); err == nil && n > 0 {
			f.Log.Warn("requeued stale jobs", "household", id, "count", n)
		}
	}
	return q.RunOne(ctx)
}
