package store

import (
	"context"
	"database/sql"

	"pantry/internal/store/db"
)

// InTx runs fn inside a transaction, committing on nil error.
func InTx(ctx context.Context, conn *sql.DB, fn func(q *db.Queries) error) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
