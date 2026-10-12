package receipts

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"shop/internal/store"
	"shop/internal/store/db"
)

// Duplicate detection. A receipt is a "possible duplicate" of an OLDER receipt (lower id: the one
// uploaded first) when
//
//   - both are parsed (ok or needs_review) and have a printed total and a purchase date,
//   - the printed totals are equal (to the cent) and above zero,
//   - the purchase day is the same local day (Europe/Amsterdam; a date-only purchased_at is stored
//     as local midnight, so it falls on the right day),
//   - the shop is the same, or at least one of the two has no shop (unassigned),
//   - and, for baskets under SmallBasketCents, both have at least MinSmallBasketItems item lines
//     or their item lines overlap by LineOverlap.
//
// The match is "total+date+lines" when at least LineOverlap of the normalised item lines are shared
// (multiset intersection divided by the longer item list), otherwise "total+date". The time of day
// is reported as TimeMatches (both have a real time and it is equal to the minute) but does not
// change the match kind. Nothing is stored except the user's "keep both" decision; nothing is ever
// deleted or merged by this code. A multi-page receipt is one stitched image, so it is one receipt.
const (
	LineOverlap         = 0.6
	SmallBasketCents    = 1000
	MinSmallBasketItems = 3

	MatchTotalDate      = "total+date"
	MatchTotalDateLines = "total+date+lines"
)

// Duplicate describes the older receipt a newer one looks like.
type Duplicate struct {
	ReceiptID      int64
	PurchasedAt    time.Time
	TotalCents     int64
	ShopID         *int64
	Match          string
	SharedLines    int  // item lines both receipts have
	ItemLines      int  // item lines on the newer receipt
	OtherItemLines int  // item lines on the older receipt
	TimeMatches    bool // same time of day to the minute (only when both carry a time)
}

type dupRec struct {
	id    int64
	shop  *int64
	at    time.Time
	total int64
}

func toDupRec(id int64, shop *int64, at string, total int64) (dupRec, bool) {
	t, err := store.ParseTS(at)
	if err != nil {
		return dupRec{}, false
	}
	return dupRec{id: id, shop: shop, at: t, total: total}, true
}

func (s *Service) localDay(t time.Time) time.Time {
	y, m, d := t.In(s.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, s.loc)
}

func (s *Service) hasTime(t time.Time) bool {
	l := t.In(s.loc)
	return l.Hour() != 0 || l.Minute() != 0
}

func shopsCompatible(a, b *int64) bool { return a == nil || b == nil || *a == *b }

// PossibleDuplicate returns the older receipt that receipt id looks like, or nil.
func (s *Service) PossibleDuplicate(ctx context.Context, id int64) (*Duplicate, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !rec.TotalCents.Valid || rec.TotalCents.Int64 <= 0 || !rec.PurchasedAt.Valid || (rec.Status != "ok" && rec.Status != "needs_review") {
		return nil, nil
	}
	if n, err := s.q.IsDuplicateDismissed(ctx, id); err != nil || n > 0 {
		return nil, err
	}
	me, ok := toDupRec(id, store.IntPtr(rec.ShopID), rec.PurchasedAt.String, rec.TotalCents.Int64)
	if !ok {
		return nil, nil
	}
	day := s.localDay(me.at)
	rows, err := s.q.ListDuplicateCandidates(ctx, db.ListDuplicateCandidatesParams{
		TotalCents: sql.NullInt64{Int64: me.total, Valid: true}, PurchasedAt: store.Str(store.TS(day)),
		PurchasedAt_2: store.Str(store.TS(day.AddDate(0, 0, 1))), ID: id,
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	var older []dupRec
	for _, r := range rows {
		if o, ok := toDupRec(r.ID, store.IntPtr(r.ShopID), r.PurchasedAt.String, r.TotalCents.Int64); ok {
			older = append(older, o)
		}
	}
	norms, err := s.itemNorms(ctx, append([]int64{id}, ids(older)...))
	if err != nil {
		return nil, err
	}
	return s.best(me, older, norms), nil
}

// PossibleDuplicates returns, for every receipt that currently looks like an older one, that match.
// Dismissed receipts are left out.
func (s *Service) PossibleDuplicates(ctx context.Context) (map[int64]*Duplicate, error) {
	rows, err := s.q.ListDatedReceiptsForDuplicates(ctx)
	if err != nil {
		return nil, err
	}
	dismissed, err := s.q.ListDuplicateDismissals(ctx)
	if err != nil {
		return nil, err
	}
	skip := map[int64]bool{}
	for _, id := range dismissed {
		skip[id] = true
	}
	type key struct {
		total int64
		day   string
	}
	groups := map[key][]dupRec{}
	for _, r := range rows {
		rec, ok := toDupRec(r.ID, store.IntPtr(r.ShopID), r.PurchasedAt.String, r.TotalCents.Int64)
		if !ok {
			continue
		}
		k := key{rec.total, s.localDay(rec.at).Format(time.DateOnly)}
		groups[k] = append(groups[k], rec)
	}
	out := map[int64]*Duplicate{}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		norms, err := s.itemNorms(ctx, ids(g))
		if err != nil {
			return nil, err
		}
		for i, me := range g { // g is ordered by id
			if skip[me.id] || i == 0 {
				continue
			}
			if d := s.best(me, g[:i], norms); d != nil {
				out[me.id] = d
			}
		}
	}
	return out, nil
}

// DismissDuplicate remembers "keep both" for a receipt. Idempotent.
func (s *Service) DismissDuplicate(ctx context.Context, id int64) error {
	if _, err := s.get(ctx, id); err != nil {
		return err
	}
	return s.q.DismissDuplicate(ctx, db.DismissDuplicateParams{ReceiptID: id, DismissedAt: store.Now()})
}

// UndismissDuplicate forgets the decision (Undo). Idempotent.
func (s *Service) UndismissDuplicate(ctx context.Context, id int64) error {
	if _, err := s.get(ctx, id); err != nil {
		return err
	}
	return s.q.UndismissDuplicate(ctx, id)
}

func ids(rs []dupRec) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.id
	}
	return out
}

