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
	public := map[string]bool{"/healthz": true, "/version": true, "/v1/auth/status": true, "/v1/auth/pair": true, "/v1/server-info": true}
	pathRe := regexp.MustCompile(`^  (/[^:\s]+):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):`)
	idRe := regexp.MustCompile(`\{[^}]+\}`)
	cur, n := "", 0
	for _, line := range strings.Split(string(b), "\n") {
		if m := pathRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if m := methodRe.FindStringSubmatch(line); m != nil && cur != "" && !public[cur] {
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
