package httpapi_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"shop/internal/apitypes"
)

func jsonMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, body)
	}
	return m
}

// uploadAndConfirm uploads a receipt whose OCR text is the given rows and confirms it.
func uploadAndConfirm(e *env, shopID int64, rows []string, seed uint8) (apitypes.Receipt, apitypes.Receipt) {
	e.ocr.rows.Store(rows)
	r := decodeAs[apitypes.Receipt](e.t, e.upload(shopID, pngBytes(seed), nil))
	e.drain()
	rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", r.Id), nil)
	if rec.Code != 200 {
		e.t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	return r, decodeAs[apitypes.Receipt](e.t, rec)
}

func TestConfirmCreatesProductsWhenFlagIsOn(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	rows := []string{"01-10-2026 10:00|", "AH BIO MELK|1,39", "FANTA|3,25", "TOTAAL|4,64"}

	e.auto.SetEnabled(false)
	_, off := uploadAndConfirm(e, shop.Id, rows, 51)
	if off.AutoProducts != nil {
		t.Errorf("auto_products present with the flag off: %+v", off.AutoProducts)
	}
	if n := count(t, e, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Errorf("%d products created with the flag off", n)
	}

	e.auto.SetEnabled(true)
	_, on := uploadAndConfirm(e, shop.Id, []string{"02-10-2026 10:00|", "AH BIO MELK|1,39", "FANTA|3,25", "TOTAAL|4,64"}, 52)
	if on.AutoProducts == nil || on.AutoProducts.Created != 2 || on.AutoProducts.Error != nil {
		t.Fatalf("auto_products = %+v; want 2 created", on.AutoProducts)
	}
	// the flag-on confirm links the lines; a second confirm creates nothing
	rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", on.Id), nil)
	again := decodeAs[apitypes.Receipt](t, rec)
	if again.AutoProducts == nil || again.AutoProducts.Created != 0 || again.AutoProducts.Linked != 0 {
		t.Errorf("second confirm = %+v; want nothing new", again.AutoProducts)
	}
	for _, l := range *on.Lines {
		_ = l
	}
	if n := count(t, e, `SELECT COUNT(*) FROM products`); n != 2 {
		t.Errorf("%d products; want 2", n)
	}
}

func count(t *testing.T, e *env, q string) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReviewQueueMergeUndoOverHTTP(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	// same order as on the real receipts: the long variant first, then the short one
	uploadAndConfirm(e, shop.Id, []string{"01-10-2026 10:00|", "ARLA BIO HALFV. MELK|1,89", "TOTAAL|1,89"}, 61)
	uploadAndConfirm(e, shop.Id, []string{"02-10-2026 10:00|", "ARLA MELK|1,79", "TOTAAL|1,79"}, 62)

	cheap := e.do("GET", "/v1/products/review-queue?limit=0", nil)
	m := jsonMap(t, cheap.Body.String())
	if cheap.Code != 200 || m["total"].(float64) < 1 || m["items"] != nil && len(m["items"].([]any)) != 0 {
		t.Fatalf("limit=0: %d %s", cheap.Code, cheap.Body)
	}
	if _, ok := m["counts"].(map[string]any)["near_match"]; !ok {
		t.Errorf("counts lacks near_match: %v", m["counts"])
	}

	q := decodeAs[apitypes.ReviewQueue](t, e.do("GET", "/v1/products/review-queue?reason=near_match", nil))
	if len(q.Items) != 1 || q.Items[0].SuggestedMerge == nil || q.Items[0].SourceLineCount != 1 || q.Items[0].SourceLines[0].RawText == "" {
		t.Fatalf("near_match queue = %+v", q)
	}
	card := q.Items[0]
	if card.Product.CreatedVia == nil || *card.Product.CreatedVia != "receipt" || card.Product.NeedsReview == nil || !*card.Product.NeedsReview {
		t.Errorf("product flags on the card: %+v", card.Product)
	}

	rec := e.do("POST", fmt.Sprintf("/v1/products/%d/review", card.Product.Id), map[string]any{"action": "merge"})
	if rec.Code != 200 {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body)
	}
	res := decodeAs[apitypes.ReviewResult](t, rec)
	if res.Product.Id != card.SuggestedMerge.ProductId || res.MergedAway == nil || res.MergedAway.Id != card.Product.Id || res.MergeId == nil || *res.Kind != apitypes.ReviewResultKindMerge {
		t.Fatalf("merge result = %+v", res)
	}
	// the card left the queue: a second decision is a 409
	if rec := e.do("POST", fmt.Sprintf("/v1/products/%d/review", card.Product.Id), map[string]any{"action": "accept"}); rec.Code != 404 && rec.Code != 409 {
		t.Errorf("decision on a merged-away product: %d", rec.Code)
	}

	u := e.do("POST", fmt.Sprintf("/v1/product-merges/%d/undo", *res.MergeId), nil)
	if u.Code != 200 {
		t.Fatalf("undo: %d %s", u.Code, u.Body)
	}
	un := decodeAs[apitypes.UndoResult](t, u)
	if un.Kind != apitypes.UndoResultKindMerge || un.Product.Id != card.Product.Id || un.Product.NeedsReview == nil || !*un.Product.NeedsReview ||
		un.Product.ReviewReason == nil || *un.Product.ReviewReason != "near_match" || un.Product.SuggestedMerge == nil {
		t.Errorf("undo must restore the review state: %+v", un.Product)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/product-merges/%d/undo", *res.MergeId), nil); rec.Code != 409 {
		t.Errorf("second undo: %d, want 409", rec.Code)
	}
}

