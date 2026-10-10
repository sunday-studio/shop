package autoprod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"shop/internal/jobs"
	"shop/internal/match"
	"shop/internal/openfoodfacts"
	"shop/internal/products"
	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/translate"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
	ErrDisabled = errors.New("automatic products are off")
	ErrGone     = errors.New("gone")
)

// Thresholds from docs/PRODUCT-AUTOPOPULATE.md §3.2.
const (
	AutoLinkScore  = 0.92
	ReviewScore    = 0.75
	UndoWindowDays = 30
)

// Review reasons, in queue order.
const (
	ReasonNearMatch      = "near_match"
	ReasonBarcodeName    = "barcode_name_match"
	ReasonOddName        = "odd_name"
	ReasonNoCategory     = "no_category"
	ReasonBarcodeMinimal = "barcode_minimal"
	ReasonOFFUnavailable = "barcode_off_unavailable"
)

var Reasons = []string{ReasonNearMatch, ReasonBarcodeName, ReasonOddName, ReasonNoCategory, ReasonBarcodeMinimal, ReasonOFFUnavailable}

const JobOFFRefresh = "off_refresh"

type Service struct {
	conn    *sql.DB
	q       *db.Queries
	matcher *match.Matcher
	prod    *products.Service
	off     *openfoodfacts.Client
	queue   *jobs.Queue
	log     *slog.Logger
	enabled bool
	now     func() time.Time

	// OFFTimeout bounds the Open Food Facts call of a barcode scan (5 s).
	OFFTimeout time.Duration
	// OFFRetryAfter is the delay before the first off_refresh retry (10 min).
	OFFRetryAfter time.Duration
}

// New builds the service. enabled is the AUTO_PRODUCTS flag: it gates receipt confirm and backfill only.
func New(conn *sql.DB, matcher *match.Matcher, prod *products.Service, off *openfoodfacts.Client, queue *jobs.Queue, log *slog.Logger, enabled bool) *Service {
	return &Service{conn: conn, q: db.New(conn), matcher: matcher, prod: prod, off: off, queue: queue, log: log,
		enabled: enabled, now: time.Now, OFFTimeout: 5 * time.Second, OFFRetryAfter: 10 * time.Minute}
}

// Enabled reports the AUTO_PRODUCTS flag.
func (s *Service) Enabled() bool { return s.enabled }

// Stats counts what create_or_link did.
type Stats struct {
	Linked      int    `json:"linked"`
	Created     int    `json:"created"`
	NeedsReview int    `json:"needs_review"`
	Skipped     int    `json:"skipped"`
	Error       string `json:"error,omitempty"`
}

func (a *Stats) add(b Stats) {
	a.Linked += b.Linked
	a.Created += b.Created
	a.NeedsReview += b.NeedsReview
	a.Skipped += b.Skipped
}

