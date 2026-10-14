package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"shop/internal/auth"
	"shop/internal/version"
)

func TestServerInfoSingleMode(t *testing.T) {
	e := newEnv(t)
	get := func() (int, map[string]any, string) {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/server-info", nil)) // no token: it is public
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m, rec.Header().Get("Cache-Control")
	}
	code, m, cache := get()
	if code != 200 || cache != "public, max-age=60" {
		t.Fatalf("%d cache=%q", code, cache)
	}
	if m["server"] != "shop" || m["mode"] != "single" || m["api_level"].(float64) != float64(version.APILevel) || m["min_app_build"].(float64) != 1 {
		t.Errorf("body: %v", m)
	}
	// No pairing password yet: no login method.
	if lm := m["login_methods"].([]any); len(lm) != 0 || m["pairing_enabled"] != false {
		t.Errorf("before a password is set: %v %v", m["login_methods"], m["pairing_enabled"])
	}
	f := m["features"].(map[string]any)
	if f["households"] != false || f["billing"] != false || f["mcp"] != true {
		t.Errorf("features: %v", f)
	}
	lim := m["limits"].(map[string]any)
	if lim["max_upload_bytes"].(float64) != 20971520 {
		t.Errorf("limits: %v", lim)
	}
	if v, ok := lim["receipts_per_month"]; !ok || v != nil {
		t.Errorf("an unset limit must be null, got %v (present %v)", v, ok)
	}
	if _, ok := m["billing"]; ok {
		t.Error("billing is hosted only")
	}

	// With a pairing password the password login is offered, like /v1/auth/status says.
	if err := auth.SetPassword(context.Background(), e.q, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	_, m, _ = get()
	if lm := m["login_methods"].([]any); len(lm) != 1 || lm[0] != "pairing_password" || m["pairing_enabled"] != true {
		t.Errorf("after a password is set: %v %v", m["login_methods"], m["pairing_enabled"])
	}
}

// api_level and the document version move together with openapi.yaml: a changed contract needs both.
func TestServerInfoMatchesSpec(t *testing.T) {
	b, err := os.ReadFile("../../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^  /v1/server-info:\n    get:`).Match(b) {
		t.Error("openapi.yaml has no GET /v1/server-info")
	}
	if !regexp.MustCompile(`(?m)^    ServerInfo:\n`).Match(b) {
		t.Error("openapi.yaml has no ServerInfo schema")
	}
	if version.APILevel < 8 {
		t.Errorf("APILevel %d: the server-info contract is level 8", version.APILevel)
	}
}
