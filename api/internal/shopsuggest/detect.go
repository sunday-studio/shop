// Package shopsuggest reads a receipt's header (chain name + branch address) from OCR rows so the
// app can suggest which shop a receipt belongs to. Supermarket-style receipts only: when the header
// is unreadable (cropped slips) it suggests nothing. No LLMs; plain rules + fuzzy matching.
package shopsuggest

import (
	"regexp"
	"strings"
	"unicode"

	"shop/internal/match"
)

// headerRows is how far down the receipt the chain name may appear.
const headerRows = 10

// Detected is what the header of one receipt says.
type Detected struct {
	Chain    string   // canonical chain name ("Albert Heijn"), or a name-like row for unknown shops
	Known    bool     // Chain is in the built-in list
	Branch   string   // "Hoofdstraat 12, Teststad" ("" when no address row was found)
	Key      string   // chain + street + number, folded; identifies the branch across receipts
	Street   string   // folded street name
	Number   string   // house number, e.g. "68-70"
	Evidence []string // the header rows used
	Conf     float64

	hasPostcode bool
}

type chain struct {
	name    string
	aliases []string // folded; short ones (<=4 runes) must match a whole word
}

var chains = []chain{
	{"Albert Heijn", []string{"albert heijn"}},
	{"Dirk", []string{"dirk van den broek", "dirk"}},
	{"Jumbo", []string{"jumbo"}},
	{"Lidl", []string{"lidl"}},
	{"Aldi", []string{"aldi"}},
	{"Plus", []string{"plus"}},
	{"Coop", []string{"coop"}},
	{"Spar", []string{"spar"}},
	{"Vomar", []string{"vomar"}},
	{"Hoogvliet", []string{"hoogvliet"}},
	{"Dekamarkt", []string{"dekamarkt", "deka markt"}},
	{"Poiesz", []string{"poiesz"}},
	{"Ekoplaza", []string{"ekoplaza"}},
	{"Kruidvat", []string{"kruidvat"}},
	{"Etos", []string{"etos"}},
	{"Hema", []string{"hema"}},
	{"Action", []string{"action"}},
	{"Jan Linders", []string{"jan linders"}},
	{"Nettorama", []string{"nettorama"}},
	{"Boni", []string{"boni"}},
}

// Keyboard keys that show up in photos taken of a receipt lying on a laptop.
var noiseWords = map[string]bool{
	"command": true, "cmd": true, "option": true, "ption": true, "ctrl": true, "otrl": true, "control": true,
	"shift": true, "shitt": true, "shifi": true, "caps": true, "lock": true, "tab": true, "alt": true,
	"esc": true, "fn": true, "return": true, "delete": true,
}

var (
	postcodeRe = regexp.MustCompile(`\b(\d{4})\s?([a-z]{2})\b`)
	// street + house number: "lange kerkstraat 34", "broersveld 68-70", "ring 5a"
	streetRe = regexp.MustCompile(`^([a-z][a-z .'-]{2,}?)\s+(\d{1,4}\s?[a-z]?(?:\s?-\s?\d{1,4})?)\b`)
	// Rows that are receipt furniture, never a shop name.
	furnitureRe = regexp.MustCompile(`aantal|omschrijving|prijs|bedrag|kassabon|totaal|eur|artikelen|bon|subtotaal|bonus`)
)

func isNoise(folded string) bool {
	words := strings.Fields(folded)
	if len(words) == 0 {
		return true
	}
	for _, w := range words {
		if !noiseWords[w] && len([]rune(w)) > 1 {
			return false
		}
	}
	return true
}

// matchChain finds a known chain in a folded row: whole-word match for short aliases, a fuzzy match
// of the alias against consecutive words for longer ones (tolerates "Albert Heiin").
func matchChain(folded string) (c chain, score float64, rest string, ok bool) {
	words := strings.Fields(folded)
	var best chain
	bestScore, bestEnd := 0.0, 0
	for _, c := range chains {
		for _, a := range c.aliases {
			aw := strings.Fields(a)
			score := 0.0
			end := 0
			for i := 0; i+len(aw) <= len(words); i++ {
				cand := strings.Join(words[i:i+len(aw)], " ")
				var s float64
				if len([]rune(a)) <= 4 {
					if cand == a {
						s = 1
					}
				} else {
					s = match.JaroWinkler(a, cand)
					if s < 0.92 {
						s = 0
					}
				}
				if s > score {
					score, end = s, i+len(aw)
				}
			}
			// the alias that consumes more words wins, then the better score ("dirk van den broek" over "dirk")
			if score > 0 && (end > bestEnd || (end == bestEnd && score > bestScore)) {
				best, bestScore, bestEnd = c, score, end
			}
		}
	}
	return best, bestScore, strings.Join(words[bestEnd:], " "), bestScore > 0
}

