package listplan

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// dump is everything a group change may touch, as text, to compare before and after an undo.
func (e *env) dump() string {
	e.t.Helper()
	var b strings.Builder
	q := func(title, query string) {
		rows, err := e.conn.Query(query)
		if err != nil {
			e.t.Fatal(err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		b.WriteString(title + "\n")
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				e.t.Fatal(err)
			}
			fmt.Fprintln(&b, vals...)
		}
	}
	q("groups", `SELECT id, name, category_id, base_unit, created_at, updated_at FROM product_groups ORDER BY id`)
	q("products", `SELECT id, group_id FROM products ORDER BY id`)
	q("denied", `SELECT a_id, b_id FROM product_group_denied ORDER BY a_id, b_id`)
	return b.String()
}

func (e *env) groupOf(product int64) int64 {
	var g *int64
	if err := e.conn.QueryRow(`SELECT group_id FROM products WHERE id = ?`, product).Scan(&g); err != nil {
		e.t.Fatal(err)
	}
	if g == nil {
		return 0
	}
	return *g
}

func memberIDs(g *Group) []int64 {
	var out []int64
	for _, m := range g.Members {
		out = append(out, m.ProductID)
	}
	return out
}

type milks struct {
	e              *env
	a, b, c, d     int64 // AH, Dirk, Lidl, an unrelated product
	pa, pb, pc, pd int64
}

func newMilks(t *testing.T) *milks {
	e := newEnv(t)
	m := &milks{e: e}
	m.a, m.b, m.c = e.shop("Albert Heijn"), e.shop("Dirk"), e.shop("Lidl")
	dairy := e.category("Zuivel")
	m.pa = e.product("AH Halfvolle melk", dairy, 1000, "ml")
	m.pb = e.product("Dirk Halfvolle melk", dairy, 1000, "ml")
	m.pc = e.product("Lidl Halfvolle melk", dairy, 1000, "ml")
	m.pd = e.product("Bak olie", e.category("Voorraad"), 1000, "ml")
	e.buy(m.a, 5, m.pa, 150)
	e.buy(m.b, 4, m.pb, 100)
	e.buy(m.c, 3, m.pc, 90)
	e.buy(m.a, 2, m.pd, 300)
	return m
}

func TestConfirmCreatesAGroupSpanningTwoShops(t *testing.T) {
	m := newMilks(t)
	res, err := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	if err != nil {
		t.Fatal(err)
	}
	g := res.Group
	if g == nil || res.OpID == 0 || g.Name != "AH Halfvolle melk" || g.BaseUnit != "l" || g.CategoryID == nil || len(g.Members) != 2 || g.ShopCount != 2 {
		t.Fatalf("group = %+v", g)
	}
	for _, mem := range g.Members {
		if len(mem.Shops) != 1 || mem.Purchases != 1 || mem.LastBought == nil || !mem.PackSizeKnown {
			t.Fatalf("member = %+v", mem)
		}
	}
	if m.e.groupOf(m.pa) != g.ID || m.e.groupOf(m.pb) != g.ID || m.e.groupOf(m.pc) != 0 {
		t.Fatal("membership not stored")
	}
	// name given -> used
	res2, err := m.e.s.Confirm(m.e.ctx, []int64{m.pc, m.pa}, "Melk")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Group.ID != g.ID || res2.Group.Name != "Melk" || len(res2.Group.Members) != 3 || res2.Group.ShopCount != 3 {
		t.Fatalf("joining an existing group: %+v", res2.Group)
	}
	if list, _ := m.e.s.Groups(m.e.ctx); len(list) != 1 {
		t.Fatalf("groups = %d", len(list))
	}
	for _, bad := range [][]int64{{m.pa}, {m.pa, m.pa}, {m.pa, 9999}, nil} {
		if _, err := m.e.s.Confirm(m.e.ctx, bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("Confirm(%v) = %v", bad, err)
		}
	}
	if _, err := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, strings.Repeat("x", 101)); !errors.Is(err, ErrInvalid) {
		t.Errorf("long name: %v", err)
	}
}

func TestConfirmUndo(t *testing.T) {
	m := newMilks(t)
	before := m.e.dump()
	res, err := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.e.dump() == before {
		t.Fatal("confirm changed nothing")
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); err != nil {
		t.Fatal(err)
	}
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); !errors.Is(err, ErrConflict) {
		t.Fatalf("second undo = %v", err)
	}
	if _, err := m.e.s.Undo(m.e.ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown op = %v", err)
	}
}

