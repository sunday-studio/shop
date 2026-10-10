package receipts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"shop/internal/match"
	"shop/internal/receipt"
	"shop/internal/store"
	"shop/internal/store/db"
)

// HeaderPatch carries optional header changes; nil means unchanged.
type HeaderPatch struct {
	ShopID      *int64
	PurchasedAt *time.Time
	TotalCents  *int64
}

func (s *Service) Get(ctx context.Context, id int64) (db.Receipt, []db.ReceiptLine, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return rec, nil, err
	}
	lines, err := s.q.ListLines(ctx, id)
	return rec, lines, err
}

func (s *Service) UpdateHeader(ctx context.Context, id int64, p HeaderPatch) (db.Receipt, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return rec, err
	}
	arg := db.UpdateReceiptHeaderParams{
		ShopID: rec.ShopID, PurchasedAt: rec.PurchasedAt, DateLocked: rec.DateLocked,
		TotalCents: rec.TotalCents, UpdatedAt: store.Now(), ID: id,
	}
	if p.ShopID != nil {
		if _, err := s.q.GetShop(ctx, *p.ShopID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return rec, fmt.Errorf("%w: shop %d does not exist", ErrInvalid, *p.ShopID)
			}
			return rec, err
		}
		arg.ShopID = *p.ShopID
	}
	if p.PurchasedAt != nil {
		arg.PurchasedAt, arg.DateLocked = store.NullTS(p.PurchasedAt), 1
	}
	if p.TotalCents != nil {
		arg.TotalCents = store.NullInt(p.TotalCents)
	}
	if _, err := s.q.UpdateReceiptHeader(ctx, arg); err != nil {
		return rec, err
	}
	return s.refreshReview(ctx, id)
}

func (s *Service) Confirm(ctx context.Context, id int64) (db.Receipt, error) {
	if _, err := s.get(ctx, id); err != nil {
		return db.Receipt{}, err
	}
	now := store.Now()
	return s.q.ConfirmReceipt(ctx, db.ConfirmReceiptParams{ConfirmedAt: store.Str(now), UpdatedAt: now, ID: id})
}

// LineInput is a full line as entered by the user.
type LineInput struct {
	RawText        string
	Kind           string
	Qty            float64
	Unit           string
	UnitPriceCents *int64
	LineTotalCents int64
	VATCode        string
	ParentLineID   *int64
}

func (in *LineInput) validate(ctx context.Context, q *db.Queries, receiptID int64, self int64) error {
	if !slices.Contains([]string{"item", "discount", "deposit", "bag", "other"}, in.Kind) {
		return fmt.Errorf("%w: kind %q", ErrInvalid, in.Kind)
	}
	if in.Unit == "" {
		in.Unit = "pc"
	}
	if !slices.Contains([]string{"pc", "kg", "l"}, in.Unit) {
		return fmt.Errorf("%w: unit %q", ErrInvalid, in.Unit)
	}
	if in.Qty == 0 {
		in.Qty = 1
	}
	if in.Qty < 0 {
		return fmt.Errorf("%w: qty must be positive", ErrInvalid)
	}
	if in.ParentLineID != nil {
		if *in.ParentLineID == self {
			return fmt.Errorf("%w: line cannot be its own parent", ErrInvalid)
		}
		p, err := q.GetLine(ctx, *in.ParentLineID)
		if err != nil || p.ReceiptID != receiptID {
			return fmt.Errorf("%w: parent line %d not on this receipt", ErrInvalid, *in.ParentLineID)
		}
	}
	return nil
}

func (s *Service) CreateLine(ctx context.Context, receiptID int64, in LineInput) (db.ReceiptLine, error) {
	if _, err := s.get(ctx, receiptID); err != nil {
		return db.ReceiptLine{}, err
	}
	if err := in.validate(ctx, s.q, receiptID, 0); err != nil {
		return db.ReceiptLine{}, err
	}
	var line db.ReceiptLine
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		n, err := q.MaxLineNo(ctx, receiptID)
		if err != nil {
			return err
		}
		line, err = q.InsertLine(ctx, db.InsertLineParams{
			ReceiptID: receiptID, LineNo: n + 1, RawText: in.RawText, NormText: match.Normalise(in.RawText),
			Kind: in.Kind, Qty: in.Qty, Unit: in.Unit, UnitPriceCents: store.NullInt(in.UnitPriceCents),
			LineTotalCents: in.LineTotalCents, VatCode: store.Str(in.VATCode),
			ParentLineID: store.NullInt(in.ParentLineID), UserEdited: 1,
		})
		return err
	})
	if err != nil {
		return line, err
	}
	_, err = s.refreshReview(ctx, receiptID)
	return line, err
}

