package receipt

import (
	"regexp"
	"strings"
)

// Receipts with an "AANTAL / OMSCHRIJVING / PRIJS / BEDRAG" layout print one row per item:
//
//	"2 COCA-COLA 2,49" with the line amount 4,98 at the right,
//
// i.e. count, name, unit price, then the amount that BuildRows splits off. The count column is read
// by OCR with systematic errors (a "1" comes out as "7", "T", "J", "I", "-"; "2" as "21", "2T"), and
// it is sometimes lost entirely. The printed arithmetic is therefore the authority:
//
//   - unit price in the text and amount == unit price: one piece, whatever the count column says;
//   - unit price in the text and amount == k x unit price (k >= 2): k pieces, even when the count
//     is garbled or missing ("15 RED BULL 1,75" with 8,75 is 5 pieces);
//   - no unit price in the text: the count is believed only when the amount divides by it exactly
//     into a plausible unit price, and never for "7" (the usual misreading of "1" and "2").
//
// A trailing price that does not divide the amount is a per-kg price (the weight is on the next row,
// see attachWeights) or noise: the line stays one piece at the amount.

var (
	leadCountRe = regexp.MustCompile(`^(\d{1,2})\s+\S`)
)

// minUnitCents is the cheapest believable unit price when only the count column vouches for the
// quantity; it keeps "3 MINUTEN" style names with a stray total from becoming pieces.
const minUnitCents = 5

// countFromText derives the quantity and unit price of an item row from its text (the row without the
// amount) and the line amount. ok is false when the line stays a single piece.
func countFromText(text string, total int64) (qty float64, unitCents int64, ok bool) {
	if total <= 0 {
		return 0, 0, false
	}
	text = strings.TrimSpace(text)
	if p, found := trailingPrice(text); found {
		if p <= 0 || p >= total || total%p != 0 {
			return 0, 0, false
		}
		k := total / p
		if k < 2 || k > 99 {
			return 0, 0, false
		}
		return float64(k), p, true
	}
	m := leadCountRe.FindStringSubmatch(text)
	if m == nil {
		return 0, 0, false
	}
	n := atoi(m[1])
	if n < 2 || n == 7 || total%int64(n) != 0 || total/int64(n) < minUnitCents {
		return 0, 0, false
	}
	return float64(n), total / int64(n), true
}

// trailingPrice returns the money token that ends the text, if any.
func trailingPrice(text string) (int64, bool) {
	f := strings.Fields(text)
	if len(f) < 2 { // a lone price is not a unit price
		return 0, false
	}
	last := f[len(f)-1]
	p, ok := ParseAmount(last)
	if !ok || p < 0 {
		return 0, false
	}
	return p, true
}

// textPriceOf returns the trailing price of an item row whether or not it divided the amount: the
// per-kg price of a "GEMBER LOS 5,98" row.
func textPriceOf(text string) *int64 {
	if p, ok := trailingPrice(strings.TrimSpace(text)); ok && p > 0 {
		return &p
	}
	return nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// weightOnlyRe is a row holding only a weight: "0.410 KG", "1,174 kg".
var weightOnlyRe = regexp.MustCompile(`^(\d+[.,]\d{1,3})\s*kg$`)

// RequantifyText applies the count rules to a stored line: the same derivation the parser uses, for
// lines saved before the rules existed. It returns the quantity and unit price in cents.
func RequantifyText(rawText string, total int64) (qty float64, unitCents int64, ok bool) {
	return countFromText(rawText, total)
}
