// Package mcpserver exposes Shop to an AI assistant over MCP
// (streamable HTTP). Tools are thin wrappers over the same services the
// REST API uses, and return compact results to keep the assistant's
// context small.
package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"shop/internal/autoprod"
	"shop/internal/english"
	"shop/internal/match"
	"shop/internal/money"
	"shop/internal/products"
	"shop/internal/receipts"
	"shop/internal/shopping"
	"shop/internal/stats"
	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/translate"
	"shop/internal/version"
)

type Deps struct {
	DB       *sql.DB
	Receipts *receipts.Service
	Products *products.Service
	Stats    *stats.Service
	List     *shopping.Service
	Matcher  *match.Matcher
	English  *english.Service
	Location *time.Location
	Auto     *autoprod.Service // optional: enables the review tools
}

type server struct {
	Deps
	q *db.Queries
}

const instructions = `Shop tracks grocery receipts (Dutch/English), products, prices per shop and a shopping list.
Money is given both as integer cents and formatted euros. Dates are local (Europe/Amsterdam), YYYY-MM-DD.
Product and shop arguments accept an id or a name (Dutch or English); names are fuzzy-matched and the result says which one was used.
Products have a Dutch name (as sold) and an English name_en.
Unit prices are per base unit (kg, l or piece). Prices come from receipts with status ok.`

// New builds the MCP server with all tools and resources registered.
func New(d Deps) *mcp.Server {
	if d.Location == nil {
		d.Location = time.UTC
	}
	s := &server{Deps: d, q: db.New(d.DB)}
	srv := mcp.NewServer(&mcp.Implementation{Name: "shop", Title: "Shop", Version: version.Version},
		&mcp.ServerOptions{Instructions: instructions})

	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	mcp.AddTool(srv, &mcp.Tool{Name: "spend_summary", Annotations: ro,
		Description: "Total grocery spend between two dates, grouped by day, week, month, shop or category."}, s.spendSummary)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_receipts", Annotations: ro,
		Description: "List receipts (newest first) with shop, date, total and status."}, s.listReceipts)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_receipt", Annotations: ro,
		Description: "One receipt with all its lines."}, s.getReceipt)
	mcp.AddTool(srv, &mcp.Tool{Name: "search_products", Annotations: ro,
		Description: "Fuzzy search the product catalog by name (Dutch or English, abbreviations ok)."}, s.searchProducts)
	mcp.AddTool(srv, &mcp.Tool{Name: "price_history", Annotations: ro,
		Description: "Every purchase of a product with paid price and price per base unit, oldest first."}, s.priceHistory)
	mcp.AddTool(srv, &mcp.Tool{Name: "compare_prices", Annotations: ro,
		Description: "Where a product is cheapest: latest, min and max price per base unit for each shop."}, s.comparePrices)
	mcp.AddTool(srv, &mcp.Tool{Name: "price_changes", Annotations: ro,
		Description: "Products whose price per unit changed by at least min_pct at a shop (recent vs earlier purchases)."}, s.priceChanges)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_shops", Annotations: ro,
		Description: "All shops."}, s.listShops)
	mcp.AddTool(srv, &mcp.Tool{Name: "frequent_items", Annotations: ro,
		Description: "Products bought regularly, with how often and whether they are due again (good for suggesting a shopping list)."}, s.frequentItems)
	mcp.AddTool(srv, &mcp.Tool{Name: "shopping_list_get", Annotations: ro,
		Description: "The current shopping list, with the cheapest known shop per linked product."}, s.listGet)
	mcp.AddTool(srv, &mcp.Tool{Name: "shopping_list_add",
		Annotations: &mcp.ToolAnnotations{},
		Description: "Add an item to the shopping list. Text close to a known product links to it; re-adding an open product increases its qty."}, s.listAdd)
	mcp.AddTool(srv, &mcp.Tool{Name: "shopping_list_check",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
		Description: "Mark a shopping list item as bought (checked=true) or not (checked=false)."}, s.listCheck)
	destructive := true
	mcp.AddTool(srv, &mcp.Tool{Name: "shopping_list_remove",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: true},
		Description: "Remove an item from the shopping list."}, s.listRemove)

	mcp.AddTool(srv, &mcp.Tool{Name: "translation_todo", Annotations: ro,
		Description: "Dutch words the English translator doesn't know yet, ranked by how many products use them, with example product names."}, s.translationTodo)
	mcp.AddTool(srv, &mcp.Tool{Name: "teach_word",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
		Description: "Teach the English for one Dutch word (or keep_as_is for brands/varieties). Re-translates every automatic product name; names the user typed are never changed."}, s.teachWord)

	if s.Auto != nil {
		mcp.AddTool(srv, &mcp.Tool{Name: "review_new_products", Annotations: ro,
			Description: "Products created automatically from receipts or barcode scans that wait for a decision (possible duplicates, missing category, odd names), with the receipt text they came from."}, s.reviewNewProducts)
		mcp.AddTool(srv, &mcp.Tool{Name: "resolve_review",
			Description: "Decide one product from review_new_products: accept (keep it), merge (same as the suggested or given product), keep_separate, or discard. Merge and discard can be undone in the app for 30 days."}, s.resolveReview)
	}

	srv.AddResource(&mcp.Resource{URI: "shop://shops", Name: "shops", MIMEType: "application/json",
		Description: "All shops"}, s.shopsResource)
	srv.AddResource(&mcp.Resource{URI: "shop://categories", Name: "categories", MIMEType: "application/json",
		Description: "Product categories"}, s.categoriesResource)
	return srv
}

