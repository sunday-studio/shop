package autoprod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

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
	State         string  `json:"state"` // idle | running | done | failed
	DryRun        bool    `json:"dry_run"`
	ReceiptsTotal int     `json:"receipts_total"`
	ReceiptsDone  int     `json:"receipts_done"`
	Linked        int     `json:"linked"`
	Created       int     `json:"created"`
	NeedsReview   int     `json:"needs_review"`
	SkippedIDs    []int64 `json:"skipped_receipt_ids,omitempty"` // left out on request
	Error         string  `json:"error,omitempty"`

	// Names of the products created (a dry run: that would be created), oldest receipt first, at most MaxNames.
	CreatedNames   []string `json:"created_names"`
	NamesTruncated bool     `json:"names_truncated,omitempty"`
	LinesSkipped   int      `json:"lines_skipped"` // totals, weight rows and other odd lines that are no product
	StartedAt      string   `json:"started_at,omitempty"`
	FinishedAt     string   `json:"finished_at,omitempty"`
	// Confirmed receipts that still have item lines without a product; filled in by GET when no run is going.
	EligibleReceipts int `json:"eligible_receipts"`
}

// MaxNames bounds the product names a backfill keeps for its preview and result.
const MaxNames = 200

func (st *BackfillState) addNames(lines []LineOutcome) {
	for _, l := range lines {
		if l.Action != ActionCreated {
			continue
		}
		if len(st.CreatedNames) >= MaxNames {
			st.NamesTruncated = true
			return
		}
		st.CreatedNames = append(st.CreatedNames, l.ProductName)
	}
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

// GetBackfill reports the state of the last / current backfill, with how many receipts still have lines
// to load.
func (s *Service) GetBackfill(ctx context.Context) (BackfillState, error) {
	st, err := s.loadBackfill(ctx)
	if err != nil {
		return st, err
	}
	if st.CreatedNames == nil {
		st.CreatedNames = []string{}
	}
	st.EligibleReceipts = s.eligibleReceipts(ctx)
	return st, nil
}

// eligibleReceipts counts the confirmed receipts with at least one item line a load would act on (odd rows
// and discarded lines do not count). Errors count as 0: it is information for a button, nothing more.
func (s *Service) eligibleReceipts(ctx context.Context) int {
	rows, err := s.q.UnlinkedItemLinesOfConfirmed(ctx)
	if err != nil {
		return 0
	}
	discarded, err := discardedLines(ctx, s.q)
	if err != nil {
		return 0
	}
	seen := map[int64]bool{}
	for _, l := range rows {
		if seen[l.ReceiptID] || discarded[l.ID] {
			continue
		}
		if c := CleanLineWith(s.gloss, l.RawText); !c.Skip && c.Norm != "" {
			seen[l.ReceiptID] = true
		}
	}
	return len(seen)
}

// StartBackfill queues the backfill job. It is refused (ErrDisabled) while AUTO_PRODUCTS is off and
// (ErrConflict) while one is running.
func (s *Service) StartBackfill(ctx context.Context, dryRun bool, skip []int64) (jobID int64, err error) {
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
	if err := s.saveBackfill(ctx, BackfillState{State: "running", DryRun: dryRun, ReceiptsTotal: int(total), SkippedIDs: skip, StartedAt: store.TS(s.now())}); err != nil {
		return 0, err
	}
	b, _ := json.Marshal(map[string]any{"dry_run": dryRun, "skip_receipt_ids": skip})
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
				DryRun bool    `json:"dry_run"`
				Skip   []int64 `json:"skip_receipt_ids"`
			}
			_ = json.Unmarshal(payload, &pl)
			return s.RunBackfill(ctx, pl.DryRun, pl.Skip)
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
func (s *Service) RunBackfill(ctx context.Context, dryRun bool, skip []int64) error {
	st, err := s.loadBackfill(ctx)
	if err != nil {
		return err
	}
	if st.State != "running" || st.DryRun != dryRun {
		st = BackfillState{State: "running", DryRun: dryRun, StartedAt: store.TS(s.now())}
	}
	ids, err := s.q.ConfirmedReceiptIDs(ctx, 0)
	if err != nil {
		return err
	}
	if len(skip) > 0 { // left out on request (e.g. a receipt that still needs fixing)
		kept := ids[:0:0]
		for _, id := range ids {
			if !containsID(skip, id) {
				kept = append(kept, id)
			}
		}
		ids = kept
	}
	st.ReceiptsTotal, st.SkippedIDs = len(ids), skip

	var tx *sql.Tx
	if dryRun {
		if tx, err = s.conn.BeginTx(ctx, nil); err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck
		st = BackfillState{State: "running", DryRun: true, ReceiptsTotal: len(ids), SkippedIDs: skip, StartedAt: st.StartedAt}
	}
	for i := st.ReceiptsDone; i < len(ids); i++ {
		var r Stats
		if dryRun {
			r, err = s.processReceiptTx(ctx, tx, ids[i], "backfill")
		} else {
			r, err = s.ProcessReceipt(ctx, ids[i], "backfill")
		}
		if errors.Is(err, sql.ErrNoRows) { // deleted while the run was going
			st.ReceiptsDone = i + 1
			continue
		}
		if err != nil {
			return err
		}
		st.Linked += r.Linked
		st.Created += r.Created
		st.NeedsReview += r.NeedsReview
		st.LinesSkipped += r.Skipped
		st.addNames(r.lines)
		st.ReceiptsDone = i + 1
		if !dryRun { // let the app's own writes in between receipts
			time.Sleep(5 * time.Millisecond)
		}
		if !dryRun && (i%5 == 4 || i == len(ids)-1) {
			if err := s.saveBackfill(ctx, st); err != nil {
				return err
			}
		}
	}
	if tx != nil {
		_ = tx.Rollback() // release the write lock before saving the state
	}
	st.State, st.FinishedAt = "done", store.TS(s.now())
	return s.saveBackfill(ctx, st)
}
