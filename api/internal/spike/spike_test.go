package spike

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/ocr"
	"shop/internal/receipt"
)

func TestTruthRoundTrip(t *testing.T) {
	d := Dir{Root: t.TempDir()}
	in := map[string]Truth{
		"ah/001":   {ID: "ah/001", Shop: "AH", Date: "2026-09-01", TotalCents: 451, Items: 3, Notes: "faded, torn"},
		"lidl/002": {ID: "lidl/002", Shop: "Lidl", Date: "2026-09-02", TotalCents: -50, Items: 0},
	}
	if err := d.SaveTruth(in); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(d.TruthCSV())
	if !strings.Contains(string(b), `ah/001,AH,2026-09-01,"4,51",3,"faded, torn"`) {
		t.Errorf("csv:\n%s", b)
	}
	out, err := d.LoadTruth()
	if err != nil || len(out) != 2 || out["ah/001"] != in["ah/001"] || out["lidl/002"].TotalCents != -50 {
		t.Fatalf("round trip: %+v %v", out, err)
	}

	lines := []TruthLine{{Text: "MELK", Kind: "item", Qty: 1, Unit: "pc", UnitPriceCents: 129, TotalCents: 129}}
	if err := d.SaveLines("ah/001", lines); err != nil {
		t.Fatal(err)
	}
	got, has, err := d.LoadLines("ah/001")
	if err != nil || !has || got[0] != lines[0] {
		t.Fatalf("lines: %+v %v %v", got, has, err)
	}
	_ = d.SaveLines("ah/001", nil)
	if _, has, _ := d.LoadLines("ah/001"); has {
		t.Error("nil lines should delete the label")
	}
}

func TestLoadTruthRejectsBadRows(t *testing.T) {
	d := Dir{Root: t.TempDir()}
	for _, row := range []string{
		"x,AH,2026-09-01,4.5,1,",      // one decimal
		"x,AH,01-09-2026,4,51,1,",     // wrong date format
		"x,AH,2026-09-01,\"4,51\",a,", // n_items not a number
	} {
		_ = os.WriteFile(d.TruthCSV(), []byte("id,shop,date,total,n_items,notes\n"+row+"\n"), 0o644)
		if _, err := d.LoadTruth(); err == nil {
			t.Errorf("accepted %q", row)
		}
	}
}

func TestMatchLines(t *testing.T) {
	parsed := []receipt.Line{
		{RawText: "AH HALFV MELK", Kind: receipt.KindItem, LineTotalCents: 129},
		{RawText: "KIPFILT", Kind: receipt.KindItem, LineTotalCents: 549}, // OCR typo, still a match
		{RawText: "BONUS KIP", Kind: receipt.KindDiscount, LineTotalCents: -50},
		{RawText: "PINDAKAAS", Kind: receipt.KindItem, LineTotalCents: 999}, // wrong amount
	}
	truth := []TruthLine{
		{Text: "AH HALFV MELK", Kind: "item", TotalCents: 129},
		{Text: "KIPFILET", Kind: "item", TotalCents: 549},
		{Text: "BONUS KIP", Kind: "discount", TotalCents: -50},
		{Text: "PINDAKAAS", Kind: "item", TotalCents: 239},
		{Text: "STATIEGELD", Kind: "deposit", TotalCents: 15},
	}
	tp, fp, fn := matchLines(parsed, truth)
	if tp != 3 || fp != 1 || fn != 2 {
		t.Errorf("tp/fp/fn = %d/%d/%d, want 3/1/2", tp, fp, fn)
	}
}

func TestScoreAndRecommend(t *testing.T) {
	d := Dir{Root: t.TempDir()}
	loc := time.UTC
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, loc)
	truth := map[string]Truth{
		"r1": {ID: "r1", Shop: "AH", Date: "2026-10-01", TotalCents: 129, Items: 1},
		"r2": {ID: "r2", Shop: "AH", Date: "2026-10-02", TotalCents: 200, Items: 1},
	}
	res := func(rows ...[2]string) ocr.Result {
		var r ocr.Result
		for i, row := range rows {
			y := float64(i * 40)
			r.Lines = append(r.Lines, ocr.Line{Text: row[0], Conf: 0.99, Box: [4]float64{0, y, 100, y + 20}})
			if row[1] != "" {
				r.Lines = append(r.Lines, ocr.Line{Text: row[1], Conf: 0.99, Box: [4]float64{200, y, 260, y + 20}})
			}
		}
		r.MS = 1000
		return r
	}
	// "good" reads both right; "sloppy" misreads r2's total but its lines
	// agree with the misread, so validation can't catch it (a silent error).
	_ = d.SaveCached("good", "r1", Cached{Result: res([2]string{"01-10-2026", ""}, [2]string{"MELK", "1,29"}, [2]string{"TOTAAL", "1,29"})})
	_ = d.SaveCached("good", "r2", Cached{Result: res([2]string{"02-10-2026", ""}, [2]string{"KAAS", "2,00"}, [2]string{"TOTAAL", "2,00"})})
	_ = d.SaveCached("sloppy", "r1", Cached{Result: res([2]string{"01-10-2026", ""}, [2]string{"MELK", "1,29"}, [2]string{"TOTAAL", "1,29"})})
	_ = d.SaveCached("sloppy", "r2", Cached{Result: res([2]string{"02-10-2026", ""}, [2]string{"KAAS", "8,00"}, [2]string{"TOTAAL", "8,00"})})
	_ = d.SaveMeta("good", EngineMeta{PeakMemMB: 900})
	_ = d.SaveMeta("sloppy", EngineMeta{PeakMemMB: 500})

	good, err := ScoreEngine(d, "good", truth, loc, now)
	if err != nil || good.N != 2 || good.TotalCorrect != 2 || good.AutoOK != 2 || good.DateCorrect != 2 || good.SilentErrors != 0 {
		t.Fatalf("good: %+v %v", good, err)
	}
	sloppy, _ := ScoreEngine(d, "sloppy", truth, loc, now)
	if sloppy.TotalCorrect != 1 || sloppy.SilentErrors != 1 {
		t.Fatalf("sloppy: correct=%d silent=%d", sloppy.TotalCorrect, sloppy.SilentErrors)
	}

	def, fb, _ := Recommend([]EngineScore{sloppy, good}, map[string]bool{"good": true, "sloppy": true})
	if def != "good" || fb != "" {
		t.Errorf("recommend = %q / %q, want good / none", def, fb)
	}

	out := filepath.Join(d.Root, "report.md")
	if err := WriteReport(out, []EngineScore{good, sloppy}, []Engine{{Name: "good", URL: "x"}, {Name: "sloppy", URL: "y"}}, 2, 0, 2, now); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	for _, want := range []string{"**Default engine:** `good`", "| `sloppy` | 2 | 50% ✗", "**silent**"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestParseMem(t *testing.T) {
	for in, want := range map[string]float64{"512MiB / 3GiB": 512, "1.5GiB / 10GiB": 1536, "800KiB / 1GiB": 0.78125} {
		if got, ok := parseMem(in); !ok || got != want {
			t.Errorf("parseMem(%q) = %v, want %v", in, got, want)
		}
	}
}
