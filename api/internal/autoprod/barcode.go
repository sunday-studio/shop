package autoprod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"shop/internal/jobs"
	"shop/internal/openfoodfacts"
	"shop/internal/products"
	"shop/internal/store"
	"shop/internal/store/db"
)

// offCategoryRules maps Open Food Facts categories_tags prefixes to our categories (§4.3.1), in order.
var offCategoryRules = []struct{ prefix, category string }{
	{"en:frozen", "Frozen"},
	{"en:beverages", "Drinks"}, {"en:waters", "Drinks"}, {"en:sodas", "Drinks"},
	{"en:dairies", "Dairy"}, {"en:milks", "Dairy"}, {"en:cheeses", "Dairy"}, {"en:yogurts", "Dairy"}, {"en:eggs", "Dairy"},
	{"en:meats", "Meat & fish"}, {"en:fishes", "Meat & fish"}, {"en:seafood", "Meat & fish"}, {"en:poultry", "Meat & fish"},
	{"en:breads", "Bakery"}, {"en:pastries", "Bakery"}, {"en:biscuits-and-cakes", "Bakery"},
	{"en:fruits", "Produce"}, {"en:vegetables", "Produce"},
	{"en:cleaning", "Household"}, {"en:household", "Household"},
	{"en:hygiene", "Personal care"}, {"en:cosmetics", "Personal care"},
	{"en:snacks", "Pantry"}, {"en:groceries", "Pantry"}, {"en:sauces", "Pantry"}, {"en:cereals-and-potatoes", "Pantry"}, {"en:canned-foods", "Pantry"},
}

// OFFCategory maps categories_tags to one of our category names.
func OFFCategory(tags []string) (string, bool) {
	has := func(p string) bool {
		for _, t := range tags {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
		return false
	}
	for _, r := range offCategoryRules {
		if has(r.prefix) {
			return r.category, true
		}
	}
	// plant-based foods count as Produce only when also fresh
	if has("en:plant-based-foods") && has("en:fresh") {
		return "Produce", true
	}
	return "", false
}

// BarcodeOutcome is the result of CreateFromBarcode.
type BarcodeOutcome struct {
	Status          string // known | created | created_minimal
	CreatedNow      bool
	OFFStatus       string // found | not_found | unavailable ("" when known)
	Product         db.Product
	MergeSuggestion *SuggestedMerge
	Unmatched       []products.UnmatchedLine
}

type refreshPayload struct {
	ProductID int64 `json:"product_id"`
	Tries     int   `json:"tries"`
}

const maxRefreshTries = 5

// CreateFromBarcode implements POST /v1/barcodes/{code}/product (§4.3). It is idempotent: a known
// barcode returns the existing product. Open Food Facts failures never fail the request.
func (s *Service) CreateFromBarcode(ctx context.Context, code string) (BarcodeOutcome, error) {
	if !openfoodfacts.ValidBarcode(code) {
		return BarcodeOutcome{}, errf(ErrInvalid, "barcode must be 8 or 12-14 digits")
	}
	if p, err := s.q.GetProductByBarcode(ctx, store.Str(code)); err == nil {
		return BarcodeOutcome{Status: "known", Product: p}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return BarcodeOutcome{}, err
	}

	offCtx, cancel := context.WithTimeout(ctx, s.OFFTimeout)
	pre, offErr := s.off.Lookup(offCtx, code)
	cancel()
	status := "found"
	switch {
	case errors.Is(offErr, openfoodfacts.ErrNotFound):
		status = "not_found"
	case offErr != nil:
		status = "unavailable"
		s.log.Warn("open food facts unavailable for a scan", "code", code, "err", offErr)
	}

	var p db.Product
	now := store.TS(s.now())
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		in := newProduct{createdVia: "barcode", barcode: code, noReview: false}
		switch status {
		case "found":
			in.name = pre.Name
			in.brand = pre.Brand // kept as its own field; the name is not changed to repeat it
			in.offCode = code
			in.nameEN = pre.NameEN
			if pre.NetQuantity != nil {
				in.netQty, in.netUnit = pre.NetQuantity, pre.NetUnit
			}
			if cat, ok := OFFCategory(pre.CategoryHint); ok {
				if id, err := categoryID(ctx, q, cat); err != nil {
					return err
				} else if id != 0 {
					in.categoryID, in.catSource = &id, "off"
				}
			}
		default:
			in.name = "Unknown product " + last4(code)
			in.reason = ReasonBarcodeMinimal
			in.noCategoryRule = true
			if status == "unavailable" {
				in.reason = ReasonOFFUnavailable
			}
		}
		var err error
		p, err = s.insertProduct(ctx, q, in, now)
		if err != nil {
			return err
		}
		if status == "found" && pre.Nutrition != nil {
			if err := upsertOFFNutrition(ctx, q, p.ID, *pre.Nutrition); err != nil {
				return err
			}
		}
		if status == "unavailable" {
			return s.enqueueRefresh(ctx, q, refreshPayload{ProductID: p.ID, Tries: 1}, s.OFFRetryAfter)
		}
		return nil
	})
	if err != nil {
		if isUnique(err) { // lost a race with a concurrent scan of the same code
			if ex, gerr := s.q.GetProductByBarcode(ctx, store.Str(code)); gerr == nil {
				return BarcodeOutcome{Status: "known", Product: ex}, nil
			}
		}
		return BarcodeOutcome{}, err
	}
	if s.queue != nil && status == "unavailable" {
		s.queue.Notify()
	}
	if status == "found" && pre.ImageURL != "" && s.prod != nil {
		if img, err := s.off.FetchImage(ctx, pre.ImageURL); err != nil {
			s.log.Warn("fetch off image", "product", p.ID, "err", err)
		} else if _, err := s.prod.SetImage(ctx, p.ID, img); err != nil {
			s.log.Warn("store off image", "product", p.ID, "err", err)
		}
	}

	out := BarcodeOutcome{Status: "created", CreatedNow: true, OFFStatus: status}
	if status != "found" {
		out.Status = "created_minimal"
	}
	if sug, err := s.barcodeNameMatch(ctx, p); err != nil {
		return BarcodeOutcome{}, err
	} else if sug != nil {
		out.MergeSuggestion = sug
	}
	if p, err = s.q.GetProduct(ctx, p.ID); err != nil {
		return BarcodeOutcome{}, err
	}
	out.Product = p
	if s.prod != nil {
		if ul, err := s.prod.UnmatchedLinesFor(ctx, p.ID, 14); err == nil {
			if len(ul) > 5 {
				ul = ul[:5]
			}
			out.Unmatched = ul
		}
	}
	return out, nil
}

