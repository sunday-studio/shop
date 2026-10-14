//go:build isolation

package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shop/internal/auth"
	"shop/internal/config"
	"shop/internal/store/db"
	"shop/internal/tenancy"
	"shop/internal/tenantapp"
)

// bootSharedDatabaseWorld is what the code did before the tenant seam: one server, one database, and two
// tokens in it. "Household A" and "household B" are two device tokens of the same data. Run it with
// ISOLATION_WORLD=shared to see that every isolation check fails on such a system.
func bootSharedDatabaseWorld(t *testing.T) *world {
	t.Helper()
	e := newEnv(t)
	tokB, err := auth.Create(context.Background(), e.q, "household-b")
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ocr: e.ocr, mode: "shared database: two tokens, one database"}
	mk := func(name, marker, token string) *household {
		return &household{name: name, marker: marker, token: token, w: w, conn: e.conn, handler: e.h, drain: e.drain, imagesDir: e.dataDir + "/images"}
	}
	w.a = mk("A", "xalpha", e.token)
	w.b = mk("B", "xbeta", tokB)
	return w
}

// bootMultiTenantWorld builds two real households behind one gateway: one SQLite file and one image
// folder each, tokens resolved through a directory.
func bootMultiTenantWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	f := newFakeOCR()
	t.Cleanup(f.srv.Close)
	foodFacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/product/8710400000017.json" {
			_, _ = w.Write([]byte(`{"status":1,"product":{"product_name_nl":"Halfvolle melk","brands":"AH"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	t.Cleanup(foodFacts.Close)
	logoDev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nologo.nl" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("PNG:" + strings.TrimPrefix(r.URL.Path, "/")))
	}))
	t.Cleanup(logoDev.Close)

	ams, _ := time.LoadLocation("Europe/Amsterdam")
	cfg := config.Config{
		DataDir: t.TempDir(), OCRURL: f.srv.URL, FoodFactsBaseURL: foodFacts.URL, FoodFactsUserAgent: "test",
		LogoDevToken: "pk_test", AutoProducts: true, Location: ams, Workers: 1,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := tenantapp.NewRuntime(cfg, log)
	rt.LogoBaseURL = logoDev.URL
	rt.Backoff = func(int64) time.Duration { return 0 }

	multi, err := tenancy.NewMulti(tenancy.MultiConfig{Root: t.TempDir(), BackupRoot: t.TempDir(), MaxOpen: 8, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { multi.Close() })
	dir := tenancy.NewMemoryDirectory()
	handler := rt.Gateway(multi, tenancy.DirectoryResolver{Dir: dir}).Handler()

	w := &world{t: t, ocr: f, mode: "multi-tenant: one database and one image folder per household"}
	pins := map[string]func(){}
	mk := func(name, marker, id string) *household {
		if err := multi.Provision(ctx, id); err != nil {
			t.Fatal(err)
		}
		tn, release, err := multi.Acquire(ctx, id) // pinned for the test, so it is never evicted
		if err != nil {
			t.Fatal(err)
		}
		pins[id] = release
		tok, err := auth.Create(ctx, db.New(tn.DB), "test")
		if err != nil {
			t.Fatal(err)
		}
		dir.Register(auth.Hash(tok), id)
		in, err := rt.Instance(ctx, tn)
		if err != nil {
			t.Fatal(err)
		}
		return &household{name: name, marker: marker, token: tok, w: w, conn: tn.DB, handler: handler,
			imagesDir: tn.ImagesDir, drain: func() {
				for in.Queue.RunOne(ctx) {
				}
			}}
	}
	w.a = mk("A", "xalpha", "house-a")
	w.b = mk("B", "xbeta", "house-b")
	w.erase = func(h *household) error {
		id := map[string]string{"A": "house-a", "B": "house-b"}[h.name]
		if rel := pins[id]; rel != nil {
			rel() // the test's own pin
			delete(pins, id)
		}
		err := multi.Erase(ctx, id)
		dir.ForgetHousehold(id)
		return err
	}
	return w
}
