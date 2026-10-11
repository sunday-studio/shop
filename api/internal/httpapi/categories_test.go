package httpapi_test

import (
	"fmt"
	"strings"
	"testing"

	"shop/internal/apitypes"
)

func listCats(t *testing.T, e *env) []apitypes.Category {
	t.Helper()
	return decodeAs[[]apitypes.Category](t, e.do("GET", "/v1/categories", nil))
}

func newProductIn(e *env, name string, cat int64) apitypes.Product {
	return e.product(map[string]any{"name": name, "category_id": cat})
}

func TestCategoriesAlphabeticalWithCountsAndColors(t *testing.T) {
	e := newEnv(t)
	cats := listCats(t, e)
	for i := 1; i < len(cats); i++ {
		if strings.ToLower(cats[i-1].Name) > strings.ToLower(cats[i].Name) {
			t.Errorf("not alphabetical: %q before %q", cats[i-1].Name, cats[i].Name)
		}
	}
	for _, c := range cats {
		if c.Color == nil || c.Sort != 0 {
			t.Errorf("%s: color %v sort %d; want a colour and sort 0", c.Name, c.Color, c.Sort)
		}
	}
	// a created category sorts into place whatever `sort` the old app sends, and gets a free colour
	rec := e.do("POST", "/v1/categories", map[string]any{"name": "  apple   things ", "sort": 1})
	c := decodeAs[apitypes.Category](t, rec)
	if rec.Code != 201 || c.Name != "apple things" || c.ProductCount != 0 || c.Color == nil {
		t.Fatalf("create: %d %+v", rec.Code, c)
	}
	if first := listCats(t, e)[0]; first.Id != c.Id {
		t.Errorf("first = %q; want %q", first.Name, c.Name)
	}
	// product_count counts non-deleted products
	var dairy apitypes.Category
	for _, x := range listCats(t, e) {
		if x.Name == "Dairy" {
			dairy = x
		}
	}
	p := newProductIn(e, "Melk", dairy.Id)
	newProductIn(e, "Kaas", dairy.Id)
	e.do("DELETE", fmt.Sprintf("/v1/products/%d", p.Id), nil)
	for _, x := range listCats(t, e) {
		if x.Name == "Dairy" && x.ProductCount != 1 {
			t.Errorf("Dairy product_count = %d; want 1", x.ProductCount)
		}
	}
	// bad colour
	if rec := e.do("POST", "/v1/categories", map[string]any{"name": "X", "color": "magenta"}); rec.Code != 400 {
		t.Errorf("bad colour: %d", rec.Code)
	}
}

func TestCategoryNamesAreUniqueIgnoringCase(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/v1/categories", map[string]any{"name": "dAiRy"})
	m := jsonMap(t, rec.Body.String())
	if rec.Code != 409 || m["code"] != "category_name_taken" {
		t.Fatalf("duplicate in another case: %d %v", rec.Code, m)
	}
	snacks := decodeAs[apitypes.Category](t, e.do("POST", "/v1/categories", map[string]any{"name": "Snacks", "color": "pink"}))
	if rec := e.do("POST", "/v1/categories", map[string]any{"name": "snacks"}); rec.Code != 409 {
		t.Errorf("snacks vs Snacks: %d", rec.Code)
	}
	// rename onto an existing name conflicts too
	other := decodeAs[apitypes.Category](t, e.do("POST", "/v1/categories", map[string]any{"name": "Sweets"}))
	rec = e.do("PATCH", fmt.Sprintf("/v1/categories/%d", other.Id), map[string]any{"name": " SNACKS "})
	if rec.Code != 409 || jsonMap(t, rec.Body.String())["code"] != "category_name_taken" {
		t.Errorf("rename onto taken name: %d %s", rec.Code, rec.Body)
	}
	// renaming to its own name in a different case is fine
	if rec := e.do("PATCH", fmt.Sprintf("/v1/categories/%d", other.Id), map[string]any{"name": "SWEETS"}); rec.Code != 200 {
		t.Errorf("own name, new case: %d", rec.Code)
	}

	// a deleted "Snacks" no longer blocks a new one: it is brought back (same id, colour kept)
	if rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d", snacks.Id), nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = e.do("POST", "/v1/categories", map[string]any{"name": "SNACKS"})
	back := decodeAs[apitypes.Category](t, rec)
	if rec.Code != 201 || back.Id != snacks.Id || back.Name != "SNACKS" || back.Color == nil || *back.Color != "pink" {
		t.Errorf("revive: %d %+v", rec.Code, back)
	}
	// renaming another category onto the name of a deleted one moves the dead row out of the way
	e.do("DELETE", fmt.Sprintf("/v1/categories/%d", back.Id), nil)
	rec = e.do("PATCH", fmt.Sprintf("/v1/categories/%d", other.Id), map[string]any{"name": "Snacks"})
	if rec.Code != 200 {
		t.Errorf("rename onto a deleted name: %d %s", rec.Code, rec.Body)
	}
}