// Handler serves MCP over streamable HTTP. Stateless: every request is
// self-contained, so restarts and multiple assistants need no session state.
func Handler(srv *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

// ---------------------------------------------------------------- helpers

type Amount struct {
	Cents int64  `json:"cents"`
	EUR   string `json:"eur"`
}

func amount(c int64) Amount { return Amount{Cents: c, EUR: money.EUR(c)} }

func (s *server) date(v string, addDays int) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation(time.DateOnly, v, s.Location)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q, use YYYY-MM-DD", v)
	}
	t = t.AddDate(0, 0, addDays)
	return &t, nil
}

func (s *server) localDay(t time.Time) string { return t.In(s.Location).Format(time.DateOnly) }

// resolveShop accepts an id or a (fuzzy) name.
func (s *server) resolveShop(ctx context.Context, ref string) (*db.Shop, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		sh, err := s.q.GetShop(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("no shop with id %d", id)
		}
		return &sh, nil
	}
	shops, err := s.q.ListShops(ctx)
	if err != nil {
		return nil, err
	}
	norm := match.Normalise(ref)
	var best *db.Shop
	bestScore := 0.0
	for i, sh := range shops {
		cand := match.Normalise(sh.Name + " " + sh.Chain.String + " " + sh.Branch.String)
		sc := max(match.Score(norm, match.Normalise(sh.Name)), match.Score(norm, cand))
		if strings.Contains(cand, norm) {
			sc = max(sc, 0.95)
		}
		if sc > bestScore {
			best, bestScore = &shops[i], sc
		}
	}
	if best == nil || bestScore < 0.7 {
		return nil, fmt.Errorf("no shop matches %q; use list_shops", ref)
	}
	return best, nil
}

type ProductRef struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	NameEN string `json:"name_en,omitempty"`
}

// resolveProduct accepts an id or a (fuzzy) name.
func (s *server) resolveProduct(ctx context.Context, ref string) (ProductRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ProductRef{}, errors.New("product is required")
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		p, err := s.Products.Get(ctx, id)
		if err != nil {
			return ProductRef{}, fmt.Errorf("no product with id %d", id)
		}
		return ProductRef{ID: p.ID, Name: p.Name, NameEN: p.NameEn.String}, nil
	}
	cands, err := s.Matcher.Search(ctx, ref, 0, 1, 0.6)
	if err != nil {
		return ProductRef{}, err
	}
	if len(cands) == 0 {
		return ProductRef{}, fmt.Errorf("no product matches %q; try search_products", ref)
	}
	return ProductRef{ID: cands[0].ProductID, Name: cands[0].Name, NameEN: cands[0].NameEN}, nil
}

