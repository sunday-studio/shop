package listplan

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shop/internal/match"
	"shop/internal/store"
)

const (
	// SuggestMinScore is how alike two names must be (after dropping store-brand words and pack sizes).
	SuggestMinScore = 0.80
	// MaxSuggestions bounds one answer.
	MaxSuggestions = 50
	suggestTTL     = 60 * time.Second
)

// Suggestion says "these two look like the same kind of item". It is only a question for the user: nothing is
// grouped until Confirm.
type Suggestion struct {
	A      SuggestedProduct `json:"a"`
	B      SuggestedProduct `json:"b"`
	Score  float64          `json:"score"`
	Reason string           `json:"reason"`
}

type SuggestedProduct struct {
	ProductID int64     `json:"product_id"`
	Name      string    `json:"name"`
	NameEN    string    `json:"name_en,omitempty"`
	GroupID   *int64    `json:"group_id"` // the group it is already in, if any
	Shops     []ShopRef `json:"shops"`
}

type sugCache struct {
	at    time.Time
	items []Suggestion
}

func (s *Service) invalidate() {
	s.sugMu.Lock()
	s.sugCache = nil
	s.sugMu.Unlock()
}

// builtinBrandWords are store-brand and generic words that say nothing about what the product is.
var builtinBrandWords = map[string]bool{
	"ah": true, "jumbo": true, "dirk": true, "lidl": true, "aldi": true, "plus": true, "huismerk": true,
	"gwoon": true, "euroshopper": true, "basic": true, "de": true, "beste": true, "1": true, "everyday": true,
	"boni": true, "spar": true, "coop": true, "vomar": true, "hoogvliet": true, "deka": true, "poiesz": true,
	"emte": true, "ekoplaza": true, "marqt": true, "picnic": true,
}

var sizeToken = regexp.MustCompile(`^\d+([.,]\d+)?(g|gr|kg|ml|cl|dl|l|ltr|st|stuks|x|pk|stk)?$`)

// comparable strips store-brand words, shop names and pack sizes from a name.
func comparable(name string, shopWords map[string]bool) string {
	var keep []string
	for _, w := range strings.Fields(match.Normalise(name)) {
		w = strings.Trim(w, ".,/")
		if w == "" || builtinBrandWords[w] || shopWords[w] || sizeToken.MatchString(w) {
			continue
		}
		keep = append(keep, w)
	}
	return strings.Join(keep, " ")
}

func (s *Service) shopWords(ctx context.Context, q queryer) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, COALESCE(chain, '') FROM shops WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name, chain string
		if err := rows.Scan(&name, &chain); err != nil {
			return nil, err
		}
		for _, w := range strings.Fields(match.Normalise(name + " " + chain)) {
			if len(w) >= 2 {
				out[w] = true
			}
		}
	}
	return out, rows.Err()
}

type sugProd struct {
	prodRow
	norm   string
	normEN string
	base   string
	shops  map[int64]ShopRef
}

// Suggestions returns pairs of products that may be the same kind of item, best first: a name score of at
// least SuggestMinScore after dropping store-brand words, the same category and the same base unit, bought at
// different shops, not already grouped together, not denied, not kept apart in the product review.
func (s *Service) Suggestions(ctx context.Context, limit int) ([]Suggestion, error) {
	if limit <= 0 || limit > MaxSuggestions {
		limit = MaxSuggestions
	}
	s.sugMu.Lock()
	if c := s.sugCache; c != nil && s.now().Sub(c.at) < suggestTTL {
		items := c.items
		s.sugMu.Unlock()
		return head(items, limit), nil
	}
	s.sugMu.Unlock()
	items, err := s.computeSuggestions(ctx)
	if err != nil {
		return nil, err
	}
	s.sugMu.Lock()
	s.sugCache = &sugCache{at: s.now(), items: items}
	s.sugMu.Unlock()
	return head(items, limit), nil
}

// SuggestionCount is the number of open suggestions (for the review queue badge).
func (s *Service) SuggestionCount(ctx context.Context) (int, error) {
	items, err := s.Suggestions(ctx, MaxSuggestions)
	return len(items), err
}

