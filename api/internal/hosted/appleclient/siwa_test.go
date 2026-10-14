package appleclient_test

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/appleclient"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
)

type siwaEnv struct {
	key   *ecdsa.PrivateKey
	rec   *rec
	srv   *httptest.Server
	mu    sync.Mutex
	h     http.HandlerFunc
	reqs  []url.Values
	paths []string
	ctype []string
	c     *appleclient.SIWAClient
}

func newSIWA(t *testing.T) *siwaEnv {
	e := &siwaEnv{key: hostedtest.NewKey(), rec: &rec{}}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		e.mu.Lock()
		e.reqs = append(e.reqs, r.PostForm)
		e.paths = append(e.paths, r.Method+" "+r.URL.Path)
		e.ctype = append(e.ctype, r.Header.Get("Content-Type"))
		e.mu.Unlock()
		e.h(w, r)
	}))
	t.Cleanup(e.srv.Close)
	c, err := appleclient.NewSIWAClient(appleclient.SIWAConfig{TeamID: "TEAM123456", KeyID: "SIWAKEY001", ClientID: bundle, Key: e.key,
		BaseURL: e.srv.URL, Clock: hclock.NewFake(t0), Options: e.rec.options()})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	return e
}

func (e *siwaEnv) checkSecret(t *testing.T, form url.Values) {
	t.Helper()
	hdr, claims := parseJWT(t, form.Get("client_secret"), &e.key.PublicKey)
	if hdr["alg"] != "ES256" || hdr["kid"] != "SIWAKEY001" {
		t.Errorf("header %v", hdr)
	}
	if claims["iss"] != "TEAM123456" || claims["sub"] != bundle || claims["aud"] != "https://appleid.apple.com" {
		t.Errorf("claims %v", claims)
	}
	if exp, iat := int64(claims["exp"].(float64)), int64(claims["iat"].(float64)); iat != t0.Unix() || exp-iat != 300 {
		t.Errorf("iat %d exp %d (short expiry expected)", iat, exp)
	}
	if form.Get("client_id") != bundle {
		t.Errorf("client_id %q", form.Get("client_id"))
	}
}

func TestExchange(t *testing.T) {
	e := newSIWA(t)
	e.h = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"access_token": "a", "refresh_token": "r-123", "id_token": "i", "expires_in": 3600})
	}
	rt, err := e.c.Exchange(t.Context(), "code-abc")
	if err != nil || rt != "r-123" {
		t.Fatalf("%v %q", err, rt)
	}
	f := e.reqs[0]
	if e.paths[0] != "POST /auth/token" || e.ctype[0] != "application/x-www-form-urlencoded" || f.Get("grant_type") != "authorization_code" || f.Get("code") != "code-abc" {
		t.Errorf("request: %v %v %v", e.paths[0], e.ctype[0], f)
	}
	e.checkSecret(t, f)
}

func TestExchangeErrors(t *testing.T) {
	e := newSIWA(t)
	reply := func(status int, body map[string]any) {
		e.h = func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, status, body) }
	}
	reply(400, map[string]any{"error": "invalid_grant", "error_description": "The code has expired or has been revoked."})
	if _, err := e.c.Exchange(t.Context(), "c"); !errors.Is(err, appleauth.ErrCodeInvalid) {
		t.Errorf("invalid_grant: %v", err)
	}
	reply(400, map[string]any{"error": "invalid_client"})
	if _, err := e.c.Exchange(t.Context(), "c"); !errors.Is(err, appleauth.ErrAppleUnavailable) || errors.Is(err, appleauth.ErrCodeInvalid) || !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("invalid_client: %v", err)
	}
	reply(200, map[string]any{"access_token": "a"})
	if _, err := e.c.Exchange(t.Context(), "c"); !errors.Is(err, appleauth.ErrAppleUnavailable) {
		t.Errorf("no refresh token: %v", err)
	}
	n := len(e.reqs)
	reply(500, map[string]any{})
	if _, err := e.c.Exchange(t.Context(), "c"); !errors.Is(err, appleauth.ErrAppleUnavailable) || len(e.reqs)-n != 1 {
		t.Errorf("5xx must not retry a single-use code: %v (%d requests)", err, len(e.reqs)-n)
	}
	e.srv.Close()
	if _, err := e.c.Exchange(t.Context(), "c"); !errors.Is(err, appleauth.ErrAppleUnavailable) {
		t.Errorf("network: %v", err)
	}
}