// ------------------------------------------------------------- spend

type SpendIn struct {
	From    string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD, inclusive (default: 30 days ago)"`
	To      string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD, inclusive (default: today)"`
	GroupBy string `json:"group_by,omitempty" jsonschema:"day, week, month (default), shop, category or product"`
	Shop    string `json:"shop,omitempty" jsonschema:"only this shop (id or name)"`
}

type SpendBucket struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Amount
	Receipts int     `json:"receipts"`
	Qty      float64 `json:"qty,omitempty"`  // group_by=product
	Unit     string  `json:"unit,omitempty"` // group_by=product
}

type SpendOut struct {
	From     string        `json:"from"`
	To       string        `json:"to"`
	GroupBy  string        `json:"group_by"`
	Shop     string        `json:"shop,omitempty"`
	Total    Amount        `json:"total"`
	Receipts int           `json:"receipts"`
	Buckets  []SpendBucket `json:"buckets"`
}

func (s *server) spendSummary(ctx context.Context, _ *mcp.CallToolRequest, in SpendIn) (*mcp.CallToolResult, SpendOut, error) {
	now := time.Now().In(s.Location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Location)
	from, to := today.AddDate(0, 0, -29), today.AddDate(0, 0, 1)
	if t, err := s.date(in.From, 0); err != nil {
		return nil, SpendOut{}, err
	} else if t != nil {
		from = *t
	}
	if t, err := s.date(in.To, 1); err != nil {
		return nil, SpendOut{}, err
	} else if t != nil {
		to = *t
	}
	if !from.Before(to) {
		return nil, SpendOut{}, errors.New("from must not be after to")
	}
	group := stats.ByMonth
	if in.GroupBy != "" {
		group = stats.GroupBy(in.GroupBy)
	}
	if !group.Valid() {
		return nil, SpendOut{}, errors.New("group_by must be day, week, month, shop, category or product")
	}
	shop, err := s.resolveShop(ctx, in.Shop)
	if err != nil {
		return nil, SpendOut{}, err
	}
	var shopID *int64
	out := SpendOut{From: s.localDay(from), To: s.localDay(to.AddDate(0, 0, -1)), GroupBy: string(group)}
	if shop != nil {
		shopID, out.Shop = &shop.ID, shop.Name
	}
	res, err := s.Stats.Spend(ctx, stats.SpendQuery{From: from, To: to, GroupBy: group, ShopID: shopID})
	if err != nil {
		return nil, SpendOut{}, err
	}
	out.Total, out.Receipts = amount(res.TotalCents), res.Receipts
	out.Buckets = make([]SpendBucket, 0, len(res.Buckets))
	for _, b := range res.Buckets {
		sb := SpendBucket{Key: b.Key, Label: b.Label, Amount: amount(b.Cents), Receipts: b.Receipts, Unit: b.Unit}
		if b.Qty != nil {
			sb.Qty = *b.Qty
		}
		out.Buckets = append(out.Buckets, sb)
	}
	return nil, out, nil
}

// ---------------------------------------------------------- receipts

type ListReceiptsIn struct {
	From   string `json:"from,omitempty" jsonschema:"start date YYYY-MM-DD, inclusive"`
	To     string `json:"to,omitempty" jsonschema:"end date YYYY-MM-DD, inclusive"`
	Shop   string `json:"shop,omitempty" jsonschema:"shop id or name"`
	Status string `json:"status,omitempty" jsonschema:"pending, processing, needs_review, ok or failed"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max results, default 20, max 100"`
}

type ReceiptSummary struct {
	ID     int64   `json:"id"`
	Date   string  `json:"date,omitempty"`
	Shop   string  `json:"shop"`
	Total  *Amount `json:"total,omitempty"`
	Status string  `json:"status"`
	Lines  int     `json:"lines,omitempty"`
}

type ListReceiptsOut struct {
	Receipts []ReceiptSummary `json:"receipts"`
}

