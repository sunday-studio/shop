// Package autoprod creates and links products automatically from receipt lines and barcode scans,
// and runs the review queue, merges and undo (docs/PRODUCT-AUTOPOPULATE.md).
package autoprod

import (
	"regexp"
	"strings"
	"unicode"

	"shop/internal/match"
	"shop/internal/translate"
)

var (
	qtyPrefixRe   = regexp.MustCompile(`^\d{1,2}\s+`)
	bulletRe      = regexp.MustCompile(`^[-–•*.\s]+`)
	trailPriceRe  = regexp.MustCompile(`\s+\d+[.,]\d{2}$`)
	trailVATRe    = regexp.MustCompile(`\s+[A-Da-d]$`)
	weightRowRe   = regexp.MustCompile(`^\d+[.,]?\d*\s?kg(\s*x\s*\d+)?$`)
	kiloResidueRe = regexp.MustCompile(`^[a-z]{1,2} [a-z]{2,4} kilo$`)
	threeLetters  = regexp.MustCompile(`[a-z]{3}`)
	vowelRe       = regexp.MustCompile(`[aeiouy]`)
	lettersOnly   = regexp.MustCompile(`^[a-z]+$`)
)

// brands are kept as written and count as known words for the noise filter.
var brands = map[string]bool{
	"coca-cola": true, "fanta": true, "lipton": true, "maggi": true, "euroma": true, "kikkoman": true,
	"kewpie": true, "indomie": true, "arla": true, "camp": true, "princes": true, "kokki": true,
	"uchibori": true, "starb": true, "ah": true, "dirk": true,
}

// Cleaned is a receipt line text after cleaning.
type Cleaned struct {
	Name string // title-cased product name ("" when Skip)
	Norm string // match.Normalise(Name)
	Skip bool   // not a product (weight row, too short)
	Odd  bool   // first token looks like OCR junk the filter could not drop
}

func knownWord(w string) bool {
	if brands[w] {
		return true
	}
	if _, ok := match.ExpandAbbreviation(w); ok {
		return true
	}
	r := translate.Translate(w)
	return r.Words > 0 && r.Known == r.Words
}

// CleanLine applies the rules of §3.1 to the raw text of an item line.
func CleanLine(raw string) Cleaned {
	s := strings.TrimSpace(raw)
	if weightRowRe.MatchString(strings.ToLower(s)) {
		return Cleaned{Skip: true}
	}
	s = qtyPrefixRe.ReplaceAllString(s, "")
	s = bulletRe.ReplaceAllString(s, "")
	s = trailPriceRe.ReplaceAllString(s, "")
	s = trailVATRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)

	toks := strings.Fields(s)
	// Noise filter: up to 2 leading vowel-less junk tokens.
	for drop := 0; drop < 2 && len(toks) > 1; drop++ {
		f := match.Fold(strings.Trim(toks[0], ".,"))
		if len(f) >= 3 && lettersOnly.MatchString(f) && !vowelRe.MatchString(f) && !knownWord(f) {
			toks = toks[1:]
			continue
		}
		break
	}
	s = strings.Join(toks, " ")

	f := match.Fold(s)
	if !threeLetters.MatchString(f) || kiloResidueRe.MatchString(f) {
		return Cleaned{Skip: true}
	}
	odd := false
	if len(toks) > 0 {
		first := match.Fold(strings.Trim(toks[0], ".,"))
		if len(first) >= 8 && lettersOnly.MatchString(first) && !knownWord(first) {
			odd = true
		}
	}
	name := titleCase(s)
	return Cleaned{Name: name, Norm: match.Normalise(name), Odd: odd}
}

// titleCase capitalises each word, keeping the AH brand prefix upper-case.
func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if w == "ah" {
			words[i] = "AH"
			continue
		}
		r := []rune(w)
		up := true
		for j, c := range r {
			if up && unicode.IsLetter(c) {
				r[j] = unicode.ToUpper(c)
			}
			up = c == '-' || (up && !unicode.IsLetter(c) && c != '\'')
		}
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
