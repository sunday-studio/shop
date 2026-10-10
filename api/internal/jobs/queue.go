// Package jobs is a small durable job queue stored in SQLite.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"shop/internal/store"
	"shop/internal/store/db"
)

const (
	MaxAttempts = 3
	staleAfter  = 10 * time.Minute
	pollEvery   = 2 * time.Second
)

// Handler processes one job payload. OnFail runs once a job has used up
// its attempts (or returned a Permanent error).
type Handler struct {
	Run    func(ctx context.Context, payload []byte) error
	OnFail func(ctx context.Context, payload []byte, err error)
}

type permanent struct{ error }

// Permanent marks an error as not worth retrying.
func Permanent(err error) error { return permanent{err} }

type Queue struct {
	q        *db.Queries
	log      *slog.Logger
	handlers map[string]Handler
	wake     chan struct{}
	// Backoff returns the delay before retrying after the given attempt.
	Backoff func(attempt int64) time.Duration
}

func New(conn *sql.DB, log *slog.Logger) *Queue {
	return &Queue{
		q:        db.New(conn),
		log:      log,
		handlers: map[string]Handler{},
		wake:     make(chan struct{}, 1),
		Backoff: func(attempt int64) time.Duration {
			return time.Duration(attempt*attempt) * 30 * time.Second
		},
	}
}

func (q *Queue) Register(kind string, h Handler) { q.handlers[kind] = h }

// Enqueue adds a job using the given queries (so callers can enqueue inside
// their own transaction). Call Notify after commit to wake a worker.
func Enqueue(ctx context.Context, qs *db.Queries, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := store.Now()
	_, err = qs.EnqueueJob(ctx, db.EnqueueJobParams{
		Kind: kind, Payload: string(b), RunAfter: now, CreatedAt: now, UpdatedAt: now,
	})
	return err
}

// Notify wakes an idle worker.
func (q *Queue) Notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Run starts n workers and blocks until ctx is cancelled.
func (q *Queue) Run(ctx context.Context, n int) {
	if c, err := q.q.RequeueStaleJobs(ctx, db.RequeueStaleJobsParams{
		Now: store.Now(), StaleBefore: store.TS(time.Now().Add(-staleAfter)),
	}); err == nil && c > 0 {
		q.log.Warn("requeued stale jobs", "count", c)
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.worker(ctx)
		}()
	}
	wg.Wait()
}

func (q *Queue) worker(ctx context.Context) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		for q.RunOne(ctx) {
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		case <-t.C:
		}
	}
}

// RunOne claims and runs a single ready job. It reports whether a job ran.
func (q *Queue) RunOne(ctx context.Context) bool {
	job, err := q.q.ClaimJob(ctx, store.Now())
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		q.log.Error("claim job", "err", err)
		return false
	}

	start := time.Now()
	h, ok := q.handlers[job.Kind]
	if !ok {
		err = Permanent(fmt.Errorf("no handler for job kind %q", job.Kind))
	} else {
		err = safeRun(ctx, h, []byte(job.Payload))
	}
	log := q.log.With("job", job.ID, "kind", job.Kind, "attempt", job.Attempts, "ms", time.Since(start).Milliseconds())

	// Use a fresh context so bookkeeping survives shutdown mid-job.
	bg := context.WithoutCancel(ctx)
	switch {
	case err == nil:
		log.Info("job done")
		_ = q.q.FinishJob(bg, db.FinishJobParams{UpdatedAt: store.Now(), ID: job.ID})
	case isPermanent(err) || job.Attempts >= MaxAttempts:
		log.Error("job failed", "err", err)
		_ = q.q.FailJob(bg, db.FailJobParams{LastError: store.Str(err.Error()), UpdatedAt: store.Now(), ID: job.ID})
		if ok && h.OnFail != nil {
			h.OnFail(bg, []byte(job.Payload), err)
		}
	default:
		next := time.Now().Add(q.Backoff(job.Attempts))
		log.Warn("job retry", "err", err, "run_after", next)
		_ = q.q.RetryJob(bg, db.RetryJobParams{
			RunAfter: store.TS(next), LastError: store.Str(err.Error()), UpdatedAt: store.Now(), ID: job.ID,
		})
	}
	return true
}

func safeRun(ctx context.Context, h Handler, payload []byte) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = Permanent(fmt.Errorf("panic: %v", v))
		}
	}()
	return h.Run(ctx, payload)
}

func isPermanent(err error) bool {
	var p permanent
	return errors.As(err, &p)
}
