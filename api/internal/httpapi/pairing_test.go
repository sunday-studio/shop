package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shop/internal/auth"
)

func (e *env) pairReq(ip, password, device string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"password": password, "device_name": device})
	req := httptest.NewRequest("POST", "/v1/auth/pair", bytes.NewReader(b))
	req.RemoteAddr = ip + ":5555"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) asToken(token, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

type pairResp struct {
	Token  string `json:"token"`
	Device struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Current bool   `json:"current"`
	} `json:"device"`
}

func TestPairing(t *testing.T) {
	e := newEnv(t)
	status := func() map[string]any {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/auth/status", nil))
		return decodeAs[map[string]any](t, rec)
	}
	if s := status(); s["server"] != "shop" || s["pairing_enabled"] != false {
		t.Fatalf("status before setup: %v", s)
	}
	if rec := e.pairReq("10.0.0.2", "anything-long", "iPhone"); rec.Code != http.StatusConflict {
		t.Fatalf("pair without password: %d", rec.Code)
	}

	if err := auth.SetPassword(context.Background(), e.q, "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	pw := auth.GeneratePassphrase()
	if parts := strings.Split(pw, "-"); len(parts) != 5 {
		t.Fatalf("passphrase %q", pw)
	}
	if err := auth.SetPassword(context.Background(), e.q, pw); err != nil {
		t.Fatal(err)
	}
	if s := status(); s["pairing_enabled"] != true {
		t.Fatalf("status after setup: %v", s)
	}

	// Wrong password → 401, and after 5 failures the address is locked out.
	for i := range 5 {
		if rec := e.pairReq("10.0.0.9", "wrong-password", "x"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	if rec := e.pairReq("10.0.0.9", pw, "x"); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("locked out: %d %v", rec.Code, rec.Header())
	}

	// Another address with the right password pairs fine.
	rec := e.pairReq("10.0.0.2", pw, "Cas's iPhone")
	if rec.Code != http.StatusCreated {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}
	phone := decodeAs[pairResp](t, rec)
	if !strings.HasPrefix(phone.Token, "st_") || phone.Device.Name != "Cas's iPhone" {
		t.Fatalf("pair response: %+v", phone)
	}
	// The issued token works on the API.
	if me := decodeAs[map[string]any](t, e.asToken(phone.Token, "GET", "/v1/auth/me")); me["kind"] != "device" {
		t.Fatalf("me: %v", me)
	}
	if rec := e.asToken(phone.Token, "GET", "/v1/shops"); rec.Code != 200 {
		t.Fatalf("api with device token: %d", rec.Code)
	}

	// Same device name gets a suffix; the list marks the caller.
	tablet := decodeAs[pairResp](t, e.pairReq("10.0.0.3", pw, "Cas's iPhone"))
	if tablet.Device.Name != "Cas's iPhone (2)" {
		t.Errorf("dedup name: %q", tablet.Device.Name)
	}
	devs := decodeAs[[]struct {
		ID      int64 `json:"id"`
		Current bool  `json:"current"`
	}](t, e.asToken(phone.Token, "GET", "/v1/auth/devices"))
	if len(devs) != 2 || !devs[0].Current || devs[1].Current {
		t.Errorf("devices: %+v", devs)
	}

	// Revoking a device kills its token; CLI tokens can't be revoked over the API.
	if rec := e.asToken(phone.Token, "DELETE", fmt.Sprintf("/v1/auth/devices/%d", tablet.Device.ID)); rec.Code != 204 {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec := e.asToken(tablet.Token, "GET", "/v1/shops"); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still works: %d", rec.Code)
	}
	var cliID int64
	_ = e.conn.QueryRow(`SELECT id FROM api_tokens WHERE kind = 'manual'`).Scan(&cliID)
	if rec := e.asToken(phone.Token, "DELETE", fmt.Sprintf("/v1/auth/devices/%d", cliID)); rec.Code != 404 {
		t.Errorf("revoke manual token via API: %d", rec.Code)
	}
}
