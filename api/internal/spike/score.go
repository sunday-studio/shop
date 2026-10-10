package spike

import (
	"sort"
	"time"

	"shop/internal/match"
	"shop/internal/receipt"
)

// Targets from docs/PLAN.md §13.
var Targets = struct {
	TotalCorrect, SelfConsistent, DateCorrect, LineF1 float64
	DefaultMS, FallbackMS                             int64
	DefaultMemMB, FallbackMemMB                       float64
}{0.85, 0.80, 0.95, 0.85, 15000, 90000, 3072, 10240}

// ReceiptScore is one engine × one labelled receipt.
type ReceiptScore struct {
	ID, Engine, Shop string
	OCRError         string
	TruthTotal       int64
	ParsedTotal      *int64
	ParsedSum        int64
	TotalCorrect     bool // parsed total == labelled total
	SelfConsistent   bool // Σ lines == parsed total
	DateCorrect      bool
	ItemsCorrect     bool
	AutoOK           bool // no review reasons
	SilentError      bool // AutoOK but the total is wrong: the dangerous case
	Reasons          []string
	EngineMS, WallMS int64
	HasLines         bool
	LineTP, LineFP   int
	LineFN           int
	ParsedItems      int
	TruthItems       int
}

// EngineScore aggregates one engine over all labelled receipts.
type EngineScore struct {
	Engine                 string
	Version                string
	N                      int // labelled receipts with cached OCR
	OCRErrors              int
	TotalCorrect           int
	SelfConsistent         int
	DateCorrect            int
	ItemsCorrect           int
	AutoOK                 int
	SilentErrors           int
	LineTP, LineFP, LineFN int
	LineReceipts           int
	MedianMS, P95MS        int64
	PeakMemMB              float64
	Reasons                map[string]int
	ByShop                 map[string][2]int // shop → {correct, n}
	Receipts               []ReceiptScore
}

func (e EngineScore) Rate(n int) float64 {
	if e.N == 0 {
		return 0
	}
	return float64(n) / float64(e.N)
}

// LineF1 over all line-labelled receipts.
func (e EngineScore) LineF1() (p, r, f1 float64) {
	if e.LineTP == 0 {
		return 0, 0, 0
	}
	p = float64(e.LineTP) / float64(e.LineTP+e.LineFP)
	r = float64(e.LineTP) / float64(e.LineTP+e.LineFN)
	return p, r, 2 * p * r / (p + r)
}

// ScoreEngine parses every cached OCR result for labelled receipts.
func ScoreEngine(d Dir, engine string, truth map[string]Truth, loc *time.Location, now time.Time) (EngineScore, error) {
	es := EngineScore{Engine: engine, Reasons: map[string]int{}, ByShop: map[string][2]int{}}
	meta := d.LoadMeta(engine)
	es.Version, es.PeakMemMB = meta.Version, meta.PeakMemMB

	ids := make([]string, 0, len(truth))
	for id := range truth {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var times []int64
	for _, id := range ids {
		t := truth[id]
		c, ok, err := d.LoadCached(engine, id)
		if err != nil {
			return es, err
		}
		if !ok {
			continue
		}
		es.N++
		rs := ReceiptScore{ID: id, Engine: engine, Shop: t.Shop, TruthTotal: t.TotalCents, TruthItems: t.Items,
			EngineMS: c.Result.MS, WallMS: c.WallMS}
		shop := es.ByShop[t.Shop]
		shop[1]++

		if c.Error != "" {
			rs.OCRError = c.Error
			es.OCRErrors++
			es.ByShop[t.Shop] = shop
			es.Receipts = append(es.Receipts, rs)
			continue
		}
		times = append(times, max(c.Result.MS, c.WallMS))

		p := receipt.Parse(c.Result, receipt.Options{Location: loc, Now: now})
		rs.ParsedTotal, rs.ParsedSum, rs.Reasons = p.TotalCents, p.SumCents, p.Reasons
		rs.TotalCorrect = p.TotalCents != nil && *p.TotalCents == t.TotalCents
		rs.SelfConsistent = p.TotalCents != nil && abs(p.SumCents-*p.TotalCents) <= 1
		rs.DateCorrect = p.PurchasedAt != nil && p.PurchasedAt.In(loc).Format(time.DateOnly) == t.Date
		for _, l := range p.Lines {
			if l.Kind == receipt.KindItem {
				rs.ParsedItems++
			}
		}
		rs.ItemsCorrect = rs.ParsedItems == t.Items
		rs.AutoOK = len(p.Reasons) == 0
		rs.SilentError = rs.AutoOK && !rs.TotalCorrect

		if lines, has, err := d.LoadLines(id); err != nil {
			return es, err
		} else if has {
			rs.HasLines = true
			rs.LineTP, rs.LineFP, rs.LineFN = matchLines(p.Lines, lines)
			es.LineTP += rs.LineTP
			es.LineFP += rs.LineFP
			es.LineFN += rs.LineFN
			es.LineReceipts++
		}

		for _, r := range p.Reasons {
			es.Reasons[r]++
		}
		count := func(b bool, n *int) {
			if b {
				*n++
			}
		}
		count(rs.TotalCorrect, &es.TotalCorrect)
		count(rs.SelfConsistent, &es.SelfConsistent)
		count(rs.DateCorrect, &es.DateCorrect)
		count(rs.ItemsCorrect, &es.ItemsCorrect)
		count(rs.AutoOK, &es.AutoOK)
		count(rs.SilentError, &es.SilentErrors)
		if rs.TotalCorrect {
			shop[0]++
		}
		es.ByShop[t.Shop] = shop
		es.Receipts = append(es.Receipts, rs)
	}

	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	if n := len(times); n > 0 {
		es.MedianMS = times[n/2]
		es.P95MS = times[min(n-1, int(float64(n)*0.95))]
	}
	return es, nil
}

// LineMatchScore is the minimum text similarity for a parsed line to count
// as the labelled line (amount and kind must match exactly).
const LineMatchScore = 0.8

// matchLines greedily pairs parsed lines with labelled lines.
func matchLines(parsed []receipt.Line, truth []TruthLine) (tp, fp, fn int) {
	used := make([]bool, len(truth))
	for _, p := range parsed {
		best, bestScore := -1, LineMatchScore
		pn := match.Normalise(p.RawText)
		for i, t := range truth {
			if used[i] || t.TotalCents != p.LineTotalCents || t.Kind != string(p.Kind) {
				continue
			}
			s := match.Score(pn, match.Normalise(t.Text))
			if pn == "" && match.Normalise(t.Text) == "" {
				s = 1
			}
			if s >= bestScore {
				best, bestScore = i, s
			}
		}
		if best >= 0 {
			used[best] = true
			tp++
		} else {
			fp++
		}
	}
	for _, u := range used {
		if !u {
			fn++
		}
	}
	return tp, fp, fn
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
