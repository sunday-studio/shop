package shopping

// Phase A: confirming a receipt checks off what was planned.
//
// Rules (notes/shopping-list-plan.md section 1 and 2):
//   - only confirmed receipts, dated within RecentDays of the confirmation, can touch the list;
//   - an open item whose linked product is on the receipt is checked;
//   - a free-text item whose best line scores >= CheckScore is checked, >= SuggestScore is a suggestion;
//   - a different product of the same kind as a linked item is a suggestion (substitute), never a check;
//   - a partial quantity keeps the item open with a reduced qty and the note "1 of 3 bought";
//   - each (receipt, item) pair is handled once, so a repeated confirm changes nothing, and an undo
//     is remembered so the same receipt never re-checks the item.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"shop/internal/match"
	"shop/internal/store"
	"shop/internal/store/db"
)

const (
	// CheckScore is the fuzzy score from which free text is checked automatically.
	CheckScore = 0.90
	// SuggestScore is the fuzzy score from which a match is offered as a suggestion.
	SuggestScore = match.SuggestThreshold
	// RecentDays is how many days before its confirmation a receipt may be dated and still tick the list.
	RecentDays = 3
)

const (
	ReasonMatch      = "match"
	ReasonSubstitute = "substitute"
)

// Suggestion is a "Looks bought?" row: an open item and the receipt line that may be it.
type Suggestion struct {
	db.ShoppingListSuggestion
	ItemText string
	LineText string // the receipt line as printed
}

// ReceiptResult is what reconciling one receipt did.
type ReceiptResult struct {
	Checked     []Item       // items checked off
	Reduced     []Item       // items kept open with a smaller qty
	Suggestions []Suggestion // new suggestions (open)
}

func (r ReceiptResult) Empty() bool {
	return len(r.Checked) == 0 && len(r.Reduced) == 0 && len(r.Suggestions) == 0
}

// WithLocation sets the time zone used to decide whether a receipt date is recent.
func (s *Service) WithLocation(loc *time.Location) *Service {
	if loc != nil {
		s.loc = loc
	}
	return s
}

// eligible says whether the receipt may touch the list now.
func (s *Service) eligible(rec db.Receipt) bool {
	if rec.Status != "ok" || !rec.ConfirmedAt.Valid || !rec.PurchasedAt.Valid {
		return false
	}
	conf, err := store.ParseTS(rec.ConfirmedAt.String)
	if err != nil {
		return false
	}
	bought, err := store.ParseTS(rec.PurchasedAt.String)
	if err != nil {
		return false
	}
	days := dayNumber(conf.In(s.loc)) - dayNumber(bought.In(s.loc))
	return days >= -1 && days <= RecentDays // a day of slack ahead for time zones, never a future date
}

func dayNumber(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Unix() / 86400)
}

// rline is a receipt line prepared for matching.
type rline struct {
	db.ListLinesOfReceiptRow
	texts []string // normalised: printed text, product name, English name
}

type decision struct {
	item  db.ShoppingListItem
	line  rline
	bound float64 // quantity this receipt covers
	score float64
	check bool // else a suggestion
	why   string
}

