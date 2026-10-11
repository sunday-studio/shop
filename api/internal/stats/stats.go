// Package stats computes spend reports and price analytics from receipts.
// All money is integer cents; per-unit prices are cents per kg, l or piece.
package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"shop/internal/store"
	"shop/internal/store/db"
)

type Service struct {
	q   *db.Queries
	loc *time.Location
}

func New(conn *sql.DB, loc *time.Location) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{q: db.New(conn), loc: loc}
}

// ---------------------------------------------------------------- spend

type GroupBy string

const (
	ByDay      GroupBy = "day"
	ByWeek     GroupBy = "week"
	ByMonth    GroupBy = "month"
	ByShop     GroupBy = "shop"
	ByCategory GroupBy = "category"
)

const ByProduct GroupBy = "product"

func (g GroupBy) Valid() bool {
	switch g {
	case ByDay, ByWeek, ByMonth, ByShop, ByCategory, ByProduct:
		return true
	}
	return false
}

func (g GroupBy) isTime() bool { return g == ByDay || g == ByWeek || g == ByMonth }

// Part is one slice of a split time bucket (a category or shop).
type Part struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Cents int64  `json:"cents"`
}

type Bucket struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Cents    int64    `json:"cents"`
	Receipts int      `json:"receipts"`
	Qty      *float64 `json:"qty,omitempty"`  // group_by=product
	Unit     string   `json:"unit,omitempty"` // group_by=product: pc | kg | l
	Split    []Part   `json:"split,omitempty"`
}

type Spend struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"` // exclusive
	GroupBy    GroupBy   `json:"group_by"`
	SplitBy    GroupBy   `json:"split_by,omitempty"`
	TotalCents int64     `json:"total_cents"`
	Receipts   int       `json:"receipts"`
	Buckets    []Bucket  `json:"buckets"`
}

// SpendQuery selects and groups spend.
type SpendQuery struct {
	From, To   time.Time // [From, To)
	GroupBy    GroupBy
	SplitBy    GroupBy // "", category or shop; only with a time GroupBy
	ShopID     *int64
	CategoryID *int64 // only with GroupBy product
}

// ErrInvalid marks a bad SpendQuery.
var ErrInvalid = errors.New("invalid spend query")

func (q SpendQuery) validate() error {
	if !q.GroupBy.Valid() {
		return fmt.Errorf("%w: group_by must be day, week, month, shop, category or product", ErrInvalid)
	}
	if q.SplitBy != "" && (!q.GroupBy.isTime() || (q.SplitBy != ByCategory && q.SplitBy != ByShop)) {
		return fmt.Errorf("%w: split_by must be category or shop, with group_by day, week or month", ErrInvalid)
	}
	if q.CategoryID != nil && q.GroupBy != ByProduct {
		return fmt.Errorf("%w: category_id is only supported with group_by=product", ErrInvalid)
	}
	return nil
}