func (s *server) shopNames(ctx context.Context) (map[int64]string, error) {
	shops, err := s.q.ListShops(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(shops))
	for _, sh := range shops {
		m[sh.ID] = sh.Name
	}
	return m, nil
}

func (s *server) summary(r db.Receipt, names map[int64]string) ReceiptSummary {
	out := ReceiptSummary{ID: r.ID, Shop: names[r.ShopID.Int64], Status: r.Status}
	if !r.ShopID.Valid {
		out.Shop = "Unassigned"
	} else if out.Shop == "" {
		out.Shop = fmt.Sprintf("shop %d", r.ShopID.Int64)
	}
	if t := store.TimePtr(r.PurchasedAt); t != nil {
		out.Date = t.In(s.Location).Format("2006-01-02 15:04")
	}
	if r.TotalCents.Valid {
		a := amount(r.TotalCents.Int64)
		out.Total = &a
	}
	return out
}

func (s *server) listReceipts(ctx context.Context, _ *mcp.CallToolRequest, in ListReceiptsIn) (*mcp.CallToolResult, ListReceiptsOut, error) {
	f := receipts.ListFilter{Limit: 20}
	if in.Limit > 0 {
		f.Limit = int64(min(in.Limit, 100))
	}
	var err error
	if f.From, err = s.date(in.From, 0); err != nil {
		return nil, ListReceiptsOut{}, err
	}
	if f.To, err = s.date(in.To, 1); err != nil {
		return nil, ListReceiptsOut{}, err
	}
	if in.Status != "" {
		f.Status = &in.Status
	}
	shop, err := s.resolveShop(ctx, in.Shop)
	if err != nil {
		return nil, ListReceiptsOut{}, err
	}
	if shop != nil {
		f.ShopID = &shop.ID
	}
	recs, err := s.Receipts.List(ctx, f)
	if err != nil {
		return nil, ListReceiptsOut{}, err
	}
	names, err := s.shopNames(ctx)
	if err != nil {
		return nil, ListReceiptsOut{}, err
	}
	out := ListReceiptsOut{Receipts: make([]ReceiptSummary, len(recs))}
	for i, r := range recs {
		out.Receipts[i] = s.summary(r.Receipt, names)
		out.Receipts[i].Lines = int(r.LineCount)
	}
	return nil, out, nil
}

type GetReceiptIn struct {
	ID int64 `json:"id" jsonschema:"receipt id"`
}

type ReceiptLine struct {
	Text    string  `json:"text"`
	Kind    string  `json:"kind"`
	Qty     float64 `json:"qty"`
	Unit    string  `json:"unit"`
	Total   Amount  `json:"total"`
	Product string  `json:"product,omitempty"`
	TextEN  string  `json:"text_en,omitempty"`
}

type GetReceiptOut struct {
	ReceiptSummary
	Payment       string        `json:"payment,omitempty"`
	ReviewReasons []string      `json:"review_reasons,omitempty"`
	Lines         []ReceiptLine `json:"lines"`
}

func (s *server) getReceipt(ctx context.Context, _ *mcp.CallToolRequest, in GetReceiptIn) (*mcp.CallToolResult, GetReceiptOut, error) {
	rec, lines, err := s.Receipts.Get(ctx, in.ID)
	if err != nil {
		return nil, GetReceiptOut{}, fmt.Errorf("receipt %d: %w", in.ID, err)
	}
	names, err := s.shopNames(ctx)
	if err != nil {
		return nil, GetReceiptOut{}, err
	}
	out := GetReceiptOut{ReceiptSummary: s.summary(rec, names), Payment: rec.PaymentMethod.String, Lines: []ReceiptLine{}}
	if rec.ReviewReasons.Valid {
		_ = json.Unmarshal([]byte(rec.ReviewReasons.String), &out.ReviewReasons)
	}
	out.ReceiptSummary.Lines = len(lines)
	productNames := map[int64][2]string{}
	for _, l := range lines {
		rl := ReceiptLine{Text: l.RawText, Kind: l.Kind, Qty: l.Qty, Unit: l.Unit, Total: amount(l.LineTotalCents)}
		if l.ProductID.Valid {
			id := l.ProductID.Int64
			if _, ok := productNames[id]; !ok {
				if p, err := s.Products.Get(ctx, id); err == nil {
					productNames[id] = [2]string{p.Name, p.NameEn.String}
				}
			}
			rl.Product, rl.TextEN = productNames[id][0], productNames[id][1]
		} else if t := translate.Translate(match.Normalise(l.RawText)); t.OK() && l.Kind == "item" {
			rl.TextEN = t.Text
		}
		out.Lines = append(out.Lines, rl)
	}
	return nil, out, nil
}

