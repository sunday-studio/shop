package autoprod

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"shop/internal/match"
	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/translate"
)

// SuggestedMerge is the product the card could be merged with.
type SuggestedMerge struct {
	ProductID int64
	Name      string
	NameEN    string
	Brand     string
	Score     float64
}

// SourceLine is a receipt line a product was created from.
type SourceLine struct {
	LineID         int64
	ReceiptID      int64
	RawText        string
	ShopName       string
	PurchasedAt    string // RFC 3339, the upload time when the receipt has no date
	LineTotalCents int64
}

type QueueItem struct {
	Product         db.Product
	Reason          string
	SuggestedMerge  *SuggestedMerge
	SourceLineCount int
	SourceLines     []SourceLine // at most 3
}

type Queue struct {
	Total  int
	Counts map[string]int
	Items  []QueueItem
}

// ReviewQueue lists the products waiting for a decision (§4.4). limit 0 returns only the counts.
func (s *Service) ReviewQueue(ctx context.Context, limit, offset int, reason string) (Queue, error) {
	out := Queue{Counts: map[string]int{}}
	for _, r := range Reasons {
		out.Counts[r] = 0
	}
	rows, err := s.q.ReviewCounts(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Counts[r.Reason] = int(r.N)
		out.Total += int(r.N)
	}
	if reason != "" {
		if !slices.Contains(Reasons, reason) {
			return out, errf(ErrInvalid, "unknown reason %q", reason)
		}
	}
	if limit <= 0 {
		return out, nil
	}
	var rsn any
	if reason != "" {
		rsn = reason
	}
	ps, err := s.q.ReviewQueue(ctx, db.ReviewQueueParams{Reason: rsn, Lim: int64(limit), Off: int64(offset)})
	if err != nil {
		return out, err
	}
	for _, p := range ps {
		it := QueueItem{Product: p, Reason: p.ReviewReason.String}
		if p.SuggestedMergeID.Valid {
			if sp, err := s.q.GetProduct(ctx, p.SuggestedMergeID.Int64); err == nil {
				it.SuggestedMerge = &SuggestedMerge{ProductID: sp.ID, Name: sp.Name, NameEN: sp.NameEn.String, Brand: sp.Brand.String,
					Score: match.Score(match.Normalise(p.Name), match.Normalise(sp.Name))}
			}
		}
		n, err := s.q.CountSourceLines(ctx, nid(p.ID))
		if err != nil {
			return out, err
		}
		it.SourceLineCount = int(n)
		ls, err := s.q.SourceLines(ctx, db.SourceLinesParams{ProductID: nid(p.ID), Limit: 3})
		if err != nil {
			return out, err
		}
		for _, l := range ls {
			at := l.CreatedAt
			if l.PurchasedAt.Valid {
				at = l.PurchasedAt.String
			}
			it.SourceLines = append(it.SourceLines, SourceLine{LineID: l.LineID, ReceiptID: l.ReceiptID, RawText: l.RawText,
				ShopName: l.ShopName, PurchasedAt: at, LineTotalCents: l.LineTotalCents})
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// ReviewInput is the body of POST /v1/products/{id}/review.
type ReviewInput struct {
	Action        string
	CategoryID    *int64
	Name          *string
	IntoProductID *int64
}

// ReviewResult carries whichever outcome the action had.
type ReviewResult struct {
	Product db.Product
	Merge   *MergeResult // merge
	Discard *MergeResult // discard
}

// Review applies a decision to a product in the queue.
func (s *Service) Review(ctx context.Context, id int64, in ReviewInput) (ReviewResult, error) {
	var res ReviewResult
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		p, err := q.GetProduct(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if p.NeedsReview == 0 {
			return errf(ErrConflict, "this product is no longer waiting for review")
		}
		now := store.TS(s.now())
		clear := func(pid int64) error {
			return q.SetProductReview(ctx, db.SetProductReviewParams{UpdatedAt: now, ID: pid})
		}
		switch in.Action {
		case "accept":
			if in.CategoryID != nil {
				if _, err := q.GetCategory(ctx, *in.CategoryID); err != nil {
					return errf(ErrInvalid, "category %d does not exist", *in.CategoryID)
				}
				if err := q.SetProductCategory(ctx, db.SetProductCategoryParams{CategoryID: nid(*in.CategoryID), CategorySource: store.Str("manual"), UpdatedAt: now, ID: id}); err != nil {
					return err
				}
			}
			if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
				name := strings.TrimSpace(*in.Name)
				if err := q.SetProductNameManual(ctx, db.SetProductNameManualParams{Name: name, UpdatedAt: now, ID: id}); err != nil {
					return err
				}
				if p.NameEnSource.String != "manual" && p.NameEnSource.String != "openfoodfacts" {
					en := sql.NullString{}
					src := sql.NullString{}
					if t := translate.Translate(name); t.OK() {
						en, src = store.Str(t.Text), store.Str("dictionary")
					}
					if err := q.SetProductEnglish(ctx, db.SetProductEnglishParams{NameEn: en, NameEnSource: src, ID: id}); err != nil {
						return err
					}
				}
			}
			if err := clear(id); err != nil {
				return err
			}
		case "keep_separate":
			if p.SuggestedMergeID.Valid {
				a, b := id, p.SuggestedMergeID.Int64
				if a > b {
					a, b = b, a
				}
				if err := q.InsertDistinctPair(ctx, db.InsertDistinctPairParams{AID: a, BID: b, CreatedAt: now}); err != nil {
					return err
				}
			}
			if err := clear(id); err != nil {
				return err
			}
		case "merge":
			other := p.SuggestedMergeID.Int64
			if in.IntoProductID != nil {
				other = *in.IntoProductID
			}
			if other == 0 {
				return errf(ErrInvalid, "no product to merge with: pass into_product_id")
			}
			from, into := id, other
			if p.ReviewReason.String == ReasonBarcodeName {
				from, into = other, id // the barcode product on the card survives
			}
			m, err := s.mergeTx(ctx, q, from, into)
			if err != nil {
				return err
			}
			surv := m.Product
			if surv.ID == id { // the card survived: its question is answered unless it still has no category
				reason := sql.NullString{}
				review := int64(0)
				if !surv.CategoryID.Valid {
					review, reason = 1, store.Str(ReasonNoCategory)
				}
				if err := q.SetProductReview(ctx, db.SetProductReviewParams{NeedsReview: review, ReviewReason: reason, UpdatedAt: now, ID: id}); err != nil {
					return err
				}
				if m.Product, err = q.GetProduct(ctx, id); err != nil {
					return err
				}
			}
			res.Merge = &m
			res.Product = m.Product
			return nil
		case "discard":
			m, err := s.discardTx(ctx, q, p)
			if err != nil {
				return err
			}
			res.Discard = &m
			res.Product = p
			return nil
		default:
			return errf(ErrInvalid, "action must be accept, merge, keep_separate or discard")
		}
		res.Product, err = q.GetProduct(ctx, id)
		return err
	})
	return res, err
}
