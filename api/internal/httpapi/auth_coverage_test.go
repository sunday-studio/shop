package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every documented API route except the pairing ones must refuse a request without a token, and refuse a
// wrong token. The routes come from openapi.yaml, so a new route is covered without touching this test.
func TestEveryRouteRequiresAToken(t *testing.T) {
	e := newEnv(t)
	b, err := os.ReadFile("../../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	public := map[string]bool{"/healthz": true, "/version": true, "/v1/auth/status": true, "/v1/auth/pair": true, "/v1/server-info": true,
		// Public routes of the hosted service; on a single-household server they answer 404 (hosted_only).
		"/v1/auth/apple/nonce": true, "/v1/auth/apple": true, "/v1/auth/apple/events": true,
		"/v1/billing/apple/notifications": true, "/v1/billing/plan-states": true}
	hostedPublicOps := map[string]bool{"get /v1/household/invites/{id}": true}
	pathRe := regexp.MustCompile(`^  (/[^:\s]+):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):`)
	idRe := regexp.MustCompile(`\{[^}]+\}`)
	cur, n := "", 0
	for _, line := range strings.Split(string(b), "\n") {
		if m := pathRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if m := methodRe.FindStringSubmatch(line); m != nil && cur != "" && !public[cur] && !hostedPublicOps[m[1]+" "+cur] {
			path := idRe.ReplaceAllString(cur, "1")
			for _, auth := range []string{"", "Bearer not-a-real-token"} {
				req := httptest.NewRequest(strings.ToUpper(m[1]), path, nil)
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				rec := httptest.NewRecorder()
				e.h.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("%s %s with %q: %d, want 401", m[1], path, auth, rec.Code)
				}
			}
			n++
		}
	}
	if n < 60 {
		t.Errorf("only %d routes checked; is openapi.yaml being parsed?", n)
	}
}

// The routes of the hosted service answer with the contract's 404 codes on a single-household server.
func TestHostedRoutesOnSingleServer(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ method, path, code string }{
		{"POST", "/v1/auth/apple/nonce", "login_method_unavailable"},
		{"POST", "/v1/auth/apple", "login_method_unavailable"},
		{"POST", "/v1/auth/apple/events", "login_method_unavailable"},
		{"POST", "/v1/billing/apple/notifications", "hosted_only"},
		{"GET", "/v1/billing/plan-states", "hosted_only"},
		{"GET", "/v1/household/invites/ABCD-ABCD-ABCD", "hosted_only"},
	} {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	for _, p := range []string{"/v1/account", "/v1/account/export", "/v1/household", "/v1/household/leave", "/v1/billing/apple/refresh"} {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 401 {
			t.Errorf("%s without a token: %d, want 401", p, rec.Code)
		}
		rec = httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.Header.Set("Authorization", "Bearer "+e.token)
		e.h.ServeHTTP(rec, req)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"hosted_only"`) {
			t.Errorf("%s with a token: %d %s", p, rec.Code, rec.Body)
		}
	}
}
