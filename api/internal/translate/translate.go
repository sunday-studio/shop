// Package translate gives Dutch grocery names an English rendering using a
// curated glossary and Dutch word-formation rules (compounds, plurals,
// adjective endings, diminutives). It is deterministic and offline: no
// models. Words it doesn't know (brands, codes) pass through unchanged.
package translate

import (
	"bufio"
	_ "embed"
	"strings"
	"unicode"

	"shop/internal/match"
)

//go:embed glossary.tsv
var glossaryTSV string

var (
	glossary  = map[string]string{} // folded dutch (may contain spaces) → english
	maxPhrase = 1
)

func init() {
	sc := bufio.NewScanner(strings.NewReader(glossaryTSV))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		nl, en, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		key := match.Fold(strings.TrimSpace(nl))
		if _, dup := glossary[key]; dup {
			continue // first entry wins
		}
		glossary[key] = strings.TrimSpace(en)
		maxPhrase = max(maxPhrase, len(strings.Fields(key)))
	}
}

// Result is a translation with how much of the input it covered.
type Result struct {
	Text     string
	Known    int // words translated
	Words    int // words that could have been translated (letters only)
	Complete bool
}

// MinCoverage is the share of words that must be known for a translation
// to be used automatically.
const MinCoverage = 0.5

// OK reports whether the translation is good enough to show.
func (r Result) OK() bool {
	return r.Known > 0 && float64(r.Known) >= MinCoverage*float64(r.Words)
}

