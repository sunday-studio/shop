package autoprod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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
	gloss   *translate.Glossary // the household's taught words; nil = built-in glossary only

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

// ProcessReceipt links or creates a product for every unlinked item line of a receipt (§3.2). via is
// "receipt" or "backfill".
//
// The matching (the slow part, many reads) runs first without a transaction; only the resulting writes
// run in one short transaction, so a background run never holds the database's single write lock for
// longer than a few milliseconds and the app's own writes are not starved.
func (s *Service) ProcessReceipt(ctx context.Context, receiptID int64, via string) (Stats, error) {
	rec, lines, err := s.loadForLinking(ctx, s.q, receiptID)
	if err != nil {
		return Stats{}, err
	}
	pl, err := s.planLinks(ctx, s.q, s.matcher, rec, lines, via)
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	err = store.InTx(ctx, s.conn, func(q *db.Queries) error {
		var err error
		st, err = s.applyPlan(ctx, q, rec, pl, via)
		return err
	})
	return st, err
}

// processReceiptTx does plan and apply inside the caller's transaction, so later receipts of a dry run see
// the products earlier ones would have created.
func (s *Service) processReceiptTx(ctx context.Context, tx *sql.Tx, receiptID int64, via string) (Stats, error) {
	q := db.New(tx)
	rec, lines, err := s.loadForLinking(ctx, q, receiptID)
	if err != nil {
		return Stats{}, err
	}
	pl, err := s.planLinks(ctx, q, s.matcher.On(tx), rec, lines, via)
	if err != nil {
		return Stats{}, err
	}
	return s.applyPlan(ctx, q, rec, pl, via)
}

func (s *Service) loadForLinking(ctx context.Context, q *db.Queries, receiptID int64) (db.Receipt, []db.ReceiptLine, error) {
	rec, err := q.GetReceipt(ctx, receiptID)
	if err != nil {
		return db.Receipt{}, nil, err
	}
	lines, err := q.ItemLinesToLink(ctx, receiptID)
	return rec, lines, err
}

// step is what to do with one line.
type step struct {
	line     db.ReceiptLine
	skip     bool
	existing int64 // link to this existing product
	pending  int   // link to the product created by step creates[pending-1] earlier in this plan
	create   bool  // this line creates a product
	in       newProduct
	sugExist int64 // near match: suggested merge with an existing product
	sugPend  int   // near match: suggested merge with a pending one (1-based)
	norms    []string
	viaAlias bool // found by an alias: nothing new to learn
}

type plan struct {
	steps   []step
	creates []string // normalised names of the products the plan creates, in order
}

// planLinks decides, for each line, to link to an existing product, to link to a product created by an
// earlier line of the same receipt, or to create one. It only reads.
func (s *Service) planLinks(ctx context.Context, q *db.Queries, m *match.Matcher, rec db.Receipt, lines []db.ReceiptLine, via string) (plan, error) {
	var pl plan
	localAlias := map[string]int{} // norm -> 1-based index into pl.creates
	for _, line := range lines {
		c := CleanLineWith(s.gloss, line.RawText)
		if c.Skip || c.Norm == "" {
			pl.steps = append(pl.steps, step{line: line, skip: true})
			continue
		}
		st := step{line: line}
		for _, norm := range uniq(c.Norm, line.NormText) {
			if norm != "" {
				st.norms = append(st.norms, norm)
			}
		}
		// 1. alias on the cleaned or the raw text
		found := false
		for _, norm := range st.norms {
			if idx, ok := localAlias[norm]; ok {
				st.pending, found = idx, true
				break
			}
			pid, err := q.LookupAlias(ctx, db.LookupAliasParams{NormText: norm, ShopID: rec.ShopID})
			if err == nil {
				st.existing, found = pid, true
				break
			} else if !errors.Is(err, sql.ErrNoRows) {
				return plan{}, err
			}
		}
		if found {
			st.viaAlias = true
			pl.steps = append(pl.steps, st)
			continue
		}

		// 2. fuzzy: against the catalogue and against products this receipt is about to create.
		// shopID 0: the matcher's same-shop alias bonus (+0.05) would push a 0.87 near match over the 0.92
		// auto-link line; the thresholds of the spec are on the plain score.
		cands, err := m.Search(ctx, c.Name, 0, 20, ReviewScore)
		if err != nil {
			return plan{}, err
		}
		bestScore, bestExist, bestPend := 0.0, int64(0), 0
		if len(cands) > 0 {
			bestScore, bestExist = cands[0].Score, cands[0].ProductID
		}
		for i, n := range pl.creates {
			if sc := match.Score(c.Norm, n); sc > bestScore && sc >= ReviewScore {
				bestScore, bestExist, bestPend = sc, 0, i+1
			}
		}
		if bestScore >= AutoLinkScore {
			st.existing, st.pending = bestExist, bestPend
			for _, norm := range st.norms {
				if bestPend > 0 {
					localAlias[norm] = bestPend
				}
			}
			pl.steps = append(pl.steps, st)
			continue
		}

		// 3. create
		st.create = true
		st.in = newProduct{name: c.Name, createdVia: via, odd: c.Odd}
		if bestScore >= ReviewScore {
			st.in.reason = ReasonNearMatch
			st.sugExist, st.sugPend = bestExist, bestPend
		}
		pl.creates = append(pl.creates, c.Norm)
		for _, norm := range st.norms {
			localAlias[norm] = len(pl.creates)
		}
		pl.steps = append(pl.steps, st)
	}
	return pl, nil
}

