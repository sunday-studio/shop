package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"shop/internal/apitypes"
	"shop/internal/store"
)

// Phase C: product groups and GET /v1/list/plan over HTTP.

type planSeed struct {
	ahShop, dirkShop         int64
	ahMilk, dirkMilk, coffee int64
	milkItem, coffeeItem     int64
}

func (e *env) sql(q string, args ...any) int64 {
	e.t.Helper()
	res, err := e.conn.Exec(q, args...)
	if err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

var seedN int

func (e *env) buy(shop, product, days int64, cents int64) {
	e.t.Helper()
	seedN++
	at := store.TS(time.Now().AddDate(0, 0, -int(days)))
	rid := e.sql(`INSERT INTO receipts (shop_id, purchased_at, image_path, image_sha256, status, confirmed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'ok', 't', 't', 't')`, shop, at, fmt.Sprintf("plan%d", seedN), fmt.Sprintf("plan-sha%d", seedN))
	e.sql(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, line_total_cents, product_id)
		VALUES (?, 1, 'x', 'x', 'item', 1, 'pc', ?, ?)`, rid, cents, product)
}

func (e *env) seedPlan() planSeed {
	e.t.Helper()
	var x planSeed
	x.ahShop, x.dirkShop = e.shop("Albert Heijn").Id, e.shop("Dirk").Id
	cat := e.sql(`INSERT INTO categories (name, sort) VALUES ('Zuivel2', 5)`)
	mk := func(name string, size int, unit string) int64 {
		return e.sql(`INSERT INTO products (name, category_id, net_quantity, net_unit, created_at, updated_at) VALUES (?, ?, ?, ?, 't', 't')`, name, cat, size, unit)
	}
	x.ahMilk, x.dirkMilk = mk("AH Halfvolle melk", 1000, "ml"), mk("Dirk Halfvolle melk", 1000, "ml")
	x.coffee = mk("Douwe Egberts koffie", 250, "g")
	bread := mk("Volkoren brood", 800, "g")
	for _, d := range []int64{5, 12, 20} {
		e.buy(x.ahShop, x.ahMilk, d, 150)
	}
	for _, d := range []int64{3, 10} {
		e.buy(x.dirkShop, x.dirkMilk, d, 90)
	}
	e.buy(x.ahShop, x.coffee, 5, 300)
	e.buy(x.dirkShop, x.coffee, 3, 250)
	e.buy(x.ahShop, bread, 5, 100)
	e.buy(x.dirkShop, bread, 3, 120)
	x.milkItem = int64(e.addItem(map[string]any{"text": "melk", "product_id": x.ahMilk, "qty": 4})["id"].(float64))
	x.coffeeItem = int64(e.addItem(map[string]any{"text": "koffie", "product_id": x.coffee})["id"].(float64))
	e.addItem(map[string]any{"text": "brood", "product_id": bread})
	e.addItem(map[string]any{"text": "pepper"})
	return x
}

func TestListPlanOverHTTP(t *testing.T) {
	e := newEnv(t)
	x := e.seedPlan()

	// before the group is confirmed the milk cannot be compared and the plan asks the question
	rec := e.do("GET", "/v1/list/plan", nil)
	if rec.Code != 200 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	p := decodeAs[apitypes.ListPlan](t, rec)
	if p.Status != apitypes.ListPlanStatusOneShopFine || p.Summary.ComparedCount != 2 || p.Summary.NoPriceCount != 1 || p.Summary.ItemCount != 4 || len(p.Items) != 4 {
		t.Fatalf("plan before the group: %+v", p.Summary)
	}
	var milk apitypes.PlanItem
	for _, it := range p.Items {
		if it.ItemId == x.milkItem {
			milk = it
		}
	}
	if milk.PairConfirmation == nil || milk.PairConfirmation.ProductA != x.ahMilk || milk.PairConfirmation.ProductB != x.dirkMilk {
		t.Fatalf("pair confirmation = %+v", milk.PairConfirmation)
	}

	// the review queue knows about the suggestion
	q := decodeAs[apitypes.ReviewQueue](t, e.do("GET", "/v1/products/review-queue?limit=0", nil))
	if q.SameKindTotal == nil || *q.SameKindTotal != 1 {
		t.Fatalf("same_kind_total = %v", q.SameKindTotal)
	}
	sg := decodeAs[[]apitypes.ProductGroupSuggestion](t, e.do("GET", "/v1/product-groups/suggestions", nil))
	if len(sg) != 1 || sg[0].Score < 0.8 || sg[0].Reason != apitypes.SimilarNameOtherShop {
		t.Fatalf("suggestions = %+v", sg)
	}

	// yes, same kind
	rec = e.do("POST", "/v1/product-groups/confirm", map[string]any{"product_ids": []int64{x.ahMilk, x.dirkMilk}, "name": "Halfvolle melk"})
	if rec.Code != 200 {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	res := decodeAs[apitypes.ProductGroupResult](t, rec)
	if res.Group == nil || res.Group.Name != "Halfvolle melk" || len(res.Group.Members) != 2 || res.Group.ShopCount != 2 || res.OpId == 0 || res.DissolvedGroupIds == nil {
		t.Fatalf("result = %+v", res)
	}
	q = decodeAs[apitypes.ReviewQueue](t, e.do("GET", "/v1/products/review-queue?limit=0", nil))
	if *q.SameKindTotal != 0 {
		t.Fatalf("same_kind_total after confirm = %d", *q.SameKindTotal)
	}

	p = decodeAs[apitypes.ListPlan](t, e.do("GET", "/v1/list/plan", nil))
	s := p.Summary
	if p.Status != apitypes.ListPlanStatusSplit || s.GrossSavingCents != 290 || s.SavingCents != 40 || s.ComparedCount != 3 || s.ExtraStopCents != 250 || s.ExtraStops != 1 {
		t.Fatalf("plan after the group: %s %+v", p.Status, s)
	}
	if p.UsualShopId == nil || *p.UsualShopId != x.ahShop || len(p.Stores) != 2 || !p.Stores[0].IsUsual {
		t.Fatalf("stores = %+v", p.Stores)
	}
	if !p.ComputedAt.After(time.Now().Add(-time.Minute)) {
		t.Fatalf("computed_at = %v", p.ComputedAt)
	}
	for _, it := range p.Items {
		if it.ItemId == x.milkItem && (it.ShopId == nil || *it.ShopId != x.dirkShop || it.SavingCents != 240 || it.Basis == nil || *it.Basis != apitypes.PerL || it.Explanation == "") {
			t.Fatalf("milk = %+v", it)
		}
		if it.Text == "pepper" && (it.Status != apitypes.NoPrice || it.NoPriceReason == nil || *it.NoPriceReason != apitypes.NotLinked) {
			t.Fatalf("pepper = %+v", it)
		}
	}

	// what-if parameters are validated and do not stick
	for _, bad := range []string{"extra_stop_cents=-1", "extra_stop_cents=abc", "extra_stop_cents=2001", "max_stops=0", "max_stops=4"} {
		if rec := e.do("GET", "/v1/list/plan?"+bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	p = decodeAs[apitypes.ListPlan](t, e.do("GET", "/v1/list/plan?extra_stop_cents=400", nil))
	if p.Status != apitypes.ListPlanStatusOneShopFine || p.Summary.SplitSavingCents != 290 {
		t.Fatalf("what-if: %s %+v", p.Status, p.Summary)
	}
	if p = decodeAs[apitypes.ListPlan](t, e.do("GET", "/v1/list/plan", nil)); p.Status != apitypes.ListPlanStatusSplit {
		t.Fatal("what-if must not be saved")
	}

	// settings
	st := decodeAs[apitypes.ListPlanSettings](t, e.do("GET", "/v1/list/plan/settings", nil))
	if st.ExtraStopCents != 250 || st.MaxStops != 2 {
		t.Fatalf("defaults = %+v", st)
	}
	st = decodeAs[apitypes.ListPlanSettings](t, e.do("PATCH", "/v1/list/plan/settings", map[string]any{"extra_stop_cents": 500}))
	if st.ExtraStopCents != 500 || st.MaxStops != 2 {
		t.Fatalf("patched = %+v", st)
	}
	if rec := e.do("PATCH", "/v1/list/plan/settings", map[string]any{"max_stops": 9}); rec.Code != 400 {
		t.Fatalf("bad max_stops: %d", rec.Code)
	}
	if rec := e.do("PATCH", "/v1/list/plan/settings", map[string]any{"nope": 1}); rec.Code != 400 {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if p = decodeAs[apitypes.ListPlan](t, e.do("GET", "/v1/list/plan", nil)); p.Status != apitypes.ListPlanStatusOneShopFine || p.Summary.ExtraStopCents != 500 {
		t.Fatalf("saved cost not used: %s %+v", p.Status, p.Summary)
	}
}

func TestProductGroupsOverHTTP(t *testing.T) {
	e := newEnv(t)
	x := e.seedPlan()
	third := e.sql(`INSERT INTO products (name, category_id, net_quantity, net_unit, created_at, updated_at) SELECT 'Lidl Halfvolle melk', category_id, 1000, 'ml', 't', 't' FROM products WHERE id = ?`, x.ahMilk)

	// create, rename, list, get
	res := decodeAs[apitypes.ProductGroupResult](t, e.do("POST", "/v1/product-groups", map[string]any{"product_ids": []int64{x.ahMilk, x.dirkMilk}}))
	gid := res.Group.Id
	if rec := e.do("PATCH", fmt.Sprintf("/v1/product-groups/%d", gid), map[string]any{"name": "Melk"}); rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("PATCH", fmt.Sprintf("/v1/product-groups/%d", gid), map[string]any{"name": " "}); rec.Code != 400 {
		t.Fatalf("blank name: %d", rec.Code)
	}
	list := decodeAs[[]apitypes.ProductGroup](t, e.do("GET", "/v1/product-groups", nil))
	if len(list) != 1 || list[0].Name != "Melk" || list[0].BaseUnit != apitypes.ProductGroupBaseUnitL {
		t.Fatalf("list = %+v", list)
	}
	if rec := e.do("GET", fmt.Sprintf("/v1/product-groups/%d", gid), nil); rec.Code != 200 {
		t.Fatalf("get: %d", rec.Code)
	}
	if rec := e.do("GET", "/v1/product-groups/999", nil); rec.Code != 404 {
		t.Fatalf("missing: %d", rec.Code)
	}
	if rec := e.do("GET", "/v1/product-groups/abc", nil); rec.Code != 400 {
		t.Fatalf("bad id: %d", rec.Code)
	}
	for _, body := range []map[string]any{{"product_ids": []int64{x.ahMilk}}, {"product_ids": []int64{x.ahMilk, 9999}}, {"bogus": 1}} {
		if rec := e.do("POST", "/v1/product-groups", body); rec.Code != 400 {
			t.Fatalf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}

	// a second group, merged into the first, then undone
	g2 := decodeAs[apitypes.ProductGroupResult](t, e.do("POST", "/v1/product-groups", map[string]any{"product_ids": []int64{third, x.coffee}, "name": "Other"}))
	merge := decodeAs[apitypes.ProductGroupResult](t, e.do("POST", fmt.Sprintf("/v1/product-groups/%d/merge", gid), map[string]any{"into_group_id": g2.Group.Id}))
	if merge.Group == nil || merge.Group.Id != g2.Group.Id || len(merge.Group.Members) != 4 || len(merge.DissolvedGroupIds) != 1 {
		t.Fatalf("merge = %+v", merge)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/product-groups/%d/merge", gid), map[string]any{"into_group_id": g2.Group.Id}); rec.Code != 404 {
		t.Fatalf("merging a gone group: %d", rec.Code)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/product-groups/%d/merge", g2.Group.Id), map[string]any{"into_group_id": g2.Group.Id}); rec.Code != 400 {
		t.Fatalf("self merge: %d", rec.Code)
	}
	undo := decodeAs[apitypes.ProductGroupResult](t, e.do("POST", fmt.Sprintf("/v1/product-group-ops/%d/undo", merge.OpId), nil))
	if undo.Group == nil {
		t.Fatalf("undo = %+v", undo)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/product-group-ops/%d/undo", merge.OpId), nil); rec.Code != 409 {
		t.Fatalf("second undo: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/product-group-ops/999/undo", nil); rec.Code != 404 {
		t.Fatalf("unknown op: %d", rec.Code)
	}
	if n := len(decodeAs[[]apitypes.ProductGroup](t, e.do("GET", "/v1/product-groups", nil))); n != 2 {
		t.Fatalf("groups after undo = %d", n)
	}

	// split one product out of a two-member group: the group is dissolved
	split := decodeAs[apitypes.ProductGroupResult](t, e.do("POST", fmt.Sprintf("/v1/product-groups/%d/split", gid), map[string]any{"product_ids": []int64{x.dirkMilk}}))
	if split.Group != nil || len(split.DissolvedGroupIds) != 1 {
		t.Fatalf("split = %+v", split)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/product-groups/%d/split", g2.Group.Id), map[string]any{"product_ids": []int64{}}); rec.Code != 400 {
		t.Fatalf("empty split: %d", rec.Code)
	}
	e.do("POST", fmt.Sprintf("/v1/product-group-ops/%d/undo", split.OpId), nil)

	// delete and undo
	del := decodeAs[apitypes.ProductGroupResult](t, e.do("DELETE", fmt.Sprintf("/v1/product-groups/%d", g2.Group.Id), nil))
	if len(decodeAs[[]apitypes.ProductGroup](t, e.do("GET", "/v1/product-groups", nil))) != 1 {
		t.Fatal("delete")
	}
	e.do("POST", fmt.Sprintf("/v1/product-group-ops/%d/undo", del.OpId), nil)
	if len(decodeAs[[]apitypes.ProductGroup](t, e.do("GET", "/v1/product-groups", nil))) != 2 {
		t.Fatal("undo delete")
	}

	// deny: 409 inside a group, 400 for nonsense, 200 for a real pair, then undo
	if rec := e.do("POST", "/v1/product-groups/deny", map[string]any{"product_a_id": x.ahMilk, "product_b_id": x.dirkMilk}); rec.Code != 409 {
		t.Fatalf("deny inside a group: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/product-groups/deny", map[string]any{"product_a_id": x.ahMilk, "product_b_id": x.ahMilk}); rec.Code != 400 {
		t.Fatalf("deny self: %d", rec.Code)
	}
}

func TestDenyOverHTTPHidesTheSuggestion(t *testing.T) {
	e := newEnv(t)
	x := e.seedPlan()
	rec := e.do("POST", "/v1/product-groups/deny", map[string]any{"product_a_id": x.dirkMilk, "product_b_id": x.ahMilk})
	if rec.Code != 200 {
		t.Fatalf("deny: %d %s", rec.Code, rec.Body)
	}
	if sg := decodeAs[[]apitypes.ProductGroupSuggestion](t, e.do("GET", "/v1/product-groups/suggestions", nil)); len(sg) != 0 {
		t.Fatalf("denied pair suggested: %+v", sg)
	}
	p := decodeAs[apitypes.ListPlan](t, e.do("GET", "/v1/list/plan", nil))
	for _, it := range p.Items {
		if it.PairConfirmation != nil {
			t.Fatalf("denied pair still asked: %+v", it)
		}
	}
}
