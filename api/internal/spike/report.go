package spike

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"shop/internal/receipt"
)

func pct(n, d int) string {
	if d == 0 {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(n)/float64(d))
}

func mark(ok bool) string {
	if ok {
		return " ✓"
	}
	return " ✗"
}

func secs(ms int64) string {
	if ms == 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}

func memStr(mb float64) string {
	if mb == 0 {
		return "n/a"
	}
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

// meetsQuality: accuracy targets that don't depend on speed.
func meetsQuality(e EngineScore) bool {
	_, _, f1 := e.LineF1()
	return e.Rate(e.TotalCorrect) >= Targets.TotalCorrect &&
		e.Rate(e.SelfConsistent) >= Targets.SelfConsistent &&
		e.Rate(e.DateCorrect) >= Targets.DateCorrect &&
		(e.LineReceipts == 0 || f1 >= Targets.LineF1)
}

// fitsBudget: an engine whose memory was not sampled counts as fitting.
func fitsBudget(e EngineScore, ms int64, memMB float64) bool {
	return e.N > 0 && e.MedianMS <= ms && (e.PeakMemMB == 0 || e.PeakMemMB <= memMB)
}

// Recommend picks a default engine (fast and good enough) and a fallback
// (most accurate within the slower budget, different from the default).
// Engines are ranked by total-correct rate, then fewer silent errors, then speed.
func Recommend(scores []EngineScore, dockerOnly map[string]bool) (def, fallback string, why []string) {
	ranked := append([]EngineScore(nil), scores...)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Rate(a.TotalCorrect) != b.Rate(b.TotalCorrect) {
			return a.Rate(a.TotalCorrect) > b.Rate(b.TotalCorrect)
		}
		if a.SilentErrors != b.SilentErrors {
			return a.SilentErrors < b.SilentErrors
		}
		return a.MedianMS < b.MedianMS
	})
	for _, e := range ranked {
		if dockerOnly[e.Engine] && fitsBudget(e, Targets.DefaultMS, Targets.DefaultMemMB) && meetsQuality(e) {
			def = e.Engine
			break
		}
	}
	if def == "" {
		why = append(why, "No Docker engine meets every target within the default budget (≤15 s, ≤3 GB); "+
			"improve the parser rules first (see the failure list), then re-score.")
		for _, e := range ranked {
			if dockerOnly[e.Engine] && fitsBudget(e, Targets.DefaultMS, Targets.DefaultMemMB) {
				def = e.Engine
				why = append(why, fmt.Sprintf("Best Docker engine within budget for now: %s.", def))
				break
			}
		}
	}
	for _, e := range ranked {
		if e.Engine != def && dockerOnly[e.Engine] && fitsBudget(e, Targets.FallbackMS, Targets.FallbackMemMB) &&
			e.Rate(e.TotalCorrect) > rateOf(scores, def) {
			fallback = e.Engine
			break
		}
	}
	if fallback == "" {
		why = append(why, "No fallback: no other Docker engine is more accurate than the default within ≤90 s / ≤10 GB.")
	}
	return def, fallback, why
}

func rateOf(scores []EngineScore, engine string) float64 {
	for _, e := range scores {
		if e.Engine == engine {
			return e.Rate(e.TotalCorrect)
		}
	}
	return 0
}

