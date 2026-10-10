// Package products manages the product catalog: CRUD, images, nutrition,
// barcode lookup via Open Food Facts, and linking receipt lines (aliases).
package products

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"pantry/internal/match"
	"pantry/internal/openfoodfacts"
	"pantry/internal/store"
	"pantry/internal/store/db"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
)

type Service struct {
	conn      *sql.DB
	q         *db.Queries
	matcher   *match.Matcher
	foodFacts *openfoodfacts.Client
	imagesDir string
	log       *slog.Logger
}

func New(conn *sql.DB, matcher *match.Matcher, foodFacts *openfoodfacts.Client, imagesDir string, log *slog.Logger) *Service {
	return &Service{conn: conn, q: db.New(conn), matcher: matcher, foodFacts: foodFacts, imagesDir: imagesDir, log: log}
}

// Input is a product as entered by the user (or prefilled from OFF).
type Input struct {
	Name        string
	Brand       *string
	CategoryID  *int64
	Barcode     *string
	NetQuantity *float64
	NetUnit     *string
	OFFCode     *string
	ImageURL    *string // OFF image to download on create
	Nutrition   *Nutrition
}

type Nutrition struct {
	Per          string
	EnergyKcal   *float64
	Fat          *float64
	SaturatedFat *float64
	Carbs        *float64
	Sugars       *float64
	Fibre        *float64
	Protein      *float64
	Salt         *float64
	Source       string // off | manual
}

// Product bundles a product row with its nutrition.
type Product struct {
	db.Product
	Nutrition *db.Nutrition
}

func (in *Input) validate(ctx context.Context, q *db.Queries) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if in.Barcode != nil {
		b := strings.TrimSpace(*in.Barcode)
		if b == "" {
			in.Barcode = nil
		} else if !openfoodfacts.ValidBarcode(b) {
			return fmt.Errorf("%w: barcode must be 8 or 12-14 digits", ErrInvalid)
		} else {
			in.Barcode = &b
		}
	}
	if in.NetUnit != nil && !slices.Contains([]string{"g", "ml", "pc"}, *in.NetUnit) {
		return fmt.Errorf("%w: net_unit must be g, ml or pc", ErrInvalid)
	}
	if (in.NetQuantity == nil) != (in.NetUnit == nil) {
		return fmt.Errorf("%w: net_quantity and net_unit go together", ErrInvalid)
	}
	if in.NetQuantity != nil && *in.NetQuantity <= 0 {
		return fmt.Errorf("%w: net_quantity must be positive", ErrInvalid)
	}
	if in.CategoryID != nil {
		if _, err := q.GetCategory(ctx, *in.CategoryID); err != nil {
			return fmt.Errorf("%w: category %d does not exist", ErrInvalid, *in.CategoryID)
		}
	}
	if in.ImageURL != nil && !openfoodfacts.AllowedImageURL(*in.ImageURL) {
		return fmt.Errorf("%w: image_url must be an https Open Food Facts image", ErrInvalid)
	}
	if in.Nutrition != nil {
		if err := in.Nutrition.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (n *Nutrition) validate() error {
	if n.Per == "" {
		n.Per = "100g"
	}
	if n.Per != "100g" && n.Per != "100ml" {
		return fmt.Errorf("%w: nutrition per must be 100g or 100ml", ErrInvalid)
	}
	if n.Source == "" {
		n.Source = "manual"
	}
	if n.Source != "manual" && n.Source != "off" {
		return fmt.Errorf("%w: nutrition source must be manual or off", ErrInvalid)
	}
	for _, v := range []*float64{n.EnergyKcal, n.Fat, n.SaturatedFat, n.Carbs, n.Sugars, n.Fibre, n.Protein, n.Salt} {
		if v != nil && *v < 0 {
			return fmt.Errorf("%w: nutrition values must be >= 0", ErrInvalid)
		}
	}
	return nil
}

func floatNull(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func (s *Service) Create(ctx context.Context, in Input) (Product, error) {
	if err := in.validate(ctx, s.q); err != nil {
		return Product{}, err
	}
	var p db.Product
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		now := store.Now()
		var err error
		p, err = q.CreateProduct(ctx, db.CreateProductParams{
			Name: in.Name, Brand: store.NullStr(in.Brand), CategoryID: store.NullInt(in.CategoryID),
			Barcode: store.NullStr(in.Barcode), NetQuantity: floatNull(in.NetQuantity),
			NetUnit: store.NullStr(in.NetUnit), OffCode: store.NullStr(in.OFFCode),
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: a product with barcode %s exists", ErrConflict, *in.Barcode)
			}
			return err
		}
		if in.Nutrition != nil {
			return upsertNutrition(ctx, q, p.ID, *in.Nutrition)
		}
		return nil
	})
	if err != nil {
		return Product{}, err
	}
	if in.ImageURL != nil {
		// Best effort: the product is useful without its photo.
		if img, err := s.foodFacts.FetchImage(ctx, *in.ImageURL); err != nil {
			s.log.Warn("fetch off image", "product", p.ID, "err", err)
		} else if _, err := s.SetImage(ctx, p.ID, img); err != nil {
			s.log.Warn("store off image", "product", p.ID, "err", err)
		}
	}
	return s.Get(ctx, p.ID)
}

