package logos

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNormaliseDomain(t *testing.T) {
	for in, want := range map[string]string{
		"ah.nl": "ah.nl", "AH.NL": "ah.nl", "https://www.ah.nl/": "ah.nl", "www.jumbo.com": "jumbo.com", "": "",
	} {
		if got, err := NormaliseDomain(in); err != nil || got != want {
			t.Errorf("NormaliseDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"localhost", "169.254.169.254", "ah", "a b.nl", "../etc/passwd"} {
		if _, err := NormaliseDomain(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestGuessDomain(t *testing.T) {
	cases := []struct{ name, chain, want string }{
		{"Albert Heijn Zuid", "", "ah.nl"},
		{"Lidl", "", "lidl.nl"},
		{"Buurtsuper", "Jumbo", "jumbo.com"},
		{"AH to go Centraal", "", "ah.nl"},
		{"Toko Dun Yong", "", ""},
		{"Plusminus", "", ""}, // not "plus"
	}
	for _, c := range cases {
		if got := GuessDomain(c.name, c.chain); got != c.want {
			t.Errorf("GuessDomain(%q, %q) = %q, want %q", c.name, c.chain, got, c.want)
		}
	}
}

func TestCache(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("token") != "pk_test" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/unknown.nl" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNG"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := New("pk_test", dir).WithBaseURL(srv.URL)

	for range 2 { // second call is served from disk
		p, err := c.Path(context.Background(), "ah.nl")
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(p); string(b) != "PNG" {
			t.Fatalf("cached file = %q", b)
		}
	}
	for range 2 { // misses are remembered
		if _, err := c.Path(context.Background(), "unknown.nl"); !errors.Is(err, ErrNoLogo) {
			t.Fatalf("unknown: %v", err)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("logo.dev calls = %d, want 2", n)
	}

	if _, err := New("", dir).Path(context.Background(), "ah.nl"); !errors.Is(err, ErrDisabled) {
		t.Errorf("no token: %v", err)
	}
	var nilCache *Cache
	if nilCache.Enabled() {
		t.Error("nil cache must be disabled")
	}

	// Network errors must not leak the token into logs.
	down := New("pk_secret", t.TempDir()).WithBaseURL("http://127.0.0.1:1")
	if _, err := down.Path(context.Background(), "jumbo.com"); err == nil || strings.Contains(err.Error(), "pk_secret") {
		t.Errorf("error leaks token or is nil: %v", err)
	}
}
