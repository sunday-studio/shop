// Package shopping is the shopping list for the next trip. It is built for
// an offline-first app: adds are idempotent via client ids, every write
// bumps updated_at, and deletions leave tombstones so clients can sync
// changes since a point in time.
package shopping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"shop/internal/match"
	"shop/internal/stats"
	"shop/internal/store"
	"shop/internal/store/db"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
	// ErrConflict: the request contradicts an earlier decision (e.g. accepting a dismissed suggestion).
	ErrConflict = errors.New("conflict")
	// ErrGone: a retried add whose item has since been deleted.
	ErrGone = errors.New("item was deleted")
)

// autoLinkScore is how close free text must be to a product name to be
// linked automatically (e.g. "oat milk" typed by the assistant).
const autoLinkScore = 0.9

// TombstoneTTL is how long deletions are remembered for sync. Clients that
// last synced longer ago must refetch the whole list.
const TombstoneTTL = 30 * 24 * time.Hour

type Service struct {
	conn    *sql.DB
	q       *db.Queries
	matcher *match.Matcher
	stats   *stats.Service
	now     func() time.Time
	loc     *time.Location
}

func New(conn *sql.DB, matcher *match.Matcher, st *stats.Service) *Service {
	return &Service{conn: conn, q: db.New(conn), matcher: matcher, stats: st, now: time.Now, loc: time.Local}
}

type Input struct {
	Text      string
	ProductID *int64
	Qty       *float64
	Note      *string
	ClientID  string // optional; makes Add idempotent
}

// Item is a list entry with the cheapest known shop for linked products.
type Item struct {
	db.ShoppingListItem
	ProductName string
	Cheapest    *stats.ShopPrice
}

func (s *Service) resolve(ctx context.Context, in *Input) error {
	in.Text = strings.TrimSpace(in.Text)
	in.ClientID = strings.TrimSpace(in.ClientID)
	if len(in.ClientID) > 100 {
		return fmt.Errorf("%w: client_id is too long", ErrInvalid)
	}
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

func (s *Service) stamp() string { return store.TS(s.now()) }

// Add puts an item on the list. Adding a product that is already open on
// the list bumps its quantity instead of duplicating it. With a client id,
// a repeat of the same request returns the original result unchanged
// (created=false), so offline retries never double the quantity.
func (s *Service) Add(ctx context.Context, in Input) (Item, bool, error) {
	if in.ClientID != "" {
		if it, ok, err := s.replay(ctx, in.ClientID); err != nil || ok {
			return it, false, err
		}
	}
	if err := s.resolve(ctx, &in); err != nil {
		return Item{}, false, err
	}
	var row db.ShoppingListItem
	created := false
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := s.stamp()
		var err error
		cur, ferr := sql.ErrNoRows, error(nil)
		var open db.ShoppingListItem
		if in.ProductID != nil {
			open, ferr = q.FindOpenShoppingItemByProduct(ctx, store.NullInt(in.ProductID))
			cur = ferr
		}
		switch {
		case in.ProductID != nil && cur == nil:
			qty := 1.0
			if open.Qty.Valid {
				qty = open.Qty.Float64
			}
			add := 1.0
			if in.Qty != nil {
				add = *in.Qty
			}
			note := open.Note
			if in.Note != nil {
				note = store.NullStr(in.Note)
			}
			row, err = q.UpdateShoppingItem(ctx, db.UpdateShoppingItemParams{
				ProductID: open.ProductID, Text: open.Text, Qty: sql.NullFloat64{Float64: qty + add, Valid: true},
				Note: note, UpdatedAt: store.Str(now), ID: open.ID,
			})
		case errors.Is(cur, sql.ErrNoRows):
			row, err = q.CreateShoppingItem(ctx, db.CreateShoppingItemParams{
				ProductID: store.NullInt(in.ProductID), Text: in.Text, Qty: floatNull(in.Qty),
				Note: store.NullStr(in.Note), CreatedAt: now, UpdatedAt: store.Str(now),
			})
			created = true
		default:
			err = cur
		}
		if err != nil {
			return err
		}
		if in.ClientID != "" {
			return q.PutClientID(ctx, db.PutClientIDParams{ClientID: in.ClientID, ItemID: row.ID, CreatedAt: now})
		}
		return nil
	})
	if err != nil {
		// Two copies of the same retry racing: the loser replays the winner.
		if in.ClientID != "" && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			it, _, rerr := s.replay(ctx, in.ClientID)
			return it, false, rerr
		}
		return Item{}, false, err
	}
	it, err := s.enrich(ctx, row)
	return it, created, err
}

