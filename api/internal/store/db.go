// Package store owns the SQLite connection, migrations and generated queries.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"shop/internal/migrations"
)

// Open opens (creating if needed) the SQLite database at path with the
// pragmas every connection needs. Pragmas go in the DSN so they apply to
// each pooled connection, not just the first.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, db *sql.DB) error {
	p, err := newProvider(db)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

// MigrationVersion reports the current schema version.
func MigrationVersion(ctx context.Context, db *sql.DB) (int64, error) {
	p, err := newProvider(db)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

func newProvider(db *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectSQLite3, db, migrations.FS)
}
