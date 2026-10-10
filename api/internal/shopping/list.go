// Package shopping is the shopping list for the next trip.
package shopping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"pantry/internal/match"
	"pantry/internal/stats"
	"pantry/internal/store"
	"pantry/internal/store/db"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
)

// autoLinkScore is how close free text must be to a product name to be
// linked automatically (e.g. "oat milk" typed by the assistant).
const autoLinkScore = 0.9

type Service struct {
	q       *db.Queries
	matcher *match.Matcher
	stats   *stats.Service
}

func New(conn *sql.DB, matcher *match.Matcher, st *stats.Service) *Service {
	return &Service{q: db.New(conn), matcher: matcher, stats: st}
}

type Input struct {
	Text      string
	ProductID *int64
	Qty       *float64
	Note      *string
}

// Item is a list entry with the cheapest known shop for linked products.
type Item struct {
	db.ShoppingListItem
	ProductName string
	Cheapest    *stats.ShopPrice
}

func (s *Service) resolve(ctx context.Context, in *Input) error {
	in.Text = strings.TrimSpace(in.Text)
	if in.ProductID != nil {
		p, err := s.q.GetProduct(ctx, *in.ProductID)
		if err != nil {
			return fmt.Errorf("%w: product %d does not exist", ErrInvalid, *in.ProductID)
		}
		if in.Text == "" {
			in.Text = p.Name
		}
	}
	if in.Text == "" {
		return fmt.Errorf("%w: text or product_id is required", ErrInvalid)
	}
	if in.Qty != nil && *in.Qty <= 0 {
		return fmt.Errorf("%w: qty must be positive", ErrInvalid)
	}
	if in.ProductID == nil {
		cands, err := s.matcher.Search(ctx, in.Text, 0, 1, autoLinkScore)
		if err != nil {
			return err
		}
		if len(cands) == 1 {
			id := cands[0].ProductID
			in.ProductID = &id
		}
	}
	return nil
}

func floatNull(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

// Add puts an item on the list. Adding a product that is already open on
// the list bumps its quantity instead of duplicating it.
func (s *Service) Add(ctx context.Context, in Input) (Item, bool, error) {
	if err := s.resolve(ctx, &in); err != nil {
		return Item{}, false, err
	}
	if in.ProductID != nil {
		if cur, err := s.q.FindOpenShoppingItemByProduct(ctx, store.NullInt(in.ProductID)); err == nil {
			qty := 1.0
			if cur.Qty.Valid {
				qty = cur.Qty.Float64
			}
			add := 1.0
			if in.Qty != nil {
				add = *in.Qty
			}
			qty += add
			note := cur.Note
			if in.Note != nil {
				note = store.NullStr(in.Note)
			}
			row, err := s.q.UpdateShoppingItem(ctx, db.UpdateShoppingItemParams{
				ProductID: cur.ProductID, Text: cur.Text, Qty: sql.NullFloat64{Float64: qty, Valid: true}, Note: note, ID: cur.ID,
			})
			if err != nil {
				return Item{}, false, err
			}
			it, err := s.enrich(ctx, row)
			return it, false, err
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Item{}, false, err
		}
	}
	row, err := s.q.CreateShoppingItem(ctx, db.CreateShoppingItemParams{
		ProductID: store.NullInt(in.ProductID), Text: in.Text, Qty: floatNull(in.Qty),
		Note: store.NullStr(in.Note), CreatedAt: store.Now(),
	})
	if err != nil {
		return Item{}, false, err
	}
	it, err := s.enrich(ctx, row)
	return it, true, err
}

func (s *Service) Update(ctx context.Context, id int64, in Input) (Item, error) {
	if _, err := s.get(ctx, id); err != nil {
		return Item{}, err
	}
	if err := s.resolve(ctx, &in); err != nil {
		return Item{}, err
	}
	row, err := s.q.UpdateShoppingItem(ctx, db.UpdateShoppingItemParams{
		ProductID: store.NullInt(in.ProductID), Text: in.Text, Qty: floatNull(in.Qty), Note: store.NullStr(in.Note), ID: id,
	})
	if err != nil {
		return Item{}, err
	}
	return s.enrich(ctx, row)
}

func (s *Service) SetChecked(ctx context.Context, id int64, checked bool) (Item, error) {
	if _, err := s.get(ctx, id); err != nil {
		return Item{}, err
	}
	var at sql.NullString
	if checked {
		at = store.Str(store.Now())
	}
	row, err := s.q.SetShoppingItemChecked(ctx, db.SetShoppingItemCheckedParams{CheckedAt: at, ID: id})
	if err != nil {
		return Item{}, err
	}
	return s.enrich(ctx, row)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteShoppingItem(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ClearChecked(ctx context.Context) (int64, error) {
	return s.q.ClearCheckedShoppingItems(ctx)
}

// List returns open items (and checked ones when includeChecked).
func (s *Service) List(ctx context.Context, includeChecked bool) ([]Item, error) {
	var inc any = 0
	if includeChecked {
		inc = 1
	}
	rows, err := s.q.ListShoppingItems(ctx, inc)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, r := range rows {
		it, err := s.enrich(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

func (s *Service) get(ctx context.Context, id int64) (db.ShoppingListItem, error) {
	it, err := s.q.GetShoppingItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

func (s *Service) enrich(ctx context.Context, r db.ShoppingListItem) (Item, error) {
	it := Item{ShoppingListItem: r}
	if !r.ProductID.Valid {
		return it, nil
	}
	if p, err := s.q.GetProduct(ctx, r.ProductID.Int64); err == nil {
		it.ProductName = p.Name
	}
	cmp, err := s.stats.Compare(ctx, r.ProductID.Int64)
	if err != nil {
		return it, err
	}
	if len(cmp) > 0 {
		c := cmp[0]
		it.Cheapest = &c
	}
	return it, nil
}