// Detect inspects the first rows of a receipt. rows are the visual rows' raw texts, top to bottom.
// Returns nil when nothing safe can be said.
func Detect(rows []string) *Detected {
	n := min(len(rows), headerRows+4)
	folded := make([]string, n)
	for i := 0; i < n; i++ {
		folded[i] = strings.TrimSpace(match.Fold(rows[i]))
	}

	// 1) a known chain in the first header rows
	for i := 0; i < min(n, headerRows); i++ {
		if isNoise(folded[i]) {
			continue
		}
		if c, score, rest, ok := matchChain(folded[i]); ok {
			d := &Detected{Chain: c.name, Known: true, Conf: 0.7 + 0.2*score, Evidence: []string{rows[i]}}
			// "Dirk van den Broek Marktweg 68-70": the address may follow the name on the same row.
			if rest != "" {
				folded = append([]string(nil), folded...)
				rows = append([]string(nil), rows...)
				folded[i], rows[i] = rest, rest
				d.fillBranch(rows, folded, i, n)
			} else {
				d.fillBranch(rows, folded, i+1, n)
			}
			return d
		}
	}

	// 2) unknown shop: a name-like row immediately followed by an address row
	for i := 0; i+1 < min(n, headerRows); i++ {
		if isNoise(folded[i]) || !nameLike(rows[i], folded[i]) {
			continue
		}
		d := &Detected{Chain: titleCase(strings.TrimSpace(rows[i])), Evidence: []string{rows[i]}, Conf: 0.4}
		d.fillBranch(rows, folded, i+1, min(n, i+4))
		// Without a known chain, demand a real postcode: otherwise product rows pass as addresses.
		if d.Branch != "" && d.hasPostcode {
			return d
		}
	}
	return nil
}

func nameLike(raw, folded string) bool {
	words := strings.Fields(folded)
	if len(words) == 0 || len(words) > 4 || len([]rune(folded)) < 3 || furnitureRe.MatchString(folded) {
		return false
	}
	for _, r := range raw {
		if unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// fillBranch looks for street+number (and town / postcode) in the rows after the chain row.
func (d *Detected) fillBranch(rows, folded []string, from, to int) {
	to = min(to, from+4, len(rows))
	street, town := "", ""
	for i := from; i < to; i++ {
		f := folded[i]
		if isNoise(f) {
			continue
		}
		if d.Known { // "Dirk van den Broek Marktweg 68-70": drop the chain's own name
			if _, _, rest, ok := matchChain(f); ok {
				if f = rest; f == "" {
					continue
				}
			}
		}
		if street == "" {
			if m := streetRe.FindStringSubmatch(f); m != nil && !postcodeRe.MatchString(f[:len(m[1])]) {
				d.Street, d.Number = strings.TrimSpace(m[1]), strings.ReplaceAll(strings.TrimSpace(m[2]), " ", "")
				street = d.Street + " " + d.Number
				d.Evidence = append(d.Evidence, rows[i])
				// "Hoofdstraat 12 Teststad": the town trails the number
				rest := strings.TrimFunc(f[len(m[0]):], func(r rune) bool { return !unicode.IsLetter(r) })
				if rest != "" && !strings.ContainsAny(rest, "0123456789") {
					town = rest
				}
				continue
			}
		}
		if m := postcodeRe.FindStringSubmatchIndex(f); m != nil && street != "" {
			if t := strings.TrimSpace(f[m[1]:]); town == "" && t != "" {
				town = strings.Fields(t)[0]
				// drop trailing phone number etc.
				town = strings.TrimFunc(town, func(r rune) bool { return !unicode.IsLetter(r) })
			}
			d.Evidence = append(d.Evidence, rows[i])
			d.hasPostcode = true
			break
		}
		if street != "" && town != "" {
			break
		}
	}
	if street == "" {
		return
	}
	d.Branch = titleCase(street)
	if town != "" {
		d.Branch += ", " + titleCase(town)
	}
	d.Key = match.Fold(d.Chain) + "|" + street
	d.Conf = min(1, d.Conf+0.1)
}

func titleCase(s string) string {
	parts := strings.Fields(strings.ToLower(s))
	for i, p := range parts {
		r := []rune(p)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
		}
		parts[i] = string(r)
	}
	return strings.Join(parts, " ")
}

// SameBranch reports whether two detections describe the same shop branch, tolerating OCR typos in
// the street name ("Marktwag" / "Marktweg"). The house number must match exactly.
func SameBranch(a, b *Detected) bool {
	if a == nil || b == nil || a.Street == "" || b.Street == "" {
		return false
	}
	return match.Fold(a.Chain) == match.Fold(b.Chain) && a.Number == b.Number &&
		match.JaroWinkler(a.Street, b.Street) >= 0.9
}
