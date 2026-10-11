package listplan

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"shop/internal/stats"
)

func TestUnitPriceMatchesStats(t *testing.T) {
	nf := func(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: f > 0} }
	ns := func(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
	cases := []struct {
		paid int64
		qty  float64
		unit string
		nq   float64
		nu   string
		want int64
		base string
		know bool
	}{
		{150, 1, "pc", 1000, "ml", 150, "l", true},
		{90, 1, "pc", 500, "ml", 180, "l", true},
		{300, 2, "pc", 250, "g", 600, "kg", true},
		{498, 0.83, "kg", 0, "", 600, "kg", true},
		{99, 1, "pc", 6, "pc", 17, "pc", true}, // 16.5 rounds half away from zero
		{239, 1, "pc", 0, "", 239, "pc", false},
		{239, 0, "pc", 0, "", 239, "pc", false},
	}
	for _, c := range cases {
		got, base, known, _ := unitPrice(c.paid, c.qty, c.unit, nf(c.nq), ns(c.nu))
		sg, sb := stats.UnitPrice(c.paid, c.qty, c.unit, nf(c.nq), ns(c.nu))
		if got != c.want || base != c.base || known != c.know {
			t.Errorf("%+v: got %d %s known=%v", c, got, base, known)
		}
		if got != sg || base != sb {
			t.Errorf("%+v: differs from stats.UnitPrice (%d %s)", c, sg, sb)
		}
	}
}

func TestMedian(t *testing.T) {
	if medianOf([]int64{130, 100, 120}) != 120 || medianOf([]int64{100, 101}) != 101 || medianOf([]int64{7}) != 7 {
		t.Fatal("median")
	}
}

// Prices are the median of the last 3 purchases in the window, after discounts, from ok receipts that have a
// shop; receipts under review, without a shop or at a deleted shop do not count.
func TestPriceSelection(t *testing.T) {
	e := newEnv(t)
	a, b := e.shop("Albert Heijn"), e.shop("Dirk")
	cat := e.category("Voorraad")
	rice := e.product("Rijst", cat, 1000, "g")
	e.buy(b, 2, e.product("Other", cat, 0, ""), 100) // Dirk has a receipt, so it is a candidate
	for i, c := range []struct {
		days  int
		total int64
	}{{50, 400}, {40, 440}, {30, 480}, {20, 520}} {
		_ = i
		e.buy(a, c.days, rice, c.total)
	}
	// the newest purchase had a 120 cent discount linked to it
	rid := e.receipt(a, 10, "ok")
	l := e.line(rid, rice, 560, 1, "pc")
	e.discount(rid, l, -120)
	// noise that must not count: under review, no shop, deleted shop, zero paid
	e.line(e.receipt(a, 1, "needs_review"), rice, 1, 1, "pc")
	e.line(e.receipt(0, 1, "ok"), rice, 2, 1, "pc")
	gone := e.shop("Gone")
	e.line(e.receipt(gone, 1, "ok"), rice, 3, 1, "pc")
	e.exec(`UPDATE shops SET deleted_at = 't' WHERE id = ?`, gone)
	e.line(e.receipt(a, 1, "ok"), rice, 0, 1, "pc")
	it := e.item("rijst", rice, 1)

	p := e.plan()
	pi := p.item(it)
	if len(pi.Prices) != 1 || pi.Prices[0].ShopID != a {
		t.Fatalf("prices = %+v", pi.Prices)
	}
	got := pi.Prices[0]
	// last 3 per kg: 480, 520, 440 (560-120) -> median 480
	if got.UnitCents != 480 || got.BaseUnit != "kg" || len(got.Points) != 3 || got.Purchases != 5 {
		t.Fatalf("price = %+v", got)
	}
	if got.Points[0].PaidCents != 440 || got.Points[1].PaidCents != 520 || got.Points[2].PaidCents != 480 {
		t.Fatalf("points not newest first, after discount: %+v", got.Points)
	}
	if pi.Status != ItemOnlyHere || !pi.PackSizeKnown || pi.Basis != "per_kg" {
		t.Fatalf("item = %+v", pi)
	}
	if pi.EstimateCents == nil || *pi.EstimateCents != 480 { // a 1 kg pack
		t.Fatalf("estimate = %v", pi.EstimateCents)
	}
	if !reflect.DeepEqual(pi.Confidence, []string{ConfNotEnough}) {
		t.Fatalf("confidence = %v", pi.Confidence)
	}
}

