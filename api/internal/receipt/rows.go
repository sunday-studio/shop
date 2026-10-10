package receipt

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"shop/internal/ocr"
)

// Row is one visual line of the receipt, rebuilt from OCR boxes.
type Row struct {
	Raw    string  // full text, left to right
	Text   string  // Raw minus the trailing amount / VAT code
	Amount *int64  // right-most money token, cents
	VAT    string  // trailing VAT code (A/B/C/D, 1/2) if present
	Conf   float64 // min OCR confidence of the row's pieces
	Y      float64 // vertical centre, for debugging
}

var (
	vatTokenRe     = regexp.MustCompile(`^(?i:[a-d]|[0-2])$`)
	amountVATGlued = regexp.MustCompile(`^(.*\d[.,]\d{2}-?)[.,:;']?([A-Da-d])$`)
	// OCR often reads the decimal comma as a semicolon: "1;338 kg".
	semicolonDecimal = regexp.MustCompile(`(\d);(\d)`)
)

// cleanOCRText fixes systematic OCR confusions before parsing.
func cleanOCRText(s string) string {
	return semicolonDecimal.ReplaceAllString(s, "$1,$2")
}

// trimNoise drops stray punctuation OCR attaches to either end of a token
// ("B." / "3,39." / ".4,81" / "4,51'"). A leading minus is kept.
func trimNoise(tok string) string {
	return strings.Trim(tok, ".,:;'`\"")
}

type piece struct {
	ocr.Line
	y0, y1, yc float64
}

// BuildRows clusters OCR lines into rows by vertical overlap and splits
// off the price column.
func BuildRows(lines []ocr.Line) []Row {
	ps := make([]piece, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		ps = append(ps, piece{Line: l, y0: l.Box[1], y1: l.Box[3], yc: (l.Box[1] + l.Box[3]) / 2})
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].yc < ps[j].yc })

	var groups [][]piece
	for _, p := range ps {
		if n := len(groups); n > 0 && overlaps(groups[n-1], p.y0, p.y1) {
			groups[n-1] = append(groups[n-1], p)
			continue
		}
		groups = append(groups, []piece{p})
	}

	rows := make([]Row, 0, len(groups))
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool { return g[i].Box[0] < g[j].Box[0] })
		texts := make([]string, len(g))
		conf, ysum := 1.0, 0.0
		for i, p := range g {
			texts[i] = strings.TrimSpace(p.Text)
			conf = min(conf, p.Conf)
			ysum += p.yc
		}
		r := splitAmount(cleanOCRText(strings.Join(texts, " ")))
		r.Conf = conf
		r.Y = ysum / float64(len(g))
		rows = append(rows, r)
	}
	return mergeSplitQtyRows(mergeBlankAmountRows(rows))
}

// overlaps reports whether [y0,y1] overlaps the row's vertical extent by at
// least half of the smaller height.
func overlaps(row []piece, y0, y1 float64) bool {
	// Compare against the row's first piece (its anchor) to stop rows
	// drifting downwards on slightly skewed receipts.
	a := row[0]
	inter := min(a.y1, y1) - max(a.y0, y0)
	if inter <= 0 {
		return false
	}
	return inter >= 0.5*min(a.y1-a.y0, y1-y0)
}

// splitAmount separates "AH MELK 1,29 B" into text "AH MELK", amount 129, VAT "B".
func splitAmount(raw string) Row {
	r := Row{Raw: raw, Text: raw}
	toks := strings.Fields(raw)
	if len(toks) == 0 {
		return r
	}
	// Ignore trailing tokens that are only punctuation, and stray
	// punctuation on the last two tokens (price and VAT code).
	for len(toks) > 0 && trimNoise(toks[len(toks)-1]) == "" {
		toks = toks[:len(toks)-1]
	}
	if len(toks) == 0 {
		return r
	}
	for i := max(0, len(toks)-2); i < len(toks); i++ {
		toks[i] = trimNoise(toks[i])
	}
	vat := ""
	end := len(toks)
	if m := amountVATGlued.FindStringSubmatch(toks[end-1]); m != nil {
		toks[end-1], vat = m[1], strings.ToUpper(m[2])
	} else if end >= 2 && vatTokenRe.MatchString(toks[end-1]) {
		if _, ok := ParseAmount(toks[end-2]); ok {
			vat = strings.ToUpper(toks[end-1])
			end--
		}
	}

	last := toks[end-1]
	// "€ 1,29" / "EUR 1,29": currency split from the number.
	cents, ok := ParseAmount(last)
	if !ok {
		return r
	}
	end--
	if end > 0 {
		if c := strings.ToLower(toks[end-1]); c == "€" || c == "eur" {
			end--
		}
	}
	r.Amount = &cents
	r.VAT = vat
	r.Text = strings.Join(toks[:end], " ")
	return r
}

// mergeBlankAmountRows joins an amount that OCR put on its own row, with no text, to the label row above
// it that has none: "22 SUBTOTAAL" / "8,68", "TOTAAL" / "84,68", "1 MANDARIJNEN" / "2,79".
func mergeBlankAmountRows(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if n := len(out); n > 0 && strings.TrimSpace(r.Text) == "" && r.Amount != nil &&
			out[n-1].Amount == nil && strings.TrimSpace(out[n-1].Text) != "" {
			out[n-1].Amount, out[n-1].VAT = r.Amount, r.VAT
			out[n-1].Raw = strings.TrimSpace(out[n-1].Raw + " " + r.Raw)
			continue
		}
		out = append(out, r)
	}
	return out
}

var leadingQtyRe = regexp.MustCompile(`^(\d{1,2})\s+\S`)

// mergeSplitQtyRows repairs a photo where the line total landed beside a junk fragment and the quantity
// row below it carries only the unit price: "Nt 1,70" / "2 DILLE 0,85" is one line "2 DILLE 1,70". Applied
// only when the arithmetic fits exactly (qty x unit price == the junk row's amount).
func mergeSplitQtyRows(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for i := 0; i < len(rows); i++ {
		r := rows[i]
		if i+1 < len(rows) && r.Amount != nil && junkText(r.Text) {
			nx := rows[i+1]
			if m := leadingQtyRe.FindStringSubmatch(nx.Text); m != nil && nx.Amount != nil {
				if q, _ := strconv.Atoi(m[1]); q >= 2 && int64(q)**nx.Amount == *r.Amount {
					out = append(out, Row{Raw: nx.Text + " " + r.Raw, Text: nx.Text, Amount: r.Amount, VAT: r.VAT, Conf: min(r.Conf, nx.Conf), Y: nx.Y})
					i++
					continue
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// junkText: at most two letters and no digits, e.g. the OCR fragment "Nt".
func junkText(s string) bool {
	letters := 0
	for _, c := range s {
		switch {
		case unicode.IsDigit(c):
			return false
		case unicode.IsLetter(c):
			letters++
		}
	}
	return letters <= 2
}