func upsertNutrition(ctx context.Context, q *db.Queries, id int64, n Nutrition) error {
	_, err := q.UpsertNutrition(ctx, db.UpsertNutritionParams{
		ProductID: id, Per: n.Per, EnergyKcal: floatNull(n.EnergyKcal), Fat: floatNull(n.Fat),
		SaturatedFat: floatNull(n.SaturatedFat), Carbs: floatNull(n.Carbs), Sugars: floatNull(n.Sugars),
		Fibre: floatNull(n.Fibre), Protein: floatNull(n.Protein), Salt: floatNull(n.Salt), Source: n.Source,
	})
	return err
}

func (s *Service) Get(ctx context.Context, id int64) (Product, error) {
	p, err := s.q.GetProduct(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Product{}, ErrNotFound
	} else if err != nil {
		return Product{}, err
	}
	out := Product{Product: p}
	if n, err := s.q.GetNutrition(ctx, id); err == nil {
		out.Nutrition = &n
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	return out, nil
}

// Update replaces the editable fields. Nutrition is only touched when given.
func (s *Service) Update(ctx context.Context, id int64, in Input) (Product, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return Product{}, err
	}
	if err := in.validate(ctx, s.q); err != nil {
		return Product{}, err
	}
	err := store.InTx(ctx, s.conn, func(q *db.Queries) error {
		_, err := q.UpdateProduct(ctx, db.UpdateProductParams{
			Name: in.Name, Brand: store.NullStr(in.Brand), CategoryID: store.NullInt(in.CategoryID),
			Barcode: store.NullStr(in.Barcode), NetQuantity: floatNull(in.NetQuantity),
			NetUnit: store.NullStr(in.NetUnit), UpdatedAt: store.Now(), ID: id,
		})
		if isUnique(err) {
			return fmt.Errorf("%w: barcode already used by another product", ErrConflict)
		}
		if err != nil {
			return err
		}
		if in.Nutrition != nil {
			return upsertNutrition(ctx, q, id, *in.Nutrition)
		}
		return nil
	})
	if err != nil {
		return Product{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	now := store.Now()
	n, err := s.q.SoftDeleteProduct(ctx, db.SoftDeleteProductParams{DeletedAt: store.Str(now), UpdatedAt: now, ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) SetNutrition(ctx context.Context, id int64, n *Nutrition) (Product, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return Product{}, err
	}
	if n == nil {
		if err := s.q.DeleteNutrition(ctx, id); err != nil {
			return Product{}, err
		}
		return s.Get(ctx, id)
	}
	if err := n.validate(); err != nil {
		return Product{}, err
	}
	if err := upsertNutrition(ctx, s.q, id, *n); err != nil {
		return Product{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) SetImage(ctx context.Context, id int64, data []byte) (Product, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Product{}, err
	}
	img, err := normaliseImage(data)
	if err != nil {
		return Product{}, err
	}
	rel, err := saveImage(s.imagesDir, img)
	if err != nil {
		return Product{}, err
	}
	if err := s.q.SetProductImage(ctx, db.SetProductImageParams{ImagePath: store.Str(rel), UpdatedAt: store.Now(), ID: id}); err != nil {
		return Product{}, err
	}
	if old := cur.ImagePath.String; cur.ImagePath.Valid && old != rel {
		_ = os.Remove(filepath.Join(s.imagesDir, old))
	}
	return s.Get(ctx, id)
}

func (s *Service) ImagePath(p Product) string {
	if !p.ImagePath.Valid {
		return ""
	}
	return filepath.Join(s.imagesDir, p.ImagePath.String)
}

// SearchResult is a product with its match score (1 for plain listing).
type SearchResult struct {
	db.Product
	Score float64
}

// List returns products; with a query they are ranked by fuzzy score.
func (s *Service) List(ctx context.Context, query string, categoryID *int64, limit, offset int) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		arg := db.ListProductsParams{Lim: int64(limit), Off: int64(offset)}
		if categoryID != nil {
			arg.CategoryID = *categoryID
		}
		ps, err := s.q.ListProducts(ctx, arg)
		if err != nil {
			return nil, err
		}
		out := make([]SearchResult, len(ps))
		for i, p := range ps {
			out[i] = SearchResult{Product: p, Score: 1}
		}
		return out, nil
	}

	cands, err := s.matcher.Search(ctx, query, 0, 0, 0.5)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.ProductID
	}
	if len(ids) == 0 {
		return nil, nil
	}
	ps, err := s.q.GetProductsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]db.Product{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	var out []SearchResult
	for _, c := range cands {
		p, ok := byID[c.ProductID]
		if !ok || (categoryID != nil && (!p.CategoryID.Valid || p.CategoryID.Int64 != *categoryID)) {
			continue
		}
		out = append(out, SearchResult{Product: p, Score: c.Score})
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// BarcodeResult is either a known local product or an OFF prefill.
type BarcodeResult struct {
	Local   *Product
	Prefill *openfoodfacts.Product
}

func (s *Service) Barcode(ctx context.Context, code string) (BarcodeResult, error) {
	if !openfoodfacts.ValidBarcode(code) {
		return BarcodeResult{}, fmt.Errorf("%w: barcode must be 8 or 12-14 digits", ErrInvalid)
	}
	if p, err := s.q.GetProductByBarcode(ctx, store.Str(code)); err == nil {
		full, err := s.Get(ctx, p.ID)
		return BarcodeResult{Local: &full}, err
	} else if !errors.Is(err, sql.ErrNoRows) {
		return BarcodeResult{}, err
	}
	pre, err := s.foodFacts.Lookup(ctx, code)
	if errors.Is(err, openfoodfacts.ErrNotFound) {
		return BarcodeResult{}, ErrNotFound
	}
	if err != nil {
		return BarcodeResult{}, err
	}
	return BarcodeResult{Prefill: &pre}, nil
}

// LinkResult reports what a link did.
type LinkResult struct {
	Line         db.ReceiptLine
	AlsoLinked   int64 // other lines updated through the new alias
	AliasCreated bool
}

// LinkLine sets (or clears, with productID nil) a line's product. With
// remember, the line's text at that shop becomes an alias so future
// receipts — and other unmatched lines — link automatically.
func (s *Service) LinkLine(ctx context.Context, lineID int64, productID *int64, remember bool) (LinkResult, error) {
	var res LinkResult
	line, err := s.q.GetLine(ctx, lineID)
	if errors.Is(err, sql.ErrNoRows) {
		return res, ErrNotFound
	} else if err != nil {
		return res, err
	}
	if productID != nil {
		if _, err := s.q.GetProduct(ctx, *productID); err != nil {
			return res, fmt.Errorf("%w: product %d does not exist", ErrInvalid, *productID)
		}
	}
	rec, err := s.q.GetReceipt(ctx, line.ReceiptID)
	if err != nil {
		return res, err
	}
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		res.Line, err = q.SetLineProduct(ctx, db.SetLineProductParams{ProductID: store.NullInt(productID), ID: lineID})
		if err != nil || productID == nil || !remember || line.NormText == "" {
			return err
		}
		if _, err := q.UpsertAlias(ctx, db.UpsertAliasParams{
			ShopID: sql.NullInt64{Int64: rec.ShopID, Valid: true}, NormText: line.NormText,
			ProductID: *productID, CreatedAt: store.Now(),
		}); err != nil {
			return err
		}
		res.AliasCreated = true
		res.AlsoLinked, err = q.LinkMatchingLines(ctx, db.LinkMatchingLinesParams{
			ProductID: sql.NullInt64{Int64: *productID, Valid: true}, NormText: line.NormText, ShopID: rec.ShopID,
		})
		return err
	})
	return res, err
}

// UnmatchedLine is a recent receipt line without a product, scored against
// a product so the app can offer "is this one of these?".
type UnmatchedLine struct {
	db.RecentUnmatchedLinesRow
	Score float64
}

func (s *Service) UnmatchedLinesFor(ctx context.Context, productID int64, days int) ([]UnmatchedLine, error) {
	p, err := s.Get(ctx, productID)
	if err != nil {
		return nil, err
	}
	since := store.TS(time.Now().AddDate(0, 0, -days))
	rows, err := s.q.RecentUnmatchedLines(ctx, store.Str(since))
	if err != nil {
		return nil, err
	}
	name := match.Normalise(p.Name + " " + p.Brand.String)
	nameOnly := match.Normalise(p.Name)
	out := make([]UnmatchedLine, len(rows))
	for i, r := range rows {
		out[i] = UnmatchedLine{RecentUnmatchedLinesRow: r,
			Score: max(match.Score(r.NormText, name), match.Score(r.NormText, nameOnly))}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func (s *Service) Aliases(ctx context.Context, productID int64) ([]db.ListAliasesForProductRow, error) {
	if _, err := s.Get(ctx, productID); err != nil {
		return nil, err
	}
	return s.q.ListAliasesForProduct(ctx, productID)
}

func (s *Service) DeleteAlias(ctx context.Context, id int64) error {
	n, err := s.q.DeleteAlias(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Suggest proxies the matcher for receipt lines.
func (s *Service) Suggest(ctx context.Context, shopID int64, text string) ([]match.Candidate, error) {
	return s.matcher.Suggest(ctx, shopID, text)
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
