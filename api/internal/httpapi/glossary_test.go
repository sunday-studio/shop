package httpapi_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"shop/internal/apitypes"
)

type teachResp struct {
	Term            apitypes.GlossaryTerm       `json:"term"`
	Previous        *apitypes.GlossaryTermInput `json:"previous"`
	ProductsUpdated int                         `json:"products_updated"`
}

type todoResp struct {
	Totals struct {
		WordsLeft    int `json:"words_left"`
		ProductsLeft int `json:"products_left"`
	} `json:"totals"`
	Words []struct {
		Word     string   `json:"word"`
		Key      string   `json:"key"`
		Products int      `json:"products"`
		Lines    int      `json:"lines"`
		Examples []string `json:"examples"`
	} `json:"words"`
	Products []struct {
		ID            int64           `json:"id"`
		Name          string          `json:"name"`
		NameENStatus  string          `json:"name_en_status"`
		NameENUnknown []apitypes.Span `json:"name_en_unknown"`
		ShopName      *string         `json:"shop_name"`
	} `json:"products"`
}

func TestTeachWords(t *testing.T) {
	e := newEnv(t)
	get := func(id int64) apitypes.Product {
		return decodeAs[apitypes.Product](t, e.do("GET", fmt.Sprintf("/v1/products/%d", id), nil))
	}
	appels := e.product(map[string]any{"name": "Appels Elstar"})
	sap := e.product(map[string]any{"name": "Elstar sap"})
	calve := e.product(map[string]any{"name": "Calvé pindakaas"})
	unox := e.product(map[string]any{"name": "Unox Knorr"})
	manual := e.product(map[string]any{"name": "Elstar handpeer", "name_en": "Hand pear"})

	// Status + spans on product reads.
	if *appels.NameEnStatus != apitypes.NameEnStatusPartial || *appels.NameEn != "Apples Elstar" {
		t.Fatalf("appels: %v %v", *appels.NameEnStatus, *appels.NameEn)
	}
	if sp := *appels.NameEnUnknown; len(sp) != 1 || sp[0] != (apitypes.Span{Word: "Elstar", Key: "elstar", Start: 7, End: 13}) {
		t.Errorf("appels spans: %+v", sp)
	}
	if sp := *calve.NameEnUnknown; len(sp) != 1 || sp[0].Word != "Calvé" || sp[0].Key != "calve" || sp[0].End-sp[0].Start != 5 {
		t.Errorf("calvé span: %+v", sp)
	}
	if *unox.NameEnStatus != apitypes.NameEnStatusMissing || unox.NameEn != nil || len(*unox.NameEnUnknown) != 2 {
		t.Errorf("unox: %+v", unox)
	}
	if *manual.NameEnStatus != apitypes.NameEnStatusManual || len(*manual.NameEnUnknown) != 0 {
		t.Errorf("manual: %+v", manual)
	}

	// Last purchase shop shows in the queue.
	shop := e.shop("Lidl")
	r := e.receipt(shop.Id, 1, "01-10-2026|", "APPELS ELSTAR|2,49", "ELSTAR TAART|3,99", "TOTAAL|6,48")
	e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", (*r.Lines)[0].Id), map[string]any{"product_id": appels.Id})

	// The queue: totals from the server, words ranked by products.
	todo := decodeAs[todoResp](t, e.do("GET", "/v1/translations/todo", nil))
	if todo.Totals.WordsLeft != 4 || todo.Totals.ProductsLeft != 4 {
		t.Fatalf("totals: %+v", todo.Totals)
	}
	if w := todo.Words[0]; w.Key != "elstar" || w.Products != 2 || w.Lines != 1 || len(w.Examples) != 2 {
		t.Errorf("top word: %+v", w)
	}
	if p := todo.Products[0]; p.ID != appels.Id || p.ShopName == nil || *p.ShopName != "Lidl" {
		t.Errorf("most recently bought first, with shop: %+v", p)
	}
	if lim := decodeAs[todoResp](t, e.do("GET", "/v1/translations/todo?limit=1", nil)); len(lim.Words) != 1 || lim.Totals.WordsLeft != 4 {
		t.Errorf("limit trims lists, not totals: %+v", lim.Totals)
	}

	// Preview: nothing saved.
	pre := decodeAs[struct {
		ProductsAffected int `json:"products_affected"`
		Examples         []struct {
			Name        string `json:"name"`
			NameENAfter string `json:"name_en_after"`
			StatusAfter string `json:"status_after"`
		} `json:"examples"`
	}](t, e.do("POST", "/v1/glossary/preview", map[string]any{"dutch": "Elstar", "keep_as_is": true}))
	if pre.ProductsAffected != 2 || pre.Examples[0].StatusAfter != "dictionary" {
		t.Fatalf("preview: %+v", pre)
	}
	if *get(appels.Id).NameEnStatus != apitypes.NameEnStatusPartial {
		t.Fatal("preview must not save")
	}

	// Teach (keep as is): both products leave the queue, the manual one is untouched.
	tr := decodeAs[teachResp](t, e.do("PUT", "/v1/glossary/Elstar", map[string]any{"keep_as_is": true}))
	if tr.ProductsUpdated != 2 || tr.Previous != nil || !tr.Term.KeepAsIs || tr.Term.Dutch != "elstar" || tr.Term.ProductsCount != 3 {
		t.Fatalf("teach: %+v", tr)
	}
	if p := get(sap.Id); *p.NameEnStatus != apitypes.NameEnStatusDictionary || *p.NameEn != "Elstar juice" {
		t.Errorf("sap after teach: %v %v", *p.NameEnStatus, *p.NameEn)
	}
	if p := get(manual.Id); *p.NameEn != "Hand pear" {
		t.Errorf("manual name touched: %v", *p.NameEn)
	}

	// Re-teaching returns the previous value; undo restores it exactly.
	tr2 := decodeAs[teachResp](t, e.do("PUT", "/v1/glossary/elstar", map[string]any{"english": "Elstar apple"}))
	if tr2.Previous == nil || !*tr2.Previous.KeepAsIs || tr2.ProductsUpdated != 2 {
		t.Fatalf("re-teach: %+v", tr2)
	}
	if p := get(appels.Id); *p.NameEn != "Apples Elstar apple" {
		t.Errorf("after re-teach: %v", *p.NameEn)
	}
	undo := decodeAs[teachResp](t, e.do("PUT", "/v1/glossary/elstar", map[string]any{"keep_as_is": true}))
	if undo.ProductsUpdated != 2 {
		t.Errorf("undo via previous: %+v", undo)
	}

	// Glossary list with counts; delete goes back to built-in behaviour.
	terms := decodeAs[[]apitypes.GlossaryTerm](t, e.do("GET", "/v1/glossary", nil))
	if len(terms) != 1 || terms[0].ProductsCount != 3 {
		t.Errorf("glossary: %+v", terms)
	}
	del := decodeAs[map[string]int](t, e.do("DELETE", "/v1/glossary/elstar", nil))
	if del["products_updated"] != 2 || *get(appels.Id).NameEnStatus != apitypes.NameEnStatusPartial {
		t.Errorf("delete: %+v", del)
	}
	if rec := e.do("DELETE", "/v1/glossary/elstar", nil); rec.Code != 404 {
		t.Errorf("delete missing: %d", rec.Code)
	}

	// Validation.
	if rec := e.do("PUT", "/v1/glossary/"+url.PathEscape("two words"), map[string]any{"english": "x"}); rec.Code != http.StatusBadRequest {
		t.Errorf("phrase: %d", rec.Code)
	}
	if rec := e.do("PUT", "/v1/glossary/unox", map[string]any{"english": " "}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty english: %d", rec.Code)
	}

	// Live preview endpoint and receipt-line spans.
	tp := decodeAs[map[string]any](t, e.do("GET", "/v1/translate?text="+url.QueryEscape("Calvé pindakaas"), nil))
	if tp["status"] != "partial" || tp["english"] != "Calvé peanut butter" {
		t.Errorf("translate preview: %v", tp)
	}
	line := (*decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r.Id), nil)).Lines)[1]
	if line.TextEnStatus == nil || *line.TextEnStatus != "partial" || len(*line.TextEnUnknown) != 1 || (*line.TextEnUnknown)[0].Key != "elstar" {
		t.Errorf("line gloss: %+v %+v", line.TextEnStatus, line.TextEnUnknown)
	}
}
