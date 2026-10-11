package autoprod

import (
	"context"
	"errors"
	"testing"
)

func TestReviewQueueAndActions(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)
	s := newService(t, conn, true)
	processAll(t, s, loadReceipts(t, conn), "receipt")

	q, err := s.ReviewQueue(ctx, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, n := range q.Counts {
		sum += n
	}
	if q.Total == 0 || sum != q.Total || len(q.Items) != 0 {
		t.Fatalf("limit=0: total %d counts %v items %d; want counts only", q.Total, q.Counts, len(q.Items))
	}
	if len(q.Counts) != len(Reasons) {
		t.Errorf("counts has %d reasons; want all %d", len(q.Counts), len(Reasons))
	}
	full, err := s.ReviewQueue(ctx, 100, 0, "")
	if err != nil || len(full.Items) != full.Total {
		t.Fatalf("queue: %d items of %d, %v", len(full.Items), full.Total, err)
	}
	// ordered by reason, near_match first, each item has its source line
	rank := map[string]int{}
	for i, r := range Reasons {
		rank[r] = i
	}
	last := -1
	for _, it := range full.Items {
		if rank[it.Reason] < last {
			t.Fatalf("queue not ordered by reason at %q", it.Product.Name)
		}
		last = rank[it.Reason]
		if it.SourceLineCount < 1 || len(it.SourceLines) < 1 || len(it.SourceLines) > 3 {
			t.Errorf("%s: %d source lines (%d shown)", it.Product.Name, it.SourceLineCount, len(it.SourceLines))
		}
		if it.Reason == ReasonNearMatch && it.SuggestedMerge == nil {
			t.Errorf("%s: near_match without a suggestion", it.Product.Name)
		}
	}
	nearOnly, _ := s.ReviewQueue(ctx, 100, 0, ReasonNearMatch)
	if len(nearOnly.Items) != q.Counts[ReasonNearMatch] {
		t.Errorf("reason filter: %d items, count says %d", len(nearOnly.Items), q.Counts[ReasonNearMatch])
	}
	if _, err := s.ReviewQueue(ctx, 5, 0, "bogus"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bogus reason err = %v", err)
	}

	// accept with category and name together
	var noCat Queue
	noCat, _ = s.ReviewQueue(ctx, 100, 0, ReasonNoCategory)
	if len(noCat.Items) == 0 {
		t.Fatal("expected products without a category")
	}
	p := noCat.Items[0].Product
	cat := int64(1)
	name := "My new name"
	r, err := s.Review(ctx, p.ID, ReviewInput{Action: "accept", CategoryID: &cat, Name: &name})
	if err != nil || r.Product.Name != name || r.Product.CategoryID.Int64 != cat || r.Product.CategorySource.String != "manual" || r.Product.NeedsReview != 0 {
		t.Fatalf("accept = %+v, %v", r.Product, err)
	}
	if _, err := s.Review(ctx, p.ID, ReviewInput{Action: "accept"}); !errors.Is(err, ErrConflict) {
		t.Errorf("accept twice err = %v; want conflict", err)
	}

	// near_match merge: the card's product disappears into the suggestion
	card := nearOnly.Items[0]
	m, err := s.Review(ctx, card.Product.ID, ReviewInput{Action: "merge"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Merge == nil || m.Product.ID != card.SuggestedMerge.ProductID || m.Merge.MergedAway.ID != card.Product.ID || m.Merge.Kind != "merge" {
		t.Fatalf("near_match merge result = %+v", m.Merge)
	}

	// keep_separate remembers the pair and clears the flag
	if len(nearOnly.Items) > 1 {
		c2 := nearOnly.Items[1]
		if _, err := s.Review(ctx, c2.Product.ID, ReviewInput{Action: "keep_separate"}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, conn, `SELECT COUNT(*) FROM product_distinct`); n != 1 {
			t.Errorf("%d distinct pairs; want 1", n)
		}
	}

	// discard
	after, _ := s.ReviewQueue(ctx, 100, 0, "")
	if len(after.Items) == 0 {
		t.Fatal("nothing left to discard")
	}
	for _, it := range after.Items[:1] {
		d, err := s.Review(ctx, it.Product.ID, ReviewInput{Action: "discard"})
		if err != nil || d.Discard == nil || d.Discard.Kind != "discard" {
			t.Fatalf("discard = %+v, %v", d.Discard, err)
		}
		if _, err := s.Review(ctx, it.Product.ID, ReviewInput{Action: "discard"}); err == nil {
			t.Error("discarding twice must fail")
		}
	}
}
