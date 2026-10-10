package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"shop/internal/apitypes"
)

func TestEnglishNames(t *testing.T) {
	e := newEnv(t)
	put := func(id int64, body map[string]any) apitypes.Product {
		t.Helper()
		rec := e.do("PATCH", fmt.Sprintf("/v1/products/%d", id), body)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body)
		}
		return decodeAs[apitypes.Product](t, rec)
	}
	en := func(p apitypes.Product) (string, string) {
		name, src := "", ""
		if p.NameEn != nil {
			name = *p.NameEn
		}
		if p.NameEnSource != nil {
			src = string(*p.NameEnSource)
		}
		return name, src
	}

	// Dutch name → automatic English.
	milk := e.product(map[string]any{"name": "Halfvolle melk"})
	if n, src := en(milk); n != "Semi-skimmed milk" || src != "dictionary" {
		t.Fatalf("auto english: %q %q", n, src)
	}
	chicken := e.product(map[string]any{"name": "Kipfilet"})
	e.product(map[string]any{"name": "Unox Cup-a-Soup"}) // no English: brand words only

	// Search works in both languages.
	for q, want := range map[string]string{"semi skimmed milk": "Halfvolle melk", "halfvolle": "Halfvolle melk", "chicken breast": "Kipfilet"} {
		res := decodeAs[[]apitypes.Product](t, e.do("GET", "/v1/products?q="+urlq(q), nil))
		if len(res) == 0 || res[0].Name != want {
			t.Errorf("search %q → %v", q, names(res))
		}
	}

	// Renaming the Dutch name re-translates a dictionary name…
	milk = put(milk.Id, map[string]any{"name": "Volle melk"})
	if n, _ := en(milk); n != "Whole milk" {
		t.Errorf("re-translate: %q", n)
	}
	// …but keeps a name you typed.
	milk = put(milk.Id, map[string]any{"name": "Volle melk", "name_en": "Full-fat milk"})
	milk = put(milk.Id, map[string]any{"name": "Volle melk 1L"})
	if n, src := en(milk); n != "Full-fat milk" || src != "manual" {
		t.Errorf("manual kept: %q %q", n, src)
	}
	// "" goes back to automatic.
	milk = put(milk.Id, map[string]any{"name": "Volle melk 1L", "name_en": ""})
	if n, src := en(milk); n != "Whole milk 1L" || src != "dictionary" {
		t.Errorf("reset to auto: %q %q", n, src)
	}

	// Open Food Facts English name, saved with its source.
	pre := decodeAs[apitypes.BarcodeLookup](t, e.do("GET", "/v1/barcodes/8710400000017", nil))
	p := e.product(map[string]any{"name": pre.Prefill.Name, "name_en": "Semi-skimmed milk (OFF)", "name_en_source": "openfoodfacts", "barcode": "8710400000017"})
	if n, src := en(p); n != "Semi-skimmed milk (OFF)" || src != "openfoodfacts" {
		t.Errorf("off english: %q %q", n, src)
	}

	// Receipt lines get an English gloss; shopping list links from English text.
	shop := e.shop("AH")
	r := e.receipt(shop.Id, 1, "01-10-2026|", "AH HALFV MELK|1,29", "KIPFILET|5,49", "TOTAAL|6,78")
	lines := *r.Lines
	if lines[0].TextEn == nil || *lines[0].TextEn != "Ah semi-skimmed milk" {
		t.Errorf("text_en: %v", lines[0].TextEn)
	}
	item := decodeAs[apitypes.ListItem](t, e.do("POST", "/v1/list", map[string]any{"text": "chicken breast"}))
	if item.ProductId == nil || *item.ProductId != chicken.Id {
		t.Errorf("list item from English text not linked: %+v", item)
	}
}
