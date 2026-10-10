package autoprod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"shop/internal/store"
	"shop/internal/store/db"
)

// ErrDifferentBarcodes is shown to the user as is.
var ErrDifferentBarcodes = errors.New("These two have different barcodes.")

// ProductRef names a product in a result.
type ProductRef struct {
	ID   int64
	Name string
}

// MergeResult is returned by Merge, Discard and Split.
type MergeResult struct {
	MergeID      int64
	Kind         string
	Product      db.Product // survivor (merge), new product (split)
	MergedAway   *ProductRef
	LinesMoved   int // lines moved / unlinked
	AliasesMoved int
}

type reviewBefore struct {
	NeedsReview int64  `json:"needs_review"`
	Reason      string `json:"reason"`
	Suggested   int64  `json:"suggested"`
	Barcode     string `json:"barcode"`
}

// filled records what a merge copied into the surviving product, so undo can clear exactly that.
type filled struct {
	Barcode        string   `json:"barcode,omitempty"`
	Brand          string   `json:"brand,omitempty"`
	ImagePath      string   `json:"image_path,omitempty"`
	NetQuantity    *float64 `json:"net_quantity,omitempty"`
	NetUnit        string   `json:"net_unit,omitempty"`
	OffCode        string   `json:"off_code,omitempty"`
	CategoryID     int64    `json:"category_id,omitempty"`
	CategorySource string   `json:"category_source,omitempty"`
	Nutrition      bool     `json:"nutrition,omitempty"`
	NameEn         string   `json:"name_en,omitempty"`
	NameEnSource   string   `json:"name_en_source,omitempty"`
	PrevNameEn     string   `json:"prev_name_en,omitempty"`
	PrevNameEnSrc  string   `json:"prev_name_en_source,omitempty"`
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func reviewOf(p db.Product) reviewBefore {
	return reviewBefore{NeedsReview: p.NeedsReview, Reason: p.ReviewReason.String, Suggested: p.SuggestedMergeID.Int64, Barcode: p.Barcode.String}
}

// Merge makes the product `from` disappear into `into`: lines and aliases move over, empty fields of
// `into` are filled from `from`, `from` is soft-deleted with merged_into_id. Reversible with Undo.
func (s *Service) Merge(ctx context.Context, fromID, intoID int64) (MergeResult, error) {
	var res MergeResult
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		var err error
		res, err = s.mergeTx(ctx, q, fromID, intoID)
		return err
	})
	return res, err
}

