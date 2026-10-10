package receipt

import (
	"slices"
	"strings"
	"testing"
	"time"

	"shop/internal/ocr"
)

var (
	ams = mustLoc("Europe/Amsterdam")
	now = time.Date(2026, 10, 10, 18, 0, 0, 0, ams)
)

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// layout builds OCR lines the way PP-OCR returns them: the description and
// the price column as separate boxes on the same baseline (with a little
// vertical jitter). Each row is "left|right"; right may be empty.
func layout(rows ...string) ocr.Result {
	var lines []ocr.Line
	for i, r := range rows {
		y := float64(40 + i*46)
		left, right, _ := strings.Cut(r, "|")
		if left != "" {
			lines = append(lines, ocr.Line{Text: left, Conf: 0.98, Box: [4]float64{30, y, 300, y + 30}})
		}
		if right != "" {
			j := float64(i%3) - 1 // ±1px jitter
			lines = append(lines, ocr.Line{Text: right, Conf: 0.97, Box: [4]float64{380, y + j, 460, y + 32 + j}})
		}
	}
	return ocr.Result{Engine: "test", Lines: lines}
}

func parse(rows ...string) Result {
	return Parse(layout(rows...), Options{Location: ams, Now: now})
}

type want struct {
	text  string
	kind  Kind
	qty   float64
	unit  string
	price int64
	total int64
}

func checkLines(t *testing.T, got []Line, w []want) {
	t.Helper()
	if len(got) != len(w) {
		for _, l := range got {
			t.Logf("  %-20q %-8s qty=%v %s total=%d", l.RawText, l.Kind, l.Qty, l.Unit, l.LineTotalCents)
		}
		t.Fatalf("got %d lines, want %d", len(got), len(w))
	}
	for i, l := range got {
		e := w[i]
		price := int64(0)
		if l.UnitPriceCents != nil {
			price = *l.UnitPriceCents
		}
		if l.RawText != e.text || l.Kind != e.kind || l.Qty != e.qty || l.Unit != e.unit ||
			price != e.price || l.LineTotalCents != e.total {
			t.Errorf("line %d = {%q %s qty=%v %s price=%d total=%d}, want %+v",
				i, l.RawText, l.Kind, l.Qty, l.Unit, price, l.LineTotalCents, e)
		}
	}
}

func TestParseFullReceipt(t *testing.T) {
	r := parse(
		"ALBERT HEIJN|",
		"Kassabon 10-10-2026 14:32|",
		"AH HALFV MELK 1,5L|1,29 B",
		"BANANEN|1,78 B",
		"2 x 0,89|",
		"KIPFILET|1,79 B",
		"0,512 kg x 3,49 EUR/kg|",
		"BONUS KIPFILET|-0,50 B",
		"STATIEGELD|0,15",
		"DRAAGTAS|0,25",
		"TOTAAL|4,76",
		"PIN|4,76",
		"BTW 9% over 3,70|0,33",
	)
	checkLines(t, r.Lines, []want{
		{"AH HALFV MELK 1,5L", KindItem, 1, "pc", 129, 129},
		{"BANANEN", KindItem, 2, "pc", 89, 178},
		{"KIPFILET", KindItem, 0.512, "kg", 349, 179},
		{"BONUS KIPFILET", KindDiscount, 1, "pc", 0, -50},
		{"STATIEGELD", KindDeposit, 1, "pc", 15, 15},
		{"DRAAGTAS", KindBag, 1, "pc", 25, 25},
	})
	if r.Lines[3].Parent != 2 {
		t.Errorf("discount parent = %d, want 2", r.Lines[3].Parent)
	}
	if r.TotalCents == nil || *r.TotalCents != 476 || r.SumCents != 476 {
		t.Errorf("total=%v sum=%d", r.TotalCents, r.SumCents)
	}
	if len(r.Reasons) != 0 {
		t.Errorf("reasons = %v, want none", r.Reasons)
	}
	if r.PaymentMethod != "pin" {
		t.Errorf("payment = %q", r.PaymentMethod)
	}
	wantDate := time.Date(2026, 10, 10, 14, 32, 0, 0, ams)
	if r.PurchasedAt == nil || !r.PurchasedAt.Equal(wantDate) {
		t.Errorf("date = %v, want %v", r.PurchasedAt, wantDate)
	}
	if r.Lines[0].VATCode != "B" {
		t.Errorf("vat = %q", r.Lines[0].VATCode)
	}
}

