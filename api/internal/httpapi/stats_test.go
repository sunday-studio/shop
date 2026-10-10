package httpapi_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"shop/internal/apitypes"
	"shop/internal/stats"
)

// statsFixture: two shops, milk bought 4× (3 AH, 1 Lidl), bananas once.
type statsFixture struct {
	e              *env
	ah, lidl       apitypes.Shop
	milk, bananas  apitypes.Product
	zuivel         int64
	r1, r2, r3, r4 apitypes.Receipt
}

func newStatsFixture(t *testing.T) *statsFixture {
	e := newEnv(t)
	f := &statsFixture{e: e, ah: e.shop("AH"), lidl: e.shop("Lidl")}
	for _, c := range decodeAs[[]apitypes.Category](t, e.do("GET", "/v1/categories", nil)) {
		if c.Name == "Zuivel" {
			f.zuivel = c.Id
		}
	}
	f.milk = e.product(map[string]any{"name": "Halfvolle melk", "category_id": f.zuivel, "net_quantity": 1500, "net_unit": "ml"})
	f.bananas = e.product(map[string]any{"name": "Bananen"})

	f.r1 = e.receipt(f.ah.Id, 1, "01-09-2026 10:00|", "MELK|1,29", "BANANEN|", "1,000 kg x 1,99|1,99", "TOTAAL|3,28")
	f.r2 = e.receipt(f.ah.Id, 2, "15-09-2026 10:00|", "MELK|1,39", "TOTAAL|1,39")
	f.r3 = e.receipt(f.lidl.Id, 3, "20-09-2026 10:00|", "HALFVOLLE MELK|1,19", "TOTAAL|1,19")
	f.r4 = e.receipt(f.ah.Id, 4, "01-10-2026 10:00|", "MELK|1,49", "BONUS MELK|-0,10", "TOTAAL|1,39")
	for _, r := range []apitypes.Receipt{f.r1, f.r2, f.r3, f.r4} {
		if r.Status != apitypes.ReceiptStatusOk {
			t.Fatalf("fixture receipt %d: %s %v", r.Id, r.Status, r.ReviewReasons)
		}
	}
	link := func(lineID, productID int64) {
		if rec := e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", lineID), map[string]any{"product_id": productID}); rec.Code != 200 {
			t.Fatalf("link: %d %s", rec.Code, rec.Body)
		}
	}
	link((*f.r1.Lines)[0].Id, f.milk.Id) // alias back-fills r2 and r4 (same text at AH)
	link((*f.r1.Lines)[1].Id, f.bananas.Id)
	link((*f.r3.Lines)[0].Id, f.milk.Id)
	return f
}

