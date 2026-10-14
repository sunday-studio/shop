package appleclient_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"shop/internal/hosted/appleauth"
	"shop/internal/hosted/appleclient"
	"shop/internal/hosted/hclock"
	"shop/internal/hosted/hostedtest"
)

func TestJWKSFetcher(t *testing.T) {
	idp := hostedtest.NewFakeIDP()
	var hits atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(502)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/auth/keys" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		doc, _ := idp.FetchJWKS(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.Write(doc)
	}))
	defer srv.Close()
	clk := hclock.NewFake(t0)
	r := &rec{}
	f := &appleclient.JWKSFetcher{URL: srv.URL + "/auth/keys", Clock: clk, Options: r.options(), MinInterval: 10 * time.Second, MaxStale: time.Hour}

	if _, err := f.FetchJWKS(t.Context()); err != nil || hits.Load() != 1 {
		t.Fatalf("first fetch: %v hits=%d", err, hits.Load())
	}
	// within the rate-limit window: memory
	clk.Advance(5 * time.Second)
	f.FetchJWKS(t.Context())
	if hits.Load() != 1 {
		t.Errorf("rate limit: %d hits", hits.Load())
	}
	// after the window: network again
	clk.Advance(6 * time.Second)
	if _, err := f.FetchJWKS(t.Context()); err != nil || hits.Load() != 2 {
		t.Fatalf("refetch: %v hits=%d", err, hits.Load())
	}
	// Apple down: stale document served for MaxStale, then an error
	fail.Store(true)
	clk.Advance(30 * time.Second)
	if doc, err := f.FetchJWKS(t.Context()); err != nil || len(doc) == 0 {
		t.Fatalf("stale: %v", err)
	}
	clk.Advance(2 * time.Hour)
	if _, err := f.FetchJWKS(t.Context()); err == nil {
		t.Error("stale document served beyond MaxStale")
	}
	// never cached: a fresh fetcher fails on a bad document
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"keys":[]}`)) }))
	defer bad.Close()
	if _, err := (&appleclient.JWKSFetcher{URL: bad.URL, Options: r.options()}).FetchJWKS(t.Context()); err == nil {
		t.Error("empty key set accepted")
	}
}

// With appleauth.KeySet on top: unknown kid triggers one refresh and then resolves.
func TestJWKSWithKeySet(t *testing.T) {
	idp := hostedtest.NewFakeIDP()
	idp.Hidden["ec-1"] = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, _ := idp.FetchJWKS(r.Context())
		w.Write(doc)
	}))
	defer srv.Close()
	clk := hclock.NewFake(t0)
	r := &rec{}
	ks := &appleauth.KeySet{Fetcher: &appleclient.JWKSFetcher{URL: srv.URL, Clock: clk, Options: r.options()}, Clock: clk}
	if _, err := ks.Key(t.Context(), "rsa-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Key(t.Context(), "ec-1"); err == nil {
		t.Fatal("hidden kid resolved")
	}
	delete(idp.Hidden, "ec-1") // Apple rotates in a new key
	clk.Advance(2 * time.Minute)
	if _, err := ks.Key(t.Context(), "ec-1"); err != nil {
		t.Fatalf("refresh on unknown kid: %v", err)
	}
}