func (s *Service) mergeTx(ctx context.Context, q *db.Queries, fromID, intoID int64) (MergeResult, error) {
	if fromID == intoID {
		return MergeResult{}, errf(ErrInvalid, "a product cannot be merged into itself")
	}
	from, err := q.GetProduct(ctx, fromID)
	if err != nil {
		return MergeResult{}, notFound(err)
	}
	into, err := q.GetProduct(ctx, intoID)
	if err != nil {
		return MergeResult{}, notFound(err)
	}
	if from.Barcode.Valid && into.Barcode.Valid && from.Barcode.String != into.Barcode.String {
		return MergeResult{}, ErrDifferentBarcodes
	}
	now := store.TS(s.now())

	lineIDs, err := q.ListLineIDsByProduct(ctx, nid(fromID))
	if err != nil {
		return MergeResult{}, err
	}
	aliases, err := q.ListAliasesByProduct(ctx, fromID)
	if err != nil {
		return MergeResult{}, err
	}
	for _, id := range lineIDs {
		if _, err := q.MoveLineIfLinked(ctx, db.MoveLineIfLinkedParams{ToProduct: nid(intoID), ID: id, Expected: nid(fromID)}); err != nil {
			return MergeResult{}, err
		}
	}
	aliasIDs := make([]int64, 0, len(aliases))
	for _, a := range aliases {
		if _, err := q.MoveAliasIfOwned(ctx, db.MoveAliasIfOwnedParams{ToProduct: intoID, ID: a.ID, Expected: fromID}); err != nil {
			return MergeResult{}, err
		}
		aliasIDs = append(aliasIDs, a.ID)
	}

	// free the barcode (UNIQUE) before it is copied over
	if err := q.MarkProductMerged(ctx, db.MarkProductMergedParams{
		DeletedAt: store.Str(now), MergedIntoID: nid(intoID), UpdatedAt: now, ID: fromID}); err != nil {
		return MergeResult{}, err
	}
	if err := q.ClearSuggestionsPointingAt(ctx, db.ClearSuggestionsPointingAtParams{UpdatedAt: now, SuggestedMergeID: nid(fromID)}); err != nil {
		return MergeResult{}, err
	}

	var f filled
	fill := db.FillProductFieldsParams{
		Barcode: into.Barcode, Brand: into.Brand, ImagePath: into.ImagePath, NetQuantity: into.NetQuantity,
		NetUnit: into.NetUnit, OffCode: into.OffCode, CategoryID: into.CategoryID, CategorySource: into.CategorySource,
		UpdatedAt: now, ID: intoID,
	}
	if !into.Barcode.Valid && from.Barcode.Valid {
		fill.Barcode, f.Barcode = from.Barcode, from.Barcode.String
	}
	if !into.Brand.Valid && from.Brand.Valid {
		fill.Brand, f.Brand = from.Brand, from.Brand.String
	}
	if !into.ImagePath.Valid && from.ImagePath.Valid {
		fill.ImagePath, f.ImagePath = from.ImagePath, from.ImagePath.String
	}
	if !into.NetQuantity.Valid && from.NetQuantity.Valid {
		fill.NetQuantity, fill.NetUnit = from.NetQuantity, from.NetUnit
		v := from.NetQuantity.Float64
		f.NetQuantity, f.NetUnit = &v, from.NetUnit.String
	}
	if !into.OffCode.Valid && from.OffCode.Valid {
		fill.OffCode, f.OffCode = from.OffCode, from.OffCode.String
	}
	if !into.CategoryID.Valid && from.CategoryID.Valid {
		fill.CategoryID, fill.CategorySource = from.CategoryID, from.CategorySource
		f.CategoryID, f.CategorySource = from.CategoryID.Int64, from.CategorySource.String
	}
	if err := q.FillProductFields(ctx, fill); err != nil {
		return MergeResult{}, err
	}
	if _, err := q.GetNutrition(ctx, intoID); errors.Is(err, sql.ErrNoRows) {
		if n, err := q.GetNutrition(ctx, fromID); err == nil {
			if _, err := q.UpsertNutrition(ctx, db.UpsertNutritionParams{ProductID: intoID, Per: n.Per, EnergyKcal: n.EnergyKcal,
				Fat: n.Fat, SaturatedFat: n.SaturatedFat, Carbs: n.Carbs, Sugars: n.Sugars, Fibre: n.Fibre, Protein: n.Protein,
				Salt: n.Salt, Source: n.Source}); err != nil {
				return MergeResult{}, err
			}
			f.Nutrition = true
		}
	}
	// a name_en typed by the user wins over anything else
	if from.NameEnSource.String == "manual" && into.NameEnSource.String != "manual" {
		if err := q.SetProductEnglish(ctx, db.SetProductEnglishParams{NameEn: from.NameEn, NameEnSource: from.NameEnSource, ID: intoID}); err != nil {
			return MergeResult{}, err
		}
		f.NameEn, f.NameEnSource = from.NameEn.String, from.NameEnSource.String
		f.PrevNameEn, f.PrevNameEnSrc = into.NameEn.String, into.NameEnSource.String
	}

	m, err := q.InsertMerge(ctx, db.InsertMergeParams{
		FromProductID: fromID, IntoProductID: nid(intoID), LineIds: mustJSON(lineIDs), AliasIds: mustJSON(aliasIDs),
		AliasRows: "[]", IntoFieldsBefore: mustJSON(f), FromReviewBefore: mustJSON(reviewOf(from)), Kind: "merge", CreatedAt: now,
	})
	if err != nil {
		return MergeResult{}, err
	}
	survivor, err := q.GetProduct(ctx, intoID)
	if err != nil {
		return MergeResult{}, err
	}
	return MergeResult{MergeID: m.ID, Kind: "merge", Product: survivor, MergedAway: &ProductRef{ID: from.ID, Name: from.Name},
		LinesMoved: len(lineIDs), AliasesMoved: len(aliasIDs)}, nil
}

