package receipt

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"shop/internal/ocr"
)

// goldenFiles are real (sanitised) receipts that reconcile; qty_golden.json holds the expected
// kind|qty|unit|unit price|line total|raw text of every line, reviewed by hand against the photos'
// printed arithmetic. UPDATE_GOLDEN=1 rewrites it (review the diff).
var goldenFiles = []string{
	"live/receipt_1.json", "live/receipt_2.json", "live/receipt_4.json", "live/receipt_5.json", "live/receipt_6.json",
	"live/receipt_7.json", "live/receipt_8.json", "live/receipt_10.json", "live/receipt_12.json", "live/receipt_13.json",
	"live/receipt_14.json", "live/receipt_16.json", "live/receipt_17.json", "live/receipt_19.json", "live/receipt_20.json",
	"live/receipt_21.json", "live/receipt_23.json", "real_1466.json", "real_1470.json", "ah_subtotal_1469.json", "ah_subtotal_1471.json",
}

func goldenLines(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var res ocr.Result
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	r := Parse(res, Options{Location: ams, Now: now})
	var out []string
	for _, l := range r.Lines {
		up := int64(-1)
		if l.UnitPriceCents != nil {
			up = *l.UnitPriceCents
		}
		out = append(out, fmt.Sprintf("%s|%v|%s|%d|%d|%s", l.Kind, l.Qty, l.Unit, up, l.LineTotalCents, l.RawText))
	}
	return out
}

func TestQuantityGolden(t *testing.T) {
	golden := map[string][]string{}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		for _, f := range goldenFiles {
			golden[f] = goldenLines(t, f)
		}
		b, _ := json.MarshalIndent(golden, "", " ")
		if err := os.WriteFile("testdata/qty_golden.json", b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile("testdata/qty_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &golden); err != nil {
		t.Fatal(err)
	}
	for _, f := range goldenFiles {
		got, want := goldenLines(t, f), golden[f]
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s:\n got  %s\n want %s", f, strings.Join(got, "\n      "), strings.Join(want, "\n      "))
		}
	}
}

// The invariant behind the golden file: on every reconciling real receipt each item with a quantity
// has qty x unit price == line total (pieces exactly, weights within a cent).
func TestQuantityTimesPriceIsTotal(t *testing.T) {
	for _, f := range goldenFiles {
		b, _ := os.ReadFile("testdata/" + f)
		var res ocr.Result
		_ = json.Unmarshal(b, &res)
		for _, l := range Parse(res, Options{Location: ams, Now: now}).Lines {
			if l.Kind != KindItem && l.Kind != KindDeposit || l.UnitPriceCents == nil {
				continue
			}
			tol := int64(0)
			if l.Unit != "pc" {
				tol = 1
			}
			if d := abs64(mulCents(l.Qty, *l.UnitPriceCents) - l.LineTotalCents); d > tol {
				t.Errorf("%s: %q qty %v x %d != %d", f, l.RawText, l.Qty, *l.UnitPriceCents, l.LineTotalCents)
			}
		}
	}
}