// A house brand per litre against a different pack size: the comparison is per litre, not per pack.
func TestPerUnitNotPerPack(t *testing.T) {
	e := newEnv(t)
	a, b := e.shop("Albert Heijn"), e.shop("Dirk")
	cat := e.category("Zuivel")
	litre := e.product("AH Halfvolle melk", cat, 1000, "ml")
	half := e.product("Dirk Halfvolle melk", cat, 500, "ml")
	e.buy(a, 5, litre, 150)
	e.buy(a, 9, litre, 150)
	e.buy(a, 10, e.product("Filler", cat, 0, ""), 100) // A has the most receipts: the usual shop
	e.buy(b, 3, half, 90)                              // 90 for half a litre is 180 per litre: dearer than the litre pack
	e.buy(b, 6, half, 90)
	if _, err := e.s.Confirm(e.ctx, []int64{litre, half}, ""); err != nil {
		t.Fatal(err)
	}
	it := e.item("melk", litre, 2)
	pi := e.plan().item(it)
	if pi.BaseUnit != "l" || len(pi.Prices) != 2 {
		t.Fatalf("item = %+v", pi)
	}
	byShop := map[int64]ShopPrice{}
	for _, p := range pi.Prices {
		byShop[p.ShopID] = p
	}
	if byShop[a].UnitCents != 150 || byShop[b].UnitCents != 180 {
		t.Fatalf("per litre prices = %d, %d", byShop[a].UnitCents, byShop[b].UnitCents)
	}
	if pi.Prices[0].ShopID != a {
		t.Fatal("cheapest per litre must come first, not the cheapest pack")
	}
	if !contains(pi.Confidence, ConfDifferentPack) {
		t.Fatalf("1 l and 0.5 l packs differ by more than 20%%: %v", pi.Confidence)
	}
	if pi.EstimateCents == nil || *pi.EstimateCents != 300 { // 2 packs of 1 l at 150 per l
		t.Fatalf("estimate = %v", deref(pi.EstimateCents))
	}
}

// No pack size: compared per piece, and the response says so.
func TestPerPieceWhenPackSizeUnknown(t *testing.T) {
	e := newEnv(t)
	a, b := e.shop("Albert Heijn"), e.shop("Dirk")
	cat := e.category("Overig")
	x := e.product("AH Kaarsen", cat, 0, "")
	y := e.product("Dirk Kaarsen", cat, 0, "")
	e.buy(a, 4, x, 300)
	e.buy(b, 2, y, 200)
	e.buy(a, 3, e.product("Pad", cat, 0, ""), 100)
	e.buy(b, 5, e.product("Pad2", cat, 0, ""), 100)
	if _, err := e.s.Confirm(e.ctx, []int64{x, y}, ""); err != nil {
		t.Fatal(err)
	}
	it := e.item("kaarsen", x, 3)
	p := e.plan(Options{ExtraStopCents: ip(0)})
	pi := p.item(it)
	if pi.Basis != "per_piece" || pi.BaseUnit != "pc" || pi.PackSizeKnown {
		t.Fatalf("item = %+v", pi)
	}
	if !contains(pi.Confidence, ConfDifferentPack) {
		t.Fatalf("confidence = %v", pi.Confidence)
	}
	if !strings.Contains(pi.Explanation, "Pack size unknown") {
		t.Fatalf("explanation = %q", pi.Explanation)
	}
	if p.Summary.PerPieceCount != 1 {
		t.Fatalf("per_piece_count = %d", p.Summary.PerPieceCount)
	}
	if p.Basis.PiecesNote == "" {
		t.Fatal("basis note missing")
	}
}

// Prices in different base units are never mixed.
func TestUnitsAreNotMixed(t *testing.T) {
	e := newEnv(t)
	a, b := e.shop("Albert Heijn"), e.shop("Dirk")
	cat := e.category("Voorraad")
	sized := e.product("Pasta AH", cat, 500, "g")
	unsized := e.product("Pasta Dirk", cat, 0, "")
	e.buy(a, 4, sized, 120)
	e.buy(a, 8, sized, 120)
	e.buy(b, 3, unsized, 80)
	if _, err := e.s.Confirm(e.ctx, []int64{sized, unsized}, ""); err != nil {
		t.Fatal(err)
	}
	id := e.item("pasta", sized, 1)
	pi := e.plan().item(id)
	if pi.BaseUnit != "kg" || len(pi.Prices) != 1 || pi.Prices[0].ShopID != a {
		t.Fatalf("only the per-kg prices may be used: %+v", pi.Prices)
	}
	if !contains(pi.Confidence, ConfDifferentPack) || !strings.Contains(pi.Explanation, "another unit") {
		t.Fatalf("mismatch must be flagged: %v %q", pi.Confidence, pi.Explanation)
	}
}

