package match

import (
	"sort"
	"strings"
)

// Score compares two normalised strings in [0,1]:
// 0.6·Jaro-Winkler + 0.4·token-set ratio. Jaro-Winkler rewards shared
// prefixes (receipt abbreviations), token-set handles word order and
// extra words ("ah halfvol melk" vs "melk halfvol").
func Score(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	return 0.6*JaroWinkler(a, b) + 0.4*TokenSetRatio(a, b)
}

// JaroWinkler similarity in [0,1].
func JaroWinkler(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 || lb == 0 {
		return 0
	}
	window := max(la, lb)/2 - 1
	if window < 0 {
		window = 0
	}
	ma := make([]bool, la)
	mb := make([]bool, lb)
	matches := 0
	for i := range ra {
		lo, hi := max(0, i-window), min(lb, i+window+1)
		for j := lo; j < hi; j++ {
			if !mb[j] && ra[i] == rb[j] {
				ma[i], mb[j] = true, true
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return 0
	}
	transpositions, k := 0, 0
	for i := range ra {
		if !ma[i] {
			continue
		}
		for !mb[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}
	m := float64(matches)
	jaro := (m/float64(la) + m/float64(lb) + (m-float64(transpositions)/2)/m) / 3

	prefix := 0
	for i := 0; i < min(4, la, lb) && ra[i] == rb[i]; i++ {
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

// TokenSetRatio is the fuzzywuzzy-style token set ratio in [0,1].
func TokenSetRatio(a, b string) float64 {
	ta, tb := tokenSet(a), tokenSet(b)
	var inter, onlyA, onlyB []string
	for t := range ta {
		if tb[t] {
			inter = append(inter, t)
		} else {
			onlyA = append(onlyA, t)
		}
	}
	for t := range tb {
		if !ta[t] {
			onlyB = append(onlyB, t)
		}
	}
	sort.Strings(inter)
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	base := strings.Join(inter, " ")
	sa := strings.TrimSpace(base + " " + strings.Join(onlyA, " "))
	sb := strings.TrimSpace(base + " " + strings.Join(onlyB, " "))
	best := ratio(sa, sb)
	if base != "" {
		best = max(best, ratio(base, sa), ratio(base, sb))
	}
	return best
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.Fields(s) {
		out[t] = true
	}
	return out
}

// ratio is the normalised Levenshtein similarity in [0,1].
func ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	total := len(ra) + len(rb)
	if total == 0 {
		return 1
	}
	return float64(total-levenshtein(ra, rb)) / float64(total)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Trigrams returns the distinct 3-rune substrings of each word (words
// shorter than 3 runes are returned whole), for FTS5 trigram OR-queries.
func Trigrams(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.Fields(s) {
		r := []rune(w)
		if len(r) < 3 {
			continue
		}
		for i := 0; i+3 <= len(r); i++ {
			g := string(r[i : i+3])
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}