func TestRevoke(t *testing.T) {
	e := newSIWA(t)
	e.h = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }
	if err := e.c.Revoke(t.Context(), "r-123"); err != nil {
		t.Fatal(err)
	}
	f := e.reqs[0]
	if e.paths[0] != "POST /auth/revoke" || f.Get("token") != "r-123" || f.Get("token_type_hint") != "refresh_token" {
		t.Errorf("request: %v %v", e.paths[0], f)
	}
	e.checkSecret(t, f)

	// already revoked counts as done
	e.h = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
	}
	if err := e.c.Revoke(t.Context(), "r"); err != nil {
		t.Errorf("invalid_grant: %v", err)
	}
	// 503 twice then ok: retried
	n := 0
	e.h = func(w http.ResponseWriter, _ *http.Request) {
		if n++; n < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}
	if err := e.c.Revoke(t.Context(), "r"); err != nil || n != 3 {
		t.Errorf("retry: %v n=%d", err, n)
	}
	// permanent failure surfaces as unavailable
	e.h = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 400, map[string]any{"error": "invalid_client"})
	}
	if err := e.c.Revoke(t.Context(), "r"); !errors.Is(err, appleauth.ErrAppleUnavailable) {
		t.Errorf("invalid_client: %v", err)
	}
	// the refresh token is never logged
	if strings.Contains(e.rec.Logs.String(), "r-123") || strings.Contains(e.rec.Logs.String(), "client_secret") {
		t.Errorf("logs: %s", e.rec.Logs.String())
	}
}

func TestSIWAConfigValidation(t *testing.T) {
	if _, err := appleclient.NewSIWAClient(appleclient.SIWAConfig{TeamID: "t"}); err == nil {
		t.Error("incomplete config accepted")
	}
}

func TestKeyLoading(t *testing.T) {
	k := hostedtest.NewKey()
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	got, err := appleclient.ParseECPrivateKeyPEM(p8)
	if err != nil || !got.Equal(k) {
		t.Fatalf("pkcs8: %v", err)
	}
	sec1, _ := x509.MarshalECPrivateKey(k)
	if got, err = appleclient.ParseECPrivateKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1})); err != nil || !got.Equal(k) {
		t.Fatalf("sec1: %v", err)
	}
	// file
	dir := t.TempDir()
	f := filepath.Join(dir, "AuthKey.p8")
	os.WriteFile(f, p8, 0o600)
	t.Setenv("T_KEY", "")
	t.Setenv("T_KEY_FILE", f)
	if got, err = appleclient.LoadKeyFromEnv("T_KEY"); err != nil || !got.Equal(k) {
		t.Fatalf("file: %v", err)
	}
	// env with escaped newlines
	t.Setenv("T_KEY", strings.ReplaceAll(strings.TrimSpace(string(p8)), "\n", `\n`))
	if got, err = appleclient.LoadKeyFromEnv("T_KEY"); err != nil || !got.Equal(k) {
		t.Fatalf("env: %v", err)
	}
	// failures never echo the input
	t.Setenv("T_KEY", "-----BEGIN PRIVATE KEY-----\nSUPERSECRET\n-----END PRIVATE KEY-----")
	if _, err = appleclient.LoadKeyFromEnv("T_KEY"); err == nil || strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("bad key: %v", err)
	}
	t.Setenv("T_KEY", "")
	t.Setenv("T_KEY_FILE", filepath.Join(dir, "missing"))
	if _, err = appleclient.LoadKeyFromEnv("T_KEY"); err == nil {
		t.Error("missing file accepted")
	}
	t.Setenv("T_KEY_FILE", "")
	if _, err = appleclient.LoadKeyFromEnv("T_KEY"); err == nil {
		t.Error("unset accepted")
	}
	// RSA key refused
	if _, err = appleclient.ParseECPrivateKeyPEM([]byte("junk")); err == nil {
		t.Error("junk accepted")
	}
}

var _ = time.Second