func TestStalePrices(t *testing.T) {
	e := newEnv(t)
	a := e.shop("Albert Heijn")
	veg := e.category("Groente & fruit")
	dry := e.category("Voorraad")
	e.buy(a, 1, e.product("Anything", dry, 0, ""), 100) // a recent receipt: A is the usual shop
	cases := []struct {
		name string
		cat  int64
		days int
		want string
	}{
		{"produce 40 days", veg, 40, "stale"},
		{"produce 25 days", veg, 25, ""},
		{"dry goods 40 days", dry, 40, ""},
		{"dry goods 100 days", dry, 100, "stale"},
	}
	for _, c := range cases {
		p := e.product(c.name, c.cat, 0, "")
		e.buy(a, c.days, p, 200)
		id := e.item(c.name, p, 1)
		pi := e.plan().item(id)
		if c.want == "stale" {
			if pi.Status != ItemNoPrice || pi.NoPriceReason != "stale" || pi.LastSeen == nil {
				t.Errorf("%s: %+v", c.name, pi)
			}
			if !strings.Contains(pi.Explanation, "old") {
				t.Errorf("%s: explanation %q", c.name, pi.Explanation)
			}
		} else if pi.Status == ItemNoPrice {
			t.Errorf("%s: should be priced: %+v", c.name, pi)
		}
	}
}

func TestItemsWithoutPrice(t *testing.T) {
	e := newEnv(t)
	a := e.shop("Albert Heijn")
	cat := e.category("Voorraad")
	e.buy(a, 1, e.product("Anything", cat, 0, ""), 100)
	never := e.product("Never bought", cat, 0, "")
	free := e.item("pepper", 0, 0)
	neverItem := e.item("never", never, 1)
	p := e.plan()
	if f := p.item(free); f.Status != ItemNoPrice || f.NoPriceReason != "not_linked" || f.UnitCents != nil || f.EstimateCents != nil {
		t.Fatalf("free text: %+v", f)
	}
	if n := p.item(neverItem); n.NoPriceReason != "never_bought" {
		t.Fatalf("never bought: %+v", n)
	}
	if p.Summary.NoPriceCount != 2 || p.Summary.ItemCount != 2 || p.Summary.PricedCount != 0 {
		t.Fatalf("summary = %+v", p.Summary)
	}
	if p.Status != StatusNotEnoughData || p.Summary.Confidence != "none" || p.Summary.SavingCents != 0 {
		t.Fatalf("no data must not promise anything: %+v", p.Summary)
	}
	// no-price items stay with the usual shop and are counted as unpriced there
	if len(p.Stores) != 1 || p.Stores[0].ItemCount != 2 || p.Stores[0].UnpricedCount != 2 || p.Stores[0].EstimateCents != 0 {
		t.Fatalf("stores = %+v", p.Stores)
	}
}

func TestEmptyListAndNoReceipts(t *testing.T) {
	e := newEnv(t)
	p := e.plan()
	if p.Status != StatusNotEnoughData || p.UsualShopID != nil || len(p.Stores) != 0 || p.Items == nil || p.Stores == nil {
		t.Fatalf("plan = %+v", p)
	}
	e.item("milk", 0, 0)
	p = e.plan()
	if p.Items[0].ShopID != nil || p.Items[0].NoPriceReason != "not_linked" {
		t.Fatalf("item = %+v", p.Items[0])
	}
}

// tradeOff builds: shop A (usual) and shop B; house-brand milk cheaper at B, coffee cheaper at B, bread
// cheaper at A. By hand, per item at A -> B: milk 4 x 1 l: 6.00 -> 3.60 (2.40), coffee 250 g: 3.00 -> 2.50
// (0.50), bread stays. Gross saving for the split is 2.90.
type tradeOff struct {
	e                    *env
	a, b                 int64
	milkA, milkB         int64
	coffee, bread        int64
	milkItem, coffeeItem int64
	breadItem            int64
}

