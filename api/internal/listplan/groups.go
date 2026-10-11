// Package listplan answers "where should I buy what on my list?".
//
// Two parts:
//   - product groups: the user says once that two products are "the same kind of item" (the house-brand
//     milk of two shops). Suggestions are computed on demand and are never applied automatically; every
//     change is recorded so it can be undone;
//   - the plan: GET /v1/list/plan splits the open list by shop, net of a cost per extra stop, and counts
//     savings only on items that have a fresh price at both shops. See plan.go for the rules.
package listplan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"shop/internal/store"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
)

// UndoWindow is how long a group change can be undone.
const UndoWindow = 30 * 24 * time.Hour

const maxNameRunes = 100

// queryer is what both *sql.DB and *sql.Tx offer.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type Service struct {
	conn *sql.DB
	now  func() time.Time

	sugMu    sync.Mutex
	sugCache *sugCache
}

func New(conn *sql.DB) *Service { return &Service{conn: conn, now: time.Now} }

func (s *Service) stamp() string { return store.TS(s.now()) }

// effectiveGroup is the group a live product counts in: its own, or, when it has none, the group of a product
// that was merged into it (a merge must not silently take a product out of its group).
const effectiveGroup = `COALESCE(p.group_id, (SELECT m.group_id FROM products m WHERE m.merged_into_id = p.id AND m.group_id IS NOT NULL ORDER BY m.id LIMIT 1))`

// Member is a product in a group.
type Member struct {
	ProductID     int64      `json:"product_id"`
	Name          string     `json:"name"`
	NameEN        string     `json:"name_en,omitempty"`
	Brand         string     `json:"brand,omitempty"`
	CategoryID    *int64     `json:"category_id"`
	BaseUnit      string     `json:"base_unit"`
	PackSizeKnown bool       `json:"pack_size_known"`
	Shops         []ShopRef  `json:"shops"`
	Purchases     int        `json:"purchases"`
	LastBought    *time.Time `json:"last_bought_at"`
}

type ShopRef struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Chain string `json:"chain,omitempty"`
}

// Group is a set of products that are the same kind of item.
type Group struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	CategoryID *int64    `json:"category_id"`
	BaseUnit   string    `json:"base_unit"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Members    []Member  `json:"members"`
	// ShopCount is how many shops the members were bought at (a group is useful for plans at 2 or more).
	ShopCount int `json:"shop_count"`
}

// unitOf classifies a pack size: g → kg, ml → l, pc → pc; no size means "pc" with known=false.
func unitOf(qty sql.NullFloat64, unit sql.NullString) (base string, known bool) {
	if qty.Valid && qty.Float64 > 0 && unit.Valid {
		switch unit.String {
		case "g":
			return "kg", true
		case "ml":
			return "l", true
		case "pc":
			return "pc", true
		}
	}
	return "pc", false
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

type prodRow struct {
	id         int64
	name       string
	nameEN     string
	brand      string
	categoryID sql.NullInt64
	netQty     sql.NullFloat64
	netUnit    sql.NullString
	group      sql.NullInt64 // effective group
}

func loadProducts(ctx context.Context, q queryer, where string, args ...any) ([]prodRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT p.id, p.name, COALESCE(p.name_en, ''), COALESCE(p.brand, ''), p.category_id,
		p.net_quantity, p.net_unit, `+effectiveGroup+`
		FROM products p WHERE p.deleted_at IS NULL AND `+where+` ORDER BY p.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []prodRow
	for rows.Next() {
		var r prodRow
		if err := rows.Scan(&r.id, &r.name, &r.nameEN, &r.brand, &r.categoryID, &r.netQty, &r.netUnit, &r.group); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func placeholders[T any](ids []T) (string, []any) {
	if len(ids) == 0 {
		return "NULL", nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

type shopInfo struct {
	purchases int
	last      string
	shops     map[int64]ShopRef
}

// purchaseInfo returns, per product, the shops it was bought at (ok receipts with a date) and when last.
func purchaseInfo(ctx context.Context, q queryer, ids []int64) (map[int64]*shopInfo, error) {
	out := map[int64]*shopInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	ph, args := placeholders(ids)
	rows, err := q.QueryContext(ctx, `SELECT pp.product_id, pp.shop_id, s.name, COALESCE(s.chain, ''), COUNT(*), MAX(pp.purchased_at)
		FROM price_points pp JOIN shops s ON s.id = pp.shop_id AND s.deleted_at IS NULL
		WHERE pp.product_id IN (`+ph+`) AND pp.purchased_at IS NOT NULL AND pp.paid_cents > 0
		GROUP BY pp.product_id, pp.shop_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, sid int64
		var name, chain, last string
		var n int
		if err := rows.Scan(&pid, &sid, &name, &chain, &n, &last); err != nil {
			return nil, err
		}
		si := out[pid]
		if si == nil {
			si = &shopInfo{shops: map[int64]ShopRef{}}
			out[pid] = si
		}
		si.purchases += n
		if last > si.last {
			si.last = last
		}
		si.shops[sid] = ShopRef{ID: sid, Name: name, Chain: chain}
	}
	return out, rows.Err()
}