func TestDeleteCategoryMovesProducts(t *testing.T) {
	e := newEnv(t)
	byName := map[string]apitypes.Category{}
	for _, c := range listCats(t, e) {
		byName[c.Name] = c
	}
	dairy, pantry := byName["Dairy"], byName["Pantry"]
	melk := newProductIn(e, "Melk", dairy.Id)
	kaas := newProductIn(e, "Kaas", dairy.Id)

	// products and no move_to: 409 with the count, nothing deleted
	rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d", dairy.Id), nil)
	m := jsonMap(t, rec.Body.String())
	if rec.Code != 409 || m["code"] != "category_has_products" || m["product_count"] != float64(2) {
		t.Fatalf("no move_to: %d %v", rec.Code, m)
	}
	if len(listCats(t, e)) != 10 {
		t.Error("a refused delete must delete nothing")
	}
	// bad targets
	for _, to := range []string{fmt.Sprint(dairy.Id), "99999", "abc", "-1"} {
		if rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d?move_to=%s", dairy.Id, to), nil); rec.Code != 400 {
			t.Errorf("move_to=%s: %d, want 400", to, rec.Code)
		}
	}
	if len(listCats(t, e)) != 10 {
		t.Error("a refused delete must delete nothing")
	}

	// move to another category
	if rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d?move_to=%d", dairy.Id, pantry.Id), nil); rec.Code != 204 {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []int64{melk.Id, kaas.Id} {
		p := decodeAs[apitypes.Product](t, e.do("GET", fmt.Sprintf("/v1/products/%d", id), nil))
		if p.CategoryId == nil || *p.CategoryId != pantry.Id || p.CategorySource == nil || *p.CategorySource != "manual" {
			t.Errorf("product %d: category %v source %v", id, p.CategoryId, p.CategorySource)
		}
	}
	for _, c := range listCats(t, e) {
		if c.Name == "Pantry" && c.ProductCount != 2 {
			t.Errorf("Pantry count = %d; want 2", c.ProductCount)
		}
		if c.Name == "Dairy" {
			t.Error("Dairy is still listed")
		}
	}

	// move_to=0: products lose their category
	bakery := byName["Bakery"]
	brood := newProductIn(e, "Brood", bakery.Id)
	if rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d?move_to=0", bakery.Id), nil); rec.Code != 204 {
		t.Fatalf("move_to=0: %d", rec.Code)
	}
	if p := decodeAs[apitypes.Product](t, e.do("GET", fmt.Sprintf("/v1/products/%d", brood.Id), nil)); p.CategoryId != nil {
		t.Errorf("category after move_to=0: %v", *p.CategoryId)
	}
	// an empty category deletes without move_to; a missing one is 404
	frozen := byName["Frozen"]
	if rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d", frozen.Id), nil); rec.Code != 204 {
		t.Errorf("empty category: %d", rec.Code)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/categories/%d", frozen.Id), nil); rec.Code != 404 {
		t.Errorf("already deleted: %d, want 404", rec.Code)
	}
}

func TestProductsByCategoryCarrySummaries(t *testing.T) {
	e := newEnv(t)
	shop := e.shop("AH")
	var dairy apitypes.Category
	for _, c := range listCats(t, e) {
		if c.Name == "Dairy" {
			dairy = c
		}
	}
	melk := newProductIn(e, "Melk", dairy.Id)
	e.ocr.rows.Store([]string{"01-10-2026 10:00|", "MELK|1,29", "TOTAAL|1,29"})
	r := decodeAs[apitypes.Receipt](t, e.upload(shop.Id, pngBytes(91), nil))
	e.drain()
	got := decodeAs[apitypes.Receipt](t, e.do("GET", fmt.Sprintf("/v1/receipts/%d", r.Id), nil))
	for _, l := range *got.Lines {
		if l.Kind == apitypes.Item {
			e.do("PUT", fmt.Sprintf("/v1/lines/%d/product", l.Id), map[string]any{"product_id": melk.Id})
		}
	}
	e.do("POST", fmt.Sprintf("/v1/receipts/%d/confirm", r.Id), nil)
	list := decodeAs[[]apitypes.Product](t, e.do("GET", fmt.Sprintf("/v1/products?category_id=%d", dairy.Id), nil))
	if len(list) != 1 || list[0].LastBoughtAt == nil || list[0].Purchases == nil || *list[0].Purchases != 1 {
		t.Fatalf("category list = %+v; want last_bought_at and purchases", list)
	}
}

func TestProductRestore(t *testing.T) {
	e := newEnv(t)
	p := e.product(map[string]any{"name": "Fanta", "barcode": "8711111111111"})
	if rec := e.do("POST", fmt.Sprintf("/v1/products/%d/restore", p.Id), nil); rec.Code != 409 {
		t.Errorf("restore a live product: %d, want 409", rec.Code)
	}
	if rec := e.do("DELETE", fmt.Sprintf("/v1/products/%d", p.Id), nil); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec := e.do("POST", fmt.Sprintf("/v1/products/%d/restore", p.Id), nil)
	back := decodeAs[apitypes.Product](t, rec)
	if rec.Code != 200 || back.Id != p.Id || back.Barcode == nil || *back.Barcode != "8711111111111" {
		t.Fatalf("restore: %d %+v", rec.Code, back)
	}
	// somebody took the barcode meanwhile: the product returns without it
	e.do("DELETE", fmt.Sprintf("/v1/products/%d", p.Id), nil)
	e.product(map[string]any{"name": "Other", "barcode": "8711111111111"})
	back = decodeAs[apitypes.Product](t, e.do("POST", fmt.Sprintf("/v1/products/%d/restore", p.Id), nil))
	if back.Barcode != nil {
		t.Errorf("barcode = %v; want none (taken)", *back.Barcode)
	}
	// too old
	e.do("DELETE", fmt.Sprintf("/v1/products/%d", p.Id), nil)
	if _, err := e.conn.Exec(`UPDATE products SET deleted_at = '2020-01-01T00:00:00Z' WHERE id = ?`, p.Id); err != nil {
		t.Fatal(err)
	}
	if rec := e.do("POST", fmt.Sprintf("/v1/products/%d/restore", p.Id), nil); rec.Code != 409 {
		t.Errorf("restore after 30 days: %d, want 409", rec.Code)
	}
	if rec := e.do("POST", "/v1/products/999999/restore", nil); rec.Code != 404 {
		t.Errorf("restore unknown: %d", rec.Code)
	}
}