func newTradeOff(t *testing.T, group bool) *tradeOff {
	e := newEnv(t)
	x := &tradeOff{e: e}
	x.a, x.b = e.shop("Albert Heijn"), e.shop("Dirk")
	dairy, dry := e.category("Zuivel"), e.category("Voorraad")
	x.milkA = e.product("AH Halfvolle melk", dairy, 1000, "ml")
	x.milkB = e.product("Dirk Halfvolle melk", dairy, 1000, "ml")
	x.coffee = e.product("Douwe Egberts koffie", dry, 250, "g")
	x.bread = e.product("Volkoren brood", dry, 800, "g")
	for _, d := range []int{5, 12, 20} {
		e.buy(x.a, d, x.milkA, 150)
	}
	for _, d := range []int{3, 10} {
		e.buy(x.b, d, x.milkB, 90)
	}
	e.buy(x.a, 5, x.coffee, 300)
	e.buy(x.b, 3, x.coffee, 250)
	e.buy(x.a, 5, x.bread, 100)
	e.buy(x.b, 3, x.bread, 120)
	if group {
		if _, err := e.s.Confirm(e.ctx, []int64{x.milkA, x.milkB}, "Halfvolle melk"); err != nil {
			t.Fatal(err)
		}
	}
	x.milkItem = e.item("melk", x.milkA, 4)
	x.coffeeItem = e.item("koffie", x.coffee, 1)
	x.breadItem = e.item("brood", x.bread, 1)
	return x
}

func TestPlanSplitsWhenWorthTheExtraStop(t *testing.T) {
	x := newTradeOff(t, true)
	p := x.e.plan()
	if p.Status != StatusSplit || p.UsualShopID == nil || *p.UsualShopID != x.a {
		t.Fatalf("status = %s usual = %v", p.Status, p.UsualShopID)
	}
	s := p.Summary
	if s.GrossSavingCents != 290 || s.SavingCents != 40 || s.ExtraStops != 1 || s.ExtraStopCents != 250 || s.ExtraStopTotalCents != 250 || s.OneShopFine {
		t.Fatalf("summary = %+v", s)
	}
	if s.ComparedCount != 3 || s.PricedCount != 3 || s.NoPriceCount != 0 || s.Coverage != 1 {
		t.Fatalf("coverage = %+v", s)
	}
	// 6.00 + 3.00 + 1.00 at A alone; 3.60 + 2.50 + 1.00 in the plan
	if s.BaselineCents != 1000 || s.PlanCents != 710 {
		t.Fatalf("baseline/plan = %d/%d", s.BaselineCents, s.PlanCents)
	}
	if s.Confidence != "medium" {
		t.Fatalf("confidence = %s", s.Confidence)
	}
	milk, coffee, bread := p.item(x.milkItem), p.item(x.coffeeItem), p.item(x.breadItem)
	if *milk.ShopID != x.b || milk.SavingCents != 240 || *milk.EstimateCents != 360 || *milk.UnitCents != 90*1 {
		t.Fatalf("milk = %+v", milk)
	}
	if *coffee.ShopID != x.b || coffee.SavingCents != 50 || *coffee.EstimateCents != 250 || *coffee.UnitCents != 1000 {
		t.Fatalf("coffee = %+v", coffee)
	}
	if *bread.ShopID != x.a || bread.SavingCents != 0 || *bread.EstimateCents != 100 || bread.Status != ItemCompared {
		t.Fatalf("bread = %+v", bread)
	}
	if len(milk.OtherPrices) != 1 || milk.OtherPrices[0].ShopID != x.a || milk.OtherPrices[0].UnitCents != 150 {
		t.Fatalf("milk other prices = %+v", milk.OtherPrices)
	}
	if milk.ProductGroupID == nil {
		t.Fatal("milk should report its product group")
	}
	if len(p.Stores) != 2 || !p.Stores[0].IsUsual || p.Stores[0].ShopID != x.a || p.Stores[0].ItemCount != 1 || p.Stores[0].EstimateCents != 100 ||
		p.Stores[1].ShopID != x.b || p.Stores[1].ItemCount != 2 || p.Stores[1].EstimateCents != 610 {
		t.Fatalf("stores = %+v", p.Stores)
	}
	if !strings.Contains(milk.Explanation, "Cheaper at Dirk") || !strings.Contains(milk.Explanation, "€0.90 per l") || !strings.Contains(milk.Explanation, "€1.50 per l") {
		t.Fatalf("explanation = %q", milk.Explanation)
	}
	if p.ComputedAt.IsZero() || p.Rev < 0 {
		t.Fatal("computed_at missing")
	}
}

