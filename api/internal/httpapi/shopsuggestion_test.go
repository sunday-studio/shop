package httpapi_test

import (
	"fmt"
	"testing"

	"shop/internal/apitypes"
)

func ahRows(street string) []string {
	return []string{"command|", "Albert Heijn|", street + " Teststad|", "MELK|1,29", "TOTAAL|1,29"}
}

func uploadWith(e *env, shopID int64, rows []string, seed uint8) apitypes.Receipt {
	e.ocr.rows.Store(rows)
	r := decodeAs[apitypes.Receipt](e.t, e.upload(shopID, pngBytes(seed), nil))
	e.drain()
	return r
}

func TestShopSuggestionNewThenAcceptMovesWaiting(t *testing.T) {
	e := newEnv(t)
	holding := e.shop("Unsorted")
	a := uploadWith(e, holding.Id, ahRows("Hoofdstraat 12"), 11)
	b := uploadWith(e, holding.Id, ahRows("Hoofdstraat 12"), 12)
	other := uploadWith(e, holding.Id, ahRows("Hoofdstraat 9"), 13) // another branch: untouched

	got := decodeAs[struct {
		Suggestion *apitypes.ShopSuggestion `json:"suggestion"`
	}](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", a.Id), nil))
	s := got.Suggestion
	if s == nil || s.Kind != apitypes.New || s.Name != "Albert Heijn" ||
		s.Domain == nil || *s.Domain != "ah.nl" || s.Branch == nil || *s.Branch != "Hoofdstraat 12, Teststad" ||
		len(s.WaitingReceiptIds) != 1 || s.WaitingReceiptIds[0] != b.Id {
		t.Fatalf("suggestion = %+v", s)
	}
	// also embedded in the single-receipt GET
	full := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", a.Id), nil))
	if full.ShopSuggestion == nil {
		t.Error("shop_suggestion missing from GET /v1/receipts/{id}")
	}

	rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/shop-suggestion/accept", a.Id), map[string]any{"name": "AH Teststad", "domain": "ah.nl"})
	if rec.Code != 200 {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	res := decodeAs[struct {
		Shop   apitypes.Shop `json:"shop"`
		Linked []int64       `json:"linked_receipt_ids"`
	}](t, rec)
	if res.Shop.Name != "AH Teststad" || res.Shop.Domain == nil || *res.Shop.Domain != "ah.nl" || len(res.Linked) != 2 {
		t.Fatalf("accept result = %+v", res)
	}
	for id, want := range map[int64]int64{a.Id: res.Shop.Id, b.Id: res.Shop.Id, other.Id: holding.Id} {
		if g := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", id), nil)); g.ShopId == nil || *g.ShopId != want {
			t.Errorf("receipt %d on shop %d, want %d", id, g.ShopId, want)
		}
	}

	// Now the receipt is on its shop: nothing more to suggest. A new receipt from the same branch
	// is offered the existing shop.
	c := uploadWith(e, holding.Id, ahRows("Hoofdstraat 12"), 14)
	g := decodeAs[struct {
		Suggestion *apitypes.ShopSuggestion `json:"suggestion"`
	}](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", c.Id), nil))
	if g.Suggestion == nil || g.Suggestion.Kind != apitypes.Existing || *g.Suggestion.ShopId != res.Shop.Id {
		t.Fatalf("second suggestion = %+v; want existing shop %d", g.Suggestion, res.Shop.Id)
	}
}

func TestShopSuggestionRejectAndNone(t *testing.T) {
	e := newEnv(t)
	holding := e.shop("Unsorted")
	a := uploadWith(e, holding.Id, ahRows("Hoofdstraat 12"), 21)
	if rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/shop-suggestion/reject", a.Id), nil); rec.Code != 204 {
		t.Fatalf("reject: %d", rec.Code)
	}
	g := decodeAs[struct {
		Suggestion *apitypes.ShopSuggestion `json:"suggestion"`
	}](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", a.Id), nil))
	if g.Suggestion != nil {
		t.Errorf("rejected suggestion came back: %+v", g.Suggestion)
	}
	// a cropped slip has no header: no suggestion
	b := uploadWith(e, holding.Id, []string{"DEZE BON SC|", "KIPFILET|4,00", "TOTAAL|4,00"}, 22)
	g = decodeAs[struct {
		Suggestion *apitypes.ShopSuggestion `json:"suggestion"`
	}](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", b.Id), nil))
	if g.Suggestion != nil {
		t.Errorf("slip got a suggestion: %+v", g.Suggestion)
	}
}

func TestUnassignedUpload(t *testing.T) {
	e := newEnv(t)
	e.ocr.rows.Store(ahRows("Hoofdstraat 12"))
	taken := "2026-09-30T08:15:00Z"
	rec := e.upload(0, pngBytes(31), map[string]string{"photo_taken_at": taken})
	if rec.Code != 202 {
		t.Fatalf("upload without shop: %d %s", rec.Code, rec.Body)
	}
	r := decodeAs[apitypes.Receipt](t, rec)
	e.drain()
	got := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r.Id), nil))
	if got.ShopId != nil {
		t.Errorf("shop_id = %v; want absent", *got.ShopId)
	}
	if got.PhotoTakenAt == nil || got.PhotoTakenAt.UTC().Format("2006-01-02T15:04:05Z") != taken {
		t.Errorf("photo_taken_at = %v; want %s", got.PhotoTakenAt, taken)
	}
	// the hint never becomes the purchase date: the OCR text has no date, so it stays empty
	if got.PurchasedAt != nil && got.PurchasedAt.UTC().Format("2006-01-02") == "2026-09-30" {
		t.Errorf("purchased_at took the photo date: %v", got.PurchasedAt)
	}
	if rec := e.upload(99999, pngBytes(32), nil); rec.Code != 400 {
		t.Errorf("unknown shop: %d, want 400", rec.Code)
	}
	if rec := e.upload(0, pngBytes(33), map[string]string{"photo_taken_at": "yesterday"}); rec.Code != 400 {
		t.Errorf("bad photo_taken_at: %d, want 400", rec.Code)
	}
}