// ---------------------------------------------------------- products

type SearchIn struct {
	Query string `json:"query" jsonschema:"product name or receipt text"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results, default 10"`
}

type ProductHit struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	NameEN string  `json:"name_en,omitempty"`
	Brand  string  `json:"brand,omitempty"`
	Score  float64 `json:"score"`
}

type SearchOut struct {
	Products []ProductHit `json:"products"`
}

func (s *server) searchProducts(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, SearchOut, error) {
	limit := 10
	if in.Limit > 0 {
		limit = min(in.Limit, 50)
	}
	cands, err := s.Matcher.Search(ctx, in.Query, 0, limit, 0.5)
	if err != nil {
		return nil, SearchOut{}, err
	}
	out := SearchOut{Products: make([]ProductHit, len(cands))}
	for i, c := range cands {
		out.Products[i] = ProductHit{ID: c.ProductID, Name: c.Name, NameEN: c.NameEN, Brand: c.Brand, Score: float64(int(c.Score*100)) / 100}
	}
	return nil, out, nil
}

type PriceHistoryIn struct {
	Product string `json:"product" jsonschema:"product id or name"`
	Shop    string `json:"shop,omitempty" jsonschema:"only this shop (id or name)"`
}

type PricePoint struct {
	Date      string `json:"date"`
	Shop      string `json:"shop"`
	Paid      Amount `json:"paid"`
	UnitPrice Amount `json:"unit_price"`
	BaseUnit  string `json:"base_unit"`
}

type PriceHistoryOut struct {
	Product ProductRef   `json:"product"`
	Points  []PricePoint `json:"points"`
}

func (s *server) priceHistory(ctx context.Context, _ *mcp.CallToolRequest, in PriceHistoryIn) (*mcp.CallToolResult, PriceHistoryOut, error) {
	p, err := s.resolveProduct(ctx, in.Product)
	if err != nil {
		return nil, PriceHistoryOut{}, err
	}
	shop, err := s.resolveShop(ctx, in.Shop)
	if err != nil {
		return nil, PriceHistoryOut{}, err
	}
	var shopID *int64
	if shop != nil {
		shopID = &shop.ID
	}
	pts, err := s.Stats.PriceHistory(ctx, p.ID, shopID)
	if err != nil {
		return nil, PriceHistoryOut{}, err
	}
	out := PriceHistoryOut{Product: p, Points: make([]PricePoint, len(pts))}
	for i, pt := range pts {
		out.Points[i] = PricePoint{Date: s.localDay(pt.At), Shop: pt.ShopName, Paid: amount(pt.PaidCents),
			UnitPrice: amount(pt.UnitCents), BaseUnit: pt.BaseUnit}
	}
	return nil, out, nil
}

type CompareIn struct {
	Product string `json:"product" jsonschema:"product id or name"`
}

type ShopPrice struct {
	Shop       string `json:"shop"`
	BaseUnit   string `json:"base_unit"`
	Latest     Amount `json:"latest_unit_price"`
	LatestDate string `json:"latest_date"`
	Min        Amount `json:"min_unit_price"`
	Max        Amount `json:"max_unit_price"`
	Purchases  int    `json:"purchases"`
	Cheapest   bool   `json:"cheapest"`
}

type CompareOut struct {
	Product ProductRef  `json:"product"`
	Shops   []ShopPrice `json:"shops"`
}