func TestUndoTooOld(t *testing.T) {
	m := newMilks(t)
	res, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	m.e.s.now = func() time.Time { return now.Add(UndoWindow + time.Hour) }
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); !errors.Is(err, ErrConflict) {
		t.Fatalf("undo after the window = %v", err)
	}
}

func TestConfirmAcrossTwoGroupsMergesThemAndUndoes(t *testing.T) {
	m := newMilks(t)
	g1, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "AH+Dirk")
	g2, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pc, m.pd}, "Lidl+Olie")
	before := m.e.dump()
	res, err := m.e.s.Confirm(m.e.ctx, []int64{m.pb, m.pc}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Group.ID != g1.Group.ID || len(res.Group.Members) != 4 || len(res.Dissolved) != 1 || res.Dissolved[0] != g2.Group.ID {
		t.Fatalf("result = %+v %v", res.Group, res.Dissolved)
	}
	if _, err := m.e.s.GetGroup(m.e.ctx, g2.Group.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the absorbed group must be gone: %v", err)
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); err != nil {
		t.Fatal(err)
	}
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}
}

func TestMergeGroupsAndUndo(t *testing.T) {
	m := newMilks(t)
	g1, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "One")
	g2, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pc, m.pd}, "Two")
	before := m.e.dump()
	res, err := m.e.s.Merge(m.e.ctx, g2.Group.ID, g1.Group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Group.ID != g1.Group.ID || len(res.Group.Members) != 4 || res.Group.Name != "One" {
		t.Fatalf("merged = %+v", res.Group)
	}
	if _, err := m.e.s.GetGroup(m.e.ctx, g2.Group.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("source group must be gone")
	}
	if _, err := m.e.s.Merge(m.e.ctx, g1.Group.ID, g1.Group.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self merge = %v", err)
	}
	if _, err := m.e.s.Merge(m.e.ctx, 999, g1.Group.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown group = %v", err)
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); err != nil {
		t.Fatal(err)
	}
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}
}

func TestSplitAndUndo(t *testing.T) {
	m := newMilks(t)
	g, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb, m.pc}, "Melk")
	before := m.e.dump()

	// one product leaves: the group keeps the other two
	res, err := m.e.s.Split(m.e.ctx, g.Group.ID, []int64{m.pc}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Group == nil || len(res.Group.Members) != 2 || m.e.groupOf(m.pc) != 0 || len(res.Dissolved) != 0 {
		t.Fatalf("split one = %+v", res)
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); err != nil {
		t.Fatal(err)
	}
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}

	// two leave: the group would have one member, so it is dissolved
	res, err = m.e.s.Split(m.e.ctx, g.Group.ID, []int64{m.pa, m.pb}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Group != nil || len(res.Dissolved) != 1 || m.e.groupOf(m.pa) != 0 || m.e.groupOf(m.pb) != 0 || m.e.groupOf(m.pc) != 0 {
		t.Fatalf("split leaving one = %+v", res)
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); err != nil {
		t.Fatal(err)
	}
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}

	// keep_together: the two that leave form a group of their own
	res, err = m.e.s.Split(m.e.ctx, g.Group.ID, []int64{m.pa, m.pb}, true, "AH and Dirk")
	if err != nil {
		t.Fatal(err)
	}
	if res.Group == nil || res.Group.Name != "AH and Dirk" || len(res.Group.Members) != 2 || res.Group.ID == g.Group.ID {
		t.Fatalf("split together = %+v", res)
	}
	if _, err := m.e.s.GetGroup(m.e.ctx, g.Group.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("the old group had one member left and must be dissolved")
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); err != nil {
		t.Fatal(err)
	}
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}

	if _, err := m.e.s.Split(m.e.ctx, g.Group.ID, []int64{m.pd}, false, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a product that is not a member = %v", err)
	}
	if _, err := m.e.s.Split(m.e.ctx, g.Group.ID, nil, false, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty split = %v", err)
	}
}

func TestDeleteGroupAndUndo(t *testing.T) {
	m := newMilks(t)
	g, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	before := m.e.dump()
	res, err := m.e.s.Delete(m.e.ctx, g.Group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.e.groupOf(m.pa) != 0 || m.e.groupOf(m.pb) != 0 {
		t.Fatal("products must be ungrouped")
	}
	var products int
	m.e.conn.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&products)
	if products != 4 {
		t.Fatalf("deleting a group must keep its products, have %d", products)
	}
	if _, err := m.e.s.Undo(m.e.ctx, res.OpID); err != nil {
		t.Fatal(err)
	}
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}
	if _, err := m.e.s.Delete(m.e.ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown = %v", err)
	}
}