func TestPlanSingleShopWhenSplitCostsMoreThanItSaves(t *testing.T) {
	x := newTradeOff(t, true)
	for _, c := range []struct {
		extra int64
		split bool
		net   int64
	}{{0, true, 290}, {250, true, 40}, {289, true, 1}, {290, false, 0}, {400, false, 0}} {
		p := x.e.plan(Options{ExtraStopCents: ip(c.extra)})
		if (p.Status == StatusSplit) != c.split {
			t.Fatalf("extra %d: status %s", c.extra, p.Status)
		}
		if p.Summary.SavingCents != c.net {
			t.Fatalf("extra %d: saving %d, want %d", c.extra, p.Summary.SavingCents, c.net)
		}
		if !c.split {
			s := p.Summary
			if !s.OneShopFine || s.SplitSavingCents != 290 || s.GrossSavingCents != 0 || s.ExtraStops != 0 {
				t.Fatalf("extra %d: summary %+v", c.extra, s)
			}
			for _, it := range p.Items {
				if *it.ShopID != x.a || it.SavingCents != 0 {
					t.Fatalf("one shop is fine: everything stays at the usual shop: %+v", it)
				}
			}
			if len(p.Stores) != 1 {
				t.Fatalf("stores = %+v", p.Stores)
			}
		}
	}
	// max_stops = 1 never splits, whatever the cost
	p := x.e.plan(Options{ExtraStopCents: ip(0), MaxStops: ii(1)})
	if p.Status != StatusOneShopFine || p.Summary.SavingCents != 0 {
		t.Fatalf("max_stops 1: %s %+v", p.Status, p.Summary)
	}
}

func TestSettingsDriveThePlan(t *testing.T) {
	x := newTradeOff(t, true)
	st, err := x.e.s.Settings(x.e.ctx)
	if err != nil || st.ExtraStopCents != 250 || st.MaxStops != 2 {
		t.Fatalf("defaults = %+v %v", st, err)
	}
	st, err = x.e.s.UpdateSettings(x.e.ctx, ip(1000), nil)
	if err != nil || st.ExtraStopCents != 1000 || st.MaxStops != 2 {
		t.Fatalf("update = %+v %v", st, err)
	}
	if p := x.e.plan(); p.Status != StatusOneShopFine || p.Summary.ExtraStopCents != 1000 {
		t.Fatalf("stored cost not used: %s %+v", p.Status, p.Summary)
	}
	// a what-if call does not change the stored value
	if p := x.e.plan(Options{ExtraStopCents: ip(0)}); p.Status != StatusSplit {
		t.Fatalf("what-if: %s", p.Status)
	}
	if st, _ := x.e.s.Settings(x.e.ctx); st.ExtraStopCents != 1000 {
		t.Fatal("what-if must not be saved")
	}
	for _, bad := range []struct {
		e *int64
		m *int
	}{{ip(-1), nil}, {ip(MaxExtraStopCents + 1), nil}, {nil, ii(0)}, {nil, ii(4)}} {
		if _, err := x.e.s.UpdateSettings(x.e.ctx, bad.e, bad.m); err == nil {
			t.Fatalf("accepted %v %v", bad.e, bad.m)
		}
	}
	if _, err := x.e.s.Plan(x.e.ctx, Options{ExtraStopCents: ip(-5)}); err == nil {
		t.Fatal("plan accepted a negative stop cost")
	}
}

// Without the confirmed group the house-brand milk has a price at one shop only, so nothing about it is
// counted, and the plan asks whether the two milks are the same kind of item.
func TestUnconfirmedHouseBrandsAreNotCompared(t *testing.T) {
	x := newTradeOff(t, false)
	p := x.e.plan()
	milk := p.item(x.milkItem)
	if milk.Status != ItemOnlyHere || len(milk.Prices) != 1 || milk.SavingCents != 0 {
		t.Fatalf("milk = %+v", milk)
	}
	if milk.Pair == nil || milk.Pair.ProductA != x.milkA || milk.Pair.ProductB != x.milkB || milk.Pair.ShopA != "Albert Heijn" || milk.Pair.ShopB != "Dirk" {
		t.Fatalf("pair confirmation = %+v", milk.Pair)
	}
	if !contains(milk.Confidence, ConfNotEnough) {
		t.Fatalf("confidence = %v", milk.Confidence)
	}
	// only coffee and bread are comparable; coffee alone saves 0.50, less than a stop
	s := p.Summary
	if s.ComparedCount != 2 || p.Status != StatusOneShopFine || s.SplitSavingCents != 50 {
		t.Fatalf("summary = %+v status %s", s, p.Status)
	}
	// confirming it changes the answer
	if _, err := x.e.s.Confirm(x.e.ctx, []int64{x.milkA, x.milkB}, ""); err != nil {
		t.Fatal(err)
	}
	p = x.e.plan()
	if p.Status != StatusSplit || p.item(x.milkItem).Pair != nil || p.item(x.milkItem).Status != ItemCompared {
		t.Fatalf("after confirm: %s %+v", p.Status, p.item(x.milkItem))
	}
}

