package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shop/internal/store"
)

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	data, bdir := t.TempDir(), t.TempDir()
	dbPath := filepath.Join(data, "shop.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shops (name, created_at, updated_at) VALUES ('AH', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(data, "images", "receipts", "ab", "abc.jpg")
	_ = os.MkdirAll(filepath.Dir(img), 0o755)
	_ = os.WriteFile(img, []byte("jpeg"), 0o644)

	clock := time.Date(2026, 10, 10, 3, 30, 0, 0, time.UTC)
	o := Options{DB: db, DataDir: data, BackupDir: bdir, KeepDays: 30, Now: func() time.Time { return clock }}
	st := Run(ctx, o)
	if !st.OK || st.ImagesCopied != 1 || st.Snapshot != "shop-20261010-033000.db" {
		t.Fatalf("first run: %+v", st)
	}
	// Second run copies nothing new.
	clock = clock.Add(24 * time.Hour)
	if st := Run(ctx, o); !st.OK || st.ImagesCopied != 0 || st.ImagesTotal != 1 {
		t.Fatalf("second run: %+v", st)
	}
	// Status is mirrored into the data dir for /healthz.
	if s, err := ReadStatus(filepath.Join(data, "backup_status.json")); err != nil || !s.OK {
		t.Fatalf("status: %+v %v", s, err)
	}

	// Lose everything, then restore the latest snapshot.
	db.Close()
	_ = os.RemoveAll(data)
	_ = os.MkdirAll(data, 0o755)
	copied, err := Restore(ctx, bdir, "latest", dbPath, data)
	if err != nil || copied != 1 {
		t.Fatalf("restore: %d %v", copied, err)
	}
	restored, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var name string
	if err := restored.QueryRow(`SELECT name FROM shops`).Scan(&name); err != nil || name != "AH" {
		t.Fatalf("restored shop = %q, %v", name, err)
	}
	if b, err := os.ReadFile(img); err != nil || string(b) != "jpeg" {
		t.Fatalf("restored image: %q %v", b, err)
	}
}

func TestMissingBackupDir(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := Run(ctx, Options{DB: db, DataDir: data, BackupDir: filepath.Join(data, "nas-not-mounted")})
	if st.OK || st.Error == "" {
		t.Fatalf("expected failure: %+v", st)
	}
	if s, err := ReadStatus(filepath.Join(data, "backup_status.json")); err != nil || s.OK {
		t.Fatalf("failure must be recorded locally: %+v %v", s, err)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for _, d := range []int{0, 1, 2, 40, 50, 60} {
		name := snapshotPrefix + now.AddDate(0, 0, -d).Format(snapshotLayout) + ".db"
		_ = os.WriteFile(filepath.Join(dir, name), nil, 0o644)
	}
	n, err := Prune(dir, 30, now)
	if err != nil || n != 3 {
		t.Fatalf("pruned %d, %v; want 3", n, err)
	}
	// Old snapshots survive when they are among the newest minKeep.
	dir2 := t.TempDir()
	for _, d := range []int{100, 200} {
		_ = os.WriteFile(filepath.Join(dir2, snapshotPrefix+now.AddDate(0, 0, -d).Format(snapshotLayout)+".db"), nil, 0o644)
	}
	if n, _ := Prune(dir2, 30, now); n != 0 {
		t.Fatalf("pruned %d of the only snapshots", n)
	}
}
