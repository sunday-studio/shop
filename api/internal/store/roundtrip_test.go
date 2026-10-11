package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"shop/internal/store"
)

// Every migration must apply, roll back and apply again on an empty database.
func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := store.MigrateDownTo(ctx, conn, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	var tables int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'goose_db_version'`).Scan(&tables)
	if tables != 0 {
		t.Errorf("%d tables left after rolling everything back", tables)
	}
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if v, _ := store.MigrationVersion(ctx, conn); v < 14 {
		t.Errorf("version after the second up = %d", v)
	}
}