// ProcessReceipt links or creates a product for every unlinked item line of a receipt (§3.2) in one
// transaction. via is "receipt" or "backfill". With dry the work is rolled back after counting.
func (s *Service) ProcessReceipt(ctx context.Context, receiptID int64, via string) (Stats, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return Stats{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	st, err := s.processReceiptTx(ctx, tx, receiptID, via)
	if err != nil {
		return Stats{}, err
	}
	return st, tx.Commit()
}

func (s *Service) processReceiptTx(ctx context.Context, tx *sql.Tx, receiptID int64, via string) (Stats, error) {
	q := db.New(tx)
	m := s.matcher.On(tx)
	rec, err := q.GetReceipt(ctx, receiptID)
	if err != nil {
		return Stats{}, err
	}
	lines, err := q.ItemLinesToLink(ctx, receiptID)
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	for _, l := range lines {
		o, err := s.createOrLink(ctx, q, m, rec, l, via)
		if err != nil {
			return Stats{}, err
		}
		st.add(o)
	}
	return st, nil
}

// createOrLink handles one item line.
func (s *Service) createOrLink(ctx context.Context, q *db.Queries, m *match.Matcher, rec db.Receipt, line db.ReceiptLine, via string) (Stats, error) {
	c := CleanLine(line.RawText)
	if c.Skip || c.Norm == "" {
		return Stats{Skipped: 1}, nil
	}
	now := store.TS(s.now())
	link := func(productID int64) error {
		_, err := q.SetLineProduct(ctx, db.SetLineProductParams{ProductID: sql.NullInt64{Int64: productID, Valid: true}, ID: line.ID})
		return err
	}
	writeAlias := func(productID int64) error {
		for _, norm := range uniq(c.Norm, line.NormText) {
			if norm == "" {
				continue
			}
			if err := upsertAlias(ctx, q, rec.ShopID, norm, productID, now); err != nil {
				return err
			}
		}
		return nil
	}

	// 1. alias on the cleaned or the raw text
	for _, norm := range uniq(c.Norm, line.NormText) {
		if norm == "" {
			continue
		}
		pid, err := q.LookupAlias(ctx, db.LookupAliasParams{NormText: norm, ShopID: rec.ShopID})
		if err == nil {
			return Stats{Linked: 1}, link(pid)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Stats{}, err
		}
	}

	// 2. fuzzy
	// shopID 0: the matcher's same-shop alias bonus (+0.05) would push a 0.87 near match over the 0.92
	// auto-link line; the thresholds of the spec are on the plain score.
	const shop = int64(0)
	cands, err := m.Search(ctx, c.Name, shop, 20, ReviewScore)
	if err != nil {
		return Stats{}, err
	}
	var best *match.Candidate
	if len(cands) > 0 {
		best = &cands[0]
	}
	if best != nil && best.Score >= AutoLinkScore {
		if err := link(best.ProductID); err != nil {
			return Stats{}, err
		}
		return Stats{Linked: 1}, writeAlias(best.ProductID)
	}

	// 3. create
	in := newProduct{name: c.Name, createdVia: via, odd: c.Odd}
	if best != nil {
		in.suggested = best.ProductID
		in.reason = ReasonNearMatch
	}
	p, err := s.insertProduct(ctx, q, in, now)
	if err != nil {
		return Stats{}, err
	}
	if err := link(p.ID); err != nil {
		return Stats{}, err
	}
	if err := writeAlias(p.ID); err != nil {
		return Stats{}, err
	}
	st := Stats{Created: 1}
	if p.NeedsReview != 0 {
		st.NeedsReview = 1
	}
	return st, nil
}

type newProduct struct {
	name           string
	brand          string
	barcode        string
	offCode        string
	netQty         *float64
	netUnit        string
	nameEN         string // given (Open Food Facts)
	createdVia     string
	reason         string // wins over no_category / odd_name
	suggested      int64
	odd            bool
	noReview       bool   // made by the user: never queued
	noCategoryRule bool   // skip the keyword rules (placeholder names)
	categoryID     *int64 // already decided (OFF mapping); else keyword rules
	catSource      string
}

// insertProduct creates the row with category, English name and review state per §3.2.
func (s *Service) insertProduct(ctx context.Context, q *db.Queries, in newProduct, now string) (db.Product, error) {
	arg := db.InsertAutoProductParams{
		Name: in.name, CreatedVia: in.createdVia, CreatedAt: now, UpdatedAt: now,
		Brand: store.NullStr(strPtr(in.brand)), Barcode: store.NullStr(strPtr(in.barcode)), OffCode: store.NullStr(strPtr(in.offCode)),
	}
	if in.netQty != nil {
		arg.NetQuantity = sql.NullFloat64{Float64: *in.netQty, Valid: true}
		arg.NetUnit = store.NullStr(strPtr(in.netUnit))
	}
	switch {
	case in.nameEN != "":
		arg.NameEn, arg.NameEnSource = store.Str(in.nameEN), store.Str("openfoodfacts")
	default:
		if t := translate.Translate(in.name); t.OK() {
			arg.NameEn, arg.NameEnSource = store.Str(t.Text), store.Str("dictionary")
		}
	}
	if in.categoryID != nil {
		arg.CategoryID, arg.CategorySource = store.NullInt(in.categoryID), store.Str(in.catSource)
	} else if in.noCategoryRule {
		// leave uncategorised
	} else if id, ok, err := GuessCategory(ctx, q, in.name); err != nil {
		return db.Product{}, err
	} else if ok {
		arg.CategoryID, arg.CategorySource = sql.NullInt64{Int64: id, Valid: true}, store.Str("rule")
	}
	reason := in.reason
	if reason == "" && in.odd {
		reason = ReasonOddName
	}
	if reason == "" && !arg.CategoryID.Valid {
		reason = ReasonNoCategory
	}
	if in.noReview {
		reason = ""
	}
	if reason != "" {
		arg.NeedsReview, arg.ReviewReason = 1, store.Str(reason)
	}
	if in.suggested != 0 {
		arg.SuggestedMergeID = sql.NullInt64{Int64: in.suggested, Valid: true}
	}
	return q.InsertAutoProduct(ctx, arg)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

// upsertAlias writes (shop, norm) -> product; with no shop it writes a global alias (UNIQUE does not
// cover NULL, so those are deduplicated by hand).
func upsertAlias(ctx context.Context, q *db.Queries, shop sql.NullInt64, norm string, productID int64, now string) error {
	if shop.Valid {
		_, err := q.UpsertAlias(ctx, db.UpsertAliasParams{ShopID: shop, NormText: norm, ProductID: productID, CreatedAt: now})
		return err
	}
	if _, err := q.FindGlobalAlias(ctx, norm); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err := q.UpsertAlias(ctx, db.UpsertAliasParams{NormText: norm, ProductID: productID, CreatedAt: now})
	return err
}

func fold(s string) string { return strings.TrimSpace(match.Fold(s)) }

func errf(base error, format string, a ...any) error {
	return fmt.Errorf("%w: %s", base, fmt.Sprintf(format, a...))
}

// Similarity is the match score of two product names.
func Similarity(a, b string) float64 { return match.Score(match.Normalise(a), match.Normalise(b)) }

// SetEnabled switches the AUTO_PRODUCTS behaviour (tests).
func (s *Service) SetEnabled(on bool) { s.enabled = on }
