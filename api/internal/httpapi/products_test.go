package httpapi_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"shop/internal/apitypes"
)

func (e *env) product(body map[string]any) apitypes.Product {
	e.t.Helper()
	rec := e.do("POST", "/v1/products", body)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create product: %d %s", rec.Code, rec.Body)
	}
	return decodeAs[apitypes.Product](e.t, rec)
}

func (e *env) receipt(shopID int64, seed uint8, rows ...string) apitypes.Receipt {
	e.t.Helper()
	e.ocr.rows.Store(rows)
	rec := e.upload(shopID, pngBytes(seed), nil)
	if rec.Code != http.StatusAccepted {
		e.t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	id := decodeAs[apitypes.Receipt](e.t, rec).Id
	e.drain()
	return decodeAs[apitypes.Receipt](e.t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", id), nil))
}

func TestProductCRUDAndSearch(t *testing.T) {
	e := newEnv(t)
	milk := e.product(map[string]any{
		"name": "Halfvolle melk", "brand": "AH", "barcode": "8710400000017",
		"net_quantity": 1500, "net_unit": "ml",
		"nutrition": map[string]any{"per": "100ml", "energy_kcal": 46, "protein": 3.5},
	})
	if milk.Nutrition == nil || *milk.Nutrition.EnergyKcal != 46 || *milk.Nutrition.Source != "manual" {
		t.Fatalf("nutrition = %+v", milk.Nutrition)
	}
	e.product(map[string]any{"name": "Volle yoghurt", "brand": "Zuivelhoeve"})
	e.product(map[string]any{"name": "Haverdrank barista", "brand": "Oatly"})

	// Validation and conflicts.
	if rec := e.do("POST", "/v1/products", map[string]any{"name": "X", "barcode": "8710400000017"}); rec.Code != 409 {
		t.Errorf("duplicate barcode: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/products", map[string]any{"name": "X", "net_quantity": 1}); rec.Code != 400 {
		t.Errorf("quantity without unit: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/products", map[string]any{"name": "X", "image_url": "https://evil.example/x.jpg"}); rec.Code != 400 {
		t.Errorf("foreign image_url: %d", rec.Code)
	}

	// Fuzzy search tolerates receipt-style abbreviations and typos.
	for q, want := range map[string]string{
		"halfv melk":  "Halfvolle melk",
		"yoghurt vol": "Volle yoghurt",
		"haverdrnk":   "Haverdrank barista",
	} {
		res := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products?q="+urlq(q), nil))
		if len(res) == 0 || res[0].Name != want {
			t.Errorf("search %q: got %+v", q, names(res))
		}
	}
	all := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products", nil))
	if len(all) != 3 || all[0].Name != "Halfvolle melk" {
		t.Errorf("list: %v", names(all))
	}

	// Update keeps nutrition unless provided.
	up := decodeAs[apitypes.Product](t, e.do("PATCH", fmt.Sprintf("/v1/products/%d", milk.Id),
		map[string]any{"name": "Halfvolle melk", "brand": "AH", "barcode": "8710400000017"}))
	if up.Nutrition == nil || up.NetQuantity != nil {
		t.Errorf("update: %+v", up)
	}

	// Nutrition replace + delete.
	n := decodeAs[apitypes.Product](t, e.do("PUT", fmt.Sprintf("/v1/products/%d/nutrition", milk.Id),
		map[string]any{"per": "100ml", "fat": 1.5}))
	if n.Nutrition.EnergyKcal != nil || *n.Nutrition.Fat != 1.5 {
		t.Errorf("nutrition put: %+v", n.Nutrition)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/products/%d/nutrition", milk.Id), nil); rec.Code != 204 {
		t.Errorf("nutrition delete: %d", rec.Code)
	}

	// Image upload is resized and served as JPEG.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "p.png")
	_, _ = fw.Write(pngBytes(9))
	_ = mw.Close()
	req := httptest.NewRequest("PUT", fmt.Sprintf("/v1/products/%d/image", milk.Id), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if p := decodeAs[apitypes.Product](t, rec); !p.HasImage {
		t.Fatalf("image upload: %d %s", rec.Code, rec.Body)
	}
	if img := e.do("GET", fmt.Sprintf("/v1/products/%d/image", milk.Id), nil); img.Code != 200 || img.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("image get: %d %s", img.Code, img.Header().Get("Content-Type"))
	}

	// Soft delete frees the barcode.
	if rec := e.do("DELETE", fmt.Sprintf("/v1/products/%d", milk.Id), nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if res := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products?q=halfvolle", nil)); len(res) != 0 {
		t.Errorf("deleted product still searchable: %v", names(res))
	}
	e.product(map[string]any{"name": "Melk", "barcode": "8710400000017"})
}

func TestBarcodeLookup(t *testing.T) {
	e := newEnv(t)
	// Unknown locally → Open Food Facts prefill.
	res := decodeAs[apitypes.BarcodeLookup](t, e.do("GET", "/v1/barcodes/8710400000017", nil))
	if res.Source != apitypes.BarcodeLookupSourceOpenfoodfacts || res.Prefill == nil || res.Prefill.Name != "Halfvolle melk" {
		t.Fatalf("prefill: %+v", res)
	}
	if res.Prefill.NetQuantity == nil || *res.Prefill.NetQuantity != 1500 || *res.Prefill.Nutrition.Per != "100ml" {
		t.Errorf("prefill details: %+v %+v", res.Prefill, res.Prefill.Nutrition)
	}

	// Save it (as the app would, from the prefill) → now local.
	e.product(map[string]any{
		"name": res.Prefill.Name, "barcode": "8710400000017", "openfoodfacts_code": res.Prefill.Code,
		"nutrition": map[string]any{"per": "100ml", "energy_kcal": 46, "source": "openfoodfacts"},
	})
	res = decodeAs[apitypes.BarcodeLookup](t, e.do("GET", "/v1/barcodes/8710400000017", nil))
	if res.Source != apitypes.BarcodeLookupSourceLocal || res.Product == nil || *res.Product.Nutrition.Source != "openfoodfacts" {
		t.Fatalf("local: %+v", res)
	}

	if rec := e.do("GET", "/v1/barcodes/12345670", nil); rec.Code != 404 {
		t.Errorf("unknown: %d", rec.Code)
	}
	if rec := e.do("GET", "/v1/barcodes/abc", nil); rec.Code != 400 {
		t.Errorf("invalid: %d", rec.Code)
	}
}

func TestLinkingLearnsAliases(t *testing.T) {
	e := newEnv(t)
	ah, lidl := e.shop("AH"), e.shop("Lidl")
	milk := e.product(map[string]any{"name": "Halfvolle melk", "brand": "AH"})

	rows := []string{"01-10-2026|", "AH HALFV MELK|1,29", "KAAS|4,99", "TOTAAL|6,28"}
	r1 := e.receipt(ah.Id, 1, rows...)
	lines := *r1.Lines
	if lines[0].ProductId != nil {
		t.Fatal("nothing should be linked before an alias exists")
	}
	// Unmatched item lines carry suggestions.
	if lines[0].Suggestions == nil || len(*lines[0].Suggestions) == 0 || (*lines[0].Suggestions)[0].ProductId != milk.Id {
		t.Fatalf("suggestions = %+v", lines[0].Suggestions)
	}

	// A second, unprocessed-yet-unlinked receipt with the same text.
	r2 := e.receipt(ah.Id, 2, "02-10-2026|", "AH HALFV MELK|1,29", "TOTAAL|1,29")

	// Recent unmatched lines for the product rank the milk lines first.
	um := decodeAs[[]apitypes.UnmatchedLine](t, e.do("GET", fmt.Sprintf("/v1/products/%d/unmatched-lines?days=30", milk.Id), nil))
	if len(um) != 3 || um[0].Line.RawText != "AH HALFV MELK" || um[2].Line.RawText != "KAAS" {
		t.Fatalf("unmatched: %+v", um)
	}

	// Link with remember → alias, and the other receipt's line is linked too.
	res := decodeAs[apitypes.LinkResult](t, e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", lines[0].Id),
		map[string]any{"product_id": milk.Id}))
	if !res.AliasCreated || res.AlsoLinked != 1 || res.Line.ProductId == nil {
		t.Fatalf("link: %+v", res)
	}
	got2 := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r2.Id), nil))
	if (*got2.Lines)[0].ProductId == nil || *(*got2.Lines)[0].ProductId != milk.Id {
		t.Errorf("second receipt not linked: %+v", (*got2.Lines)[0])
	}

	// New receipts at the same shop auto-link; other shops don't.
	r3 := e.receipt(ah.Id, 3, "03-10-2026|", "AH HALFV MELK|1,35", "TOTAAL|1,35")
	if (*r3.Lines)[0].ProductId == nil {
		t.Error("new AH receipt should auto-link via alias")
	}
	if l := (*r3.Lines)[0]; l.MatchedBy == nil || *l.MatchedBy != apitypes.ReceiptLineMatchedByAlias || l.MatchScore == nil || *l.MatchScore != 1 {
		t.Errorf("alias link at scan: matched_by %v score %v", l.MatchedBy, l.MatchScore)
	}
	// The alias is the AH shop's, but the identical text scores 1.0 against the catalogue, which is what
	// the confirm step would link too: the scan links it now (by score, not by alias).
	r4 := e.receipt(lidl.Id, 4, "03-10-2026|", "AH HALFV MELK|1,35", "TOTAAL|1,35")
	if l := (*r4.Lines)[0]; l.ProductId == nil || l.MatchedBy == nil || *l.MatchedBy != apitypes.ReceiptLineMatchedByScore {
		t.Errorf("Lidl receipt: %+v", l)
	}

	// Aliases are listed and deletable.
	aliases := decodeAs[[]apitypes.Alias](t, e.do("GET", fmt.Sprintf("/v1/products/%d/aliases", milk.Id), nil))
	if len(aliases) != 1 || aliases[0].Text != "ah halfvol melk" || *aliases[0].ShopName != "AH" {
		t.Fatalf("aliases: %+v", aliases)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/aliases/%d", aliases[0].Id), nil); rec.Code != 204 {
		t.Fatalf("delete alias: %d", rec.Code)
	}
	r5 := e.receipt(ah.Id, 5, "04-10-2026|", "AH HALFV MELK|1,35", "TOTAAL|1,35")
	if (*r5.Lines)[0].ProductId != nil {
		t.Error("deleted alias must stop auto-linking")
	}

	// Unlink.
	res = decodeAs[apitypes.LinkResult](t, e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", lines[0].Id), map[string]any{"product_id": nil}))
	if res.Line.ProductId != nil || res.AliasCreated {
		t.Errorf("unlink: %+v", res)
	}
	if rec := e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", lines[0].Id), map[string]any{"product_id": 999}); rec.Code != 400 {
		t.Errorf("unknown product: %d", rec.Code)
	}
}

func names(ps []apitypes.Product) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func urlq(s string) string {
	b := []byte{}
	for _, c := range []byte(s) {
		if c == ' ' {
			b = append(b, '+')
		} else {
			b = append(b, c)
		}
	}
	return string(b)
}