func nid(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Discard soft-deletes an auto-created product, unlinks its lines and removes its aliases.
// Manual products cannot be discarded.
func (s *Service) Discard(ctx context.Context, id int64) (MergeResult, error) {
	var res MergeResult
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		p, err := q.GetProduct(ctx, id)
		if err != nil {
			return notFound(err)
		}
		res, err = s.discardTx(ctx, q, p)
		return err
	})
	return res, err
}

func (s *Service) discardTx(ctx context.Context, q *db.Queries, p db.Product) (MergeResult, error) {
	id := p.ID
	if p.CreatedVia == "manual" {
		return MergeResult{}, errf(ErrInvalid, "a product you made yourself cannot be discarded")
	}
	now := store.TS(s.now())
	lineIDs, err := q.ListLineIDsByProduct(ctx, nid(id))
	if err != nil {
		return MergeResult{}, err
	}
	for _, lid := range lineIDs {
		if _, err := q.MoveLineIfLinked(ctx, db.MoveLineIfLinkedParams{ID: lid, Expected: nid(id)}); err != nil {
			return MergeResult{}, err
		}
	}
	aliases, err := q.ListAliasesByProduct(ctx, id)
	if err != nil {
		return MergeResult{}, err
	}
	ids := make([]int64, len(aliases))
	for i, a := range aliases {
		ids[i] = a.ID
		if _, err := q.DeleteAlias(ctx, a.ID); err != nil {
			return MergeResult{}, err
		}
	}
	if err := q.MarkProductDiscarded(ctx, db.MarkProductDiscardedParams{DeletedAt: store.Str(now), UpdatedAt: now, ID: id}); err != nil {
		return MergeResult{}, err
	}
	if err := q.ClearSuggestionsPointingAt(ctx, db.ClearSuggestionsPointingAtParams{UpdatedAt: now, SuggestedMergeID: nid(id)}); err != nil {
		return MergeResult{}, err
	}
	m, err := q.InsertMerge(ctx, db.InsertMergeParams{
		FromProductID: id, LineIds: mustJSON(lineIDs), AliasIds: mustJSON(ids), AliasRows: mustJSON(aliases),
		IntoFieldsBefore: "{}", FromReviewBefore: mustJSON(reviewOf(p)), Kind: "discard", CreatedAt: now,
	})
	if err != nil {
		return MergeResult{}, err
	}
	return MergeResult{MergeID: m.ID, Kind: "discard", Product: p, LinesMoved: len(lineIDs), AliasesMoved: len(ids)}, nil
}

// UndoResult is returned by Undo.
type UndoResult struct {
	Kind          string
	Product       db.Product // restored product
	Into          db.Product // survivor (merge); same as Product for discard
	LinesRestored int
	LinesSkipped  int
}

