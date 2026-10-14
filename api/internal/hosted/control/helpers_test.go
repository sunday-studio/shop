package control_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// openRaw opens a second raw handle on the same file to prove the triggers hold.
func openRaw(t *testing.T, e *env) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
