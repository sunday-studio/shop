package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"shop/internal/apitypes"
)

type fixLine struct {
	Raw   string `json:"raw"`
	Kind  string `json:"kind"`
	Cents int64  `json:"cents"`
}
type fixReceipt struct {
	File  string    `json:"file"`
	Shop  string    `json:"shop"`
	Date  string    `json:"date"`
	Lines []fixLine `json:"lines"`
}

func cents(c int64) string { return fmt.Sprintf("%d,%02d", c/100, c%100) }

func (e *env) getReceipt(id int64) apitypes.Receipt {
	e.t.Helper()
	return decodeAs[apitypes.Receipt](e.t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", id), nil))
}

func autoLinked(l apitypes.ReceiptLine) bool {
	return l.ProductId != nil && l.MatchedBy != nil && (*l.MatchedBy == apitypes.ReceiptLineMatchedByAlias || *l.MatchedBy == apitypes.ReceiptLineMatchedByScore)
}

// The 13 real receipts, one after the other, each scanned and then confirmed: repeats link at scan time,
// nothing is created at scan time, confirm reuses the links, and the six known wrong pairs never link.
func TestScanMatchingOnRealReceipts(t *testing.T) {
	b, err := os.ReadFile("../autoprod/testdata/receipts.json")
	if err != nil {
		t.Fatal(err)
	}
	var recs []fixReceipt
	if err := json.Unmarshal(b, &recs); err != nil {
		t.Fatal(err)
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Date < recs[j].Date })

	e := newEnv(t)
	shops := map[string]int64{}
	var scanLinked, confirmLinked, suggested, items int
	firstLinked := -1
	for n, r := range recs {
		if _, ok := shops[r.Shop]; !ok {
			shops[r.Shop] = e.shop(r.Shop).Id
		}
		d, _ := time.Parse(time.RFC3339, r.Date)
		rows := []string{d.Format("02-01-2006") + " 12:00|"}
		var sum int64
		for _, l := range r.Lines {
			if l.Kind != "item" {
				continue
			}
			rows = append(rows, l.Raw+"|"+cents(l.Cents))
			sum += l.Cents
		}
		rows = append(rows, "TOTAAL|"+cents(sum))

		before := count(t, e, `SELECT COUNT(*) FROM products`)
		rec := e.receipt(shops[r.Shop], uint8(100+n), rows...)
		if after := count(t, e, `SELECT COUNT(*) FROM products`); after != before {
			t.Fatalf("receipt %s: scanning created %d products", r.File, after-before)
		}
		got := map[int64]int64{} // line -> product at scan
		linkedHere := 0
		for _, l := range *rec.Lines {
			if l.Kind != apitypes.Item {
				continue
			}
			items++
			if autoLinked(l) {
				linkedHere++
				got[l.Id] = *l.ProductId
				if l.MatchScore == nil || *l.MatchScore < 0.92 {
					t.Errorf("%s: auto link below 0.92: %+v", r.File, l)
				}
			} else if l.ProductId != nil {
				t.Errorf("%s: a link at scan must be flagged auto: %+v", r.File, l)
			}
			if l.ProductId == nil && l.Suggestions != nil {
				for _, s := range *l.Suggestions {
					if s.Score < 0.75 || s.Score >= 0.92 {
						t.Errorf("%s: suggestion score %v outside [0.75, 0.92)", r.File, s.Score)
					}
				}
				if len(*l.Suggestions) > 3 {
					t.Errorf("%s: %d suggestions", r.File, len(*l.Suggestions))
				}
				if len(*l.Suggestions) > 0 {
					suggested++
				}
			}
		}
		if firstLinked < 0 && n == 0 && linkedHere != 0 {
			t.Errorf("first receipt: %d lines linked with an empty catalogue", linkedHere)
		}
		scanLinked += linkedHere

		cr := decodeAs[apitypes.Receipt](t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", rec.Id), nil))
		if cr.AutoProducts != nil {
			confirmLinked += cr.AutoProducts.Linked
		}
		// confirm must not relink or re-flag what the scan linked
		for _, l := range *cr.Lines {
			if want, ok := got[l.Id]; ok && (l.ProductId == nil || *l.ProductId != want || !autoLinked(l)) {
				t.Errorf("%s: confirm changed the scan link of line %d: %+v", r.File, l.Id, l)
			}
		}
	}
	t.Logf("%d item lines: %d linked at scan, %d more linked on confirm, %d lines with suggestions, %d products",
		items, scanLinked, confirmLinked, suggested, count(t, e, `SELECT COUNT(*) FROM products`))
	if scanLinked < 20 {
		t.Errorf("only %d lines linked at scan time", scanLinked)
	}

	// the six wrong pairs of docs/PRODUCT-AUTOPOPULATE.md section 2 never share a product
	pairs := [][2]string{
		{"AH BIO MELK", "AH BIO SAP"}, {"ARLA BIO HALFV. MELK", "ARLA MELK"}, {"AH BROCCOLI", "AH BIO SAP"},
		{"AH PEPERMIX", "AH HAVERMOUT"}, {"AH PLUOT", "AH SPRUITJES"}, {"KIP LOPPOOT", "KIP POTEN"},
	}
	productsOf := func(text string) map[int64]bool {
		rows, err := e.conn.Query(`SELECT DISTINCT product_id FROM receipt_lines WHERE kind = 'item' AND product_id IS NOT NULL
			AND upper(raw_text) LIKE ? AND upper(raw_text) NOT LIKE ?`, "%"+text+"%", "%"+text+" %X")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[int64]bool{}
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			out[id] = true
		}
		return out
	}
	for _, p := range pairs {
		a, bb := productsOf(p[0]), productsOf(p[1])
		for id := range a {
			if bb[id] && !strings.Contains(p[1], p[0]) && !strings.Contains(p[0], p[1]) {
				t.Errorf("%q and %q ended on the same product %d", p[0], p[1], id)
			}
		}
	}
}