func TestQtyRowResolvedByArithmetic(t *testing.T) {
	// The "2 x 0,89" row sits between two items; only the second matches.
	r := parse("01-10-2026|", "MELK|1,29", "2 x 0,89|", "BANANEN|1,78", "TOTAAL|3,07")
	checkLines(t, r.Lines, []want{
		{"MELK", KindItem, 1, "pc", 129, 129},
		{"BANANEN", KindItem, 2, "pc", 89, 178},
	})
	if len(r.Reasons) != 0 {
		t.Errorf("reasons = %v", r.Reasons)
	}
}

func TestDescriptionThenWeightWithTotal(t *testing.T) {
	r := parse("01-10-2026|", "BANANEN|", "0,512kg x 3,49|1,79", "TOTAAL|1,79")
	checkLines(t, r.Lines, []want{{"BANANEN", KindItem, 0.512, "kg", 349, 179}})
	if len(r.Reasons) != 0 {
		t.Errorf("reasons = %v", r.Reasons)
	}
}

func TestInlineQty(t *testing.T) {
	r := parse("01-10-2026|", "MELK 2 x 0,89|1,78", "TOTAAL|1,78")
	checkLines(t, r.Lines, []want{{"MELK", KindItem, 2, "pc", 89, 178}})
}

func TestDetailRowRepeatingTotal(t *testing.T) {
	r := parse("01-10-2026|", "MELK|1,78", "2 x 0,89|1,78", "TOTAAL|1,78")
	checkLines(t, r.Lines, []want{{"MELK", KindItem, 2, "pc", 89, 178}})
	if len(r.Reasons) != 0 {
		t.Errorf("reasons = %v", r.Reasons)
	}
}

func TestBonusBlockAfterSubtotal(t *testing.T) {
	r := parse(
		"01-10-2026|",
		"KIPFILET|4,00",
		"HAVERMOUT|1,00",
		"SUBTOTAAL|5,00",
		"BONUS KIPFILET|0,50-",
		"BONUS BOX|-0,25",
		"TOTAAL|4,25",
	)
	checkLines(t, r.Lines, []want{
		{"KIPFILET", KindItem, 1, "pc", 400, 400},
		{"HAVERMOUT", KindItem, 1, "pc", 100, 100},
		{"BONUS KIPFILET", KindDiscount, 1, "pc", 0, -50},
		{"BONUS BOX", KindDiscount, 1, "pc", 0, -25},
	})
	if r.Lines[2].Parent != 0 || r.Lines[3].Parent != -1 {
		t.Errorf("parents = %d, %d; want 0, -1", r.Lines[2].Parent, r.Lines[3].Parent)
	}
	if len(r.Reasons) != 0 {
		t.Errorf("reasons = %v", r.Reasons)
	}
}

func TestGluedVATCodeAndSingleBox(t *testing.T) {
	// Some engines return the whole row as one box.
	res := ocr.Result{Lines: []ocr.Line{
		{Text: "05-10-2026 09:12", Conf: 0.99, Box: [4]float64{10, 10, 200, 30}},
		{Text: "YOGHURT 0% 1,29B", Conf: 0.99, Box: [4]float64{10, 50, 400, 80}},
		{Text: "Te betalen € 1,29", Conf: 0.99, Box: [4]float64{10, 90, 400, 120}},
	}}
	r := Parse(res, Options{Location: ams, Now: now})
	checkLines(t, r.Lines, []want{{"YOGHURT 0%", KindItem, 1, "pc", 129, 129}})
	if r.Lines[0].VATCode != "B" || len(r.Reasons) != 0 {
		t.Errorf("vat=%q reasons=%v", r.Lines[0].VATCode, r.Reasons)
	}
}

func TestReasons(t *testing.T) {
	cases := []struct {
		name string
		rows []string
		want []string
	}{
		{"no total", []string{"01-10-2026|", "MELK|1,29"}, []string{ReasonNoTotal}},
		{"mismatch", []string{"01-10-2026|", "MELK|1,29", "TOTAAL|2,29"}, []string{ReasonTotalMismatch}},
		{"no date", []string{"MELK|1,29", "TOTAAL|1,29"}, []string{ReasonNoDate}},
		{"line math", []string{"01-10-2026|", "MELK 2 x 0,89|1,99", "TOTAAL|1,99"}, []string{ReasonLineMath}},
		{"old date rejected", []string{"01-10-2019|", "MELK|1,29", "TOTAAL|1,29"}, []string{ReasonNoDate}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := parse(c.rows...)
			if !slices.Equal(r.Reasons, c.want) {
				t.Errorf("reasons = %v, want %v", r.Reasons, c.want)
			}
		})
	}
}

