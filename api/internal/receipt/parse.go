// Package receipt turns OCR output into structured receipt lines using
// deterministic rules (no models). See docs/PLAN.md §5.
package receipt

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"pantry/internal/match"
	"pantry/internal/ocr"
)

// ParserVersion is stored on each receipt; bump it whenever rules change so
// receipts can be selectively re-parsed.
const ParserVersion = 1

type Kind string

const (
	KindItem     Kind = "item"
	KindDiscount Kind = "discount"
	KindDeposit  Kind = "deposit"
	KindBag      Kind = "bag"
)

type Line struct {
	RawText        string
	Kind           Kind
	Qty            float64
	Unit           string // pc | kg
	UnitPriceCents *int64
	LineTotalCents int64
	VATCode        string
	Parent         int // index into Result.Lines, -1 if none
	row            int
	modified       bool // qty/weight already applied
}

type Result struct {
	Lines         []Line
	TotalCents    *int64
	SumCents      int64
	PurchasedAt   *time.Time
	PaymentMethod string
	Reasons       []string
	Rows          []Row
}

type Options struct {
	Location *time.Location
	Now      time.Time
}

// Review reasons.
const (
	ReasonNoTotal       = "no_total"
	ReasonTotalMismatch = "total_mismatch"
	ReasonLineMath      = "line_math"
	ReasonNoDate        = "no_date"
	ReasonLowConf       = "low_conf"
	ReasonUnparsedRows  = "unparsed_rows"
)

type rowKind int

const (
	rIgnore rowKind = iota
	rItem
	rDesc
	rQty
	rWeight
	rDiscount
	rDeposit
	rBag
	rSubtotal
)

var (
	totalRe    = regexp.MustCompile(`^(totaal|te betalen|total|te voldoen|totaalbedrag)\b`)
	subtotalRe = regexp.MustCompile(`^sub\s*-?\s*tota`)
	paymentRe  = regexp.MustCompile(`\b(pin|pinnen|pinbetaling|maestro|v ?pay|visa|mastercard|contant|contactloos|creditcard|debit|betaald)\b`)
	ignoreRe   = regexp.MustCompile(`^(btw|b\.t\.w|vat|aantal|artikelen|bonuskaart|air ?miles|klantenkaart)\b`)
	discountRe = regexp.MustCompile(`(bonus|korting|actie|voordeel|kassakorting|prijsvoordeel|aanbieding)`)
	depositRe  = regexp.MustCompile(`(statiegeld|emballage|leeggoed)`)
	bagRe      = regexp.MustCompile(`(^|\s)((draag|plastic |boodschappen)?tas(je)?|zak|bag)(\s|$)`)
	// "2 x 0,89" (unit price inside the text); total, if any, is the row amount.
	qtyFullRe = regexp.MustCompile(`^(\d{1,3})\s*[x×*]\s*(?:€\s*)?(-?\d+[.,]\d{2})$`)
	// "2 x" / "2x" with the unit price as the row amount.
	qtyBareRe = regexp.MustCompile(`^(\d{1,3})\s*[x×*]$`)
	// "MELK 2 x 0,89" — qty embedded in an item row.
	qtyInlineRe = regexp.MustCompile(`(?:^|\s)(\d{1,3})\s*[x×*]\s*(?:€\s*)?(\d+[.,]\d{2})(?:\s|$)`)
	weightRe    = regexp.MustCompile(`(\d+[.,]\d{1,3})\s*kg\s*[x×*@]?\s*(?:€|eur)?\s*(\d+[.,]\d{2})(?:\s*(?:€|eur)?\s*/\s*kg)?`)
	letters     = regexp.MustCompile(`[a-z]{2,}`)
)

type rowInfo struct {
	kind  rowKind
	qty   float64
	unit  string
	price *int64 // unit price
	total *int64 // line total if the row carries one
	desc  string // description text with qty/weight removed
}