// Spend totals receipts in [From, To).
//
//   - Time buckets are local and include empty periods so charts have no gaps.
//   - Shop and plain time buckets sum receipt totals.
//   - Category and product buckets sum receipt lines: discounts count
//     towards their item, and unlinked lines fall under "Uncategorised"
//     (category) or are left out (product).
//   - With split_by, each time bucket carries its parts. Its cents is the
//     sum of the parts, so stacked bars add up exactly.
//
// TotalCents and Receipts always describe all receipts in the range.
func (s *Service) Spend(ctx context.Context, q SpendQuery) (Spend, error) {
	if err := q.validate(); err != nil {
		return Spend{}, err
	}
	out := Spend{From: q.From, To: q.To, GroupBy: q.GroupBy, SplitBy: q.SplitBy, Buckets: []Bucket{}}
	var shop any
	if q.ShopID != nil {
		shop = *q.ShopID
	}
	rows, err := s.q.SpendReceipts(ctx, db.SpendReceiptsParams{FromTs: store.Str(store.TS(q.From)), ToTs: store.Str(store.TS(q.To)), ShopID: shop})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.TotalCents += r.TotalCents
	}
	out.Receipts = len(rows)

	var lines []db.SpendLinesRow
	if q.GroupBy == ByCategory || q.GroupBy == ByProduct || q.SplitBy == ByCategory {
		if lines, err = s.q.SpendLines(ctx, db.SpendLinesParams{FromTs: store.Str(store.TS(q.From)), ToTs: store.Str(store.TS(q.To)), ShopID: shop}); err != nil {
			return out, err
		}
	}

	g := newGrouper()
	categoryOf := func(l db.SpendLinesRow) (string, string) {
		if l.CategoryID.Valid {
			return strconv.FormatInt(l.CategoryID.Int64, 10), l.CategoryName.String
		}
		return "none", "Uncategorised"
	}

	switch {
	case q.GroupBy == ByShop:
		for _, r := range rows {
			g.add(shopKey(r.ShopID), r.ShopName, r.TotalCents, r.ID)
		}
	case q.GroupBy == ByCategory:
		for _, l := range lines {
			k, lb := categoryOf(l)
			g.add(k, lb, l.LineTotalCents, l.ReceiptID)
		}
	case q.GroupBy == ByProduct:
		for _, l := range lines {
			if !l.ProductID.Valid || (q.CategoryID != nil && (!l.CategoryID.Valid || l.CategoryID.Int64 != *q.CategoryID)) {
				continue
			}
			b := g.add(strconv.FormatInt(l.ProductID.Int64, 10), l.ProductName.String, l.LineTotalCents, l.ReceiptID)
			if l.Kind == "item" {
				g.addQty(b, l.Qty, l.Unit)
			}
		}
	default: // time
		for _, k := range s.periods(q.From, q.To, q.GroupBy) {
			g.ensure(k.Key, k.Label)
		}
		if q.SplitBy == ByCategory {
			for _, l := range lines {
				t, err := store.ParseTS(l.At)
				if err != nil {
					continue
				}
				p := s.period(t, q.GroupBy)
				b := g.add(p.Key, p.Label, l.LineTotalCents, l.ReceiptID)
				ck, cl := categoryOf(l)
				g.addPart(b, ck, cl, l.LineTotalCents)
			}
		} else {
			for _, r := range rows {
				t, err := store.ParseTS(r.At)
				if err != nil {
					continue
				}
				p := s.period(t, q.GroupBy)
				b := g.add(p.Key, p.Label, r.TotalCents, r.ID)
				if q.SplitBy == ByShop {
					g.addPart(b, shopKey(r.ShopID), r.ShopName, r.TotalCents)
				}
			}
		}
	}

	out.Buckets = g.buckets(!q.GroupBy.isTime())
	return out, nil
}

// grouper accumulates buckets, counting each receipt once per bucket.
type grouper struct {
	byKey map[string]*Bucket
	order []string
	seen  map[string]map[int64]bool
	parts map[*Bucket]map[string]*Part
	qty   map[*Bucket]map[string]float64 // unit → qty
}

func newGrouper() *grouper {
	return &grouper{byKey: map[string]*Bucket{}, seen: map[string]map[int64]bool{},
		parts: map[*Bucket]map[string]*Part{}, qty: map[*Bucket]map[string]float64{}}
}

func (g *grouper) ensure(key, label string) *Bucket {
	b, ok := g.byKey[key]
	if !ok {
		b = &Bucket{Key: key, Label: label}
		g.byKey[key] = b
		g.order = append(g.order, key)
		g.seen[key] = map[int64]bool{}
	}
	return b
}

func (g *grouper) add(key, label string, cents, receipt int64) *Bucket {
	b := g.ensure(key, label)
	b.Cents += cents
	if !g.seen[key][receipt] {
		g.seen[key][receipt] = true
		b.Receipts++
	}
	return b
}

