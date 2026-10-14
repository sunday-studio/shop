package autoprod

import (
	"context"
	"errors"
	"testing"
)

// Per receipt, on the 13 real receipts: also for a receipt confirmed long ago, and for one not confirmed.
func TestLoadFromReceiptOnTheRealReceipts(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	ids := loadReceipts(t, conn)

	// two receipts are not confirmed yet: one is fine (ok), one still needs review
	if _, err := conn.Exec(`UPDATE receipts SET confirmed_at = NULL WHERE id = ?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE receipts SET confirmed_at = NULL, status = 'needs_review' WHERE id = ?`, ids[2]); err != nil {
		t.Fatal(err)
	}

	// a dry run reports and writes nothing
	dry, err := s.LoadFromReceipt(ctx, ids[0], true)
	if err != nil || !dry.DryRun || dry.Created == 0 || len(dry.Lines) == 0 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Fatalf("a dry run made %d products", n)
	}

	var all ReceiptLoad
	for _, id := range ids {
		r, err := s.LoadFromReceipt(ctx, id, false)
		if err != nil {
			t.Fatalf("receipt %d: %v", id, err)
		}
		if got := r.Linked + r.Created + r.Skipped; got != len(r.Lines) {
			t.Errorf("receipt %d: %d outcomes for %+v", id, len(r.Lines), r)
		}
		for _, l := range r.Lines {
			if l.Action == ActionCreated && (l.ProductID == 0 || l.ProductName == "") || l.Action == ActionSkipped && l.SkipReason == "" {
				t.Errorf("incomplete outcome %+v", l)
			}
		}
		all.Created += r.Created
		all.Linked += r.Linked
		all.NeedsReview += r.NeedsReview
		all.Skipped += r.Skipped
		if id == ids[1] && r.Confirmed || id == ids[0] && !r.Confirmed {
			t.Errorf("confirmed flag wrong for receipt %d: %+v", id, r)
		}
	}
	products := count(t, conn, `SELECT COUNT(*) FROM products`)
	if products != all.Created || products < 55 || products > 80 {
		t.Fatalf("%d products for %+v", products, all)
	}
	// marked like the ones made on confirm, so the review queue and the badge treat them the same
	if n := count(t, conn, `SELECT COUNT(*) FROM products WHERE created_via != 'receipt'`); n != 0 {
		t.Errorf("%d products not marked created_via=receipt", n)
	}
	q, err := s.ReviewQueue(ctx, 200, 0, "")
	if err != nil || q.Total == 0 || q.Total != all.NeedsReview {
		t.Errorf("review queue %d, loads said %d to review (%v)", q.Total, all.NeedsReview, err)
	}
	// near matches are queued, never merged on their own
	if n := count(t, conn, `SELECT COUNT(*) FROM products WHERE review_reason = 'near_match' AND suggested_merge_id IS NOT NULL`); n < 3 {
		t.Errorf("%d near matches queued", n)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products WHERE merged_into_id IS NOT NULL`); n != 0 {
		t.Errorf("%d products merged automatically", n)
	}
	// the same rows as confirm would have decided
	conn2 := newDB(t)
	s2 := newService(t, conn2, true)
	ids2 := loadReceipts(t, conn2)
	want := processAll(t, s2, ids2, "receipt")
	if want.Created != all.Created || want.Linked != all.Linked || want.NeedsReview != all.NeedsReview {
		t.Errorf("confirm-time create_or_link %+v differs from the on-demand loads %+v", want, all)
	}

	// idempotent: a second call over every receipt finds nothing to do
	for _, id := range ids {
		r, err := s.LoadFromReceipt(ctx, id, false)
		if err != nil || r.Created != 0 || r.Linked != 0 {
			t.Fatalf("second load of receipt %d = %+v, %v", id, r, err)
		}
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != products {
		t.Errorf("products %d -> %d on the second pass", products, n)
	}
	// and confirming the receipts that were not confirmed adds nothing either
	for _, id := range ids[1:3] {
		if st, err := s.ProcessReceipt(ctx, id, "receipt"); err != nil || st.Created != 0 || st.Linked != 0 {
			t.Errorf("confirm after load = %+v, %v", st, err)
		}
	}
}

func TestLoadFromReceiptRefusals(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	ids := loadReceipts(t, conn)

	if _, err := s.LoadFromReceipt(ctx, 99999, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown receipt: %v", err)
	}
	for _, status := range []string{"pending", "processing", "failed"} {
		if _, err := conn.Exec(`UPDATE receipts SET status = ? WHERE id = ?`, status, ids[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := s.LoadFromReceipt(ctx, ids[0], false); !errors.Is(err, ErrConflict) {
			t.Errorf("status %s: %v; want a conflict", status, err)
		}
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Errorf("%d products from refused loads", n)
	}
	if _, err := newService(t, conn, false).LoadFromReceipt(ctx, ids[1], false); !errors.Is(err, ErrDisabled) {
		t.Errorf("flag off: %v", err)
	}
}

// A product the user discarded in the review queue does not come back when receipts are loaded again, and
// undoing the discard makes the lines loadable as before.
func TestLoadDoesNotBringBackDiscardedProducts(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	ids := loadReceipts(t, conn)
	if _, err := s.LoadFromReceipt(ctx, ids[0], false); err != nil {
		t.Fatal(err)
	}
	var pid int64
	if err := conn.QueryRow(`SELECT id FROM products WHERE deleted_at IS NULL ORDER BY id LIMIT 1`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	d, err := s.Discard(ctx, pid)
	if err != nil || d.LinesMoved == 0 {
		t.Fatalf("discard = %+v, %v", d, err)
	}
	before := count(t, conn, `SELECT COUNT(*) FROM products`)
	r, err := s.LoadFromReceipt(ctx, ids[0], false)
	if err != nil || r.Created != 0 || r.Linked != 0 {
		t.Fatalf("load after discard = %+v, %v", r, err)
	}
	discarded := 0
	for _, l := range r.Lines {
		if l.SkipReason == SkipDiscarded {
			discarded++
		}
	}
	if discarded != d.LinesMoved {
		t.Errorf("%d lines reported as discarded, %d were unlinked", discarded, d.LinesMoved)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != before {
		t.Errorf("products %d -> %d", before, n)
	}
}

func TestBackfillPreviewNamesAndExcludedReceipts(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	ids := loadReceipts(t, conn)

	st, _ := s.GetBackfill(ctx)
	if st.EligibleReceipts != len(ids) {
		t.Errorf("eligible = %d, want %d", st.EligibleReceipts, len(ids))
	}
	skip := []int64{ids[0], ids[3]}
	if _, err := s.StartBackfill(ctx, true, skip); err != nil {
		t.Fatal(err)
	}
	if err := s.RunBackfill(ctx, true, skip); err != nil {
		t.Fatal(err)
	}
	dry, _ := s.GetBackfill(ctx)
	if dry.State != "done" || !dry.DryRun || len(dry.CreatedNames) != dry.Created || dry.Created == 0 || dry.StartedAt == "" || dry.FinishedAt == "" {
		t.Fatalf("dry run = %+v", dry)
	}
	if dry.ReceiptsTotal != len(ids)-2 || dry.LinesSkipped == 0 {
		t.Errorf("dry run total %d / skipped lines %d", dry.ReceiptsTotal, dry.LinesSkipped)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Fatalf("preview made %d products", n)
	}

	if _, err := s.StartBackfill(ctx, false, skip); err != nil {
		t.Fatal(err)
	}
	if err := s.RunBackfill(ctx, false, skip); err != nil {
		t.Fatal(err)
	}
	done, _ := s.GetBackfill(ctx)
	if done.Created != dry.Created || len(done.CreatedNames) != done.Created {
		t.Errorf("preview said %d, the run made %+v", dry.Created, done)
	}
	for i, name := range done.CreatedNames {
		if name != dry.CreatedNames[i] {
			t.Fatalf("name %d: preview %q, result %q", i, dry.CreatedNames[i], name)
		}
	}
	// nothing came from the receipts left out
	for _, id := range skip {
		if n := count(t, conn, `SELECT COUNT(*) FROM receipt_lines WHERE receipt_id = ? AND product_id IS NOT NULL`, id); n != 0 {
			t.Errorf("receipt %d was excluded but %d of its lines got a product", id, n)
		}
	}
	// the excluded ones stay available for a later, deliberate load
	if st, _ := s.GetBackfill(ctx); st.EligibleReceipts != 2 {
		t.Errorf("eligible after the run = %d, want the 2 excluded", st.EligibleReceipts)
	}
	// twice creates nothing new
	if _, err := s.StartBackfill(ctx, false, skip); err != nil {
		t.Fatal(err)
	}
	if err := s.RunBackfill(ctx, false, skip); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetBackfill(ctx); again.Created != 0 || again.Linked != 0 || len(again.CreatedNames) != 0 {
		t.Errorf("second run = %+v", again)
	}
}
