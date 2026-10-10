package match

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// SuggestThreshold is the minimum score shown as a link suggestion.
const SuggestThreshold = 0.75

const sameShopBonus = 0.05

type Candidate struct {
	ProductID int64   `json:"product_id"`
	Name      string  `json:"name"`
	NameEN    string  `json:"name_en,omitempty"`
	Brand     string  `json:"brand,omitempty"`
	Score     float64 `json:"score"`
	Via       string  `json:"via"` // "product", "product_en" or "alias"
}

// Matcher finds products for free text using the search_fts trigram
// index for candidates and Score for ranking.
type Matcher struct{ db *sql.DB }

func NewMatcher(db *sql.DB) *Matcher { return &Matcher{db: db} }

// Search ranks products for text. shopID (0 = any) gives aliases learned
// at that shop a small bonus. Results with score < minScore are dropped.
func (m *Matcher) Search(ctx context.Context, text string, shopID int64, limit int, minScore float64) ([]Candidate, error) {
	norm := Normalise(text)
	if norm == "" {
		return nil, nil
	}

	type hit struct {
		kind string
		ref  int64
		shop sql.NullInt64
		text string
	}
	var hits []hit
	var rows *sql.Rows
	var err error
	if grams := Trigrams(norm); len(grams) > 0 {
		q := make([]string, len(grams))
		for i, g := range grams {
			q[i] = `"` + g + `"`
		}
		rows, err = m.db.QueryContext(ctx,
			`SELECT kind, ref_id, shop_id, text FROM search_fts WHERE search_fts MATCH ? ORDER BY rank LIMIT 300`,
			strings.Join(q, " OR "))
	} else {
		// Only very short words: trigram search can't help, scan everything.
		rows, err = m.db.QueryContext(ctx, `SELECT kind, ref_id, shop_id, text FROM search_fts LIMIT 2000`)
	}
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.kind, &h.ref, &h.shop, &h.text); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	best := map[int64]Candidate{}
	for _, h := range hits {
		s := Score(norm, Normalise(h.text))
		if h.kind == "alias" && shopID != 0 && h.shop.Valid && h.shop.Int64 == shopID {
			s = min(1, s+sameShopBonus)
		}
		if c, ok := best[h.ref]; !ok || s > c.Score {
			best[h.ref] = Candidate{ProductID: h.ref, Score: s, Via: h.kind}
		}
	}
	if len(best) == 0 {
		return nil, nil
	}

	// Attach names; this also drops soft-deleted products.
	ids := make([]any, 0, len(best))
	ph := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
		ph = append(ph, "?")
	}
	prows, err := m.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(name_en, ''), COALESCE(brand, '') FROM products WHERE deleted_at IS NULL AND id IN (`+strings.Join(ph, ",")+`)`, ids...)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	var out []Candidate
	for prows.Next() {
		var id int64
		var name, nameEN, brand string
		if err := prows.Scan(&id, &name, &nameEN, &brand); err != nil {
			return nil, err
		}
		c := best[id]
		if c.Score < minScore {
			continue
		}
		c.Name, c.NameEN, c.Brand = name, nameEN, brand
		out = append(out, c)
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ProductID < out[j].ProductID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Suggest returns up to 3 link suggestions for a receipt line.
func (m *Matcher) Suggest(ctx context.Context, shopID int64, rawText string) ([]Candidate, error) {
	return m.Search(ctx, rawText, shopID, 3, SuggestThreshold)
}

// AliasHit is a learned receipt text (alias) matching a search.
type AliasHit struct {
	Text        string
	ProductID   int64
	ProductName string
	ShopID      *int64
	ShopName    string
	Score       float64
}

// SearchAliases finds learned receipt texts ("AH HALFV MELK" → product).
func (m *Matcher) SearchAliases(ctx context.Context, text string, limit int, minScore float64) ([]AliasHit, error) {
	norm := Normalise(text)
	grams := Trigrams(norm)
	if len(grams) == 0 {
		return nil, nil
	}
	q := make([]string, len(grams))
	for i, g := range grams {
		q[i] = `"` + g + `"`
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT f.text, f.ref_id, p.name, a.shop_id, COALESCE(s.name, '')
		FROM search_fts f
		JOIN products p ON p.id = f.ref_id AND p.deleted_at IS NULL
		JOIN product_aliases a ON a.product_id = f.ref_id AND a.norm_text = f.text AND a.shop_id IS f.shop_id
		LEFT JOIN shops s ON s.id = a.shop_id
		WHERE search_fts MATCH ? AND f.kind = 'alias'
		LIMIT 300`, strings.Join(q, " OR "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AliasHit
	for rows.Next() {
		var h AliasHit
		var shop sql.NullInt64
		if err := rows.Scan(&h.Text, &h.ProductID, &h.ProductName, &shop, &h.ShopName); err != nil {
			return nil, err
		}
		if shop.Valid {
			h.ShopID = &shop.Int64
		}
		if h.Score = Score(norm, h.Text); h.Score >= minScore {
			out = append(out, h)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
