package listplan

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/store"
)

// now is the fixed clock of every test.
var now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

type env struct {
	t    *testing.T
	conn *sql.DB
	s    *Service
	ctx  context.Context
	n    int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	s := New(conn)
	s.now = func() time.Time { return now }
	return &env{t: t, conn: conn, s: s, ctx: ctx}
}

func (e *env) exec(q string, args ...any) int64 {
	e.t.Helper()
	res, err := e.conn.Exec(q, args...)
	if err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *env) shop(name string) int64 {
	return e.exec(`INSERT INTO shops (name, created_at, updated_at) VALUES (?, 't', 't')`, name)
}

func (e *env) category(name string) int64 {
	return e.exec(`INSERT INTO categories (name, sort) VALUES (?, 500)`, name)
}

// product creates a product; size is net quantity in unit ("g", "ml", "pc"), 0 for none.
func (e *env) product(name string, cat int64, size float64, unit string) int64 {
	var c, q, u any
	if cat != 0 {
		c = cat
	}
	if size > 0 {
		q, u = size, unit
	}
	return e.exec(`INSERT INTO products (name, category_id, net_quantity, net_unit, created_at, updated_at) VALUES (?, ?, ?, ?, 't', 't')`, name, c, q, u)
}

func (e *env) daysAgo(d int) string { return store.TS(now.AddDate(0, 0, -d)) }

func (e *env) receipt(shop int64, daysAgo int, status string) int64 {
	e.n++
	var sid any
	if shop != 0 {
		sid = shop
	}
	return e.exec(`INSERT INTO receipts (shop_id, purchased_at, image_path, image_sha256, status, confirmed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 't', 't', 't')`, sid, e.daysAgo(daysAgo), fmt.Sprintf("p%d", e.n), fmt.Sprintf("s%d", e.n), status)
}

func (e *env) line(rid, product, total int64, qty float64, unit string) int64 {
	e.n++
	return e.exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, line_total_cents, product_id)
		VALUES (?, ?, 'x', 'x', 'item', ?, ?, ?, ?)`, rid, e.n, qty, unit, total, product)
}

func (e *env) discount(rid, parent, total int64) {
	e.n++
	e.exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, line_total_cents, parent_line_id)
		VALUES (?, ?, 'korting', 'korting', 'discount', 1, 'pc', ?, ?)`, rid, e.n, total, parent)
}

// buy is a receipt with one line.
func (e *env) buy(shop int64, daysAgo int, product, total int64) {
	e.t.Helper()
	e.line(e.receipt(shop, daysAgo, "ok"), product, total, 1, "pc")
}

func (e *env) item(text string, product int64, qty float64) int64 {
	var p, q any
	if product != 0 {
		p = product
	}
	if qty > 0 {
		q = qty
	}
	e.n++
	return e.exec(`INSERT INTO shopping_list_items (product_id, text, qty, created_at, updated_at) VALUES (?, ?, ?, ?, 't')`, p, text, q, store.TS(now))
}

func (e *env) plan(opts ...Options) Plan {
	e.t.Helper()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	p, err := e.s.Plan(e.ctx, o)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (p Plan) item(id int64) PlanItem {
	for _, it := range p.Items {
		if it.ItemID == id {
			return it
		}
	}
	panic("item not in plan")
}

func ip(v int64) *int64 { return &v }
func ii(v int) *int     { return &v }

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
