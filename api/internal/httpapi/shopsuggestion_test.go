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
		if g := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", id), nil)); g.ShopId != want {
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
