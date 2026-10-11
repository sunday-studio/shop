package shopping

// Phase B: groups inside the one list.
//
// A group is a named checklist ("Jerk chicken", "BBQ") or a template (a saved checklist that can be
// started again). Items belong to the main list (no group) or to one group. Rules:
//   - one open item per (product, group); the same product may be open in several groups at once;
//   - template items are blueprints: they are not part of GET /v1/list, the changes feed, or receipt checks;
//   - archived groups keep their items but receipts no longer touch them;
//   - deleting a group either moves its items to the main list (keep) or deletes them; it can be undone
//     for RestoreWindow;
//   - every create is idempotent: by client_id (offline retries) or, for an assistant, by external_ref.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"shop/internal/store"
	"shop/internal/store/db"
)

const (
	KindChecklist = "checklist"
	KindTemplate  = "template"

	// MaxGroupItems is how many items one create or add call may carry.
	MaxGroupItems = 200
	maxNameRunes  = 100
	// RestoreWindow is how long a deleted group can still be restored.
	RestoreWindow = 10 * time.Minute
)

// Group is a checklist or template with counts. Items is filled when the group is loaded whole.
type Group struct {
	db.ShoppingListGroup
	ItemCount    int64
	CheckedCount int64
	Items        []Item
}

// GroupInput creates a group with its items in one step.
type GroupInput struct {
	ClientID    string // makes the create idempotent (offline retries)
	Name        string
	Note        *string
	Kind        string // checklist (default) or template
	Source      string // user (default) or assistant
	ExternalRef string // idempotency key for assistants, unique per source
	Sort        *int64
	Items       []Input // in order; ClientID and GroupID of the lines are ignored
}

// GroupResult is what a create-like call produced. ItemIDs lines up with the request's items (two lines for
// the same product share an id); on a repeat of the request it is rebuilt from the first answer.
type GroupResult struct {
	Group   Group
	ItemIDs []int64
	Created bool
}

// GroupPatch changes a group; nil fields stay.
type GroupPatch struct {
	Name     *string
	Note     *string // "" clears
	Sort     *int64
	Archived *bool
}

// DeleteResult describes a deleted group.
type DeleteResult struct {
	Kept           []Item  // items moved to the main list (keep)
	DeletedItemIDs []int64 // items deleted with the group
	RestoreUntil   time.Time
}

func (s *Service) groupsOf(ctx context.Context, rows []db.ShoppingListGroup) ([]Group, error) {
	counts, err := s.q.ShoppingGroupCounts(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int64]db.ShoppingGroupCountsRow{}
	for _, c := range counts {
		byID[c.GroupID.Int64] = c
	}
	out := make([]Group, len(rows))
	for i, r := range rows {
		out[i] = Group{ShoppingListGroup: r, ItemCount: byID[r.ID].ItemCount, CheckedCount: byID[r.ID].CheckedCount}
	}
	return out, nil
}

func (s *Service) loadGroup(ctx context.Context, id int64, withItems bool) (Group, error) {
	row, err := s.q.GetShoppingGroup(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, err
	}
	gs, err := s.groupsOf(ctx, []db.ShoppingListGroup{row})
	if err != nil {
		return Group{}, err
	}
	g := gs[0]
	if withItems {
		items, err := s.q.ListGroupItems(ctx, store.NullInt(&id))
		if err != nil {
			return g, err
		}
		if g.Items, err = s.enrichAll(ctx, items); err != nil {
			return g, err
		}
	}
	return g, nil
}

// GetGroup returns one group with its items.
func (s *Service) GetGroup(ctx context.Context, id int64) (Group, error) {
	return s.loadGroup(ctx, id, true)
}

