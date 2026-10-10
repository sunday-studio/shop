package openfoodfacts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLookup(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("User-Agent") != "Pantry/test" {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/api/v2/product/8710400000000.json":
			_, _ = w.Write([]byte(`{"status":1,"product":{
				"product_name":"Semi-skimmed milk","product_name_nl":"Halfvolle melk",
				"brands":"AH, Albert Heijn","product_quantity":"1.5","product_quantity_unit":"l",
				"nutrition_data_per":"100g",
				"nutriments":{"energy-kcal_100g":46,"fat_100g":"1.5","proteins_100g":3.5,"salt_100g":0.1,"carbohydrates_100g":4.7},
				"image_front_url":"https://images.openfoodfacts.org/x.jpg"}}`))
		default:
			_, _ = w.Write([]byte(`{"status":0,"status_verbose":"product not found"}`))
		}
	}))
	defer srv.Close()
	c := New("Pantry/test").WithBaseURL(srv.URL)

	p, err := c.Lookup(context.Background(), "8710400000000")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Halfvolle melk" || p.Brand != "AH" {
		t.Errorf("name/brand = %q / %q", p.Name, p.Brand)
	}
	if p.NetQuantity == nil || *p.NetQuantity != 1500 || p.NetUnit != "ml" {
		t.Errorf("quantity = %v %s", p.NetQuantity, p.NetUnit)
	}
	if p.Nutrition == nil || p.Nutrition.Per != "100ml" || *p.Nutrition.Fat != 1.5 || *p.Nutrition.EnergyKcal != 46 {
		t.Errorf("nutrition = %+v", p.Nutrition)
	}

	// Not found is cached.
	for range 2 {
		if _, err := c.Lookup(context.Background(), "12345678"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2 (negative cache)", calls.Load())
	}
	if _, err := c.Lookup(context.Background(), "abc"); err == nil {
		t.Error("invalid barcode accepted")
	}
}

func TestAllowedImageURL(t *testing.T) {
	ok := []string{"https://images.openfoodfacts.org/images/products/871/front.jpg", "https://openfoodfacts.org/x.png"}
	bad := []string{"http://images.openfoodfacts.org/x.jpg", "https://evil.com/openfoodfacts.org", "https://openfoodfacts.org.evil.com/x", "file:///etc/passwd", "https://169.254.169.254/"}
	for _, u := range ok {
		if !AllowedImageURL(u) {
			t.Errorf("%s should be allowed", u)
		}
	}
	for _, u := range bad {
		if AllowedImageURL(u) {
			t.Errorf("%s should be rejected", u)
		}
	}
}