func (g *grouper) addPart(b *Bucket, key, label string, cents int64) {
	m := g.parts[b]
	if m == nil {
		m = map[string]*Part{}
		g.parts[b] = m
	}
	p := m[key]
	if p == nil {
		p = &Part{Key: key, Label: label}
		m[key] = p
	}
	p.Cents += cents
}

func (g *grouper) addQty(b *Bucket, qty float64, unit string) {
	m := g.qty[b]
	if m == nil {
		m = map[string]float64{}
		g.qty[b] = m
	}
	m[unit] += qty
}

// buckets returns the result; non-time groupings are sorted by spend.
func (g *grouper) buckets(bySpend bool) []Bucket {
	out := make([]Bucket, 0, len(g.byKey))
	for _, k := range g.order {
		b := *g.byKey[k]
		if ps := g.parts[g.byKey[k]]; len(ps) > 0 {
			for _, p := range ps {
				b.Split = append(b.Split, *p)
			}
			sort.Slice(b.Split, func(i, j int) bool {
				if b.Split[i].Cents != b.Split[j].Cents {
					return b.Split[i].Cents > b.Split[j].Cents
				}
				return lessByName(b.Split[i].Label, b.Split[i].Key, b.Split[j].Label, b.Split[j].Key)
			})
		}
		// Quantity in the unit bought most (a product is rarely both weighed and counted).
		if qs := g.qty[g.byKey[k]]; len(qs) > 0 {
			best := ""
			for u, q := range qs {
				if best == "" || q > qs[best] || (q == qs[best] && u < best) {
					best = u
				}
			}
			q := math.Round(qs[best]*1000) / 1000
			b.Qty, b.Unit = &q, best
		}
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if bySpend && out[i].Cents != out[j].Cents {
			return out[i].Cents > out[j].Cents
		}
		if bySpend {
			return lessByName(out[i].Label, out[i].Key, out[j].Label, out[j].Key)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (s *Service) period(t time.Time, g GroupBy) Bucket {
	t = t.In(s.loc)
	switch g {
	case ByDay:
		k := t.Format(time.DateOnly)
		return Bucket{Key: k, Label: k}
	case ByWeek:
		y, w := t.ISOWeek()
		k := fmt.Sprintf("%d-W%02d", y, w)
		return Bucket{Key: k, Label: k}
	default:
		return Bucket{Key: t.Format("2006-01"), Label: t.Format("Jan 2006")}
	}
}

// periods lists every bucket between from and to (exclusive), capped to
// keep a huge range from exploding.
func (s *Service) periods(from, to time.Time, g GroupBy) []Bucket {
	var out []Bucket
	seen := map[string]bool{}
	step := func(t time.Time) time.Time {
		switch g {
		case ByDay:
			return t.AddDate(0, 0, 1)
		case ByWeek:
			return t.AddDate(0, 0, 7)
		default:
			return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		}
	}
	t := from.In(s.loc)
	if g == ByMonth {
		t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, s.loc)
	}
	for i := 0; t.Before(to) && i < 1000; i++ {
		b := s.period(t, g)
		if !seen[b.Key] {
			seen[b.Key] = true
			out = append(out, b)
		}
		t = step(t)
	}
	return out
}

// --------------------------------------------------------------- prices

// PricePoint is one purchase of a product, normalised to a base unit.
type PricePoint struct {
	LineID    int64     `json:"line_id"`
	ShopID    int64     `json:"shop_id"`
	ShopName  string    `json:"shop_name"`
	At        time.Time `json:"at"`
	Qty       float64   `json:"qty"`
	Unit      string    `json:"unit"`
	PaidCents int64     `json:"paid_cents"`
	UnitCents int64     `json:"unit_cents"` // per base unit
	BaseUnit  string    `json:"base_unit"`  // kg | l | pc
}

// UnitPrice converts a paid line into cents per base unit. Weighed lines
// (kg/l) divide by qty; piece lines use the product's package size when
// known (g → per kg, ml → per l, pc → per piece).
func UnitPrice(paid int64, qty float64, unit string, netQty sql.NullFloat64, netUnit sql.NullString) (int64, string) {
	if qty <= 0 {
		qty = 1
	}
	switch unit {
	case "kg":
		return round(float64(paid) / qty), "kg"
	case "l":
		return round(float64(paid) / qty), "l"
	}
	if netQty.Valid && netQty.Float64 > 0 && netUnit.Valid {
		switch netUnit.String {
		case "g":
			return round(float64(paid) / (qty * netQty.Float64 / 1000)), "kg"
		case "ml":
			return round(float64(paid) / (qty * netQty.Float64 / 1000)), "l"
		case "pc":
			return round(float64(paid) / (qty * netQty.Float64)), "pc"
		}
	}
	return round(float64(paid) / qty), "pc"
}

func round(f float64) int64 { return int64(math.Round(f)) }

func toPoint(r db.ProductPricePointsRow) (PricePoint, bool) {
	t, err := store.ParseTS(r.PurchasedAt)
	if err != nil {
		return PricePoint{}, false
	}
	uc, base := UnitPrice(r.PaidCents, r.Qty, r.Unit, r.NetQuantity, r.NetUnit)
	return PricePoint{
		LineID: r.LineID, ShopID: r.ShopID, ShopName: r.ShopName, At: t, Qty: r.Qty, Unit: r.Unit,
		PaidCents: r.PaidCents, UnitCents: uc, BaseUnit: base,
	}, true
}

// PriceHistory returns a product's purchases, oldest first.
func (s *Service) PriceHistory(ctx context.Context, productID int64, shopID *int64) ([]PricePoint, error) {
	var shop any
	if shopID != nil {
		shop = *shopID
	}
	rows, err := s.q.ProductPricePoints(ctx, db.ProductPricePointsParams{ProductID: productID, ShopID: shop})
	if err != nil {
		return nil, err
	}
	out := make([]PricePoint, 0, len(rows))
	for _, r := range rows {
		if p, ok := toPoint(r); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

type ShopPrice struct {
	ShopID    int64     `json:"shop_id"`
	ShopName  string    `json:"shop_name"`
	BaseUnit  string    `json:"base_unit"`
	Latest    int64     `json:"latest_unit_cents"`
	LatestAt  time.Time `json:"latest_at"`
	Min       int64     `json:"min_unit_cents"`
	Max       int64     `json:"max_unit_cents"`
	Purchases int       `json:"purchases"`
	Cheapest  bool      `json:"cheapest"`
}

// Compare summarises a product's price per shop, cheapest latest price first.
func (s *Service) Compare(ctx context.Context, productID int64) ([]ShopPrice, error) {
	pts, err := s.PriceHistory(ctx, productID, nil)
	if err != nil {
		return nil, err
	}
	return compareFrom(pts), nil
}

// compareFrom builds per-shop prices from a product's points (oldest first).
func compareFrom(pts []PricePoint) []ShopPrice {
	byShop := map[int64]*ShopPrice{}
	var order []int64
	for _, p := range pts { // oldest → newest, so the last write is the latest
		sp, ok := byShop[p.ShopID]
		if !ok {
			sp = &ShopPrice{ShopID: p.ShopID, ShopName: p.ShopName, Min: p.UnitCents, Max: p.UnitCents}
			byShop[p.ShopID] = sp
			order = append(order, p.ShopID)
		}
		sp.BaseUnit, sp.Latest, sp.LatestAt = p.BaseUnit, p.UnitCents, p.At
		sp.Min, sp.Max = min(sp.Min, p.UnitCents), max(sp.Max, p.UnitCents)
		sp.Purchases++
	}
	out := make([]ShopPrice, 0, len(order))
	for _, id := range order {
		out = append(out, *byShop[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Latest < out[j].Latest })
	if len(out) > 0 {
		out[0].Cheapest = true
	}
	return out
}

// ProductSummary is what product lists show next to each product.
type ProductSummary struct {
	Cheapest     *ShopPrice // best latest price per base unit (ok receipts)
	LastBoughtAt *time.Time
	Purchases    int // receipts it was bought on (ok + needs_review)
}

// ProductSummaries computes summaries for many products in two queries,
// so a product list doesn't need one compare call per row.
func (s *Service) ProductSummaries(ctx context.Context, ids []int64) (map[int64]ProductSummary, error) {
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make(map[int64]ProductSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ProductPricePoints(ctx, db.ProductPricePointsParams{})
	if err != nil {
		return nil, err
	}
	points := map[int64][]PricePoint{}
	for _, r := range rows {
		if !r.ProductID.Valid || !want[r.ProductID.Int64] {
			continue
		}
		if p, ok := toPoint(r); ok {
			points[r.ProductID.Int64] = append(points[r.ProductID.Int64], p)
		}
	}
	for id, pts := range points {
		if cmp := compareFrom(pts); len(cmp) > 0 {
			c := cmp[0]
			out[id] = ProductSummary{Cheapest: &c}
		}
	}
	sums, err := s.q.ProductPurchaseSummary(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range sums {
		if !r.ProductID.Valid || !want[r.ProductID.Int64] {
			continue
		}
		ps := out[r.ProductID.Int64]
		ps.Purchases = int(r.Purchases)
		if t, err := store.ParseTS(r.LastAt); err == nil {
			ps.LastBoughtAt = &t
		}
		out[r.ProductID.Int64] = ps
	}
	return out, nil
}

type PriceChange struct {
	ProductID   int64     `json:"product_id"`
	ProductName string    `json:"product_name"`
	ShopID      int64     `json:"shop_id"`
	ShopName    string    `json:"shop_name"`
	BaseUnit    string    `json:"base_unit"`
	OldCents    int64     `json:"old_unit_cents"`
	NewCents    int64     `json:"new_unit_cents"`
	Pct         float64   `json:"pct"`
	LatestAt    time.Time `json:"latest_at"`
}

// PriceChanges compares, per product and shop, the median of the latest 3
// purchases with the median of the 3 before them. Only pairs with a
// purchase on/after since and a change of at least minPct are returned,
// biggest change first.
func (s *Service) PriceChanges(ctx context.Context, since time.Time, minPct float64) ([]PriceChange, error) {
	rows, err := s.q.ProductPricePoints(ctx, db.ProductPricePointsParams{})
	if err != nil {
		return nil, err
	}
	type key struct{ product, shop int64 }
	series := map[key][]PricePoint{}
	names := map[int64]string{}
	var keys []key
	for _, r := range rows {
		p, ok := toPoint(r)
		if !ok || !r.ProductID.Valid {
			continue
		}
		k := key{r.ProductID.Int64, r.ShopID}
		if _, seen := series[k]; !seen {
			keys = append(keys, k)
		}
		series[k] = append(series[k], p)
		names[k.product] = r.ProductName
	}

	out := []PriceChange{}
	for _, k := range keys {
		pts := series[k]
		// Compare like with like: keep the base unit of the latest point.
		base := pts[len(pts)-1].BaseUnit
		var vals []PricePoint
		for _, p := range pts {
			if p.BaseUnit == base {
				vals = append(vals, p)
			}
		}
		if len(vals) < 2 || vals[len(vals)-1].At.Before(since) {
			continue
		}
		n := len(vals)
		recent := vals[max(0, n-3):]
		prev := vals[max(0, n-3-len(recent)):max(0, n-len(recent))]
		if len(prev) == 0 {
			// Only 2–3 points: split them in half.
			half := n / 2
			prev, recent = vals[:half], vals[half:]
		}
		oldC, newC := median(prev), median(recent)
		if oldC == 0 {
			continue
		}
		pct := float64(newC-oldC) / float64(oldC) * 100
		if math.Abs(pct) < minPct {
			continue
		}
		out = append(out, PriceChange{
			ProductID: k.product, ProductName: names[k.product], ShopID: k.shop, ShopName: pts[0].ShopName,
			BaseUnit: base, OldCents: oldC, NewCents: newC, Pct: math.Round(pct*10) / 10,
			LatestAt: vals[n-1].At,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return math.Abs(out[i].Pct) > math.Abs(out[j].Pct) })
	return out, nil
}

func median(ps []PricePoint) int64 {
	v := make([]int64, len(ps))
	for i, p := range ps {
		v[i] = p.UnitCents
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	n := len(v)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// ------------------------------------------------------- frequent items

type FrequentItem struct {
	ProductID          int64     `json:"product_id"`
	ProductName        string    `json:"product_name"`
	Purchases          int       `json:"purchases"`
	LastBought         time.Time `json:"last_bought"`
	MedianIntervalDays float64   `json:"median_interval_days"`
	DaysSince          float64   `json:"days_since"`
	Due                bool      `json:"due"`
}

// FrequentItems lists products bought at least minCount times in the last
// lookbackDays. An item is due once the days since the last purchase reach
// its typical (median) gap between purchases. Due items come first, most
// overdue first.
func (s *Service) FrequentItems(ctx context.Context, lookbackDays, minCount int, now time.Time) ([]FrequentItem, error) {
	rows, err := s.q.ProductPurchases(ctx, store.Str(store.TS(now.AddDate(0, 0, -lookbackDays))))
	if err != nil {
		return nil, err
	}
	type acc struct {
		name  string
		dates []time.Time
	}
	by := map[int64]*acc{}
	var ids []int64
	for _, r := range rows {
		if !r.ProductID.Valid {
			continue
		}
		t, err := store.ParseTS(r.At)
		if err != nil {
			continue
		}
		a, ok := by[r.ProductID.Int64]
		if !ok {
			a = &acc{name: r.ProductName}
			by[r.ProductID.Int64] = a
			ids = append(ids, r.ProductID.Int64)
		}
		a.dates = append(a.dates, t)
	}

	out := []FrequentItem{}
	for _, id := range ids {
		a := by[id]
		if len(a.dates) < minCount {
			continue
		}
		sort.Slice(a.dates, func(i, j int) bool { return a.dates[i].Before(a.dates[j]) })
		var gaps []float64
		for i := 1; i < len(a.dates); i++ {
			gaps = append(gaps, a.dates[i].Sub(a.dates[i-1]).Hours()/24)
		}
		sort.Float64s(gaps)
		med := 0.0
		if n := len(gaps); n > 0 {
			med = gaps[n/2]
			if n%2 == 0 {
				med = (gaps[n/2-1] + gaps[n/2]) / 2
			}
		}
		last := a.dates[len(a.dates)-1]
		since := now.Sub(last).Hours() / 24
		out = append(out, FrequentItem{
			ProductID: id, ProductName: a.name, Purchases: len(a.dates), LastBought: last,
			MedianIntervalDays: math.Round(med*10) / 10, DaysSince: math.Round(since*10) / 10,
			Due: med > 0 && since >= med,
		})
	}
	overdue := func(f FrequentItem) float64 {
		if f.MedianIntervalDays == 0 {
			return 0
		}
		return f.DaysSince / f.MedianIntervalDays
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Due != out[j].Due {
			return out[i].Due
		}
		return overdue(out[i]) > overdue(out[j])
	})
	return out, nil
}

// shopKey is the bucket key of a receipt's shop; receipts without one share "none" ("Unassigned").
func shopKey(id sql.NullInt64) string {
	if !id.Valid {
		return "none"
	}
	return strconv.FormatInt(id.Int64, 10)
}

// lessByName orders equal-spend buckets alphabetically (ignoring case), then by key for stability.
func lessByName(la, ka, lb, kb string) bool {
	if a, b := strings.ToLower(la), strings.ToLower(lb); a != b {
		return a < b
	}
	return ka < kb
}