// Savings are counted only on items with a price at both shops.
func TestSavingsOnlyWhereBothShopsHavePrices(t *testing.T) {
	x := newTradeOff(t, true)
	dry := x.e.category("Overig")
	onlyB := x.e.product("Only at Dirk", dry, 500, "g")
	x.e.buy(x.b, 2, onlyB, 10) // absurdly cheap, but unknown at the usual shop
	onlyA := x.e.product("Only at AH", dry, 500, "g")
	x.e.buy(x.a, 2, onlyA, 500)
	itB, itA := x.e.item("only b", onlyB, 1), x.e.item("only a", onlyA, 1)
	p := x.e.plan()
	b := p.item(itB)
	if b.Status != ItemOnlyElsewhere || *b.ShopID != x.a || b.UnitCents != nil || b.EstimateCents != nil || b.SavingCents != 0 {
		t.Fatalf("only at B: %+v", b)
	}
	if len(b.OtherPrices) != 1 || b.OtherPrices[0].ShopID != x.b {
		t.Fatalf("its price at the other shop must still be shown: %+v", b.OtherPrices)
	}
	a := p.item(itA)
	if a.Status != ItemOnlyHere || a.SavingCents != 0 || *a.ShopID != x.a {
		t.Fatalf("only at A: %+v", a)
	}
	// the headline is unchanged: 290 gross, 40 net, still 3 compared of 5
	s := p.Summary
	if s.GrossSavingCents != 290 || s.SavingCents != 40 || s.ComparedCount != 3 || s.ItemCount != 5 || s.PricedCount != 5 {
		t.Fatalf("summary = %+v", s)
	}
	if s.Coverage != 0.6 {
		t.Fatalf("coverage = %v", s.Coverage)
	}
	// the usual shop has the unpriced one in its section
	if p.Stores[0].UnpricedCount != 1 {
		t.Fatalf("stores = %+v", p.Stores)
	}
}

func TestNotEnoughDataToCompare(t *testing.T) {
	e := newEnv(t)
	a, b := e.shop("Albert Heijn"), e.shop("Dirk")
	cat := e.category("Voorraad")
	one := e.product("Koffie", cat, 250, "g")
	e.buy(a, 4, one, 900)
	e.buy(b, 3, one, 100) // a huge difference, but a single item
	e.buy(a, 5, e.product("Other", cat, 0, ""), 100)
	it := e.item("koffie", one, 1)
	p := e.plan(Options{ExtraStopCents: ip(0)})
	if p.Status != StatusNotEnoughData || p.Summary.SavingCents != 0 || p.Summary.Confidence != "none" || p.Summary.ComparedCount != 1 {
		t.Fatalf("one comparable item must not promise a saving: %s %+v", p.Status, p.Summary)
	}
	if *p.item(it).ShopID != a {
		t.Fatal("everything stays at the usual shop")
	}
	// a second comparable item is enough to say something
	two := e.product("Thee", cat, 100, "g")
	e.buy(a, 4, two, 500)
	e.buy(b, 3, two, 100)
	e.item("thee", two, 1)
	p = e.plan(Options{ExtraStopCents: ip(0)})
	if p.Status != StatusSplit || p.Summary.Confidence != "low" {
		t.Fatalf("status %s conf %s", p.Status, p.Summary.Confidence)
	}
}

