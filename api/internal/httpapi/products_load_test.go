package httpapi_test

import (
	"fmt"
	"testing"

	"shop/internal/apitypes"
)

type loadAnswer struct {
	ReceiptID   int64 `json:"receipt_id"`
	DryRun      bool  `json:"dry_run"`
	Confirmed   bool  `json:"confirmed"`
	Linked      int   `json:"linked"`
	Created     int   `json:"created"`
	NeedsReview int   `json:"needs_review"`
	Skipped     int   `json:"skipped"`
	Lines       []struct {
		LineID      int64  `json:"line_id"`
		RawText     string `json:"raw_text"`
		Action      string `json:"action"`
		ProductID   int64  `json:"product_id"`
		ProductName string `json:"product_name"`
		NeedsReview bool   `json:"needs_review"`
		SkipReason  string `json:"skip_reason"`
	} `json:"lines"`
}

// Load products from one receipt on request: before it is confirmed, after it was confirmed with the
// feature off, twice, and with the answer per line.
func TestLoadProductsFromAReceiptOverHTTP(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.auto.SetEnabled(false)

	// a receipt that is read but not confirmed
	e.ocr.rows.Store([]string{"01-10-2026 10:00|", "AH BIO MELK|1,39", "FANTA|3,25", "TOTAAL|4,64"})
	up := decodeAs[apitypes.Receipt](t, e.upload(shop.Id, pngBytes(71), nil))
	e.drain()
	path := fmt.Sprintf("/v1/receipts/%d/load-products", up.Id)

	if rec := e.do("POST", path, nil); rec.Code != 409 {
		t.Errorf("with the feature off: %d, want 409", rec.Code)
	}
	e.auto.SetEnabled(true)
	if rec := e.do("POST", "/v1/receipts/99999/load-products", nil); rec.Code != 404 {
		t.Errorf("unknown receipt: %d, want 404", rec.Code)
	}

	dry := decodeAs[loadAnswer](t, e.do("POST", path, map[string]any{"dry_run": true}))
	if !dry.DryRun || dry.Created != 2 || len(dry.Lines) != 2 || count(t, e, `SELECT COUNT(*) FROM products`) != 0 {
		t.Fatalf("dry run = %+v", dry)
	}
	rec := e.do("POST", path, nil)
	if rec.Code != 200 {
		t.Fatalf("load: %d %s", rec.Code, rec.Body)
	}
	got := decodeAs[loadAnswer](t, rec)
	if got.ReceiptID != up.Id || got.Confirmed || got.Created != 2 || got.Linked != 0 || len(got.Lines) != 2 {
		t.Fatalf("load = %+v", got)
	}
	for _, l := range got.Lines {
		if l.Action != "created" || l.ProductID == 0 || l.ProductName == "" || l.LineID == 0 || l.RawText == "" {
			t.Errorf("line %+v", l)
		}
	}
	// the products look like the ones confirm makes: created_via=receipt, listed, in the review queue when unsure
	if n := count(t, e, `SELECT COUNT(*) FROM products WHERE created_via = 'receipt'`); n != 2 {
		t.Errorf("%d products with created_via=receipt", n)
	}
	list := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products", nil))
	for _, p := range list {
		if p.CreatedVia == nil || *p.CreatedVia != apitypes.ProductCreatedViaReceipt {
			t.Errorf("product %q created_via = %v", p.Name, p.CreatedVia)
		}
	}
	q := decodeAs[apitypes.ReviewQueue](t, e.do("GET", "/v1/products/review-queue", nil))
	if q.Total != got.NeedsReview {
		t.Errorf("review queue %d, load said %d", q.Total, got.NeedsReview)
	}
	// the receipt's lines point at the products now
	if n := count(t, e, fmt.Sprintf(`SELECT COUNT(*) FROM receipt_lines WHERE receipt_id = %d AND kind = 'item' AND product_id IS NOT NULL`, up.Id)); n != 2 {
		t.Errorf("%d lines linked", n)
	}

	// running it again, and confirming afterwards, creates nothing new
	again := decodeAs[loadAnswer](t, e.do("POST", path, nil))
	if again.Created != 0 || again.Linked != 0 || len(again.Lines) != 0 {
		t.Errorf("second load = %+v", again)
	}
	conf := decodeAs[apitypes.Receipt](t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", up.Id), nil))
	if conf.AutoProducts == nil || conf.AutoProducts.Created != 0 || conf.AutoProducts.Linked != 0 {
		t.Errorf("confirm after load = %+v", conf.AutoProducts)
	}
	if n := count(t, e, `SELECT COUNT(*) FROM products`); n != 2 {
		t.Errorf("%d products", n)
	}

	// a receipt confirmed earlier (with the feature off) loads too; its line that the matcher already linked
	// by alias at parse time is left alone, the new one gets a product
	e.auto.SetEnabled(false)
	_, old := uploadAndConfirm(e, shop.Id, []string{"02-10-2026 10:00|", "AH BIO MELK|1,39", "BROOD|2,10", "TOTAAL|3,49"}, 72)
	e.auto.SetEnabled(true)
	got = decodeAs[loadAnswer](t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/load-products", old.Id), nil))
	if !got.Confirmed || got.Created != 1 || len(got.Lines) != 1 || got.Lines[0].ProductName != "Brood" {
		t.Errorf("old receipt = %+v; want 1 created (Brood)", got)
	}
}

// A receipt that is still being read cannot be loaded.
func TestLoadProductsRefusesAReceiptThatIsStillBeingRead(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.ocr.rows.Store([]string{"01-10-2026 10:00|", "FANTA|3,25", "TOTAAL|3,25"})
	up := decodeAs[apitypes.Receipt](t, e.upload(shop.Id, pngBytes(73), nil)) // not drained: pending
	if rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/load-products", up.Id), nil); rec.Code != 409 {
		t.Errorf("pending receipt: %d %s, want 409", rec.Code, rec.Body)
	}
	if n := count(t, e, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Errorf("%d products", n)
	}
}

// Load from all receipts: preview with names, the real run, a receipt left out, a second run.
func TestLoadFromAllReceiptsOverHTTP(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.auto.SetEnabled(false)
	r1, _ := uploadAndConfirm(e, shop.Id, []string{"01-10-2026 10:00|", "AH MELK|1,29", "FANTA|3,25", "TOTAAL|4,54"}, 81)
	r2, _ := uploadAndConfirm(e, shop.Id, []string{"02-10-2026 10:00|", "KAAS|4,00", "TOTAAL|4,00"}, 82)
	e.auto.SetEnabled(true)

	st := jsonMap(t, e.do("GET", "/v1/products/autopopulate/backfill", nil).Body.String())
	if st["eligible_receipts"].(float64) != 2 || st["state"] != "idle" {
		t.Errorf("before: %v", st)
	}
	skip := []int64{r2.Id}
	if rec := e.do("POST", "/v1/products/autopopulate/backfill", map[string]any{"dry_run": true, "skip_receipt_ids": skip}); rec.Code != 202 {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	e.drain()
	st = jsonMap(t, e.do("GET", "/v1/products/autopopulate/backfill", nil).Body.String())
	names, _ := st["created_names"].([]any)
	if st["state"] != "done" || st["dry_run"] != true || st["created"].(float64) != 2 || len(names) != 2 || st["receipts_done"] != st["receipts_total"] {
		t.Errorf("preview = %v", st)
	}
	if n := count(t, e, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Errorf("preview made %d products", n)
	}
	e.do("POST", "/v1/products/autopopulate/backfill", map[string]any{"skip_receipt_ids": skip})
	e.drain()
	st = jsonMap(t, e.do("GET", "/v1/products/autopopulate/backfill", nil).Body.String())
	if st["state"] != "done" || st["created"].(float64) != 2 || st["eligible_receipts"].(float64) != 1 {
		t.Errorf("real run = %v", st)
	}
	if n := count(t, e, fmt.Sprintf(`SELECT COUNT(*) FROM receipt_lines WHERE receipt_id = %d AND product_id IS NOT NULL`, r2.Id)); n != 0 {
		t.Errorf("the excluded receipt got %d products", n)
	}
	if n := count(t, e, `SELECT COUNT(*) FROM products WHERE created_via = 'backfill'`); n != 2 {
		t.Errorf("%d backfill products", n)
	}
	// twice: nothing new
	e.do("POST", "/v1/products/autopopulate/backfill", map[string]any{"skip_receipt_ids": skip})
	e.drain()
	st = jsonMap(t, e.do("GET", "/v1/products/autopopulate/backfill", nil).Body.String())
	if st["created"].(float64) != 0 || st["linked"].(float64) != 0 {
		t.Errorf("second run = %v", st)
	}
	// the receipt left out can still be loaded on purpose
	got := decodeAs[loadAnswer](t, e.do("POST", fmt.Sprintf("/v1/receipts/%d/load-products", r2.Id), nil))
	if got.Created != 1 {
		t.Errorf("deliberate load of the left-out receipt = %+v", got)
	}
	_ = r1
}