func TestCountColumnPatterns(t *testing.T) {
	cases := []struct {
		name string
		row  string
		want want
	}{
		{"count and unit price", "2 COCA-COLA 2,49|4,98", want{"2 COCA-COLA 2,49", KindItem, 2, "pc", 249, 498}},
		{"count 1", "1 SPELTWAFELS|1,79", want{"1 SPELTWAFELS", KindItem, 1, "pc", 179, 179}},
		{"count column read as 21", "21 AH BIO MELK 1,39|2,78", want{"21 AH BIO MELK 1,39", KindItem, 2, "pc", 139, 278}},
		{"two digit count glued to a speck", "2T SCHARRELEI 4,19|8,38", want{"2T SCHARRELEI 4,19", KindItem, 2, "pc", 419, 838}},
		{"count 15 is really 5", "15 RED BULL 1,75|8,75", want{"15 RED BULL 1,75", KindItem, 5, "pc", 175, 875}},
		{"count lost, price printed", ". KIWI SUNGOLD 6,59|13,18", want{". KIWI SUNGOLD 6,59", KindItem, 2, "pc", 659, 1318}},
		{"count noise letter, price printed", "J KOH THAI 2,29|9,16", want{"J KOH THAI 2,29", KindItem, 4, "pc", 229, 916}},
		{"count is 10", "10 JW SARDINES 2,29|22,90", want{"10 JW SARDINES 2,29", KindItem, 10, "pc", 229, 2290}},
		{"count above one, amount equals price: one piece", "2 DILLE 2,29|2,29", want{"2 DILLE 2,29", KindItem, 1, "pc", 229, 229}},
		{"count without printed price", "2 DILLE|1,70", want{"2 DILLE", KindItem, 2, "pc", 85, 170}},
		{"count that does not divide the amount", "3 NALYS KP|11,29", want{"3 NALYS KP", KindItem, 1, "pc", 1129, 1129}},
		{"7 is a misread 1, never a count without a price", "7 LIPTON PEACH|3,50", want{"7 LIPTON PEACH", KindItem, 1, "pc", 350, 350}},
		{"letter noise is not a count", "T CAMP YOGHURT|3,69", want{"T CAMP YOGHURT", KindItem, 1, "pc", 369, 369}},
		{"article number is not a count", "1234 PASTA|2,19", want{"1234 PASTA", KindItem, 1, "pc", 219, 219}},
		{"volume in the name is not a count", "HALFVOLLE MELK 1 5L|1,75", want{"HALFVOLLE MELK 1 5L", KindItem, 1, "pc", 175, 175}},
		{"pack in the name", "6X330ML BIER|4,99", want{"6X330ML BIER", KindItem, 1, "pc", 499, 499}},
		{"pack with a decimal volume is not six pieces", "BIER 6 x 0,33 L|4,99", want{"BIER 6 x 0,33 L", KindItem, 1, "pc", 499, 499}},
		{"bottle size before the price", "SUNFLOWER 0.92L|1,59", want{"SUNFLOWER 0.92L", KindItem, 1, "pc", 159, 159}},
		{"deposit with count", "6 Statiegeld 0,25|1,50", want{"6 Statiegeld 0,25", KindDeposit, 6, "pc", 25, 150}},
		{"deposit one", "1 Statiegeld 0,25|0,25", want{"1 Statiegeld 0,25", KindDeposit, 1, "pc", 25, 25}},
		{"inline multiplication", "MELK 2 x 0,89|1,78", want{"MELK", KindItem, 2, "pc", 89, 178}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := parse("01-10-2026|", c.row)
			if len(r.Lines) != 1 {
				t.Fatalf("lines = %+v", r.Lines)
			}
			checkLines(t, r.Lines, []want{c.want})
		})
	}
}

func TestPerUnitStyleRows(t *testing.T) {
	// "3 x 1,25 €/St" below the name, amount at the right of the qty row.
	r := parse("01-10-2026|", "RODE UIEN 1KG|", "3 x 1,25 €/St|3,75", "TOTAAL|3,75")
	checkLines(t, r.Lines, []want{{"RODE UIEN 1KG", KindItem, 3, "pc", 125, 375}})

	// Weighed item: per-kg price in the text, the weight on its own row.
	r = parse("01-10-2026|", "GEMBER LOS 5,98|2,45", "0.410 KG|", "TOTAAL|2,45")
	checkLines(t, r.Lines, []want{{"GEMBER LOS 5,98", KindItem, 0.41, "kg", 598, 245}})

	// A weight that does not fit the amount is not attached.
	r = parse("01-10-2026|", "GEMBER LOS 5,98|2,45", "0.900 KG|", "TOTAAL|2,45")
	checkLines(t, r.Lines, []want{{"GEMBER LOS 5,98", KindItem, 1, "pc", 245, 245}})

	// Weight and per-kg price on one row, litres likewise.
	r = parse("01-10-2026|", "ZOETE AARDAPPEL|", "3,860 kg x 3,49 €/kg|13,47", "TOTAAL|13,47")
	checkLines(t, r.Lines, []want{{"ZOETE AARDAPPEL", KindItem, 3.86, "kg", 349, 1347}})
	r = parse("01-10-2026|", "DIESEL|", "1,25 l x 1,89 €/l|2,36", "TOTAAL|2,36")
	checkLines(t, r.Lines, []want{{"DIESEL", KindItem, 1.25, "l", 189, 236}})
}

func TestRequantifyText(t *testing.T) {
	q, up, ok := RequantifyText("2 COCA-COLA 2,49", 498)
	if !ok || q != 2 || up != 249 {
		t.Errorf("got %v %d %v", q, up, ok)
	}
	if _, _, ok := RequantifyText("1 SPELTWAFELS", 179); ok {
		t.Error("single piece must stay")
	}
}