func TestScanMatchingKnownWrongPairsAreOnlyEverSuggested(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("AH")
	pairs := [][2]string{ // existing product, receipt text
		{"AH BIO SAP", "AH BIO MELK"}, {"ARLA MELK", "ARLA BIO HALFV. MELK"}, {"AH BIO SAP", "AH BROCCOLI"},
		{"AH HAVERMOUT", "AH PEPERMIX"}, {"AH SPRUITJES", "AH PLUOT"}, {"KIP POTEN", "KIP LOPPOOT"},
	}
	var rows = []string{"01-10-2026 10:00|"}
	var total int64
	for _, p := range pairs {
		e.product(map[string]any{"name": p[0]})
		rows = append(rows, p[1]+"|1,00")
		total += 100
	}
	rows = append(rows, "TOTAAL|"+cents(total))
	r := e.receipt(ah.Id, 1, rows...)
	for _, l := range *r.Lines {
		if l.Kind != apitypes.Item {
			continue
		}
		if l.ProductId != nil {
			t.Errorf("%q was linked (%v %v): never auto-link a known wrong pair", l.RawText, l.MatchedBy, l.MatchScore)
		}
	}
}

func TestSuggestionsAreStoredAndAcceptedWithTheLinkEndpoint(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("AH")
	sap := e.product(map[string]any{"name": "AH Bio Sap"})
	r := e.receipt(ah.Id, 1, "01-10-2026 10:00|", "AH BIO MELK|1,39", "TOTAAL|1,39")
	l := (*r.Lines)[0]
	if l.ProductId != nil || l.MatchedBy != nil || l.Suggestions == nil || len(*l.Suggestions) == 0 {
		t.Fatalf("want an unlinked line with suggestions, got %+v", l)
	}
	sg := *l.Suggestions
	if sg[0].Score < 0.75 || sg[0].Score >= 0.92 || sg[0].Name == "" {
		t.Fatalf("suggestion = %+v", sg[0])
	}
	if sg[0].ProductId != sap.Id {
		t.Fatalf("suggested product %d, want %d", sg[0].ProductId, sap.Id)
	}
	// stored: a product renamed since is shown with its current name
	if rec := e.do("PATCH", fmt.Sprintf("/v1/products/%d", sg[0].ProductId), map[string]any{"name": "Renamed " + sg[0].Name}); rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	r = e.getReceipt(r.Id)
	if got := (*(*r.Lines)[0].Suggestions)[0].Name; !strings.HasPrefix(got, "Renamed") {
		t.Errorf("suggestion name = %q, want the current product name", got)
	}
	// one tap = the link endpoint
	res := decodeAs[apitypes.LinkResult](t, e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", l.Id), map[string]any{"product_id": sg[0].ProductId}))
	if res.Line.ProductId == nil || res.Line.MatchedBy == nil || *res.Line.MatchedBy != apitypes.ReceiptLineMatchedByUser || res.Line.MatchScore != nil {
		t.Fatalf("accepted line = %+v", res.Line)
	}
	r = e.getReceipt(r.Id)
	if x := (*r.Lines)[0]; x.Suggestions != nil && len(*x.Suggestions) != 0 {
		t.Errorf("suggestions after linking: %+v", x.Suggestions)
	}
}

func TestAutoLinkIsUndoableAndAUserLinkSurvivesReparse(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("AH")
	milk := e.product(map[string]any{"name": "AH Halfvolle Melk"})
	other := e.product(map[string]any{"name": "Volle yoghurt"})

	r := e.receipt(ah.Id, 1, "01-10-2026 10:00|", "AH HALFVOLLE MELK|1,29", "KAAS|4,99", "TOTAAL|6,28")
	l := (*r.Lines)[0]
	if !autoLinked(l) || *l.ProductId != milk.Id {
		t.Fatalf("exact name should link at scan: %+v", l)
	}
	// undo = unlink; the scan does not link it again until a reparse
	res := decodeAs[apitypes.LinkResult](t, e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", l.Id), map[string]any{"product_id": nil}))
	if res.Line.ProductId != nil || res.Line.MatchedBy != nil {
		t.Fatalf("undo: %+v", res.Line)
	}
	// the user links the second line themselves
	cheese := (*r.Lines)[1]
	decodeAs[apitypes.LinkResult](t, e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", cheese.Id),
		map[string]any{"product_id": other.Id, "remember": false}))

	if rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/reparse", r.Id), nil); rec.Code != 202 {
		t.Fatalf("reparse: %d %s", rec.Code, rec.Body)
	}
	e.drain()
	r = e.getReceipt(r.Id)
	a, b := (*r.Lines)[0], (*r.Lines)[1]
	if !autoLinked(a) || *a.ProductId != milk.Id {
		t.Errorf("reparse should link the milk again: %+v", a)
	}
	if b.ProductId == nil || *b.ProductId != other.Id || b.MatchedBy == nil || *b.MatchedBy != apitypes.ReceiptLineMatchedByUser {
		t.Errorf("reparse lost the user's link: %+v", b)
	}
}