// applyPlan writes a plan: products, line links and aliases.
func (s *Service) applyPlan(ctx context.Context, q *db.Queries, rec db.Receipt, pl plan, via string) (Stats, error) {
	now := store.TS(s.now())
	var st Stats
	created := make([]int64, 0, len(pl.creates))
	link := func(lineID, productID int64) error {
		_, err := q.SetLineProduct(ctx, db.SetLineProductParams{ProductID: sql.NullInt64{Int64: productID, Valid: true}, ID: lineID})
		return err
	}
	aliases := func(norms []string, productID int64) error {
		for _, norm := range norms {
			if err := upsertAlias(ctx, q, rec.ShopID, norm, productID, now); err != nil {
				return err
			}
		}
		return nil
	}
	for _, step := range pl.steps {
		switch {
		case step.skip:
			st.Skipped++
		case step.create:
			in := step.in
			switch {
			case step.sugPend > 0:
				in.suggested = created[step.sugPend-1]
			case step.sugExist > 0:
				in.suggested = step.sugExist
			}
			p, err := s.insertProduct(ctx, q, in, now)
			if err != nil {
				return Stats{}, err
			}
			created = append(created, p.ID)
			if err := link(step.line.ID, p.ID); err != nil {
				return Stats{}, err
			}
			if err := aliases(step.norms, p.ID); err != nil {
				return Stats{}, err
			}
			st.Created++
			if p.NeedsReview != 0 {
				st.NeedsReview++
			}
		default:
			pid := step.existing
			if step.pending > 0 {
				pid = created[step.pending-1]
			} else if _, err := q.GetProduct(ctx, pid); err != nil {
				// merged or deleted since the plan was made: leave the line, the next run decides again
				if errors.Is(err, sql.ErrNoRows) {
					st.Skipped++
					continue
				}
				return Stats{}, err
			}
			if err := link(step.line.ID, pid); err != nil {
				return Stats{}, err
			}
			// a link found through an alias already has its alias; a fuzzy link learns this spelling
			if !step.viaAlias {
				if err := aliases(step.norms, pid); err != nil {
					return Stats{}, err
				}
			}
			st.Linked++
		}
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

// UseGlossary makes automatic English names use the household's taught words.
func (s *Service) UseGlossary(g *translate.Glossary) { s.gloss = g }

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
		if t := s.gloss.Translate(in.name); t.OK() {
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

func errf(base error, format string, a ...any) error {
	return fmt.Errorf("%w: %s", base, fmt.Sprintf(format, a...))
}

// Similarity is the match score of two product names.
func Similarity(a, b string) float64 { return match.Score(match.Normalise(a), match.Normalise(b)) }

// SetEnabled switches the AUTO_PRODUCTS behaviour (tests).
func (s *Service) SetEnabled(on bool) { s.enabled = on }