// ListGroups returns groups in display order. kind "" means all kinds. Archived groups come last and only
// when asked for. withItems loads the items of every group.
func (s *Service) ListGroups(ctx context.Context, kind string, includeArchived, withItems bool) ([]Group, error) {
	if kind != "" && kind != KindChecklist && kind != KindTemplate {
		return nil, fmt.Errorf("%w: kind must be checklist or template", ErrInvalid)
	}
	var arch any = 0
	if includeArchived {
		arch = 1
	}
	rows, err := s.q.ListShoppingGroups(ctx, db.ListShoppingGroupsParams{Kind: kind, IncludeArchived: arch})
	if err != nil {
		return nil, err
	}
	gs, err := s.groupsOf(ctx, rows)
	if err != nil {
		return nil, err
	}
	if withItems {
		for i := range gs {
			id := gs[i].ID
			items, err := s.q.ListGroupItems(ctx, store.NullInt(&id))
			if err != nil {
				return nil, err
			}
			if gs[i].Items, err = s.enrichAll(ctx, items); err != nil {
				return nil, err
			}
		}
	}
	return gs, nil
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", fmt.Errorf("%w: name is too long (max %d characters)", ErrInvalid, maxNameRunes)
	}
	return name, nil
}

func (in *GroupInput) normalise() error {
	var err error
	if in.Name, err = cleanName(in.Name); err != nil {
		return err
	}
	switch in.Kind {
	case "":
		in.Kind = KindChecklist
	case KindChecklist, KindTemplate:
	default:
		return fmt.Errorf("%w: kind must be checklist or template", ErrInvalid)
	}
	switch in.Source {
	case "":
		in.Source = SourceUser
	case SourceUser, SourceAssistant:
	default:
		return fmt.Errorf("%w: source must be user or assistant", ErrInvalid)
	}
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.ExternalRef = strings.TrimSpace(in.ExternalRef)
	if len(in.ClientID) > 100 || len(in.ExternalRef) > 100 {
		return fmt.Errorf("%w: client_id and external_ref are at most 100 characters", ErrInvalid)
	}
	if len(in.Items) > MaxGroupItems {
		return fmt.Errorf("%w: at most %d items at once", ErrInvalid, MaxGroupItems)
	}
	return nil
}

// resolveLines prepares every line (product links, validation) before anything is written, so a bad line
// fails the whole call.
func (s *Service) resolveLines(ctx context.Context, lines []Input, source string) ([]Input, error) {
	out := make([]Input, len(lines))
	for i, l := range lines {
		l.ClientID, l.GroupID = "", nil
		if l.Source == "" {
			l.Source = source
		}
		if err := s.resolve(ctx, &l); err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		out[i] = l
	}
	return out, nil
}

func lineKey(clientID string, i int) string { return clientID + "#" + strconv.Itoa(i) }

