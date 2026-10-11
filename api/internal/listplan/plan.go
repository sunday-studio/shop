package listplan

// GET /v1/list/plan: which shop for which item on the open list.
//
// The rules, in one place:
//   - prices come only from receipts with status ok, a date and a shop, after the discounts linked to the line
//     (the price_points view); a line paid at 0 or less is ignored;
//   - a price is cents per kg, per l or per piece. Weighed lines and products with a pack size are per kg or
//     l; a product without a pack size is compared per piece, and the answer says so (pack_size_known=false);
//   - an item's price at a shop is the median of its last 3 purchases there inside the window (90 days, 30
//     for fresh produce). Older purchases are ignored: an item with only old prices has no price;
//   - an item linked to a product group is priced from every member of the group, so house-brand milk at two
//     shops compares. Prices in different base units are never mixed;
//   - the usual shop is the one with the most receipts in the last 90 days. The plan tries the usual shop alone
//     and the usual shop with up to max_stops-1 of the next shops (4 shops at most);
//   - savings are counted only on items that have a price at the usual shop and at the shop they move to, and
//     are reduced by extra_stop_cents for every extra shop. If that is not above 0 the answer is
//     "one shop is fine"; with fewer than MinCompared comparable items there is not enough data to say anything;
//   - items with no price anywhere stay with the usual shop and carry no estimate.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"shop/internal/store"
)

const (
	DefaultExtraStopCents = 250
	MaxExtraStopCents     = 2000
	DefaultMaxStops       = 2
	MaxMaxStops           = 3

	WindowDays      = 90
	FreshWindowDays = 30
	// LastPurchases is how many purchases per shop feed the median.
	LastPurchases = 3
	// MinCompared is how many items must be priced at the usual shop and elsewhere before the plan says anything.
	MinCompared   = 2
	maxCandidates = 4

	settingsKey = "list_plan"
)

const (
	StatusSplit         = "split"
	StatusOneShopFine   = "one_shop_fine"
	StatusNotEnoughData = "not_enough_data"

	ItemCompared      = "compared"       // priced at the usual shop and at another shop
	ItemOnlyHere      = "only_here"      // priced at the usual shop only
	ItemOnlyElsewhere = "only_elsewhere" // priced at another shop only; stays with the usual shop, no estimate
	ItemNoPrice       = "no_price"

	ConfSeenOnce      = "seen_once"
	ConfOldPrice      = "old_price"
	ConfDifferentPack = "different_pack"
	ConfNotEnough     = "not_enough_data"
)

// Settings are stored on the server so every phone and the assistant agree.
type Settings struct {
	ExtraStopCents int64 `json:"extra_stop_cents"`
	MaxStops       int   `json:"max_stops"`
}

func defaultSettings() Settings {
	return Settings{ExtraStopCents: DefaultExtraStopCents, MaxStops: DefaultMaxStops}
}

func (st Settings) validate() error {
	if st.ExtraStopCents < 0 || st.ExtraStopCents > MaxExtraStopCents {
		return fmt.Errorf("%w: extra_stop_cents must be 0..%d", ErrInvalid, MaxExtraStopCents)
	}
	if st.MaxStops < 1 || st.MaxStops > MaxMaxStops {
		return fmt.Errorf("%w: max_stops must be 1..%d", ErrInvalid, MaxMaxStops)
	}
	return nil
}

// Settings returns the stored plan settings (defaults when never saved).
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := defaultSettings()
	var v string
	err := s.conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	var stored Settings
	if json.Unmarshal([]byte(v), &stored) == nil && stored.validate() == nil {
		st = stored
	}
	return st, nil
}

// UpdateSettings changes the given fields.
func (s *Service) UpdateSettings(ctx context.Context, extra *int64, maxStops *int) (Settings, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return st, err
	}
	if extra != nil {
		st.ExtraStopCents = *extra
	}
	if maxStops != nil {
		st.MaxStops = *maxStops
	}
	if err := st.validate(); err != nil {
		return st, err
	}
	b, _ := json.Marshal(st)
	_, err = s.conn.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, settingsKey, string(b), s.stamp())
	return st, err
}

// Options override the stored settings for one call (what-if); nil keeps the stored value.
type Options struct {
	ExtraStopCents *int64
	MaxStops       *int
}

// ---- response ----

type Plan struct {
	ComputedAt  time.Time  `json:"computed_at"`
	Rev         int64      `json:"list_rev"`
	Status      string     `json:"status"` // split | one_shop_fine | not_enough_data
	UsualShopID *int64     `json:"usual_shop_id"`
	Stores      []Store    `json:"stores"`
	Items       []PlanItem `json:"items"`
	Summary     Summary    `json:"summary"`
	Basis       Basis      `json:"basis"`
}

// Basis says how prices were worked out, so the app can print the same words the server used.
type Basis struct {
	WindowDays      int    `json:"window_days"`
	FreshWindowDays int    `json:"fresh_window_days"`
	MedianOf        int    `json:"median_of"`
	Comparison      string `json:"comparison"` // per_kg_l_or_piece
	PiecesNote      string `json:"pieces_note"`
}