// Undo reverses a merge, discard or split recorded in product_merges. It is exact for 30 days: lines
// the user relinked since are left alone and counted in LinesSkipped.
func (s *Service) Undo(ctx context.Context, mergeID int64) (UndoResult, error) {
	var res UndoResult
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		m, err := q.GetMerge(ctx, mergeID)
		if err != nil {
			return notFound(err)
		}
		if m.UndoneAt.Valid {
			return errf(ErrConflict, "already undone")
		}
		created, _ := store.ParseTS(m.CreatedAt)
		if s.now().Sub(created) > UndoWindowDays*24*time.Hour {
			return errf(ErrGone, "older than %d days", UndoWindowDays)
		}
		now := store.TS(s.now())
		var lineIDs, aliasIDs []int64
		_ = json.Unmarshal([]byte(m.LineIds), &lineIDs)
		_ = json.Unmarshal([]byte(m.AliasIds), &aliasIDs)
		var rb reviewBefore
		_ = json.Unmarshal([]byte(m.FromReviewBefore), &rb)
		res.Kind = m.Kind

		switch m.Kind {
		case "merge":
			intoID := m.IntoProductID.Int64
			var f filled
			_ = json.Unmarshal([]byte(m.IntoFieldsBefore), &f)
			if err := s.clearFilled(ctx, q, intoID, f, now); err != nil {
				return err
			}
			if err := restoreProduct(ctx, q, m.FromProductID, rb, now); err != nil {
				return err
			}
			res.LinesRestored, res.LinesSkipped, err = moveLines(ctx, q, lineIDs, nid(intoID), nid(m.FromProductID))
			if err != nil {
				return err
			}
			for _, aid := range aliasIDs {
				if _, err := q.MoveAliasIfOwned(ctx, db.MoveAliasIfOwnedParams{ToProduct: m.FromProductID, ID: aid, Expected: intoID}); err != nil {
					return err
				}
			}
			if res.Into, err = q.GetProduct(ctx, intoID); err != nil {
				return err
			}
		case "discard":
			if err := restoreProduct(ctx, q, m.FromProductID, rb, now); err != nil {
				return err
			}
			res.LinesRestored, res.LinesSkipped, err = moveLines(ctx, q, lineIDs, sql.NullInt64{}, nid(m.FromProductID))
			if err != nil {
				return err
			}
			var rows []db.ProductAlias
			_ = json.Unmarshal([]byte(m.AliasRows), &rows)
			for _, a := range rows {
				if err := q.InsertAliasRow(ctx, db.InsertAliasRowParams{ID: a.ID, ShopID: a.ShopID, NormText: a.NormText, ProductID: a.ProductID, CreatedAt: a.CreatedAt}); err != nil {
					return err
				}
			}
		case "split":
			// from = the new product, into = the product the lines came from
			intoID := m.IntoProductID.Int64
			res.LinesRestored, res.LinesSkipped, err = moveLines(ctx, q, lineIDs, nid(m.FromProductID), nid(intoID))
			if err != nil {
				return err
			}
			for _, aid := range aliasIDs {
				if _, err := q.MoveAliasIfOwned(ctx, db.MoveAliasIfOwnedParams{ToProduct: intoID, ID: aid, Expected: m.FromProductID}); err != nil {
					return err
				}
			}
			if err := q.MarkProductDiscarded(ctx, db.MarkProductDiscardedParams{DeletedAt: store.Str(now), UpdatedAt: now, ID: m.FromProductID}); err != nil {
				return err
			}
			p, err := q.GetProduct(ctx, intoID)
			if err != nil {
				return err
			}
			res.Product, res.Into = p, p
			return q.MarkMergeUndone(ctx, db.MarkMergeUndoneParams{UndoneAt: store.Str(now), ID: mergeID})
		}
		if res.Product, err = q.GetProduct(ctx, m.FromProductID); err != nil {
			return err
		}
		if m.Kind == "discard" {
			res.Into = res.Product
		}
		return q.MarkMergeUndone(ctx, db.MarkMergeUndoneParams{UndoneAt: store.Str(now), ID: mergeID})
	})
	return res, err
}

func restoreProduct(ctx context.Context, q *db.Queries, id int64, rb reviewBefore, now string) error {
	arg := db.RestoreProductParams{NeedsReview: rb.NeedsReview, UpdatedAt: now, ID: id}
	if rb.Barcode != "" {
		arg.Barcode = store.Str(rb.Barcode)
	}
	if rb.Reason != "" {
		arg.ReviewReason = store.Str(rb.Reason)
	}
	if rb.Suggested != 0 {
		arg.SuggestedMergeID = nid(rb.Suggested)
	}
	return q.RestoreProduct(ctx, arg)
}

// moveLines sets product_id from `expected` to `to` for each line still pointing at expected.
func moveLines(ctx context.Context, q *db.Queries, ids []int64, expected, to sql.NullInt64) (moved, skipped int, err error) {
	for _, id := range ids {
		n, err := q.MoveLineIfLinked(ctx, db.MoveLineIfLinkedParams{ToProduct: to, ID: id, Expected: expected})
		if err != nil {
			return 0, 0, err
		}
		if n > 0 {
			moved++
		} else {
			skipped++
		}
	}
	return moved, skipped, nil
}

