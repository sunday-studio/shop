package receipts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"shop/internal/autoprod"
	"shop/internal/match"
	"shop/internal/receipt"
	"shop/internal/store"
	"shop/internal/store/db"
	"shop/internal/translate"
)

// Scan-time matching: when a receipt is processed, every item line is run through the matcher.
//
//	exact alias, or score >= AutoLinkScore  -> the line is linked (match_source alias|score)
//	SuggestFloor <= score < AutoLinkScore    -> the best 3 candidates are stored on the line as suggestions
//	below                                    -> nothing
//
// No product is created here (that stays with create_or_link on confirm, which skips lines that already
// have a product), and no alias is learned (a wrong auto link must not teach the matcher anything; the
// user's tap does, through the link endpoint). The thresholds are the ones of
// docs/PRODUCT-AUTOPOPULATE.md section 3.2, on the plain score (no same-shop bonus).
const (
	AutoLinkScore = autoprod.AutoLinkScore // 0.92
	SuggestFloor  = autoprod.ReviewScore   // 0.75
	MaxSuggested  = 3
)

// Source values stored in receipt_lines.match_source.
const (
	MatchAlias = "alias"
	MatchScore = "score"
	MatchUser  = "user" // reported for a link without match_source; never stored
)

// Suggestion is one stored candidate; names are looked up when the receipt is read.
type Suggestion struct {
	ProductID int64   `json:"product_id"`
	Score     float64 `json:"score"`
}

// ParseSuggestions reads receipt_lines.suggestions. ok is false when the matcher never ran on the line.
func ParseSuggestions(s sql.NullString) (out []Suggestion, ok bool) {
	if !s.Valid {
		return nil, false
	}
	if json.Unmarshal([]byte(s.String), &out) != nil {
		return nil, false
	}
	return out, true
}

// MatchedBy is the API's matched_by for a stored line: "" while unlinked.
func MatchedBy(l db.ReceiptLine) string {
	switch {
	case !l.ProductID.Valid:
		return ""
	case l.MatchSource.Valid:
		return l.MatchSource.String
	default:
		return MatchUser
	}
}

// UseMatcher replaces the matcher (default: one on the service's own database handle).
func (s *Service) UseMatcher(m *match.Matcher) { s.matcher = m }

// UseGlossary sets the household's taught words, which the line cleaner counts as known words.
func (s *Service) UseGlossary(g *translate.Glossary) { s.gloss = g }

// lineMatch is the matcher's verdict on one parsed line.
type lineMatch struct {
	productID   int64
	source      string
	score       float64
	suggestions []Suggestion
	ran         bool // false for lines that are not products (suggestions stay NULL)
}

// matchLines runs the matcher over the parsed lines. It only reads, so it runs outside the write
// transaction: the writes are short and a 40-line receipt never holds the single writer while it searches.
func (s *Service) matchLines(ctx context.Context, rec db.Receipt, lines []receipt.Line) ([]lineMatch, error) {
	out := make([]lineMatch, len(lines))
	for i, l := range lines {
		norm := match.Normalise(l.RawText)
		if l.Kind != receipt.KindItem || norm == "" {
			continue
		}
		c := autoprod.CleanLineWith(s.gloss, l.RawText)
		if c.Skip || c.Norm == "" { // a weight row, a total: not a product
			continue
		}
		m := lineMatch{ran: true, suggestions: []Suggestion{}}
		// 1. exact alias, on the cleaned or the raw text
		for _, n := range uniqNonEmpty(c.Norm, norm) {
			pid, err := s.q.LookupAlias(ctx, db.LookupAliasParams{NormText: n, ShopID: rec.ShopID})
			if err == nil {
				m.productID, m.source, m.score = pid, MatchAlias, 1
				break
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		// 2. fuzzy
		if m.productID == 0 {
			cands, err := s.matcher.Search(ctx, c.Name, 0, 20, SuggestFloor)
			if err != nil {
				return nil, err
			}
			if len(cands) > 0 && cands[0].Score >= AutoLinkScore {
				m.productID, m.source, m.score = cands[0].ProductID, MatchScore, round3(cands[0].Score)
			} else {
				for _, cd := range cands {
					if len(m.suggestions) == MaxSuggested {
						break
					}
					m.suggestions = append(m.suggestions, Suggestion{ProductID: cd.ProductID, Score: round3(cd.Score)})
				}
			}
		}
		out[i] = m
	}
	return out, nil
}

func uniqNonEmpty(a, b string) []string {
	switch {
	case a == "":
		return []string{b}
	case b == "" || a == b:
		return []string{a}
	}
	return []string{a, b}
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// userLinks are the product links of a receipt's current lines that the user (or the confirm step)
// made, kept across a re-parse: a re-parse must not throw away a decision.
type userLink struct {
	productID int64
	norm      string
	total     int64
	used      bool
}

func collectUserLinks(ctx context.Context, q *db.Queries, receiptID int64) ([]*userLink, error) {
	old, err := q.ListLines(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	var out []*userLink
	for _, l := range old {
		if l.Kind != "item" || !l.ProductID.Valid || l.MatchSource.Valid {
			continue
		}
		if _, err := q.GetProduct(ctx, l.ProductID.Int64); err != nil { // deleted or merged away since
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		out = append(out, &userLink{productID: l.ProductID.Int64, norm: l.NormText, total: l.LineTotalCents})
	}
	return out, nil
}

// takeUserLink finds the kept link for a re-parsed line: same text and amount first, then the same text
// when only one such link remains.
func takeUserLink(links []*userLink, norm string, total int64) (int64, bool) {
	for _, l := range links {
		if !l.used && l.norm == norm && l.total == total {
			l.used = true
			return l.productID, true
		}
	}
	var only *userLink
	n := 0
	for _, l := range links {
		if !l.used && l.norm == norm {
			only = l
			n++
		}
	}
	if n == 1 {
		only.used = true
		return only.productID, true
	}
	return 0, false
}

// lineParams fills the product and match columns of a new line.
func lineParams(p *db.InsertLineParams, m lineMatch, kept int64) {
	switch {
	case kept != 0:
		p.ProductID = sql.NullInt64{Int64: kept, Valid: true}
		p.Suggestions = store.Str("[]")
	case m.productID != 0:
		p.ProductID = sql.NullInt64{Int64: m.productID, Valid: true}
		p.MatchSource = store.Str(m.source)
		p.MatchScore = sql.NullFloat64{Float64: m.score, Valid: true}
		p.Suggestions = store.Str("[]")
	case m.ran:
		b, _ := json.Marshal(m.suggestions)
		p.Suggestions = store.Str(string(b))
	}
}