func head(in []Suggestion, n int) []Suggestion {
	if len(in) > n {
		in = in[:n]
	}
	return append([]Suggestion{}, in...)
}

func (s *Service) computeSuggestions(ctx context.Context) ([]Suggestion, error) {
	words, err := s.shopWords(ctx, s.conn)
	if err != nil {
		return nil, err
	}
	ps, err := loadProducts(ctx, s.conn, `EXISTS (SELECT 1 FROM price_points pp WHERE pp.product_id = p.id AND pp.purchased_at IS NOT NULL AND pp.paid_cents > 0)`)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.id
	}
	info, err := purchaseInfo(ctx, s.conn, ids)
	if err != nil {
		return nil, err
	}
	pairs := map[[2]int64]bool{}
	for _, q := range []string{`SELECT a_id, b_id FROM product_group_denied`, `SELECT a_id, b_id FROM product_distinct`} {
		rows, err := s.conn.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var a, b int64
			if err := rows.Scan(&a, &b); err != nil {
				rows.Close()
				return nil, err
			}
			pairs[[2]int64{a, b}] = true
		}
		rows.Close()
	}
	type block struct {
		cat  int64
		base string
	}
	blocks := map[block][]*sugProd{}
	for _, p := range ps {
		if !p.categoryID.Valid {
			continue
		}
		si := info[p.id]
		if si == nil || len(si.shops) == 0 {
			continue
		}
		norm := comparable(p.name, words)
		if norm == "" {
			continue
		}
		base, _ := unitOf(p.netQty, p.netUnit)
		sp := &sugProd{prodRow: p, norm: norm, base: base, shops: si.shops}
		if p.nameEN != "" {
			sp.normEN = comparable(p.nameEN, words)
		}
		b := block{p.categoryID.Int64, base}
		blocks[b] = append(blocks[b], sp)
	}
	best := map[[2]string]Suggestion{}
	key := func(p *sugProd) string {
		if p.group.Valid {
			return "g" + strconv.FormatInt(p.group.Int64, 10)
		}
		return "p" + strconv.FormatInt(p.id, 10)
	}
	for _, list := range blocks {
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				a, b := list[i], list[j]
				if a.group.Valid && b.group.Valid && a.group.Int64 == b.group.Int64 {
					continue
				}
				lo, hi := ordered(a.id, b.id)
				if pairs[[2]int64{lo, hi}] {
					continue
				}
				if !differentShops(a.shops, b.shops) {
					continue
				}
				score := match.Score(a.norm, b.norm)
				if a.normEN != "" && b.normEN != "" {
					score = max(score, match.Score(a.normEN, b.normEN))
				}
				if score < SuggestMinScore {
					continue
				}
				ka, kb := key(a), key(b)
				if ka > kb {
					ka, kb = kb, ka
				}
				k := [2]string{ka, kb}
				if cur, ok := best[k]; ok && (cur.Score > score || (cur.Score == score && cur.A.ProductID < a.id)) {
					continue
				}
				best[k] = Suggestion{A: sugOf(a, info), B: sugOf(b, info), Score: score, Reason: "similar_name_other_shop"}
			}
		}
	}
	out := make([]Suggestion, 0, len(best))
	for _, v := range best {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].A.ProductID != out[j].A.ProductID {
			return out[i].A.ProductID < out[j].A.ProductID
		}
		return out[i].B.ProductID < out[j].B.ProductID
	})
	return out, nil
}

func sugOf(p *sugProd, info map[int64]*shopInfo) SuggestedProduct {
	return SuggestedProduct{ProductID: p.id, Name: p.name, NameEN: p.nameEN, GroupID: store.IntPtr(p.group), Shops: info[p.id].shopList()}
}

// differentShops: together they were bought at two or more shops, so a price comparison would have something to say.
func differentShops(a, b map[int64]ShopRef) bool {
	all := map[int64]bool{}
	for id := range a {
		all[id] = true
	}
	for id := range b {
		all[id] = true
	}
	return len(all) >= 2
}