func (s *server) comparePrices(ctx context.Context, _ *mcp.CallToolRequest, in CompareIn) (*mcp.CallToolResult, CompareOut, error) {
	p, err := s.resolveProduct(ctx, in.Product)
	if err != nil {
		return nil, CompareOut{}, err
	}
	res, err := s.Stats.Compare(ctx, p.ID)
	if err != nil {
		return nil, CompareOut{}, err
	}
	out := CompareOut{Product: p, Shops: make([]ShopPrice, len(res))}
	for i, r := range res {
		out.Shops[i] = ShopPrice{Shop: r.ShopName, BaseUnit: r.BaseUnit, Latest: amount(r.Latest),
			LatestDate: s.localDay(r.LatestAt), Min: amount(r.Min), Max: amount(r.Max),
			Purchases: r.Purchases, Cheapest: r.Cheapest}
	}
	return nil, out, nil
}

type PriceChangesIn struct {
	Since  string  `json:"since,omitempty" jsonschema:"only products bought since YYYY-MM-DD (default: 90 days ago)"`
	MinPct float64 `json:"min_pct,omitempty" jsonschema:"minimum absolute change in percent (default 5)"`
}

type PriceChange struct {
	Product  string  `json:"product"`
	Shop     string  `json:"shop"`
	BaseUnit string  `json:"base_unit"`
	Old      Amount  `json:"old_unit_price"`
	New      Amount  `json:"new_unit_price"`
	Pct      float64 `json:"pct"`
	Latest   string  `json:"latest_date"`
}

type PriceChangesOut struct {
	Changes []PriceChange `json:"changes"`
}

func (s *server) priceChanges(ctx context.Context, _ *mcp.CallToolRequest, in PriceChangesIn) (*mcp.CallToolResult, PriceChangesOut, error) {
	since := time.Now().AddDate(0, 0, -90)
	if t, err := s.date(in.Since, 0); err != nil {
		return nil, PriceChangesOut{}, err
	} else if t != nil {
		since = *t
	}
	minPct := 5.0
	if in.MinPct > 0 {
		minPct = in.MinPct
	}
	res, err := s.Stats.PriceChanges(ctx, since, minPct)
	if err != nil {
		return nil, PriceChangesOut{}, err
	}
	out := PriceChangesOut{Changes: make([]PriceChange, len(res))}
	for i, c := range res {
		out.Changes[i] = PriceChange{Product: c.ProductName, Shop: c.ShopName, BaseUnit: c.BaseUnit,
			Old: amount(c.OldCents), New: amount(c.NewCents), Pct: c.Pct, Latest: s.localDay(c.LatestAt)}
	}
	return nil, out, nil
}

type Empty struct{}

type Shop struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	City string `json:"city,omitempty"`
}

type ShopsOut struct {
	Shops []Shop `json:"shops"`
}

func (s *server) shops(ctx context.Context) (ShopsOut, error) {
	rows, err := s.q.ListShops(ctx)
	if err != nil {
		return ShopsOut{}, err
	}
	out := ShopsOut{Shops: make([]Shop, len(rows))}
	for i, r := range rows {
		out.Shops[i] = Shop{ID: r.ID, Name: r.Name, City: r.City.String}
	}
	return out, nil
}

func (s *server) listShops(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, ShopsOut, error) {
	out, err := s.shops(ctx)
	return nil, out, err
}

type FrequentIn struct {
	LookbackDays int `json:"lookback_days,omitempty" jsonschema:"history window in days (default 180)"`
	MinCount     int `json:"min_count,omitempty" jsonschema:"minimum purchases to count as frequent (default 3)"`
}

type FrequentItem struct {
	Product            ProductRef `json:"product"`
	Purchases          int        `json:"purchases"`
	LastBought         string     `json:"last_bought"`
	MedianIntervalDays float64    `json:"median_interval_days"`
	DaysSince          float64    `json:"days_since"`
	Due                bool       `json:"due"`
}

type FrequentOut struct {
	Items []FrequentItem `json:"items"`
}

