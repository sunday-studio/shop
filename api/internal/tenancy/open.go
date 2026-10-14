package tenancy

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"shop/internal/backup"
	"shop/internal/store"
)

// OpenDB opens a household database file and brings its schema up to date. As a safety net for
// upgrades it snapshots an existing database into snapshotDir before its schema changes.
func OpenDB(ctx context.Context, path, snapshotDir string, log *slog.Logger) (*sql.DB, error) {
	conn, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	if pending, v, err := store.PendingMigrations(ctx, conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("check migrations: %w", err)
	} else if pending && v > 0 {
		snap := filepath.Join(snapshotDir, fmt.Sprintf("pre-migrate-v%d-%s.db", v, time.Now().UTC().Format("20060102-150405")))
		if err := backup.Snapshot(ctx, conn, snap); err != nil {
			conn.Close()
			return nil, fmt.Errorf("pre-migration snapshot: %w", err)
		}
		if log != nil {
			log.Info("pre-migration snapshot", "path", snap, "from_version", v)
		}
	}
	if err := store.Migrate(ctx, conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return conn, nil
}
