package receipts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"shop/internal/match"
	"shop/internal/ocr"
	"shop/internal/receipt"
	"shop/internal/store"
	"shop/internal/store/db"
)

const settingRequantify = "requantify_parser_version"

// RequantifyOnce brings the quantities of receipts read by an older parser up to the current rules: the
// stored OCR is parsed again and, for every line nobody edited whose text and amount are found
// again, quantity, unit and unit price are replaced. Nothing else changes (not the lines, not the
// links, not the status, not confirmed receipts' totals), so receipts the user already confirmed get
// the right per-unit prices too. It runs once per parser version (a setting remembers).
func (s *Service) RequantifyOnce(ctx context.Context) (int, error) {
	want := fmt.Sprint(receipt.ParserVersion)
	if v, err := s.q.GetSetting(ctx, settingRequantify); err == nil && v == want {
		return 0, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	n, err := s.Requantify(ctx)
	if err != nil {
		return n, err
	}
	return n, s.q.PutSetting(ctx, db.PutSettingParams{Key: settingRequantify, Value: want, UpdatedAt: store.Now()})
}

// Requantify does the work of RequantifyOnce unconditionally and returns how many lines changed.
func (s *Service) Requantify(ctx context.Context) (int, error) {
	ids, err := s.q.ListReceiptIDsToRequantify(ctx, sql.NullInt64{Int64: receipt.ParserVersion, Valid: true})
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, id := range ids {
		n, err := s.requantifyReceipt(ctx, id)
		if err != nil {
			return changed, err
		}
		changed += n
	}
	return changed, nil
}

type lineKey struct {
	kind  string
	norm  string
	total int64
}

func (s *Service) requantifyReceipt(ctx context.Context, id int64) (int, error) {
	rec, err := s.q.GetReceipt(ctx, id)
	if err != nil || !rec.RawOcrJson.Valid {
		return 0, err
	}
	var res ocr.Result
	if json.Unmarshal([]byte(rec.RawOcrJson.String), &res) != nil {
		return 0, nil
	}
	loc := s.loc
	if loc == nil {
		loc = time.UTC
	}
	parsed := receipt.Parse(res, receipt.Options{Location: loc, Now: time.Now()})
	byKey := map[lineKey][]receipt.Line{}
	for _, l := range parsed.Lines {
		k := lineKey{string(l.Kind), match.Normalise(l.RawText), l.LineTotalCents}
		byKey[k] = append(byKey[k], l)
	}
	lines, err := s.q.ListLines(ctx, id)
	if err != nil {
		return 0, err
	}
	changed := 0
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		for _, l := range lines {
			if l.UserEdited != 0 || (l.Kind != "item" && l.Kind != "deposit" && l.Kind != "bag") {
				continue
			}
			cands := byKey[lineKey{l.Kind, l.NormText, l.LineTotalCents}]
			if len(cands) == 0 || cands[0].UnitPriceCents == nil {
				continue
			}
			same := true
			for _, c := range cands { // the same text and amount twice must read the same way
				same = same && c.UnitPriceCents != nil && c.Qty == cands[0].Qty && c.Unit == cands[0].Unit && *c.UnitPriceCents == *cands[0].UnitPriceCents
			}
			if !same {
				continue
			}
			n := cands[0]
			if n.Qty == l.Qty && n.Unit == l.Unit && l.UnitPriceCents.Valid && l.UnitPriceCents.Int64 == *n.UnitPriceCents {
				continue
			}
			if err := q.SetLineQuantity(ctx, db.SetLineQuantityParams{
				Qty: n.Qty, Unit: n.Unit, UnitPriceCents: sql.NullInt64{Int64: *n.UnitPriceCents, Valid: true}, ID: l.ID,
			}); err != nil {
				return err
			}
			changed++
		}
		return nil
	})
	return changed, err
}
