package receipt

import (
	"regexp"
	"strconv"
	"strings"
)

// amountRe matches a money token as printed on Dutch/English receipts:
// "1,29", "1.29", "-0,50", "0,50-", "1.234,56", "€1,29", "EUR 1,29".
var amountRe = regexp.MustCompile(`^(-)?(?:€|eur)?\s*(\d{1,3}(?:[.\s]\d{3})*|\d+)[.,](\d{2})(-)?$`)

// ParseAmount converts a money token to cents. It requires exactly two
// decimals so that quantities ("0,512") and integers are not mistaken
// for prices.
func ParseAmount(s string) (int64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	m := amountRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	whole := strings.NewReplacer(".", "", " ", "").Replace(m[2])
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	frac, _ := strconv.ParseInt(m[3], 10, 64)
	cents := w*100 + frac
	if m[1] == "-" || m[4] == "-" {
		cents = -cents
	}
	return cents, true
}

// ParseDecimal parses "0,512" or "0.512" as a float.
func ParseDecimal(s string) (float64, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// mulCents returns qty × unitCents rounded half away from zero.
func mulCents(qty float64, unitCents int64) int64 {
	v := qty * float64(unitCents)
	if v < 0 {
		return int64(v - 0.5)
	}
	return int64(v + 0.5)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
