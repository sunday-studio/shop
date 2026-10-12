package listplan

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"shop/internal/match"
)

type fixLine struct {
	Raw   string `json:"raw"`
	Kind  string `json:"kind"`
	Cents int64  `json:"cents"`
}
type fixReceipt struct {
	File  string    `json:"file"`
	Shop  string    `json:"shop"`
	Date  string    `json:"date"`
	Lines []fixLine `json:"lines"`
}

var (
	leadQty   = regexp.MustCompile(`^(\d+)\s+`)
	trailCost = regexp.MustCompile(`\s+\d+[.,]\d{2}$`)
)

// realFixture loads the 13 real receipts (the same file the product tests use) with a product per distinct
// cleaned line text per shop (receipts are linked the way the matcher links same-text lines at one shop).
// It returns product ids by shop and cleaned name.
func (e *env) realFixture() map[string]int64 {
	e.t.Helper()
	b, err := os.ReadFile("../autoprod/testdata/receipts.json")
	if err != nil {
		e.t.Fatal(err)
	}
	var recs []fixReceipt
	if err := json.Unmarshal(b, &recs); err != nil {
		e.t.Fatal(err)
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Date < recs[j].Date })
	cat := e.category("Fixture")
	shops := map[string]int64{}
	prods := map[string]int64{}
	for _, r := range recs {
		sid, ok := shops[r.Shop]
		if !ok {
			sid = e.shop(r.Shop)
			shops[r.Shop] = sid
		}
		at, _ := time.Parse(time.RFC3339, r.Date)
		rid := e.exec(`INSERT INTO receipts (shop_id, purchased_at, image_path, image_sha256, status, confirmed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'ok', 't', 't', 't')`, sid, at.UTC().Format("2006-01-02T15:04:05Z"), "p"+r.File, "s"+r.File)
		for i, l := range r.Lines {
			if l.Kind != "item" || l.Cents <= 0 {
				e.exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, line_total_cents) VALUES (?, ?, ?, ?, ?, ?)`,
					rid, i, l.Raw, match.Normalise(l.Raw), l.Kind, l.Cents)
				continue
			}
			name := strings.TrimSpace(trailCost.ReplaceAllString(strings.TrimSpace(l.Raw), ""))
			qty := 1.0
			if m := leadQty.FindStringSubmatch(name); m != nil {
				n, _ := strconv.Atoi(m[1])
				qty = float64(n)
				name = strings.TrimSpace(name[len(m[0]):])
			}
			name = strings.TrimLeft(name, "-= ")
			if len(name) < 3 || strings.ContainsAny(name, "=") {
				continue
			}
			key := r.Shop + "|" + match.Normalise(name)
			pid, ok := prods[key]
			if !ok {
				pid = e.product(name, cat, 0, "")
				prods[key] = pid
			}
			e.exec(`INSERT INTO receipt_lines (receipt_id, line_no, raw_text, norm_text, kind, qty, unit, line_total_cents, product_id)
				VALUES (?, ?, ?, ?, 'item', ?, 'pc', ?, ?)`, rid, i, l.Raw, match.Normalise(l.Raw), qty, l.Cents, pid)
		}
	}
	return prods
}

// The 13 real receipts: three shops, 74 distinct product texts, only one pair that looks like the same kind
// of item (the Arla milk). Without a confirmed group nothing is comparable; the numbers below are the
// coverage the app would show on this data.
func TestRealReceiptsCoverage(t *testing.T) {
	e := newEnv(t)
	e.s.now = func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) }
	prods := e.realFixture()
	if len(prods) != 74 {
		t.Fatalf("fixture has %d products", len(prods))
	}
	ah, dirk := "Albert Heijn|", "Dirk|"
	arla, arlaDirk := prods[ah+"arla melk"], prods[dirk+"arla bio halfvol melk"]
	if arla == 0 || arlaDirk == 0 {
		t.Fatalf("fixture products missing: %v", prods)
	}

	sg, err := e.s.Suggestions(e.ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(sg) != 1 || sg[0].Score < SuggestMinScore {
		t.Fatalf("suggestions on the real receipts: %+v", sg)
	}
	pair := map[int64]bool{sg[0].A.ProductID: true, sg[0].B.ProductID: true}
	if !pair[arla] || !pair[arlaDirk] {
		t.Fatalf("expected the Arla pair, got %+v", sg[0])
	}

	items := map[string]int64{
		"arla":     e.item("arla melk", arla, 1),
		"citroen":  e.item("citroengras", prods[dirk+"citroengras"], 1),
		"tijm":     e.item("tijm", prods[dirk+"tijm"], 1),
		"haver":    e.item("havermout", prods[ah+"ah havermout"], 1),
		"cola":     e.item("cola", prods[ah+"coca cola"], 1),
		"zalm":     e.item("zalm", prods[ah+"ah zalmfilet"], 1),
		"pepper":   e.item("pepper", 0, 0),
		"unbought": e.item("unbought", e.product("Never scanned", 0, 0, ""), 1),
	}
	for k, id := range items {
		if id == 0 {
			t.Fatalf("missing %s", k)
		}
	}
	if prods[dirk+"citroengras"] == 0 || prods[dirk+"tijm"] == 0 || prods[ah+"ah havermout"] == 0 || prods[ah+"coca cola"] == 0 || prods[ah+"ah zalmfilet"] == 0 {
		t.Fatalf("fixture keys changed: %v", prods)
	}

	// 1. no group: not one item has a price at two shops
	p := e.plan()
	s := p.Summary
	if s.ItemCount != 8 || s.PricedCount != 6 || s.NoPriceCount != 2 || s.ComparedCount != 0 || s.Coverage != 0 {
		t.Fatalf("coverage without groups = %+v", s)
	}
	if p.Status != StatusNotEnoughData || s.Confidence != "none" || s.SavingCents != 0 || s.OneShopFine {
		t.Fatalf("no group, no promise: %s %+v", p.Status, s)
	}
	if p.UsualShopID == nil || p.Stores[0].Name != "Albert Heijn" || p.Stores[0].Receipts90d != 6 {
		t.Fatalf("usual shop = %+v", p.Stores)
	}
	if got := p.item(items["citroen"]); got.Status != ItemOnlyElsewhere || *got.ShopID != *p.UsualShopID || got.EstimateCents != nil {
		t.Fatalf("dirk-only item: %+v", got)
	}
	if got := p.item(items["arla"]); got.Pair == nil || got.Status != ItemOnlyHere {
		t.Fatalf("arla before the group: %+v", got)
	}
	if p.Stores[0].ItemCount != 8 || p.Stores[0].UnpricedCount != 4 { // citroengras, tijm, pepper, unbought have no estimate at AH
		t.Fatalf("store = %+v", p.Stores[0])
	}

	// 2. the user confirms the Arla pair: one item is now comparable, which is not enough to promise anything
	if _, err := e.s.Confirm(e.ctx, []int64{arla, arlaDirk}, "Arla melk"); err != nil {
		t.Fatal(err)
	}
	p = e.plan(Options{ExtraStopCents: ip(0)})
	s = p.Summary
	if s.ComparedCount != 1 || s.PricedCount != 6 || s.NoPriceCount != 2 || s.Coverage != 0.13 {
		t.Fatalf("coverage with the pair = %+v", s)
	}
	if p.Status != StatusNotEnoughData || s.SavingCents != 0 || s.Confidence != "none" {
		t.Fatalf("one comparable item: %s %+v", p.Status, s)
	}
	arlaItem := p.item(items["arla"])
	if arlaItem.Status != ItemCompared || arlaItem.Pair != nil || !contains(arlaItem.Confidence, ConfSeenOnce) || arlaItem.PackSizeKnown {
		t.Fatalf("arla after the group: %+v", arlaItem)
	}

	// 3. a second Arla item (the Dirk one) makes two comparable items: 239 at AH against 169 at Dirk
	halfv := e.item("halfvolle melk", arlaDirk, 1)
	p = e.plan()
	s = p.Summary
	if s.ComparedCount != 2 || s.Coverage != 0.22 || p.Status != StatusOneShopFine || !s.OneShopFine || s.SplitSavingCents != 140 || s.SavingCents != 0 {
		t.Fatalf("two Arla items at the default 2.50 stop: %s %+v", p.Status, s)
	}
	p = e.plan(Options{ExtraStopCents: ip(0)})
	s = p.Summary
	if p.Status != StatusSplit || s.GrossSavingCents != 140 || s.SavingCents != 140 || s.BaselineCents != 478 || s.PlanCents != 338 || s.Confidence != "low" {
		t.Fatalf("free extra stop: %s %+v", p.Status, s)
	}
	for _, id := range []int64{items["arla"], halfv} {
		it := p.item(id)
		if it.Status != ItemCompared || *it.ShopID != p.Stores[1].ShopID || p.Stores[1].Name != "Dirk" || it.SavingCents != 70 || *it.UnitCents != 169 {
			t.Fatalf("arla item = %+v", it)
		}
	}
	// the Dirk price is 72 days old: flagged, with the date in the explanation
	if it := p.item(halfv); !contains(it.Confidence, ConfOldPrice) || !strings.Contains(it.Explanation, "Cheaper at Dirk") || !strings.Contains(it.Explanation, "28 Apr") {
		t.Fatalf("explanation/confidence = %q %v", it.Explanation, it.Confidence)
	}
	if p.Stores[1].EstimateCents != 338 || p.Stores[1].ItemCount != 2 {
		t.Fatalf("stores = %+v", p.Stores)
	}
}
