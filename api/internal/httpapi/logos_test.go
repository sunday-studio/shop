package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"shop/internal/apitypes"
)

func TestShopLogos(t *testing.T) {
	e := newEnv(t)
	ah := e.shop("Albert Heijn Zuid") // domain guessed from the name
	if ah.Domain == nil || *ah.Domain != "ah.nl" || ah.LogoUrl == nil || *ah.LogoUrl != fmt.Sprintf("/v1/shops/%d/logo", ah.Id) {
		t.Fatalf("guessed: %+v", ah)
	}
	rec := e.do("GET", *ah.LogoUrl, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != "PNG:ah.nl" {
		t.Fatalf("logo: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}

	// Unknown shop: no domain, no logo; set one by hand (URL forms are cleaned up).
	toko := e.shop("Toko Dun Yong")
	if toko.Domain != nil || toko.LogoUrl != nil {
		t.Errorf("unknown shop: %+v", toko)
	}
	if rec := e.do("GET", fmt.Sprintf("/v1/shops/%d/logo", toko.Id), nil); rec.Code != 404 {
		t.Errorf("no domain: %d", rec.Code)
	}
	up := decodeAs[apitypes.Shop](t, e.do("PATCH", fmt.Sprintf("/v1/shops/%d", toko.Id),
		map[string]any{"name": "Toko Dun Yong", "domain": "https://www.dunyong.nl/"}))
	if up.Domain == nil || *up.Domain != "dunyong.nl" {
		t.Errorf("set domain: %+v", up.Domain)
	}
	// Renaming without a domain keeps it; "" clears it.
	up = decodeAs[apitypes.Shop](t, e.do("PATCH", fmt.Sprintf("/v1/shops/%d", toko.Id), map[string]any{"name": "Dun Yong"}))
	if up.Domain == nil || *up.Domain != "dunyong.nl" {
		t.Errorf("kept domain: %+v", up.Domain)
	}
	up = decodeAs[apitypes.Shop](t, e.do("PATCH", fmt.Sprintf("/v1/shops/%d", toko.Id), map[string]any{"name": "Dun Yong", "domain": ""}))
	if up.Domain != nil || up.LogoUrl != nil {
		t.Errorf("cleared: %+v", up)
	}
	if rec := e.do("POST", "/v1/shops", map[string]any{"name": "X", "domain": "not a domain"}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad domain: %d", rec.Code)
	}
	none := e.do("POST", "/v1/shops", map[string]any{"name": "Y", "domain": "nologo.nl"})
	if rec := e.do("GET", fmt.Sprintf("/v1/shops/%d/logo", decodeAs[apitypes.Shop](t, none).Id), nil); rec.Code != 404 {
		t.Errorf("logo.dev has none: %d", rec.Code)
	}
}