func (s *Service) UpdateLine(ctx context.Context, lineID int64, in LineInput) (db.ReceiptLine, error) {
	cur, err := s.q.GetLine(ctx, lineID)
	if errors.Is(err, sql.ErrNoRows) {
		return cur, ErrNotFound
	} else if err != nil {
		return cur, err
	}
	if err := in.validate(ctx, s.q, cur.ReceiptID, lineID); err != nil {
		return cur, err
	}
	line, err := s.q.UpdateLine(ctx, db.UpdateLineParams{
		RawText: in.RawText, NormText: match.Normalise(in.RawText), Kind: in.Kind, Qty: in.Qty, Unit: in.Unit,
		UnitPriceCents: store.NullInt(in.UnitPriceCents), LineTotalCents: in.LineTotalCents,
		VatCode: store.Str(in.VATCode), ParentLineID: store.NullInt(in.ParentLineID), ID: lineID,
	})
	if err != nil {
		return line, err
	}
	_, err = s.refreshReview(ctx, cur.ReceiptID)
	return line, err
}

func (s *Service) DeleteLine(ctx context.Context, lineID int64) error {
	cur, err := s.q.GetLine(ctx, lineID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := s.q.DeleteLine(ctx, lineID); err != nil {
		return err
	}
	// Mark the receipt as edited so a reparse doesn't silently restore the line.
	if err := s.markHeaderEdited(ctx, cur.ReceiptID); err != nil {
		return err
	}
	_, err = s.refreshReview(ctx, cur.ReceiptID)
	return err
}

func (s *Service) markHeaderEdited(ctx context.Context, id int64) error {
	rec, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.q.UpdateReceiptHeader(ctx, db.UpdateReceiptHeaderParams{
		ShopID: rec.ShopID, PurchasedAt: rec.PurchasedAt, DateLocked: rec.DateLocked,
		TotalCents: rec.TotalCents, UpdatedAt: store.Now(), ID: id,
	})
	return err
}

// refreshReview recomputes the sum and the arithmetic review reasons from
// the current lines. OCR-quality reasons (low_conf, unparsed_rows) are
// dropped once the user has touched the receipt.
func (s *Service) refreshReview(ctx context.Context, id int64) (db.Receipt, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return rec, err
	}
	lines, err := s.q.ListLines(ctx, id)
	if err != nil {
		return rec, err
	}
	var sum int64
	reasons := []string{}
	lineMath := false
	for _, l := range lines {
		sum += l.LineTotalCents
		if l.Kind == "item" && l.UnitPriceCents.Valid {
			want := int64(l.Qty*float64(l.UnitPriceCents.Int64) + 0.5)
			tol := int64(0)
			if l.Unit != "pc" {
				tol = 1
			}
			if d := want - l.LineTotalCents; d > tol || d < -tol {
				lineMath = true
			}
		}
	}
	switch {
	case !rec.TotalCents.Valid:
		reasons = append(reasons, receipt.ReasonNoTotal)
	case sum-rec.TotalCents.Int64 > 1 || rec.TotalCents.Int64-sum > 1:
		reasons = append(reasons, receipt.ReasonTotalMismatch)
	}
	if lineMath {
		reasons = append(reasons, receipt.ReasonLineMath)
	}
	if !rec.PurchasedAt.Valid {
		reasons = append(reasons, receipt.ReasonNoDate)
	}

	status := rec.Status
	if !rec.ConfirmedAt.Valid && (status == "ok" || status == "needs_review") {
		status = "ok"
		if len(reasons) > 0 {
			status = "needs_review"
		}
	}
	b, _ := json.Marshal(reasons)
	if err := s.q.SetReview(ctx, db.SetReviewParams{
		ParsedSumCents: sql.NullInt64{Int64: sum, Valid: true}, ReviewReasons: store.Str(string(b)),
		Status: status, UpdatedAt: store.Now(), ID: id,
	}); err != nil {
		return rec, err
	}
	return s.get(ctx, id)
}

// ListFilter selects receipts for listing.
type ListFilter struct {
	ShopID   *int64
	Status   *string
	From, To *time.Time // [From, To)
	BeforeID *int64
	Limit    int64
}

// List returns receipts with their item and unlinked line counts.
func (s *Service) List(ctx context.Context, f ListFilter) ([]db.ListReceiptsRow, error) {
	arg := db.ListReceiptsParams{Lim: f.Limit}
	if f.ShopID != nil {
		arg.ShopID = *f.ShopID
	}
	if f.Status != nil {
		arg.Status = *f.Status
	}
	if f.From != nil {
		arg.FromTs = store.TS(*f.From)
	}
	if f.To != nil {
		arg.ToTs = store.TS(*f.To)
	}
	if f.BeforeID != nil {
		arg.BeforeID = *f.BeforeID
	}
	return s.q.ListReceipts(ctx, arg)
}

// ReparseAll queues a re-parse for every unedited receipt (optionally only
// those with the given status). It returns how many were queued.
func (s *Service) ReparseAll(ctx context.Context, status string) (int, error) {
	var st any
	if status != "" {
		st = status
	}
	ids, err := s.q.ListReceiptIDsForReparse(ctx, st)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := s.Requeue(ctx, id, false, false); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// Counts returns the number of receipts per status (every status present).
func (s *Service) Counts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.q.CountReceiptsByStatus(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{"pending": 0, "processing": 0, "needs_review": 0, "ok": 0, "failed": 0}
	var total int64
	for _, r := range rows {
		out[r.Status] = r.N
		total += r.N
	}
	out["total"] = total
	return out, nil
}
