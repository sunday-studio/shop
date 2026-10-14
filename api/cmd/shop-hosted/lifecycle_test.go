package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shop/internal/hosted/control"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedapi"
	"shop/internal/hosted/lifecycle"
)

type cli struct {
	t      *testing.T
	base   string
	admin  string
	client *http.Client
}

func (c *cli) call(base, method, path, token string, body any, hdr ...string) (int, map[string]any, http.Header) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m, resp.Header
}

func (c *cli) pub(method, path, token string, body any, hdr ...string) (int, map[string]any) {
	s, m, _ := c.call(c.base, method, path, token, body, hdr...)
	return s, m
}
func (c *cli) adm(method, path string, body any) (int, map[string]any) {
	s, m, _ := c.call(c.admin, method, path, "sa_dev", body, "X-Admin-Operator", "test")
	return s, m
}

// The whole lifecycle over real HTTP with the fake Apple of SHOP_APPLE_MODE=fake and a fake clock.
func TestLifecycleOverHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHOP_MODE", "hosted")
	t.Setenv("SHOP_DATA_DIR", dir)
	t.Setenv("SHOP_APPLE_MODE", "fake")
	t.Setenv("SHOP_APPLE_FAKE_ACK", "i-know-this-is-a-test")
	t.Setenv("SHOP_APPLE_ENVIRONMENT", "sandbox")
	t.Setenv("ADMIN_TOKEN_HASH", hostedapi.HashAdminToken("sa_dev"))
	t.Setenv("OCR_URL", "http://127.0.0.1:1")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	clock := hclock.NewFake(time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC))
	s, err := newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), clock, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	ts := httptest.NewServer(s.API.Handler())
	t.Cleanup(ts.Close)
	as := httptest.NewServer(s.API.AdminHandler(cfg.AdminHash))
	t.Cleanup(as.Close)
	c := &cli{t: t, base: ts.URL, admin: as.URL, client: &http.Client{Timeout: 20 * time.Second}}
	ctx := context.Background()

	// sign in with an identity token from the fake Apple
	st, n := c.pub("POST", "/v1/auth/apple/nonce", "", nil)
	if st != 201 {
		t.Fatal(st, n)
	}
	nonce := n["nonce"].(string)
	st, it := c.adm("POST", "/admin/v1/dev/identity-token", map[string]any{"sub": "dev-ama", "nonce": nonce})
	if st != 200 {
		t.Fatal(st, it)
	}
	st, si := c.pub("POST", "/v1/auth/apple", "", map[string]any{"identity_token": it["identity_token"], "authorization_code": it["authorization_code"], "nonce": nonce, "device_name": "Dev iPhone"})
	if st != 201 {
		t.Fatal(st, si)
	}
	tok := si["token"].(string)
	acc := si["account"].(map[string]any)

	// trial: a signed transaction from the fake App Store, posted by the "app"
	st, tx := c.adm("POST", "/admin/v1/dev/transaction", map[string]any{"app_account_token": acc["app_account_token"], "original_transaction_id": "otx-dev-1", "trial": true})
	if st != 200 {
		t.Fatal(st, tx)
	}
	st, b := c.pub("POST", "/v1/billing/apple/transactions", tok, map[string]any{"signed_transaction": tx["signed_transaction"]})
	if st != 200 || b["result"] != "bound" {
		t.Fatal(st, b)
	}
	state := func() string {
		_, a := c.pub("GET", "/v1/account", tok, nil)
		return a["plan"].(map[string]any)["state"].(string)
	}
	if state() != "trial" {
		t.Fatal(state())
	}
	if st, _ := c.pub("POST", "/v1/shops", tok, map[string]any{"name": "AH"}); st != 201 {
		t.Fatal(st)
	}

	// day 14: the first charge arrives as a signed notification; a replay is harmless
	clock.Advance(14 * 24 * time.Hour)
	st, nt := c.adm("POST", "/admin/v1/dev/notification", map[string]any{"type": "DID_RENEW", "uuid": "n-renew-1", "app_account_token": acc["app_account_token"], "original_transaction_id": "otx-dev-1", "trial": false, "days": 30})
	if st != 200 {
		t.Fatal(st, nt)
	}
	for i := 0; i < 2; i++ {
		if st, _ := c.pub("POST", "/v1/billing/apple/notifications", "", map[string]any{"signedPayload": nt["signedPayload"]}); st != 200 {
			t.Fatal(st)
		}
	}
	if state() != "active" {
		t.Fatal(state())
	}

	// the subscription expires: read-only, export still works
	clock.Advance(31 * 24 * time.Hour)
	_, nt = c.adm("POST", "/admin/v1/dev/notification", map[string]any{"type": "EXPIRED", "subtype": "VOLUNTARY", "uuid": "n-exp-1", "app_account_token": acc["app_account_token"], "original_transaction_id": "otx-dev-1", "auto_renew": false})
	if st, _ := c.pub("POST", "/v1/billing/apple/notifications", "", map[string]any{"signedPayload": nt["signedPayload"]}); st != 200 {
		t.Fatal(st)
	}
	if state() != "lapsed_read_only" {
		t.Fatal(state())
	}
	if st, p := c.pub("POST", "/v1/shops", tok, map[string]any{"name": "Lidl"}); st != 403 || p["code"] != "read_only" {
		t.Fatal(st, p)
	}
	if st, _ := c.pub("GET", "/v1/shops", tok, nil); st != 200 {
		t.Fatal(st)
	}
	st, ex := c.pub("POST", "/v1/account/export", tok, nil)
	if st != 202 {
		t.Fatal(st, ex)
	}
	s.API.RunExports(ctx)
	_, ex = c.pub("GET", "/v1/account/export", tok, nil)
	if ex["status"] != "ready" {
		t.Fatal(ex)
	}
	req, _ := http.NewRequest("GET", ts.URL+ex["download_url"].(string), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.client.Do(req)
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatal(err, resp)
	}
	resp.Body.Close()

	// day 30 of the lapse: pending deletion; the scheduler tombstones, then erases after 24 h
	hh, _, _ := s.Store.HouseholdOfAccount(ctx, acc["id"].(string))
	hhDir := filepath.Join(dir, "households", hh.ID)
	if _, err := os.Stat(hhDir); err != nil {
		t.Fatal("household folder missing", err)
	}
	clock.Advance(31 * 24 * time.Hour)
	if state() != "pending_deletion" {
		t.Fatal(state())
	}
	if ran, err := s.Sched.RunOnce(ctx); err != nil || !ran {
		t.Fatal(ran, err)
	}
	clock.Advance(25 * time.Hour)
	// the lapse path needs a fresh Apple status that agrees (the fake says "expired")
	if _, err := s.Sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hhDir); !os.IsNotExist(err) {
		h, _ := s.Store.HouseholdByID(ctx, hh.ID)
		t.Fatalf("household folder still there (status %s): %v", h.Status, err)
	}
	if h, _ := s.Store.HouseholdByID(ctx, hh.ID); h.Status != control.HouseholdDeleted {
		t.Fatal(h.Status)
	}
	if state() != "deleted" {
		t.Fatal(state())
	}
	if st, p := c.pub("GET", "/v1/shops", tok, nil); st != 403 || p["code"] != "plan_required" {
		t.Fatal(st, p)
	}
	// the account row stays; a new subscription creates an empty household
	_, tx = c.adm("POST", "/admin/v1/dev/transaction", map[string]any{"app_account_token": acc["app_account_token"], "original_transaction_id": "otx-dev-2", "trial": false})
	if st, b := c.pub("POST", "/v1/billing/apple/transactions", tok, map[string]any{"signed_transaction": tx["signed_transaction"]}); st != 200 {
		t.Fatal(st, b)
	}
	if st, sh := c.pub("GET", "/v1/shops", tok, nil); st != 200 || strings.Contains(toJSON(sh), "AH") {
		t.Fatal(st, sh)
	}
	_ = lifecycle.ActErase
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestConfigRefusals(t *testing.T) {
	t.Setenv("SHOP_DATA_DIR", t.TempDir())
	t.Setenv("SHOP_MODE", "single")
	if _, err := loadConfig(); err == nil {
		t.Fatal("SHOP_MODE=single accepted")
	}
	t.Setenv("SHOP_MODE", "hosted")
	t.Setenv("ADMIN_ADDR", "0.0.0.0:8081")
	if _, err := loadConfig(); err == nil {
		t.Fatal("non-loopback admin address accepted")
	}
	t.Setenv("ADMIN_ADDR", "127.0.0.1:8081")
	t.Setenv("SHOP_APPLE_MODE", "fake")
	t.Setenv("SHOP_APPLE_ENVIRONMENT", "production")
	if _, err := loadConfig(); err == nil {
		t.Fatal("fake Apple in production accepted")
	}
	t.Setenv("SHOP_APPLE_MODE", "live")
	t.Setenv("SHOP_APPLE_ROOT_CA_FILE", "/tmp/x.pem")
	if _, err := loadConfig(); err == nil {
		t.Fatal("test root in production accepted")
	}
	t.Setenv("SHOP_APPLE_ROOT_CA_FILE", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	// production without Apple keys must not start
	if _, err := newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), hclock.System{}, false); err == nil {
		t.Fatal("production started without Apple keys")
	}
}