func (si *shopInfo) shopList() []ShopRef {
	if si == nil {
		return []ShopRef{}
	}
	out := make([]ShopRef, 0, len(si.shops))
	for _, s := range si.shops {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type groupRow struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	CategoryID sql.NullInt64  `json:"category_id"`
	BaseUnit   sql.NullString `json:"base_unit"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

func getGroupRow(ctx context.Context, q queryer, id int64) (groupRow, error) {
	var g groupRow
	err := q.QueryRowContext(ctx, `SELECT id, name, category_id, base_unit, created_at, updated_at FROM product_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.CategoryID, &g.BaseUnit, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

func allGroupRows(ctx context.Context, q queryer) ([]groupRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, category_id, base_unit, created_at, updated_at FROM product_groups ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []groupRow
	for rows.Next() {
		var g groupRow
		if err := rows.Scan(&g.ID, &g.Name, &g.CategoryID, &g.BaseUnit, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Service) buildGroups(ctx context.Context, q queryer, rows []groupRow) ([]Group, error) {
	ps, err := loadProducts(ctx, q, effectiveGroup+` IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.id
	}
	info, err := purchaseInfo(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	byGroup := map[int64][]Member{}
	groupShops := map[int64]map[int64]bool{}
	for _, p := range ps {
		base, known := unitOf(p.netQty, p.netUnit)
		si := info[p.id]
		m := Member{ProductID: p.id, Name: p.name, NameEN: p.nameEN, Brand: p.brand, CategoryID: store.IntPtr(p.categoryID),
			BaseUnit: base, PackSizeKnown: known, Shops: si.shopList()}
		if si != nil {
			m.Purchases = si.purchases
			if t, err := store.ParseTS(si.last); err == nil {
				m.LastBought = &t
			}
		}
		g := p.group.Int64
		byGroup[g] = append(byGroup[g], m)
		if groupShops[g] == nil {
			groupShops[g] = map[int64]bool{}
		}
		for _, sh := range m.Shops {
			groupShops[g][sh.ID] = true
		}
	}
	out := make([]Group, 0, len(rows))
	for _, r := range rows {
		g := Group{ID: r.ID, Name: r.Name, CategoryID: store.IntPtr(r.CategoryID), BaseUnit: r.BaseUnit.String,
			Members: byGroup[r.ID], ShopCount: len(groupShops[r.ID])}
		if g.Members == nil {
			g.Members = []Member{}
		}
		g.CreatedAt, _ = store.ParseTS(r.CreatedAt)
		g.UpdatedAt, _ = store.ParseTS(r.UpdatedAt)
		out = append(out, g)
	}
	return out, nil
}

// Groups lists every product group with its members.
func (s *Service) Groups(ctx context.Context) ([]Group, error) {
	rows, err := allGroupRows(ctx, s.conn)
	if err != nil {
		return nil, err
	}
	return s.buildGroups(ctx, s.conn, rows)
}

// GetGroup returns one group with its members.
func (s *Service) GetGroup(ctx context.Context, id int64) (Group, error) {
	r, err := getGroupRow(ctx, s.conn, id)
	if err != nil {
		return Group{}, err
	}
	gs, err := s.buildGroups(ctx, s.conn, []groupRow{r})
	if err != nil {
		return Group{}, err
	}
	return gs[0], nil
}

// Rename changes a group's name.
func (s *Service) Rename(ctx context.Context, id int64, name string) (Group, error) {
	name, err := cleanName(name)
	if err != nil {
		return Group{}, err
	}
	res, err := s.conn.ExecContext(ctx, `UPDATE product_groups SET name = ?, updated_at = ? WHERE id = ?`, name, s.stamp(), id)
	if err != nil {
		return Group{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Group{}, ErrNotFound
	}
	return s.GetGroup(ctx, id)
}

// ---- changes and undo ----

// snapshot is what an undo needs: groups as they were, which groups the change created, the group_id of every
// product it touched, and the denied pairs it added or removed.
type snapshot struct {
	Groups        []groupRow  `json:"groups,omitempty"`
	Created       []int64     `json:"created,omitempty"`
	Products      []prodGroup `json:"products,omitempty"`
	Denied        [][2]int64  `json:"denied,omitempty"`
	DeniedRemoved [][2]int64  `json:"denied_removed,omitempty"`
}

type prodGroup struct {
	ID    int64  `json:"id"`
	Group *int64 `json:"group"`
}

// change collects a snapshot while a transaction edits groups.
type change struct {
	ctx  context.Context
	tx   queryer
	snap snapshot
	seen map[int64]bool // products
	gone map[int64]bool // groups recorded
	now  string
}

func (s *Service) newChange(ctx context.Context, tx queryer) *change {
	return &change{ctx: ctx, tx: tx, seen: map[int64]bool{}, gone: map[int64]bool{}, now: s.stamp()}
}

func (c *change) recordGroup(id int64) error {
	if c.gone[id] {
		return nil
	}
	c.gone[id] = true
	g, err := getGroupRow(c.ctx, c.tx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	c.snap.Groups = append(c.snap.Groups, g)
	return nil
}

// setGroup puts a product (and anything merged into it) in a group, or in none when to is nil.
func (c *change) setGroup(productID int64, to *int64) error {
	rows, err := c.tx.QueryContext(c.ctx, `SELECT id, group_id FROM products WHERE id = ? OR (merged_into_id = ? AND group_id IS NOT NULL)`, productID, productID)
	if err != nil {
		return err
	}
	var touched []prodGroup
	for rows.Next() {
		var pg prodGroup
		var g sql.NullInt64
		if err := rows.Scan(&pg.ID, &g); err != nil {
			rows.Close()
			return err
		}
		pg.Group = store.IntPtr(g)
		touched = append(touched, pg)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, pg := range touched {
		if !c.seen[pg.ID] {
			c.seen[pg.ID] = true
			c.snap.Products = append(c.snap.Products, pg)
		}
		var v any
		if pg.ID == productID && to != nil {
			v = *to
		}
		if _, err := c.tx.ExecContext(c.ctx, `UPDATE products SET group_id = ?, updated_at = ? WHERE id = ?`, v, c.now, pg.ID); err != nil {
			return err
		}
	}
	return nil
}

func (c *change) memberIDs(group int64) ([]int64, error) {
	ps, err := loadProducts(c.ctx, c.tx, effectiveGroup+` = ?`, group)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.id
	}
	return ids, nil
}

// refresh recomputes a group's category and unit from its members; a group with fewer than two members is
// dissolved. It reports whether the group still exists.
func (c *change) refresh(group int64) (bool, error) {
	if err := c.recordGroup(group); err != nil {
		return false, err
	}
	ps, err := loadProducts(c.ctx, c.tx, effectiveGroup+` = ?`, group)
	if err != nil {
		return false, err
	}
	if len(ps) < 2 {
		for _, p := range ps {
			if err := c.setGroup(p.id, nil); err != nil {
				return false, err
			}
		}
		_, err := c.tx.ExecContext(c.ctx, `DELETE FROM product_groups WHERE id = ?`, group)
		return false, err
	}
	var cat sql.NullInt64
	base, haveBase := "pc", false
	for _, p := range ps {
		if !cat.Valid && p.categoryID.Valid {
			cat = p.categoryID
		}
		if b, known := unitOf(p.netQty, p.netUnit); known && !haveBase {
			base, haveBase = b, true
		}
	}
	_, err = c.tx.ExecContext(c.ctx, `UPDATE product_groups SET category_id = ?, base_unit = ?, updated_at = ? WHERE id = ?`, cat, base, c.now, group)
	return true, err
}

func (c *change) finish(kind string) (int64, error) {
	b, _ := json.Marshal(c.snap)
	res, err := c.tx.ExecContext(c.ctx, `INSERT INTO product_group_ops (kind, snapshot, created_at) VALUES (?, ?, ?)`, kind, string(b), c.now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Service) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

// Result is the outcome of a group change: the group (nil when it no longer exists) and the id to undo it.
type Result struct {
	Group *Group `json:"group"`
	OpID  int64  `json:"op_id"`
	// Dissolved lists groups that ended up with fewer than two members, or were merged away, and are gone.
	Dissolved []int64 `json:"dissolved_group_ids"`
}

func uniq(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Confirm says the given products are the same kind of item. Products that already belong to groups pull those
// groups in: all of them become one group (the oldest keeps its id and, unless name is given, its name).
func (s *Service) Confirm(ctx context.Context, productIDs []int64, name string) (Result, error) {
	productIDs = uniq(productIDs)
	if len(productIDs) < 2 {
		return Result{}, fmt.Errorf("%w: at least two different products are needed", ErrInvalid)
	}
	if len(productIDs) > 50 {
		return Result{}, fmt.Errorf("%w: at most 50 products at once", ErrInvalid)
	}
	name = strings.TrimSpace(name)
	if name != "" {
		var err error
		if name, err = cleanName(name); err != nil {
			return Result{}, err
		}
	}
	res := Result{Dissolved: []int64{}}
	var gid int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		ph, args := placeholders(productIDs)
		ps, err := loadProducts(ctx, tx, `p.id IN (`+ph+`)`, args...)
		if err != nil {
			return err
		}
		if len(ps) != len(productIDs) {
			return fmt.Errorf("%w: a product does not exist", ErrInvalid)
		}
		c := s.newChange(ctx, tx)
		// groups involved
		var existing []int64
		for _, p := range ps {
			if p.group.Valid {
				existing = append(existing, p.group.Int64)
			}
		}
		existing = uniq(existing)
		sort.Slice(existing, func(i, j int) bool { return existing[i] < existing[j] })
		var all []int64
		all = append(all, productIDs...)
		for _, g := range existing {
			ms, err := c.memberIDs(g)
			if err != nil {
				return err
			}
			all = append(all, ms...)
		}
		all = uniq(all)
		if len(existing) == 0 {
			if name == "" {
				name = defaultName(ps)
			}
			r, err := tx.ExecContext(ctx, `INSERT INTO product_groups (name, created_at, updated_at) VALUES (?, ?, ?)`, name, c.now, c.now)
			if err != nil {
				return err
			}
			gid, _ = r.LastInsertId()
			c.snap.Created = append(c.snap.Created, gid)
		} else {
			gid = existing[0]
			if err := c.recordGroup(gid); err != nil {
				return err
			}
			if name != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE product_groups SET name = ?, updated_at = ? WHERE id = ?`, name, c.now, gid); err != nil {
					return err
				}
			}
		}
		for _, other := range existing {
			if other != gid {
				if err := c.recordGroup(other); err != nil {
					return err
				}
				res.Dissolved = append(res.Dissolved, other)
			}
		}
		for _, id := range all {
			if err := c.setGroup(id, &gid); err != nil {
				return err
			}
		}
		for _, other := range existing {
			if other != gid {
				if _, err := tx.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ?`, other); err != nil {
					return err
				}
			}
		}
		if _, err := c.refresh(gid); err != nil {
			return err
		}
		// a pair the user denied earlier is now confirmed
		for i := range all {
			for j := i + 1; j < len(all); j++ {
				a, b := ordered(all[i], all[j])
				r, err := tx.ExecContext(ctx, `DELETE FROM product_group_denied WHERE a_id = ? AND b_id = ?`, a, b)
				if err != nil {
					return err
				}
				if n, _ := r.RowsAffected(); n > 0 {
					c.snap.DeniedRemoved = append(c.snap.DeniedRemoved, [2]int64{a, b})
				}
			}
		}
		res.OpID, err = c.finish("confirm")
		return err
	})
	if err != nil {
		return Result{}, err
	}
	g, err := s.GetGroup(ctx, gid)
	if err != nil {
		return Result{}, err
	}
	res.Group = &g
	return res, nil
}

func ordered(a, b int64) (int64, int64) {
	if a > b {
		return b, a
	}
	return a, b
}

// defaultName is the English name of the first product that has one, else its name.
func defaultName(ps []prodRow) string {
	for _, p := range ps {
		if p.nameEN != "" {
			return p.nameEN
		}
	}
	return ps[0].name
}

// Merge makes group id disappear into group intoID: its members move over.
func (s *Service) Merge(ctx context.Context, id, intoID int64) (Result, error) {
	if id == intoID {
		return Result{}, fmt.Errorf("%w: a group cannot be merged into itself", ErrInvalid)
	}
	res := Result{Dissolved: []int64{}}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := getGroupRow(ctx, tx, id); err != nil {
			return err
		}
		if _, err := getGroupRow(ctx, tx, intoID); err != nil {
			return err
		}
		c := s.newChange(ctx, tx)
		if err := c.recordGroup(id); err != nil {
			return err
		}
		if err := c.recordGroup(intoID); err != nil {
			return err
		}
		ms, err := c.memberIDs(id)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if err := c.setGroup(m, &intoID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ?`, id); err != nil {
			return err
		}
		res.Dissolved = []int64{id}
		if _, err := c.refresh(intoID); err != nil {
			return err
		}
		res.OpID, err = c.finish("merge")
		return err
	})
	if err != nil {
		return Result{}, err
	}
	g, err := s.GetGroup(ctx, intoID)
	if err != nil {
		return Result{}, err
	}
	res.Group = &g
	return res, nil
}

// Split takes products out of a group. With keepTogether (and at least two products) they form a new group
// named newName instead of being left without one. A group left with fewer than two members is dissolved.
func (s *Service) Split(ctx context.Context, id int64, productIDs []int64, keepTogether bool, newName string) (Result, error) {
	productIDs = uniq(productIDs)
	if len(productIDs) == 0 {
		return Result{}, fmt.Errorf("%w: product_ids is required", ErrInvalid)
	}
	newName = strings.TrimSpace(newName)
	if newName != "" {
		var err error
		if newName, err = cleanName(newName); err != nil {
			return Result{}, err
		}
	}
	res := Result{Dissolved: []int64{}}
	var stillThere bool
	var newID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		g, err := getGroupRow(ctx, tx, id)
		if err != nil {
			return err
		}
		c := s.newChange(ctx, tx)
		members, err := c.memberIDs(id)
		if err != nil {
			return err
		}
		in := map[int64]bool{}
		for _, m := range members {
			in[m] = true
		}
		for _, p := range productIDs {
			if !in[p] {
				return fmt.Errorf("%w: product %d is not in this group", ErrInvalid, p)
			}
		}
		if err := c.recordGroup(id); err != nil {
			return err
		}
		if keepTogether && len(productIDs) >= 2 && len(productIDs) < len(members) {
			if newName == "" {
				newName = g.Name
			}
			r, err := tx.ExecContext(ctx, `INSERT INTO product_groups (name, created_at, updated_at) VALUES (?, ?, ?)`, newName, c.now, c.now)
			if err != nil {
				return err
			}
			newID, _ = r.LastInsertId()
			c.snap.Created = append(c.snap.Created, newID)
		}
		for _, p := range productIDs {
			var to *int64
			if newID != 0 {
				to = &newID
			}
			if err := c.setGroup(p, to); err != nil {
				return err
			}
		}
		if newID != 0 {
			if _, err := c.refresh(newID); err != nil {
				return err
			}
		}
		if stillThere, err = c.refresh(id); err != nil {
			return err
		}
		if !stillThere {
			res.Dissolved = []int64{id}
		}
		res.OpID, err = c.finish("split")
		return err
	})
	if err != nil {
		return Result{}, err
	}
	show := id
	switch {
	case stillThere:
	case newID != 0:
		show = newID
	default:
		return res, nil
	}
	g, err := s.GetGroup(ctx, show)
	if err != nil {
		return Result{}, err
	}
	res.Group = &g
	return res, nil
}