// Translate renders a Dutch product name in English.
func Translate(dutch string) Result {
	// Commas stay attached: "1,5l" is a size, and clean() strips a trailing one.
	toks := strings.Fields(strings.NewReplacer("/", " / ", "&", " & ").Replace(dutch))
	var out []string
	var res Result
	for i := 0; i < len(toks); {
		// Longest glossary phrase starting here.
		matched := false
		for n := min(maxPhrase, len(toks)-i); n >= 2; n-- {
			key := match.Fold(strings.Join(cleanWords(toks[i:i+n]), " "))
			if en, ok := glossary[key]; ok {
				out = append(out, en)
				res.Known += n
				res.Words += n
				i += n
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		tok := toks[i]
		i++
		w := clean(tok)
		if w == "" || !isWord(w) {
			out = append(out, tok) // numbers, sizes, symbols
			continue
		}
		res.Words++
		if en, ok := word(match.Fold(w)); ok {
			res.Known++
			if en != "" {
				out = append(out, en+trailingPunct(tok))
			}
			continue
		}
		out = append(out, tok)
	}
	res.Text = sentenceCase(strings.Join(out, " "))
	res.Complete = res.Known == res.Words
	return res
}

func clean(tok string) string {
	return strings.TrimFunc(tok, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// trailingPunct returns punctuation after the word ("jong," → ",").
func trailingPunct(tok string) string {
	return tok[len(strings.TrimRightFunc(tok, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })):]
}

func cleanWords(ts []string) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = clean(t)
	}
	return out
}

func isWord(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			return false
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters >= 2
}

// word translates one folded Dutch word: exact, then inflection, then compound.
func word(w string) (string, bool) {
	if en, ok := lookup(w); ok {
		return en, true
	}
	return compound(w)
}

// lookup tries the word and its inflected stems.
func lookup(w string) (string, bool) {
	if en, ok := glossary[w]; ok {
		return dropMarker(en), true
	}
	// Diminutives: worteltjes → wortel (+ plural).
	for _, suf := range []string{"tjes", "jes", "tje", "je"} {
		if stem, ok := strings.CutSuffix(w, suf); ok && len(stem) >= 3 {
			if en, ok := glossary[stem]; ok {
				if strings.HasSuffix(suf, "s") {
					return plural(en), true
				}
				return en, true
			}
		}
	}
	// Plurals: appels → appel, bananen → banaan? (vowel lengthening) → try both.
	if stem, ok := strings.CutSuffix(w, "en"); ok && len(stem) >= 2 {
		for _, s := range stems(stem) {
			if en, ok := glossary[s]; ok {
				return plural(en), true
			}
		}
	}
	if stem, ok := strings.CutSuffix(w, "s"); ok && len(stem) >= 3 {
		if en, ok := glossary[stem]; ok {
			return plural(en), true
		}
	}
	// Adjective ending: gerookte → gerookt.
	if stem, ok := strings.CutSuffix(w, "e"); ok && len(stem) >= 3 {
		if en, ok := glossary[stem]; ok {
			return en, true
		}
	}
	return "", false
}

// stems undoes Dutch spelling changes before -en: kipp→kip, banan→banaan.
func stems(s string) []string {
	out := []string{s}
	n := len(s)
	if n >= 2 && s[n-1] == s[n-2] && !strings.ContainsRune("aeiou", rune(s[n-1])) {
		out = append(out, s[:n-1]) // doubled consonant
	}
	if n >= 2 && strings.ContainsRune("aeou", rune(s[n-2])) && !strings.ContainsRune("aeiou", rune(s[n-1])) {
		out = append(out, s[:n-1]+s[n-2:]) // open syllable: banan → banaan
	}
	return out
}

// compound splits e.g. "volkorenbrood" into glossary words, allowing the
// Dutch linking letters -s-, -e-, -en- (and a doubled consonant before -en-).
// The split with the fewest parts wins; the last part may be inflected.
func compound(w string) (string, bool) {
	n := len(w)
	type step struct {
		parts int
		prev  int
		en    string
	}
	best := make([]*step, n+1)
	best[0] = &step{}
	for i := 0; i < n; i++ {
		if best[i] == nil {
			continue
		}
		for j := i + 3; j <= n; j++ {
			part := w[i:j]
			var en string
			var ok bool
			if j == n {
				en, ok = lookup(part) // the head may be plural etc.
			} else {
				en, ok = modifier(part)
			}
			if !ok {
				continue
			}
			// Linking letters after a non-final part.
			ends := []int{j}
			if j < n {
				for _, link := range []string{"s", "e", "en"} {
					if strings.HasPrefix(w[j:], link) && j+len(link) < n {
						ends = append(ends, j+len(link))
					}
				}
				if j+3 < n && w[j] == w[j-1] && strings.HasPrefix(w[j+1:], "en") {
					ends = append(ends, j+3) // kip+p+en+soep
				}
			}
			for _, e := range ends {
				cand := &step{parts: best[i].parts + 1, prev: i, en: en}
				if best[e] == nil || cand.parts < best[e].parts {
					best[e] = cand
				}
			}
		}
	}
	if best[n] == nil || best[n].parts < 2 {
		return "", false
	}
	var parts []string
	for at := n; at > 0; at = best[at].prev {
		if best[at].en != "" {
			parts = append([]string{best[at].en}, parts...)
		}
	}
	return strings.Join(parts, " "), true
}

// modifier translates a non-final compound part. English compounds use
// the singular ("tomato soup", not "tomatoes soup"), so a Dutch plural part
// is mapped through its singular when the glossary has it.
func modifier(part string) (string, bool) {
	if stem, ok := strings.CutSuffix(part, "en"); ok && len(stem) >= 2 {
		for _, s := range stems(stem) {
			if en, ok := glossary[s]; ok {
				return dropMarker(en), true
			}
		}
	}
	if en, ok := glossary[part]; ok {
		return dropMarker(en), true
	}
	return "", false
}

// dropMarker maps glossary entries of "-" (function words) to "".
func dropMarker(en string) string {
	if en == "-" {
		return ""
	}
	return en
}

func plural(en string) string {
	if en == "" || strings.Contains(en, " ") && !strings.HasSuffix(en, "s") {
		// multi-word: pluralise the last word
		if i := strings.LastIndex(en, " "); i >= 0 {
			return en[:i+1] + plural(en[i+1:])
		}
	}
	switch {
	case strings.HasSuffix(en, "s"), strings.HasSuffix(en, "x"), strings.HasSuffix(en, "ch"), strings.HasSuffix(en, "sh"):
		if strings.HasSuffix(en, "ss") || !strings.HasSuffix(en, "s") {
			return en + "es"
		}
		return en // already plural
	case strings.HasSuffix(en, "y") && len(en) > 1 && !strings.ContainsRune("aeiou", rune(en[len(en)-2])):
		return en[:len(en)-1] + "ies"
	case strings.HasSuffix(en, "o") && (en == "potato" || en == "tomato"):
		return en + "es"
	}
	return en + "s"
}

func sentenceCase(s string) string {
	for i, r := range s {
		return s[:i] + string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}
