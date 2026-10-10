// Package match links receipt text to products using deterministic fuzzy
// matching: normalisation, exact aliases, and trigram/Jaro-Winkler scoring.
package match

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var accentStripper = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Fold lowercases and strips accents: "Crème Fraîche" → "creme fraiche".
func Fold(s string) string {
	out, _, err := transform.String(accentStripper, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(out)
}

var (
	nonWord    = regexp.MustCompile(`[^a-z0-9%.,/ ]+`)
	spaces     = regexp.MustCompile(`\s+`)
	moneyToken = regexp.MustCompile(`(^|\s)-?€?\d+[.,]\d{2}-?(\s|$)`)
	// Common receipt abbreviations, expanded so aliases and search line up.
	abbreviations = map[string]string{
		"halfv": "halfvol", "halfvolle": "halfvol", "hv": "halfvol",
		"vl": "vol", "volle": "vol",
		"ltr": "l", "liter": "l",
		"gr": "g", "gram": "g",
		"st": "stuks", "stk": "stuks",
		"&": "en",
	}
)

// Normalise turns raw receipt or product text into the canonical form used
// for alias keys and fuzzy scoring.
func Normalise(s string) string {
	s = Fold(s)
	s = strings.ReplaceAll(s, "&", " en ")
	s = moneyToken.ReplaceAllString(s, " ")
	s = nonWord.ReplaceAllString(s, " ")
	words := strings.Fields(spaces.ReplaceAllString(s, " "))
	for i, w := range words {
		w = strings.Trim(w, ".,/")
		if x, ok := abbreviations[w]; ok {
			w = x
		}
		words[i] = w
	}
	return strings.Join(strings.Fields(strings.Join(words, " ")), " ")
}