func TestEditedQuantityIsKeptOnReparse(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("AH")
	r := e.receipt(ah.Id, 1, "01-10-2026 10:00|", "2 COCA-COLA 2,49|4,98", "TOTAAL|4,98")
	l := (*r.Lines)[0]
	if l.Qty != 2 || l.UnitPriceCents == nil || *l.UnitPriceCents != 249 {
		t.Fatalf("parsed line = %+v", l)
	}
	rec := e.do("PATCH", fmt.Sprintf("/v1/lines/%d", l.Id), map[string]any{
		"raw_text": l.RawText, "kind": "item", "qty": 3, "unit_price_cents": 166, "line_total_cents": 498})
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/reparse", r.Id), nil); rec.Code != 409 {
		t.Fatalf("reparse of an edited receipt: %d, want 409", rec.Code)
	}
	if ids, err := e.q.ListReceiptIDsForReparse(context.Background(), nil); err != nil || len(ids) != 0 {
		t.Fatalf("bulk reparse would redo the edited receipt: %v %v", ids, err)
	}
	if got := (*e.getReceipt(r.Id).Lines)[0]; got.Qty != 3 || !got.UserEdited {
		t.Errorf("edited quantity lost: %+v", got)
	}
}

func TestScanMatchingFortyLinesIsFast(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("AH")
	rows := []string{"01-10-2026 10:00|"}
	var total int64
	for i := 0; i < 300; i++ { // a catalogue of 300 products
		e.product(map[string]any{"name": fmt.Sprintf("Product %s %d", []string{"melk", "kaas", "brood", "sap", "rijst", "pasta"}[i%6], i)})
	}
	for i := 0; i < 40; i++ {
		rows = append(rows, fmt.Sprintf("%s %d|1,00", []string{"AH MELK", "BIO KAAS", "VOLKOREN BROOD", "ZAK RIJST"}[i%4], i))
		total += 100
	}
	rows = append(rows, "TOTAAL|"+cents(total))
	start := time.Now()
	r := e.receipt(ah.Id, 1, rows...)
	took := time.Since(start)
	if n := len(*r.Lines); n != 40 {
		t.Fatalf("%d lines", n)
	}
	t.Logf("upload, OCR, parse and matching of 40 lines against 300 products: %v", took)
	if took > 5*time.Second {
		t.Errorf("too slow: %v", took)
	}
}