func TestMergeErrorsAndSplitOverHTTP(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	_, rc := uploadAndConfirm(e, shop.Id, []string{"01-10-2026 10:00|", "FANTA|3,25", "TOTAAL|3,25"}, 71)
	uploadAndConfirm(e, shop.Id, []string{"02-10-2026 10:00|", "AH MELK|1,29", "TOTAAL|1,29"}, 72)
	var a, b int64
	_ = e.conn.QueryRow(`SELECT id FROM products WHERE name='Fanta'`).Scan(&a)
	_ = e.conn.QueryRow(`SELECT id FROM products WHERE name='AH Melk'`).Scan(&b)
	_, _ = e.conn.Exec(`UPDATE products SET barcode='8711111111111' WHERE id=?`, a)
	_, _ = e.conn.Exec(`UPDATE products SET barcode='8722222222222' WHERE id=?`, b)

	rec := e.do("POST", fmt.Sprintf("/v1/products/%d/merge", a), map[string]any{"into_product_id": b})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "These two have different barcodes.") {
		t.Errorf("different barcodes: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/products/%d/merge", a), map[string]any{"into_product_id": a}); rec.Code != 422 {
		t.Errorf("self merge: %d, want 422", rec.Code)
	}

	// split the Fanta line into its own product
	lines := *rc.Lines
	var lineID int64
	for _, l := range lines {
		if l.Kind == apitypes.Item {
			lineID = l.Id
		}
	}
	rec = e.do("POST", fmt.Sprintf("/v1/lines/%d/split-product", lineID), map[string]any{"name": "Fanta Orange"})
	if rec.Code != 201 {
		t.Fatalf("split: %d %s", rec.Code, rec.Body)
	}
	sp := decodeAs[apitypes.SplitResult](t, rec)
	if sp.Product.Name != "Fanta Orange" || sp.Kind != apitypes.SplitResultKindSplit {
		t.Errorf("split result = %+v", sp)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/product-merges/%d/undo", sp.MergeId), nil); rec.Code != 200 {
		t.Errorf("undo split: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/v1/lines/999999/split-product", nil); rec.Code != 404 {
		t.Errorf("split missing line: %d", rec.Code)
	}
}