func TestRename(t *testing.T) {
	m := newMilks(t)
	g, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	got, err := m.e.s.Rename(m.e.ctx, g.Group.ID, "  Halfvolle melk  ")
	if err != nil || got.Name != "Halfvolle melk" {
		t.Fatalf("rename = %+v %v", got, err)
	}
	if _, err := m.e.s.Rename(m.e.ctx, g.Group.ID, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank = %v", err)
	}
	if _, err := m.e.s.Rename(m.e.ctx, 999, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown = %v", err)
	}
}

// Merging two products in the catalogue must not take the survivor out of its group.
func TestProductMergeKeepsTheGroup(t *testing.T) {
	m := newMilks(t)
	dup := m.e.product("AH Halfvolle melk 1L", m.e.category("Dubbel"), 1000, "ml")
	g, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	// the grouped product disappears into dup, as the product merge does
	m.e.exec(`UPDATE products SET deleted_at = 't', merged_into_id = ? WHERE id = ?`, dup, m.pa)
	got, err := m.e.s.GetGroup(m.e.ctx, g.Group.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := memberIDs(&got)
	if len(ids) != 2 || ids[0] != m.pb && ids[1] != m.pb || ids[0] != dup && ids[1] != dup {
		t.Fatalf("members after merge = %v", ids)
	}
	// and a split takes the survivor out for real
	if _, err := m.e.s.Split(m.e.ctx, g.Group.ID, []int64{dup}, false, ""); err != nil {
		t.Fatal(err)
	}
	if m.e.groupOf(m.pa) != 0 {
		t.Fatal("the merged-away product must not keep the group alive")
	}
}

func TestSuggestionsAreNeverApplied(t *testing.T) {
	m := newMilks(t)
	sg, err := m.e.s.Suggestions(m.e.ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	// three house-brand milks at three shops: three pairs, nothing about the oil
	if len(sg) != 3 {
		t.Fatalf("suggestions = %+v", sg)
	}
	for _, s := range sg {
		if s.Score < SuggestMinScore || s.A.ProductID == m.pd || s.B.ProductID == m.pd || len(s.A.Shops) != 1 || len(s.B.Shops) != 1 {
			t.Fatalf("suggestion = %+v", s)
		}
	}
	if n, _ := m.e.s.SuggestionCount(m.e.ctx); n != 3 {
		t.Fatalf("count = %d", n)
	}
	// reading suggestions, planning and listing never group anything
	m.e.item("melk", m.pa, 1)
	m.e.plan()
	var grouped, groups int
	m.e.conn.QueryRow(`SELECT COUNT(*) FROM products WHERE group_id IS NOT NULL`).Scan(&grouped)
	m.e.conn.QueryRow(`SELECT COUNT(*) FROM product_groups`).Scan(&groups)
	if grouped != 0 || groups != 0 {
		t.Fatalf("something was grouped automatically: %d products, %d groups", grouped, groups)
	}
}

func TestSuggestionsRules(t *testing.T) {
	e := newEnv(t)
	a, b := e.shop("Albert Heijn"), e.shop("Dirk")
	dairy, other := e.category("Zuivel"), e.category("Overig")
	mk := func(name string, cat int64, size float64, unit string, shop int64) int64 {
		p := e.product(name, cat, size, unit)
		e.buy(shop, 3, p, 100)
		return p
	}
	ahMelk := mk("AH Halfvolle melk", dairy, 1000, "ml", a)
	dirkMelk := mk("Dirk halfvolle melk 1 L", dairy, 1000, "ml", b)
	mk("Dirk Volle melk", dairy, 1000, "ml", b)                // too different from the half-fat milk? (checked below)
	mk("Dirk halfvolle melk", other, 1000, "ml", b)            // other category
	mk("AH Halfvolle melk klein", dairy, 200, "ml", b)         // same name, 200 ml: still litres, so allowed
	mk("Albert Heijn Halfvolle melk 500g", dairy, 500, "g", b) // other base unit (kg)
	sg, _ := e.s.Suggestions(e.ctx, 50)
	has := func(x, y int64) bool {
		for _, s := range sg {
			if (s.A.ProductID == x && s.B.ProductID == y) || (s.A.ProductID == y && s.B.ProductID == x) {
				return true
			}
		}
		return false
	}
	if !has(ahMelk, dirkMelk) {
		t.Fatalf("AH and Dirk half-fat milk should be suggested: %+v", sg)
	}
	var cat, kg int64
	e.conn.QueryRow(`SELECT id FROM products WHERE name = 'Dirk halfvolle melk'`).Scan(&cat)
	e.conn.QueryRow(`SELECT id FROM products WHERE name LIKE 'Albert Heijn%'`).Scan(&kg)
	if has(ahMelk, cat) || has(ahMelk, kg) {
		t.Fatalf("different category or unit must not be suggested: %+v", sg)
	}
	// same shop only: no use for a price comparison
	e2 := newEnv(t)
	s1 := e2.shop("Albert Heijn")
	d2 := e2.category("Zuivel")
	x := e2.product("AH Halfvolle melk", d2, 1000, "ml")
	y := e2.product("Halfvolle melk bio", d2, 1000, "ml")
	e2.buy(s1, 1, x, 100)
	e2.buy(s1, 2, y, 100)
	if sg, _ := e2.s.Suggestions(e2.ctx, 50); len(sg) != 0 {
		t.Fatalf("products of one shop only: %+v", sg)
	}
}

func TestDenyHidesASuggestionAndUndoBringsItBack(t *testing.T) {
	m := newMilks(t)
	op, err := m.e.s.Deny(m.e.ctx, m.pb, m.pa) // order does not matter
	if err != nil || op == 0 {
		t.Fatal(err)
	}
	sg, _ := m.e.s.Suggestions(m.e.ctx, 50)
	if len(sg) != 2 {
		t.Fatalf("suggestions after deny = %d", len(sg))
	}
	for _, s := range sg {
		if (s.A.ProductID == m.pa && s.B.ProductID == m.pb) || (s.A.ProductID == m.pb && s.B.ProductID == m.pa) {
			t.Fatal("denied pair came back")
		}
	}
	// denying twice is harmless
	if _, err := m.e.s.Deny(m.e.ctx, m.pa, m.pb); err != nil {
		t.Fatal(err)
	}
	if _, err := m.e.s.Undo(m.e.ctx, op); err != nil {
		t.Fatal(err)
	}
	if sg, _ := m.e.s.Suggestions(m.e.ctx, 50); len(sg) != 3 {
		t.Fatalf("after undoing the deny: %d", len(sg))
	}
	if _, err := m.e.s.Deny(m.e.ctx, m.pa, m.pa); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self = %v", err)
	}
	if _, err := m.e.s.Deny(m.e.ctx, m.pa, 999); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown = %v", err)
	}
	g, _ := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	if _, err := m.e.s.Deny(m.e.ctx, m.pa, m.pb); !errors.Is(err, ErrConflict) {
		t.Fatalf("deny inside a group = %v", err)
	}
	_ = g
}