// Delete dissolves a group: its products stay, the grouping goes.
func (s *Service) Delete(ctx context.Context, id int64) (Result, error) {
	res := Result{Dissolved: []int64{}}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := getGroupRow(ctx, tx, id); err != nil {
			return err
		}
		c := s.newChange(ctx, tx)
		if err := c.recordGroup(id); err != nil {
			return err
		}
		ms, err := c.memberIDs(id)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if err := c.setGroup(m, nil); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ?`, id); err != nil {
			return err
		}
		res.Dissolved = []int64{id}
		res.OpID, err = c.finish("delete")
		return err
	})
	return res, err
}

// Deny records that two products are NOT the same kind of item; the pair is not suggested again.
func (s *Service) Deny(ctx context.Context, a, b int64) (int64, error) {
	if a == b {
		return 0, fmt.Errorf("%w: two different products are needed", ErrInvalid)
	}
	a, b = ordered(a, b)
	var op int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		ps, err := loadProducts(ctx, tx, `p.id IN (?, ?)`, a, b)
		if err != nil {
			return err
		}
		if len(ps) != 2 {
			return fmt.Errorf("%w: a product does not exist", ErrInvalid)
		}
		if ps[0].group.Valid && ps[1].group.Valid && ps[0].group.Int64 == ps[1].group.Int64 {
			return fmt.Errorf("%w: these two are already in one group; split them first", ErrConflict)
		}
		c := s.newChange(ctx, tx)
		r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO product_group_denied (a_id, b_id, created_at) VALUES (?, ?, ?)`, a, b, c.now)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			c.snap.Denied = append(c.snap.Denied, [2]int64{a, b})
		}
		op, err = c.finish("deny")
		return err
	})
	return op, err
}