// ReconcileReceipt checks off or suggests list items for a confirmed receipt. It is safe to call
// again for the same receipt: pairs it already handled are skipped.
func (s *Service) ReconcileReceipt(ctx context.Context, receiptID int64) (ReceiptResult, error) {
	var out ReceiptResult
	rec, err := s.q.GetReceipt(ctx, receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !s.eligible(rec) {
		return out, nil
	}
	open, err := s.q.ListReconcilableItems(ctx)
	if err != nil || len(open) == 0 {
		return out, err
	}
	rows, err := s.q.ListLinesOfReceipt(ctx, receiptID)
	if err != nil || len(rows) == 0 {
		return out, err
	}
	lines := make([]rline, len(rows))
	for i, r := range rows {
		lines[i] = rline{ListLinesOfReceiptRow: r}
		for _, t := range []string{r.RawText, r.ProductName, r.ProductNameEn} {
			if n := match.Normalise(t); n != "" {
				lines[i].texts = append(lines[i].texts, n)
			}
		}
	}

	cats := map[int64]sql.NullInt64{}
	for _, it := range open {
		if it.ProductID.Valid {
			if p, err := s.q.GetProduct(ctx, it.ProductID.Int64); err == nil {
				cats[it.ProductID.Int64] = p.CategoryID
			}
		}
	}
	decisions := s.plan(open, lines, cats)
	if len(decisions) == 0 {
		return out, nil
	}

	var checkedIDs, reducedIDs, sugIDs []int64
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := s.stamp()
		for _, d := range decisions {
			cur, err := q.GetShoppingItem(ctx, d.item.ID)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && cur.CheckedAt.Valid) {
				continue // deleted or checked meanwhile
			}
			if err != nil {
				return err
			}
			if !d.check {
				n, err := q.InsertListSuggestion(ctx, db.InsertListSuggestionParams{
					ItemID: cur.ID, ReceiptID: receiptID, LineID: store.NullInt(&d.line.ID),
					Score: d.score, Reason: d.why, CreatedAt: now,
				})
				if err != nil {
					return err
				}
				if n > 0 {
					sugIDs = append(sugIDs, cur.ID)
				}
				continue
			}
			kind, err := applyBought(ctx, q, cur, receiptID, d.line.ID, d.bound, now)
			if err != nil {
				return err
			}
			switch kind {
			case "checked":
				checkedIDs = append(checkedIDs, cur.ID)
			case "reduced":
				reducedIDs = append(reducedIDs, cur.ID)
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if out.Checked, err = s.itemsByID(ctx, checkedIDs); err != nil {
		return out, err
	}
	if out.Reduced, err = s.itemsByID(ctx, reducedIDs); err != nil {
		return out, err
	}
	sugs, err := s.q.ListReceiptSuggestions(ctx, receiptID)
	if err != nil {
		return out, err
	}
	fresh := map[int64]bool{}
	for _, id := range sugIDs {
		fresh[id] = true
	}
	for _, sg := range sugs {
		if fresh[sg.ItemID] {
			out.Suggestions = append(out.Suggestions, s.suggestion(ctx, sg))
		}
	}
	return out, nil
}

// applyBought records what a receipt did to the item and updates it: a full check, or a smaller qty.
// It returns "" when this (receipt, item) pair was already handled.
func applyBought(ctx context.Context, q *db.Queries, it db.ShoppingListItem, receiptID, lineID int64, bought float64, now string) (string, error) {
	need := 1.0
	if it.Qty.Valid {
		need = it.Qty.Float64
	}
	if bought > 0 && bought < need {
		note := fmt.Sprintf("%s of %s bought", fmtQty(bought), fmtQty(need))
		if it.Note.Valid && it.Note.String != "" {
			note = it.Note.String + " · " + note
		}
		n, err := q.InsertReceiptCheck(ctx, db.InsertReceiptCheckParams{
			ReceiptID: receiptID, ItemID: it.ID, Kind: "reduced", LineID: store.NullInt(&lineID),
			BoughtQty: sql.NullFloat64{Float64: bought, Valid: true}, PrevQty: it.Qty, PrevNote: it.Note,
			SetNote: store.Str(note), CreatedAt: now,
		})
		if err != nil || n == 0 {
			return "", err
		}
		_, err = q.SetShoppingItemQtyNote(ctx, db.SetShoppingItemQtyNoteParams{
			Qty: sql.NullFloat64{Float64: need - bought, Valid: true}, Note: store.Str(note), UpdatedAt: store.Str(now), ID: it.ID,
		})
		return "reduced", err
	}
	n, err := q.InsertReceiptCheck(ctx, db.InsertReceiptCheckParams{
		ReceiptID: receiptID, ItemID: it.ID, Kind: "checked", LineID: store.NullInt(&lineID),
		BoughtQty: sql.NullFloat64{Float64: bought, Valid: true}, CreatedAt: now,
	})
	if err != nil || n == 0 {
		return "", err
	}
	_, err = q.CheckShoppingItemFromReceipt(ctx, db.CheckShoppingItemFromReceiptParams{
		CheckedAt: store.Str(now), UpdatedAt: store.Str(now), CheckedReceiptID: store.NullInt(&receiptID),
		CheckedLineID: store.NullInt(&lineID), ID: it.ID,
	})
	return "checked", err
}

func fmtQty(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// plan decides, without writing, what each open item gets from the receipt.
func (s *Service) plan(open []db.ShoppingListItem, lines []rline, cats map[int64]sql.NullInt64) []decision {
	var out []decision
	usedLine := map[int64]bool{}
	handled := map[int64]bool{}

	// 1. Items linked to a product that is on the receipt.
	byProduct := map[int64][]rline{}
	for _, l := range lines {
		if l.ProductID.Valid {
			byProduct[l.ProductID.Int64] = append(byProduct[l.ProductID.Int64], l)
		}
	}
	// Quantity of each product the receipt holds. One purchase checks the product everywhere it is open
	// (main list and every checklist), so each item is measured against the whole purchase.
	remaining := map[int64]float64{}
	for pid, ls := range byProduct {
		for _, l := range ls {
			remaining[pid] += lineQty(l)
		}
	}
	for _, it := range open {
		if !it.ProductID.Valid {
			continue
		}
		ls := byProduct[it.ProductID.Int64]
		if len(ls) == 0 {
			continue
		}
		have := remaining[it.ProductID.Int64]
		if have <= 0 {
			continue
		}
		need := 1.0
		if it.Qty.Valid {
			need = it.Qty.Float64
		}
		got := min(have, need)
		if ls[0].Unit != "pc" {
			got = need // weighed or by volume: any purchase covers the item
		}
		for _, l := range ls {
			usedLine[l.ID] = true
		}
		handled[it.ID] = true
		out = append(out, decision{item: it, line: ls[0], bound: got, score: 1, check: true, why: ReasonMatch})
	}

	// 2. Everything else against the lines no linked item claimed.
	type pair struct {
		item  db.ShoppingListItem
		line  rline
		score float64
		why   string
	}
	var pairs []pair
	for _, it := range open {
		if handled[it.ID] {
			continue
		}
		itemNorm := []string{match.Normalise(it.Text)}
		for _, l := range lines {
			if usedLine[l.ID] {
				continue
			}
			best := 0.0
			for _, a := range itemNorm {
				for _, b := range l.texts {
					best = max(best, match.Score(a, b))
				}
			}
			if best < SuggestScore {
				continue
			}
			p := pair{item: it, line: l, score: best, why: ReasonMatch}
			if it.ProductID.Valid {
				if !l.ProductID.Valid || l.ProductID.Int64 == it.ProductID.Int64 {
					continue
				}
				// A linked item whose product was not bought: only a similar product of the same
				// category is offered, and only as a suggestion.
				ic, lc := cats[it.ProductID.Int64], l.ProductCategoryID
				if !ic.Valid || !lc.Valid || ic.Int64 != lc.Int64 {
					continue
				}
				p.why = ReasonSubstitute
			}
			pairs = append(pairs, p)
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })
	itemDone := map[int64]bool{}
	for _, p := range pairs {
		if itemDone[p.item.ID] || usedLine[p.line.ID] {
			continue
		}
		itemDone[p.item.ID] = true
		usedLine[p.line.ID] = true
		d := decision{item: p.item, line: p.line, score: p.score, why: p.why}
		if p.why == ReasonMatch && p.score >= CheckScore {
			d.check = true
			need := 1.0
			if p.item.Qty.Valid {
				need = p.item.Qty.Float64
			}
			d.bound = lineQty(p.line)
			if p.line.Unit != "pc" || d.bound > need {
				d.bound = need
			}
		}
		out = append(out, d)
	}

	// 3. The same free text open in another group is the same purchase: it gets the same decision.
	twinKey := func(it db.ShoppingListItem) string {
		return strconv.FormatInt(it.ProductID.Int64, 10) + "|" + match.Normalise(it.Text)
	}
	decided := map[int64]bool{}
	byKey := map[string]decision{}
	for _, d := range out {
		decided[d.item.ID] = true
		if !d.item.ProductID.Valid {
			byKey[twinKey(d.item)] = d
		}
	}
	for _, it := range open {
		if decided[it.ID] || it.ProductID.Valid {
			continue
		}
		if d, ok := byKey[twinKey(it)]; ok {
			d.item = it
			if d.check {
				d.bound = min(d.bound, itemNeed(it))
			}
			out = append(out, d)
		}
	}
	return out
}

func itemNeed(it db.ShoppingListItem) float64 {
	if it.Qty.Valid {
		return it.Qty.Float64
	}
	return 1
}

func lineQty(l rline) float64 {
	if l.Qty <= 0 {
		return 1
	}
	return l.Qty
}

func (s *Service) itemsByID(ctx context.Context, ids []int64) ([]Item, error) {
	out := []Item{}
	for _, id := range ids {
		r, err := s.q.GetShoppingItem(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		it, err := s.enrich(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

func (s *Service) suggestion(ctx context.Context, sg db.ShoppingListSuggestion) Suggestion {
	out := Suggestion{ShoppingListSuggestion: sg}
	if it, err := s.q.GetShoppingItem(ctx, sg.ItemID); err == nil {
		out.ItemText = it.Text
	}
	if sg.LineID.Valid {
		if l, err := s.q.GetLine(ctx, sg.LineID.Int64); err == nil {
			out.LineText = l.RawText
		}
	}
	return out
}

// Suggestions lists the open "Looks bought?" rows.
func (s *Service) Suggestions(ctx context.Context) ([]Suggestion, error) {
	rows, err := s.q.ListOpenSuggestions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Suggestion, len(rows))
	for i, r := range rows {
		out[i] = s.suggestion(ctx, r)
	}
	return out, nil
}

// AcceptSuggestion checks the item off as bought on that receipt. Accepting twice is harmless; accepting
// a dismissed suggestion is ErrConflict.
func (s *Service) AcceptSuggestion(ctx context.Context, id int64) (Item, error) {
	sg, err := s.getSuggestion(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if sg.State == "dismissed" {
		return Item{}, fmt.Errorf("%w: suggestion was dismissed", ErrConflict)
	}
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := s.stamp()
		if sg.State == "open" {
			if err := q.ResolveListSuggestion(ctx, db.ResolveListSuggestionParams{State: "accepted", ResolvedAt: store.Str(now), ID: id}); err != nil {
				return err
			}
		}
		it, err := q.GetShoppingItem(ctx, sg.ItemID)
		if err != nil || it.CheckedAt.Valid || sg.State != "open" {
			return err
		}
		n, err := q.InsertReceiptCheck(ctx, db.InsertReceiptCheckParams{
			ReceiptID: sg.ReceiptID, ItemID: it.ID, Kind: "checked", LineID: sg.LineID, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		_ = n // n == 0: this receipt already handled the item once; the user's accept still wins
		_, err = q.CheckShoppingItemFromReceipt(ctx, db.CheckShoppingItemFromReceiptParams{
			CheckedAt: store.Str(now), UpdatedAt: store.Str(now), CheckedReceiptID: store.NullInt(&sg.ReceiptID),
			CheckedLineID: sg.LineID, ID: it.ID,
		})
		return err
	})
	if err != nil {
		return Item{}, err
	}
	row, err := s.get(ctx, sg.ItemID)
	if err != nil {
		return Item{}, err
	}
	return s.enrich(ctx, row)
}

// DismissSuggestion hides a suggestion for good (the same receipt never suggests the item again).
func (s *Service) DismissSuggestion(ctx context.Context, id int64) error {
	sg, err := s.getSuggestion(ctx, id)
	if err != nil {
		return err
	}
	if sg.State == "accepted" {
		return fmt.Errorf("%w: suggestion was accepted", ErrConflict)
	}
	if sg.State == "dismissed" {
		return nil
	}
	return s.q.ResolveListSuggestion(ctx, db.ResolveListSuggestionParams{State: "dismissed", ResolvedAt: store.Str(s.stamp()), ID: id})
}

func (s *Service) getSuggestion(ctx context.Context, id int64) (db.ShoppingListSuggestion, error) {
	sg, err := s.q.GetListSuggestion(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return sg, ErrNotFound
	}
	return sg, err
}

// UndoReceipt puts back every item the receipt checked or reduced and returns them. Items the user has
// changed since (unchecked, re-checked by hand, deleted) are left alone. The receipt will not touch those
// items again. Undoing twice returns nothing the second time.
func (s *Service) UndoReceipt(ctx context.Context, receiptID int64) ([]Item, error) {
	if _, err := s.q.GetReceipt(ctx, receiptID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return s.revert(ctx, receiptID, false)
}

// RevertReceipt is UndoReceipt for a receipt that is being deleted or unconfirmed: it also forgets
// what it did, so a later confirm of the (re-parsed) receipt starts clean.
func (s *Service) RevertReceipt(ctx context.Context, receiptID int64) ([]Item, error) {
	return s.revert(ctx, receiptID, true)
}

func (s *Service) revert(ctx context.Context, receiptID int64, purge bool) ([]Item, error) {
	var ids []int64
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := s.stamp()
		checks, err := q.ActiveReceiptChecks(ctx, receiptID)
		if err != nil {
			return err
		}
		for _, c := range checks {
			it, err := q.GetShoppingItem(ctx, c.ItemID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			switch c.Kind {
			case "checked":
				if it.CheckedAt.Valid && it.CheckSource == "receipt" && it.CheckedReceiptID.Int64 == receiptID {
					if _, err := q.SetShoppingItemChecked(ctx, db.SetShoppingItemCheckedParams{UpdatedAt: store.Str(now), ID: it.ID}); err != nil {
						return err
					}
					ids = append(ids, it.ID)
				}
			case "reduced":
				if it.CheckedAt.Valid {
					continue
				}
				qty := it.Qty
				if c.BoughtQty.Valid {
					qty = sql.NullFloat64{Float64: it.Qty.Float64 + c.BoughtQty.Float64, Valid: true}
					if !it.Qty.Valid {
						qty = c.PrevQty
					}
				}
				note := it.Note
				if note == c.SetNote {
					note = c.PrevNote
				}
				if _, err := q.SetShoppingItemQtyNote(ctx, db.SetShoppingItemQtyNoteParams{Qty: qty, Note: note, UpdatedAt: store.Str(now), ID: it.ID}); err != nil {
					return err
				}
				ids = append(ids, it.ID)
			}
			if err := q.MarkReceiptCheckUndone(ctx, db.MarkReceiptCheckUndoneParams{UndoneAt: store.Str(now), ID: c.ID}); err != nil {
				return err
			}
		}
		if purge {
			if err := q.PurgeReceiptChecks(ctx, receiptID); err != nil {
				return err
			}
			return q.PurgeReceiptSuggestions(ctx, receiptID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.itemsByID(ctx, ids)
}