// WriteReport renders the markdown report.
func WriteReport(path string, scores []EngineScore, engines []Engine, labelled, lineLabelled, images int, generated time.Time) error {
	dockerOnly := map[string]bool{}
	notes := map[string]string{}
	for _, e := range engines {
		dockerOnly[e.Name] = e.URL != ""
		notes[e.Name] = e.Notes
	}
	def, fallback, why := Recommend(scores, dockerOnly)

	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# Spike report: OCR engines × receipt parser\n\n")
	w("_Generated %s by `shop-spike score`. Parser version %d. Re-run after labelling more receipts or changing the parser._\n\n",
		generated.Format("2006-01-02 15:04"), receipt.ParserVersion)
	w("**Corpus:** %d images, %d labelled (totals/date/shop), %d with line-level labels.\n\n", images, labelled, lineLabelled)

	w("## Recommendation\n\n")
	if def != "" {
		w("- **Default engine:** `%s`\n", def)
	}
	if fallback != "" {
		w("- **Fallback engine** (re-OCR of `needs_review` receipts): `%s`\n", fallback)
	}
	for _, s := range why {
		w("- %s\n", s)
	}
	w("\n")

	w("## Results\n\n")
	w("Targets: total correct ≥ %.0f%%, Σ lines = total ≥ %.0f%%, date ≥ %.0f%%, line F1 ≥ %.2f, "+
		"default ≤ %s and ≤ %s, fallback ≤ %s and ≤ %s.\n\n",
		100*Targets.TotalCorrect, 100*Targets.SelfConsistent, 100*Targets.DateCorrect, Targets.LineF1,
		secs(Targets.DefaultMS), memStr(Targets.DefaultMemMB), secs(Targets.FallbackMS), memStr(Targets.FallbackMemMB))
	w("| Engine | n | Total correct | Σ lines = total | Date | Item count | Auto-ok | **Silent errors** | Line P / R / F1 | Median / p95 time | Peak RAM |\n")
	w("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, e := range scores {
		p, r, f1 := e.LineF1()
		lines := "–"
		if e.LineReceipts > 0 {
			lines = fmt.Sprintf("%.2f / %.2f / %.2f%s", p, r, f1, mark(f1 >= Targets.LineF1))
		}
		w("| `%s` | %d | %s%s | %s%s | %s%s | %s | %s | %d | %s | %s / %s | %s |\n",
			e.Engine, e.N,
			pct(e.TotalCorrect, e.N), mark(e.Rate(e.TotalCorrect) >= Targets.TotalCorrect),
			pct(e.SelfConsistent, e.N), mark(e.Rate(e.SelfConsistent) >= Targets.SelfConsistent),
			pct(e.DateCorrect, e.N), mark(e.Rate(e.DateCorrect) >= Targets.DateCorrect),
			pct(e.ItemsCorrect, e.N), pct(e.AutoOK, e.N), e.SilentErrors, lines,
			secs(e.MedianMS), secs(e.P95MS), memStr(e.PeakMemMB))
	}
	w("\n- **Auto-ok**: no review reasons, so the receipt would skip the review queue.\n")
	w("- **Silent errors**: auto-ok but the total is wrong. These are the receipts the validation failed to catch. The target is 0.\n")
	w("- Time is per receipt on this machine, excluding queueing. Peak RAM is sampled from `docker stats`.\n\n")

	w("### Engines\n\n| Engine | Version | Notes |\n|---|---|---|\n")
	for _, e := range scores {
		w("| `%s` | %s | %s |\n", e.Engine, e.Version, notes[e.Engine])
	}
	w("\n")

	// Per shop.
	shops := map[string]bool{}
	for _, e := range scores {
		for s := range e.ByShop {
			shops[s] = true
		}
	}
	shopList := make([]string, 0, len(shops))
	for s := range shops {
		shopList = append(shopList, s)
	}
	sort.Strings(shopList)
	w("## Total correct per shop\n\n| Shop |")
	for _, e := range scores {
		w(" `%s` |", e.Engine)
	}
	w("\n|---|")
	for range scores {
		w("---|")
	}
	w("\n")
	for _, s := range shopList {
		w("| %s |", s)
		for _, e := range scores {
			c := e.ByShop[s]
			w(" %s (%d/%d) |", pct(c[0], c[1]), c[0], c[1])
		}
		w("\n")
	}
	w("\n")

	// Review reasons.
	w("## Review reasons (how often each fired)\n\n| Reason |")
	for _, e := range scores {
		w(" `%s` |", e.Engine)
	}
	w("\n|---|")
	for range scores {
		w("---|")
	}
	w("\n")
	for _, r := range []string{receipt.ReasonNoTotal, receipt.ReasonTotalMismatch, receipt.ReasonLineMath,
		receipt.ReasonNoDate, receipt.ReasonLowConf, receipt.ReasonUnparsedRows} {
		w("| `%s` |", r)
		for _, e := range scores {
			w(" %d |", e.Reasons[r])
		}
		w("\n")
	}
	w("| OCR errors |")
	for _, e := range scores {
		w(" %d |", e.OCRErrors)
	}
	w("\n\n")

	// Silent errors are the dangerous ones, so list them for every engine.
	w("## Silent errors (all engines)\n\n")
	w("Receipts that passed every check but have the wrong total. Each one shows a validation gap.\n\n")
	silent := 0
	for _, e := range scores {
		for _, rs := range e.Receipts {
			if !rs.SilentError {
				continue
			}
			if silent == 0 {
				w("| Engine | Receipt | Shop | Labelled | Parsed | Note |\n|---|---|---|---|---|---|\n")
			}
			silent++
			w("| `%s` | `%s` | %s | %s | %s | **silent** |\n", e.Engine, rs.ID, rs.Shop, formatCents(rs.TruthTotal), centsStr(rs.ParsedTotal))
		}
	}
	if silent == 0 {
		w("_None._\n")
	}
	w("\n")

	// Failure list for the default engine (or the first).
	focus := def
	if focus == "" && len(scores) > 0 {
		focus = scores[0].Engine
	}
	for _, e := range scores {
		if e.Engine != focus {
			continue
		}
		w("## Receipts with a wrong total (`%s`)\n\n", focus)
		w("Start parser work here: `shop-spike show <id> -engine %s` prints the rebuilt rows and the parse.\n\n", focus)
		w("| Receipt | Shop | Labelled | Parsed total | Σ lines | Reasons | Note |\n|---|---|---|---|---|---|---|\n")
		n := 0
		for _, rs := range e.Receipts {
			if rs.TotalCorrect {
				continue
			}
			n++
			parsed := "–"
			if rs.ParsedTotal != nil {
				parsed = formatCents(*rs.ParsedTotal)
			}
			note := ""
			switch {
			case rs.OCRError != "":
				note = "OCR error"
			case rs.SilentError:
				note = "**silent**"
			case rs.ParsedTotal == nil:
				note = "total not found"
			case rs.SelfConsistent:
				note = "lines add up to a wrong total (OCR misread?)"
			}
			w("| `%s` | %s | %s | %s | %s | %s | %s |\n", rs.ID, rs.Shop, formatCents(rs.TruthTotal), parsed,
				formatCents(rs.ParsedSum), strings.Join(rs.Reasons, ", "), note)
		}
		if n == 0 {
			w("_None._\n")
		}
		w("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// WriteResultsCSV writes one row per engine × receipt for spreadsheets.
func WriteResultsCSV(path string, scores []EngineScore) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cw := csv.NewWriter(f)
	_ = cw.Write([]string{"engine", "id", "shop", "truth_total", "parsed_total", "parsed_sum", "total_correct",
		"self_consistent", "date_correct", "items_correct", "auto_ok", "silent_error", "reasons",
		"line_tp", "line_fp", "line_fn", "engine_ms", "wall_ms", "ocr_error"})
	b := strconv.FormatBool
	for _, e := range scores {
		for _, r := range e.Receipts {
			parsed := ""
			if r.ParsedTotal != nil {
				parsed = formatCents(*r.ParsedTotal)
			}
			_ = cw.Write([]string{e.Engine, r.ID, r.Shop, formatCents(r.TruthTotal), parsed, formatCents(r.ParsedSum),
				b(r.TotalCorrect), b(r.SelfConsistent), b(r.DateCorrect), b(r.ItemsCorrect), b(r.AutoOK), b(r.SilentError),
				strings.Join(r.Reasons, " "), strconv.Itoa(r.LineTP), strconv.Itoa(r.LineFP), strconv.Itoa(r.LineFN),
				strconv.FormatInt(r.EngineMS, 10), strconv.FormatInt(r.WallMS, 10), r.OCRError})
		}
	}
	cw.Flush()
	return cw.Error()
}
