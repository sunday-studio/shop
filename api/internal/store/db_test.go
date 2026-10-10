package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"shop/internal/store"
	"shop/internal/store/db"
)

func TestMigrateFreshDB(t *testing.T) {
	ctx := context.Background()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Idempotent.
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	v, err := store.MigrationVersion(ctx, conn)
	if err != nil || v < 2 {
		t.Fatalf("schema version = %d, %v; want >= 2", v, err)
	}

	var mode string
	if err := conn.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v; want wal", mode, err)
	}
	var fk int
	if err := conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v; want 1", fk, err)
	}

	var cats int
	if err := conn.QueryRow("SELECT COUNT(*) FROM categories").Scan(&cats); err != nil || cats != 10 {
		t.Errorf("seeded categories = %d, %v; want 10", cats, err)
	}
}

// The trigram tokenizer is what fuzzy search relies on; make sure the
// bundled SQLite supports it and substring matches work.
func TestTrigramSearch(t *testing.T) {
	ctx := context.Background()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}

	for _, txt := range []string{"ah halfvolle melk 1l", "jumbo volle yoghurt", "haverdrank barista"} {
		if _, err := conn.Exec(`INSERT INTO search_fts(kind, ref_id, text) VALUES ('product', 1, ?)`, txt); err != nil {
			t.Fatal(err)
		}
	}
	var got string
	err = conn.QueryRow(`SELECT text FROM search_fts WHERE search_fts MATCH 'halfv' ORDER BY rank LIMIT 1`).Scan(&got)
	if err != nil || got != "ah halfvolle melk 1l" {
		t.Fatalf("trigram match = %q, %v", got, err)
	}
}

func TestShopsCRUD(t *testing.T) {
	ctx := context.Background()
	conn, err := store.Open(filepath.Join(t.TempDir(), "shop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := store.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)

	now := "2026-10-10T10:00:00Z"
	shop, err := q.CreateShop(ctx, db.CreateShopParams{Name: "Lidl", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if shop.ParserProfile != "generic" {
		t.Errorf("parser_profile = %q; want generic", shop.ParserProfile)
	}

	n, err := q.SoftDeleteShop(ctx, db.SoftDeleteShopParams{DeletedAt: sql.NullString{String: now, Valid: true}, UpdatedAt: now, ID: shop.ID})
	if err != nil || n != 1 {
		t.Fatalf("soft delete rows = %d, %v", n, err)
	}
	shops, err := q.ListShops(ctx)
	if err != nil || len(shops) != 0 {
		t.Fatalf("list after delete = %d, %v; want 0", len(shops), err)
	}
}