// Parse extracts receipt lines, total, date and payment from OCR output.
func Parse(res ocr.Result, opt Options) Result {
	if opt.Location == nil {
		opt.Location = time.UTC
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	rows := BuildRows(res.Lines)
	out := Result{Rows: rows}

	folded := make([]string, len(rows))
	raws := make([]string, len(rows))
	for i, r := range rows {
		folded[i] = strings.TrimSpace(match.Fold(r.Text))
		raws[i] = r.Raw
	}

	totalIdx := -1
	for i, r := range rows {
		if r.Amount != nil && totalRe.MatchString(folded[i]) && !subtotalRe.MatchString(folded[i]) {
			totalIdx = i
			break
		}
	}
	end := len(rows)
	if totalIdx >= 0 {
		out.TotalCents = rows[totalIdx].Amount
		end = totalIdx
		for i := totalIdx + 1; i < len(rows); i++ {
			if m := paymentRe.FindString(match.Fold(rows[i].Raw)); m != "" {
				out.PaymentMethod = paymentMethod(m)
				break
			}
		}
	} else {
		// Without a total, stop the item region at the first payment row.
		for i := range rows {
			if rows[i].Amount != nil && paymentRe.MatchString(folded[i]) {
				end = i
				break
			}
		}
	}

	infos := make([]rowInfo, len(rows))
	start := -1
	for i := 0; i < end; i++ {
		infos[i] = classify(rows[i], folded[i])
		if start < 0 && (rows[i].Amount != nil || infos[i].kind == rQty || infos[i].kind == rWeight) &&
			infos[i].kind != rSubtotal {
			start = i
		}
	}
	if start < 0 {
		start = end
	} else if k := infos[start].kind; (k == rQty || k == rWeight) && start > 0 && infos[start-1].kind == rDesc {
		start-- // first item's name sits on the row above its qty/weight row
	}

	lines, unattached := assemble(rows, infos, start, end)
	out.Lines = lines

	for _, l := range out.Lines {
		out.SumCents += l.LineTotalCents
	}
	if t, ok := FindDate(raws, opt.Location, opt.Now); ok {
		out.PurchasedAt = &t
	}

	out.Reasons = validate(out, res.Lines, unattached)
	return out
}

func classify(r Row, f string) rowInfo {
	switch {
	case subtotalRe.MatchString(f):
		return rowInfo{kind: rSubtotal}
	case ignoreRe.MatchString(f):
		return rowInfo{kind: rIgnore}
	}

	if m := qtyFullRe.FindStringSubmatch(f); m != nil {
		q, _ := ParseDecimal(m[1])
		p, _ := ParseAmount(m[2])
		return rowInfo{kind: rQty, qty: q, unit: "pc", price: &p, total: r.Amount}
	}
	if m := qtyBareRe.FindStringSubmatch(f); m != nil && r.Amount != nil {
		q, _ := ParseDecimal(m[1])
		return rowInfo{kind: rQty, qty: q, unit: "pc", price: r.Amount}
	}
	if loc := weightRe.FindStringSubmatchIndex(f); loc != nil {
		q, _ := ParseDecimal(f[loc[2]:loc[3]])
		p, _ := ParseAmount(f[loc[4]:loc[5]])
		desc := strings.TrimSpace(r.Text[:min(loc[0], len(r.Text))] + " " + r.Text[min(loc[1], len(r.Text)):])
		info := rowInfo{kind: rWeight, qty: q, unit: "kg", price: &p, total: r.Amount}
		if letters.MatchString(match.Fold(desc)) && r.Amount != nil {
			info.kind, info.desc = rItem, desc
		}
		return info
	}

	if r.Amount == nil {
		if letters.MatchString(f) {
			return rowInfo{kind: rDesc, desc: r.Text}
		}
		return rowInfo{kind: rIgnore}
	}

	switch {
	case depositRe.MatchString(f):
		return rowInfo{kind: rDeposit, total: r.Amount}
	case *r.Amount < 0 || discountRe.MatchString(f):
		t := -abs64(*r.Amount) // discounts always reduce the sum
		return rowInfo{kind: rDiscount, total: &t}
	case bagRe.MatchString(f) && *r.Amount <= 100:
		return rowInfo{kind: rBag, total: r.Amount}
	}

	info := rowInfo{kind: rItem, qty: 1, unit: "pc", total: r.Amount, desc: r.Text}
	if loc := qtyInlineRe.FindStringSubmatchIndex(f); loc != nil {
		q, _ := ParseDecimal(f[loc[2]:loc[3]])
		p, _ := ParseAmount(f[loc[4]:loc[5]])
		info.qty, info.price = q, &p
		info.desc = strings.TrimSpace(r.Text[:min(loc[0], len(r.Text))] + " " + r.Text[min(loc[1], len(r.Text)):])
	}
	return info
}

type modifier struct {
	row   int
	qty   float64
	unit  string
	price int64
}

func assemble(rows []Row, infos []rowInfo, start, end int) ([]Line, int) {
	var lines []Line
	var mods []modifier
	unattached := 0
	afterSubtotal := false
	pendingDesc := -1

	newItem := func(row int, desc string, info rowInfo) {
		l := Line{
			RawText: strings.TrimSpace(desc), Kind: KindItem, Qty: 1, Unit: "pc",
			LineTotalCents: *info.total, VATCode: rows[row].VAT, Parent: -1, row: row,
		}
		if info.price != nil {
			l.Qty, l.Unit, l.modified = info.qty, info.unit, true
			p := *info.price
			l.UnitPriceCents = &p
		} else {
			p := l.LineTotalCents
			l.UnitPriceCents = &p
		}
		lines = append(lines, l)
	}

	for i := start; i < end; i++ {
		info := infos[i]
		switch info.kind {
		case rDesc:
			pendingDesc = i
			continue
		case rItem:
			newItem(i, info.desc, info)
		case rQty, rWeight:
			if info.total != nil && pendingDesc == i-1 {
				// Description on one row, qty/weight + total on the next.
				newItem(i, rows[pendingDesc].Text, info)
			} else if n := len(lines); info.total != nil && n > 0 && lines[n-1].Kind == KindItem &&
				!lines[n-1].modified && lines[n-1].LineTotalCents == *info.total {
				// "MELK 1,78" followed by "2 x 0,89 1,78": detail row for the item above.
				mods = append(mods, modifier{row: i, qty: info.qty, unit: info.unit, price: *info.price})
			} else if info.total != nil {
				newItem(i, rows[i].Text, info)
			} else {
				mods = append(mods, modifier{row: i, qty: info.qty, unit: info.unit, price: *info.price})
			}
		case rDiscount:
			l := Line{RawText: rows[i].Text, Kind: KindDiscount, Qty: 1, Unit: "pc",
				LineTotalCents: *info.total, VATCode: rows[i].VAT, Parent: -1, row: i}
			l.Parent = discountParent(lines, l.RawText, afterSubtotal)
			lines = append(lines, l)
		case rDeposit, rBag:
			k := KindDeposit
			if info.kind == rBag {
				k = KindBag
			}
			p := *info.total
			lines = append(lines, Line{RawText: rows[i].Text, Kind: k, Qty: 1, Unit: "pc",
				UnitPriceCents: &p, LineTotalCents: p, VATCode: rows[i].VAT, Parent: -1, row: i})
		case rSubtotal:
			afterSubtotal = true
		}
		pendingDesc = -1
	}

	for _, m := range mods {
		if idx := pickModifierTarget(lines, m); idx >= 0 {
			l := &lines[idx]
			l.Qty, l.Unit, l.modified = m.qty, m.unit, true
			p := m.price
			l.UnitPriceCents = &p
		} else {
			unattached++
		}
	}

	return lines, unattached
}

// pickModifierTarget chooses the item a qty/weight row belongs to: the
// adjacent item (previous or next) whose total matches qty × unit price,
// falling back to the previous unmodified item.
func pickModifierTarget(lines []Line, m modifier) int {
	prev, next := -1, -1
	for i, l := range lines {
		if l.Kind != KindItem || l.modified {
			continue
		}
		if l.row < m.row {
			prev = i
		} else if next < 0 {
			next = i
		}
	}
	want := mulCents(m.qty, m.price)
	fits := func(i int) bool { return i >= 0 && abs64(lines[i].LineTotalCents-want) <= 1 }
	switch {
	case fits(prev):
		return prev
	case fits(next):
		return next
	case prev >= 0:
		return prev
	default:
		return next
	}
}

// discountParent links a discount to the item it reduces: an item sharing a
// distinctive word with the discount text, else (before the subtotal) the
// preceding item.
func discountParent(lines []Line, text string, afterSubtotal bool) int {
	words := significantWords(text)
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Kind != KindItem {
			continue
		}
		itemWords := significantWords(lines[i].RawText)
		for w := range words {
			if itemWords[w] {
				return i
			}
		}
	}
	if afterSubtotal {
		return -1
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Kind == KindItem {
			return i
		}
	}
	return -1
}

func significantWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(match.Normalise(s)) {
		if len(w) >= 4 && !discountRe.MatchString(w) {
			out[w] = true
		}
	}
	return out
}

func paymentMethod(m string) string {
	switch {
	case strings.Contains(m, "contant"):
		return "contant"
	case strings.Contains(m, "visa"), strings.Contains(m, "mastercard"), strings.Contains(m, "credit"):
		return "creditcard"
	default:
		return "pin"
	}
}

func validate(r Result, ocrLines []ocr.Line, unattached int) []string {
	reasons := []string{}
	if r.TotalCents == nil {
		reasons = append(reasons, ReasonNoTotal)
	} else if abs64(r.SumCents-*r.TotalCents) > 1 {
		reasons = append(reasons, ReasonTotalMismatch)
	}
	for _, l := range r.Lines {
		if l.Kind == KindItem && l.modified && l.UnitPriceCents != nil {
			tol := int64(1)
			if l.Unit == "pc" {
				tol = 0
			}
			if abs64(mulCents(l.Qty, *l.UnitPriceCents)-l.LineTotalCents) > tol {
				reasons = append(reasons, ReasonLineMath)
				break
			}
		}
	}
	if r.PurchasedAt == nil {
		reasons = append(reasons, ReasonNoDate)
	}
	if medianConf(ocrLines) < 0.80 {
		reasons = append(reasons, ReasonLowConf)
	}
	if unattached >= 1 {
		reasons = append(reasons, ReasonUnparsedRows)
	}
	return reasons
}

func medianConf(ls []ocr.Line) float64 {
	if len(ls) == 0 {
		return 0
	}
	c := make([]float64, len(ls))
	for i, l := range ls {
		c[i] = l.Conf
	}
	sort.Float64s(c)
	return c[len(c)/2]
}