// Confirming a pair that was denied earlier removes the denial; undo puts it back.
func TestConfirmClearsADenial(t *testing.T) {
	m := newMilks(t)
	m.e.s.Deny(m.e.ctx, m.pa, m.pb)
	before := m.e.dump()
	res, err := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, "")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	m.e.conn.QueryRow(`SELECT COUNT(*) FROM product_group_denied`).Scan(&n)
	if n != 0 {
		t.Fatal("denial must be cleared")
	}
	m.e.s.Undo(m.e.ctx, res.OpID)
	if got := m.e.dump(); got != before {
		t.Fatalf("undo not exact:\n%s\nwant\n%s", got, before)
	}
}

// A kept-separate decision from the product review counts as a no too.
func TestKeepSeparateFromReviewHidesSuggestion(t *testing.T) {
	m := newMilks(t)
	a, b := ordered(m.pa, m.pb)
	m.e.exec(`INSERT INTO product_distinct (a_id, b_id, created_at) VALUES (?, ?, 't')`, a, b)
	if sg, _ := m.e.s.Suggestions(m.e.ctx, 50); len(sg) != 2 {
		t.Fatalf("suggestions = %d", len(sg))
	}
}

func TestSuggestionsLimitAndCache(t *testing.T) {
	m := newMilks(t)
	if sg, _ := m.e.s.Suggestions(m.e.ctx, 1); len(sg) != 1 {
		t.Fatalf("limit: %d", len(sg))
	}
	// cached for a minute, dropped when a group changes
	m.e.product("Jumbo Halfvolle melk", m.e.category("Zuivel2"), 1000, "ml")
	if _, err := m.e.s.Confirm(m.e.ctx, []int64{m.pa, m.pb}, ""); err != nil {
		t.Fatal(err)
	}
	sg, _ := m.e.s.Suggestions(m.e.ctx, 50)
	// pa+pb are one group now: pc pairs with the group once, not twice
	if len(sg) != 1 {
		t.Fatalf("suggestions after grouping = %+v", sg)
	}
	if sg[0].A.GroupID == nil && sg[0].B.GroupID == nil {
		t.Fatalf("the suggestion should point at the existing group: %+v", sg[0])
	}
}
