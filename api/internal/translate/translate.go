// Package translate gives Dutch grocery names an English rendering using a
// curated glossary and Dutch word-formation rules (compounds, plurals,
// adjective endings, diminutives). It is deterministic and offline: no
// models. Words it doesn't know (brands, codes) pass through unchanged.
package translate

import (
	"bufio"
	_ "embed"
	"strings"
	"sync"
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

// Term is a user glossary entry. KeepAsIs marks words (brands, varieties)
// that stay as written.
type Term struct {
	English  string
	KeepAsIs bool
}

var (
	userMu    sync.RWMutex
	userTerms = map[string]Term{} // folded single word → term
)

// SetUserTerms replaces the user glossary, which overrides the built-in one.
func SetUserTerms(terms map[string]Term) {
	m := make(map[string]Term, len(terms))
	for k, v := range terms {
		m[Key(k)] = v
	}
	userMu.Lock()
	userTerms = m
	userMu.Unlock()
}

// Key is the glossary key for a word: folded, no surrounding punctuation.
func Key(word string) string { return match.Fold(clean(word)) }

// Span marks an unknown word. Start/End are UTF-16 offsets (end exclusive)
// so they map directly onto NSRange in the app.
type Span struct {
	Word  string `json:"word"` // as written
	Key   string `json:"key"`  // folded glossary key
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Status of a dictionary translation.
const (
	StatusDictionary = "dictionary" // every word known
	StatusPartial    = "partial"    // usable, some words unknown
	StatusMissing    = "missing"    // too few words known to show
)

// Result is a translation with how much of the input it covered.
type Result struct {
	Text         string
	Known        int // words translated (or kept as is)
	Words        int // words that could have been translated (letters only)
	Complete     bool
	Unknown      []Span // positions in Text
	UnknownInput []Span // positions in the Dutch input
}

// MinCoverage is the share of words that must be known for a translation
// to be used automatically.
const MinCoverage = 0.5

// OK reports whether the translation is good enough to show. Names without
// any translatable word ("7UP 330ml") are fine as they are.
func (r Result) OK() bool {
	if r.Words == 0 {
		return true
	}
	return r.Known > 0 && float64(r.Known) >= MinCoverage*float64(r.Words)
}

// Status classifies the result for the UI.
func (r Result) Status() string {
	switch {
	case r.Complete:
		return StatusDictionary
	case r.OK():
		return StatusPartial
	default:
		return StatusMissing
	}
}

type token struct {
	s          string
	start, end int // byte offsets in the input
}

// tokenize splits on whitespace and around "/" and "&", keeping offsets.
// Commas stay attached: "1,5l" is a size.
func tokenize(s string) []token {
	var out []token
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out = append(out, token{s[start:end], start, end})
			start = -1
		}
	}
	for i, r := range s {
		switch {
		case unicode.IsSpace(r):
			flush(i)
		case r == '/' || r == '&':
			flush(i)
			out = append(out, token{string(r), i, i + 1})
		default:
			if start < 0 {
				start = i
			}
		}
	}
	flush(len(s))
	return out
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// Translate renders a Dutch product name in English using the built-in and
// user glossaries.
func Translate(dutch string) Result { return translate(dutch, nil) }

// TranslateWith is Translate with one extra user term applied on top, for
// previewing a term before saving it.
func TranslateWith(dutch, key string, t Term) Result {
	return translate(dutch, map[string]Term{Key(key): t})
}

func translate(dutch string, extra map[string]Term) Result {
	userMu.RLock()
	defer userMu.RUnlock()
	g := glossaryView{extra: extra}

	toks := tokenize(dutch)
	type piece struct {
		text    string
		unknown *token
	}
	var pieces []piece
	var res Result
	for i := 0; i < len(toks); {
		// Longest built-in phrase starting here.
		matched := false
		for n := min(maxPhrase, len(toks)-i); n >= 2; n-- {
			words := make([]string, n)
			for k := range n {
				words[k] = clean(toks[i+k].s)
			}
			if en, ok := glossary[match.Fold(strings.Join(words, " "))]; ok {
				pieces = append(pieces, piece{text: en + trailingPunct(toks[i+n-1].s)})
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
		w := clean(tok.s)
		if w == "" || !isWord(w) {
			pieces = append(pieces, piece{text: tok.s}) // numbers, sizes, symbols
			continue
		}
		res.Words++
		key := match.Fold(w)
		if t, ok := g.user(key); ok {
			res.Known++
			if t.KeepAsIs {
				pieces = append(pieces, piece{text: tok.s})
			} else if t.English != "" {
				pieces = append(pieces, piece{text: t.English + trailingPunct(tok.s)})
			}
			continue
		}
		en, ok := g.word(key)
		if !ok {
			if long, isAbbr := match.ExpandAbbreviation(key); isAbbr {
				en, ok = g.word(long)
			}
		}
		if ok {
			res.Known++
			if en != "" {
				pieces = append(pieces, piece{text: en + trailingPunct(tok.s)})
			}
			continue
		}
		t := tok
		pieces = append(pieces, piece{text: tok.s, unknown: &t})
	}

	var b strings.Builder
	for i, p := range pieces {
		if i > 0 {
			b.WriteByte(' ')
		}
		at := b.Len()
		text := p.text
		if i == 0 {
			text = sentenceCase(text)
		}
		b.WriteString(text)
		if p.unknown != nil {
			w := clean(p.unknown.s)
			lead := strings.Index(p.unknown.s, w) // skip leading punctuation
			outStart := utf16Len(b.String()[:at+lead])
			res.Unknown = append(res.Unknown, Span{Word: w, Key: match.Fold(w),
				Start: outStart, End: outStart + utf16Len(w)})
			inStart := utf16Len(dutch[:p.unknown.start+lead])
			res.UnknownInput = append(res.UnknownInput, Span{Word: w, Key: match.Fold(w),
				Start: inStart, End: inStart + utf16Len(w)})
		}
	}
	res.Text = b.String()
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

// glossaryView resolves words: a preview term, then the user glossary,
// then the built-in glossary. Callers hold userMu (read).
type glossaryView struct{ extra map[string]Term }

func (g glossaryView) user(key string) (Term, bool) {
	if t, ok := g.extra[key]; ok {
		return t, true
	}
	t, ok := userTerms[key]
	return t, ok
}

// get returns the English for a glossary key ("" for dropped function
// words; the word itself for keep-as-is user terms).
func (g glossaryView) get(key string) (string, bool) {
	if t, ok := g.user(key); ok {
		if t.KeepAsIs {
			return key, true
		}
		return t.English, true
	}
	en, ok := glossary[key]
	return dropMarker(en), ok
}

// word translates one folded Dutch word: exact, then inflection, then compound.
func (g glossaryView) word(w string) (string, bool) {
	if en, ok := g.lookup(w); ok {
		return en, true
	}
	return g.compound(w)
}

// lookup tries the word and its inflected stems.
func (g glossaryView) lookup(w string) (string, bool) {
	if en, ok := g.get(w); ok {
		return en, true
	}
	// Diminutives: worteltjes → wortel (+ plural).
	for _, suf := range []string{"tjes", "jes", "tje", "je"} {
		if stem, ok := strings.CutSuffix(w, suf); ok && len(stem) >= 3 {
			if en, ok := g.get(stem); ok {
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
			if en, ok := g.get(s); ok {
				return plural(en), true
			}
		}
	}
	if stem, ok := strings.CutSuffix(w, "s"); ok && len(stem) >= 3 {
		if en, ok := g.get(stem); ok {
			return plural(en), true
		}
	}
	// Adjective ending: gerookte → gerookt.
	if stem, ok := strings.CutSuffix(w, "e"); ok && len(stem) >= 3 {
		if en, ok := g.get(stem); ok {
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
func (g glossaryView) compound(w string) (string, bool) {
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
				en, ok = g.lookup(part) // the head may be plural etc.
			} else {
				en, ok = g.modifier(part)
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
func (g glossaryView) modifier(part string) (string, bool) {
	if stem, ok := strings.CutSuffix(part, "en"); ok && len(stem) >= 2 {
		for _, s := range stems(stem) {
			if en, ok := g.get(s); ok {
				return dropMarker(en), true
			}
		}
	}
	if en, ok := g.get(part); ok {
		return en, true
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
	if en == "" {
		return ""
	}
	if strings.Contains(en, " ") && !strings.HasSuffix(en, "s") {
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
