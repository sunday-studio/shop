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

const (
	SourceUser      = "user"
	SourceAssistant = "assistant"

	KindItem  = "item"
	KindGroup = "group"
)

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
	GroupID   *int64 // optional; the group (checklist or template) the item belongs to, nil = main list
	Source    string // "user" (default) or "assistant"
}

// Item is a list entry with the cheapest known shop for linked products.
type Item struct {
	db.ShoppingListItem
	ProductName string
	Cheapest    *stats.ShopPrice
	// OtherGroupIDs are the live checklists where the same product is also open (excluding this item);
	// OnMainList says it is also open on the main list. Both are computed when the item is read.
	OtherGroupIDs []int64
	OnMainList    bool
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
	switch in.Source {
	case "":
		in.Source = SourceUser
	case SourceUser, SourceAssistant:
	default:
		return fmt.Errorf("%w: source must be user or assistant", ErrInvalid)
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
		row, created, err = addItemTx(ctx, q, in, now)
		if err != nil {
			return err
		}
		if in.ClientID != "" {
			return q.PutClientID(ctx, db.PutClientIDParams{ClientID: in.ClientID, ItemID: row.ID, Kind: KindItem, CreatedAt: now})
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
	it, err := s.enrichWritten(ctx, row)
	return it, created, err
}

// addItemTx adds a resolved input to its group (or the main list). An open item with the same product in
// the same group gets its quantity bumped instead (one open item per product and group).
func addItemTx(ctx context.Context, q *db.Queries, in Input, now string) (db.ShoppingListItem, bool, error) {
	if in.GroupID != nil {
		if _, err := q.GetShoppingGroup(ctx, *in.GroupID); errors.Is(err, sql.ErrNoRows) {
			return db.ShoppingListItem{}, false, fmt.Errorf("%w: group %d", ErrNotFound, *in.GroupID)
		} else if err != nil {
			return db.ShoppingListItem{}, false, err
		}
	}
	var open db.ShoppingListItem
	var cur error = sql.ErrNoRows
	if in.ProductID != nil {
		open, cur = q.FindOpenShoppingItemByProduct(ctx, db.FindOpenShoppingItemByProductParams{
			ProductID: store.NullInt(in.ProductID), GroupID: store.NullInt(in.GroupID),
		})
	}
	var row db.ShoppingListItem
	var err error
	created := false
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
		src := in.Source
		if src == "" {
			src = SourceUser
		}
		row, err = q.CreateShoppingItem(ctx, db.CreateShoppingItemParams{
			ProductID: store.NullInt(in.ProductID), Text: in.Text, Qty: floatNull(in.Qty),
			Note: store.NullStr(in.Note), GroupID: store.NullInt(in.GroupID), Source: src,
			CreatedAt: now, UpdatedAt: store.Str(now),
		})
		created = true
	default:
		err = cur
	}
	if err != nil {
		return row, false, err
	}
	if in.GroupID != nil {
		if err := q.TouchShoppingGroup(ctx, db.TouchShoppingGroupParams{UpdatedAt: now, ID: *in.GroupID}); err != nil {
			return row, created, err
		}
	}
	return row, created, nil
}

// replay returns the item a client id already produced.
func (s *Service) replay(ctx context.Context, clientID string) (Item, bool, error) {
	ref, err := s.q.GetClientID(ctx, clientID)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	if ref.Kind != KindItem {
		return Item{}, true, fmt.Errorf("%w: client_id was used for a group", ErrConflict)
	}
	row, err := s.q.GetShoppingItem(ctx, ref.ItemID)
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
	return s.enrichWritten(ctx, row)
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
	return s.enrichWritten(ctx, row)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return store.InTx(ctx, s.conn, func(q *db.Queries) error {
		it, err := q.GetShoppingItem(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.stamp()
		if err := deleteItemTx(ctx, q, it, now); err != nil {
			return err
		}
		return touchGroup(ctx, q, it.GroupID, now)
	})
}

// deleteItemTx removes an item and leaves a tombstone.
func deleteItemTx(ctx context.Context, q *db.Queries, it db.ShoppingListItem, now string) error {
	if _, err := q.DeleteShoppingItem(ctx, it.ID); err != nil {
		return err
	}
	return q.RecordShoppingTombstone(ctx, db.RecordShoppingTombstoneParams{Kind: KindItem, ID: it.ID, DeletedAt: now})
}

func touchGroup(ctx context.Context, q *db.Queries, id sql.NullInt64, now string) error {
	if !id.Valid {
		return nil
	}
	return q.TouchShoppingGroup(ctx, db.TouchShoppingGroupParams{UpdatedAt: now, ID: id.Int64})
}

// ClearChecked removes the checked items of the main list. Checked items inside groups are the groups'
// progress and stay.
func (s *Service) ClearChecked(ctx context.Context) (int64, error) {
	var n int64
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		ids, err := q.CheckedShoppingItemIDs(ctx)
		if err != nil {
			return err
		}
		now := s.stamp()
		for _, id := range ids {
			if err := deleteItemTx(ctx, q, db.ShoppingListItem{ID: id}, now); err != nil {
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
	Items           []Item  // created, edited, checked or unchecked since (incl. checked items); no template items
	Groups          []Group // groups (any kind, archived too) created or edited since
	DeletedIDs      []int64 // items deleted since
	DeletedGroupIDs []int64 // groups deleted since
	ServerTime      time.Time
	Rev             int64 // the rev to send as since_rev next time
	// FullResync: since is older than the tombstone window. With the rev cursor, Items and Groups then hold
	// everything and the phone replaces its copy; with the time cursor the phone refetches GET /v1/list.
	FullResync bool
}

// Rev is the current value of the list's write counter.
func (s *Service) Rev(ctx context.Context) (int64, error) {
	r, err := s.q.GetListRev(ctx)
	return r.Rev, err
}

// pruneTombstones forgets old deletions and remembers how far it went, so a stale rev cursor is noticed.
func (s *Service) pruneTombstones(ctx context.Context, now time.Time) error {
	cut := store.TS(now.Add(-TombstoneTTL))
	return store.InTx(ctx, s.conn, func(q *db.Queries) error {
		top, err := q.MaxPrunableTombstoneRev(ctx, cut)
		if err != nil {
			return err
		}
		if top > 0 {
			if err := q.RaisePrunedRev(ctx, top); err != nil {
				return err
			}
		}
		if err := q.PruneShoppingTombstones(ctx, cut); err != nil {
			return err
		}
		return q.PruneGroupTrash(ctx, cut)
	})
}

func splitTombstones(kinds []string, ids []int64) (items, groups []int64) {
	items, groups = []int64{}, []int64{}
	for i, k := range kinds {
		if k == KindGroup {
			groups = append(groups, ids[i])
		} else {
			items = append(items, ids[i])
		}
	}
	return
}

// Changes returns items changed and deleted at or after since (the original, time based cursor). The
// boundary is inclusive (timestamps have second precision), so a client may see an item twice; applying it
// again is harmless. It also returns the groups and the current rev, so a client can move to ChangesSinceRev.
func (s *Service) Changes(ctx context.Context, since time.Time) (Changes, error) {
	now := s.now()
	out := Changes{ServerTime: now.UTC().Truncate(time.Second), DeletedIDs: []int64{}, DeletedGroupIDs: []int64{}}
	if err := s.pruneTombstones(ctx, now); err != nil {
		return out, err
	}
	rev, err := s.Rev(ctx)
	if err != nil {
		return out, err
	}
	out.Rev = rev
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
	grows, err := s.q.ShoppingGroupsChangedSince(ctx, store.TS(since))
	if err != nil {
		return out, err
	}
	if out.Groups, err = s.groupsOf(ctx, grows); err != nil {
		return out, err
	}
	ts, err := s.q.ShoppingTombstonesSince(ctx, store.TS(since))
	if err != nil {
		return out, err
	}
	kinds, ids := make([]string, len(ts)), make([]int64, len(ts))
	for i, t := range ts {
		kinds[i], ids[i] = t.Kind, t.ID
	}
	out.DeletedIDs, out.DeletedGroupIDs = splitTombstones(kinds, ids)
	return out, nil
}

// ChangesSinceRev is the rev based cursor: everything written after rev sinceRev. Send the returned Rev
// next time. sinceRev 0 (a new phone), or older than the tombstones still kept, returns the complete list
// with FullResync set: the phone replaces its copy instead of merging. Unlike the time cursor, nothing is
// repeated and nothing can be missed at a second boundary.
func (s *Service) ChangesSinceRev(ctx context.Context, sinceRev int64) (Changes, error) {
	now := s.now()
	out := Changes{ServerTime: now.UTC().Truncate(time.Second), DeletedIDs: []int64{}, DeletedGroupIDs: []int64{}}
	if err := s.pruneTombstones(ctx, now); err != nil {
		return out, err
	}
	if sinceRev < 0 {
		return out, fmt.Errorf("%w: since_rev must be 0 or more", ErrInvalid)
	}
	r, err := s.q.GetListRev(ctx)
	if err != nil {
		return out, err
	}
	out.Rev = r.Rev
	if sinceRev > r.Rev {
		return out, fmt.Errorf("%w: since_rev %d is ahead of the server (rev %d); start again from 0", ErrInvalid, sinceRev, r.Rev)
	}
	if sinceRev == 0 || sinceRev < r.PrunedRev {
		out.FullResync = true
		sinceRev = 0
	}
	rows, err := s.q.ShoppingItemsChangedSinceRev(ctx, sinceRev)
	if err != nil {
		return out, err
	}
	if out.Items, err = s.enrichAll(ctx, rows); err != nil {
		return out, err
	}
	grows, err := s.q.ShoppingGroupsChangedSinceRev(ctx, sinceRev)
	if err != nil {
		return out, err
	}
	if out.Groups, err = s.groupsOf(ctx, grows); err != nil {
		return out, err
	}
	if !out.FullResync {
		ts, err := s.q.ShoppingTombstonesSinceRev(ctx, sinceRev)
		if err != nil {
			return out, err
		}
		kinds, ids := make([]string, len(ts)), make([]int64, len(ts))
		for i, t := range ts {
			kinds[i], ids[i] = t.Kind, t.ID
		}
		out.DeletedIDs, out.DeletedGroupIDs = splitTombstones(kinds, ids)
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

// enrichWritten is enrich for a row that came back from an INSERT or UPDATE: those return the row before the
// rev trigger ran, so it is read again to carry its real rev.
func (s *Service) enrichWritten(ctx context.Context, r db.ShoppingListItem) (Item, error) {
	fresh, err := s.q.GetShoppingItem(ctx, r.ID)
	if err != nil {
		return Item{}, err
	}
	return s.enrich(ctx, fresh)
}

func (s *Service) enrich(ctx context.Context, r db.ShoppingListItem) (Item, error) {
	it := Item{ShoppingListItem: r}
	if !r.ProductID.Valid {
		return it, nil
	}
	others, err := s.q.OpenGroupsOfProduct(ctx, db.OpenGroupsOfProductParams{ProductID: r.ProductID, ID: r.ID})
	if err != nil {
		return it, err
	}
	for _, g := range others {
		if g.Valid {
			it.OtherGroupIDs = append(it.OtherGroupIDs, g.Int64)
		} else {
			it.OnMainList = true
		}
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
