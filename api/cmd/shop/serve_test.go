package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shop/internal/store"
)

func TestOpenAppSnapshotsBeforeMigrating(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("SHOP_DATA_DIR", dir)

	// An install on an older schema.
	conn, err := store.Open(filepath.Join(dir, "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateTo(ctx, conn, 2); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	a, err := openApp(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()

	entries, _ := os.ReadDir(dir)
	var snaps []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pre-migrate-v2-") {
			snaps = append(snaps, e.Name())
		}
	}
	if len(snaps) != 1 {
		t.Fatalf("pre-migration snapshots = %v", snaps)
	}
	if pending, _, _ := store.PendingMigrations(ctx, a.db); pending {
		t.Error("migrations still pending after openApp")
	}

	// Opening again with nothing pending must not snapshot.
	a.db.Close()
	b, err := openApp(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	b.db.Close()
	entries, _ = os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pre-migrate-") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("snapshots after second open = %d, want 1", n)
	}
}