type Store struct {
	ShopID        int64  `json:"shop_id"`
	Name          string `json:"name"`
	Chain         string `json:"chain,omitempty"`
	ItemCount     int    `json:"item_count"`
	EstimateCents int64  `json:"estimate_cents"`
	// UnpricedCount is how many of its items have no estimate (not counted in estimate_cents).
	UnpricedCount int  `json:"unpriced_count"`
	IsUsual       bool `json:"is_usual"`
	Receipts90d   int  `json:"receipts_90d"`
}

type Summary struct {
	ItemCount     int `json:"item_count"`
	PricedCount   int `json:"priced_count"`   // a fresh price at some shop
	ComparedCount int `json:"compared_count"` // priced at the usual shop and at another
	NoPriceCount  int `json:"no_price_count"`
	// PerPieceCount are priced items whose pack size is unknown, compared per piece.
	PerPieceCount int `json:"per_piece_count"`
	// SavingCents is the net saving of the recommended split: gross savings minus the extra stops. 0 unless status=split.
	SavingCents      int64 `json:"saving_cents"`
	GrossSavingCents int64 `json:"gross_saving_cents"`
	ExtraStops       int   `json:"extra_stops"`
	// ExtraStopCents is the cost applied for each extra stop.
	ExtraStopCents      int64 `json:"extra_stop_cents"`
	ExtraStopTotalCents int64 `json:"extra_stop_total_cents"`
	MaxStops            int   `json:"max_stops"`
	OneShopFine         bool  `json:"one_shop_fine"`
	// SplitSavingCents is the gross saving of the best split that was too small to be worth its extra stop.
	SplitSavingCents int64 `json:"split_saving_cents"`
	// BaselineCents is what the compared items cost at the usual shop alone; PlanCents the same items in the plan.
	BaselineCents int64 `json:"baseline_cents"`
	PlanCents     int64 `json:"plan_cents"`
	// Confidence is none | low | medium | high; Coverage is compared_count / item_count.
	Confidence string  `json:"confidence"`
	Coverage   float64 `json:"coverage"`
}

type Point struct {
	At          time.Time `json:"at"`
	UnitCents   int64     `json:"unit_cents"`
	PaidCents   int64     `json:"paid_cents"`
	ProductID   int64     `json:"product_id"`
	ProductName string    `json:"product_name"`
}

// ShopPrice is an item's price at one shop with the purchases it was worked out from.
type ShopPrice struct {
	ShopID    int64     `json:"shop_id"`
	ShopName  string    `json:"shop_name"`
	UnitCents int64     `json:"unit_cents"` // median of Points
	BaseUnit  string    `json:"base_unit"`
	Purchases int       `json:"purchases"` // purchases in the window, not just the last 3
	LatestAt  time.Time `json:"latest_at"`
	AgeDays   int       `json:"age_days"`
	IsUsual   bool      `json:"is_usual"`
	Points    []Point   `json:"points"` // the purchases the median is taken from, newest first
}

type OtherPrice struct {
	ShopID    int64 `json:"shop_id"`
	UnitCents int64 `json:"unit_cents"`
}

// PairConfirmation is the question "is A the same kind of item as B?" that would unlock a comparison.
type PairConfirmation struct {
	ProductA int64  `json:"product_a"`
	ProductB int64  `json:"product_b"`
	NameA    string `json:"name_a"`
	NameB    string `json:"name_b"`
	ShopA    string `json:"shop_a"`
	ShopB    string `json:"shop_b"`
	ShopAID  int64  `json:"shop_a_id"`
	ShopBID  int64  `json:"shop_b_id"`
}

type PlanItem struct {
	ItemID         int64   `json:"item_id"`
	Text           string  `json:"text"`
	ProductID      *int64  `json:"product_id"`
	ProductGroupID *int64  `json:"product_group_id"`
	ListGroupID    *int64  `json:"group_id"`
	Qty            float64 `json:"qty"`
	// ShopID is where to buy it: the usual shop unless the plan moves it.
	ShopID *int64 `json:"shop_id"`
	Status string `json:"status"`
	// NoPriceReason: not_linked | never_bought | stale | no_usual_shop (only when status=no_price).
	NoPriceReason string `json:"no_price_reason,omitempty"`
	// UnitCents / BaseUnit: price at ShopID (nil when there is none there).
	UnitCents     *int64 `json:"unit_cents"`
	BaseUnit      string `json:"base_unit,omitempty"`
	PackSizeKnown bool   `json:"pack_size_known"`
	// Basis: per_kg | per_l | per_piece
	Basis         string            `json:"basis,omitempty"`
	EstimateCents *int64            `json:"estimate_cents"`
	SavingCents   int64             `json:"saving_cents"` // counted saving if the plan moves it, else 0
	OtherPrices   []OtherPrice      `json:"other_prices"`
	Prices        []ShopPrice       `json:"prices"`
	Confidence    []string          `json:"confidence"`
	LastSeen      *time.Time        `json:"last_seen"`
	LastSeenShop  *int64            `json:"last_seen_shop_id"`
	Explanation   string            `json:"explanation"`
	Pair          *PairConfirmation `json:"pair_confirmation"`
}