// Undo reverses a group change exactly. Within UndoWindow, once.
func (s *Service) Undo(ctx context.Context, opID int64) (Result, error) {
	res := Result{Dissolved: []int64{}}
	var focus int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var kind, snapJSON, created string
		var undone sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT kind, snapshot, created_at, undone_at FROM product_group_ops WHERE id = ?`, opID).
			Scan(&kind, &snapJSON, &created, &undone)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if undone.Valid {
			return fmt.Errorf("%w: already undone", ErrConflict)
		}
		if t, err := store.ParseTS(created); err == nil && s.now().Sub(t) > UndoWindow {
			return fmt.Errorf("%w: too old to undo", ErrConflict)
		}
		var snap snapshot
		if err := json.Unmarshal([]byte(snapJSON), &snap); err != nil {
			return err
		}
		now := s.stamp()
		// groups first (products may point at them), then products, then the groups that did not exist
		for _, g := range snap.Groups {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO product_groups (id, name, category_id, base_unit, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
				g.ID, g.Name, g.CategoryID, g.BaseUnit, g.CreatedAt, g.UpdatedAt); err != nil {
				return err
			}
			if focus == 0 {
				focus = g.ID
			}
		}
		for _, p := range snap.Products {
			var v any
			if p.Group != nil {
				v = *p.Group
			}
			if _, err := tx.ExecContext(ctx, `UPDATE products SET group_id = ?, updated_at = ? WHERE id = ?`, v, now, p.ID); err != nil {
				return err
			}
		}
		for _, id := range snap.Created {
			if _, err := tx.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ?`, id); err != nil {
				return err
			}
		}
		for _, d := range snap.Denied {
			if _, err := tx.ExecContext(ctx, `DELETE FROM product_group_denied WHERE a_id = ? AND b_id = ?`, d[0], d[1]); err != nil {
				return err
			}
		}
		for _, d := range snap.DeniedRemoved {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO product_group_denied (a_id, b_id, created_at) VALUES (?, ?, ?)`, d[0], d[1], now); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE product_group_ops SET undone_at = ? WHERE id = ?`, now, opID)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	res.OpID = opID
	if focus != 0 {
		if g, err := s.GetGroup(ctx, focus); err == nil {
			res.Group = &g
		}
	}
	return res, nil
}
