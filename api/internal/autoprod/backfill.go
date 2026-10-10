package autoprod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"shop/internal/jobs"
	"shop/internal/store"
	"shop/internal/store/db"
)

const (
	JobBackfill     = "autopop_backfill"
	backfillSetting = "autopop_backfill_state"
)

// BackfillState is what GET /v1/products/autopopulate/backfill reports.
type BackfillState struct {
	State         string `json:"state"` // idle | running | done | failed
	DryRun        bool   `json:"dry_run"`
	ReceiptsTotal int    `json:"receipts_total"`
	ReceiptsDone  int    `json:"receipts_done"`
	Linked        int    `json:"linked"`
	Created       int    `json:"created"`
	NeedsReview   int    `json:"needs_review"`
	Error         string `json:"error,omitempty"`
}

func (s *Service) loadBackfill(ctx context.Context) (BackfillState, error) {
	v, err := s.q.GetSetting(ctx, backfillSetting)
	if errors.Is(err, sql.ErrNoRows) {
		return BackfillState{State: "idle"}, nil
	}
	if err != nil {
		return BackfillState{}, err
	}
	var st BackfillState
	if err := json.Unmarshal([]byte(v), &st); err != nil {
		return BackfillState{State: "idle"}, nil
	}
	return st, nil
}

func (s *Service) saveBackfill(ctx context.Context, st BackfillState) error {
	b, _ := json.Marshal(st)
	return s.q.PutSetting(ctx, db.PutSettingParams{Key: backfillSetting, Value: string(b), UpdatedAt: store.Now()})
}

// GetBackfill reports the state of the last / current backfill.
func (s *Service) GetBackfill(ctx context.Context) (BackfillState, error) { return s.loadBackfill(ctx) }

// StartBackfill queues the backfill job. It is refused (ErrDisabled) while AUTO_PRODUCTS is off and
// (ErrConflict) while one is running.
func (s *Service) StartBackfill(ctx context.Context, dryRun bool) (jobID int64, err error) {
	if !s.enabled {
		return 0, ErrDisabled
	}
	st, err := s.loadBackfill(ctx)
	if err != nil {
		return 0, err
	}
	if st.State == "running" {
		return 0, errf(ErrConflict, "a backfill is already running")
	}
	total, err := s.q.CountConfirmedReceipts(ctx)
	if err != nil {
		return 0, err
	}
	if err := s.saveBackfill(ctx, BackfillState{State: "running", DryRun: dryRun, ReceiptsTotal: int(total)}); err != nil {
		return 0, err
	}
	b, _ := json.Marshal(map[string]bool{"dry_run": dryRun})
	now := store.Now()
	j, err := s.q.EnqueueJob(ctx, db.EnqueueJobParams{Kind: JobBackfill, Payload: string(b), RunAfter: now, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return 0, err
	}
	if s.queue != nil {
		s.queue.Notify()
	}
	return j.ID, nil
}

func (s *Service) registerBackfill(q *jobs.Queue) {
	q.Register(JobBackfill, jobs.Handler{
		Run: func(ctx context.Context, payload []byte) error {
			var pl struct {
				DryRun bool `json:"dry_run"`
			}
			_ = json.Unmarshal(payload, &pl)
			return s.RunBackfill(ctx, pl.DryRun)
		},
		OnFail: func(ctx context.Context, _ []byte, err error) {
			st, _ := s.loadBackfill(ctx)
			st.State, st.Error = "failed", err.Error()
			_ = s.saveBackfill(ctx, st)
		},
	})
}

// RunBackfill links or creates products for every confirmed receipt, oldest first (so the earliest
// spelling becomes the product name). It resumes from the saved progress and is idempotent: lines that
// already have a product are skipped. A dry run counts inside one transaction that is rolled back.
func (s *Service) RunBackfill(ctx context.Context, dryRun bool) error {
	st, err := s.loadBackfill(ctx)
	if err != nil {
		return err
	}
	if st.State != "running" || st.DryRun != dryRun {
		st = BackfillState{State: "running", DryRun: dryRun}
	}
	ids, err := s.q.ConfirmedReceiptIDs(ctx, 0)
	if err != nil {
		return err
	}
	st.ReceiptsTotal = len(ids)

	var tx *sql.Tx
	if dryRun {
		if tx, err = s.conn.BeginTx(ctx, nil); err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck
		st = BackfillState{State: "running", DryRun: true, ReceiptsTotal: len(ids)}
	}
	for i := st.ReceiptsDone; i < len(ids); i++ {
		var r Stats
		if dryRun {
			r, err = s.processReceiptTx(ctx, tx, ids[i], "backfill")
		} else {
			r, err = s.ProcessReceipt(ctx, ids[i], "backfill")
		}
		if err != nil {
			return err
		}
		st.Linked += r.Linked
		st.Created += r.Created
		st.NeedsReview += r.NeedsReview
		st.ReceiptsDone = i + 1
		if !dryRun && (i%10 == 9 || i == len(ids)-1) {
			if err := s.saveBackfill(ctx, st); err != nil {
				return err
			}
		}
	}
	if tx != nil {
		_ = tx.Rollback() // release the write lock before saving the state
	}
	st.State = "done"
	return s.saveBackfill(ctx, st)
}