// barcodeNameMatch looks for a receipt-born product (no barcode) that is probably the same thing,
// stores it as suggested_merge_id and sets reason barcode_name_match (§4.3 step 3).
func (s *Service) barcodeNameMatch(ctx context.Context, p db.Product) (*SuggestedMerge, error) {
	cands, err := s.matcher.Search(ctx, p.Name, 0, 20, ReviewScore)
	if err != nil {
		return nil, err
	}
	for _, c := range cands {
		if c.ProductID == p.ID {
			continue
		}
		o, err := s.q.GetProduct(ctx, c.ProductID)
		if err != nil || o.Barcode.Valid || o.CreatedVia == "barcode" {
			continue
		}
		a, b := p.ID, o.ID
		if a > b {
			a, b = b, a
		}
		if n, err := s.q.IsDistinctPair(ctx, db.IsDistinctPairParams{AID: a, BID: b}); err != nil {
			return nil, err
		} else if n > 0 {
			continue
		}
		now := store.TS(s.now())
		if err := s.q.SetProductReview(ctx, db.SetProductReviewParams{NeedsReview: 1, ReviewReason: store.Str(ReasonBarcodeName),
			SuggestedMergeID: nid(o.ID), UpdatedAt: now, ID: p.ID}); err != nil {
			return nil, err
		}
		return &SuggestedMerge{ProductID: o.ID, Name: o.Name, NameEN: o.NameEn.String, Brand: o.Brand.String, Score: c.Score}, nil
	}
	return nil, nil
}

func (s *Service) enqueueRefresh(ctx context.Context, q *db.Queries, pl refreshPayload, after time.Duration) error {
	b, _ := json.Marshal(pl)
	now := s.now()
	_, err := q.EnqueueJob(ctx, db.EnqueueJobParams{Kind: JobOFFRefresh, Payload: string(b),
		RunAfter: store.TS(now.Add(after)), CreatedAt: store.TS(now), UpdatedAt: store.TS(now)})
	return err
}

// RegisterJobs installs the off_refresh handler on the queue.
func (s *Service) RegisterJobs(q *jobs.Queue) {
	s.queue = q
	s.registerBackfill(q)
	q.Register(JobOFFRefresh, jobs.Handler{Run: func(ctx context.Context, payload []byte) error {
		var pl refreshPayload
		if err := json.Unmarshal(payload, &pl); err != nil {
			return jobs.Permanent(err)
		}
		_, status, err := s.refresh(ctx, pl.ProductID)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
			return nil // product gone or no barcode: nothing to refresh
		}
		if err != nil {
			return err
		}
		if status == "unavailable" && pl.Tries < maxRefreshTries {
			return s.enqueueRefresh(ctx, s.q, refreshPayload{ProductID: pl.ProductID, Tries: pl.Tries + 1}, s.OFFRetryAfter)
		}
		return nil
	}})
}

// Refresh retries Open Food Facts now for a product with a barcode (POST /v1/products/{id}/refresh).
func (s *Service) Refresh(ctx context.Context, id int64) (db.Product, string, error) {
	return s.refresh(ctx, id)
}