func (s *server) frequentItems(ctx context.Context, _ *mcp.CallToolRequest, in FrequentIn) (*mcp.CallToolResult, FrequentOut, error) {
	lookback, minCount := 180, 3
	if in.LookbackDays > 0 {
		lookback = min(in.LookbackDays, 730)
	}
	if in.MinCount > 1 {
		minCount = in.MinCount
	}
	res, err := s.Stats.FrequentItems(ctx, lookback, minCount, time.Now())
	if err != nil {
		return nil, FrequentOut{}, err
	}
	out := FrequentOut{Items: make([]FrequentItem, len(res))}
	for i, f := range res {
		out.Items[i] = FrequentItem{Product: ProductRef{ID: f.ProductID, Name: f.ProductName}, Purchases: f.Purchases,
			LastBought: s.localDay(f.LastBought), MedianIntervalDays: f.MedianIntervalDays, DaysSince: f.DaysSince, Due: f.Due}
	}
	return nil, out, nil
}

// -------------------------------------------------------------- list

type ListItem struct {
	ID       int64       `json:"id"`
	Text     string      `json:"text"`
	Qty      float64     `json:"qty,omitempty"`
	Note     string      `json:"note,omitempty"`
	Product  *ProductRef `json:"product,omitempty"`
	Checked  bool        `json:"checked"`
	Cheapest string      `json:"cheapest_at,omitempty"`
}

func toItem(it shopping.Item) ListItem {
	out := ListItem{ID: it.ID, Text: it.Text, Qty: it.Qty.Float64, Note: it.Note.String, Checked: it.CheckedAt.Valid}
	if it.ProductID.Valid {
		out.Product = &ProductRef{ID: it.ProductID.Int64, Name: it.ProductName}
	}
	if c := it.Cheapest; c != nil {
		out.Cheapest = fmt.Sprintf("%s (%s/%s)", c.ShopName, money.EUR(c.Latest), c.BaseUnit)
	}
	return out
}

type ListGetIn struct {
	IncludeChecked bool `json:"include_checked,omitempty" jsonschema:"also return items already checked off"`
}

type ListOut struct {
	Items []ListItem `json:"items"`
}

func (s *server) listGet(ctx context.Context, _ *mcp.CallToolRequest, in ListGetIn) (*mcp.CallToolResult, ListOut, error) {
	items, err := s.List.List(ctx, in.IncludeChecked)
	if err != nil {
		return nil, ListOut{}, err
	}
	out := ListOut{Items: make([]ListItem, len(items))}
	for i, it := range items {
		out.Items[i] = toItem(it)
	}
	return nil, out, nil
}

type ListAddIn struct {
	Text    string  `json:"text" jsonschema:"what to buy, e.g. 'havermelk' or 'eggs'"`
	Qty     float64 `json:"qty,omitempty" jsonschema:"how many (optional)"`
	Note    string  `json:"note,omitempty" jsonschema:"optional note"`
	Product string  `json:"product,omitempty" jsonschema:"link to this product (id or name) instead of matching the text"`
}

type ListAddOut struct {
	Item   ListItem `json:"item"`
	Merged bool     `json:"merged" jsonschema:"true if an existing open item's qty was increased"`
}

func (s *server) listAdd(ctx context.Context, _ *mcp.CallToolRequest, in ListAddIn) (*mcp.CallToolResult, ListAddOut, error) {
	si := shopping.Input{Text: in.Text}
	if in.Qty > 0 {
		si.Qty = &in.Qty
	}
	if in.Note != "" {
		si.Note = &in.Note
	}
	if in.Product != "" {
		p, err := s.resolveProduct(ctx, in.Product)
		if err != nil {
			return nil, ListAddOut{}, err
		}
		si.ProductID = &p.ID
	}
	it, created, err := s.List.Add(ctx, si)
	if err != nil {
		return nil, ListAddOut{}, err
	}
	return nil, ListAddOut{Item: toItem(it), Merged: !created}, nil
}

type ListCheckIn struct {
	ID      int64 `json:"id" jsonschema:"shopping list item id"`
	Checked *bool `json:"checked,omitempty" jsonschema:"true (default) to check off, false to uncheck"`
}

