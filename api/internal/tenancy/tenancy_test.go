package tenancy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shop/internal/auth"
	"shop/internal/store/db"
)

func newMulti(t *testing.T, max int) *Multi {
	t.Helper()
	m, err := NewMulti(MultiConfig{Root: t.TempDir(), BackupRoot: t.TempDir(), MaxOpen: max})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{"a": true, "house-1": true, "0f3a": true, "": false, "..": false, "../x": false, "a/b": false, "A": false, ".hidden": false, "a b": false} {
		if ValidID(id) != want {
			t.Errorf("ValidID(%q) = %v, want %v", id, !want, want)
		}
	}
	m := newMulti(t, 2)
	if _, _, err := m.Acquire(context.Background(), "../escape"); !errors.Is(err, ErrUnknownTenant) {
		t.Errorf("path traversal: %v", err)
	}
}

func TestMultiOpensLazilyAndMigrates(t *testing.T) {
	ctx := context.Background()
	m := newMulti(t, 4)
	if _, _, err := m.Acquire(ctx, "nobody"); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("unprovisioned household: %v", err)
	}
	if err := m.Provision(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if n := m.Open(); n != 1 {
		t.Fatalf("open = %d", n)
	}
	tn, release, err := m.Acquire(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var n int
	if err := tn.DB.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("schema not migrated / seeded: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(tn.Dir, "shop.db")); err != nil {
		t.Fatal(err)
	}
	if tn.ImagesDir != filepath.Join(tn.Dir, "images") {
		t.Errorf("images dir %q", tn.ImagesDir)
	}
	ids, _ := m.IDs(ctx)
	if len(ids) != 1 || ids[0] != "alpha" {
		t.Errorf("ids = %v", ids)
	}
}

func TestMultiHouseholdsAreSeparateDatabases(t *testing.T) {
	ctx := context.Background()
	m := newMulti(t, 4)
	for _, id := range []string{"a", "b"} {
		if err := m.Provision(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	ta, ra, _ := m.Acquire(ctx, "a")
	tb, rb, _ := m.Acquire(ctx, "b")
	defer ra()
	defer rb()
	if _, err := ta.DB.Exec(`INSERT INTO shops (name, created_at, updated_at) VALUES ('only in a', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = tb.DB.QueryRow(`SELECT COUNT(*) FROM shops`).Scan(&n)
	if n != 0 {
		t.Errorf("household b sees %d shops of a", n)
	}
}

func TestMultiEvictsLeastRecentlyUsedAndNeverInUse(t *testing.T) {
	ctx := context.Background()
	m := newMulti(t, 2)
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := m.Provision(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if n := m.Open(); n > 2 {
		t.Fatalf("open = %d after provisioning 4 with MaxOpen 2", n)
	}
	// Pin a, then touch b, c, d: a must stay open.
	ta, ra, err := m.Acquire(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	var closed atomic.Int32
	ta.OnClose(func() { closed.Add(1) })
	for _, id := range []string{"b", "c", "d"} {
		_, r, err := m.Acquire(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		r()
	}
	if err := ta.DB.PingContext(ctx); err != nil {
		t.Fatalf("a pinned tenant was closed: %v", err)
	}
	if closed.Load() != 0 {
		t.Fatal("close hooks ran for a pinned tenant")
	}
	ra() // unpinned: now it can be evicted
	_, r, _ := m.Acquire(ctx, "d")
	r()
	_, r, _ = m.Acquire(ctx, "c")
	r()
	if n := m.Open(); n > 2 {
		t.Errorf("open = %d, want <= 2", n)
	}
	if closed.Load() != 1 {
		t.Errorf("close hooks ran %d times for the evicted tenant, want 1", closed.Load())
	}
	// An evicted household opens again with its data.
	ta2, r2, err := m.Acquire(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer r2()
	if err := ta2.DB.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMultiOpensOnceUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	m := newMulti(t, 4)
	if err := m.Provision(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	m.Close() // forget it; reopen from many goroutines
	m2, _ := NewMulti(MultiConfig{Root: m.cfg.Root, MaxOpen: 4})
	t.Cleanup(func() { m2.Close() })
	var wg sync.WaitGroup
	got := make(chan *Tenant, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tn, r, err := m2.Acquire(ctx, "a")
			if err != nil {
				t.Error(err)
				return
			}
			defer r()
			got <- tn
		}()
	}
	wg.Wait()
	close(got)
	var first *Tenant
	for tn := range got {
		if first == nil {
			first = tn
		} else if tn != first {
			t.Fatal("two tenant instances for one household")
		}
	}
}

func TestMultiOneBrokenHouseholdDoesNotBlockOthers(t *testing.T) {
	ctx := context.Background()
	m := newMulti(t, 4)
	for _, id := range []string{"good", "bad"} {
		if err := m.Provision(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	if err := os.WriteFile(filepath.Join(m.Dir("bad"), "shop.db"), []byte("this is not a sqlite file at all, just text padding padding padding"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2, _ := NewMulti(MultiConfig{Root: m.cfg.Root, MaxOpen: 4})
	t.Cleanup(func() { m2.Close() })
	_, _, err := m2.Acquire(ctx, "bad")
	var ue *UnavailableError
	if !errors.As(err, &ue) || ue.ID != "bad" || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("bad household: %v", err)
	}
	if _, r, err := m2.Acquire(ctx, "good"); err != nil {
		t.Fatalf("good household blocked by the bad one: %v", err)
	} else {
		r()
	}
}

func TestMultiEraseWaitsForUsersAndRemovesEverything(t *testing.T) {
	ctx := context.Background()
	m := newMulti(t, 4)
	for _, id := range []string{"gone", "stay"} {
		if err := m.Provision(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	bdir := m.BackupDir("gone")
	_ = os.MkdirAll(bdir, 0o755)
	_ = os.WriteFile(filepath.Join(bdir, "last_backup.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(m.Dir("gone"), "images", "x.jpg"), []byte("img"), 0o644)

	tn, release, err := m.Acquire(ctx, "gone")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.Erase(ctx, "gone") }()
	select {
	case err := <-done:
		t.Fatalf("erase finished while the household was in use: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// New users are refused while the erase waits.
	if _, _, err := m.Acquire(ctx, "gone"); !errors.Is(err, ErrUnknownTenant) {
		t.Errorf("acquire during erase: %v", err)
	}
	if err := tn.DB.PingContext(ctx); err != nil {
		t.Fatalf("db closed under an in-flight user: %v", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{m.Dir("gone"), bdir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if _, _, err := m.Acquire(ctx, "gone"); !errors.Is(err, ErrUnknownTenant) {
		t.Errorf("acquire after erase: %v", err)
	}
	if _, r, err := m.Acquire(ctx, "stay"); err != nil {
		t.Errorf("other household: %v", err)
	} else {
		r()
	}
	if ids, _ := m.IDs(ctx); len(ids) != 1 || ids[0] != "stay" {
		t.Errorf("ids = %v", ids)
	}
}

func TestStateIsBuiltOnceAndDiesWithTheTenant(t *testing.T) {
	ctx := context.Background()
	m := newMulti(t, 1)
	_ = m.Provision(ctx, "a")
	_ = m.Provision(ctx, "b")
	tn, r, _ := m.Acquire(ctx, "a")
	var builds int
	build := func() (any, error) { builds++; return &builds, nil }
	tn.State("hook", func() (any, error) { tn.OnClose(func() {}); return 1, nil }) // a build may register a close hook
	v1, _ := tn.State("k", build)
	v2, _ := tn.State("k", build)
	if v1 != v2 || builds != 1 {
		t.Errorf("builds = %d", builds)
	}
	r()
	_, rb, _ := m.Acquire(ctx, "b") // evicts a
	rb()
	tn2, r2, _ := m.Acquire(ctx, "a")
	defer r2()
	tn2.State("k", build)
	if builds != 2 {
		t.Errorf("state survived eviction: builds = %d", builds)
	}
}

func TestDirectoryResolver(t *testing.T) {
	ctx := context.Background()
	d := NewMemoryDirectory()
	d.Register(auth.Hash("st_abc"), "house-a")
	r := DirectoryResolver{Dir: d}
	if id, err := r.Resolve(ctx, "st_abc"); err != nil || id != "house-a" {
		t.Errorf("known token: %q %v", id, err)
	}
	if _, err := r.Resolve(ctx, "st_other"); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("unknown token: %v", err)
	}
	if _, err := r.Resolve(ctx, ""); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("empty token: %v", err)
	}
	d.ForgetHousehold("house-a")
	if _, err := r.Resolve(ctx, "st_abc"); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("after ForgetHousehold: %v", err)
	}
}

func TestSingleProvider(t *testing.T) {
	ctx := context.Background()
	conn, err := OpenDB(ctx, filepath.Join(t.TempDir(), "shop.db"), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSingle(conn, "/data", "/data/images")
	tn, release, err := s.Acquire(ctx, DefaultID)
	if err != nil || tn.DB != conn {
		t.Fatalf("%v", err)
	}
	release()
	if _, _, err := s.Acquire(ctx, "other"); !errors.Is(err, ErrUnknownTenant) {
		t.Errorf("other id: %v", err)
	}
	if err := s.Erase(ctx, DefaultID); !errors.Is(err, ErrNotSupported) {
		t.Errorf("erase: %v", err)
	}
	if id, _ := (SingleResolver{}).Resolve(ctx, "anything"); id != DefaultID {
		t.Errorf("resolver: %q", id)
	}
	_ = db.New(conn)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