// clearFilled empties the fields a merge copied into `into`, if they still hold the copied value.
func (s *Service) clearFilled(ctx context.Context, q *db.Queries, intoID int64, f filled, now string) error {
	p, err := q.GetProductAny(ctx, intoID)
	if err != nil {
		return err
	}
	fill := db.FillProductFieldsParams{Barcode: p.Barcode, Brand: p.Brand, ImagePath: p.ImagePath, NetQuantity: p.NetQuantity,
		NetUnit: p.NetUnit, OffCode: p.OffCode, CategoryID: p.CategoryID, CategorySource: p.CategorySource, UpdatedAt: now, ID: intoID}
	if f.Barcode != "" && p.Barcode.String == f.Barcode {
		fill.Barcode = sql.NullString{}
	}
	if f.Brand != "" && p.Brand.String == f.Brand {
		fill.Brand = sql.NullString{}
	}
	if f.ImagePath != "" && p.ImagePath.String == f.ImagePath {
		fill.ImagePath = sql.NullString{}
	}
	if f.NetQuantity != nil && p.NetQuantity.Float64 == *f.NetQuantity {
		fill.NetQuantity, fill.NetUnit = sql.NullFloat64{}, sql.NullString{}
	}
	if f.OffCode != "" && p.OffCode.String == f.OffCode {
		fill.OffCode = sql.NullString{}
	}
	if f.CategoryID != 0 && p.CategoryID.Int64 == f.CategoryID {
		fill.CategoryID, fill.CategorySource = sql.NullInt64{}, sql.NullString{}
	}
	if err := q.FillProductFields(ctx, fill); err != nil {
		return err
	}
	if f.Nutrition {
		if err := q.DeleteNutrition(ctx, intoID); err != nil {
			return err
		}
	}
	if f.NameEn != "" && p.NameEn.String == f.NameEn {
		return q.SetProductEnglish(ctx, db.SetProductEnglishParams{NameEn: strOrNull(f.PrevNameEn), NameEnSource: strOrNull(f.PrevNameEnSrc), ID: intoID})
	}
	return nil
}

func strOrNull(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Split gives one receipt line its own product: a new manual product takes over this line, every line
// with the same shop and text that pointed at the old product, and the matching aliases. Reversible.
func (s *Service) Split(ctx context.Context, lineID int64, name string) (MergeResult, error) {
	var res MergeResult
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		line, err := q.GetLine(ctx, lineID)
		if err != nil {
			return notFound(err)
		}
		if line.Kind != "item" {
			return errf(ErrInvalid, "only item lines can be split into a product")
		}
		if !line.ProductID.Valid {
			return ErrNotFound
		}
		rec, err := q.GetReceipt(ctx, line.ReceiptID)
		if err != nil {
			return err
		}
		old := line.ProductID.Int64
		c := CleanLine(line.RawText)
		name = trimSpace(name)
		if name == "" {
			name = c.Name
		}
		if name == "" {
			name = trimSpace(line.RawText)
		}
		now := store.TS(s.now())
		np, err := s.insertProduct(ctx, q, newProduct{name: name, createdVia: "manual", noReview: true}, now)
		if err != nil {
			return err
		}
		ids, err := q.ListLinesForSplit(ctx, db.ListLinesForSplitParams{ProductID: nid(old), NormText: line.NormText, ShopID: rec.ShopID})
		if err != nil {
			return err
		}
		if !containsID(ids, lineID) {
			ids = append(ids, lineID)
		}
		for _, id := range ids {
			if _, err := q.MoveLineIfLinked(ctx, db.MoveLineIfLinkedParams{ToProduct: nid(np.ID), ID: id, Expected: nid(old)}); err != nil {
				return err
			}
		}
		aliases, err := q.ListAliasesForSplit(ctx, db.ListAliasesForSplitParams{ProductID: old, ShopID: rec.ShopID, NormA: line.NormText, NormB: c.Norm})
		if err != nil {
			return err
		}
		aliasIDs := make([]int64, 0, len(aliases))
		for _, a := range aliases {
			if _, err := q.MoveAliasIfOwned(ctx, db.MoveAliasIfOwnedParams{ToProduct: np.ID, ID: a.ID, Expected: old}); err != nil {
				return err
			}
			aliasIDs = append(aliasIDs, a.ID)
		}
		m, err := q.InsertMerge(ctx, db.InsertMergeParams{
			FromProductID: np.ID, IntoProductID: nid(old), LineIds: mustJSON(ids), AliasIds: mustJSON(aliasIDs), AliasRows: "[]",
			IntoFieldsBefore: "{}", FromReviewBefore: "{}", Kind: "split", CreatedAt: now,
		})
		if err != nil {
			return err
		}
		res = MergeResult{MergeID: m.ID, Kind: "split", Product: np, LinesMoved: len(ids), AliasesMoved: len(aliasIDs)}
		return nil
	})
	return res, err
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