func TestSpend(t *testing.T) {
	f := newStatsFixture(t)
	e := f.e
	get := func(q string) apitypes.Spend {
		rec := e.do("GET", "/v1/stats/spend?"+q, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
		return decodeAs[apitypes.Spend](t, rec)
	}
	byKey := func(s apitypes.Spend) map[string]int64 {
		m := map[string]int64{}
		var sum int64
		for _, b := range s.Buckets {
			m[b.Key] = b.Cents
			sum += b.Cents
		}
		if sum != s.TotalCents {
			t.Errorf("%s: buckets sum %d != total %d", s.GroupBy, sum, s.TotalCents)
		}
		return m
	}

	month := get("from=2026-09-01&to=2026-10-31&group_by=month")
	if m := byKey(month); month.TotalCents != 725 || month.Receipts != 4 || m["2026-09"] != 586 || m["2026-10"] != 139 {
		t.Errorf("month: %+v", month)
	}
	shop := get("from=2026-09-01&to=2026-10-31&group_by=shop")
	if m := byKey(shop); m[fmt.Sprint(f.ah.Id)] != 606 || m[fmt.Sprint(f.lidl.Id)] != 119 || shop.Buckets[0].Label != "AH" {
		t.Errorf("shop: %+v", shop.Buckets)
	}
	cat := get("from=2026-09-01&to=2026-10-31&group_by=category")
	if m := byKey(cat); m[fmt.Sprint(f.zuivel)] != 526 || m["none"] != 199 {
		t.Errorf("category: %+v", cat.Buckets)
	}
	week := get("from=2026-09-01&to=2026-09-30&group_by=week")
	if len(week.Buckets) != 5 { // W36..W40 including empty weeks
		t.Errorf("weeks: %d %+v", len(week.Buckets), week.Buckets)
	}
	byKey(week)
	day := get(fmt.Sprintf("from=2026-09-01&to=2026-09-30&group_by=day&shop_id=%d", f.lidl.Id))
	if m := byKey(day); len(day.Buckets) != 30 || m["2026-09-20"] != 119 || day.TotalCents != 119 {
		t.Errorf("day/lidl: total=%d buckets=%d", day.TotalCents, len(day.Buckets))
	}
	if rec := e.do("GET", "/v1/stats/spend?group_by=year", nil); rec.Code != 400 {
		t.Errorf("bad group_by: %d", rec.Code)
	}
	if rec := e.do("GET", "/v1/stats/spend?from=2026-10-01&to=2026-09-01", nil); rec.Code != 400 {
		t.Errorf("reversed range: %d", rec.Code)
	}
}

func TestPriceHistoryCompareAndChanges(t *testing.T) {
	f := newStatsFixture(t)
	e := f.e

	pts := decodeAs[[]apitypes.PricePoint](t, e.do("GET", fmt.Sprintf("/v1/stats/products/%d/prices", f.milk.Id), nil))
	want := []int64{86, 93, 79, 93} // cents per litre; r4 is 1,49 − 0,10 bonus
	if len(pts) != 4 {
		t.Fatalf("points: %+v", pts)
	}
	for i, p := range pts {
		if p.UnitCents != want[i] || p.BaseUnit != "l" {
			t.Errorf("point %d = %d/%s, want %d/l", i, p.UnitCents, p.BaseUnit, want[i])
		}
	}
	if pts[3].PaidCents != 139 {
		t.Errorf("discount not applied: paid %d", pts[3].PaidCents)
	}
	ahOnly := decodeAs[[]apitypes.PricePoint](t, e.do("GET", fmt.Sprintf("/v1/stats/products/%d/prices?shop_id=%d", f.milk.Id, f.ah.Id), nil))
	if len(ahOnly) != 3 {
		t.Errorf("AH points: %d", len(ahOnly))
	}
	ban := decodeAs[[]apitypes.PricePoint](t, e.do("GET", fmt.Sprintf("/v1/stats/products/%d/prices", f.bananas.Id), nil))
	if len(ban) != 1 || ban[0].UnitCents != 199 || ban[0].BaseUnit != "kg" {
		t.Errorf("bananas: %+v", ban)
	}

	cmp := decodeAs[[]apitypes.ShopPrice](t, e.do("GET", fmt.Sprintf("/v1/stats/products/%d/compare", f.milk.Id), nil))
	if len(cmp) != 2 || cmp[0].ShopName != "Lidl" || !cmp[0].Cheapest || cmp[0].LatestUnitCents != 79 {
		t.Fatalf("compare: %+v", cmp)
	}
	if ah := cmp[1]; ah.LatestUnitCents != 93 || ah.MinUnitCents != 86 || ah.MaxUnitCents != 93 || ah.Purchases != 3 || ah.Cheapest {
		t.Errorf("compare AH: %+v", ah)
	}
	if rec := e.do("GET", "/v1/stats/products/999/compare", nil); rec.Code != 404 {
		t.Errorf("unknown product: %d", rec.Code)
	}

	changes := decodeAs[[]apitypes.PriceChange](t, e.do("GET", "/v1/stats/price-changes?since=2026-09-01&min_pct=5", nil))
	if len(changes) != 1 || changes[0].ShopName != "AH" || changes[0].OldUnitCents != 86 || changes[0].NewUnitCents != 93 || changes[0].Pct != 8.1 {
		t.Fatalf("changes: %+v", changes)
	}
	if none := decodeAs[[]apitypes.PriceChange](t, e.do("GET", "/v1/stats/price-changes?since=2026-09-01&min_pct=10", nil)); len(none) != 0 {
		t.Errorf("min_pct filter: %+v", none)
	}
}

func TestFrequentItems(t *testing.T) {
	f := newStatsFixture(t)
	st := stats.New(f.e.conn, time.UTC)
	// Milk: 4 purchases, gaps 14/5/11 days → median 11.
	notYet := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	items, err := st.FrequentItems(context.Background(), 365, 3, notYet)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ProductName != "Halfvolle melk" || items[0].Purchases != 4 ||
		items[0].MedianIntervalDays != 11 || items[0].Due {
		t.Fatalf("frequent (Oct 5): %+v", items)
	}
	due, _ := st.FrequentItems(context.Background(), 365, 3, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if len(due) != 1 || !due[0].Due {
		t.Errorf("frequent (Oct 20): %+v", due)
	}
	// Over HTTP the contract decodes.
	rec := f.e.do("GET", "/v1/stats/frequent-items?lookback_days=730&min_count=3", nil)
	if got := decodeAs[[]apitypes.FrequentItem](t, rec); len(got) != 1 {
		t.Errorf("http frequent: %+v", got)
	}
}

func TestShoppingList(t *testing.T) {
	f := newStatsFixture(t)
	e := f.e

	// Free text close to a product name links automatically and shows the cheapest shop.
	rec := e.do("POST", "/v1/list", map[string]any{"text": "halfvolle melk"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	milk := decodeAs[apitypes.ListItem](t, rec)
	if milk.ProductId == nil || *milk.ProductId != f.milk.Id || milk.Cheapest == nil || milk.Cheapest.ShopName != "Lidl" {
		t.Fatalf("auto-linked item: %+v", milk)
	}
	// Adding the same product again merges.
	rec = e.do("POST", "/v1/list", map[string]any{"product_id": f.milk.Id, "qty": 2})
	if merged := decodeAs[apitypes.ListItem](t, rec); rec.Code != 200 || merged.Id != milk.Id || *merged.Qty != 3 {
		t.Fatalf("merge: %d %+v", rec.Code, merged)
	}
	// Unknown text stays free text.
	paper := decodeAs[apitypes.ListItem](t, e.do("POST", "/v1/list", map[string]any{"text": "wc papier", "note": "8 rollen"}))
	if paper.ProductId != nil || *paper.Note != "8 rollen" {
		t.Errorf("free text: %+v", paper)
	}
	if rec := e.do("POST", "/v1/list", map[string]any{"text": "  "}); rec.Code != 400 {
		t.Errorf("empty: %d", rec.Code)
	}

	// Check → hidden by default, shown with include_checked, then cleared.
	if rec := e.do("POST", fmt.Sprintf("/v1/list/%d/check", paper.Id), nil); rec.Code != 200 {
		t.Fatalf("check: %d", rec.Code)
	}
	if open := decodeAs[[]apitypes.ListItem](t, e.do("GET", "/v1/list", nil)); len(open) != 1 || open[0].Id != milk.Id {
		t.Errorf("open: %+v", open)
	}
	if all := decodeAs[[]apitypes.ListItem](t, e.do("GET", "/v1/list?include_checked=true", nil)); len(all) != 2 || all[1].CheckedAt == nil {
		t.Errorf("all: %+v", all)
	}
	e.do("POST", fmt.Sprintf("/v1/list/%d/uncheck", paper.Id), nil)
	e.do("POST", fmt.Sprintf("/v1/list/%d/check", paper.Id), nil)
	cleared := decodeAs[map[string]int64](t, e.do("DELETE", "/v1/list/checked", nil))
	if cleared["removed"] != 1 {
		t.Errorf("clear: %+v", cleared)
	}

	upd := decodeAs[apitypes.ListItem](t, e.do("PATCH", fmt.Sprintf("/v1/list/%d", milk.Id), map[string]any{"text": "melk (2x 1,5L)", "product_id": f.milk.Id, "qty": 2}))
	if upd.Text != "melk (2x 1,5L)" || *upd.Qty != 2 {
		t.Errorf("update: %+v", upd)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/list/%d", milk.Id), nil); rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/list/%d", milk.Id), nil); rec.Code != 404 {
		t.Errorf("delete again: %d", rec.Code)
	}
}

func TestUnitPrice(t *testing.T) {
	nq := func(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: true} }
	nu := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	cases := []struct {
		paid    int64
		qty     float64
		unit    string
		netQ    sql.NullFloat64
		netU    sql.NullString
		want    int64
		wantFor string
	}{
		{179, 0.512, "kg", sql.NullFloat64{}, sql.NullString{}, 350, "kg"},
		{129, 1, "pc", nq(1500), nu("ml"), 86, "l"},
		{249, 1, "pc", nq(400), nu("g"), 623, "kg"},
		{178, 2, "pc", nq(500), nu("g"), 178, "kg"},
		{300, 1, "pc", nq(6), nu("pc"), 50, "pc"},
		{89, 1, "pc", sql.NullFloat64{}, sql.NullString{}, 89, "pc"},
	}
	for _, c := range cases {
		got, base := stats.UnitPrice(c.paid, c.qty, c.unit, c.netQ, c.netU)
		if got != c.want || base != c.wantFor {
			t.Errorf("UnitPrice(%d, %v %s, %v %v) = %d/%s, want %d/%s", c.paid, c.qty, c.unit, c.netQ, c.netU, got, base, c.want, c.wantFor)
		}
	}
}

func TestProductSummariesAndSort(t *testing.T) {
	f := newStatsFixture(t)
	e := f.e
	cheese := e.product(map[string]any{"name": "Kaas"}) // never bought

	recent := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products?sort=recent", nil))
	if len(recent) != 3 || recent[0].Id != f.milk.Id || recent[1].Id != f.bananas.Id || recent[2].Id != cheese.Id {
		t.Fatalf("recent order: %v", names(recent))
	}
	m := recent[0]
	if m.Purchases == nil || *m.Purchases != 4 || m.LastBoughtAt == nil || m.LastBoughtAt.Format("2006-01-02") != "2026-10-01" {
		t.Errorf("milk purchases/last: %v %v", m.Purchases, m.LastBoughtAt)
	}
	if m.Cheapest == nil || m.Cheapest.ShopName != "Lidl" || m.Cheapest.LatestUnitCents != 79 || !m.Cheapest.Cheapest {
		t.Errorf("milk cheapest: %+v", m.Cheapest)
	}
	if c := recent[2]; *c.Purchases != 0 || c.Cheapest != nil || c.LastBoughtAt != nil {
		t.Errorf("never bought: %+v", c)
	}
	byCount := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products?sort=purchases", nil))
	if byCount[0].Id != f.milk.Id || byCount[1].Id != f.bananas.Id {
		t.Errorf("purchases order: %v", names(byCount))
	}
	if one := decodeAs[apitypes.Product](t, e.do("GET", fmt.Sprintf("/v1/products/%d", f.milk.Id), nil)); one.Cheapest == nil || *one.Purchases != 4 {
		t.Errorf("single product summary: %+v", one)
	}
	if rec := e.do("GET", "/v1/products?sort=price", nil); rec.Code != 400 {
		t.Errorf("bad sort: %d", rec.Code)
	}
}

func TestSpendSplitAndProducts(t *testing.T) {
	f := newStatsFixture(t)
	e := f.e
	get := func(q string) apitypes.Spend {
		rec := e.do("GET", "/v1/stats/spend?from=2026-09-01&to=2026-10-31&"+q, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
		return decodeAs[apitypes.Spend](t, rec)
	}
	parts := func(b apitypes.SpendBucket) map[string]int64 {
		m := map[string]int64{}
		var sum int64
		for _, p := range *b.Split {
			m[p.Key] = p.Cents
			sum += p.Cents
		}
		if sum != b.Cents {
			t.Errorf("bucket %s: split sums to %d, cents %d", b.Key, sum, b.Cents)
		}
		return m
	}

	// #10: months stacked by category; stacks add up to the bar.
	cat := get("group_by=month&split_by=category")
	z := fmt.Sprint(f.zuivel)
	if sep := parts(cat.Buckets[0]); cat.Buckets[0].Key != "2026-09" || sep[z] != 387 || sep["none"] != 199 {
		t.Errorf("september by category: %+v", *cat.Buckets[0].Split)
	}
	if oct := parts(cat.Buckets[1]); oct[z] != 139 {
		t.Errorf("october by category: %+v", oct)
	}
	shop := get("group_by=month&split_by=shop")
	if sep := parts(shop.Buckets[0]); sep[fmt.Sprint(f.ah.Id)] != 467 || sep[fmt.Sprint(f.lidl.Id)] != 119 {
		t.Errorf("september by shop: %+v", sep)
	}

	// #11: spend per product with quantity, optionally one category.
	prod := get("group_by=product")
	if len(prod.Buckets) != 2 || prod.Buckets[0].Label != "Halfvolle melk" || prod.Buckets[0].Cents != 526 ||
		*prod.Buckets[0].Qty != 4 || *prod.Buckets[0].Unit != "pc" {
		t.Fatalf("by product: %+v", prod.Buckets)
	}
	if b := prod.Buckets[1]; b.Label != "Bananen" || *b.Qty != 1 || *b.Unit != "kg" || b.Cents != 199 {
		t.Errorf("bananas: %+v", b)
	}
	if only := get(fmt.Sprintf("group_by=product&category_id=%d", f.zuivel)); len(only.Buckets) != 1 || only.Buckets[0].Label != "Halfvolle melk" {
		t.Errorf("category filter: %+v", only.Buckets)
	}

	for _, bad := range []string{"group_by=shop&split_by=category", "group_by=month&split_by=product", "group_by=month&category_id=1"} {
		if rec := e.do("GET", "/v1/stats/spend?"+bad, nil); rec.Code != 400 {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
}