// CreateGroup makes a group and all its items in one transaction. With a client_id a repeat returns the
// group the first call made (Created=false), however many times the phone retries. With an external_ref
// (assistants) a repeat returns the existing group of that source and ref, and ignores the items.
func (s *Service) CreateGroup(ctx context.Context, in GroupInput) (GroupResult, error) {
	if err := in.normalise(); err != nil {
		return GroupResult{}, err
	}
	if res, ok, err := s.replayGroup(ctx, in); err != nil || ok {
		return res, err
	}
	lines, err := s.resolveLines(ctx, in.Items, in.Source)
	if err != nil {
		return GroupResult{}, err
	}
	var gid int64
	var ids []int64
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := s.stamp()
		sort := int64(0)
		if in.Sort != nil {
			sort = *in.Sort
		} else {
			max, err := q.MaxShoppingGroupSort(ctx)
			if err != nil {
				return err
			}
			sort = max + 1
		}
		g, err := q.CreateShoppingGroup(ctx, db.CreateShoppingGroupParams{
			Name: in.Name, Kind: in.Kind, Note: store.NullStr(in.Note), Source: in.Source,
			ExternalRef: nullIfEmpty(in.ExternalRef), Sort: sort, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		gid = g.ID
		if in.ClientID != "" {
			if err := q.PutClientID(ctx, db.PutClientIDParams{ClientID: in.ClientID, ItemID: g.ID, Kind: KindGroup, CreatedAt: now}); err != nil {
				return err
			}
		}
		ids = ids[:0]
		for i, l := range lines {
			l.GroupID = &g.ID
			row, _, err := addItemTx(ctx, q, l, now)
			if err != nil {
				return err
			}
			ids = append(ids, row.ID)
			if in.ClientID != "" {
				if err := q.PutClientID(ctx, db.PutClientIDParams{ClientID: lineKey(in.ClientID, i), ItemID: row.ID, Kind: KindItem, CreatedAt: now}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		// Two copies of one request racing: the loser replays the winner.
		if store.IsUniqueViolation(err) {
			if res, ok, rerr := s.replayGroup(ctx, in); rerr != nil || ok {
				return res, rerr
			}
		}
		return GroupResult{}, err
	}
	g, err := s.loadGroup(ctx, gid, true)
	return GroupResult{Group: g, ItemIDs: nonNil(ids), Created: true}, err
}

func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return store.Str(s)
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// replayGroup answers a repeated create from what the first one made.
func (s *Service) replayGroup(ctx context.Context, in GroupInput) (GroupResult, bool, error) {
	if in.ClientID != "" {
		ref, err := s.q.GetClientID(ctx, in.ClientID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return GroupResult{}, false, err
		}
		if err == nil {
			if ref.Kind != KindGroup {
				return GroupResult{}, true, fmt.Errorf("%w: client_id was used for an item", ErrConflict)
			}
			g, err := s.loadGroup(ctx, ref.ItemID, true)
			if errors.Is(err, ErrNotFound) {
				return GroupResult{}, true, ErrGone
			}
			if err != nil {
				return GroupResult{}, true, err
			}
			ids := make([]int64, 0, len(in.Items))
			for i := 0; i < MaxGroupItems; i++ {
				r, err := s.q.GetClientID(ctx, lineKey(in.ClientID, i))
				if err != nil {
					break // one line key per item of the first call
				}
				ids = append(ids, r.ItemID)
			}
			return GroupResult{Group: g, ItemIDs: ids}, true, nil
		}
	}
	if in.ExternalRef != "" {
		row, err := s.q.FindShoppingGroupByRef(ctx, db.FindShoppingGroupByRefParams{Source: in.Source, ExternalRef: store.Str(in.ExternalRef)})
		if err == nil {
			g, err := s.loadGroup(ctx, row.ID, true)
			return GroupResult{Group: g, ItemIDs: []int64{}}, true, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return GroupResult{}, false, err
		}
	}
	return GroupResult{}, false, nil
}

// UpdateGroup renames, annotates, reorders, archives or restores (unarchives) a group.
func (s *Service) UpdateGroup(ctx context.Context, id int64, p GroupPatch) (Group, error) {
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		g, err := q.GetShoppingGroup(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.stamp()
		arg := db.UpdateShoppingGroupParams{Name: g.Name, Note: g.Note, Sort: g.Sort, ArchivedAt: g.ArchivedAt, UpdatedAt: now, ID: id}
		if p.Name != nil {
			if arg.Name, err = cleanName(*p.Name); err != nil {
				return err
			}
		}
		if p.Note != nil {
			arg.Note = nullIfEmpty(strings.TrimSpace(*p.Note))
		}
		if p.Sort != nil {
			arg.Sort = *p.Sort
		}
		if p.Archived != nil {
			switch {
			case *p.Archived && !g.ArchivedAt.Valid:
				arg.ArchivedAt = store.Str(now)
			case !*p.Archived:
				arg.ArchivedAt = sql.NullString{}
			}
		}
		_, err = q.UpdateShoppingGroup(ctx, arg)
		return err
	})
	if err != nil {
		return Group{}, err
	}
	return s.loadGroup(ctx, id, true)
}

// AddItems puts items into a group (merging same-product lines) and returns them in request order.
func (s *Service) AddItems(ctx context.Context, groupID int64, lines []Input, source string) ([]Item, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: items must not be empty", ErrInvalid)
	}
	if len(lines) > MaxGroupItems {
		return nil, fmt.Errorf("%w: at most %d items at once", ErrInvalid, MaxGroupItems)
	}
	if source == "" {
		source = SourceUser
	}
	if _, err := s.q.GetShoppingGroup(ctx, groupID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	prepared, err := s.resolveLines(ctx, lines, source)
	if err != nil {
		return nil, err
	}
	var ids []int64
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := s.stamp()
		ids = ids[:0]
		for _, l := range prepared {
			l.GroupID = &groupID
			row, _, err := addItemTx(ctx, q, l, now)
			if err != nil {
				return err
			}
			ids = append(ids, row.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.itemsByID(ctx, ids)
}

// MoveItem moves an item to a group, or to the main list (groupID nil). If the target already has the same
// product open, the quantities are added and the target item is returned. Template items cannot move, and
// nothing can move into a template (edit the template instead).
func (s *Service) MoveItem(ctx context.Context, itemID int64, groupID *int64) (Item, error) {
	var out int64
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		it, err := q.GetShoppingItem(ctx, itemID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = it.ID
		if sameGroup(it.GroupID, groupID) {
			return nil
		}
		if it.GroupID.Valid {
			src, err := q.GetShoppingGroup(ctx, it.GroupID.Int64)
			if err == nil && src.Kind == KindTemplate {
				return fmt.Errorf("%w: template items cannot be moved", ErrInvalid)
			}
		}
		if groupID != nil {
			dst, err := q.GetShoppingGroup(ctx, *groupID)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: group %d", ErrNotFound, *groupID)
			}
			if err != nil {
				return err
			}
			if dst.Kind == KindTemplate {
				return fmt.Errorf("%w: cannot move an item into a template", ErrInvalid)
			}
		}
		now := s.stamp()
		if !it.CheckedAt.Valid && it.ProductID.Valid {
			twin, err := q.FindOpenShoppingItemByProduct(ctx, db.FindOpenShoppingItemByProductParams{ProductID: it.ProductID, GroupID: store.NullInt(groupID)})
			if err == nil {
				qty := twin.Qty.Float64
				if !twin.Qty.Valid {
					qty = 1
				}
				add := it.Qty.Float64
				if !it.Qty.Valid {
					add = 1
				}
				if _, err := q.UpdateShoppingItem(ctx, db.UpdateShoppingItemParams{
					ProductID: twin.ProductID, Text: twin.Text, Qty: sql.NullFloat64{Float64: qty + add, Valid: true},
					Note: twin.Note, UpdatedAt: store.Str(now), ID: twin.ID,
				}); err != nil {
					return err
				}
				out = twin.ID
				if err := deleteItemTx(ctx, q, it, now); err != nil {
					return err
				}
				if err := touchGroup(ctx, q, it.GroupID, now); err != nil {
					return err
				}
				return touchGroup(ctx, q, store.NullInt(groupID), now)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if _, err := q.MoveShoppingItem(ctx, db.MoveShoppingItemParams{GroupID: store.NullInt(groupID), UpdatedAt: store.Str(now), ID: it.ID}); err != nil {
			return err
		}
		if err := touchGroup(ctx, q, it.GroupID, now); err != nil {
			return err
		}
		return touchGroup(ctx, q, store.NullInt(groupID), now)
	})
	if err != nil {
		return Item{}, err
	}
	row, err := s.get(ctx, out)
	if err != nil {
		return Item{}, err
	}
	return s.enrich(ctx, row)
}

func sameGroup(a sql.NullInt64, b *int64) bool {
	if b == nil {
		return !a.Valid
	}
	return a.Valid && a.Int64 == *b
}

type trashSnapshot struct {
	Group db.ShoppingListGroup  `json:"group"`
	Items []db.ShoppingListItem `json:"items"`
}

// DeleteGroup deletes a group. With keepItems its items move to the main list; otherwise they are deleted
// too. A template always deletes its items. The deletion can be undone with RestoreGroup for RestoreWindow.
func (s *Service) DeleteGroup(ctx context.Context, id int64, keepItems bool) (DeleteResult, error) {
	var res DeleteResult
	var keptIDs []int64
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		g, err := q.GetShoppingGroup(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if g.Kind == KindTemplate {
			keepItems = false
		}
		items, err := q.ListGroupItems(ctx, store.NullInt(&id))
		if err != nil {
			return err
		}
		snap, err := json.Marshal(trashSnapshot{Group: g, Items: items})
		if err != nil {
			return err
		}
		now := s.stamp()
		mode := "delete"
		if keepItems {
			mode = "keep"
		}
		res.DeletedItemIDs, keptIDs = []int64{}, nil
		for _, it := range items {
			if keepItems {
				if _, err := q.MoveShoppingItem(ctx, db.MoveShoppingItemParams{UpdatedAt: store.Str(now), ID: it.ID}); err != nil {
					return err
				}
				keptIDs = append(keptIDs, it.ID)
				continue
			}
			if err := deleteItemTx(ctx, q, it, now); err != nil {
				return err
			}
			res.DeletedItemIDs = append(res.DeletedItemIDs, it.ID)
		}
		if _, err := q.DeleteShoppingGroup(ctx, id); err != nil {
			return err
		}
		if err := q.RecordShoppingTombstone(ctx, db.RecordShoppingTombstoneParams{Kind: KindGroup, ID: id, DeletedAt: now}); err != nil {
			return err
		}
		return q.PutGroupTrash(ctx, db.PutGroupTrashParams{GroupID: id, Mode: mode, Snapshot: string(snap), DeletedAt: now})
	})
	if err != nil {
		return res, err
	}
	res.RestoreUntil = s.now().UTC().Truncate(time.Second).Add(RestoreWindow)
	if res.Kept, err = s.itemsByID(ctx, keptIDs); err != nil {
		return res, err
	}
	if res.Kept == nil {
		res.Kept = []Item{}
	}
	return res, nil
}

// RestoreGroup undoes DeleteGroup within RestoreWindow: the group comes back with its old id, and so do its
// items (deleted ones are recreated, kept ones move back from the main list). Restoring twice returns the
// group again. Items changed in the meantime keep their changes. After the window it is ErrGone.
func (s *Service) RestoreGroup(ctx context.Context, id int64) (Group, error) {
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		tr, err := q.GetGroupTrash(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if tr.RestoredAt.Valid {
			return nil
		}
		at, err := store.ParseTS(tr.DeletedAt)
		if err != nil {
			return err
		}
		if s.now().After(at.Add(RestoreWindow)) {
			return ErrGone
		}
		var snap trashSnapshot
		if err := json.Unmarshal([]byte(tr.Snapshot), &snap); err != nil {
			return err
		}
		now := s.stamp()
		g := snap.Group
		if _, err := q.RestoreShoppingGroup(ctx, db.RestoreShoppingGroupParams{
			ID: g.ID, Name: g.Name, Kind: g.Kind, Note: g.Note, Source: g.Source, ExternalRef: g.ExternalRef,
			Sort: g.Sort, ArchivedAt: g.ArchivedAt, CreatedAt: g.CreatedAt, UpdatedAt: store.Str(now).String,
		}); err != nil {
			if store.IsUniqueViolation(err) {
				return fmt.Errorf("%w: another group already uses this external_ref", ErrConflict)
			}
			return err
		}
		gid := store.NullInt(&g.ID)
		for _, it := range snap.Items {
			cur, err := q.GetShoppingItem(ctx, it.ID)
			switch {
			case err == nil && tr.Mode == "keep" && !cur.GroupID.Valid:
				if _, err := q.MoveShoppingItem(ctx, db.MoveShoppingItemParams{GroupID: gid, UpdatedAt: store.Str(now), ID: it.ID}); err != nil {
					return err
				}
			case err == nil:
				// the id now belongs to something else, or the user moved the item on: leave it
			case errors.Is(err, sql.ErrNoRows) && tr.Mode == "delete":
				pid := it.ProductID
				if pid.Valid {
					if _, perr := q.GetProduct(ctx, pid.Int64); perr != nil {
						pid = sql.NullInt64{}
					}
				}
				if _, err := q.RestoreShoppingItem(ctx, db.RestoreShoppingItemParams{
					ID: it.ID, ProductID: pid, Text: it.Text, Qty: it.Qty, Note: it.Note, CheckedAt: it.CheckedAt,
					GroupID: gid, Source: it.Source, CreatedAt: it.CreatedAt, UpdatedAt: store.Str(now),
					CheckSource: it.CheckSource, CheckedReceiptID: it.CheckedReceiptID, CheckedLineID: it.CheckedLineID,
				}); err != nil {
					return err
				}
			case err != nil && !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		return q.MarkGroupTrashRestored(ctx, db.MarkGroupTrashRestoredParams{RestoredAt: store.Str(now), GroupID: id})
	})
	if err != nil {
		return Group{}, err
	}
	return s.loadGroup(ctx, id, true)
}

// Instantiate starts a checklist from a template: same items, all unchecked. name is optional (defaults to
// the template's). With a client_id a repeat returns the first result.
func (s *Service) Instantiate(ctx context.Context, templateID int64, name, clientID, source string) (GroupResult, error) {
	return s.clone(ctx, templateID, KindTemplate, KindChecklist, name, clientID, source)
}

// SaveAsTemplate saves a checklist's items (unchecked) as a template.
func (s *Service) SaveAsTemplate(ctx context.Context, groupID int64, name, clientID string) (GroupResult, error) {
	return s.clone(ctx, groupID, KindChecklist, KindTemplate, name, clientID, "")
}

func (s *Service) clone(ctx context.Context, srcID int64, srcKind, dstKind, name, clientID, source string) (GroupResult, error) {
	clientID = strings.TrimSpace(clientID)
	if len(clientID) > 100 {
		return GroupResult{}, fmt.Errorf("%w: client_id is too long", ErrInvalid)
	}
	if source == "" {
		source = SourceUser
	}
	if source != SourceUser && source != SourceAssistant {
		return GroupResult{}, fmt.Errorf("%w: source must be user or assistant", ErrInvalid)
	}
	if clientID != "" {
		if res, ok, err := s.replayGroup(ctx, GroupInput{ClientID: clientID}); err != nil || ok {
			return res, err
		}
	}
	var gid int64
	var ids []int64
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		src, err := q.GetShoppingGroup(ctx, srcID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if src.Kind != srcKind {
			return fmt.Errorf("%w: group %d is a %s, expected a %s", ErrInvalid, srcID, src.Kind, srcKind)
		}
		if strings.TrimSpace(name) == "" {
			name = src.Name
		}
		if name, err = cleanName(name); err != nil {
			return err
		}
		items, err := q.ListGroupItems(ctx, store.NullInt(&srcID))
		if err != nil {
			return err
		}
		now := s.stamp()
		max, err := q.MaxShoppingGroupSort(ctx)
		if err != nil {
			return err
		}
		g, err := q.CreateShoppingGroup(ctx, db.CreateShoppingGroupParams{
			Name: name, Kind: dstKind, Note: src.Note, Source: source, Sort: max + 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		gid = g.ID
		if clientID != "" {
			if err := q.PutClientID(ctx, db.PutClientIDParams{ClientID: clientID, ItemID: g.ID, Kind: KindGroup, CreatedAt: now}); err != nil {
				return err
			}
		}
		for i, it := range items {
			row, err := q.CreateShoppingItem(ctx, db.CreateShoppingItemParams{
				ProductID: it.ProductID, Text: it.Text, Qty: it.Qty, Note: it.Note, GroupID: store.NullInt(&g.ID),
				Source: source, CreatedAt: now, UpdatedAt: store.Str(now),
			})
			if err != nil {
				return err
			}
			ids = append(ids, row.ID)
			if clientID != "" {
				if err := q.PutClientID(ctx, db.PutClientIDParams{ClientID: lineKey(clientID, i), ItemID: row.ID, Kind: KindItem, CreatedAt: now}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if clientID != "" && store.IsUniqueViolation(err) {
			if res, ok, rerr := s.replayGroup(ctx, GroupInput{ClientID: clientID}); rerr != nil || ok {
				return res, rerr
			}
		}
		return GroupResult{}, err
	}
	g, err := s.loadGroup(ctx, gid, true)
	return GroupResult{Group: g, ItemIDs: nonNil(ids), Created: true}, err
}