// refresh fills only the empty fields of the product from Open Food Facts; a name the user typed is
// never replaced (only the "Unknown product …" placeholder is).
func (s *Service) refresh(ctx context.Context, id int64) (db.Product, string, error) {
	p, err := s.q.GetProduct(ctx, id)
	if err != nil {
		return db.Product{}, "", notFound(err)
	}
	if !p.Barcode.Valid {
		return db.Product{}, "", errf(ErrInvalid, "this product has no barcode")
	}
	offCtx, cancel := context.WithTimeout(ctx, s.OFFTimeout)
	pre, offErr := s.off.Lookup(offCtx, p.Barcode.String)
	cancel()
	now := store.TS(s.now())
	switch {
	case errors.Is(offErr, openfoodfacts.ErrNotFound):
		if p.ReviewReason.String == ReasonOFFUnavailable {
			if err := s.q.SetProductReview(ctx, db.SetProductReviewParams{NeedsReview: 1, ReviewReason: store.Str(ReasonBarcodeMinimal), UpdatedAt: now, ID: id}); err != nil {
				return p, "", err
			}
		}
		p, err = s.q.GetProduct(ctx, id)
		return p, "not_found", err
	case offErr != nil:
		return p, "unavailable", nil
	}
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		if strings.HasPrefix(p.Name, "Unknown product ") && pre.Name != "" {
			if err := q.SetProductNameManual(ctx, db.SetProductNameManualParams{Name: pre.Name, UpdatedAt: now, ID: id}); err != nil {
				return err
			}
		}
		fill := db.FillProductFieldsParams{Barcode: p.Barcode, Brand: p.Brand, ImagePath: p.ImagePath, NetQuantity: p.NetQuantity,
			NetUnit: p.NetUnit, OffCode: p.OffCode, CategoryID: p.CategoryID, CategorySource: p.CategorySource, UpdatedAt: now, ID: id}
		if !p.Brand.Valid && pre.Brand != "" {
			fill.Brand = store.Str(pre.Brand)
		}
		if !p.NetQuantity.Valid && pre.NetQuantity != nil {
			fill.NetQuantity, fill.NetUnit = sql.NullFloat64{Float64: *pre.NetQuantity, Valid: true}, store.Str(pre.NetUnit)
		}
		if !p.OffCode.Valid {
			fill.OffCode = store.Str(p.Barcode.String)
		}
		if !p.CategoryID.Valid {
			if cat, ok := OFFCategory(pre.CategoryHint); ok {
				if cid, err := categoryID(ctx, q, cat); err != nil {
					return err
				} else if cid != 0 {
					fill.CategoryID, fill.CategorySource = nid(cid), store.Str("off")
				}
			}
		}
		if err := q.FillProductFields(ctx, fill); err != nil {
			return err
		}
		if pre.NameEN != "" && !p.NameEnSource.Valid {
			if err := q.SetProductEnglish(ctx, db.SetProductEnglishParams{NameEn: store.Str(pre.NameEN), NameEnSource: store.Str("openfoodfacts"), ID: id}); err != nil {
				return err
			}
		}
		if _, err := q.GetNutrition(ctx, id); errors.Is(err, sql.ErrNoRows) && pre.Nutrition != nil {
			if err := upsertOFFNutrition(ctx, q, id, *pre.Nutrition); err != nil {
				return err
			}
		}
		// the reason is answered once the details are in
		if p.ReviewReason.String == ReasonOFFUnavailable || p.ReviewReason.String == ReasonBarcodeMinimal {
			review, reason := int64(0), sql.NullString{}
			if !fill.CategoryID.Valid {
				review, reason = 1, store.Str(ReasonNoCategory)
			}
			return q.SetProductReview(ctx, db.SetProductReviewParams{NeedsReview: review, ReviewReason: reason, SuggestedMergeID: p.SuggestedMergeID, UpdatedAt: now, ID: id})
		}
		return nil
	})
	if err != nil {
		return p, "", err
	}
	if p.ImagePath.Valid == false && pre.ImageURL != "" && s.prod != nil {
		if img, err := s.off.FetchImage(ctx, pre.ImageURL); err == nil {
			_, _ = s.prod.SetImage(ctx, id, img)
		}
	}
	p, err = s.q.GetProduct(ctx, id)
	return p, "found", err
}

func categoryID(ctx context.Context, q *db.Queries, name string) (int64, error) {
	cats, err := q.ListCategories(ctx)
	if err != nil {
		return 0, err
	}
	for _, c := range cats {
		if c.Name == name {
			return c.ID, nil
		}
	}
	return 0, nil
}

func upsertOFFNutrition(ctx context.Context, q *db.Queries, id int64, n openfoodfacts.Nutrition) error {
	f := func(p *float64) sql.NullFloat64 {
		if p == nil {
			return sql.NullFloat64{}
		}
		return sql.NullFloat64{Float64: *p, Valid: true}
	}
	_, err := q.UpsertNutrition(ctx, db.UpsertNutritionParams{ProductID: id, Per: n.Per, EnergyKcal: f(n.EnergyKcal), Fat: f(n.Fat),
		SaturatedFat: f(n.SaturatedFat), Carbs: f(n.Carbs), Sugars: f(n.Sugars), Fibre: f(n.Fibre), Protein: f(n.Protein), Salt: f(n.Salt), Source: "off"})
	return err
}

func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