// replay returns the item a client id already produced.
func (s *Service) replay(ctx context.Context, clientID string) (Item, bool, error) {
	id, err := s.q.GetClientID(ctx, clientID)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	row, err := s.q.GetShoppingItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, true, ErrGone
	}
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
		ProductID: store.NullInt(in.ProductID), Text: in.Text, Qty: floatNull(in.Qty), Note: store.NullStr(in.Note),
		UpdatedAt: store.Str(s.stamp()), ID: id,
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
	now := s.stamp()
	var at sql.NullString
	if checked {
		at = store.Str(now)
	}
	row, err := s.q.SetShoppingItemChecked(ctx, db.SetShoppingItemCheckedParams{CheckedAt: at, UpdatedAt: store.Str(now), ID: id})
	if err != nil {
		return Item{}, err
	}
	return s.enrich(ctx, row)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return store.InTx(ctx, s.conn, func(q *db.Queries) error {
		n, err := q.DeleteShoppingItem(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return q.RecordShoppingDeletion(ctx, db.RecordShoppingDeletionParams{ItemID: id, DeletedAt: s.stamp()})
	})
}

func (s *Service) ClearChecked(ctx context.Context) (int64, error) {
	var n int64
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		ids, err := q.CheckedShoppingItemIDs(ctx)
		if err != nil {
			return err
		}
		now := s.stamp()
		for _, id := range ids {
			if _, err := q.DeleteShoppingItem(ctx, id); err != nil {
				return err
			}
			if err := q.RecordShoppingDeletion(ctx, db.RecordShoppingDeletionParams{ItemID: id, DeletedAt: now}); err != nil {
				return err
			}
		}
		n = int64(len(ids))
		return nil
	})
	return n, err
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
	return s.enrichAll(ctx, rows)
}

// Changes is what changed since a sync point.
type Changes struct {
	Items      []Item  // created, edited, checked or unchecked since (incl. checked items)
	DeletedIDs []int64 // deleted since
	ServerTime time.Time
	FullResync bool // since is older than the tombstone window: refetch everything
}

// Changes returns items changed and deleted at or after since. The
// boundary is inclusive (timestamps have second precision), so a client
// may see an item twice; applying it again is harmless.
func (s *Service) Changes(ctx context.Context, since time.Time) (Changes, error) {
	now := s.now()
	out := Changes{ServerTime: now.UTC().Truncate(time.Second), DeletedIDs: []int64{}}
	if err := s.q.PruneShoppingDeletions(ctx, store.TS(now.Add(-TombstoneTTL))); err != nil {
		return out, err
	}
	if since.Before(now.Add(-TombstoneTTL)) {
		out.FullResync = true
	}
	rows, err := s.q.ShoppingItemsChangedSince(ctx, store.Str(store.TS(since)))
	if err != nil {
		return out, err
	}
	if out.Items, err = s.enrichAll(ctx, rows); err != nil {
		return out, err
	}
	if out.DeletedIDs, err = s.q.ShoppingDeletionsSince(ctx, store.TS(since)); err != nil {
		return out, err
	}
	if out.DeletedIDs == nil {
		out.DeletedIDs = []int64{}
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

func (s *Service) enrichAll(ctx context.Context, rows []db.ShoppingListItem) ([]Item, error) {
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