func TestBarcodeCreateOverHTTP(t *testing.T) {
	e := newEnv(t)
	code := "8710400000017" // the fake Open Food Facts knows this one
	rec := e.do("POST", "/v1/barcodes/"+code+"/product", nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	m := jsonMap(t, rec.Body.String())
	if m["status"] != "created" || m["created_now"] != true || m["off_status"] != "found" {
		t.Errorf("create body = %v", m)
	}
	if v, ok := m["merge_suggestion"]; !ok || v != nil {
		t.Errorf("merge_suggestion must be present and null: %v", m["merge_suggestion"])
	}
	prod := m["product"].(map[string]any)
	if prod["created_via"] != "barcode" || prod["barcode"] != code || prod["created_at"] == nil {
		t.Errorf("product = %v", prod)
	}

	rec = e.do("POST", "/v1/barcodes/"+code+"/product", nil) // a retry after a lost response
	if rec.Code != 200 {
		t.Fatalf("repeat: %d", rec.Code)
	}
	m = jsonMap(t, rec.Body.String())
	if m["status"] != "known" || m["created_now"] != false || m["merge_suggestion"] != nil {
		t.Errorf("repeat body = %v", m)
	}
	if ul, ok := m["unmatched_lines"].([]any); !ok || len(ul) != 0 {
		t.Errorf("unmatched_lines must be []: %v", m["unmatched_lines"])
	}
	if _, ok := m["off_status"]; ok {
		t.Error("off_status must be absent for known")
	}

	// unknown to OFF: minimal product
	rec = e.do("POST", "/v1/barcodes/8719999999999/product", nil)
	m = jsonMap(t, rec.Body.String())
	if rec.Code != 201 || m["status"] != "created_minimal" || m["off_status"] != "not_found" {
		t.Errorf("minimal: %d %v", rec.Code, m)
	}
	if rec := e.do("POST", "/v1/barcodes/123/product", nil); rec.Code != 400 {
		t.Errorf("bad code: %d", rec.Code)
	}
	// the queue shows it, and refresh on a product without OFF data keeps it minimal
	q := decodeAs[apitypes.ReviewQueue](t, e.do("GET", "/v1/products/review-queue?reason=barcode_minimal", nil))
	if len(q.Items) != 1 {
		t.Fatalf("barcode_minimal queue = %d items", len(q.Items))
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/products/%d/refresh", q.Items[0].Product.Id), nil); rec.Code != 200 {
		t.Errorf("refresh: %d %s", rec.Code, rec.Body)
	}
}

func TestBackfillOverHTTP(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	e.auto.SetEnabled(false)
	uploadAndConfirm(e, shop.Id, []string{"01-10-2026 10:00|", "AH MELK|1,29", "FANTA|3,25", "TOTAAL|4,54"}, 81)

	if rec := e.do("POST", "/v1/products/autopopulate/backfill", map[string]any{"dry_run": true}); rec.Code != 409 {
		t.Errorf("backfill with the flag off: %d, want 409", rec.Code)
	}
	e.auto.SetEnabled(true)
	rec := e.do("POST", "/v1/products/autopopulate/backfill", map[string]any{"dry_run": true})
	if rec.Code != 202 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/v1/products/autopopulate/backfill", nil); rec.Code != 409 {
		t.Errorf("second start while running: %d, want 409", rec.Code)
	}
	e.drain()
	st := jsonMap(t, e.do("GET", "/v1/products/autopopulate/backfill", nil).Body.String())
	if st["state"] != "done" || st["dry_run"] != true || st["created"].(float64) != 2 {
		t.Errorf("dry run state = %v", st)
	}
	if n := count(t, e, `SELECT COUNT(*) FROM products`); n != 0 {
		t.Errorf("dry run created %d products", n)
	}
	// a skip list leaves that receipt out
	rec = e.do("POST", "/v1/products/autopopulate/backfill", map[string]any{"dry_run": true, "skip_receipt_ids": []int64{1}})
	if rec.Code != 202 {
		t.Fatalf("start with skip: %d %s", rec.Code, rec.Body)
	}
	e.drain()
	st = jsonMap(t, e.do("GET", "/v1/products/autopopulate/backfill", nil).Body.String())
	if st["receipts_total"].(float64) != 0 || st["created"].(float64) != 0 {
		t.Errorf("skipping the only receipt: %v", st)
	}
	e.do("POST", "/v1/products/autopopulate/backfill", map[string]any{"dry_run": false})
	e.drain()
	st = jsonMap(t, e.do("GET", "/v1/products/autopopulate/backfill", nil).Body.String())
	if st["state"] != "done" || st["created"].(float64) != 2 || count(t, e, `SELECT COUNT(*) FROM products`) != 2 {
		t.Errorf("real run state = %v", st)
	}
}