func TestUnassignedReceiptsCountInSpendAndAreSuggestedAndMoved(t *testing.T) {
	e := newEnv(t)
	e.ocr.rows.Store(ahRows("Hoofdstraat 12"))
	u1 := decodeAs[apitypes.Receipt](t, e.upload(0, pngBytes(42), nil))
	u2 := decodeAs[apitypes.Receipt](t, e.upload(0, pngBytes(43), nil))
	e.drain()

	spend := decodeAs[struct {
		Buckets []apitypes.SpendBucket `json:"buckets"`
	}](t, e.do("GET", "/v1/stats/spend?group_by=shop&from=2020-01-01&to=2100-01-01", nil))
	var total int64
	var unassigned bool
	for _, b := range spend.Buckets {
		total += b.Cents
		if b.Key == "none" && b.Label == "Unassigned" {
			unassigned = true
		}
	}
	if !unassigned || total == 0 {
		t.Fatalf("spend by shop = %+v; want an Unassigned bucket with the money in it", spend.Buckets)
	}

	g := decodeAs[struct {
		Suggestion *apitypes.ShopSuggestion `json:"suggestion"`
	}](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d/shop-suggestion", u1.Id), nil))
	if g.Suggestion == nil || len(g.Suggestion.WaitingReceiptIds) != 1 || g.Suggestion.WaitingReceiptIds[0] != u2.Id {
		t.Fatalf("suggestion = %+v; want the other unassigned receipt waiting", g.Suggestion)
	}
	rec := e.do("POST", fmt.Sprintf("/v1/receipts/%d/shop-suggestion/accept", u1.Id), map[string]any{})
	res := decodeAs[struct {
		Shop   apitypes.Shop `json:"shop"`
		Linked []int64       `json:"linked_receipt_ids"`
	}](t, rec)
	if rec.Code != 200 || len(res.Linked) != 2 {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []int64{u1.Id, u2.Id} {
		if g := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", id), nil)); g.ShopId == nil || *g.ShopId != res.Shop.Id {
			t.Errorf("receipt %d not moved to shop %d: %v", id, res.Shop.Id, g.ShopId)
		}
	}
}