func (s *Service) itemNorms(ctx context.Context, receiptIDs []int64) (map[int64][]string, error) {
	rows, err := s.q.ListItemNormsForReceipts(ctx, receiptIDs)
	if err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, r := range rows {
		out[r.ReceiptID] = append(out[r.ReceiptID], r.NormText)
	}
	return out, nil
}

// best picks the strongest match among older receipts: line overlap first, then equal time of day,
// then the oldest purchase date, then the lowest id.
func (s *Service) best(me dupRec, older []dupRec, norms map[int64][]string) *Duplicate {
	var found []*Duplicate
	for _, o := range older {
		if d := s.pair(me, o, norms[me.id], norms[o.id]); d != nil {
			found = append(found, d)
		}
	}
	if len(found) == 0 {
		return nil
	}
	slices.SortStableFunc(found, func(a, b *Duplicate) int {
		if x, y := a.Match == MatchTotalDateLines, b.Match == MatchTotalDateLines; x != y {
			if x {
				return -1
			}
			return 1
		}
		if a.TimeMatches != b.TimeMatches {
			if a.TimeMatches {
				return -1
			}
			return 1
		}
		if c := a.PurchasedAt.Compare(b.PurchasedAt); c != 0 {
			return c
		}
		return int(a.ReceiptID - b.ReceiptID)
	})
	return found[0]
}

func (s *Service) pair(me, o dupRec, mine, theirs []string) *Duplicate {
	if me.total != o.total || me.total <= 0 || !shopsCompatible(me.shop, o.shop) ||
		!s.localDay(me.at).Equal(s.localDay(o.at)) {
		return nil
	}
	shared := sharedLines(mine, theirs)
	longest := max(len(mine), len(theirs))
	strong := longest > 0 && float64(shared)/float64(longest) >= LineOverlap
	if me.total < SmallBasketCents && !strong && min(len(mine), len(theirs)) < MinSmallBasketItems {
		return nil
	}
	d := &Duplicate{
		ReceiptID: o.id, PurchasedAt: o.at, TotalCents: o.total, ShopID: o.shop, Match: MatchTotalDate,
		SharedLines: shared, ItemLines: len(mine), OtherItemLines: len(theirs),
		TimeMatches: s.hasTime(me.at) && s.hasTime(o.at) && me.at.Truncate(time.Minute).Equal(o.at.Truncate(time.Minute)),
	}
	if strong {
		d.Match = MatchTotalDateLines
	}
	return d
}

// sharedLines is the size of the multiset intersection of two lists of normalised item lines.
func sharedLines(a, b []string) int {
	count := map[string]int{}
	for _, x := range b {
		count[x]++
	}
	n := 0
	for _, x := range a {
		if count[x] > 0 {
			count[x]--
			n++
		}
	}
	return n
}
