package autoprod

import (
	"context"
	"database/sql"
	"errors"
)

// ReceiptLoad is what "load products from this receipt" did, line by line.
type ReceiptLoad struct {
	ReceiptID   int64         `json:"receipt_id"`
	DryRun      bool          `json:"dry_run"`
	Confirmed   bool          `json:"confirmed"`
	Linked      int           `json:"linked"`
	Created     int           `json:"created"`
	NeedsReview int           `json:"needs_review"`
	Skipped     int           `json:"skipped"`
	Lines       []LineOutcome `json:"lines"`
}

// LoadFromReceipt runs create_or_link over the item lines of one receipt on request (§ products from
// receipts). It is a deliberate user action, so it works on a receipt confirmed earlier and on one that is
// not confirmed yet, as long as the receipt was read (status ok or needs_review). Totals, payment and
// other odd rows are skipped as on confirm, lines the user edited or already linked are left alone, a
// product the user discarded is not brought back, and a near match is never merged: it becomes a new
// product in the review queue. It is idempotent: a second call finds nothing left to do. Products are
// marked created_via "receipt". With dryRun nothing is written (one rolled-back transaction).
func (s *Service) LoadFromReceipt(ctx context.Context, receiptID int64, dryRun bool) (ReceiptLoad, error) {
	if !s.enabled {
		return ReceiptLoad{}, ErrDisabled
	}
	rec, err := s.q.GetReceipt(ctx, receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return ReceiptLoad{}, ErrNotFound
	} else if err != nil {
		return ReceiptLoad{}, err
	}
	switch rec.Status {
	case "ok", "needs_review":
	case "failed":
		return ReceiptLoad{}, errf(ErrConflict, "this receipt could not be read; fix or re-read it first")
	default:
		return ReceiptLoad{}, errf(ErrConflict, "this receipt is still being read")
	}

	var st Stats
	if dryRun {
		s.loadMu.Lock()
		tx, err := s.conn.BeginTx(ctx, nil)
		if err != nil {
			s.loadMu.Unlock()
			return ReceiptLoad{}, err
		}
		st, err = s.processReceiptTx(ctx, tx, receiptID, "receipt")
		_ = tx.Rollback()
		s.loadMu.Unlock()
		if err != nil {
			return ReceiptLoad{}, err
		}
	} else if st, err = s.ProcessReceipt(ctx, receiptID, "receipt"); err != nil {
		return ReceiptLoad{}, err
	}
	out := ReceiptLoad{ReceiptID: receiptID, DryRun: dryRun, Confirmed: rec.ConfirmedAt.Valid, Linked: st.Linked, Created: st.Created,
		NeedsReview: st.NeedsReview, Skipped: st.Skipped, Lines: st.lines}
	if out.Lines == nil {
		out.Lines = []LineOutcome{}
	}
	return out, nil
}