// ---- computation ----

type shopStat struct {
	id       int64
	name     string
	chain    string
	receipts int
	last     string
}

type point struct {
	at       time.Time
	shop     int64
	unit     int64
	base     string
	known    bool
	paid     int64
	product  int64
	name     string
	weighed  bool
	qty      float64
	packBase float64 // base units in one pack (0 = unknown)
}

type itemRow struct {
	id        int64
	productID sql.NullInt64
	text      string
	qty       sql.NullFloat64
	group     sql.NullInt64
}

type prodInfo struct {
	id       int64
	netQty   sql.NullFloat64
	netUnit  sql.NullString
	category string
}

func isFresh(category string) bool {
	c := strings.ToLower(category)
	return strings.Contains(c, "groente") || strings.Contains(c, "fruit") || strings.Contains(c, "vegetable") || strings.Contains(c, "produce")
}

// unitPrice converts a paid line into cents per base unit, like stats.UnitPrice, and says whether the pack
// size was known. packBase is the size of one pack in base units (0 when unknown or weighed).
func unitPrice(paid int64, qty float64, unit string, netQty sql.NullFloat64, netUnit sql.NullString) (cents int64, base string, known bool, packBase float64) {
	if qty <= 0 {
		qty = 1
	}
	switch unit {
	case "kg":
		return round(float64(paid) / qty), "kg", true, 0
	case "l":
		return round(float64(paid) / qty), "l", true, 0
	}
	if netQty.Valid && netQty.Float64 > 0 && netUnit.Valid {
		switch netUnit.String {
		case "g":
			return round(float64(paid) / (qty * netQty.Float64 / 1000)), "kg", true, netQty.Float64 / 1000
		case "ml":
			return round(float64(paid) / (qty * netQty.Float64 / 1000)), "l", true, netQty.Float64 / 1000
		case "pc":
			return round(float64(paid) / (qty * netQty.Float64)), "pc", true, netQty.Float64
		}
	}
	return round(float64(paid) / qty), "pc", false, 0
}

func round(f float64) int64 { return int64(math.Round(f)) }