type ListItemOut struct {
	Item ListItem `json:"item"`
}

func (s *server) listCheck(ctx context.Context, _ *mcp.CallToolRequest, in ListCheckIn) (*mcp.CallToolResult, ListItemOut, error) {
	checked := in.Checked == nil || *in.Checked
	it, err := s.List.SetChecked(ctx, in.ID, checked)
	if err != nil {
		return nil, ListItemOut{}, fmt.Errorf("item %d: %w", in.ID, err)
	}
	return nil, ListItemOut{Item: toItem(it)}, nil
}

type ListRemoveIn struct {
	ID int64 `json:"id" jsonschema:"shopping list item id"`
}

type OKOut struct {
	OK bool `json:"ok"`
}

func (s *server) listRemove(ctx context.Context, _ *mcp.CallToolRequest, in ListRemoveIn) (*mcp.CallToolResult, OKOut, error) {
	if err := s.List.Delete(ctx, in.ID); err != nil {
		return nil, OKOut{}, fmt.Errorf("item %d: %w", in.ID, err)
	}
	return nil, OKOut{OK: true}, nil
}

// --------------------------------------------------------- resources

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
}

func (s *server) shopsResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	out, err := s.shops(ctx)
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, out.Shops)
}

func (s *server) categoriesResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	cats, err := s.q.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	type cat struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	out := make([]cat, len(cats))
	for i, c := range cats {
		out[i] = cat{ID: c.ID, Name: c.Name}
	}
	return jsonResource(req.Params.URI, out)
}

// ----------------------------------------------------------- english

type TodoIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"max words to return, default 20"`
}

type TodoWord struct {
	Word     string   `json:"word"`
	Key      string   `json:"key"`
	Products int      `json:"products"`
	Examples []string `json:"examples"`
}

type TodoOut struct {
	WordsLeft    int        `json:"words_left"`
	ProductsLeft int        `json:"products_left"`
	Words        []TodoWord `json:"words"`
}

func (s *server) translationTodo(ctx context.Context, _ *mcp.CallToolRequest, in TodoIn) (*mcp.CallToolResult, TodoOut, error) {
	limit := 20
	if in.Limit > 0 {
		limit = min(in.Limit, 200)
	}
	t, err := s.English.Todo(ctx, limit, time.Now())
	if err != nil {
		return nil, TodoOut{}, err
	}
	out := TodoOut{WordsLeft: t.WordsLeft, ProductsLeft: t.ProductsLeft, Words: make([]TodoWord, len(t.Words))}
	for i, w := range t.Words {
		out.Words[i] = TodoWord{Word: w.Word, Key: w.Key, Products: w.Products, Examples: w.Examples}
	}
	return nil, out, nil
}

type TeachIn struct {
	Word     string `json:"word" jsonschema:"one Dutch word, e.g. Elstar"`
	English  string `json:"english,omitempty" jsonschema:"its English; omit when keep_as_is"`
	KeepAsIs bool   `json:"keep_as_is,omitempty" jsonschema:"true for brands/varieties that stay as written"`
}

type TeachOut struct {
	Word            string `json:"word"`
	English         string `json:"english,omitempty"`
	KeepAsIs        bool   `json:"keep_as_is"`
	ProductsUpdated int    `json:"products_updated"`
	Replaced        string `json:"replaced,omitempty" jsonschema:"the previous English, if the word was taught before"`
}

func (s *server) teachWord(ctx context.Context, _ *mcp.CallToolRequest, in TeachIn) (*mcp.CallToolResult, TeachOut, error) {
	t, prev, n, err := s.English.Teach(ctx, in.Word, english.TermInput{English: in.English, KeepAsIs: in.KeepAsIs})
	if err != nil {
		return nil, TeachOut{}, err
	}
	out := TeachOut{Word: t.Dutch, English: t.English, KeepAsIs: t.KeepAsIs, ProductsUpdated: n}
	if prev != nil {
		out.Replaced = prev.English
		if prev.KeepAsIs {
			out.Replaced = "(kept as is)"
		}
	}
	return nil, out, nil
}