func TestLowConfidence(t *testing.T) {
	res := layout("01-10-2026|", "MELK|1,29", "TOTAAL|1,29")
	for i := range res.Lines {
		res.Lines[i].Conf = 0.5
	}
	r := Parse(res, Options{Location: ams, Now: now})
	if !slices.Contains(r.Reasons, ReasonLowConf) {
		t.Errorf("reasons = %v", r.Reasons)
	}
}

func TestParseAmount(t *testing.T) {
	cases := map[string]int64{
		"1,29": 129, "1.29": 129, "-0,50": -50, "0,50-": -50, "1.234,56": 123456,
		"1234.56": 123456, "€1,29": 129, "EUR 1,29": 129, "12,00": 1200,
	}
	for in, want := range cases {
		if got, ok := ParseAmount(in); !ok || got != want {
			t.Errorf("ParseAmount(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"0,512", "12", "1,2", "abc", "10-10-2026", "2x"} {
		if _, ok := ParseAmount(in); ok {
			t.Errorf("ParseAmount(%q) should fail", in)
		}
	}
}

func TestFindDate(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"Datum: 03-10-2026 18:05", time.Date(2026, 10, 3, 18, 5, 0, 0, ams)},
		{"03/10/26", time.Date(2026, 10, 3, 0, 0, 0, 0, ams)},
		{"3.10.2026 9:07:11", time.Date(2026, 10, 3, 9, 7, 11, 0, ams)},
		{"07-10-202618:44", time.Date(2026, 10, 7, 18, 44, 0, 0, ams)},
		{"Kassa 3 21-09-2026 Bon 1234", time.Date(2026, 9, 21, 0, 0, 0, 0, ams)},
	}
	for _, c := range cases {
		got, ok := FindDate([]string{c.in}, ams, now)
		if !ok || !got.Equal(c.want) {
			t.Errorf("FindDate(%q) = %v, %v; want %v", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []string{"31-02-2026", "01-01-2030", "no date"} {
		if got, ok := FindDate([]string{bad}, ams, now); ok {
			t.Errorf("FindDate(%q) = %v; want none", bad, got)
		}
	}
}

// OCR noise seen in the spike: punctuation after the VAT code or price,
// and a semicolon read instead of the decimal comma.
func TestOCRNoiseTolerance(t *testing.T) {
	r := parse(
		"21-09-2026 17:08|",
		"KOFFIEBONEN|17,98 B.",
		"2 x 8,99|",
		"BANANEN|",
		"1;338 kg x 3,49 EUR/kg|.4,67 B",
		"PASTA|1,19.",
		"APPELS|2,79.B",
		"BONUS APPELS|-0,50.B",
		"TOTAAL|26,13",
	)
	checkLines(t, r.Lines, []want{
		{"KOFFIEBONEN", KindItem, 2, "pc", 899, 1798},
		{"BANANEN", KindItem, 1.338, "kg", 349, 467},
		{"PASTA", KindItem, 1, "pc", 119, 119},
		{"APPELS", KindItem, 1, "pc", 279, 279},
		{"BONUS APPELS", KindDiscount, 1, "pc", 0, -50},
	})
	if len(r.Reasons) != 0 {
		t.Errorf("reasons = %v", r.Reasons)
	}
}

// Some chains print the item count before the label ("22 SUBTOTAAL"); it must not become an item.
func TestSubtotalWithLeadingCount(t *testing.T) {
	r := parse("01-10-2026|", "MELK|1,29", "BROOD|2,00", "2 SUBTOTAAL|3,29", "2 TOTAAL|3,29")
	checkLines(t, r.Lines, []want{
		{"MELK", KindItem, 1, "pc", 129, 129},
		{"BROOD", KindItem, 1, "pc", 200, 200},
	})
	if len(r.Reasons) != 0 {
		t.Errorf("reasons = %v; want none", r.Reasons)
	}
}