// The second shop must beat the cost of its own stop: a third shop is added only when it pays.
func TestMaxStopsAndThreeShops(t *testing.T) {
	e := newEnv(t)
	a, b, c := e.shop("Albert Heijn"), e.shop("Dirk"), e.shop("Lidl")
	cat := e.category("Voorraad")
	// A has the most receipts
	for i := 0; i < 3; i++ {
		e.buy(a, 2+i, e.product("filler", cat, 0, ""), 100)
	}
	mk := func(name string, pa, pb, pc int64) int64 {
		p := e.product(name, cat, 1000, "g")
		e.buy(a, 6, p, pa)
		e.buy(b, 7, p, pb)
		e.buy(c, 8, p, pc)
		e.item(name, p, 1)
		return p
	}
	mk("rice", 1000, 700, 1000)  // B saves 300
	mk("pasta", 1000, 1000, 600) // C saves 400
	mk("flour", 1000, 1000, 1000)
	// extra stop 350: B alone nets -50, C alone 50; both: 700-700=0 -> best is C alone (net 50)
	p := e.plan(Options{ExtraStopCents: ip(350), MaxStops: ii(3)})
	if p.Status != StatusSplit || p.Summary.SavingCents != 50 || p.Summary.ExtraStops != 1 || len(p.Stores) != 2 || p.Stores[1].ShopID != c {
		t.Fatalf("350: %s %+v stores %+v", p.Status, p.Summary, p.Stores)
	}
	// extra stop 100: B and C together: 700 - 200 = 500 beats either alone (200 / 300)
	p = e.plan(Options{ExtraStopCents: ip(100), MaxStops: ii(3)})
	if p.Status != StatusSplit || p.Summary.SavingCents != 500 || p.Summary.ExtraStops != 2 || len(p.Stores) != 3 {
		t.Fatalf("100/3: %s %+v", p.Status, p.Summary)
	}
	// ... but with max 2 stops only the best single extra shop
	p = e.plan(Options{ExtraStopCents: ip(100), MaxStops: ii(2)})
	if p.Summary.SavingCents != 300 || p.Summary.ExtraStops != 1 || p.Stores[1].ShopID != c {
		t.Fatalf("100/2: %+v", p.Summary)
	}
}

func TestTiedPricesStayAtTheUsualShop(t *testing.T) {
	x := newTradeOff(t, true)
	// make coffee cost the same at B
	x.e.exec(`UPDATE receipt_lines SET line_total_cents = 300 WHERE product_id = ? AND receipt_id IN (SELECT id FROM receipts WHERE shop_id = ?)`, x.coffee, x.b)
	p := x.e.plan(Options{ExtraStopCents: ip(0)})
	if c := p.item(x.coffeeItem); *c.ShopID != x.a || c.SavingCents != 0 || c.Status != ItemCompared {
		t.Fatalf("coffee = %+v", c)
	}
	if p.Summary.GrossSavingCents != 240 {
		t.Fatalf("gross = %d", p.Summary.GrossSavingCents)
	}
}

func TestCheckedAndTemplateItemsAreNotPlanned(t *testing.T) {
	x := newTradeOff(t, true)
	x.e.exec(`UPDATE shopping_list_items SET checked_at = 't' WHERE id = ?`, x.breadItem)
	gid := x.e.exec(`INSERT INTO shopping_list_groups (name, kind, created_at, updated_at) VALUES ('tpl', 'template', 't', 't')`)
	x.e.exec(`INSERT INTO shopping_list_items (product_id, text, group_id, created_at, updated_at) VALUES (?, 'tpl item', ?, 't', 't')`, x.bread, gid)
	arch := x.e.exec(`INSERT INTO shopping_list_groups (name, kind, archived_at, created_at, updated_at) VALUES ('old', 'checklist', 't', 't', 't')`)
	x.e.exec(`INSERT INTO shopping_list_items (product_id, text, group_id, created_at, updated_at) VALUES (?, 'archived item', ?, 't', 't')`, x.bread, arch)
	live := x.e.exec(`INSERT INTO shopping_list_groups (name, kind, created_at, updated_at) VALUES ('bbq', 'checklist', 't', 't')`)
	inGroup := x.e.exec(`INSERT INTO shopping_list_items (product_id, text, group_id, created_at, updated_at) VALUES (?, 'in bbq', ?, 't', 't')`, x.coffee, live)
	p := x.e.plan()
	if len(p.Items) != 3 {
		t.Fatalf("items = %d: milk, coffee and the item in the live group", len(p.Items))
	}
	if g := p.item(inGroup); g.ListGroupID == nil || *g.ListGroupID != live {
		t.Fatalf("group id missing: %+v", g)
	}
}