func medianOf(v []int64) int64 {
	c := append([]int64(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return round(float64(c[n/2-1]+c[n/2]) / 2)
}

func medianF(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// itemPricing is everything known about one list item before the shops are chosen.
type itemPricing struct {
	row       itemRow
	members   []int64
	groupID   *int64
	window    int
	refBase   string
	prices    map[int64]*ShopPrice // fresh prices by shop
	pieces    bool                 // priced per piece with an unknown pack size
	known     bool                 // pack size known for the prices used
	mismatch  bool                 // fresh purchases in another base unit were left out
	packDiff  bool                 // members differ in pack size
	amount    float64              // base units the item needs (pack size or typical weight times quantity)
	staleLast *time.Time
	staleShop int64
	anyPoint  bool
	reason    string
	counts    map[int64]int // purchases in the window per shop
}

func (it *itemPricing) cost(shop int64) (int64, bool) {
	p, ok := it.prices[shop]
	if !ok {
		return 0, false
	}
	return round(float64(p.UnitCents) * it.amount), true
}

// Plan computes the shopping plan for the open list.
func (s *Service) Plan(ctx context.Context, opt Options) (Plan, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return Plan{}, err
	}
	if opt.ExtraStopCents != nil {
		st.ExtraStopCents = *opt.ExtraStopCents
	}
	if opt.MaxStops != nil {
		st.MaxStops = *opt.MaxStops
	}
	if err := st.validate(); err != nil {
		return Plan{}, err
	}
	now := s.now()
	plan := Plan{ComputedAt: now.UTC().Truncate(time.Second), Stores: []Store{}, Items: []PlanItem{},
		Basis: Basis{WindowDays: WindowDays, FreshWindowDays: FreshWindowDays, MedianOf: LastPurchases, Comparison: "per_kg_l_or_piece",
			PiecesNote: "Products without a pack size are compared per piece."}}
	if err := s.conn.QueryRowContext(ctx, `SELECT rev FROM shopping_list_rev WHERE id = 1`).Scan(&plan.Rev); err != nil {
		return plan, err
	}

	items, err := s.openItems(ctx)
	if err != nil {
		return plan, err
	}
	shops, err := s.rankShops(ctx, now)
	if err != nil {
		return plan, err
	}
	if len(shops) > maxCandidates {
		shops = shops[:maxCandidates]
	}
	groupOf, err := s.groupMembers(ctx)
	if err != nil {
		return plan, err
	}
	pricing, err := s.priceItems(ctx, items, groupOf, now)
	if err != nil {
		return plan, err
	}
	sugs, err := s.Suggestions(ctx, MaxSuggestions)
	if err != nil {
		return plan, err
	}

	var usual int64
	if len(shops) > 0 {
		usual = shops[0].id
		plan.UsualShopID = &usual
	}
	candidate := map[int64]bool{}
	for _, sh := range shops {
		candidate[sh.id] = true
	}
	// Only candidate shops can be recommended; other shops' prices stay visible in the explanation.
	assign := map[int64]int64{} // item id -> shop
	chosen := []int64{}
	sum := Summary{ItemCount: len(items), ExtraStopCents: st.ExtraStopCents, MaxStops: st.MaxStops, Confidence: "none"}

	status := StatusNotEnoughData
	if len(shops) > 0 {
		var comparedAll int
		for _, ip := range pricing {
			if _, ok := ip.prices[usual]; ok && hasOther(ip, usual, candidate) {
				comparedAll++
			}
		}
		best := bestSubset(pricing, shops, usual, st)
		switch {
		case comparedAll < MinCompared:
			status = StatusNotEnoughData
			chosen = []int64{usual}
		case best.net > 0:
			status = StatusSplit
			chosen = best.shops
			assign = best.assign
			sum.GrossSavingCents = best.gross
			sum.ExtraStops = len(best.shops) - 1
			sum.ExtraStopTotalCents = int64(sum.ExtraStops) * st.ExtraStopCents
			sum.SavingCents = best.net
		default:
			status = StatusOneShopFine
			chosen = []int64{usual}
			if best.gross > 0 {
				sum.SplitSavingCents = best.gross
			}
		}
	}
	plan.Status = status
	sum.OneShopFine = status == StatusOneShopFine
	inPlan := map[int64]bool{}
	for _, id := range chosen {
		inPlan[id] = true
	}

	storeIdx := map[int64]*Store{}
	for _, sh := range shops {
		if inPlan[sh.id] {
			plan.Stores = append(plan.Stores, Store{ShopID: sh.id, Name: sh.name, Chain: sh.chain, IsUsual: sh.id == usual, Receipts90d: sh.receipts})
		}
	}
	// usual first, then by name for a stable order
	sort.SliceStable(plan.Stores, func(i, j int) bool {
		if plan.Stores[i].IsUsual != plan.Stores[j].IsUsual {
			return plan.Stores[i].IsUsual
		}
		return plan.Stores[i].ShopID < plan.Stores[j].ShopID
	})
	for i := range plan.Stores {
		storeIdx[plan.Stores[i].ShopID] = &plan.Stores[i]
	}
	shopNames := map[int64]shopStat{}
	for _, sh := range shops {
		shopNames[sh.id] = sh
	}

	for _, ip := range pricing {
		pi := s.planItem(ip, usual, len(shops) > 0, assign, candidate, shopNames, sugs)
		if pi.ShopID != nil {
			if stv := storeIdx[*pi.ShopID]; stv != nil {
				stv.ItemCount++
				if pi.EstimateCents != nil {
					stv.EstimateCents += *pi.EstimateCents
				} else {
					stv.UnpricedCount++
				}
			}
		}
		switch pi.Status {
		case ItemNoPrice:
			sum.NoPriceCount++
		default:
			sum.PricedCount++
		}
		if pi.Status != ItemNoPrice && !pi.PackSizeKnown && pi.Basis == "per_piece" {
			sum.PerPieceCount++
		}
		if pi.Status == ItemCompared {
			sum.ComparedCount++
			if bc, ok := ip.cost(usual); ok {
				sum.BaselineCents += bc
				if pi.EstimateCents != nil {
					sum.PlanCents += *pi.EstimateCents
				}
			}
		}
		plan.Items = append(plan.Items, pi)
	}
	if sum.ItemCount > 0 {
		sum.Coverage = math.Round(float64(sum.ComparedCount)/float64(sum.ItemCount)*100) / 100
	}
	sum.Confidence = confidence(status, plan.Items, sum)
	plan.Summary = sum
	return plan, nil
}

// confidence rates how far the headline number can be trusted: none without enough comparable items.
func confidence(status string, items []PlanItem, sum Summary) string {
	if status == StatusNotEnoughData || sum.ComparedCount < MinCompared {
		return "none"
	}
	weak := 0
	for _, it := range items {
		if it.Status != ItemCompared {
			continue
		}
		for _, c := range it.Confidence {
			if c == ConfSeenOnce || c == ConfOldPrice || c == ConfDifferentPack {
				weak++
				break
			}
		}
	}
	switch {
	case sum.ComparedCount < 3 || sum.Coverage < 0.34:
		return "low"
	case sum.ComparedCount >= 6 && sum.Coverage >= 0.6 && weak*3 <= sum.ComparedCount:
		return "high"
	}
	return "medium"
}

func hasOther(ip *itemPricing, usual int64, candidate map[int64]bool) bool {
	for id := range ip.prices {
		if id != usual && candidate[id] {
			return true
		}
	}
	return false
}

type subsetResult struct {
	shops  []int64
	gross  int64
	net    int64
	assign map[int64]int64
}

// bestSubset tries the usual shop alone and the usual shop with up to max_stops-1 of the other candidates,
// and returns the best split by net saving (the usual shop alone is never returned; net <= 0 means no split
// pays). gross is the saving before the cost of the extra stops.
func bestSubset(pricing []*itemPricing, shops []shopStat, usual int64, st Settings) subsetResult {
	var others []int64
	for _, sh := range shops {
		if sh.id != usual {
			others = append(others, sh.id)
		}
	}
	best := subsetResult{net: math.MinInt64}
	maxExtra := st.MaxStops - 1
	var try func(start int, cur []int64)
	eval := func(extra []int64) {
		set := append([]int64{usual}, extra...)
		r := subsetResult{shops: set, assign: map[int64]int64{}}
		for _, ip := range pricing {
			uc, ok := ip.cost(usual)
			if !ok {
				continue
			}
			bestShop, bestCost := usual, uc
			for _, sh := range extra {
				if c, ok := ip.cost(sh); ok && c < bestCost {
					bestShop, bestCost = sh, c
				}
			}
			if bestShop != usual {
				r.assign[ip.row.id] = bestShop
				r.gross += uc - bestCost
			}
		}
		r.net = r.gross - int64(len(extra))*st.ExtraStopCents
		if r.net > best.net || (r.net == best.net && len(r.shops) < len(best.shops)) {
			best = r
		}
	}
	try = func(start int, cur []int64) {
		if len(cur) > 0 {
			eval(cur)
		}
		if len(cur) == maxExtra {
			return
		}
		for i := start; i < len(others); i++ {
			try(i+1, append(append([]int64(nil), cur...), others[i]))
		}
	}
	try(0, nil)
	if best.net == math.MinInt64 {
		return subsetResult{shops: []int64{usual}, net: 0, assign: map[int64]int64{}}
	}
	return best
}

// ---- reading the data ----

func (s *Service) openItems(ctx context.Context) ([]itemRow, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT i.id, i.product_id, i.text, i.qty, i.group_id
		FROM shopping_list_items i LEFT JOIN shopping_list_groups g ON g.id = i.group_id
		WHERE i.checked_at IS NULL AND (g.id IS NULL OR (g.kind = 'checklist' AND g.archived_at IS NULL))
		ORDER BY i.created_at, i.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []itemRow
	for rows.Next() {
		var r itemRow
		if err := rows.Scan(&r.id, &r.productID, &r.text, &r.qty, &r.group); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) rankShops(ctx context.Context, now time.Time) ([]shopStat, error) {
	from := store.TS(now.AddDate(0, 0, -WindowDays))
	rows, err := s.conn.QueryContext(ctx, `SELECT r.shop_id, s.name, COALESCE(s.chain, ''), COUNT(*), MAX(r.purchased_at)
		FROM receipts r JOIN shops s ON s.id = r.shop_id AND s.deleted_at IS NULL
		WHERE r.status = 'ok' AND r.purchased_at >= ? AND r.purchased_at <= ?
		GROUP BY r.shop_id ORDER BY COUNT(*) DESC, MAX(r.purchased_at) DESC, r.shop_id`, from, store.TS(now.Add(36*time.Hour)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []shopStat
	for rows.Next() {
		var sh shopStat
		if err := rows.Scan(&sh.id, &sh.name, &sh.chain, &sh.receipts, &sh.last); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// groupMembers maps a product to the live products of its group (including itself). Ungrouped products are absent.
func (s *Service) groupMembers(ctx context.Context) (map[int64][]int64, error) {
	ps, err := loadProducts(ctx, s.conn, effectiveGroup+` IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	byGroup := map[int64][]int64{}
	for _, p := range ps {
		byGroup[p.group.Int64] = append(byGroup[p.group.Int64], p.id)
	}
	out := map[int64][]int64{}
	for _, p := range ps {
		// a group with a single live member is inert
		if len(byGroup[p.group.Int64]) >= 2 {
			out[p.id] = byGroup[p.group.Int64]
		}
	}
	return out, nil
}

func (s *Service) productGroupID(ctx context.Context, productID int64) *int64 {
	var g sql.NullInt64
	_ = s.conn.QueryRowContext(ctx, `SELECT `+effectiveGroup+` FROM products p WHERE p.id = ?`, productID).Scan(&g)
	return store.IntPtr(g)
}

func (s *Service) priceItems(ctx context.Context, items []itemRow, groupOf map[int64][]int64, now time.Time) ([]*itemPricing, error) {
	var want []int64
	for _, it := range items {
		if !it.productID.Valid {
			continue
		}
		want = append(want, it.productID.Int64)
		want = append(want, groupOf[it.productID.Int64]...)
	}
	want = uniq(want)
	pts := map[int64][]point{}
	prods := map[int64]prodInfo{}
	if len(want) > 0 {
		ph, args := placeholders(want)
		rows, err := s.conn.QueryContext(ctx, `SELECT pp.product_id, pp.shop_id, CAST(pp.purchased_at AS TEXT), pp.qty, pp.unit,
				CAST(pp.paid_cents AS INTEGER), p.name, p.net_quantity, p.net_unit
			FROM price_points pp
			JOIN shops s ON s.id = pp.shop_id AND s.deleted_at IS NULL
			JOIN products p ON p.id = pp.product_id AND p.deleted_at IS NULL
			WHERE pp.product_id IN (`+ph+`) AND pp.purchased_at IS NOT NULL AND pp.paid_cents > 0
			ORDER BY pp.purchased_at DESC, pp.line_id DESC`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var pid, sid, paid int64
			var at, unit, name string
			var qty float64
			var nq sql.NullFloat64
			var nu sql.NullString
			if err := rows.Scan(&pid, &sid, &at, &qty, &unit, &paid, &name, &nq, &nu); err != nil {
				return nil, err
			}
			t, err := store.ParseTS(at)
			if err != nil || t.After(now.Add(36*time.Hour)) {
				continue
			}
			uc, base, known, pack := unitPrice(paid, qty, unit, nq, nu)
			pts[pid] = append(pts[pid], point{at: t, shop: sid, unit: uc, base: base, known: known, paid: paid, product: pid, name: name,
				weighed: unit == "kg" || unit == "l", qty: qty, packBase: pack})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		rows.Close()
		pr, err := s.conn.QueryContext(ctx, `SELECT p.id, p.net_quantity, p.net_unit, COALESCE(c.name, '')
			FROM products p LEFT JOIN categories c ON c.id = p.category_id WHERE p.id IN (`+ph+`)`, args...)
		if err != nil {
			return nil, err
		}
		defer pr.Close()
		for pr.Next() {
			var pi prodInfo
			if err := pr.Scan(&pi.id, &pi.netQty, &pi.netUnit, &pi.category); err != nil {
				return nil, err
			}
			prods[pi.id] = pi
		}
		if err := pr.Err(); err != nil {
			return nil, err
		}
	}
	shopName := map[int64]string{}
	nr, err := s.conn.QueryContext(ctx, `SELECT id, name FROM shops`)
	if err != nil {
		return nil, err
	}
	defer nr.Close()
	for nr.Next() {
		var id int64
		var n string
		if err := nr.Scan(&id, &n); err != nil {
			return nil, err
		}
		shopName[id] = n
	}

	out := make([]*itemPricing, 0, len(items))
	for _, row := range items {
		ip := &itemPricing{row: row, prices: map[int64]*ShopPrice{}, counts: map[int64]int{}, window: WindowDays}
		out = append(out, ip)
		if !row.productID.Valid {
			ip.reason = "not_linked"
			continue
		}
		pid := row.productID.Int64
		ip.members = []int64{pid}
		if ms := groupOf[pid]; len(ms) > 0 {
			ip.members = ms
			ip.groupID = s.productGroupID(ctx, pid)
		}
		if isFresh(prods[pid].category) {
			ip.window = FreshWindowDays
		}
		var all []point
		for _, m := range ip.members {
			all = append(all, pts[m]...)
		}
		sort.SliceStable(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
		ip.anyPoint = len(all) > 0
		if len(all) > 0 {
			t := all[0].at
			ip.staleLast, ip.staleShop = &t, all[0].shop
		}
		cutoff := now.AddDate(0, 0, -ip.window)
		var fresh []point
		for _, p := range all {
			if !p.at.Before(cutoff) {
				fresh = append(fresh, p)
			}
		}
		if len(fresh) == 0 {
			ip.reason = "never_bought"
			if ip.anyPoint {
				ip.reason = "stale"
			}
			continue
		}
		ip.refBase, ip.mismatch = chooseBase(fresh)
		var use []point
		for _, p := range fresh {
			if p.base == ip.refBase {
				use = append(use, p)
			}
		}
		ip.known = true
		for _, p := range use {
			if !p.known {
				ip.known = false
			}
		}
		ip.pieces = ip.refBase == "pc" && !ip.known
		byShop := map[int64][]point{}
		for _, p := range use {
			byShop[p.shop] = append(byShop[p.shop], p)
		}
		for sid, list := range byShop { // already newest first
			ip.counts[sid] = len(list)
			top := list
			if len(top) > LastPurchases {
				top = top[:LastPurchases]
			}
			units := make([]int64, len(top))
			sp := &ShopPrice{ShopID: sid, ShopName: shopName[sid], BaseUnit: ip.refBase, Purchases: len(list), LatestAt: top[0].at,
				AgeDays: int(now.Sub(top[0].at).Hours() / 24)}
			for i, p := range top {
				units[i] = p.unit
				sp.Points = append(sp.Points, Point{At: p.at, UnitCents: p.unit, PaidCents: p.paid, ProductID: p.product, ProductName: p.name})
			}
			sp.UnitCents = medianOf(units)
			ip.prices[sid] = sp
		}
		ip.amount = amountOf(row, use, prods[pid], ip.refBase, prods, ip.members)
		ip.packDiff = packsDiffer(use, prods)
	}
	return out, nil
}

// chooseBase picks the base unit the item is compared in: the one seen at the most shops, then with the most
// purchases, then kg < l < pc. mismatch says purchases in another unit were left out.
func chooseBase(fresh []point) (string, bool) {
	shops := map[string]map[int64]bool{}
	count := map[string]int{}
	for _, p := range fresh {
		if shops[p.base] == nil {
			shops[p.base] = map[int64]bool{}
		}
		shops[p.base][p.shop] = true
		count[p.base]++
	}
	var bases []string
	for b := range shops {
		bases = append(bases, b)
	}
	sort.Strings(bases) // kg, l, pc
	best := bases[0]
	for _, b := range bases[1:] {
		if len(shops[b]) > len(shops[best]) || (len(shops[b]) == len(shops[best]) && count[b] > count[best]) {
			best = b
		}
	}
	return best, len(bases) > 1
}

// amountOf is how many base units one list entry needs: the pack size (or the usual weight of a weighed
// purchase) times the quantity on the list.
func amountOf(row itemRow, use []point, own prodInfo, base string, prods map[int64]prodInfo, members []int64) float64 {
	q := 1.0
	if row.qty.Valid && row.qty.Float64 > 0 {
		q = row.qty.Float64
	}
	pack := 0.0
	if _, b, known, pb := unitPrice(0, 1, "", own.netQty, own.netUnit); known && pb > 0 && b == base {
		pack = pb
	}
	if pack == 0 && base != "pc" {
		var w []float64
		for _, p := range use {
			if p.weighed {
				w = append(w, p.qty)
			}
		}
		if len(w) > 0 {
			pack = medianF(w)
		}
	}
	if pack == 0 {
		var ps []float64
		for _, p := range use {
			if p.packBase > 0 {
				ps = append(ps, p.packBase)
			}
		}
		if len(ps) > 0 {
			pack = medianF(ps)
		}
	}
	if pack == 0 {
		pack = 1
	}
	return pack * q
}

// packsDiffer: members were bought in packs more than 20% apart in size.
func packsDiffer(use []point, prods map[int64]prodInfo) bool {
	lo, hi := math.MaxFloat64, 0.0
	for _, p := range use {
		if p.packBase > 0 {
			lo, hi = math.Min(lo, p.packBase), math.Max(hi, p.packBase)
		}
	}
	return hi > 0 && hi > lo*1.2
}

// ---- one item ----

func eur(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s€%d.%02d", sign, c/100, c%100)
}

func perUnit(base string) string {
	switch base {
	case "kg":
		return "per kg"
	case "l":
		return "per l"
	}
	return "per piece"
}

func basisOf(base string) string {
	switch base {
	case "kg":
		return "per_kg"
	case "l":
		return "per_l"
	}
	return "per_piece"
}

func (s *Service) planItem(ip *itemPricing, usual int64, haveUsual bool, assign map[int64]int64, candidate map[int64]bool, shopNames map[int64]shopStat, sugs []Suggestion) PlanItem {
	row := ip.row
	pi := PlanItem{ItemID: row.id, Text: row.text, ProductID: store.IntPtr(row.productID), ProductGroupID: ip.groupID,
		ListGroupID: store.IntPtr(row.group), Qty: 1, OtherPrices: []OtherPrice{}, Prices: []ShopPrice{}, Confidence: []string{}}
	if row.qty.Valid && row.qty.Float64 > 0 {
		pi.Qty = row.qty.Float64
	}
	if ip.staleLast != nil {
		t := *ip.staleLast
		pi.LastSeen = &t
		sh := ip.staleShop
		pi.LastSeenShop = &sh
	}
	where := usual
	if haveUsual {
		pi.ShopID = &where
	}

	// prices, newest shop info first
	var ids []int64
	for id := range ip.prices {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := ip.prices[ids[i]], ip.prices[ids[j]]
		if a.UnitCents != b.UnitCents {
			return a.UnitCents < b.UnitCents
		}
		return a.ShopID < b.ShopID
	})
	for _, id := range ids {
		sp := *ip.prices[id]
		sp.IsUsual = haveUsual && id == usual
		if sh, ok := shopNames[id]; ok {
			sp.ShopName = sh.name
		}
		pi.Prices = append(pi.Prices, sp)
	}

	if len(ip.prices) == 0 {
		pi.Status = ItemNoPrice
		pi.NoPriceReason = ip.reason
		if !haveUsual && ip.reason == "" {
			pi.NoPriceReason = "no_usual_shop"
		}
		pi.Explanation = noPriceText(ip)
		pi.Pair = s.pairFor(ip, sugs)
		return pi
	}

	pi.BaseUnit = ip.refBase
	pi.Basis = basisOf(ip.refBase)
	pi.PackSizeKnown = ip.known
	if !haveUsual {
		pi.Status = ItemNoPrice
		pi.NoPriceReason = "no_usual_shop"
		pi.ShopID = nil
		pi.Explanation = "There is no usual shop yet: scan receipts from the last 90 days first."
		return pi
	}

	_, atUsual := ip.prices[usual]
	// the cheapest other candidate shop, for the comparison
	var bestOther int64
	for _, p := range pi.Prices {
		if p.ShopID != usual && candidate[p.ShopID] {
			bestOther = p.ShopID
			break
		}
	}

	moved, isMoved := assign[row.id]
	switch {
	case atUsual && (isMoved || bestOther != 0):
		pi.Status = ItemCompared
	case atUsual:
		pi.Status = ItemOnlyHere
	default:
		pi.Status = ItemOnlyElsewhere
	}
	target := usual
	if isMoved {
		target = moved
		where = moved
	}
	for _, p := range pi.Prices {
		if p.ShopID != target {
			pi.OtherPrices = append(pi.OtherPrices, OtherPrice{ShopID: p.ShopID, UnitCents: p.UnitCents})
		}
	}
	if pi.Status != ItemOnlyElsewhere {
		tp := ip.prices[target]
		uc := tp.UnitCents
		pi.UnitCents = &uc
		est, _ := ip.cost(target)
		pi.EstimateCents = &est
	}
	if isMoved {
		uc, _ := ip.cost(usual)
		pi.SavingCents = uc - *pi.EstimateCents
	}

	// confidence flags look at the prices that make the call: the usual shop and the best other one
	judged := []int64{}
	if atUsual {
		judged = append(judged, usual)
	}
	if isMoved {
		judged = append(judged, moved)
	} else if bestOther != 0 {
		judged = append(judged, bestOther)
	} else if !atUsual {
		judged = append(judged, ids...)
	}
	for _, id := range judged {
		sp := ip.prices[id]
		if ip.counts[id] == 1 && !contains(pi.Confidence, ConfSeenOnce) {
			pi.Confidence = append(pi.Confidence, ConfSeenOnce)
		}
		if sp.AgeDays*2 > ip.window && !contains(pi.Confidence, ConfOldPrice) {
			pi.Confidence = append(pi.Confidence, ConfOldPrice)
		}
	}
	if !ip.known || ip.mismatch || ip.packDiff {
		pi.Confidence = append(pi.Confidence, ConfDifferentPack)
	}
	if pi.Status == ItemOnlyHere || pi.Status == ItemOnlyElsewhere {
		pi.Confidence = append(pi.Confidence, ConfNotEnough)
	}
	pi.Explanation = explain(ip, pi, usual, target)
	if pi.Status != ItemCompared {
		pi.Pair = s.pairFor(ip, sugs)
	}
	return pi
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func noPriceText(ip *itemPricing) string {
	switch ip.reason {
	case "not_linked":
		return "Not linked to a product yet, so there is no price."
	case "stale":
		days := ip.window
		return fmt.Sprintf("The last price is over %d days old, so it is not used.", days)
	case "never_bought":
		return "No receipt has this item yet. Scan a receipt to learn the price."
	}
	return "No price yet."
}

func dateOf(t time.Time) string { return t.Format("2 Jan") }

func explain(ip *itemPricing, pi PlanItem, usual, target int64) string {
	unit := perUnit(ip.refBase)
	priceText := func(id int64) string {
		p := ip.prices[id]
		return fmt.Sprintf("%s %s at %s (median of %d, latest %s)", eur(p.UnitCents), unit, p.ShopName, min(len(p.Points), LastPurchases), dateOf(p.LatestAt))
	}
	var b strings.Builder
	switch pi.Status {
	case ItemCompared:
		if target != usual {
			fmt.Fprintf(&b, "Cheaper at %s: %s, against %s.", ip.prices[target].ShopName, priceText(target), priceText(usual))
		} else {
			// compare with the cheapest other candidate for the sentence
			var other int64
			for _, p := range pi.Prices {
				if p.ShopID != usual {
					other = p.ShopID
					break
				}
			}
			fmt.Fprintf(&b, "Stays at %s: %s. Elsewhere: %s.", ip.prices[usual].ShopName, priceText(usual), priceText(other))
		}
	case ItemOnlyHere:
		fmt.Fprintf(&b, "Only seen at %s: %s. Nothing to compare with.", ip.prices[usual].ShopName, priceText(usual))
	case ItemOnlyElsewhere:
		var id int64
		if len(pi.Prices) > 0 {
			id = pi.Prices[0].ShopID
		}
		fmt.Fprintf(&b, "Only seen at %s: %s. No price at your usual shop, so it is left there without an estimate.", ip.prices[id].ShopName, priceText(id))
	}
	if !ip.known {
		b.WriteString(" Pack size unknown, so this is a price per piece.")
	}
	if ip.mismatch {
		b.WriteString(" Purchases in another unit were left out.")
	}
	return b.String()
}

// pairFor finds an unconfirmed "same kind of item" suggestion that could give this item a comparison.
func (s *Service) pairFor(ip *itemPricing, sugs []Suggestion) *PairConfirmation {
	if !ip.row.productID.Valid {
		return nil
	}
	mine := map[int64]bool{}
	for _, m := range ip.members {
		mine[m] = true
	}
	for _, sg := range sugs {
		var me, other SuggestedProduct
		switch {
		case mine[sg.A.ProductID]:
			me, other = sg.A, sg.B
		case mine[sg.B.ProductID]:
			me, other = sg.B, sg.A
		default:
			continue
		}
		pc := &PairConfirmation{ProductA: me.ProductID, ProductB: other.ProductID, NameA: me.Name, NameB: other.Name}
		if len(me.Shops) > 0 {
			pc.ShopA, pc.ShopAID = me.Shops[0].Name, me.Shops[0].ID
		}
		for _, sh := range other.Shops {
			if sh.ID != pc.ShopAID {
				pc.ShopB, pc.ShopBID = sh.Name, sh.ID
				break
			}
		}
		if pc.ShopB == "" && len(other.Shops) > 0 {
			pc.ShopB, pc.ShopBID = other.Shops[0].Name, other.Shops[0].ID
		}
		return pc
	}
	return nil
}
