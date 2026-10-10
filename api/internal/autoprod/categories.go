package autoprod

import (
	"context"
	_ "embed"
	"regexp"
	"strings"

	"shop/internal/match"
	"shop/internal/store/db"
)

//go:embed category_keywords.tsv
var seedKeywords string

type keyword struct {
	word     string
	category string // seed rows: category name
	catID    int64  // user rows: category id
}

var tokenRe = regexp.MustCompile(`[^a-z]+`)

func parseSeed() []keyword {
	var out []keyword
	for _, l := range strings.Split(seedKeywords, "\n") {
		if strings.HasPrefix(l, "#") || !strings.Contains(l, "\t") {
			continue
		}
		k, c, _ := strings.Cut(strings.TrimRight(l, "\r"), "\t")
		out = append(out, keyword{word: k, category: c})
	}
	return out
}

// bestKeyword implements §3.4: a keyword matches a token when equal, or when the token ends with it
// and the keyword has 4+ letters. Whole-token beats tail, then longer keyword, then later token, then
// earlier table row.
func bestKeyword(name string, kws []keyword) (keyword, bool) {
	toks := []string{}
	for _, t := range tokenRe.Split(match.Fold(name), -1) {
		if t != "" {
			toks = append(toks, t)
		}
	}
	type cand struct{ m, ti, negPos int }
	var best cand
	var bestKw keyword
	found := false
	for ti, t := range toks {
		for pos, k := range kws {
			var m int
			switch {
			case t == k.word:
				m = len(k.word) + 100
			case len(k.word) >= 4 && strings.HasSuffix(t, k.word):
				m = len(k.word)
			default:
				continue
			}
			c := cand{m, ti, -pos}
			if !found || c.m > best.m || (c.m == best.m && (c.ti > best.ti || (c.ti == best.ti && c.negPos > best.negPos))) {
				best, bestKw, found = c, k, true
			}
		}
	}
	return bestKw, found
}

// GuessCategory returns the category id for a product name from the keyword rules; user keywords
// (taught in category_keywords) override the built-in seed. ok is false when nothing matches.
func GuessCategory(ctx context.Context, q *db.Queries, name string) (int64, bool, error) {
	userRows, err := userKeywords(ctx, q)
	if err != nil {
		return 0, false, err
	}
	if k, ok := bestKeyword(name, userRows); ok {
		return k.catID, true, nil
	}
	k, ok := bestKeyword(name, parseSeed())
	if !ok {
		return 0, false, nil
	}
	cats, err := q.ListCategories(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, c := range cats {
		if c.Name == k.category {
			return c.ID, true, nil
		}
	}
	return 0, false, nil
}

func userKeywords(ctx context.Context, q *db.Queries) ([]keyword, error) {
	rows, err := q.ListCategoryKeywords(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]keyword, len(rows))
	for i, r := range rows {
		out[i] = keyword{word: r.Keyword, catID: r.CategoryID}
	}
	return out, nil
}

// seedCategoryName is used by tests: the seed rules without a database.
func seedCategoryName(name string) (string, bool) {
	k, ok := bestKeyword(name, parseSeed())
	return k.category, ok
}
